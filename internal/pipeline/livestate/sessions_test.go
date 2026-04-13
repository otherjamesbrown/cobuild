package livestate

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

type fakeSessionStore struct {
	project  string
	sessions []store.SessionRecord
	err      error
}

func (f *fakeSessionStore) ListRunningSessions(_ context.Context, project string) ([]store.SessionRecord, error) {
	f.project = project
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

func TestCollectSessionsMapsRunningRecords(t *testing.T) {
	now := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-25 * time.Minute)
	model := "gpt-5.4"
	worktreePath := "/tmp/cb-bc00c5"
	tmuxSession := "cobuild-cobuild"
	tmuxWindow := "cb-bc00c5"

	sessionStore := &fakeSessionStore{
		sessions: []store.SessionRecord{
			{
				ID:           "sess-123",
				PipelineID:   "pipe-123",
				DesignID:     "cb-1660be",
				TaskID:       "cb-bc00c5",
				Phase:        "implement",
				Project:      "cobuild",
				Runtime:      "codex",
				StartedAt:    startedAt,
				Model:        &model,
				Status:       "running",
				WorktreePath: &worktreePath,
				TmuxSession:  &tmuxSession,
				TmuxWindow:   &tmuxWindow,
			},
		},
	}

	got, err := CollectSessions(context.Background(), sessionStore, now)
	if err != nil {
		t.Fatalf("CollectSessions() error = %v", err)
	}
	if sessionStore.project != "" {
		t.Fatalf("ListRunningSessions project = %q, want empty", sessionStore.project)
	}

	want := []SessionInfo{
		{
			ID:           "sess-123",
			PipelineID:   "pipe-123",
			DesignID:     "cb-1660be",
			TaskID:       "cb-bc00c5",
			Phase:        "implement",
			Project:      "cobuild",
			Runtime:      "codex",
			StartedAt:    startedAt,
			AgeSeconds:   1500,
			Model:        &model,
			Status:       "running",
			WorktreePath: &worktreePath,
			TmuxSession:  &tmuxSession,
			TmuxWindow:   &tmuxWindow,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectSessions() = %#v, want %#v", got, want)
	}
}

func TestCollectSessionsReturnsStoreErrors(t *testing.T) {
	sessionStore := &fakeSessionStore{err: errors.New("db offline")}

	_, err := CollectSessions(context.Background(), sessionStore, time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("CollectSessions() error = nil, want error")
	}
	if got, want := err.Error(), "list running sessions: db offline"; got != want {
		t.Fatalf("CollectSessions() error = %q, want %q", got, want)
	}
}
