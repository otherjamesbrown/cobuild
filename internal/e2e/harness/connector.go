package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
)

type FakeConnectorOptions struct {
	Name     string
	IDPrefix string
	Now      func() time.Time
}

// FakeConnector is an in-memory connector implementation used by e2e tests.
// It keeps full item, edge, status, and metadata state so dispatch/orchestrate
// flows can exercise the same connector contract as production code.
type FakeConnector struct {
	mu       sync.RWMutex
	name     string
	idPrefix string
	now      func() time.Time
	nextID   int
	items    map[string]*connector.WorkItem
	incoming map[string][]connector.Edge
	outgoing map[string][]connector.Edge
}

func NewFakeConnector(opts FakeConnectorOptions) *FakeConnector {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "fake"
	}
	idPrefix := strings.TrimSpace(opts.IDPrefix)
	if idPrefix == "" {
		idPrefix = "fake"
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &FakeConnector{
		name:     name,
		idPrefix: idPrefix,
		now:      now,
		items:    map[string]*connector.WorkItem{},
		incoming: map[string][]connector.Edge{},
		outgoing: map[string][]connector.Edge{},
	}
}

func (c *FakeConnector) Name() string { return c.name }

func (c *FakeConnector) Get(_ context.Context, id string) (*connector.WorkItem, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item := c.items[id]
	if item == nil {
		return nil, fmt.Errorf("fake connector: work item %q not found", id)
	}
	out := cloneWorkItem(*item)
	out.Edges = append(out.Edges, cloneEdges(c.incoming[id])...)
	out.Edges = append(out.Edges, cloneEdges(c.outgoing[id])...)
	return &out, nil
}

func (c *FakeConnector) List(_ context.Context, filters connector.ListFilters) (*connector.ListResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	items := make([]connector.WorkItem, 0, len(c.items))
	for _, item := range c.items {
		if filters.Type != "" && item.Type != filters.Type {
			continue
		}
		if filters.Status != "" && item.Status != filters.Status {
			continue
		}
		if filters.Project != "" && item.Project != filters.Project {
			continue
		}
		items = append(items, cloneWorkItem(*item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	total := len(items)
	if filters.Limit > 0 && len(items) > filters.Limit {
		items = items[:filters.Limit]
	}
	return &connector.ListResult{Items: items, Total: total}, nil
}

func (c *FakeConnector) GetEdges(_ context.Context, id string, direction string, types []string) ([]connector.Edge, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	switch strings.TrimSpace(direction) {
	case "":
		edges := append(cloneEdges(c.incoming[id]), cloneEdges(c.outgoing[id])...)
		return filterEdges(edges, "", types), nil
	case "incoming":
		return filterEdges(cloneEdges(c.incoming[id]), "incoming", types), nil
	case "outgoing":
		return filterEdges(cloneEdges(c.outgoing[id]), "outgoing", types), nil
	default:
		return nil, fmt.Errorf("fake connector: unsupported direction %q", direction)
	}
}

func (c *FakeConnector) GetMetadata(_ context.Context, id string, key string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item := c.items[id]
	if item == nil {
		return "", fmt.Errorf("fake connector: work item %q not found", id)
	}
	value, ok := item.Metadata[key]
	if !ok || value == nil {
		return "", nil
	}
	return metadataString(value), nil
}

func (c *FakeConnector) Create(_ context.Context, req connector.CreateRequest) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := strings.TrimSpace(metadataString(req.Metadata["_id"]))
	if id == "" {
		c.nextID++
		id = fmt.Sprintf("%s-%04d", c.idPrefix, c.nextID)
	} else if c.items[id] != nil {
		return "", fmt.Errorf("fake connector: work item %q already exists", id)
	}
	now := c.now().UTC()
	project := ""
	if strings.TrimSpace(req.ParentID) != "" {
		parent := c.items[req.ParentID]
		if parent == nil {
			return "", fmt.Errorf("fake connector: to item %q not found", req.ParentID)
		}
		project = parent.Project
	}
	item := &connector.WorkItem{
		ID:        id,
		Title:     req.Title,
		Content:   req.Content,
		Type:      req.Type,
		Status:    "open",
		Project:   project,
		Labels:    append([]string(nil), req.Labels...),
		Metadata:  cloneMetadata(req.Metadata),
		CreatedAt: now,
		UpdatedAt: now,
	}
	delete(item.Metadata, "_id")
	c.items[id] = item
	if strings.TrimSpace(req.ParentID) != "" {
		if err := c.createEdgeLocked(id, req.ParentID, "child-of"); err != nil {
			return "", err
		}
	}
	return id, nil
}

func (c *FakeConnector) UpdateStatus(_ context.Context, id string, status string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item := c.items[id]
	if item == nil {
		return fmt.Errorf("fake connector: work item %q not found", id)
	}
	item.Status = status
	item.UpdatedAt = c.now().UTC()
	c.refreshEdgesLocked(id)
	return nil
}

func (c *FakeConnector) AppendContent(_ context.Context, id string, content string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item := c.items[id]
	if item == nil {
		return fmt.Errorf("fake connector: work item %q not found", id)
	}
	if item.Content == "" {
		item.Content = content
	} else {
		item.Content += content
	}
	item.UpdatedAt = c.now().UTC()
	return nil
}

func (c *FakeConnector) SetMetadata(_ context.Context, id string, key string, value any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item := c.items[id]
	if item == nil {
		return fmt.Errorf("fake connector: work item %q not found", id)
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	item.Metadata[key] = value
	item.UpdatedAt = c.now().UTC()
	return nil
}

func (c *FakeConnector) UpdateMetadataMap(ctx context.Context, id string, patch map[string]any) error {
	for key, value := range patch {
		if err := c.SetMetadata(ctx, id, key, value); err != nil {
			return err
		}
	}
	return nil
}

func (c *FakeConnector) AddLabel(_ context.Context, id string, label string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item := c.items[id]
	if item == nil {
		return fmt.Errorf("fake connector: work item %q not found", id)
	}
	for _, existing := range item.Labels {
		if existing == label {
			return nil
		}
	}
	item.Labels = append(item.Labels, label)
	item.UpdatedAt = c.now().UTC()
	return nil
}

func (c *FakeConnector) CreateEdge(_ context.Context, fromID string, toID string, edgeType string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.createEdgeLocked(fromID, toID, edgeType)
}

func (c *FakeConnector) AddItem(item connector.WorkItem) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if item.CreatedAt.IsZero() {
		item.CreatedAt = c.now().UTC()
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	item.Metadata = cloneMetadata(item.Metadata)
	item.Labels = append([]string(nil), item.Labels...)
	item.Edges = nil
	clone := item
	c.items[item.ID] = &clone
}

func (c *FakeConnector) createEdgeLocked(fromID string, toID string, edgeType string) error {
	from := c.items[fromID]
	if from == nil {
		return fmt.Errorf("fake connector: from item %q not found", fromID)
	}
	to := c.items[toID]
	if to == nil {
		return fmt.Errorf("fake connector: to item %q not found", toID)
	}

	outgoing := connector.Edge{
		Direction: "outgoing",
		EdgeType:  edgeType,
		ItemID:    toID,
		Title:     to.Title,
		Type:      to.Type,
		Status:    to.Status,
	}
	incoming := connector.Edge{
		Direction: "incoming",
		EdgeType:  edgeType,
		ItemID:    fromID,
		Title:     from.Title,
		Type:      from.Type,
		Status:    from.Status,
	}
	c.outgoing[fromID] = upsertEdge(c.outgoing[fromID], outgoing)
	c.incoming[toID] = upsertEdge(c.incoming[toID], incoming)
	return nil
}

func (c *FakeConnector) refreshEdgesLocked(changedID string) {
	item := c.items[changedID]
	if item == nil {
		return
	}
	for i := range c.outgoing[changedID] {
		peer := c.items[c.outgoing[changedID][i].ItemID]
		if peer == nil {
			continue
		}
		c.outgoing[changedID][i].Title = peer.Title
		c.outgoing[changedID][i].Type = peer.Type
		c.outgoing[changedID][i].Status = peer.Status
	}
	for i := range c.incoming[changedID] {
		peer := c.items[c.incoming[changedID][i].ItemID]
		if peer == nil {
			continue
		}
		c.incoming[changedID][i].Title = peer.Title
		c.incoming[changedID][i].Type = peer.Type
		c.incoming[changedID][i].Status = peer.Status
	}
	for owner := range c.outgoing {
		for i := range c.outgoing[owner] {
			if c.outgoing[owner][i].ItemID != changedID {
				continue
			}
			c.outgoing[owner][i].Title = item.Title
			c.outgoing[owner][i].Type = item.Type
			c.outgoing[owner][i].Status = item.Status
		}
	}
	for owner := range c.incoming {
		for i := range c.incoming[owner] {
			if c.incoming[owner][i].ItemID != changedID {
				continue
			}
			c.incoming[owner][i].Title = item.Title
			c.incoming[owner][i].Type = item.Type
			c.incoming[owner][i].Status = item.Status
		}
	}
}

func upsertEdge(edges []connector.Edge, edge connector.Edge) []connector.Edge {
	for i := range edges {
		if edges[i].ItemID == edge.ItemID && edges[i].EdgeType == edge.EdgeType && edges[i].Direction == edge.Direction {
			edges[i] = edge
			return edges
		}
	}
	return append(edges, edge)
}

func filterEdges(edges []connector.Edge, direction string, types []string) []connector.Edge {
	if len(edges) == 0 {
		return nil
	}
	filtered := make([]connector.Edge, 0, len(edges))
	for _, edge := range edges {
		if direction != "" && edge.Direction != direction {
			continue
		}
		if len(types) > 0 {
			matched := false
			for _, edgeType := range types {
				if edge.EdgeType == edgeType {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		filtered = append(filtered, edge)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].ItemID == filtered[j].ItemID {
			return filtered[i].EdgeType < filtered[j].EdgeType
		}
		return filtered[i].ItemID < filtered[j].ItemID
	})
	return filtered
}

func cloneWorkItem(item connector.WorkItem) connector.WorkItem {
	item.Labels = append([]string(nil), item.Labels...)
	item.Metadata = cloneMetadata(item.Metadata)
	item.Edges = cloneEdges(item.Edges)
	item.Raw = append(json.RawMessage(nil), item.Raw...)
	return item
}

func cloneEdges(edges []connector.Edge) []connector.Edge {
	if len(edges) == 0 {
		return nil
	}
	out := make([]connector.Edge, len(edges))
	copy(out, edges)
	return out
}

func cloneMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func metadataString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

var _ connector.Connector = (*FakeConnector)(nil)
