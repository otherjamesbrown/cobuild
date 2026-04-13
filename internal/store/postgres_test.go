package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/testutil/pgtest"
)

func TestPostgresStoreGetTasksByWave(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pg := pgtest.New(t, ctx)
	s := pg.Store
	designID := fmt.Sprintf("cb-store-wave-%d", time.Now().UnixNano())
	otherDesignID := designID + "-other"
	run := mustCreateRun(t, ctx, s, designID)
	otherRun := mustCreateRun(t, ctx, s, otherDesignID)

	t.Cleanup(func() {
		pg.CleanupDesign(t, ctx, otherDesignID)
		pg.CleanupDesign(t, ctx, designID)
	})

	wave1 := 1
	wave2 := 2
	mustAddTask(t, ctx, s, run.ID, designID+"-task-a", designID, &wave1)
	time.Sleep(5 * time.Millisecond)
	mustAddTask(t, ctx, s, run.ID, designID+"-task-b", designID, &wave1)
	time.Sleep(5 * time.Millisecond)
	mustAddTask(t, ctx, s, run.ID, designID+"-task-c", designID, &wave2)
	mustAddTask(t, ctx, s, otherRun.ID, otherDesignID+"-task-a", otherDesignID, &wave1)

	tasks, err := s.GetTasksByWave(ctx, designID, wave1)
	if err != nil {
		t.Fatalf("GetTasksByWave() error = %v", err)
	}

	if len(tasks) != 2 {
		t.Fatalf("GetTasksByWave() len = %d, want 2", len(tasks))
	}
	if tasks[0].TaskShardID != designID+"-task-a" || tasks[1].TaskShardID != designID+"-task-b" {
		t.Fatalf("GetTasksByWave() order = [%s %s], want [%s %s]",
			tasks[0].TaskShardID, tasks[1].TaskShardID,
			designID+"-task-a", designID+"-task-b")
	}
	for _, task := range tasks {
		if task.DesignID != designID {
			t.Fatalf("GetTasksByWave() returned task for design %s, want %s", task.DesignID, designID)
		}
		if task.Wave == nil || *task.Wave != wave1 {
			t.Fatalf("GetTasksByWave() returned wave %v, want %d", task.Wave, wave1)
		}
	}
}

func TestPostgresStoreIsWaveClosed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pg := pgtest.New(t, ctx)
	s := pg.Store
	designID := fmt.Sprintf("cb-store-closed-%d", time.Now().UnixNano())
	run := mustCreateRun(t, ctx, s, designID)

	t.Cleanup(func() {
		pg.CleanupDesign(t, ctx, designID)
	})

	wave1 := 1
	wave2 := 2
	taskA := designID + "-task-a"
	taskB := designID + "-task-b"

	mustAddTask(t, ctx, s, run.ID, taskA, designID, &wave1)
	mustAddTask(t, ctx, s, run.ID, taskB, designID, &wave1)
	mustAddTask(t, ctx, s, run.ID, designID+"-task-c", designID, &wave2)

	closed, err := s.IsWaveClosed(ctx, designID, wave1)
	if err != nil {
		t.Fatalf("IsWaveClosed() error = %v", err)
	}
	if closed {
		t.Fatal("IsWaveClosed() = true, want false while wave has pending tasks")
	}

	if err := s.UpdateTaskStatus(ctx, taskA, "completed"); err != nil {
		t.Fatalf("UpdateTaskStatus(%q) error = %v", taskA, err)
	}
	closed, err = s.IsWaveClosed(ctx, designID, wave1)
	if err != nil {
		t.Fatalf("IsWaveClosed() after closing one task error = %v", err)
	}
	if closed {
		t.Fatal("IsWaveClosed() = true, want false until every task in the wave is closed")
	}

	if err := s.UpdateTaskStatus(ctx, taskB, "completed"); err != nil {
		t.Fatalf("UpdateTaskStatus(%q) error = %v", taskB, err)
	}
	closed, err = s.IsWaveClosed(ctx, designID, wave1)
	if err != nil {
		t.Fatalf("IsWaveClosed() after closing all tasks error = %v", err)
	}
	if !closed {
		t.Fatal("IsWaveClosed() = false, want true when every task in the wave is closed")
	}

	closed, err = s.IsWaveClosed(ctx, designID, 99)
	if err != nil {
		t.Fatalf("IsWaveClosed() for missing wave error = %v", err)
	}
	if closed {
		t.Fatal("IsWaveClosed() = true, want false when the wave has no tasks")
	}
}

func TestPostgresStoreAdvancePhase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pg := pgtest.New(t, ctx)
	s := pg.Store
	designID := fmt.Sprintf("cb-store-advance-%d", time.Now().UnixNano())

	if _, err := s.CreateRun(ctx, designID, "cobuild-test", "design"); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	t.Cleanup(func() {
		pg.CleanupDesign(t, ctx, designID)
	})

	if err := s.AdvancePhase(ctx, designID, "design", "decompose"); err != nil {
		t.Fatalf("AdvancePhase(design→decompose) error = %v", err)
	}
	got, err := s.GetRun(ctx, designID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if got.CurrentPhase != "decompose" {
		t.Fatalf("phase after advance = %q, want decompose", got.CurrentPhase)
	}

	err = s.AdvancePhase(ctx, designID, "design", "implement")
	if err == nil {
		t.Fatal("AdvancePhase(stale) error = nil, want ErrPhaseConflict")
	}
	if !strings.Contains(err.Error(), "phase conflict") {
		t.Fatalf("AdvancePhase(stale) error = %v, want phase conflict", err)
	}

	if err := s.AdvancePhase(ctx, designID, "decompose", "implement"); err != nil {
		t.Fatalf("AdvancePhase(decompose→implement) error = %v", err)
	}

	err = s.AdvancePhase(ctx, "cb-nonexistent", "design", "decompose")
	if err == nil {
		t.Fatal("AdvancePhase(nonexistent) error = nil, want error")
	}
}

func mustAddTask(t *testing.T, ctx context.Context, s *store.PostgresStore, pipelineID, taskShardID, designID string, wave *int) {
	t.Helper()

	if err := s.AddTask(ctx, pipelineID, taskShardID, designID, wave); err != nil {
		t.Fatalf("AddTask(%q) error = %v", taskShardID, err)
	}
}

func mustCreateRun(t *testing.T, ctx context.Context, s *store.PostgresStore, designID string) *store.PipelineRun {
	t.Helper()

	run, err := s.CreateRun(ctx, designID, "cobuild-test", "implement")
	if err != nil {
		t.Fatalf("CreateRun(%q) error = %v", designID, err)
	}
	return run
}
