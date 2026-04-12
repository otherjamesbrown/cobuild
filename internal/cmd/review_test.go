package cmd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

type fakeConnector struct {
	items         map[string]*connector.WorkItem
	edges         map[string][]connector.Edge
	created       []connector.CreateRequest
	createdEdges  []struct{ fromID, toID, edgeType string }
	statusUpdates []struct{ id, status string }
}

func (f *fakeConnector) Name() string { return "fake" }

func (f *fakeConnector) Get(_ context.Context, id string) (*connector.WorkItem, error) {
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

func (f *fakeConnector) List(context.Context, connector.ListFilters) (*connector.ListResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeConnector) GetEdges(_ context.Context, id string, direction string, _ []string) ([]connector.Edge, error) {
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

func (f *fakeConnector) GetMetadata(_ context.Context, id string, key string) (string, error) {
	item, ok := f.items[id]
	if !ok || item.Metadata == nil {
		return "", nil
	}
	if v, ok := item.Metadata[key]; ok && v != nil {
		return fmt.Sprintf("%v", v), nil
	}
	return "", nil
}

func (f *fakeConnector) Create(_ context.Context, req connector.CreateRequest) (string, error) {
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

func (f *fakeConnector) UpdateStatus(_ context.Context, id string, status string) error {
	item, ok := f.items[id]
	if !ok {
		return fmt.Errorf("item not found: %s", id)
	}
	item.Status = status
	f.statusUpdates = append(f.statusUpdates, struct{ id, status string }{id: id, status: status})
	return nil
}

func (f *fakeConnector) AppendContent(_ context.Context, id string, content string) error {
	item, ok := f.items[id]
	if !ok {
		return fmt.Errorf("item not found: %s", id)
	}
	item.Content += content
	return nil
}

func (f *fakeConnector) SetMetadata(_ context.Context, id string, key string, value any) error {
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

func (f *fakeConnector) UpdateMetadataMap(_ context.Context, id string, patch map[string]any) error {
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

func (f *fakeConnector) AddLabel(_ context.Context, id string, label string) error {
	item, ok := f.items[id]
	if !ok {
		return fmt.Errorf("item not found: %s", id)
	}
	item.Labels = append(item.Labels, label)
	return nil
}

func (f *fakeConnector) CreateEdge(_ context.Context, fromID string, toID string, edgeType string) error {
	f.createdEdges = append(f.createdEdges, struct{ fromID, toID, edgeType string }{fromID: fromID, toID: toID, edgeType: edgeType})
	return nil
}

type fakeStore struct {
	runs          map[string]*store.PipelineRun
	gates         []store.PipelineGateInput
	updatePhases  []struct{ designID, phase string }
	updateStatus  []struct{ designID, status string }
	latestGateKey map[string]int
}

func (f *fakeStore) CreateRun(context.Context, string, string, string) (*store.PipelineRun, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeStore) CreateRunWithMode(context.Context, string, string, string, string) (*store.PipelineRun, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeStore) GetRun(_ context.Context, designID string) (*store.PipelineRun, error) {
	run, ok := f.runs[designID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *run
	return &cp, nil
}

func (f *fakeStore) ListRuns(context.Context, string) ([]store.PipelineRunStatus, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeStore) UpdateRunPhase(_ context.Context, designID, phase string) error {
	f.updatePhases = append(f.updatePhases, struct{ designID, phase string }{designID: designID, phase: phase})
	if run, ok := f.runs[designID]; ok {
		run.CurrentPhase = phase
	}
	return nil
}

func (f *fakeStore) UpdateRunStatus(_ context.Context, designID, status string) error {
	f.updateStatus = append(f.updateStatus, struct{ designID, status string }{designID: designID, status: status})
	if run, ok := f.runs[designID]; ok {
		run.Status = status
	}
	return nil
}

func (f *fakeStore) SetRunMode(context.Context, string, string) error { return nil }

func (f *fakeStore) RecordGate(_ context.Context, input store.PipelineGateInput) (*store.PipelineGateRecord, error) {
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

func (f *fakeStore) GetGateHistory(context.Context, string) ([]store.PipelineGateRecord, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeStore) GetLatestGateRound(_ context.Context, pipelineID, gateName string) (int, error) {
	return f.latestGateKey[pipelineID+":"+gateName], nil
}

func (f *fakeStore) AddTask(context.Context, string, string, string, *int) error { return nil }
func (f *fakeStore) ListTasks(context.Context, string) ([]store.PipelineTaskRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeStore) UpdateTaskStatus(context.Context, string, string) error { return nil }
func (f *fakeStore) CreateSession(context.Context, store.SessionInput) (*store.SessionRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeStore) EndSession(context.Context, string, store.SessionResult) error { return nil }
func (f *fakeStore) GetSession(context.Context, string) (*store.SessionRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeStore) ListSessions(context.Context, string) ([]store.SessionRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeStore) GetRunStatusCounts(context.Context, string) (map[string]int, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeStore) GetTaskStatusCounts(context.Context, string) (map[string]int, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeStore) GetGatePassRates(context.Context, string) ([]store.GatePassRate, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeStore) GetGateFailures(context.Context, string) ([]store.PipelineGateRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeStore) GetAvgTaskDuration(context.Context, string) (*float64, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeStore) Migrate(context.Context) error { return nil }
func (f *fakeStore) Close() error                  { return nil }

func TestProcessReviewHandlesDirectNeedsReviewTaskWithoutPR(t *testing.T) {
	origConn, origStore, origProject := conn, cbStore, projectName
	defer func() {
		conn = origConn
		cbStore = origStore
		projectName = origProject
	}()

	projectName = "cobuild"
	conn = &fakeConnector{
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
	cbStore = &fakeStore{
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

	fc := conn.(*fakeConnector)
	fs := cbStore.(*fakeStore)

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
	if len(fc.statusUpdates) != 1 || fc.statusUpdates[0].id != "cb-task" || fc.statusUpdates[0].status != "closed" {
		t.Fatalf("status updates = %+v, want cb-task -> closed", fc.statusUpdates)
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
	conn = &fakeConnector{
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
	cbStore = &fakeStore{
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

	fc := conn.(*fakeConnector)
	fs := cbStore.(*fakeStore)

	if len(fs.gates) != 0 {
		t.Fatalf("recorded %d gates, want 0 for idempotent closed task", len(fs.gates))
	}
	if len(fc.created) != 0 {
		t.Fatalf("created %d review items, want 0 for idempotent closed task", len(fc.created))
	}
	if len(fc.statusUpdates) != 0 {
		t.Fatalf("status updates = %+v, want none", fc.statusUpdates)
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
