package livestate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestBuildSnapshotPipelineHealthMatrix(t *testing.T) {
	now := time.Date(2026, 4, 12, 18, 0, 0, 0, time.UTC)
	cfg := config.DefaultConfig()
	cfg.Monitoring.StallTimeout = "30m"

	baseRun := store.PipelineRunStatus{
		DesignID:     "cb-123",
		Project:      "cobuild",
		Phase:        "implement",
		Status:       "active",
		LastProgress: now.Add(-5 * time.Minute),
	}
	baseSession := store.SessionRecord{
		ID:          "ps-1",
		PipelineID:  "pr-1",
		DesignID:    "cb-123",
		TaskID:      "cb-123",
		Phase:       "implement",
		Project:     "cobuild",
		Status:      "running",
		StartedAt:   now.Add(-20 * time.Minute),
		TmuxSession: ptr("cobuild-cobuild"),
		TmuxWindow:  ptr("cb-123"),
	}
	baseTmux := TmuxWindowInfo{
		SessionName: "cobuild-cobuild",
		WindowName:  "cb-123",
		Target:      "cobuild-cobuild:cb-123",
		Project:     "cobuild",
		DesignID:    "cb-123",
		TaskID:      "cb-123",
	}
	baseProcess := ProcessInfo{
		PID:       4242,
		Kind:      "orchestrate",
		Project:   "cobuild",
		DesignID:  "cb-123",
		TaskID:    "cb-123",
		StartedAt: now.Add(-20 * time.Minute),
	}

	t.Run("ok", func(t *testing.T) {
		snapshot := BuildSnapshot(BuildInput{
			Now:       now,
			Config:    cfg,
			Runs:      []store.PipelineRunStatus{baseRun},
			Sessions:  []store.SessionRecord{baseSession},
			Tmux:      []TmuxWindowInfo{baseTmux},
			Processes: []ProcessInfo{baseProcess},
		})

		if got := snapshot.Pipelines[0].Health; got != HealthOK {
			t.Fatalf("pipeline health = %s, want %s", got, HealthOK)
		}
		if got := snapshot.Pipelines[0].SessionID; got != "ps-1" {
			t.Fatalf("pipeline session id = %q, want ps-1", got)
		}
		if got := snapshot.Pipelines[0].TmuxTarget; got != "cobuild-cobuild:cb-123" {
			t.Fatalf("pipeline tmux target = %q", got)
		}
		if got := snapshot.Pipelines[0].OrchestratePID; got != 4242 {
			t.Fatalf("pipeline pid = %d, want 4242", got)
		}
	})

	t.Run("warn", func(t *testing.T) {
		run := baseRun
		run.LastProgress = now.Add(-12 * time.Minute)
		snapshot := BuildSnapshot(BuildInput{
			Now:       now,
			Config:    cfg,
			Runs:      []store.PipelineRunStatus{run},
			Sessions:  []store.SessionRecord{baseSession},
			Tmux:      []TmuxWindowInfo{baseTmux},
			Processes: []ProcessInfo{baseProcess},
		})

		row := snapshot.Pipelines[0]
		if row.Health != HealthWarn {
			t.Fatalf("pipeline health = %s, want %s", row.Health, HealthWarn)
		}
		if !strings.Contains(row.Suggestion, "idle 12m") {
			t.Fatalf("pipeline warning suggestion = %q", row.Suggestion)
		}
	})

	t.Run("stale", func(t *testing.T) {
		run := baseRun
		run.LastProgress = now.Add(-35 * time.Minute)
		snapshot := BuildSnapshot(BuildInput{
			Now:       now,
			Config:    cfg,
			Runs:      []store.PipelineRunStatus{run},
			Sessions:  []store.SessionRecord{baseSession},
			Tmux:      []TmuxWindowInfo{baseTmux},
			Processes: []ProcessInfo{baseProcess},
		})

		row := snapshot.Pipelines[0]
		if row.Health != HealthStale {
			t.Fatalf("pipeline health = %s, want %s", row.Health, HealthStale)
		}
		if !strings.Contains(row.Suggestion, "cb-123 stale 35m") {
			t.Fatalf("pipeline stale suggestion = %q", row.Suggestion)
		}
		if !strings.Contains(row.Suggestion, "`cobuild reset cb-123`") {
			t.Fatalf("pipeline stale suggestion missing reset: %q", row.Suggestion)
		}
	})

	t.Run("orphan", func(t *testing.T) {
		snapshot := BuildSnapshot(BuildInput{
			Now:       now,
			Config:    cfg,
			Runs:      []store.PipelineRunStatus{baseRun},
			Sessions:  []store.SessionRecord{baseSession},
			Processes: []ProcessInfo{baseProcess},
		})

		row := snapshot.Pipelines[0]
		if row.Health != HealthOrphan {
			t.Fatalf("pipeline health = %s, want %s", row.Health, HealthOrphan)
		}
		if !strings.Contains(row.Suggestion, "running session has no tmux window") {
			t.Fatalf("pipeline orphan suggestion = %q", row.Suggestion)
		}
	})
}

func TestBuildSnapshotPartialSourceFailureDowngradesToWarn(t *testing.T) {
	now := time.Date(2026, 4, 12, 18, 0, 0, 0, time.UTC)

	snapshot := BuildSnapshot(BuildInput{
		Now: now,
		Runs: []store.PipelineRunStatus{{
			DesignID:     "cb-124",
			Project:      "cobuild",
			Phase:        "implement",
			Status:       "active",
			LastProgress: now.Add(-4 * time.Minute),
		}},
		Sessions: []store.SessionRecord{{
			ID:          "ps-2",
			PipelineID:  "pr-2",
			DesignID:    "cb-124",
			TaskID:      "cb-124",
			Phase:       "implement",
			Project:     "cobuild",
			Status:      "running",
			StartedAt:   now.Add(-4 * time.Minute),
			TmuxSession: ptr("cobuild-cobuild"),
			TmuxWindow:  ptr("cb-124"),
		}},
		ProcessErr: errors.New("ps auxww failed"),
		TmuxErr:    errors.New("tmux server not running"),
	})

	if got := len(snapshot.Warnings); got != 2 {
		t.Fatalf("warnings = %d, want 2", got)
	}
	if snapshot.Pipelines[0].Health != HealthWarn {
		t.Fatalf("pipeline health = %s, want %s", snapshot.Pipelines[0].Health, HealthWarn)
	}
	if snapshot.Sessions[0].Health != HealthWarn {
		t.Fatalf("session health = %s, want %s", snapshot.Sessions[0].Health, HealthWarn)
	}
	if !strings.Contains(snapshot.Pipelines[0].Suggestion, "tmux state unavailable") {
		t.Fatalf("pipeline warning suggestion = %q", snapshot.Pipelines[0].Suggestion)
	}
}

func TestBuildSnapshotTmuxAndSessionRowsCarryCrossReferences(t *testing.T) {
	now := time.Date(2026, 4, 12, 18, 0, 0, 0, time.UTC)

	snapshot := BuildSnapshot(BuildInput{
		Now: now,
		Runs: []store.PipelineRunStatus{{
			DesignID:     "cb-125",
			Project:      "cobuild",
			Phase:        "implement",
			Status:       "active",
			LastProgress: now.Add(-11 * time.Minute),
		}},
		Sessions: []store.SessionRecord{{
			ID:          "ps-3",
			PipelineID:  "pr-3",
			DesignID:    "cb-125",
			TaskID:      "cb-125",
			Phase:       "implement",
			Project:     "cobuild",
			Status:      "running",
			StartedAt:   now.Add(-15 * time.Minute),
			TmuxSession: ptr("cobuild-cobuild"),
			TmuxWindow:  ptr("cb-125"),
		}},
		Tmux: []TmuxWindowInfo{{
			SessionName: "cobuild-cobuild",
			WindowName:  "cb-125",
			Target:      "cobuild-cobuild:cb-125",
			Project:     "cobuild",
			DesignID:    "cb-125",
			TaskID:      "cb-125",
		}},
		Processes: []ProcessInfo{{
			PID:       5150,
			Kind:      "orchestrate",
			Project:   "cobuild",
			DesignID:  "cb-125",
			TaskID:    "cb-125",
			StartedAt: now.Add(-15 * time.Minute),
		}},
	})

	session := snapshot.Sessions[0]
	if session.TmuxTarget != "cobuild-cobuild:cb-125" {
		t.Fatalf("session tmux target = %q", session.TmuxTarget)
	}
	if session.OrchestratePID != 5150 {
		t.Fatalf("session pid = %d, want 5150", session.OrchestratePID)
	}
	if session.Health != HealthWarn {
		t.Fatalf("session health = %s, want %s", session.Health, HealthWarn)
	}

	window := snapshot.Tmux[0]
	if window.SessionID != "ps-3" {
		t.Fatalf("tmux session id = %q, want ps-3", window.SessionID)
	}
	if window.OrchestratePID != 5150 {
		t.Fatalf("tmux pid = %d, want 5150", window.OrchestratePID)
	}
	if window.Health != HealthWarn {
		t.Fatalf("tmux health = %s, want %s", window.Health, HealthWarn)
	}
}

func TestSnapshotJSONSchemaIncludesTopLevelCollections(t *testing.T) {
	now := time.Date(2026, 4, 12, 18, 0, 0, 0, time.UTC)
	snapshot := BuildSnapshot(BuildInput{Now: now})

	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	for _, key := range []string{"generated_at", "processes", "pipelines", "tmux", "sessions"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("missing top-level key %q in %s", key, string(raw))
		}
	}
}

func ptr(s string) *string { return &s }
