// Package connector defines the interface for external work-item systems.
//
// CoBuild uses connectors to read and write work items (designs, bugs, tasks)
// from external systems like Context Palace, Beads, or Jira. This follows the
// Claude Code/CoWork connector pattern — CoBuild never accesses the external
// system's database directly; everything goes through the connector interface.
//
// CoBuild's own orchestration data (pipeline runs, gates, dispatch state) is
// stored separately and is NOT part of the connector.
package connector

import (
	"context"
	"encoding/json"
	"time"
)

// WorkItem is the universal noun for any unit of work from an external system.
// It may be a Context Palace shard, a Beads issue, or a Jira ticket.
type WorkItem struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Content   string            `json:"content"`
	Type      string            `json:"type"`   // design, bug, task, review, outcome
	Status    string            `json:"status"` // open, ready, in_progress, needs-review, closed
	Project   string            `json:"project,omitempty"`
	Creator   string            `json:"creator,omitempty"`
	Labels    []string          `json:"labels,omitempty"`
	Metadata  map[string]any    `json:"metadata,omitempty"`
	Edges     []Edge            `json:"edges,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Raw       json.RawMessage   `json:"-"` // original JSON from the connector
}

// Edge represents a relationship between two work items.
type Edge struct {
	Direction string `json:"direction,omitempty"` // incoming, outgoing
	EdgeType  string `json:"edge_type"`           // child-of, blocked-by, relates-to, etc.
	ItemID    string `json:"shard_id"`            // the related work item's ID
	Title     string `json:"title,omitempty"`
	Type      string `json:"type,omitempty"`      // type of the related item
	Status    string `json:"status,omitempty"`    // status of the related item
}

// CreateRequest holds the fields needed to create a new work item.
type CreateRequest struct {
	Title    string         `json:"title"`
	Content  string         `json:"content,omitempty"`
	Type     string         `json:"type"`     // design, bug, task, review, outcome
	Labels   []string       `json:"labels,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	ParentID string         `json:"parent_id,omitempty"` // creates a child-of edge
}

// ListFilters controls which work items are returned by List.
type ListFilters struct {
	Type    string `json:"type,omitempty"`
	Status  string `json:"status,omitempty"`
	Project string `json:"project,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// ListResult wraps a paginated list of work items.
type ListResult struct {
	Items []WorkItem `json:"items"`
	Total int        `json:"total"`
}

// Connector abstracts an external work-item system.
//
// Implementations:
//   - CPConnector: Context Palace via cxp CLI
//   - BeadsConnector: Beads via bd CLI
//   - (future) JiraConnector, LinearConnector
type Connector interface {
	// Name returns the connector type (e.g. "context-palace", "beads").
	Name() string

	// --- Read ---

	// Get returns a single work item by ID, including its edges.
	Get(ctx context.Context, id string) (*WorkItem, error)

	// List returns work items matching the given filters.
	List(ctx context.Context, filters ListFilters) (*ListResult, error)

	// GetEdges returns edges for a work item, filtered by direction and types.
	// direction: "incoming", "outgoing", or "" for both.
	// types: edge types to include (e.g. "child-of", "blocked-by"). Nil means all.
	GetEdges(ctx context.Context, id string, direction string, types []string) ([]Edge, error)

	// GetMetadata reads a single metadata value from a work item.
	GetMetadata(ctx context.Context, id string, key string) (string, error)

	// --- Write ---

	// Create creates a new work item and returns its ID.
	Create(ctx context.Context, req CreateRequest) (string, error)

	// UpdateStatus changes the status of a work item.
	UpdateStatus(ctx context.Context, id string, status string) error

	// AppendContent appends text to a work item's content.
	AppendContent(ctx context.Context, id string, content string) error

	// SetMetadata sets a single metadata key on a work item.
	SetMetadata(ctx context.Context, id string, key string, value any) error

	// UpdateMetadataMap merges multiple metadata keys into a work item.
	UpdateMetadataMap(ctx context.Context, id string, patch map[string]any) error

	// AddLabel adds a label to a work item.
	AddLabel(ctx context.Context, id string, label string) error

	// CreateEdge creates a relationship between two work items.
	CreateEdge(ctx context.Context, fromID string, toID string, edgeType string) error
}
