package livestate

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

// CommandRunner executes a command and returns its combined output.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Snapshot is a single-point-in-time view of live local pipeline state.
type Snapshot struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Processes   []ProcessInfo `json:"processes"`
	Tmux        []TmuxWindow  `json:"tmux"`
	Sessions    []SessionInfo `json:"sessions,omitempty"`
	Errors      []SourceError `json:"errors,omitempty"`
}

// SourceError records a non-fatal collector failure so callers can surface
// partial snapshots without losing the successful sections.
type SourceError struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

// ProcessInfo is a parsed live CoBuild CLI process.
type ProcessInfo struct {
	PID        int        `json:"pid"`
	Kind       string     `json:"kind"`
	Project    string     `json:"project,omitempty"`
	TargetID   string     `json:"target_id,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	AgeSeconds int64      `json:"age_seconds,omitempty"`
	Command    string     `json:"command"`
}

// TmuxWindow is a discovered tmux window in a cobuild-* session.
type TmuxWindow struct {
	SessionName string `json:"session_name"`
	Project     string `json:"project,omitempty"`
	WindowID    string `json:"window_id,omitempty"`
	WindowName  string `json:"window_name"`
	TargetID    string `json:"target_id,omitempty"`
}

// SessionStore is the narrow store surface needed to read running sessions.
type SessionStore interface {
	ListRunningSessions(ctx context.Context, project string) ([]store.SessionRecord, error)
}

// Collector wires command execution and time for snapshot collection.
type Collector struct {
	Exec  CommandRunner
	Store SessionStore
	Now   func() time.Time
}

// Collect gathers the currently supported live state sources.
func (c Collector) Collect(ctx context.Context) (Snapshot, error) {
	c = c.withDefaults()

	snapshot := Snapshot{
		GeneratedAt: c.Now(),
	}

	processes, err := CollectProcesses(ctx, c.Exec, snapshot.GeneratedAt)
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, SourceError{
			Source:  "processes",
			Message: err.Error(),
		})
	} else {
		snapshot.Processes = processes
	}

	tmuxRows, err := CollectTmux(ctx, c.Exec)
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, SourceError{
			Source:  "tmux",
			Message: err.Error(),
		})
	} else {
		snapshot.Tmux = tmuxRows
	}

	if c.Store != nil {
		sessionRows, err := CollectSessions(ctx, c.Store, snapshot.GeneratedAt)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, SourceError{
				Source:  "sessions",
				Message: err.Error(),
			})
		} else {
			snapshot.Sessions = sessionRows
		}
	}

	if len(snapshot.Errors) == 0 {
		return snapshot, nil
	}

	errs := make([]error, 0, len(snapshot.Errors))
	for _, entry := range snapshot.Errors {
		errs = append(errs, fmt.Errorf("%s: %s", entry.Source, entry.Message))
	}
	return snapshot, errors.Join(errs...)
}

func (c Collector) withDefaults() Collector {
	if c.Exec == nil {
		c.Exec = defaultCommandRunner
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

func defaultCommandRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
