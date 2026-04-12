package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestDetermineCompletionPathExplicitDirect(t *testing.T) {
	ctx := context.Background()
	taskID := "cb-direct"
	wtPath := newTestWorktree(t, taskID)

	fc := newFakeConnector()
	fc.items[taskID] = &connector.WorkItem{
		ID:       taskID,
		Title:    "Direct task",
		Type:     "task",
		Status:   "in_progress",
		Metadata: map[string]any{"completion_mode": "direct"},
	}
	restore := installTestGlobals(t, fc, newFakeStore(), "test-project")
	defer restore()

	decision, err := determineCompletionPath(ctx, fc.items[taskID], taskID, wtPath, "")
	if err != nil {
		t.Fatalf("determineCompletionPath() error = %v", err)
	}
	if !decision.Direct {
		t.Fatalf("determineCompletionPath() direct = false, want true")
	}
	if !strings.Contains(decision.Note, "completion_mode=direct") {
		t.Fatalf("determineCompletionPath() note = %q, want completion_mode reason", decision.Note)
	}
}

func TestDirectCompletionFallbackForEmptyWorktree(t *testing.T) {
	ctx := context.Background()
	taskID := "cb-empty"
	designID := "cb-design"
	wtPath := newTestWorktree(t, taskID)

	fc := newFakeConnector()
	fc.items[taskID] = &connector.WorkItem{ID: taskID, Title: "Non-code task", Type: "task", Status: "in_progress"}
	fc.items[designID] = &connector.WorkItem{ID: designID, Title: "Design", Type: "design", Status: "review"}
	fc.metadata[taskID] = map[string]string{
		"worktree_path": wtPath,
		"session_id":    "ps-1",
	}
	fc.parent[taskID] = designID

	fs := newFakeStore()
	fs.runs[taskID] = &store.PipelineRun{ID: "run-task", DesignID: taskID, CurrentPhase: "implement", Status: "active"}
	fs.runs[designID] = &store.PipelineRun{ID: "run-design", DesignID: designID, CurrentPhase: "review", Status: "active"}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	task := fc.items[taskID]
	decision, err := determineCompletionPath(ctx, task, taskID, wtPath, "")
	if err != nil {
		t.Fatalf("determineCompletionPath() error = %v", err)
	}
	if !decision.Direct {
		t.Fatalf("determineCompletionPath() direct = false, want true")
	}

	if err := completeDirectTask(ctx, taskID, wtPath, decision.Note); err != nil {
		t.Fatalf("completeDirectTask() error = %v", err)
	}

	if got := fc.items[taskID].Status; got != "closed" {
		t.Fatalf("task status = %q, want closed", got)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after direct completion: stat err = %v", err)
	}
	if len(fs.gates) != 1 {
		t.Fatalf("gate count = %d, want 1", len(fs.gates))
	}
	if fs.gates[0].GateName != "review" || fs.gates[0].Verdict != "pass" {
		t.Fatalf("gate = %+v, want review/pass", fs.gates[0])
	}
	session, ok := fs.ended["ps-1"]
	if !ok {
		t.Fatalf("session ps-1 was not ended")
	}
	if session.PRURL != "" {
		t.Fatalf("session PRURL = %q, want empty", session.PRURL)
	}
	if session.CompletionNote == "" || !strings.Contains(session.CompletionNote, "git worktree has no tracked changes") {
		t.Fatalf("session completion note = %q, want fallback reason", session.CompletionNote)
	}
	if fs.runs[taskID].CurrentPhase != "done" || fs.runs[taskID].Status != "completed" {
		t.Fatalf("task run = %+v, want done/completed", fs.runs[taskID])
	}
	if fs.runs[designID].CurrentPhase != "done" || fs.runs[designID].Status != "completed" {
		t.Fatalf("design run = %+v, want done/completed", fs.runs[designID])
	}
}

func TestDetermineCompletionPathPrefersPRFlowForDirtyWorktree(t *testing.T) {
	ctx := context.Background()
	taskID := "cb-code"
	wtPath := newTestWorktree(t, taskID)
	writeFile(t, filepath.Join(wtPath, "README.md"), "changed\n")

	fc := newFakeConnector()
	fc.items[taskID] = &connector.WorkItem{ID: taskID, Title: "Code task", Type: "task", Status: "in_progress"}
	restore := installTestGlobals(t, fc, newFakeStore(), "test-project")
	defer restore()

	decision, err := determineCompletionPath(ctx, fc.items[taskID], taskID, wtPath, "")
	if err != nil {
		t.Fatalf("determineCompletionPath() error = %v", err)
	}
	if decision.Direct {
		t.Fatalf("determineCompletionPath() direct = true, want false for dirty worktree")
	}
}

func installTestGlobals(t *testing.T, testConn connector.Connector, testStore store.Store, testProject string) func() {
	t.Helper()
	prevConn := conn
	prevStore := cbStore
	prevProject := projectName
	conn = testConn
	cbStore = testStore
	projectName = testProject
	return func() {
		conn = prevConn
		cbStore = prevStore
		projectName = prevProject
	}
}

type fakeConnector struct {
	items    map[string]*connector.WorkItem
	metadata map[string]map[string]string
	parent   map[string]string
}

func newFakeConnector() *fakeConnector {
	return &fakeConnector{
		items:    map[string]*connector.WorkItem{},
		metadata: map[string]map[string]string{},
		parent:   map[string]string{},
	}
}

func (f *fakeConnector) Name() string { return "fake" }

func (f *fakeConnector) Get(ctx context.Context, id string) (*connector.WorkItem, error) {
	item, ok := f.items[id]
	if !ok {
		return nil, fmt.Errorf("missing item %s", id)
	}
	return item, nil
}

func (f *fakeConnector) List(ctx context.Context, filters connector.ListFilters) (*connector.ListResult, error) {
	return &connector.ListResult{}, nil
}

func (f *fakeConnector) GetEdges(ctx context.Context, id string, direction string, types []string) ([]connector.Edge, error) {
	switch direction {
	case "outgoing":
		if designID := f.parent[id]; designID != "" {
			return []connector.Edge{{Direction: "outgoing", EdgeType: "child-of", ItemID: designID, Status: f.items[designID].Status}}, nil
		}
	case "incoming":
		var edges []connector.Edge
		for childID, parentID := range f.parent {
			if parentID == id {
				edges = append(edges, connector.Edge{
					Direction: "incoming",
					EdgeType:  "child-of",
					ItemID:    childID,
					Status:    f.items[childID].Status,
				})
			}
		}
		return edges, nil
	}
	return nil, nil
}

func (f *fakeConnector) GetMetadata(ctx context.Context, id string, key string) (string, error) {
	if f.metadata[id] == nil {
		return "", nil
	}
	return f.metadata[id][key], nil
}

func (f *fakeConnector) Create(ctx context.Context, req connector.CreateRequest) (string, error) {
	return "created", nil
}

func (f *fakeConnector) UpdateStatus(ctx context.Context, id string, status string) error {
	f.items[id].Status = status
	return nil
}

func (f *fakeConnector) AppendContent(ctx context.Context, id string, content string) error {
	return nil
}

func (f *fakeConnector) SetMetadata(ctx context.Context, id string, key string, value any) error {
	if f.metadata[id] == nil {
		f.metadata[id] = map[string]string{}
	}
	f.metadata[id][key] = fmt.Sprintf("%v", value)
	return nil
}

func (f *fakeConnector) UpdateMetadataMap(ctx context.Context, id string, patch map[string]any) error {
	for key, value := range patch {
		if err := f.SetMetadata(ctx, id, key, value); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeConnector) AddLabel(ctx context.Context, id string, label string) error { return nil }

func (f *fakeConnector) CreateEdge(ctx context.Context, fromID string, toID string, edgeType string) error {
	return nil
}

type fakeStore struct {
	runs  map[string]*store.PipelineRun
	gates []store.PipelineGateInput
	ended map[string]store.SessionResult
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		runs:  map[string]*store.PipelineRun{},
		ended: map[string]store.SessionResult{},
	}
}

func (f *fakeStore) CreateRun(ctx context.Context, designID, project, phase string) (*store.PipelineRun, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeStore) CreateRunWithMode(ctx context.Context, designID, project, phase, mode string) (*store.PipelineRun, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeStore) GetRun(ctx context.Context, designID string) (*store.PipelineRun, error) {
	run, ok := f.runs[designID]
	if !ok {
		return nil, fmt.Errorf("missing run %s", designID)
	}
	return run, nil
}

func (f *fakeStore) ListRuns(ctx context.Context, project string) ([]store.PipelineRunStatus, error) {
	return nil, nil
}

func (f *fakeStore) UpdateRunPhase(ctx context.Context, designID, phase string) error {
	f.runs[designID].CurrentPhase = phase
	return nil
}

func (f *fakeStore) UpdateRunStatus(ctx context.Context, designID, status string) error {
	f.runs[designID].Status = status
	return nil
}

func (f *fakeStore) SetRunMode(ctx context.Context, designID, mode string) error { return nil }

func (f *fakeStore) RecordGate(ctx context.Context, input store.PipelineGateInput) (*store.PipelineGateRecord, error) {
	f.gates = append(f.gates, input)
	return &store.PipelineGateRecord{ID: "pg-1", PipelineID: input.PipelineID, DesignID: input.DesignID}, nil
}

func (f *fakeStore) GetGateHistory(ctx context.Context, designID string) ([]store.PipelineGateRecord, error) {
	return nil, nil
}

func (f *fakeStore) GetLatestGateRound(ctx context.Context, pipelineID, gateName string) (int, error) {
	return 0, nil
}

func (f *fakeStore) AddTask(ctx context.Context, pipelineID, taskShardID, designID string, wave *int) error {
	return nil
}

func (f *fakeStore) ListTasks(ctx context.Context, pipelineID string) ([]store.PipelineTaskRecord, error) {
	return nil, nil
}

func (f *fakeStore) UpdateTaskStatus(ctx context.Context, taskShardID, status string) error {
	return nil
}

func (f *fakeStore) CreateSession(ctx context.Context, input store.SessionInput) (*store.SessionRecord, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeStore) EndSession(ctx context.Context, id string, result store.SessionResult) error {
	f.ended[id] = result
	return nil
}

func (f *fakeStore) GetSession(ctx context.Context, taskID string) (*store.SessionRecord, error) {
	return nil, nil
}

func (f *fakeStore) ListSessions(ctx context.Context, designID string) ([]store.SessionRecord, error) {
	return nil, nil
}

func (f *fakeStore) GetRunStatusCounts(ctx context.Context, project string) (map[string]int, error) {
	return nil, nil
}

func (f *fakeStore) GetTaskStatusCounts(ctx context.Context, project string) (map[string]int, error) {
	return nil, nil
}

func (f *fakeStore) GetGatePassRates(ctx context.Context, project string) ([]store.GatePassRate, error) {
	return nil, nil
}

func (f *fakeStore) GetGateFailures(ctx context.Context, project string) ([]store.PipelineGateRecord, error) {
	return nil, nil
}

func (f *fakeStore) GetAvgTaskDuration(ctx context.Context, project string) (*float64, error) {
	return nil, nil
}

func (f *fakeStore) Migrate(ctx context.Context) error { return nil }

func (f *fakeStore) Close() error { return nil }

func newTestWorktree(t *testing.T, branch string) string {
	t.Helper()

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test User")
	writeFile(t, filepath.Join(repoDir, "README.md"), "initial\n")
	runGit(t, repoDir, "add", "README.md")
	runGit(t, repoDir, "commit", "-m", "initial")

	wtPath := filepath.Join(t.TempDir(), branch)
	runGit(t, repoDir, "worktree", "add", "-b", branch, wtPath, "main")
	return wtPath
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

var _ store.Store = (*fakeStore)(nil)
var _ connector.Connector = (*fakeConnector)(nil)
