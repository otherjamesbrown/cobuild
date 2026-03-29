package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// BeadsConnector connects to Beads via the bd CLI.
type BeadsConnector struct {
	Prefix string // ID prefix (e.g. "cb")
	Repo   string // repo path (for --repo flag), empty = current dir
	Debug  bool
}

// NewBeadsConnector creates a connector that shells out to the bd binary.
func NewBeadsConnector(prefix, repo string, debug bool) *BeadsConnector {
	return &BeadsConnector{Prefix: prefix, Repo: repo, Debug: debug}
}

func (c *BeadsConnector) Name() string { return "beads" }

// --- Read ---

func (c *BeadsConnector) Get(ctx context.Context, id string) (*WorkItem, error) {
	out, err := c.run(ctx, "show", id, "--json")
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", id, err)
	}
	// bd show --json returns an array; unwrap to single item
	trimmed := strings.TrimSpace(string(out))
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(out, &items); err != nil {
			return nil, fmt.Errorf("parse beads show array: %w", err)
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("get %s: no results", id)
		}
		out = items[0]
	}
	return c.parseWorkItem(out)
}

func (c *BeadsConnector) List(ctx context.Context, filters ListFilters) (*ListResult, error) {
	args := []string{"list", "--json"}
	if filters.Type != "" {
		args = append(args, "--type", mapTypeToBead(filters.Type))
	}
	if filters.Status != "" {
		args = append(args, "--status", filters.Status)
	}
	if filters.Limit > 0 {
		args = append(args, "--limit", fmt.Sprintf("%d", filters.Limit))
	}

	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}

	var items []json.RawMessage
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("parse list: %w", err)
	}

	result := &ListResult{Total: len(items)}
	for _, raw := range items {
		item, err := c.parseWorkItem(raw)
		if err != nil {
			continue
		}
		result.Items = append(result.Items, *item)
	}
	return result, nil
}

func (c *BeadsConnector) GetEdges(ctx context.Context, id string, direction string, types []string) ([]Edge, error) {
	// Beads embeds dependencies in the show output
	item, err := c.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	var filtered []Edge
	for _, e := range item.Edges {
		if direction != "" && e.Direction != direction {
			continue
		}
		if len(types) > 0 {
			match := false
			for _, t := range types {
				if e.EdgeType == t {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		filtered = append(filtered, e)
	}
	return filtered, nil
}

func (c *BeadsConnector) GetMetadata(ctx context.Context, id string, key string) (string, error) {
	item, err := c.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if item.Metadata == nil {
		return "", nil
	}
	val, ok := item.Metadata[key]
	if !ok {
		return "", nil
	}
	switch v := val.(type) {
	case string:
		return v, nil
	default:
		b, _ := json.Marshal(v)
		return string(b), nil
	}
}

// --- Write ---

func (c *BeadsConnector) Create(ctx context.Context, req CreateRequest) (string, error) {
	args := []string{"create", req.Title, "--type", mapTypeToBead(req.Type), "--json"}
	if req.Content != "" {
		args = append(args, "--description", req.Content)
	}
	if req.ParentID != "" {
		args = append(args, "--parent", req.ParentID)
	}
	if len(req.Labels) > 0 {
		args = append(args, "--labels", strings.Join(req.Labels, ","))
	}
	if len(req.Metadata) > 0 {
		metaJSON, err := json.Marshal(req.Metadata)
		if err == nil {
			args = append(args, "--metadata", string(metaJSON))
		}
	}

	out, err := c.run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("create: %w", err)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("parse create response: %w", err)
	}
	return result.ID, nil
}

func (c *BeadsConnector) UpdateStatus(ctx context.Context, id string, status string) error {
	if status == "closed" {
		_, err := c.run(ctx, "close", id)
		return err
	}
	if status == "open" {
		_, err := c.run(ctx, "reopen", id)
		return err
	}
	_, err := c.run(ctx, "update", id, "--status", status)
	if err != nil {
		return fmt.Errorf("update status %s → %s: %w", id, status, err)
	}
	return nil
}

func (c *BeadsConnector) AppendContent(ctx context.Context, id string, content string) error {
	_, err := c.run(ctx, "update", id, "--append-notes", content)
	if err != nil {
		return fmt.Errorf("append %s: %w", id, err)
	}
	return nil
}

func (c *BeadsConnector) SetMetadata(ctx context.Context, id string, key string, value any) error {
	valStr, err := marshalValue(value)
	if err != nil {
		return err
	}
	_, err = c.run(ctx, "update", id, "--set-metadata", fmt.Sprintf("%s=%s", key, valStr))
	if err != nil {
		return fmt.Errorf("set metadata %s.%s: %w", id, key, err)
	}
	return nil
}

func (c *BeadsConnector) UpdateMetadataMap(ctx context.Context, id string, patch map[string]any) error {
	for k, v := range patch {
		if err := c.SetMetadata(ctx, id, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (c *BeadsConnector) AddLabel(ctx context.Context, id string, label string) error {
	_, err := c.run(ctx, "label", "add", id, label)
	if err != nil {
		return fmt.Errorf("add label %s %s: %w", id, label, err)
	}
	return nil
}

func (c *BeadsConnector) CreateEdge(ctx context.Context, fromID string, toID string, edgeType string) error {
	switch edgeType {
	case "blocked-by":
		_, err := c.run(ctx, "dep", "add", fromID, "--blocked-by", toID)
		return err
	case "relates-to":
		_, err := c.run(ctx, "relate", fromID, toID)
		return err
	case "child-of":
		// Beads uses --parent on the child
		_, err := c.run(ctx, "update", fromID, "--parent", toID)
		return err
	default:
		_, err := c.run(ctx, "dep", "add", fromID, toID)
		return err
	}
}

// --- Helpers ---

func (c *BeadsConnector) run(ctx context.Context, args ...string) (json.RawMessage, error) {
	if c.Repo != "" {
		args = append([]string{"--repo", c.Repo}, args...)
	}

	cmd := exec.CommandContext(ctx, "bd", args...)
	if c.Debug {
		fmt.Printf("[connector:beads] bd %s\n", strings.Join(args, " "))
	}

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			return nil, fmt.Errorf("%s (stderr: %s)", err, stderr)
		}
		return nil, err
	}
	return json.RawMessage(out), nil
}

func (c *BeadsConnector) parseWorkItem(data json.RawMessage) (*WorkItem, error) {
	var raw struct {
		ID          string          `json:"id"`
		Title       string          `json:"title"`
		Description string          `json:"description"`
		Notes       string          `json:"notes"`
		IssueType   string          `json:"issue_type"`
		Status      string          `json:"status"`
		Priority    int             `json:"priority"`
		Assignee    string          `json:"assignee"`
		Labels      []string        `json:"labels"`
		Metadata    json.RawMessage `json:"metadata"`
		ParentID    string          `json:"parent_id"`
		CreatedAt   time.Time       `json:"created_at"`
		UpdatedAt   time.Time       `json:"updated_at"`
		// Dependencies are returned inline by bd show
		Dependencies []struct {
			ID     string `json:"id"`
			FromID string `json:"from_id"`
			ToID   string `json:"to_id"`
			Type   string `json:"type"` // depends-on, blocks, relates-to
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse beads item: %w", err)
	}

	// Beads stores content across description + notes; combine for CoBuild
	content := raw.Description
	if raw.Notes != "" {
		if content != "" {
			content += "\n\n"
		}
		content += raw.Notes
	}

	item := &WorkItem{
		ID:        raw.ID,
		Title:     raw.Title,
		Content:   content,
		Type:      mapTypeFromBead(raw.IssueType),
		Status:    raw.Status,
		Creator:   raw.Assignee,
		Labels:    raw.Labels,
		CreatedAt: raw.CreatedAt,
		UpdatedAt: raw.UpdatedAt,
		Raw:       data,
	}

	if len(raw.Metadata) > 0 && string(raw.Metadata) != "null" {
		var meta map[string]any
		if json.Unmarshal(raw.Metadata, &meta) == nil {
			item.Metadata = meta
		}
	}

	// Convert dependencies to edges
	for _, dep := range raw.Dependencies {
		edge := Edge{EdgeType: mapEdgeTypeFromBead(dep.Type)}
		if dep.FromID == raw.ID {
			edge.Direction = "outgoing"
			edge.ItemID = dep.ToID
		} else {
			edge.Direction = "incoming"
			edge.ItemID = dep.FromID
		}
		item.Edges = append(item.Edges, edge)
	}

	// Parent as an edge
	if raw.ParentID != "" {
		item.Edges = append(item.Edges, Edge{
			Direction: "outgoing",
			EdgeType:  "child-of",
			ItemID:    raw.ParentID,
		})
	}

	return item, nil
}

// mapTypeToBead maps CoBuild work item types to Beads issue types.
func mapTypeToBead(t string) string {
	switch t {
	case "design":
		return "feature"
	case "bug":
		return "bug"
	case "task":
		return "task"
	case "review":
		return "decision"
	case "outcome":
		return "epic"
	default:
		return t
	}
}

// mapTypeFromBead maps Beads issue types to CoBuild work item types.
func mapTypeFromBead(t string) string {
	switch t {
	case "feature":
		return "design"
	case "bug":
		return "bug"
	case "task":
		return "task"
	case "decision":
		return "review"
	case "epic":
		return "outcome"
	case "chore":
		return "task"
	default:
		return t
	}
}

// mapEdgeTypeFromBead maps Beads dependency types to CoBuild edge types.
func mapEdgeTypeFromBead(t string) string {
	switch t {
	case "depends-on":
		return "blocked-by"
	case "blocks":
		return "blocks"
	case "relates-to", "related-to":
		return "relates-to"
	case "duplicates":
		return "relates-to"
	case "supersedes":
		return "previous-version"
	default:
		return t
	}
}
