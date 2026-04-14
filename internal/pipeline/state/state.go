// Package state resolves the effective state of a pipeline task by
// combining three sources: the work-item record (connector), the
// pipeline_run / pipeline_sessions rows (store), and any live process
// or tmux window observed on the host. The resolver normalises these
// into a single view so command handlers can reason about "is this
// task dispatchable right now?" without re-running the classification
// logic in every caller.
//
// Call sites: cobuild dispatch (conflict detection + self-heal),
// cobuild doctor, cobuild recover, and the orchestrator's progress
// monitor. A parallel, richer view — including process-level liveness
// — lives in the sibling livestate package.
package state

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

// ErrNotFound is returned when a design cannot be found in any resolver source.
var ErrNotFound = errors.New("pipeline state not found")

// Health is the resolver's canonical summary of a design's pipeline state.
type Health string

const (
	HealthOK           Health = "OK"
	HealthInconsistent Health = "INCONSISTENT"
	HealthStale        Health = "STALE"
	HealthZombie       Health = "ZOMBIE"
	HealthMissing      Health = "MISSING"
)

// SourceError records a non-fatal source collection failure.
type SourceError struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

// PipelineState is the merged, read-only state for one design.
type PipelineState struct {
	DesignID        string         `json:"design_id"`
	Project         string         `json:"project,omitempty"`
	WorkItem        *WorkItemState `json:"work_item,omitempty"`
	Run             *RunState      `json:"run,omitempty"`
	Sessions        []SessionState `json:"sessions,omitempty"`
	Tmux            []TmuxWindow   `json:"tmux,omitempty"`
	Health          Health         `json:"health"`
	Inconsistencies []string       `json:"inconsistencies,omitempty"`
	SourceErrors    []SourceError  `json:"source_errors,omitempty"`
	ResolvedAt      time.Time      `json:"resolved_at"`
}

// WorkItemState is the connector-backed portion of pipeline state.
type WorkItemState struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	Project   string    `json:"project,omitempty"`
	Labels    []string  `json:"labels,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RunState is the pipeline_runs-backed portion of pipeline state.
type RunState struct {
	ID        string    `json:"id"`
	Phase     string    `json:"phase"`
	Status    string    `json:"status"`
	Mode      string    `json:"mode,omitempty"`
	Project   string    `json:"project,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionState is the pipeline_sessions-backed portion of pipeline state.
type SessionState struct {
	ID           string     `json:"id"`
	PipelineID   string     `json:"pipeline_id"`
	DesignID     string     `json:"design_id"`
	TaskID       string     `json:"task_id,omitempty"`
	Phase        string     `json:"phase,omitempty"`
	Project      string     `json:"project,omitempty"`
	Runtime      string     `json:"runtime,omitempty"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	AgeSeconds   int64      `json:"age_seconds"`
	WorktreePath string     `json:"worktree_path,omitempty"`
	TmuxSession  string     `json:"tmux_session,omitempty"`
	TmuxWindow   string     `json:"tmux_window,omitempty"`
}

// TmuxWindow is a discovered tmux window relevant to the design.
type TmuxWindow struct {
	SessionName string `json:"session_name"`
	Project     string `json:"project,omitempty"`
	WindowID    string `json:"window_id,omitempty"`
	WindowName  string `json:"window_name"`
	TargetID    string `json:"target_id,omitempty"`
}

// Dependencies defines the resolver's read-only source inputs.
type Dependencies struct {
	Connector WorkItemGetter
	Store     Store
	Exec      CommandRunner
	Now       func() time.Time
}

// Resolver collects and merges source state for one design.
type Resolver struct {
	connector WorkItemGetter
	store     Store
	exec      CommandRunner
	now       func() time.Time
}

var (
	defaultResolverMu sync.RWMutex
	defaultResolver   = NewResolver(Dependencies{})
)

// ConfigureDefault replaces the package-level resolver used by Resolve.
func ConfigureDefault(deps Dependencies) {
	defaultResolverMu.Lock()
	defer defaultResolverMu.Unlock()
	defaultResolver = NewResolver(deps)
}

// NewResolver builds a resolver with explicit dependency seams for tests.
func NewResolver(deps Dependencies) *Resolver {
	execFn := deps.Exec
	if execFn == nil {
		execFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}
	nowFn := deps.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Resolver{
		connector: deps.Connector,
		store:     deps.Store,
		exec:      execFn,
		now:       nowFn,
	}
}

// Resolve uses the package-level default resolver configured by the CLI.
func Resolve(ctx context.Context, designID string) (*PipelineState, error) {
	defaultResolverMu.RLock()
	resolver := defaultResolver
	defaultResolverMu.RUnlock()
	return resolver.Resolve(ctx, designID)
}

// Resolve collects and merges source state for one design.
func (r *Resolver) Resolve(ctx context.Context, designID string) (*PipelineState, error) {
	resolvedAt := r.now()
	state := &PipelineState{
		DesignID:   designID,
		ResolvedAt: resolvedAt,
	}

	if designID == "" {
		state.Health = HealthMissing
		return state, fmt.Errorf("design id is required")
	}

	workItem, err := r.collectWorkItem(ctx, designID)
	switch {
	case err == nil:
		state.WorkItem = workItem
	case isNotFound(err):
	default:
		state.SourceErrors = append(state.SourceErrors, SourceError{Source: "connector", Message: err.Error()})
	}

	run, err := r.collectRun(ctx, designID)
	switch {
	case err == nil:
		state.Run = run
	case isNotFound(err):
	default:
		state.SourceErrors = append(state.SourceErrors, SourceError{Source: "run", Message: err.Error()})
	}

	sessions, err := r.collectSessions(ctx, designID, resolvedAt)
	switch {
	case err == nil:
		state.Sessions = sessions
	case isNotFound(err):
	default:
		state.SourceErrors = append(state.SourceErrors, SourceError{Source: "sessions", Message: err.Error()})
	}

	windows, err := r.collectTmux(ctx, designID)
	switch {
	case err == nil:
		state.Tmux = windows
	case isNotFound(err):
	default:
		state.SourceErrors = append(state.SourceErrors, SourceError{Source: "tmux", Message: err.Error()})
	}

	state.Project = resolveProject(state)
	state.Health, state.Inconsistencies = computeHealth(state)

	if state.Health == HealthMissing {
		return state, ErrNotFound
	}
	return state, nil
}

func resolveProject(state *PipelineState) string {
	switch {
	case state.WorkItem != nil && state.WorkItem.Project != "":
		return state.WorkItem.Project
	case state.Run != nil && state.Run.Project != "":
		return state.Run.Project
	}
	for _, session := range state.Sessions {
		if session.Project != "" {
			return session.Project
		}
	}
	for _, window := range state.Tmux {
		if window.Project != "" {
			return window.Project
		}
	}
	return ""
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrNotFound) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not found") || strings.Contains(message, "no such")
}

var _ WorkItemGetter = connector.Connector(nil)
