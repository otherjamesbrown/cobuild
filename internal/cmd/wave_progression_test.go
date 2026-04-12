package cmd

import (
	"context"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

type stubWaveStore struct {
	waveTasks map[int][]store.PipelineTaskRecord
}

func (s stubWaveStore) CreateRun(context.Context, string, string, string) (*store.PipelineRun, error) {
	panic("not used")
}
func (s stubWaveStore) CreateRunWithMode(context.Context, string, string, string, string) (*store.PipelineRun, error) {
	panic("not used")
}
func (s stubWaveStore) GetRun(context.Context, string) (*store.PipelineRun, error) { panic("not used") }
func (s stubWaveStore) ListRuns(context.Context, string) ([]store.PipelineRunStatus, error) {
	panic("not used")
}
func (s stubWaveStore) UpdateRunPhase(context.Context, string, string) error  { panic("not used") }
func (s stubWaveStore) UpdateRunStatus(context.Context, string, string) error { panic("not used") }
func (s stubWaveStore) SetRunMode(context.Context, string, string) error      { panic("not used") }
func (s stubWaveStore) RecordGate(context.Context, store.PipelineGateInput) (*store.PipelineGateRecord, error) {
	panic("not used")
}
func (s stubWaveStore) GetGateHistory(context.Context, string) ([]store.PipelineGateRecord, error) {
	panic("not used")
}
func (s stubWaveStore) GetLatestGateRound(context.Context, string, string) (int, error) {
	panic("not used")
}
func (s stubWaveStore) AddTask(context.Context, string, string, string, *int) error {
	panic("not used")
}
func (s stubWaveStore) ListTasks(context.Context, string) ([]store.PipelineTaskRecord, error) {
	panic("not used")
}
func (s stubWaveStore) ListTasksByDesign(context.Context, string) ([]store.PipelineTaskRecord, error) {
	panic("not used")
}
func (s stubWaveStore) GetTaskByShardID(context.Context, string) (*store.PipelineTaskRecord, error) {
	panic("not used")
}
func (s stubWaveStore) GetTasksByWave(_ context.Context, _ string, wave int) ([]store.PipelineTaskRecord, error) {
	return s.waveTasks[wave], nil
}
func (s stubWaveStore) UpdateTaskStatus(context.Context, string, string) error { panic("not used") }
func (s stubWaveStore) CreateSession(context.Context, store.SessionInput) (*store.SessionRecord, error) {
	panic("not used")
}
func (s stubWaveStore) EndSession(context.Context, string, store.SessionResult) error {
	panic("not used")
}
func (s stubWaveStore) GetSession(context.Context, string) (*store.SessionRecord, error) {
	panic("not used")
}
func (s stubWaveStore) ListSessions(context.Context, string) ([]store.SessionRecord, error) {
	panic("not used")
}
func (s stubWaveStore) ListRunningSessions(context.Context, string) ([]store.SessionRecord, error) {
	panic("not used")
}
func (s stubWaveStore) GetRunStatusCounts(context.Context, string) (map[string]int, error) {
	panic("not used")
}
func (s stubWaveStore) GetTaskStatusCounts(context.Context, string) (map[string]int, error) {
	panic("not used")
}
func (s stubWaveStore) GetGatePassRates(context.Context, string) ([]store.GatePassRate, error) {
	panic("not used")
}
func (s stubWaveStore) GetGateFailures(context.Context, string) ([]store.PipelineGateRecord, error) {
	panic("not used")
}
func (s stubWaveStore) GetAvgTaskDuration(context.Context, string) (*float64, error) {
	panic("not used")
}
func (s stubWaveStore) IsWaveClosed(_ context.Context, _ string, wave int) (bool, error) {
	tasks := s.waveTasks[wave]
	for _, t := range tasks {
		if t.Status != "closed" {
			return false, nil
		}
	}
	return len(tasks) > 0, nil
}
func (s stubWaveStore) Migrate(context.Context) error { panic("not used") }
func (s stubWaveStore) Close() error                  { panic("not used") }

func TestDecidePostCloseActionSerialDispatchesNextWave(t *testing.T) {
	w1 := 1
	w2 := 2
	current := &store.PipelineTaskRecord{TaskShardID: "cb-a", Wave: &w1, Status: "closed"}
	allTasks := []store.PipelineTaskRecord{
		{TaskShardID: "cb-a", Wave: &w1, Status: "closed"},
		{TaskShardID: "cb-b", Wave: &w1, Status: "closed"},
		{TaskShardID: "cb-c", Wave: &w2, Status: "pending"},
	}
	st := stubWaveStore{
		waveTasks: map[int][]store.PipelineTaskRecord{
			1: {
				{TaskShardID: "cb-a", Wave: &w1, Status: "closed"},
				{TaskShardID: "cb-b", Wave: &w1, Status: "closed"},
			},
		},
	}

	decision, err := decidePostCloseAction("serial", current, allTasks, st, context.Background(), "cb-design")
	if err != nil {
		t.Fatalf("decidePostCloseAction returned error: %v", err)
	}
	if decision.Action != postCloseDispatchNextWave {
		t.Fatalf("action = %v, want dispatch next wave", decision.Action)
	}
	if decision.NextWave != 2 {
		t.Fatalf("next wave = %d, want 2", decision.NextWave)
	}
}

func TestDecidePostCloseActionSerialCompletesDesignWhenAllClosed(t *testing.T) {
	w1 := 1
	current := &store.PipelineTaskRecord{TaskShardID: "cb-a", Wave: &w1, Status: "closed"}
	allTasks := []store.PipelineTaskRecord{
		{TaskShardID: "cb-a", Wave: &w1, Status: "closed"},
		{TaskShardID: "cb-b", Wave: &w1, Status: "closed"},
	}
	st := stubWaveStore{
		waveTasks: map[int][]store.PipelineTaskRecord{
			1: allTasks,
		},
	}

	decision, err := decidePostCloseAction("serial", current, allTasks, st, context.Background(), "cb-design")
	if err != nil {
		t.Fatalf("decidePostCloseAction returned error: %v", err)
	}
	if decision.Action != postCloseCompleteDesign {
		t.Fatalf("action = %v, want complete design", decision.Action)
	}
}

func TestDecidePostCloseActionParallelDoesNotDispatchNextWave(t *testing.T) {
	w1 := 1
	w2 := 2
	current := &store.PipelineTaskRecord{TaskShardID: "cb-a", Wave: &w1, Status: "closed"}
	allTasks := []store.PipelineTaskRecord{
		{TaskShardID: "cb-a", Wave: &w1, Status: "closed"},
		{TaskShardID: "cb-b", Wave: &w1, Status: "closed"},
		{TaskShardID: "cb-c", Wave: &w2, Status: "pending"},
	}
	st := stubWaveStore{
		waveTasks: map[int][]store.PipelineTaskRecord{
			1: {
				{TaskShardID: "cb-a", Wave: &w1, Status: "closed"},
				{TaskShardID: "cb-b", Wave: &w1, Status: "closed"},
			},
		},
	}

	decision, err := decidePostCloseAction("parallel", current, allTasks, st, context.Background(), "cb-design")
	if err != nil {
		t.Fatalf("decidePostCloseAction returned error: %v", err)
	}
	if decision.Action != postCloseNoop {
		t.Fatalf("action = %v, want no-op", decision.Action)
	}
}
