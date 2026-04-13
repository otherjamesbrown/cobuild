//go:build e2e

package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type fakeFileConnector struct {
	path     string
	name     string
	idPrefix string
}

type fakeConnectorState struct {
	NextID   int                 `json:"next_id"`
	Items    map[string]WorkItem `json:"items"`
	Incoming map[string][]Edge   `json:"incoming"`
	Outgoing map[string][]Edge   `json:"outgoing"`
}

func newFakeConnectorFromConfig(cfg map[string]string) (Connector, error) {
	path := strings.TrimSpace(cfg["state_file"])
	if path == "" {
		return nil, fmt.Errorf("fake connector requires connectors.work_items.config.state_file")
	}
	name := strings.TrimSpace(cfg["name"])
	if name == "" {
		name = "fake"
	}
	idPrefix := strings.TrimSpace(cfg["id_prefix"])
	if idPrefix == "" {
		idPrefix = "fake"
	}

	fc := &fakeFileConnector{path: path, name: name, idPrefix: idPrefix}
	if err := fc.init(); err != nil {
		return nil, err
	}
	return fc, nil
}

func (c *fakeFileConnector) Name() string { return c.name }

func (c *fakeFileConnector) Get(_ context.Context, id string) (*WorkItem, error) {
	var out *WorkItem
	err := c.withState(false, func(state *fakeConnectorState) error {
		item, ok := state.Items[id]
		if !ok {
			return fmt.Errorf("fake connector: work item %q not found", id)
		}
		cloned := cloneFakeWorkItem(item)
		cloned.Edges = append(cloned.Edges, cloneFakeEdges(state.Incoming[id])...)
		cloned.Edges = append(cloned.Edges, cloneFakeEdges(state.Outgoing[id])...)
		out = &cloned
		return nil
	})
	return out, err
}

func (c *fakeFileConnector) List(_ context.Context, filters ListFilters) (*ListResult, error) {
	result := &ListResult{}
	err := c.withState(false, func(state *fakeConnectorState) error {
		items := make([]WorkItem, 0, len(state.Items))
		for _, item := range state.Items {
			if filters.Type != "" && item.Type != filters.Type {
				continue
			}
			if filters.Status != "" && item.Status != filters.Status {
				continue
			}
			if filters.Project != "" && item.Project != filters.Project {
				continue
			}
			items = append(items, cloneFakeWorkItem(item))
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].CreatedAt.Equal(items[j].CreatedAt) {
				return items[i].ID < items[j].ID
			}
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		})
		result.Total = len(items)
		if filters.Limit > 0 && len(items) > filters.Limit {
			items = items[:filters.Limit]
		}
		result.Items = items
		return nil
	})
	return result, err
}

func (c *fakeFileConnector) GetEdges(_ context.Context, id string, direction string, types []string) ([]Edge, error) {
	var edges []Edge
	err := c.withState(false, func(state *fakeConnectorState) error {
		switch strings.TrimSpace(direction) {
		case "":
			edges = append(cloneFakeEdges(state.Incoming[id]), cloneFakeEdges(state.Outgoing[id])...)
		case "incoming":
			edges = cloneFakeEdges(state.Incoming[id])
		case "outgoing":
			edges = cloneFakeEdges(state.Outgoing[id])
		default:
			return fmt.Errorf("fake connector: unsupported direction %q", direction)
		}
		edges = filterFakeEdges(edges, direction, types)
		return nil
	})
	return edges, err
}

func (c *fakeFileConnector) GetMetadata(_ context.Context, id string, key string) (string, error) {
	value := ""
	err := c.withState(false, func(state *fakeConnectorState) error {
		item, ok := state.Items[id]
		if !ok {
			return fmt.Errorf("fake connector: work item %q not found", id)
		}
		raw, ok := item.Metadata[key]
		if !ok || raw == nil {
			return nil
		}
		value = fakeMetadataString(raw)
		return nil
	})
	return value, err
}

func (c *fakeFileConnector) Create(_ context.Context, req CreateRequest) (string, error) {
	var id string
	err := c.withState(true, func(state *fakeConnectorState) error {
		if state.Items == nil {
			state.Items = map[string]WorkItem{}
		}
		if state.Incoming == nil {
			state.Incoming = map[string][]Edge{}
		}
		if state.Outgoing == nil {
			state.Outgoing = map[string][]Edge{}
		}

		id = explicitFakeID(req.Metadata)
		if id == "" {
			state.NextID++
			id = fmt.Sprintf("%s-%04d", c.idPrefix, state.NextID)
		} else if _, exists := state.Items[id]; exists {
			return fmt.Errorf("fake connector: work item %q already exists", id)
		}

		now := time.Now().UTC()
		project := strings.TrimSpace(fakeMetadataString(req.Metadata["project"]))
		if project == "" && strings.TrimSpace(req.ParentID) != "" {
			parent, ok := state.Items[req.ParentID]
			if !ok {
				return fmt.Errorf("fake connector: to item %q not found", req.ParentID)
			}
			project = parent.Project
		}
		item := WorkItem{
			ID:        id,
			Title:     req.Title,
			Content:   req.Content,
			Type:      req.Type,
			Status:    "open",
			Project:   project,
			Labels:    append([]string(nil), req.Labels...),
			Metadata:  cloneFakeMetadata(req.Metadata),
			CreatedAt: now,
			UpdatedAt: now,
		}
		delete(item.Metadata, "_id")
		state.Items[id] = item
		if strings.TrimSpace(req.ParentID) != "" {
			if err := fakeCreateEdge(state, id, req.ParentID, "child-of"); err != nil {
				return err
			}
		}
		return nil
	})
	return id, err
}

func (c *fakeFileConnector) UpdateStatus(_ context.Context, id string, status string) error {
	return c.withState(true, func(state *fakeConnectorState) error {
		item, ok := state.Items[id]
		if !ok {
			return fmt.Errorf("fake connector: work item %q not found", id)
		}
		item.Status = status
		item.UpdatedAt = time.Now().UTC()
		state.Items[id] = item
		fakeRefreshEdges(state, id)
		return nil
	})
}

func (c *fakeFileConnector) AppendContent(_ context.Context, id string, content string) error {
	return c.withState(true, func(state *fakeConnectorState) error {
		item, ok := state.Items[id]
		if !ok {
			return fmt.Errorf("fake connector: work item %q not found", id)
		}
		if item.Content == "" {
			item.Content = content
		} else {
			item.Content += content
		}
		item.UpdatedAt = time.Now().UTC()
		state.Items[id] = item
		return nil
	})
}

func (c *fakeFileConnector) SetMetadata(_ context.Context, id string, key string, value any) error {
	return c.withState(true, func(state *fakeConnectorState) error {
		item, ok := state.Items[id]
		if !ok {
			return fmt.Errorf("fake connector: work item %q not found", id)
		}
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		item.Metadata[key] = value
		item.UpdatedAt = time.Now().UTC()
		state.Items[id] = item
		return nil
	})
}

func (c *fakeFileConnector) UpdateMetadataMap(ctx context.Context, id string, patch map[string]any) error {
	for key, value := range patch {
		if err := c.SetMetadata(ctx, id, key, value); err != nil {
			return err
		}
	}
	return nil
}

func (c *fakeFileConnector) AddLabel(_ context.Context, id string, label string) error {
	return c.withState(true, func(state *fakeConnectorState) error {
		item, ok := state.Items[id]
		if !ok {
			return fmt.Errorf("fake connector: work item %q not found", id)
		}
		for _, existing := range item.Labels {
			if existing == label {
				return nil
			}
		}
		item.Labels = append(item.Labels, label)
		item.UpdatedAt = time.Now().UTC()
		state.Items[id] = item
		return nil
	})
}

func (c *fakeFileConnector) CreateEdge(_ context.Context, fromID string, toID string, edgeType string) error {
	return c.withState(true, func(state *fakeConnectorState) error {
		return fakeCreateEdge(state, fromID, toID, edgeType)
	})
}

func (c *fakeFileConnector) init() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return fmt.Errorf("fake connector mkdir: %w", err)
	}
	if _, err := os.Stat(c.path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("fake connector stat %s: %w", c.path, err)
	}
	state := &fakeConnectorState{
		Items:    map[string]WorkItem{},
		Incoming: map[string][]Edge{},
		Outgoing: map[string][]Edge{},
	}
	return c.writeState(state)
}

func (c *fakeFileConnector) withState(write bool, fn func(state *fakeConnectorState) error) error {
	lockPath := c.path + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("fake connector lock: %w", err)
	}
	defer lockFile.Close()

	lockType := syscall.LOCK_SH
	if write {
		lockType = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(lockFile.Fd()), lockType); err != nil {
		return fmt.Errorf("fake connector flock: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	state, err := c.readState()
	if err != nil {
		return err
	}
	if err := fn(state); err != nil {
		return err
	}
	if !write {
		return nil
	}
	return c.writeState(state)
}

func (c *fakeFileConnector) readState() (*fakeConnectorState, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, fmt.Errorf("fake connector read %s: %w", c.path, err)
	}
	state := &fakeConnectorState{}
	if len(strings.TrimSpace(string(data))) == 0 {
		state.Items = map[string]WorkItem{}
		state.Incoming = map[string][]Edge{}
		state.Outgoing = map[string][]Edge{}
		return state, nil
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("fake connector parse %s: %w", c.path, err)
	}
	if state.Items == nil {
		state.Items = map[string]WorkItem{}
	}
	if state.Incoming == nil {
		state.Incoming = map[string][]Edge{}
	}
	if state.Outgoing == nil {
		state.Outgoing = map[string][]Edge{}
	}
	return state, nil
}

func (c *fakeFileConnector) writeState(state *fakeConnectorState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("fake connector marshal: %w", err)
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("fake connector write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return fmt.Errorf("fake connector rename %s: %w", c.path, err)
	}
	return nil
}

func explicitFakeID(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	raw, ok := metadata["_id"]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fakeMetadataString(raw))
}

func fakeCreateEdge(state *fakeConnectorState, fromID string, toID string, edgeType string) error {
	from, ok := state.Items[fromID]
	if !ok {
		return fmt.Errorf("fake connector: from item %q not found", fromID)
	}
	to, ok := state.Items[toID]
	if !ok {
		return fmt.Errorf("fake connector: to item %q not found", toID)
	}
	outgoing := Edge{Direction: "outgoing", EdgeType: edgeType, ItemID: toID, Title: to.Title, Type: to.Type, Status: to.Status}
	incoming := Edge{Direction: "incoming", EdgeType: edgeType, ItemID: fromID, Title: from.Title, Type: from.Type, Status: from.Status}
	state.Outgoing[fromID] = fakeUpsertEdge(state.Outgoing[fromID], outgoing)
	state.Incoming[toID] = fakeUpsertEdge(state.Incoming[toID], incoming)
	return nil
}

func fakeRefreshEdges(state *fakeConnectorState, changedID string) {
	item, ok := state.Items[changedID]
	if !ok {
		return
	}
	for owner, edges := range state.Outgoing {
		for i := range edges {
			if edges[i].ItemID != changedID {
				continue
			}
			edges[i].Title = item.Title
			edges[i].Type = item.Type
			edges[i].Status = item.Status
		}
		state.Outgoing[owner] = edges
	}
	for owner, edges := range state.Incoming {
		for i := range edges {
			if edges[i].ItemID != changedID {
				continue
			}
			edges[i].Title = item.Title
			edges[i].Type = item.Type
			edges[i].Status = item.Status
		}
		state.Incoming[owner] = edges
	}
}

func fakeUpsertEdge(edges []Edge, edge Edge) []Edge {
	for i := range edges {
		if edges[i].ItemID == edge.ItemID && edges[i].EdgeType == edge.EdgeType && edges[i].Direction == edge.Direction {
			edges[i] = edge
			return edges
		}
	}
	return append(edges, edge)
}

func filterFakeEdges(edges []Edge, direction string, types []string) []Edge {
	if len(edges) == 0 {
		return nil
	}
	filtered := make([]Edge, 0, len(edges))
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

func cloneFakeWorkItem(item WorkItem) WorkItem {
	item.Labels = append([]string(nil), item.Labels...)
	item.Metadata = cloneFakeMetadata(item.Metadata)
	item.Edges = cloneFakeEdges(item.Edges)
	item.Raw = append(json.RawMessage(nil), item.Raw...)
	return item
}

func cloneFakeEdges(edges []Edge) []Edge {
	if len(edges) == 0 {
		return nil
	}
	out := make([]Edge, len(edges))
	copy(out, edges)
	return out
}

func cloneFakeMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func fakeMetadataString(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

var _ Connector = (*fakeFileConnector)(nil)
