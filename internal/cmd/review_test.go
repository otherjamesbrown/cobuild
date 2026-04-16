package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	llmreview "github.com/otherjamesbrown/cobuild/internal/review"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

type reviewFakeConnector struct {
	items          map[string]*connector.WorkItem
	edges          map[string][]connector.Edge
	created        []connector.CreateRequest
	createdEdges   []struct{ fromID, toID, edgeType string }
	statusUpdates  []struct{ id, status string }
	setMetadataErr error
	appendErr      error
	addLabelErr    error
}

func (f *reviewFakeConnector) Name() string { return "fake" }

func (f *reviewFakeConnector) Get(_ context.Context, id string) (*connector.WorkItem, error) {
	item, ok := f.items[id]
	if !ok {
		return nil, fmt.Errorf("item not found: %s", id)
	}
	cp := *item
	if item.Metadata != nil {
		cp.Metadata = map[string]any{}
		for k, v := range item.Metadata {
			cp.Metadata[k] = v
		}
	}
	return &cp, nil
}

func (f *reviewFakeConnector) List(context.Context, connector.ListFilters) (*connector.ListResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *reviewFakeConnector) GetEdges(_ context.Context, id string, direction string, _ []string) ([]connector.Edge, error) {
	key := id + "|" + direction
	edges := append([]connector.Edge(nil), f.edges[key]...)
	for i := range edges {
		if item, ok := f.items[edges[i].ItemID]; ok {
			edges[i].Status = item.Status
			edges[i].Type = item.Type
			edges[i].Title = item.Title
		}
	}
	return edges, nil
}

func (f *reviewFakeConnector) GetMetadata(_ context.Context, id string, key string) (string, error) {
	item, ok := f.items[id]
	if !ok || item.Metadata == nil {
		return "", nil
	}
	if v, ok := item.Metadata[key]; ok && v != nil {
		return fmt.Sprintf("%v", v), nil
	}
	return "", nil
}

func (f *reviewFakeConnector) Create(_ context.Context, req connector.CreateRequest) (string, error) {
	id := fmt.Sprintf("cb-review-%d", len(f.created)+1)
	f.created = append(f.created, req)
	f.items[id] = &connector.WorkItem{
		ID:       id,
		Title:    req.Title,
		Content:  req.Content,
		Type:     req.Type,
		Status:   "closed",
		Labels:   append([]string(nil), req.Labels...),
		Metadata: req.Metadata,
	}
	return id, nil
}

func (f *reviewFakeConnector) UpdateStatus(_ context.Context, id string, status string) error {
	item, ok := f.items[id]
	if !ok {
		return fmt.Errorf("item not found: %s", id)
	}
	item.Status = status
	f.statusUpdates = append(f.statusUpdates, struct{ id, status string }{id: id, status: status})
	return nil
}

func (f *reviewFakeConnector) AppendContent(_ context.Context, id string, content string) error {
	if f.appendErr != nil {
		return f.appendErr
	}
	item, ok := f.items[id]
	if !ok {
		return fmt.Errorf("item not found: %s", id)
	}
	item.Content += content
	return nil
}

func (f *reviewFakeConnector) SetMetadata(_ context.Context, id string, key string, value any) error {
	if f.setMetadataErr != nil {
		return f.setMetadataErr
	}
	item, ok := f.items[id]
	if !ok {
		return fmt.Errorf("item not found: %s", id)
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	item.Metadata[key] = value
	return nil
}

func (f *reviewFakeConnector) UpdateMetadataMap(_ context.Context, id string, patch map[string]any) error {
	item, ok := f.items[id]
	if !ok {
		return fmt.Errorf("item not found: %s", id)
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	for k, v := range patch {
		item.Metadata[k] = v
	}
	return nil
}

func (f *reviewFakeConnector) AddLabel(_ context.Context, id string, label string) error {
	if f.addLabelErr != nil {
		return f.addLabelErr
	}
	item, ok := f.items[id]
	if !ok {
		return fmt.Errorf("item not found: %s", id)
	}
	item.Labels = append(item.Labels, label)
	return nil
}

func (f *reviewFakeConnector) CreateEdge(_ context.Context, fromID string, toID string, edgeType string) error {
	f.createdEdges = append(f.createdEdges, struct{ fromID, toID, edgeType string }{fromID: fromID, toID: toID, edgeType: edgeType})
	return nil
}

type reviewFakeStore struct {
	runs          map[string]*store.PipelineRun
	sessions      map[string]*store.SessionRecord
	gates         []store.PipelineGateInput
	updatePhases  []struct{ designID, phase string }
	updateStatus  []struct{ designID, status string }
	latestGateKey map[string]int
}

func (f *reviewFakeStore) CreateRun(context.Context, string, string, string) (*store.PipelineRun, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *reviewFakeStore) CreateRunWithMode(context.Context, string, string, string, string) (*store.PipelineRun, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *reviewFakeStore) GetRun(_ context.Context, designID string) (*store.PipelineRun, error) {
	run, ok := f.runs[designID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *run
	return &cp, nil
}

func (f *reviewFakeStore) ListRuns(context.Context, string) ([]store.PipelineRunStatus, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *reviewFakeStore) UpdateRunPhase(_ context.Context, designID, phase string) error {
	f.updatePhases = append(f.updatePhases, struct{ designID, phase string }{designID: designID, phase: phase})
	if run, ok := f.runs[designID]; ok {
		run.CurrentPhase = phase
	}
	return nil
}

func (f *reviewFakeStore) CancelRunningSessions(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (f *reviewFakeStore) CancelRunningSessionsForShard(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (f *reviewFakeStore) MarkSessionEarlyDeath(_ context.Context, _, _ string) error {
	return nil
}

func (f *reviewFakeStore) AdvancePhase(_ context.Context, designID, expectedCurrent, nextPhase string) error {
	run, ok := f.runs[designID]
	if !ok {
		return fmt.Errorf("no pipeline run for design %s", designID)
	}
	if run.CurrentPhase != expectedCurrent {
		return fmt.Errorf("expected phase %q but pipeline is in %q: %w", expectedCurrent, run.CurrentPhase, store.ErrPhaseConflict)
	}
	run.CurrentPhase = nextPhase
	f.updatePhases = append(f.updatePhases, struct{ designID, phase string }{designID: designID, phase: nextPhase})
	return nil
}

func (f *reviewFakeStore) UpdateRunStatus(_ context.Context, designID, status string) error {
	f.updateStatus = append(f.updateStatus, struct{ designID, status string }{designID: designID, status: status})
	if run, ok := f.runs[designID]; ok {
		run.Status = status
	}
	return nil
}

func (f *reviewFakeStore) SetRunMode(context.Context, string, string) error { return nil }
func (f *reviewFakeStore) ResetRun(context.Context, string, string) error   { return nil }

func (f *reviewFakeStore) RecordGate(_ context.Context, input store.PipelineGateInput) (*store.PipelineGateRecord, error) {
	f.gates = append(f.gates, input)
	key := input.PipelineID + ":" + input.GateName
	f.latestGateKey[key]++
	round := f.latestGateKey[key]
	return &store.PipelineGateRecord{
		ID:         fmt.Sprintf("gate-%d", len(f.gates)),
		PipelineID: input.PipelineID,
		DesignID:   input.DesignID,
		GateName:   input.GateName,
		Phase:      input.Phase,
		Round:      round,
		Verdict:    input.Verdict,
	}, nil
}

func (f *reviewFakeStore) GetGateHistory(context.Context, string) ([]store.PipelineGateRecord, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *reviewFakeStore) GetLatestGateRound(_ context.Context, pipelineID, gateName string) (int, error) {
	return f.latestGateKey[pipelineID+":"+gateName], nil
}

func (f *reviewFakeStore) GetPreviousGateHash(_ context.Context, _, _ string, _ int) (*string, error) {
	return nil, nil
}

func (f *reviewFakeStore) AddTask(context.Context, string, string, string, *int) error { return nil }
func (f *reviewFakeStore) ListTasks(context.Context, string) ([]store.PipelineTaskRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *reviewFakeStore) ListTasksByDesign(context.Context, string) ([]store.PipelineTaskRecord, error) {
	return nil, nil
}
func (f *reviewFakeStore) GetTaskByShardID(context.Context, string) (*store.PipelineTaskRecord, error) {
	return nil, nil
}
func (f *reviewFakeStore) UpdateTaskStatus(context.Context, string, string) error { return nil }
func (f *reviewFakeStore) UpdateTaskRebaseStatus(context.Context, string, string) error {
	return nil
}
func (f *reviewFakeStore) CreateSession(context.Context, store.SessionInput) (*store.SessionRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *reviewFakeStore) EndSession(context.Context, string, store.SessionResult) error { return nil }
func (f *reviewFakeStore) GetSession(_ context.Context, taskID string) (*store.SessionRecord, error) {
	if f.sessions == nil {
		return nil, store.ErrNotFound
	}
	rec, ok := f.sessions[taskID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *rec
	return &cp, nil
}
func (f *reviewFakeStore) ListSessions(context.Context, string) ([]store.SessionRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *reviewFakeStore) ListRunningSessions(context.Context, string) ([]store.SessionRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *reviewFakeStore) GetRunStatusCounts(context.Context, string) (map[string]int, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *reviewFakeStore) GetTaskStatusCounts(context.Context, string) (map[string]int, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *reviewFakeStore) GetGatePassRates(context.Context, string) ([]store.GatePassRate, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *reviewFakeStore) GetGateFailures(context.Context, string) ([]store.PipelineGateRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *reviewFakeStore) GetAvgTaskDuration(context.Context, string) (*float64, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *reviewFakeStore) GetTasksByWave(_ context.Context, _ string, _ int) ([]store.PipelineTaskRecord, error) {
	return nil, nil
}
func (f *reviewFakeStore) IsWaveClosed(_ context.Context, _ string, _ int) (bool, error) {
	return false, nil
}
func (f *reviewFakeStore) Migrate(context.Context) error { return nil }
func (f *reviewFakeStore) Close() error                  { return nil }

type stubReviewer struct {
	result *llmreview.ReviewResult
	err    error
	inputs []llmreview.ReviewInput
}

func (s *stubReviewer) Review(_ context.Context, input llmreview.ReviewInput) (*llmreview.ReviewResult, error) {
	s.inputs = append(s.inputs, input)
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

func installReviewCommandTestGlobals(t *testing.T, fc *reviewFakeConnector, fs *reviewFakeStore) {
	t.Helper()
	origConn, origStore, origProject := conn, cbStore, projectName
	origOutput, origCombined := execCommandOutput, execCommandCombinedOutput
	origConfigLoader, origFactory := reviewConfigLoader, reviewerFactory

	conn = fc
	cbStore = fs
	projectName = "cobuild"

	restore := func() {
		conn = origConn
		cbStore = origStore
		projectName = origProject
		execCommandOutput = origOutput
		execCommandCombinedOutput = origCombined
		reviewConfigLoader = origConfigLoader
		reviewerFactory = origFactory
	}
	t.Cleanup(restore)
}

func newPRReviewFixture() (*reviewFakeConnector, *reviewFakeStore) {
	fc := &reviewFakeConnector{
		items: map[string]*connector.WorkItem{
			"cb-task": {
				ID:      "cb-task",
				Title:   "Integrate review flow",
				Type:    "task",
				Status:  "needs-review",
				Content: "## Scope\nWire built-in review.\n\n## Acceptance Criteria\n- [ ] Posts review findings\n- [ ] Records LLM gate body\n",
				Metadata: map[string]any{
					"pr_url": "https://github.com/acme/cobuild/pull/42",
				},
			},
			"cb-design": {
				ID:      "cb-design",
				Title:   "Built-in review design",
				Type:    "design",
				Status:  "review",
				Content: "Parent design context",
			},
		},
		edges: map[string][]connector.Edge{
			"cb-task|outgoing": {
				{Direction: "outgoing", EdgeType: "child-of", ItemID: "cb-design"},
			},
			"cb-design|incoming": {
				{Direction: "incoming", EdgeType: "child-of", ItemID: "cb-task"},
			},
		},
	}
	fs := &reviewFakeStore{
		runs: map[string]*store.PipelineRun{
			"cb-task":   {ID: "run-task", DesignID: "cb-task", CurrentPhase: "review", Status: "active", Mode: "manual", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			"cb-design": {ID: "run-design", DesignID: "cb-design", CurrentPhase: "review", Status: "active", Mode: "manual", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		},
		sessions: map[string]*store.SessionRecord{
			"cb-task": {ID: "sess-task", TaskID: "cb-task", Runtime: "codex", Model: strPtr("gpt-5.4"), Status: "completed"},
		},
		latestGateKey: map[string]int{},
	}
	return fc, fs
}

func strPtr(s string) *string { return &s }

func TestProcessReviewHandlesDirectNeedsReviewTaskWithoutPR(t *testing.T) {
	origConn, origStore, origProject := conn, cbStore, projectName
	defer func() {
		conn = origConn
		cbStore = origStore
		projectName = origProject
	}()

	projectName = "cobuild"
	conn = &reviewFakeConnector{
		items: map[string]*connector.WorkItem{
			"cb-task": {
				ID:       "cb-task",
				Title:    "Direct task",
				Type:     "task",
				Status:   "needs-review",
				Metadata: map[string]any{},
			},
			"cb-design": {
				ID:     "cb-design",
				Title:  "Parent design",
				Type:   "design",
				Status: "review",
			},
		},
		edges: map[string][]connector.Edge{
			"cb-task|outgoing": {
				{Direction: "outgoing", EdgeType: "child-of", ItemID: "cb-design"},
			},
			"cb-design|incoming": {
				{Direction: "incoming", EdgeType: "child-of", ItemID: "cb-task"},
			},
		},
	}
	cbStore = &reviewFakeStore{
		runs: map[string]*store.PipelineRun{
			"cb-task":   {ID: "run-task", DesignID: "cb-task", CurrentPhase: "review", Status: "active", Mode: "manual", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			"cb-design": {ID: "run-design", DesignID: "cb-design", CurrentPhase: "review", Status: "active", Mode: "manual", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		},
		latestGateKey: map[string]int{},
	}

	processReviewCmd.Flags().Set("dry-run", "false")
	processReviewCmd.Flags().Set("review-timeout", "10")
	if err := processReviewCmd.RunE(processReviewCmd, []string{"cb-task"}); err != nil {
		t.Fatalf("process-review returned error: %v", err)
	}

	fc := conn.(*reviewFakeConnector)
	fs := cbStore.(*reviewFakeStore)

	if got := fc.items["cb-task"].Status; got != "closed" {
		t.Fatalf("task status = %q, want closed", got)
	}
	if len(fs.gates) != 1 {
		t.Fatalf("recorded %d gates, want 1", len(fs.gates))
	}
	if fs.gates[0].Verdict != "pass" {
		t.Fatalf("gate verdict = %q, want pass", fs.gates[0].Verdict)
	}
	if fs.gates[0].Body == nil || *fs.gates[0].Body != directReviewPassBody {
		t.Fatalf("gate body = %v, want %q", fs.gates[0].Body, directReviewPassBody)
	}
	if len(fc.created) != 1 || fc.created[0].Type != "review" {
		t.Fatalf("created review items = %d, want 1 synthetic review shard", len(fc.created))
	}
	if len(fc.statusUpdates) != 3 ||
		fc.statusUpdates[0].id != "cb-task" || fc.statusUpdates[0].status != "closed" ||
		fc.statusUpdates[1].id != "cb-task" || fc.statusUpdates[1].status != "closed" ||
		fc.statusUpdates[2].id != "cb-design" || fc.statusUpdates[2].status != "closed" {
		t.Fatalf("status updates = %+v, want cb-task -> closed twice then cb-design -> closed", fc.statusUpdates)
	}
	foundDesignDone := false
	for _, upd := range fs.updatePhases {
		if upd.designID == "cb-design" && upd.phase == "done" {
			foundDesignDone = true
		}
	}
	if !foundDesignDone {
		t.Fatalf("expected parent design phase to advance to done, updates = %+v", fs.updatePhases)
	}
}

func TestProcessReviewClosedDirectTaskIsIdempotent(t *testing.T) {
	origConn, origStore, origProject := conn, cbStore, projectName
	defer func() {
		conn = origConn
		cbStore = origStore
		projectName = origProject
	}()

	projectName = "cobuild"
	conn = &reviewFakeConnector{
		items: map[string]*connector.WorkItem{
			"cb-task": {
				ID:       "cb-task",
				Title:    "Closed direct task",
				Type:     "task",
				Status:   "closed",
				Metadata: map[string]any{},
			},
			"cb-design": {
				ID:     "cb-design",
				Title:  "Parent design",
				Type:   "design",
				Status: "review",
			},
		},
		edges: map[string][]connector.Edge{
			"cb-task|outgoing": {
				{Direction: "outgoing", EdgeType: "child-of", ItemID: "cb-design"},
			},
			"cb-design|incoming": {
				{Direction: "incoming", EdgeType: "child-of", ItemID: "cb-task"},
			},
		},
	}
	cbStore = &reviewFakeStore{
		runs: map[string]*store.PipelineRun{
			"cb-design": {ID: "run-design", DesignID: "cb-design", CurrentPhase: "review", Status: "active", Mode: "manual", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		},
		latestGateKey: map[string]int{},
	}

	processReviewCmd.Flags().Set("dry-run", "false")
	processReviewCmd.Flags().Set("review-timeout", "10")
	if err := processReviewCmd.RunE(processReviewCmd, []string{"cb-task"}); err != nil {
		t.Fatalf("process-review returned error: %v", err)
	}

	fc := conn.(*reviewFakeConnector)
	fs := cbStore.(*reviewFakeStore)

	if len(fs.gates) != 0 {
		t.Fatalf("recorded %d gates, want 0 for idempotent closed task", len(fs.gates))
	}
	if len(fc.created) != 0 {
		t.Fatalf("created %d review items, want 0 for idempotent closed task", len(fc.created))
	}
	if len(fc.statusUpdates) != 1 || fc.statusUpdates[0].id != "cb-design" || fc.statusUpdates[0].status != "closed" {
		t.Fatalf("status updates = %+v, want cb-design -> closed", fc.statusUpdates)
	}
	foundDesignDone := false
	for _, upd := range fs.updatePhases {
		if upd.designID == "cb-design" && upd.phase == "done" {
			foundDesignDone = true
		}
	}
	if !foundDesignDone {
		t.Fatalf("expected parent design phase to advance to done, updates = %+v", fs.updatePhases)
	}
}

func TestProcessReviewBuiltInSuccessPostsCommentAndRecordsLLMBody(t *testing.T) {
	fc, fs := newPRReviewFixture()
	installReviewCommandTestGlobals(t, fc, fs)

	cfg := config.DefaultConfig()
	cfg.Review.Provider = "auto"
	// Opt the test into the builtin path; the default (dispatched) would
	// refuse on this test's task status and no longer falls through since
	// cb-6f9ed6 (silent fall-through was creating spurious fail gates).
	cfg.Review.Mode = "builtin"
	postTrue := true
	waitTrue := true
	cfg.Review.PostComments = &postTrue
	cfg.Review.WaitForCI = &waitTrue
	reviewConfigLoader = func() *config.Config { return cfg }

	reviewer := &stubReviewer{
		result: &llmreview.ReviewResult{
			Verdict: "request-changes",
			Summary: "Misses PR comment posting.",
			Findings: []llmreview.Finding{
				{File: "internal/cmd/review.go", Line: 123, Severity: "critical", Body: "Post the comment before recording the gate."},
			},
		},
	}
	reviewerFactory = func(provider string, cfg llmreview.ProviderConfig) (llmreview.Reviewer, error) {
		if provider != "claude" {
			t.Fatalf("provider = %q, want claude", provider)
		}
		return reviewer, nil
	}

	var combinedCalls [][]string
	execCommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "gh pr view https://github.com/acme/cobuild/pull/42 --json state --jq .state":
			return []byte("OPEN\n"), nil
		case "gh pr diff 42 --repo acme/cobuild":
			return []byte("diff --git a/internal/cmd/review.go b/internal/cmd/review.go\n"), nil
		case "gh pr view 42 --repo acme/cobuild --json headRefOid --jq .headRefOid":
			return []byte("abc123\n"), nil
		case "gh api repos/acme/cobuild/commits/abc123/check-runs --jq .check_runs":
			return []byte(`[{"name":"test","status":"completed","conclusion":"success"}]`), nil
		default:
			t.Fatalf("unexpected Output command: %s", key)
			return nil, nil
		}
	}
	execCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		combinedCalls = append(combinedCalls, append([]string{name}, args...))
		key := name + " " + strings.Join(args, " ")
		switch {
		case strings.HasPrefix(key, "gh pr comment https://github.com/acme/cobuild/pull/42 --body "):
			return []byte("commented\n"), nil
		case key == "cobuild dispatch cb-task":
			return []byte("dispatched\n"), nil
		default:
			t.Fatalf("unexpected CombinedOutput command: %s", key)
			return nil, nil
		}
	}

	processReviewCmd.Flags().Set("dry-run", "false")
	processReviewCmd.Flags().Set("review-timeout", "10")
	if err := processReviewCmd.RunE(processReviewCmd, []string{"cb-task"}); err != nil {
		t.Fatalf("process-review returned error: %v", err)
	}

	if len(reviewer.inputs) != 1 {
		t.Fatalf("reviewer inputs = %d, want 1", len(reviewer.inputs))
	}
	input := reviewer.inputs[0]
	if input.ParentDesignID != "cb-design" {
		t.Fatalf("input.ParentDesignID = %q, want cb-design", input.ParentDesignID)
	}
	if len(input.AcceptanceCriteria) != 2 {
		t.Fatalf("acceptance criteria = %v, want 2 items", input.AcceptanceCriteria)
	}
	if len(combinedCalls) != 2 {
		t.Fatalf("combined calls = %v, want comment + dispatch", combinedCalls)
	}
	if got := fc.items["cb-task"].Status; got != "in_progress" {
		t.Fatalf("task status = %q, want in_progress", got)
	}
	if !strings.Contains(fc.items["cb-task"].Content, "Post the comment before recording the gate.") {
		t.Fatalf("task feedback missing built-in finding: %q", fc.items["cb-task"].Content)
	}
	if len(fs.gates) != 1 {
		t.Fatalf("recorded %d gates, want 1", len(fs.gates))
	}
	body := ""
	if fs.gates[0].Body != nil {
		body = *fs.gates[0].Body
	}
	if !strings.Contains(body, "**LLM verdict:** request-changes") {
		t.Fatalf("gate body missing LLM verdict: %q", body)
	}
	if !strings.Contains(body, "Misses PR comment posting.") {
		t.Fatalf("gate body missing LLM summary: %q", body)
	}
}

func TestProcessReviewBuiltInProviderFailureFallsBackToCIOnly(t *testing.T) {
	fc, fs := newPRReviewFixture()
	installReviewCommandTestGlobals(t, fc, fs)

	cfg := config.DefaultConfig()
	cfg.Review.Provider = "claude"
	// Opt into the builtin path explicitly; dispatched is now the default
	// and no longer falls through on refusal (cb-6f9ed6).
	cfg.Review.Mode = "builtin"
	postTrue := true
	waitTrue := true
	cfg.Review.PostComments = &postTrue
	cfg.Review.WaitForCI = &waitTrue
	reviewConfigLoader = func() *config.Config { return cfg }

	reviewerFactory = func(provider string, cfg llmreview.ProviderConfig) (llmreview.Reviewer, error) {
		return &stubReviewer{err: fmt.Errorf("model unavailable: no API key")}, nil
	}

	var combinedCalls [][]string
	execCommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "gh pr view https://github.com/acme/cobuild/pull/42 --json state --jq .state":
			return []byte("OPEN\n"), nil
		case "gh pr diff 42 --repo acme/cobuild":
			return []byte("diff --git a/internal/cmd/review.go b/internal/cmd/review.go\n"), nil
		case "gh pr view 42 --repo acme/cobuild --json headRefOid --jq .headRefOid":
			return []byte("abc123\n"), nil
		case "gh api repos/acme/cobuild/commits/abc123/check-runs --jq .check_runs":
			return []byte(`[{"name":"test","status":"completed","conclusion":"failure"}]`), nil
		case "gh api repos/acme/cobuild/actions/runs?branch=main&status=completed&per_page=1 --jq .workflow_runs[0].id":
			return []byte("main-run\n"), nil
		case "gh api repos/acme/cobuild/actions/runs/main-run/jobs --jq .jobs[] | .name + \":\" + .conclusion":
			return []byte("lint:success\n"), nil
		default:
			t.Fatalf("unexpected Output command: %s", key)
			return nil, nil
		}
	}
	execCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		combinedCalls = append(combinedCalls, append([]string{name}, args...))
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "cobuild dispatch cb-task":
			return []byte("dispatched\n"), nil
		default:
			t.Fatalf("unexpected CombinedOutput command: %s", key)
			return nil, nil
		}
	}

	processReviewCmd.Flags().Set("dry-run", "false")
	processReviewCmd.Flags().Set("review-timeout", "10")
	if err := processReviewCmd.RunE(processReviewCmd, []string{"cb-task"}); err != nil {
		t.Fatalf("process-review returned error: %v", err)
	}

	if len(combinedCalls) != 1 || strings.Join(combinedCalls[0], " ") != "cobuild dispatch cb-task" {
		t.Fatalf("combined calls = %v, want only redispatch", combinedCalls)
	}
	if len(fs.gates) != 1 {
		t.Fatalf("recorded %d gates, want 1", len(fs.gates))
	}
	body := ""
	if fs.gates[0].Body != nil {
		body = *fs.gates[0].Body
	}
	if !strings.Contains(body, "built-in claude review failed") {
		t.Fatalf("gate body missing provider warning: %q", body)
	}
	if !strings.Contains(body, "**Reviewer:** ci-fallback") {
		t.Fatalf("gate body missing fallback reviewer: %q", body)
	}
}

func TestRecordMergeFailureLogsAuditWriteErrors(t *testing.T) {
	origConn := conn
	origWarningWriter := reviewWarningWriter
	defer func() {
		conn = origConn
		reviewWarningWriter = origWarningWriter
	}()

	var warnings bytes.Buffer
	reviewWarningWriter = &warnings
	conn = &reviewFakeConnector{
		items: map[string]*connector.WorkItem{
			"cb-task": {
				ID:      "cb-task",
				Type:    "task",
				Status:  "needs-review",
				Content: "body",
				Metadata: map[string]any{
					"merge_retry_count": fmt.Sprintf("%d", mergeMaxRetries-1),
				},
			},
		},
		setMetadataErr: fmt.Errorf("metadata write failed"),
		appendErr:      fmt.Errorf("append failed"),
		addLabelErr:    fmt.Errorf("label failed"),
	}

	recordMergeFailure(context.Background(), "cb-task", "https://github.com/acme/cobuild/pull/42", "merge conflict")

	got := warnings.String()
	for _, want := range []string{
		"failed to record merge retry count for cb-task",
		"failed to append merge-blocked note to cb-task",
		"failed to add merge-blocked label to cb-task",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("warnings = %q, want substring %q", got, want)
		}
	}
}

func TestDoMergeCleanupFailureDoesNotFailMerge(t *testing.T) {
	fc, fs := newPRReviewFixture()
	installReviewCommandTestGlobals(t, fc, fs)

	prevCleanup := reviewCleanupTaskResources
	prevConfigLoader := reviewConfigLoader
	prevOutput := execCommandOutput
	prevCombined := execCommandCombinedOutput
	prevWarningWriter := reviewWarningWriter
	t.Cleanup(func() {
		reviewCleanupTaskResources = prevCleanup
		reviewConfigLoader = prevConfigLoader
		execCommandOutput = prevOutput
		execCommandCombinedOutput = prevCombined
		reviewWarningWriter = prevWarningWriter
	})

	cfg := config.DefaultConfig()
	reviewConfigLoader = func() *config.Config { return cfg }
	reviewCleanupTaskResources = func(_ context.Context, taskID string) error {
		return fmt.Errorf("worktree busy")
	}

	var warnings bytes.Buffer
	reviewWarningWriter = &warnings

	execCommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "gh api repos/acme/cobuild/pulls/42 --jq .mergeable_state + \"|\" + .head.ref":
			return nil, fmt.Errorf("skip auto-rebase probe")
		default:
			t.Fatalf("unexpected Output command: %s", key)
			return nil, nil
		}
	}
	execCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		if key != "gh pr merge https://github.com/acme/cobuild/pull/42 --squash --delete-branch" {
			t.Fatalf("unexpected CombinedOutput command: %s", key)
		}
		return []byte("merged\n"), nil
	}

	if err := doMerge(context.Background(), "cb-task", "https://github.com/acme/cobuild/pull/42"); err != nil {
		t.Fatalf("doMerge returned error after successful merge: %v", err)
	}
	if got := fc.items["cb-task"].Status; got != "closed" {
		t.Fatalf("task status = %q, want closed", got)
	}
	if !strings.Contains(warnings.String(), "merge succeeded, but local cleanup failed for cb-task: worktree busy") {
		t.Fatalf("warnings = %q, want cleanup failure warning", warnings.String())
	}
}

func TestProcessReviewExternalProviderStillWaitsForGemini(t *testing.T) {
	fc, fs := newPRReviewFixture()
	installReviewCommandTestGlobals(t, fc, fs)

	cfg := config.DefaultConfig()
	cfg.Review.Provider = "external"
	reviewConfigLoader = func() *config.Config { return cfg }

	reviewerCalled := false
	reviewerFactory = func(provider string, cfg llmreview.ProviderConfig) (llmreview.Reviewer, error) {
		reviewerCalled = true
		return &stubReviewer{}, nil
	}

	execCommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "gh pr view https://github.com/acme/cobuild/pull/42 --json state --jq .state":
			return []byte("OPEN\n"), nil
		case "gh api repos/acme/cobuild/pulls/42/reviews":
			return []byte("[]"), nil
		case "gh pr view https://github.com/acme/cobuild/pull/42 --json createdAt --jq .createdAt":
			return []byte(time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)), nil
		default:
			t.Fatalf("unexpected Output command: %s", key)
			return nil, nil
		}
	}
	execCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		t.Fatalf("unexpected CombinedOutput command: %s %s", name, strings.Join(args, " "))
		return nil, nil
	}

	processReviewCmd.Flags().Set("dry-run", "false")
	processReviewCmd.Flags().Set("review-timeout", "10")
	if err := processReviewCmd.RunE(processReviewCmd, []string{"cb-task"}); err != nil {
		t.Fatalf("process-review returned error: %v", err)
	}

	if reviewerCalled {
		t.Fatalf("built-in reviewer should not be called for external provider")
	}
	if len(fs.gates) != 0 {
		t.Fatalf("recorded %d gates, want 0 while waiting", len(fs.gates))
	}
	if got := fc.items["cb-task"].Status; got != "needs-review" {
		t.Fatalf("task status = %q, want needs-review", got)
	}
}

// Regression for cb-d5e1dd #2: when the LLM review fails (nil result) and
// the repo has no CI checks, the old verdict logic approved silently.
// It must now escalate to request-changes so the operator is forced to
// configure a review path.
func TestDetermineReviewVerdict_NoReviewCapabilityFailsLoud(t *testing.T) {
	cases := []struct {
		name string
		ci   ciCheckResult
		want string
	}{
		{"no checks configured", ciCheckResult{summary: "no CI checks configured"}, "request-changes"},
		{"could not get commit", ciCheckResult{summary: "no checks (could not get commit)"}, "request-changes"},
		{"api error", ciCheckResult{summary: "no checks (API error)"}, "request-changes"},
		{"pending still signals", ciCheckResult{summary: "pending"}, "approve"},
		{"passed checks approve", ciCheckResult{summary: "3 checks passed"}, "approve"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := determineReviewVerdict(nil, nil, tc.ci)
			if got != tc.want {
				t.Fatalf("verdict = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetermineReviewVerdict_CIFailuresAlwaysRequestChanges(t *testing.T) {
	ci := ciCheckResult{summary: "1 check failed", newFailures: []string{"test"}}
	if got := determineReviewVerdict(nil, nil, ci); got != "request-changes" {
		t.Fatalf("verdict = %q, want request-changes", got)
	}
}
