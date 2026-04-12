package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestRunDoneReturnsSuccess(t *testing.T) {
	source := &fakePhaseSource{phases: []string{"done"}}
	dispatcher := &fakeDispatcher{}
	logs := &capturedEvents{}
	runner := NewRunner(source, dispatcher, Options{
		Now:      fixedClock(),
		OnEvent:  logs.append,
		Sleep:    immediateSleep,
		Reviewer: ReviewProcessFunc(func(context.Context, string) (ReviewResult, error) { return ReviewResult{}, nil }),
	})

	err := runner.Run(context.Background(), "cb-done")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if dispatcher.calls != 0 {
		t.Fatalf("dispatch calls = %d, want 0", dispatcher.calls)
	}
	if len(logs.events) != 1 || logs.events[0].Kind != EventTerminal {
		t.Fatalf("terminal event = %+v, want single terminal event", logs.events)
	}
	if got := FormatEvent(logs.events[0]); !strings.HasPrefix(got, "[12:34:56] ") {
		t.Fatalf("FormatEvent() = %q, want timestamp prefix", got)
	}
}

func TestRunDeployReturnsSentinel(t *testing.T) {
	source := &fakePhaseSource{phases: []string{"deploy"}}
	dispatcher := &fakeDispatcher{}
	runner := NewRunner(source, dispatcher, Options{
		Now:      fixedClock(),
		Sleep:    immediateSleep,
		Reviewer: ReviewProcessFunc(func(context.Context, string) (ReviewResult, error) { return ReviewResult{}, nil }),
	})

	err := runner.Run(context.Background(), "cb-deploy")
	if err == nil {
		t.Fatalf("Run() error = nil, want deploy error")
	}
	if !errors.Is(err, ErrDeployApprovalRequired) {
		t.Fatalf("Run() error = %v, want ErrDeployApprovalRequired", err)
	}
	var deployErr *DeployRequiredError
	if !errors.As(err, &deployErr) {
		t.Fatalf("Run() error = %v, want DeployRequiredError", err)
	}
	if deployErr.ShardID != "cb-deploy" || deployErr.Phase != "deploy" {
		t.Fatalf("deploy error = %+v, want shard/phase populated", deployErr)
	}
}

func TestRunDispatchesAndWaitsForPhaseAdvance(t *testing.T) {
	source := &fakePhaseSource{phases: []string{"design", "design", "decompose", "decompose", "done"}}
	dispatcher := &fakeDispatcher{}
	logs := &capturedEvents{}
	clock := &fakeClock{current: time.Date(2026, 4, 12, 18, 45, 1, 0, time.UTC)}
	runner := NewRunner(source, dispatcher, Options{
		PollInterval: time.Second,
		PhaseTimeout: time.Minute,
		Now:          clock.Now,
		Sleep:        clock.Sleep,
		OnEvent:      logs.append,
		Reviewer:     ReviewProcessFunc(func(context.Context, string) (ReviewResult, error) { return ReviewResult{}, nil }),
	})

	err := runner.Run(context.Background(), "cb-design")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if dispatcher.calls != 2 {
		t.Fatalf("dispatch calls = %d, want 2", dispatcher.calls)
	}

	wantMessages := []string{
		"Phase: design -> dispatching",
		"Phase: design -> still waiting",
		"Phase: design -> decompose",
		"Phase: decompose -> dispatching",
		"Phase: decompose -> done",
		"Pipeline complete.",
	}
	if len(logs.events) != len(wantMessages) {
		t.Fatalf("events = %d, want %d", len(logs.events), len(wantMessages))
	}
	for i, want := range wantMessages {
		if logs.events[i].Message != want {
			t.Fatalf("event[%d] = %q, want %q", i, logs.events[i].Message, want)
		}
		if got := FormatEvent(logs.events[i]); !strings.HasPrefix(got, "[") {
			t.Fatalf("formatted event = %q, want timestamped output", got)
		}
	}
}

func TestRunTimeoutIncludesShardAndPhase(t *testing.T) {
	source := &fakePhaseSource{phases: []string{"fix", "fix", "fix", "fix"}}
	dispatcher := &fakeDispatcher{}
	clock := &fakeClock{current: time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)}
	runner := NewRunner(source, dispatcher, Options{
		PollInterval: time.Second,
		PhaseTimeout: 2 * time.Second,
		Now:          clock.Now,
		Sleep:        clock.Sleep,
		Reviewer:     ReviewProcessFunc(func(context.Context, string) (ReviewResult, error) { return ReviewResult{}, nil }),
	})

	err := runner.Run(context.Background(), "cb-timeout")
	if err == nil {
		t.Fatalf("Run() error = nil, want timeout")
	}
	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Run() error = %v, want TimeoutError", err)
	}
	if timeoutErr.ShardID != "cb-timeout" || timeoutErr.Phase != "fix" {
		t.Fatalf("timeout error = %+v, want shard/phase populated", timeoutErr)
	}
}

func TestRunUnknownPhaseIncludesShardAndPhase(t *testing.T) {
	source := &fakePhaseSource{phases: []string{"mystery"}}
	dispatcher := &fakeDispatcher{}
	runner := NewRunner(source, dispatcher, Options{
		Now:   fixedClock(),
		Sleep: immediateSleep,
	})

	err := runner.Run(context.Background(), "cb-review")
	if err == nil {
		t.Fatalf("Run() error = nil, want unknown phase error")
	}
	var phaseErr *UnknownPhaseError
	if !errors.As(err, &phaseErr) {
		t.Fatalf("Run() error = %v, want UnknownPhaseError", err)
	}
	if phaseErr.ShardID != "cb-review" || phaseErr.Phase != "mystery" {
		t.Fatalf("unknown phase error = %+v, want shard/phase populated", phaseErr)
	}
}

func TestRunStepModeCallsHook(t *testing.T) {
	source := &fakePhaseSource{phases: []string{"fix", "done"}}
	dispatcher := &fakeDispatcher{}
	var calls []string
	runner := NewRunner(source, dispatcher, Options{
		StepMode: true,
		BeforeStep: func(_ context.Context, shardID, phase string) error {
			calls = append(calls, shardID+":"+phase)
			return nil
		},
		Now:      fixedClock(),
		Sleep:    immediateSleep,
		Reviewer: ReviewProcessFunc(func(context.Context, string) (ReviewResult, error) { return ReviewResult{}, nil }),
	})

	err := runner.Run(context.Background(), "cb-step")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(calls) != 1 || calls[0] != "cb-step:fix" {
		t.Fatalf("step hook calls = %v, want [cb-step:fix]", calls)
	}
}

func TestRunImplementDispatchesReviewsAndLaterWavePickup(t *testing.T) {
	source := &fakePhaseSource{phases: []string{"implement", "implement", "implement", "implement", "done", "done"}}
	dispatcher := &fakeDispatcher{}
	waves := &fakeWaveDispatcher{}
	reviewer := &fakeReviewer{
		results: map[string][]ReviewResult{
			"cb-task-a": {{Outcome: "merged"}},
		},
	}
	tasks := &fakeTaskSource{
		snapshots: [][]store.PipelineTaskRecord{
			{
				{TaskShardID: "cb-task-a", Status: "in_progress"},
				{TaskShardID: "cb-task-b", Status: "pending"},
			},
			{
				{TaskShardID: "cb-task-a", Status: "needs-review"},
				{TaskShardID: "cb-task-b", Status: "pending"},
			},
			{
				{TaskShardID: "cb-task-a", Status: "closed"},
				{TaskShardID: "cb-task-b", Status: "in_progress"},
			},
		},
	}
	logs := &capturedEvents{}
	clock := &fakeClock{current: time.Date(2026, 4, 12, 18, 55, 7, 0, time.UTC)}
	runner := NewRunner(source, dispatcher, Options{
		PollInterval:   time.Second,
		PhaseTimeout:   time.Minute,
		Now:            clock.Now,
		Sleep:          clock.Sleep,
		OnEvent:        logs.append,
		Tasks:          tasks,
		WaveDispatcher: waves,
		Reviewer:       reviewer,
	})

	if err := runner.Run(context.Background(), "cb-design"); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if dispatcher.calls != 0 {
		t.Fatalf("dispatch calls = %d, want 0 during implement loop", dispatcher.calls)
	}
	if waves.calls != 2 {
		t.Fatalf("wave dispatch calls = %d, want 2", waves.calls)
	}
	if got := reviewer.calls; len(got) != 1 || got[0] != "cb-task-a" {
		t.Fatalf("review calls = %v, want [cb-task-a]", got)
	}

	wantMessages := []string{
		"Phase: implement -> dispatching wave",
		"Phase: implement -> still waiting on cb-task-a, cb-task-b",
		"process-review cb-task-a -> merged",
		"Phase: implement -> dispatching wave",
		"Phase: implement -> still waiting on cb-task-b",
		"Phase: implement -> done",
		"Pipeline complete.",
	}
	assertMessages(t, logs.events, wantMessages)
}

func TestRunImplementResumesMidFlightWithoutRepeatedDispatch(t *testing.T) {
	source := &fakePhaseSource{phases: []string{"implement", "implement", "implement", "implement", "done", "done"}}
	dispatcher := &fakeDispatcher{}
	waves := &fakeWaveDispatcher{}
	reviewer := &fakeReviewer{}
	tasks := &fakeTaskSource{
		snapshots: [][]store.PipelineTaskRecord{
			{{TaskShardID: "cb-task-a", Status: "in_progress"}},
			{{TaskShardID: "cb-task-a", Status: "in_progress"}},
		},
	}
	logs := &capturedEvents{}
	clock := &fakeClock{current: time.Date(2026, 4, 12, 19, 0, 0, 0, time.UTC)}
	runner := NewRunner(source, dispatcher, Options{
		PollInterval:   time.Second,
		PhaseTimeout:   time.Minute,
		Now:            clock.Now,
		Sleep:          clock.Sleep,
		OnEvent:        logs.append,
		Tasks:          tasks,
		WaveDispatcher: waves,
		Reviewer:       reviewer,
	})

	if err := runner.Run(context.Background(), "cb-resume"); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if waves.calls != 1 {
		t.Fatalf("wave dispatch calls = %d, want 1", waves.calls)
	}
	if len(reviewer.calls) != 0 {
		t.Fatalf("review calls = %v, want none", reviewer.calls)
	}
	wantMessages := []string{
		"Phase: implement -> dispatching wave",
		"Phase: implement -> still waiting on cb-task-a",
		"Phase: implement -> still waiting on cb-task-a",
		"Phase: implement -> still waiting on cb-task-a",
		"Phase: implement -> done",
		"Pipeline complete.",
	}
	assertMessages(t, logs.events, wantMessages)
}

func TestRunReviewProcessesPipelineItem(t *testing.T) {
	source := &fakePhaseSource{phases: []string{"review", "review", "done", "done"}}
	dispatcher := &fakeDispatcher{}
	reviewer := &fakeReviewer{
		results: map[string][]ReviewResult{
			"cb-review": {
				{Outcome: "waiting"},
				{Outcome: "merged"},
			},
		},
	}
	logs := &capturedEvents{}
	clock := &fakeClock{current: time.Date(2026, 4, 12, 19, 5, 0, 0, time.UTC)}
	runner := NewRunner(source, dispatcher, Options{
		PollInterval: time.Second,
		PhaseTimeout: time.Minute,
		Now:          clock.Now,
		Sleep:        clock.Sleep,
		OnEvent:      logs.append,
		Reviewer:     reviewer,
	})

	if err := runner.Run(context.Background(), "cb-review"); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if got := reviewer.calls; len(got) != 2 || got[0] != "cb-review" || got[1] != "cb-review" {
		t.Fatalf("review calls = %v, want two review attempts for cb-review", got)
	}
	wantMessages := []string{
		"process-review cb-review -> waiting",
		"Phase: review -> still waiting on cb-review",
		"process-review cb-review -> merged",
		"Phase: review -> done",
		"Pipeline complete.",
	}
	assertMessages(t, logs.events, wantMessages)
}

type fakePhaseSource struct {
	phases []string
	index  int
}

func (f *fakePhaseSource) CurrentPhase(_ context.Context, _ string) (string, error) {
	if len(f.phases) == 0 {
		return "", nil
	}
	if f.index >= len(f.phases) {
		return f.phases[len(f.phases)-1], nil
	}
	phase := f.phases[f.index]
	f.index++
	return phase, nil
}

type fakeDispatcher struct {
	calls int
}

func (f *fakeDispatcher) Dispatch(_ context.Context, _ string) error {
	f.calls++
	return nil
}

type capturedEvents struct {
	events []Event
}

func (c *capturedEvents) append(event Event) {
	c.events = append(c.events, event)
}

type fakeClock struct {
	current time.Time
}

func (f *fakeClock) Now() time.Time {
	return f.current
}

func (f *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	f.current = f.current.Add(d)
	return nil
}

func fixedClock() func() time.Time {
	now := time.Date(2026, 4, 12, 12, 34, 56, 0, time.UTC)
	return func() time.Time { return now }
}

func immediateSleep(_ context.Context, _ time.Duration) error {
	return nil
}

type fakeTaskSource struct {
	snapshots [][]store.PipelineTaskRecord
	index     int
}

func (f *fakeTaskSource) ListTasksByDesign(_ context.Context, _ string) ([]store.PipelineTaskRecord, error) {
	if len(f.snapshots) == 0 {
		return nil, nil
	}
	if f.index >= len(f.snapshots) {
		return f.snapshots[len(f.snapshots)-1], nil
	}
	tasks := f.snapshots[f.index]
	f.index++
	return tasks, nil
}

type fakeWaveDispatcher struct {
	calls int
}

func (f *fakeWaveDispatcher) DispatchWave(_ context.Context, _ string) error {
	f.calls++
	return nil
}

type fakeReviewer struct {
	results map[string][]ReviewResult
	calls   []string
}

func (f *fakeReviewer) ProcessReview(_ context.Context, shardID string) (ReviewResult, error) {
	f.calls = append(f.calls, shardID)
	if len(f.results[shardID]) == 0 {
		return ReviewResult{Outcome: "merged"}, nil
	}
	result := f.results[shardID][0]
	f.results[shardID] = f.results[shardID][1:]
	return result, nil
}

func assertMessages(t *testing.T, events []Event, want []string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("events = %d, want %d", len(events), len(want))
	}
	for i, message := range want {
		if events[i].Message != message {
			t.Fatalf("event[%d] = %q, want %q", i, events[i].Message, message)
		}
	}
}
