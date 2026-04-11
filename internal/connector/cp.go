package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CPConnector connects to Context Palace via the cxp CLI.
type CPConnector struct {
	Project string // project filter for queries
	Agent   string // agent identity
	Debug   bool   // log commands when true
}

// NewCPConnector creates a connector that shells out to the cxp binary.
func NewCPConnector(project, agent string, debug bool) *CPConnector {
	return &CPConnector{Project: project, Agent: agent, Debug: debug}
}

func (c *CPConnector) Name() string { return "context-palace" }

// --- Read ---

func (c *CPConnector) Get(ctx context.Context, id string) (*WorkItem, error) {
	out, err := c.runBare(ctx, "shard", "show", id, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", id, err)
	}
	return c.parseWorkItem(out)
}

func (c *CPConnector) List(ctx context.Context, filters ListFilters) (*ListResult, error) {
	args := []string{"shard", "list", "-o", "json"}
	if filters.Type != "" {
		args = append(args, "--type", filters.Type)
	}
	if filters.Status != "" {
		args = append(args, "--status", filters.Status)
	}
	if filters.Project != "" {
		args = append(args, "--project", filters.Project)
	}
	if filters.Limit > 0 {
		args = append(args, "--limit", fmt.Sprintf("%d", filters.Limit))
	}

	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}

	var raw struct {
		Results []json.RawMessage `json:"results"`
		Total   int               `json:"total"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse list response: %w", err)
	}

	result := &ListResult{Total: raw.Total}
	for _, r := range raw.Results {
		item, err := c.parseWorkItem(r)
		if err != nil {
			continue // skip malformed items
		}
		result.Items = append(result.Items, *item)
	}
	return result, nil
}

func (c *CPConnector) GetEdges(ctx context.Context, id string, direction string, types []string) ([]Edge, error) {
	args := []string{"shard", "edges", id, "-o", "json"}
	if direction != "" {
		args = append(args, "--direction", direction)
	}
	if len(types) > 0 {
		args = append(args, "--edge-type", strings.Join(types, ","))
	}

	out, err := c.runBare(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("edges %s: %w", id, err)
	}

	var edges []Edge
	if err := json.Unmarshal(out, &edges); err != nil {
		return nil, fmt.Errorf("parse edges: %w", err)
	}
	return edges, nil
}

func (c *CPConnector) GetMetadata(ctx context.Context, id string, key string) (string, error) {
	out, err := c.runBare(ctx, "shard", "metadata", "get", id, key)
	if err != nil {
		return "", fmt.Errorf("get metadata %s.%s: %w", id, key, err)
	}
	// cxp metadata get returns the raw value, possibly quoted
	val := strings.TrimSpace(string(out))
	// Try to unquote if it's a JSON string
	var s string
	if json.Unmarshal([]byte(val), &s) == nil {
		return s, nil
	}
	return val, nil
}

// --- Write ---

func (c *CPConnector) Create(ctx context.Context, req CreateRequest) (string, error) {
	args := []string{"shard", "create", "--type", req.Type, "--title", req.Title, "-o", "json"}
	if req.Content != "" {
		args = append(args, "--body", req.Content)
	}
	if req.ParentID != "" {
		args = append(args, "--parent", req.ParentID)
	}
	if len(req.Labels) > 0 {
		args = append(args, "--label", strings.Join(req.Labels, ","))
	}
	if len(req.Metadata) > 0 {
		metaJSON, err := json.Marshal(req.Metadata)
		if err == nil {
			args = append(args, "--meta", string(metaJSON))
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

func (c *CPConnector) UpdateStatus(ctx context.Context, id string, status string) error {
	_, err := c.runBareForShard(ctx, id, "shard", "status", id, status)
	if err != nil {
		return fmt.Errorf("update status %s → %s: %w", id, status, err)
	}
	return nil
}

func (c *CPConnector) AppendContent(ctx context.Context, id string, content string) error {
	_, err := c.runBareForShard(ctx, id, "shard", "append", id, "--body", content)
	if err != nil {
		return fmt.Errorf("append %s: %w", id, err)
	}
	return nil
}

func (c *CPConnector) SetMetadata(ctx context.Context, id string, key string, value any) error {
	valStr, err := marshalValue(value)
	if err != nil {
		return fmt.Errorf("marshal metadata value: %w", err)
	}
	_, err = c.runBareForShard(ctx, id, "shard", "metadata", "set", id, key, valStr)
	if err != nil {
		return fmt.Errorf("set metadata %s.%s: %w", id, key, err)
	}
	return nil
}

func (c *CPConnector) UpdateMetadataMap(ctx context.Context, id string, patch map[string]any) error {
	for k, v := range patch {
		if err := c.SetMetadata(ctx, id, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (c *CPConnector) AddLabel(ctx context.Context, id string, label string) error {
	_, err := c.runBareForShard(ctx, id, "shard", "label", "add", id, label)
	if err != nil {
		return fmt.Errorf("add label %s %s: %w", id, label, err)
	}
	return nil
}

func (c *CPConnector) CreateEdge(ctx context.Context, fromID string, toID string, edgeType string) error {
	// cxp shard link is a write op, scoped by --project. Use
	// runBareForShard with the "from" ID to resolve the project.
	flag := edgeTypeToFlag(edgeType)
	_, err := c.runBareForShard(ctx, fromID, "shard", "link", fromID, flag, toID)
	if err != nil {
		return fmt.Errorf("create edge %s -[%s]-> %s: %w", fromID, edgeType, toID, err)
	}
	return nil
}

// --- Helpers ---

// run executes a cxp command for list/create/query operations that need
// project scoping. Automatically appends --project and --agent global
// flags if configured. Use runBare for shard-id-targeted operations —
// cxp resolves shard IDs globally and adding --project to those
// commands causes "shard not found" errors for any cross-project task.
// (Observed during cp-cb935b's wave 1 dispatch, 2026-04-11, when
// context-palace-scoped dispatch tried to status-update a pf-9413d7
// task — the --project context-palace flag made cxp look in the wrong
// namespace even though the shard ID is globally unique.)
func (c *CPConnector) run(ctx context.Context, args ...string) (json.RawMessage, error) {
	// Add global flags if not already present in args
	hasProject := false
	hasAgent := false
	for _, a := range args {
		if a == "--project" {
			hasProject = true
		}
		if a == "--agent" {
			hasAgent = true
		}
	}
	if !hasProject && c.Project != "" {
		args = append(args, "--project", c.Project)
	}
	if !hasAgent && c.Agent != "" {
		args = append(args, "--agent", c.Agent)
	}
	return c.exec(ctx, args...)
}

// runBare executes a cxp command without auto-appending --project.
// Used for shard-id-targeted READ operations (show, edges, metadata get)
// where cxp's global shard-ID lookup finds the shard regardless of its
// home project.
//
// WRITE operations (status, append, metadata set, label add, link) use
// runBareForShard instead — cxp's write commands unfortunately filter
// shard-id lookups by the current project, so we have to pass an
// explicit --project derived from the shard ID prefix.
func (c *CPConnector) runBare(ctx context.Context, args ...string) (json.RawMessage, error) {
	// Still add --agent if configured; agent identity is separate from
	// project scoping and applies to any operation.
	hasAgent := false
	for _, a := range args {
		if a == "--agent" {
			hasAgent = true
			break
		}
	}
	if !hasAgent && c.Agent != "" {
		args = append(args, "--agent", c.Agent)
	}
	return c.exec(ctx, args...)
}

// runBareForShard is runBare + auto-resolve --project from the shard's
// ID prefix. Use for write operations (cxp shard status / append /
// metadata set / label add / link) where cxp filters id lookups by
// project even though shard IDs are globally unique. See the comment on
// exec() for the cp-cb935b wave 1 incident that motivated this.
func (c *CPConnector) runBareForShard(ctx context.Context, shardID string, args ...string) (json.RawMessage, error) {
	// Don't double-add --project if the caller already set one
	hasProject := false
	for _, a := range args {
		if a == "--project" {
			hasProject = true
			break
		}
	}
	if !hasProject {
		if proj := projectForShardID(shardID); proj != "" {
			args = append(args, "--project", proj)
		}
	}
	return c.runBare(ctx, args...)
}

// projectForShardID maps a shard-ID prefix (the part before the first "-")
// to its home project name, so write operations can be correctly scoped
// without relying on cwd auto-detection. Static map of the known projects
// in this ecosystem; extend as new projects are onboarded.
//
// Note: penf-cli shares the penfold project namespace (per its
// .cobuild.yaml) so its shards use the pf- prefix and map to penfold.
// Refactor to dynamic lookup via LoadRepoRegistry + per-project identity
// files if the set of projects gets larger or churns frequently.
func projectForShardID(id string) string {
	prefix, _, ok := strings.Cut(id, "-")
	if !ok {
		return ""
	}
	switch prefix {
	case "cb":
		return "cobuild"
	case "cp":
		return "context-palace"
	case "pf":
		return "penfold"
	case "my":
		return "mycroft"
	case "mp":
		return "moneypenny"
	default:
		return ""
	}
}

// exec is the shared shell-out for run and runBare.
//
// Sets cmd.Dir to os.TempDir() to avoid inheriting cobuild's cwd. `cxp`
// auto-detects a project from the cwd's `.cxp.yaml` / `.cobuild.yaml` and
// implicitly scopes queries to that project — so running `cxp shard status
// pf-9413d7` from a context-palace checkout fails with "shard not found"
// because cxp thinks you're asking for a pf- shard within context-palace's
// namespace. We observed this during cp-cb935b wave 1 dispatch (2026-04-11).
// Running from /tmp is neutral — no project auto-detection fires, shard IDs
// are looked up globally, and explicit --project flags (added by run() for
// list/create ops) still take precedence over the neutral cwd.
func (c *CPConnector) exec(ctx context.Context, args ...string) (json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, "cxp", args...)
	cmd.Dir = os.TempDir()
	if c.Debug {
		fmt.Printf("[connector:cp] cxp %s\n", strings.Join(args, " "))
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

// parseWorkItem converts cxp JSON output into a WorkItem.
func (c *CPConnector) parseWorkItem(data json.RawMessage) (*WorkItem, error) {
	// cxp shard show returns a rich object; shard list returns a summary.
	// Parse the superset and fill what's available.
	var raw struct {
		ID        string          `json:"id"`
		Title     string          `json:"title"`
		Content   string          `json:"content"`
		Type      string          `json:"type"`
		Status    string          `json:"status"`
		Project   string          `json:"project"`
		Creator   string          `json:"creator"`
		Metadata  json.RawMessage `json:"metadata"`
		CreatedAt time.Time       `json:"created_at"`
		UpdatedAt time.Time       `json:"updated_at"`
		Edges     []Edge          `json:"edges"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse work item: %w", err)
	}

	item := &WorkItem{
		ID:        raw.ID,
		Title:     raw.Title,
		Content:   raw.Content,
		Type:      raw.Type,
		Status:    raw.Status,
		Project:   raw.Project,
		Creator:   raw.Creator,
		Edges:     raw.Edges,
		CreatedAt: raw.CreatedAt,
		UpdatedAt: raw.UpdatedAt,
		Raw:       data,
	}

	if len(raw.Metadata) > 0 && string(raw.Metadata) != "{}" && string(raw.Metadata) != "null" {
		var meta map[string]any
		if json.Unmarshal(raw.Metadata, &meta) == nil {
			item.Metadata = meta
		}
	}

	return item, nil
}

// edgeTypeToFlag maps edge type strings to cxp shard link flags.
func edgeTypeToFlag(edgeType string) string {
	switch edgeType {
	case "child-of":
		return "--child-of"
	case "blocked-by":
		return "--blocked-by"
	case "blocks":
		return "--blocks"
	case "relates-to":
		return "--relates-to"
	case "implements":
		return "--implements"
	case "references":
		return "--references"
	case "extends":
		return "--extends"
	case "discovered-from":
		return "--discovered-from"
	case "has-artifact":
		return "--has-artifact"
	case "triggered-by":
		return "--triggered-by"
	case "previous-version":
		return "--previous-version"
	case "replies-to":
		return "--replies-to"
	default:
		return "--" + edgeType
	}
}

// marshalValue converts a Go value to a string suitable for cxp metadata set.
func marshalValue(v any) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case json.RawMessage:
		return string(val), nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}
