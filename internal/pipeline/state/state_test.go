package state

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestResolveHealthStatesAndDegradedSources(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 13, 11, 0, 0, 0, time.UTC)
	endedAt := now.Add(-5 * time.Minute)

	tests := []struct {
		name                 string
		connector            *fakeConnector
		store                *fakeStore
		exec                 CommandRunner
		wantHealth           Health
		wantErr              error
		wantInconsistencies  []string
		wantSourceErrorNames []string
	}{
		{
			name: "ok",
			connector: &fakeConnector{item: &connector.WorkItem{
				ID: "cb-ok", Type: "design", Status: "open", Project: "cobuild",
			}},
			store: &fakeStore{
				run: &store.PipelineRun{
					ID: "pr-1", DesignID: "cb-ok", Project: "cobuild", CurrentPhase: "implement", Status: "active",
				},
				sessions: []store.SessionRecord{
					{
						ID: "ps-1", DesignID: "cb-ok", PipelineID: "pr-1", Project: "cobuild", Status: "running",
						StartedAt: now.Add(-10 * time.Minute), TmuxSession: ptr("cobuild-cobuild"), TmuxWindow: ptr("cb-ok"),
					},
				},
			},
			exec:       tmuxExecFor("cb-ok"),
			wantHealth: HealthOK,
		},
		{
			name: "inconsistent",
			connector: &fakeConnector{item: &connector.WorkItem{
				ID: "cb-conflict", Type: "design", Status: "closed", Project: "cobuild",
			}},
			store: &fakeStore{
				run: &store.PipelineRun{
					ID: "pr-2", DesignID: "cb-conflict", Project: "cobuild", CurrentPhase: "review", Status: "completed",
				},
				sessions: []store.SessionRecord{
					{
						ID: "ps-2", DesignID: "cb-conflict", PipelineID: "pr-2", Project: "cobuild", Status: "running",
						StartedAt: now.Add(-20 * time.Minute), TmuxSession: ptr("cobuild-cobuild"), TmuxWindow: ptr("cb-conflict"),
					},
				},
			},
			exec:       tmuxExecFor("cb-conflict"),
			wantHealth: HealthInconsistent,
			wantInconsistencies: []string{
				"pipeline run is completed but a session is still running",
			},
		},
		{
			name: "zombie",
			connector: &fakeConnector{item: &connector.WorkItem{
				ID: "cb-zombie", Type: "design", Status: "open", Project: "cobuild",
			}},
			store: &fakeStore{
				run: &store.PipelineRun{
					ID: "pr-3", DesignID: "cb-zombie", Project: "cobuild", CurrentPhase: "implement", Status: "active",
				},
				sessions: []store.SessionRecord{
					{
						ID: "ps-3", DesignID: "cb-zombie", PipelineID: "pr-3", Project: "cobuild", Status: "running",
						StartedAt: now.Add(-15 * time.Minute), TmuxSession: ptr("cobuild-cobuild"), TmuxWindow: ptr("cb-zombie"),
					},
				},
			},
			exec:       tmuxExecFor(),
			wantHealth: HealthZombie,
			wantInconsistencies: []string{
				"session ps-3 is running but no tmux window exists",
			},
		},
		{
			name: "stale",
			connector: &fakeConnector{item: &connector.WorkItem{
				ID: "cb-stale", Type: "design", Status: "open", Project: "cobuild",
			}},
			store: &fakeStore{
				run: &store.PipelineRun{
					ID: "pr-4", DesignID: "cb-stale", Project: "cobuild", CurrentPhase: "implement", Status: "active",
				},
				sessions: []store.SessionRecord{
					{
						ID: "ps-4", DesignID: "cb-stale", PipelineID: "pr-4", Project: "cobuild", Status: "orphaned",
						StartedAt: now.Add(-30 * time.Minute), EndedAt: &endedAt,
					},
				},
			},
			exec:       tmuxExecFor(),
			wantHealth: HealthStale,
		},
		{
			name:       "missing",
			connector:  &fakeConnector{err: errors.New("work item not found")},
			store:      &fakeStore{runErr: store.ErrNotFound},
			exec:       tmuxExecFor(),
			wantHealth: HealthMissing,
			wantErr:    ErrNotFound,
		},
		{
			name: "degraded tmux source does not invent zombie",
			connector: &fakeConnector{item: &connector.WorkItem{
				ID: "cb-degraded", Type: "design", Status: "open", Project: "cobuild",
			}},
			store: &fakeStore{
				run: &store.PipelineRun{
					ID: "pr-5", DesignID: "cb-degraded", Project: "cobuild", CurrentPhase: "implement", Status: "active",
				},
				sessions: []store.SessionRecord{
					{
						ID: "ps-5", DesignID: "cb-degraded", PipelineID: "pr-5", Project: "cobuild", Status: "running",
						StartedAt: now.Add(-3 * time.Minute), TmuxSession: ptr("cobuild-cobuild"), TmuxWindow: ptr("cb-degraded"),
					},
				},
			},
			exec: func(context.Context, string, ...string) ([]byte, error) {
				return nil, errors.New("tmux unavailable")
			},
			wantHealth:           HealthOK,
			wantSourceErrorNames: []string{"tmux"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resolver := NewResolver(Dependencies{
				Connector: test.connector,
				Store:     test.store,
				Exec:      test.exec,
				Now:       func() time.Time { return now },
			})

			got, err := resolver.Resolve(context.Background(), test.connector.designID())
			if got == nil {
				t.Fatal("Resolve() returned nil state")
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.wantErr)
			}
			if got.Health != test.wantHealth {
				t.Fatalf("Health = %s, want %s", got.Health, test.wantHealth)
			}
			if !reflect.DeepEqual(got.Inconsistencies, test.wantInconsistencies) {
				t.Fatalf("Inconsistencies = %#v, want %#v", got.Inconsistencies, test.wantInconsistencies)
			}

			var gotSourceNames []string
			for _, sourceErr := range got.SourceErrors {
				gotSourceNames = append(gotSourceNames, sourceErr.Source)
			}
			if !reflect.DeepEqual(gotSourceNames, test.wantSourceErrorNames) {
				t.Fatalf("SourceErrors = %#v, want %#v", gotSourceNames, test.wantSourceErrorNames)
			}
		})
	}
}

// Regression for cb-09c328: a pipeline sitting at phase=review with no
// activity for longer than staleReviewWindow should be flagged stale so
// it can be filtered out of the "actually active" live view.
func TestComputeHealthFlagsReviewStuck(t *testing.T) {
	now := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		phase      string
		updatedAt  time.Time
		wantHealth Health
	}{
		{"review stuck past window", "review", now.Add(-48 * time.Hour), HealthStale},
		{"review fresh", "review", now.Add(-5 * time.Minute), HealthOK},
		{"implement long-running NOT stale on phase alone", "implement", now.Add(-48 * time.Hour), HealthOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &PipelineState{
				DesignID:   "cb-stuck",
				Run:        &RunState{ID: "pr-1", Status: "active", Phase: tc.phase, UpdatedAt: tc.updatedAt},
				WorkItem:   &WorkItemState{ID: "cb-stuck", Status: "open"},
				ResolvedAt: now,
			}
			got, _ := computeHealth(state)
			if got != tc.wantHealth {
				t.Fatalf("health = %q, want %q", got, tc.wantHealth)
			}
		})
	}
}

type fakeConnector struct {
	item *connector.WorkItem
	err  error
}

func (f *fakeConnector) Get(context.Context, string) (*connector.WorkItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.item, nil
}

func (f *fakeConnector) designID() string {
	if f.item != nil {
		return f.item.ID
	}
	return "cb-missing"
}

type fakeStore struct {
	run      *store.PipelineRun
	runErr   error
	sessions []store.SessionRecord
	listErr  error
}

func (f *fakeStore) GetRun(context.Context, string) (*store.PipelineRun, error) {
	if f.runErr != nil {
		return nil, f.runErr
	}
	return f.run, nil
}

func (f *fakeStore) ListSessions(context.Context, string) ([]store.SessionRecord, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]store.SessionRecord(nil), f.sessions...), nil
}

func tmuxExecFor(targets ...string) CommandRunner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "tmux" {
			return nil, errors.New("unexpected command")
		}
		switch stringsJoin(args) {
		case "list-sessions -F #{session_name}":
			return []byte("cobuild-cobuild\n"), nil
		case "list-windows -t cobuild-cobuild -F #{window_id}\t#{window_name}":
			lines := make([]string, 0, len(targets))
			for i, target := range targets {
				lines = append(lines, "@"+string(rune('1'+i))+"\t"+target)
			}
			return []byte(stringsJoinWithNewline(lines)), nil
		default:
			return nil, errors.New("unexpected tmux args")
		}
	}
}

func stringsJoin(args []string) string {
	if len(args) == 0 {
		return ""
	}
	out := args[0]
	for _, arg := range args[1:] {
		out += " " + arg
	}
	return out
}

func stringsJoinWithNewline(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := lines[0]
	for _, line := range lines[1:] {
		out += "\n" + line
	}
	return out + "\n"
}

func ptr(value string) *string {
	return &value
}
