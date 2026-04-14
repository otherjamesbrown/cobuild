package cmd

import (
	"context"
	"fmt"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

type stubWaveStore struct {
	waveTasks map[int][]store.PipelineTaskRecord
}

// errWaveStoreNotUsed reports that a stubWaveStore method was called by
// a test that wasn't expected to exercise it. Returning an error instead
// of panicking keeps a leaked stub from tearing down the test binary
// (cb-6d598a).
func errWaveStoreNotUsed(method string) error {
	return fmt.Errorf("stubWaveStore.%s: not used in this test", method)
}

func (s stubWaveStore) CreateRun(context.Context, string, string, string) (*store.PipelineRun, error) {
	return nil, errWaveStoreNotUsed("CreateRun")
}
func (s stubWaveStore) CreateRunWithMode(context.Context, string, string, string, string) (*store.PipelineRun, error) {
	return nil, errWaveStoreNotUsed("CreateRunWithMode")
}
func (s stubWaveStore) GetRun(context.Context, string) (*store.PipelineRun, error) {
	return nil, errWaveStoreNotUsed("GetRun")
}
func (s stubWaveStore) ListRuns(context.Context, string) ([]store.PipelineRunStatus, error) {
	return nil, errWaveStoreNotUsed("ListRuns")
}
func (s stubWaveStore) UpdateRunPhase(context.Context, string, string) error {
	return errWaveStoreNotUsed("UpdateRunPhase")
}
func (s stubWaveStore) AdvancePhase(context.Context, string, string, string) error {
	return errWaveStoreNotUsed("AdvancePhase")
}
func (s stubWaveStore) CancelRunningSessions(context.Context, string) (int, error) {
	return 0, errWaveStoreNotUsed("CancelRunningSessions")
}
func (s stubWaveStore) CancelRunningSessionsForShard(context.Context, string) (int, error) {
	return 0, errWaveStoreNotUsed("CancelRunningSessionsForShard")
}
func (s stubWaveStore) MarkSessionEarlyDeath(context.Context, string, string) error {
	return errWaveStoreNotUsed("MarkSessionEarlyDeath")
}
func (s stubWaveStore) UpdateRunStatus(context.Context, string, string) error {
	return errWaveStoreNotUsed("UpdateRunStatus")
}
func (s stubWaveStore) SetRunMode(context.Context, string, string) error {
	return errWaveStoreNotUsed("SetRunMode")
}
func (s stubWaveStore) ResetRun(context.Context, string, string) error {
	return errWaveStoreNotUsed("ResetRun")
}
func (s stubWaveStore) RecordGate(context.Context, store.PipelineGateInput) (*store.PipelineGateRecord, error) {
	return nil, errWaveStoreNotUsed("RecordGate")
}
func (s stubWaveStore) GetGateHistory(context.Context, string) ([]store.PipelineGateRecord, error) {
	return nil, errWaveStoreNotUsed("GetGateHistory")
}
func (s stubWaveStore) GetLatestGateRound(context.Context, string, string) (int, error) {
	return 0, errWaveStoreNotUsed("GetLatestGateRound")
}
func (s stubWaveStore) AddTask(context.Context, string, string, string, *int) error {
	return errWaveStoreNotUsed("AddTask")
}
func (s stubWaveStore) ListTasks(context.Context, string) ([]store.PipelineTaskRecord, error) {
	return nil, errWaveStoreNotUsed("ListTasks")
}
func (s stubWaveStore) ListTasksByDesign(context.Context, string) ([]store.PipelineTaskRecord, error) {
	return nil, errWaveStoreNotUsed("ListTasksByDesign")
}
func (s stubWaveStore) GetTaskByShardID(context.Context, string) (*store.PipelineTaskRecord, error) {
	return nil, errWaveStoreNotUsed("GetTaskByShardID")
}
func (s stubWaveStore) GetTasksByWave(_ context.Context, _ string, wave int) ([]store.PipelineTaskRecord, error) {
	return s.waveTasks[wave], nil
}
func (s stubWaveStore) UpdateTaskStatus(context.Context, string, string) error {
	return errWaveStoreNotUsed("UpdateTaskStatus")
}
func (s stubWaveStore) CreateSession(context.Context, store.SessionInput) (*store.SessionRecord, error) {
	return nil, errWaveStoreNotUsed("CreateSession")
}
func (s stubWaveStore) EndSession(context.Context, string, store.SessionResult) error {
	return errWaveStoreNotUsed("EndSession")
}
func (s stubWaveStore) GetSession(context.Context, string) (*store.SessionRecord, error) {
	return nil, errWaveStoreNotUsed("GetSession")
}
func (s stubWaveStore) ListSessions(context.Context, string) ([]store.SessionRecord, error) {
	return nil, errWaveStoreNotUsed("ListSessions")
}
func (s stubWaveStore) ListRunningSessions(context.Context, string) ([]store.SessionRecord, error) {
	return nil, errWaveStoreNotUsed("ListRunningSessions")
}
func (s stubWaveStore) GetRunStatusCounts(context.Context, string) (map[string]int, error) {
	return nil, errWaveStoreNotUsed("GetRunStatusCounts")
}
func (s stubWaveStore) GetTaskStatusCounts(context.Context, string) (map[string]int, error) {
	return nil, errWaveStoreNotUsed("GetTaskStatusCounts")
}
func (s stubWaveStore) GetGatePassRates(context.Context, string) ([]store.GatePassRate, error) {
	return nil, errWaveStoreNotUsed("GetGatePassRates")
}
func (s stubWaveStore) GetGateFailures(context.Context, string) ([]store.PipelineGateRecord, error) {
	return nil, errWaveStoreNotUsed("GetGateFailures")
}
func (s stubWaveStore) GetAvgTaskDuration(context.Context, string) (*float64, error) {
	return nil, errWaveStoreNotUsed("GetAvgTaskDuration")
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
func (s stubWaveStore) Migrate(context.Context) error { return errWaveStoreNotUsed("Migrate") }
func (s stubWaveStore) Close() error                  { return errWaveStoreNotUsed("Close") }

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
