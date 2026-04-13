package state

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestRecommendRecoveries(t *testing.T) {
	t.Parallel()

	state := &PipelineState{
		DesignID: "cb-recover",
		WorkItem: &WorkItemState{ID: "cb-recover", Status: "closed"},
		Run:      &RunState{ID: "run-1", Phase: "implement", Status: "active"},
		Sessions: []SessionState{
			{ID: "ps-1", DesignID: "cb-recover", Status: "running", TmuxSession: "cobuild-test", TmuxWindow: "cb-recover"},
		},
		Tmux: []TmuxWindow{
			{SessionName: "cobuild-test", WindowID: "@1", WindowName: "other-window"},
		},
	}

	got := RecommendRecoveries(state)
	if len(got) != 3 {
		t.Fatalf("len(RecommendRecoveries()) = %d, want 3", len(got))
	}

	var kinds []RecoveryKind
	var reasons []string
	for _, recommendation := range got {
		kinds = append(kinds, recommendation.Kind)
		reasons = append(reasons, recommendation.Reason)
	}

	wantKinds := []RecoveryKind{
		RecoveryCancelOrphanedSession,
		RecoveryKillOrphanTmuxWindow,
		RecoveryCompleteStaleRun,
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("kinds = %#v, want %#v", kinds, wantKinds)
	}

	wantReasons := []string{
		"session ps-1 is running but no tmux window exists",
		"tmux window cobuild-test:other-window exists but no matching pipeline session exists",
		"pipeline cb-recover run is active but work item is closed",
	}
	if !reflect.DeepEqual(reasons, wantReasons) {
		t.Fatalf("reasons = %#v, want %#v", reasons, wantReasons)
	}
}

func TestRecommendRecoveriesSkipsTmuxDerivedActionsWhenTmuxSourceFailed(t *testing.T) {
	t.Parallel()

	got := RecommendRecoveries(&PipelineState{
		DesignID:     "cb-recover",
		WorkItem:     &WorkItemState{ID: "cb-recover", Status: "closed"},
		Run:          &RunState{ID: "run-1", Phase: "implement", Status: "active"},
		Sessions:     []SessionState{{ID: "ps-1", DesignID: "cb-recover", Status: "running"}},
		SourceErrors: []SourceError{{Source: "tmux", Message: "unavailable"}},
	})

	want := []RecoveryRecommendation{{
		Kind:     RecoveryCompleteStaleRun,
		DesignID: "cb-recover",
		Reason:   "pipeline cb-recover run is active but work item is closed",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RecommendRecoveries() = %#v, want %#v", got, want)
	}
}

func TestCancelOrphanedSession(t *testing.T) {
	t.Parallel()

	t.Run("marks running session orphaned", func(t *testing.T) {
		t.Parallel()

		store := &fakeRecoveryStore{}
		session := SessionState{ID: "ps-1", DesignID: "cb-recover", Status: "running"}

		got, err := CancelOrphanedSession(context.Background(), RecoveryDependencies{Store: store}, session)
		if err != nil {
			t.Fatalf("CancelOrphanedSession() error = %v", err)
		}
		if !got.Changed {
			t.Fatalf("Changed = false, want true")
		}
		if got.Reason != "session ps-1 is running but no tmux window exists" {
			t.Fatalf("Reason = %q", got.Reason)
		}
		if len(store.endedSessions) != 1 {
			t.Fatalf("len(endedSessions) = %d, want 1", len(store.endedSessions))
		}
		if store.endedSessions[0].id != "ps-1" {
			t.Fatalf("ended session id = %q, want ps-1", store.endedSessions[0].id)
		}
		if store.endedSessions[0].result.Status != "orphaned" {
			t.Fatalf("status = %q, want orphaned", store.endedSessions[0].result.Status)
		}
		if store.endedSessions[0].result.CompletionNote != got.Reason {
			t.Fatalf("completion note = %q, want %q", store.endedSessions[0].result.CompletionNote, got.Reason)
		}
	})

	t.Run("already reconciled is a no-op", func(t *testing.T) {
		t.Parallel()

		store := &fakeRecoveryStore{}
		got, err := CancelOrphanedSession(context.Background(), RecoveryDependencies{Store: store}, SessionState{
			ID:     "ps-2",
			Status: "orphaned",
		})
		if err != nil {
			t.Fatalf("CancelOrphanedSession() error = %v", err)
		}
		if got.Changed {
			t.Fatalf("Changed = true, want false")
		}
		if len(store.endedSessions) != 0 {
			t.Fatalf("len(endedSessions) = %d, want 0", len(store.endedSessions))
		}
	})
}

func TestKillOrphanTmuxWindow(t *testing.T) {
	t.Parallel()

	t.Run("kills orphan tmux window", func(t *testing.T) {
		t.Parallel()

		var calls [][]string
		got, err := KillOrphanTmuxWindow(context.Background(), RecoveryDependencies{
			Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				calls = append(calls, append([]string{name}, args...))
				return nil, nil
			},
		}, TmuxWindow{
			SessionName: "cobuild-test",
			WindowID:    "@7",
			WindowName:  "cb-recover",
		})
		if err != nil {
			t.Fatalf("KillOrphanTmuxWindow() error = %v", err)
		}
		if !got.Changed {
			t.Fatalf("Changed = false, want true")
		}
		wantCalls := [][]string{{"tmux", "kill-window", "-t", "@7"}}
		if !reflect.DeepEqual(calls, wantCalls) {
			t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
		}
	})

	t.Run("missing window is a no-op", func(t *testing.T) {
		t.Parallel()

		got, err := KillOrphanTmuxWindow(context.Background(), RecoveryDependencies{
			Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
				return nil, errors.New("can't find window: @7")
			},
		}, TmuxWindow{
			SessionName: "cobuild-test",
			WindowID:    "@7",
			WindowName:  "cb-recover",
		})
		if err != nil {
			t.Fatalf("KillOrphanTmuxWindow() error = %v", err)
		}
		if got.Changed {
			t.Fatalf("Changed = true, want false")
		}
	})
}

func TestCompleteStaleRun(t *testing.T) {
	t.Parallel()

	t.Run("completes active run for closed work item", func(t *testing.T) {
		t.Parallel()

		store := &fakeRecoveryStore{}
		state := &PipelineState{
			DesignID: "cb-recover",
			WorkItem: &WorkItemState{ID: "cb-recover", Status: "closed"},
			Run:      &RunState{ID: "run-1", Phase: "implement", Status: "active"},
		}

		got, err := CompleteStaleRun(context.Background(), RecoveryDependencies{Store: store}, state)
		if err != nil {
			t.Fatalf("CompleteStaleRun() error = %v", err)
		}
		if !got.Changed {
			t.Fatalf("Changed = false, want true")
		}
		if !reflect.DeepEqual(store.phaseUpdates, []string{"cb-recover:done"}) {
			t.Fatalf("phaseUpdates = %#v, want cb-recover:done", store.phaseUpdates)
		}
		if !reflect.DeepEqual(store.statusUpdates, []string{"cb-recover:completed"}) {
			t.Fatalf("statusUpdates = %#v, want cb-recover:completed", store.statusUpdates)
		}
	})

	t.Run("already completed is a no-op", func(t *testing.T) {
		t.Parallel()

		store := &fakeRecoveryStore{}
		got, err := CompleteStaleRun(context.Background(), RecoveryDependencies{Store: store}, &PipelineState{
			DesignID: "cb-recover",
			WorkItem: &WorkItemState{ID: "cb-recover", Status: "closed"},
			Run:      &RunState{ID: "run-1", Phase: "done", Status: "completed"},
		})
		if err != nil {
			t.Fatalf("CompleteStaleRun() error = %v", err)
		}
		if got.Changed {
			t.Fatalf("Changed = true, want false")
		}
		if len(store.phaseUpdates) != 0 || len(store.statusUpdates) != 0 {
			t.Fatalf("unexpected updates: phase=%v status=%v", store.phaseUpdates, store.statusUpdates)
		}
	})
}

type fakeRecoveryStore struct {
	endedSessions []endedSessionCall
	phaseUpdates  []string
	statusUpdates []string
}

type endedSessionCall struct {
	id     string
	result store.SessionResult
}

func (f *fakeRecoveryStore) EndSession(ctx context.Context, id string, result store.SessionResult) error {
	f.endedSessions = append(f.endedSessions, endedSessionCall{id: id, result: result})
	return nil
}

func (f *fakeRecoveryStore) UpdateRunPhase(ctx context.Context, designID, phase string) error {
	f.phaseUpdates = append(f.phaseUpdates, fmt.Sprintf("%s:%s", designID, phase))
	return nil
}

func (f *fakeRecoveryStore) UpdateRunStatus(ctx context.Context, designID, status string) error {
	f.statusUpdates = append(f.statusUpdates, fmt.Sprintf("%s:%s", designID, status))
	return nil
}
