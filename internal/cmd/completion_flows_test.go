package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

func TestCompleteDirectOutsideWorktreeClosesWithoutPRAndRecordsPipelineState(t *testing.T) {
	ctx := context.Background()
	env := newCompletionTestEnv(t)

	taskID := "cb-direct-task"
	codeTaskID := "cb-code-task"
	designID := "cb-design"

	wtPath := newGitWorktree(t, env.repoRoot, taskID)
	completionWriteFile(t, filepath.Join(wtPath, ".cobuild", "session.log"), "direct task log\n")
	completionWriteFile(t, filepath.Join(t.TempDir(), "outside.txt"), "outside-only side effect\n")

	connectorStub := newCompletionFakeConnector()
	connectorStub.addItem(&connector.WorkItem{
		ID:      designID,
		Title:   "Mixed design",
		Type:    "design",
		Status:  "in_progress",
		Content: "parent",
	})
	connectorStub.addItem(&connector.WorkItem{
		ID:      taskID,
		Title:   "Outside-only task",
		Type:    "task",
		Status:  "in_progress",
		Content: "direct",
		Metadata: map[string]any{
			"worktree_path":   wtPath,
			"session_id":      "sess-direct",
			"completion_mode": "direct",
		},
	})
	connectorStub.addItem(&connector.WorkItem{
		ID:      codeTaskID,
		Title:   "Code sibling",
		Type:    "task",
		Status:  "closed",
		Content: "code",
	})
	connectorStub.addChild(taskID, designID)
	connectorStub.addChild(codeTaskID, designID)

	storeStub := newCompletionFakeStore()
	storeStub.runs[taskID] = &store.PipelineRun{ID: "run-task-direct", DesignID: taskID, CurrentPhase: "implement", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	storeStub.runs[designID] = &store.PipelineRun{ID: "run-design", DesignID: designID, CurrentPhase: "implement", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	storeStub.tasks["run-design"] = []store.PipelineTaskRecord{
		{ID: "pt-1", PipelineID: "run-design", TaskShardID: taskID, DesignID: designID, Status: "in_progress", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "pt-2", PipelineID: "run-design", TaskShardID: codeTaskID, DesignID: designID, Status: "closed", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	storeStub.sessions["sess-direct"] = &store.SessionRecord{ID: "sess-direct", TaskID: taskID, Status: "running"}

	restore := installCommandTestGlobals(t, connectorStub, storeStub, env.project)
	defer restore()

	if err := runCommandAndCaptureStdout(t, func() error {
		return completeCmd.RunE(completeCmd, []string{taskID})
	}); err != nil {
		t.Fatalf("complete direct task: %v", err)
	}

	task, _ := connectorStub.Get(ctx, taskID)
	if task.Status != "closed" {
		t.Fatalf("task status = %s, want closed", task.Status)
	}
	if got, _ := connectorStub.GetMetadata(ctx, taskID, "pr_url"); got != "" {
		t.Fatalf("pr_url = %q, want empty", got)
	}
	if !strings.Contains(task.Content, "Direct close: completion_mode=direct") {
		t.Fatalf("expected direct-close evidence in task content, got: %s", task.Content)
	}

	gates, _ := storeStub.GetGateHistory(ctx, taskID)
	if len(gates) != 1 || gates[0].Verdict != "pass" || gates[0].GateName != "review" {
		t.Fatalf("unexpected gates: %+v", gates)
	}
	if storeStub.runs[taskID].CurrentPhase != "done" || storeStub.runs[taskID].Status != "completed" {
		t.Fatalf("task run = %+v, want done/completed", storeStub.runs[taskID])
	}
	if storeStub.runs[designID].CurrentPhase != "done" || storeStub.runs[designID].Status != "completed" {
		t.Fatalf("design run = %+v, want done/completed", storeStub.runs[designID])
	}
	if got := storeStub.taskStatus("run-design", taskID); got != "closed" {
		t.Fatalf("design task status = %s, want closed", got)
	}
	session := storeStub.sessions["sess-direct"]
	if session.Status != "completed" {
		t.Fatalf("session status = %s, want completed", session.Status)
	}
	if session.PRURL != nil && *session.PRURL != "" {
		t.Fatalf("session PRURL = %q, want empty", *session.PRURL)
	}
}

func TestCompleteWithRepoChangesStaysOnPRPathEvenWithOutsideEffects(t *testing.T) {
	ctx := context.Background()
	env := newCompletionTestEnv(t)
	installFakeGH(t, "https://github.com/example/repo/pull/123")

	taskID := "cb-mixed-task"
	designID := "cb-design-pr"
	wtPath := newGitWorktree(t, env.repoRoot, taskID)
	completionWriteFile(t, filepath.Join(wtPath, ".cobuild", "session.log"), "code task log\n")
	completionWriteFile(t, filepath.Join(wtPath, "tracked.txt"), "changed in repo\n")
	completionWriteFile(t, filepath.Join(t.TempDir(), "outside.txt"), "outside effect\n")

	connectorStub := newCompletionFakeConnector()
	connectorStub.addItem(&connector.WorkItem{
		ID:      designID,
		Title:   "Parent design",
		Type:    "design",
		Status:  "in_progress",
		Content: "design",
	})
	connectorStub.addItem(&connector.WorkItem{
		ID:      taskID,
		Title:   "Repo + outside task",
		Type:    "task",
		Status:  "in_progress",
		Content: "task",
		Metadata: map[string]any{
			"worktree_path": wtPath,
			"session_id":    "sess-code",
		},
	})
	connectorStub.addChild(taskID, designID)

	storeStub := newCompletionFakeStore()
	storeStub.runs[taskID] = &store.PipelineRun{ID: "run-task-code", DesignID: taskID, CurrentPhase: "implement", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	storeStub.runs[designID] = &store.PipelineRun{ID: "run-design-code", DesignID: designID, CurrentPhase: "implement", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	storeStub.tasks["run-design-code"] = []store.PipelineTaskRecord{
		{ID: "pt-1", PipelineID: "run-design-code", TaskShardID: taskID, DesignID: designID, Status: "in_progress", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	storeStub.sessions["sess-code"] = &store.SessionRecord{ID: "sess-code", TaskID: taskID, Status: "running"}

	restore := installCommandTestGlobals(t, connectorStub, storeStub, env.project)
	defer restore()

	if err := runCommandAndCaptureStdout(t, func() error {
		return completeCmd.RunE(completeCmd, []string{taskID})
	}); err != nil {
		t.Fatalf("complete code task: %v", err)
	}

	task, _ := connectorStub.Get(ctx, taskID)
	if task.Status != "needs-review" {
		t.Fatalf("task status = %s, want needs-review", task.Status)
	}
	prURL, _ := connectorStub.GetMetadata(ctx, taskID, "pr_url")
	if prURL == "" {
		t.Fatal("expected pr_url to be set")
	}
	if storeStub.runs[taskID].CurrentPhase != "review" {
		t.Fatalf("task run phase = %s, want review", storeStub.runs[taskID].CurrentPhase)
	}
	if got := storeStub.taskStatus("run-design-code", taskID); got != "needs-review" {
		t.Fatalf("design task status = %s, want needs-review", got)
	}
	gates, _ := storeStub.GetGateHistory(ctx, taskID)
	if len(gates) != 0 {
		t.Fatalf("expected no direct gate for code path, got %+v", gates)
	}
	session := storeStub.sessions["sess-code"]
	if session.Status != "completed" {
		t.Fatalf("session status = %s, want completed", session.Status)
	}
	if session.PRURL == nil || *session.PRURL == "" {
		t.Fatal("expected session PRURL to be set")
	}
}

func TestNextAndShowAdvanceAfterDirectChildClosesInMixedTaskSet(t *testing.T) {
	ctx := context.Background()
	env := newCompletionTestEnv(t)

	taskID := "cb-last-direct"
	codeTaskID := "cb-already-closed"
	designID := "cb-mixed-design"
	wtPath := newGitWorktree(t, env.repoRoot, taskID)

	connectorStub := newCompletionFakeConnector()
	connectorStub.addItem(&connector.WorkItem{ID: designID, Title: "Mixed design", Type: "design", Status: "in_progress"})
	connectorStub.addItem(&connector.WorkItem{
		ID:     taskID,
		Title:  "Last direct child",
		Type:   "task",
		Status: "in_progress",
		Metadata: map[string]any{
			"worktree_path":   wtPath,
			"completion_mode": "direct",
		},
	})
	connectorStub.addItem(&connector.WorkItem{ID: codeTaskID, Title: "Closed code child", Type: "task", Status: "closed"})
	connectorStub.addChild(taskID, designID)
	connectorStub.addChild(codeTaskID, designID)

	storeStub := newCompletionFakeStore()
	storeStub.runs[taskID] = &store.PipelineRun{ID: "run-task-last", DesignID: taskID, CurrentPhase: "implement", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	storeStub.runs[designID] = &store.PipelineRun{ID: "run-design-mixed", DesignID: designID, CurrentPhase: "implement", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	storeStub.tasks["run-design-mixed"] = []store.PipelineTaskRecord{
		{ID: "pt-1", PipelineID: "run-design-mixed", TaskShardID: taskID, DesignID: designID, Status: "in_progress", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "pt-2", PipelineID: "run-design-mixed", TaskShardID: codeTaskID, DesignID: designID, Status: "closed", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}

	restore := installCommandTestGlobals(t, connectorStub, storeStub, env.project)
	defer restore()

	if err := runCommandAndCaptureStdout(t, func() error {
		return completeCmd.RunE(completeCmd, []string{taskID})
	}); err != nil {
		t.Fatalf("complete direct child: %v", err)
	}

	showOut, showErr := runCommandWithOutputs(t, showCmd, []string{designID})
	if showErr != nil {
		t.Fatalf("show design: %v", showErr)
	}
	if !strings.Contains(showOut, "Status:         completed") || !strings.Contains(showOut, "Tasks:          2 (2 closed)") {
		t.Fatalf("unexpected show output:\n%s", showOut)
	}

	nextOut, nextErr := runCommandWithOutputs(t, nextCmd, []string{designID})
	if nextErr != nil {
		t.Fatalf("next design: %v", nextErr)
	}
	if !strings.Contains(nextOut, "Pipeline complete. Nothing to do.") {
		t.Fatalf("unexpected next output:\n%s", nextOut)
	}

	if storeStub.runs[designID].CurrentPhase != "done" || storeStub.runs[designID].Status != "completed" {
		t.Fatalf("design run = %+v, want done/completed", storeStub.runs[designID])
	}
	if gates, _ := storeStub.GetGateHistory(ctx, taskID); len(gates) != 1 {
		t.Fatalf("expected one direct gate for task, got %+v", gates)
	}
}

type completionTestEnv struct {
	project  string
	repoRoot string
}

func newCompletionTestEnv(t *testing.T) completionTestEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	project := "test-project"
	repoRoot := newBareRemoteRepo(t)
	if err := config.SaveRepoRegistry(&config.RepoRegistry{
		Repos: map[string]config.RepoEntry{
			project: {Path: repoRoot},
		},
	}); err != nil {
		t.Fatalf("save repo registry: %v", err)
	}
	return completionTestEnv{project: project, repoRoot: repoRoot}
}

func newBareRemoteRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "repo")

	completionRunGit(t, root, "init", "--bare", origin)
	completionRunGit(t, root, "clone", origin, repo)
	completionRunGit(t, repo, "config", "user.email", "test@example.com")
	completionRunGit(t, repo, "config", "user.name", "Test User")
	completionWriteFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
	completionWriteFile(t, filepath.Join(repo, "CLAUDE.md"), "base claude\n")
	completionWriteFile(t, filepath.Join(repo, ".cobuild", "pipeline.yaml"), "github:\n  owner_repo: example/repo\n")
	completionRunGit(t, repo, "add", "tracked.txt", "CLAUDE.md")
	completionRunGit(t, repo, "commit", "-m", "initial")
	completionRunGit(t, repo, "branch", "-M", "main")
	completionRunGit(t, repo, "push", "-u", "origin", "main")
	return repo
}

func newGitWorktree(t *testing.T, repoRoot, branch string) string {
	t.Helper()
	wtPath := filepath.Join(t.TempDir(), "wt")
	completionRunGit(t, repoRoot, "worktree", "add", "-b", branch, wtPath, "main")
	completionWriteFile(t, filepath.Join(wtPath, ".cobuild", ".keep"), "")
	return wtPath
}

func installFakeGH(t *testing.T, prURL string) {
	t.Helper()
	binDir := t.TempDir()
	script := filepath.Join(binDir, "gh")
	content := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"pr\" ] && [ \"$2\" = \"create\" ]; then\n  echo %q\n  exit 0\nfi\nprintf 'unsupported gh invocation: %%s\\n' \"$*\" >&2\nexit 1\n", prURL)
	completionWriteFile(t, script, content)
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatalf("chmod gh shim: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installCommandTestGlobals(t *testing.T, c connector.Connector, s store.Store, project string) func() {
	t.Helper()
	oldConn := conn
	oldStore := cbStore
	oldProject := projectName
	oldFormat := outputFormat

	conn = c
	cbStore = s
	projectName = project
	outputFormat = "text"

	return func() {
		conn = oldConn
		cbStore = oldStore
		projectName = oldProject
		outputFormat = oldFormat
	}
}

func runCommandAndCaptureStdout(t *testing.T, fn func() error) error {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()

	runErr := fn()
	_ = w.Close()
	os.Stdout = oldStdout
	<-done
	return runErr
}

func runCommandWithOutputs(t *testing.T, command *cobra.Command, args []string) (string, error) {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	var stderr bytes.Buffer
	command.SetErr(&stderr)
	err = command.RunE(command, args)
	_ = w.Close()

	var out bytes.Buffer
	_, _ = out.ReadFrom(r)
	if stderr.Len() > 0 {
		out.WriteString(stderr.String())
	}
	return out.String(), err
}

func completionRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func completionWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type completionFakeConnector struct {
	items   map[string]*connector.WorkItem
	childOf map[string]string
	nextID  int
}

func newCompletionFakeConnector() *completionFakeConnector {
	return &completionFakeConnector{
		items:   map[string]*connector.WorkItem{},
		childOf: map[string]string{},
	}
}

func (f *completionFakeConnector) Name() string { return "fake" }

func (f *completionFakeConnector) Get(_ context.Context, id string) (*connector.WorkItem, error) {
	item, ok := f.items[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	copyItem := *item
	copyItem.Metadata = cloneMetadata(item.Metadata)
	return &copyItem, nil
}

func (f *completionFakeConnector) List(_ context.Context, _ connector.ListFilters) (*connector.ListResult, error) {
	return &connector.ListResult{}, nil
}

func (f *completionFakeConnector) GetEdges(_ context.Context, id string, direction string, types []string) ([]connector.Edge, error) {
	if len(types) > 0 && types[0] != "child-of" {
		return nil, nil
	}
	switch direction {
	case "outgoing":
		parentID := f.childOf[id]
		if parentID == "" {
			return nil, nil
		}
		parent := f.items[parentID]
		return []connector.Edge{{Direction: "outgoing", EdgeType: "child-of", ItemID: parentID, Title: parent.Title, Type: parent.Type, Status: parent.Status}}, nil
	case "incoming":
		var edges []connector.Edge
		for childID, parentID := range f.childOf {
			if parentID != id {
				continue
			}
			child := f.items[childID]
			edges = append(edges, connector.Edge{Direction: "incoming", EdgeType: "child-of", ItemID: childID, Title: child.Title, Type: child.Type, Status: child.Status})
		}
		return edges, nil
	default:
		return nil, nil
	}
}

func (f *completionFakeConnector) GetMetadata(_ context.Context, id string, key string) (string, error) {
	item, ok := f.items[id]
	if !ok || item.Metadata == nil {
		return "", nil
	}
	return metadataString(item.Metadata, key), nil
}

func (f *completionFakeConnector) Create(_ context.Context, req connector.CreateRequest) (string, error) {
	f.nextID++
	id := fmt.Sprintf("review-%d", f.nextID)
	f.items[id] = &connector.WorkItem{ID: id, Title: req.Title, Content: req.Content, Type: req.Type, Status: "closed", Metadata: cloneMetadata(req.Metadata)}
	return id, nil
}

func (f *completionFakeConnector) UpdateStatus(_ context.Context, id string, status string) error {
	item := f.items[id]
	item.Status = status
	return nil
}

func (f *completionFakeConnector) AppendContent(_ context.Context, id string, content string) error {
	item := f.items[id]
	if item.Content != "" && !strings.HasSuffix(item.Content, "\n") {
		item.Content += "\n"
	}
	item.Content += content
	return nil
}

func (f *completionFakeConnector) SetMetadata(_ context.Context, id string, key string, value any) error {
	item := f.items[id]
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	item.Metadata[key] = value
	return nil
}

func (f *completionFakeConnector) UpdateMetadataMap(_ context.Context, id string, patch map[string]any) error {
	item := f.items[id]
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	for k, v := range patch {
		item.Metadata[k] = v
	}
	return nil
}

func (f *completionFakeConnector) AddLabel(_ context.Context, id string, label string) error {
	item := f.items[id]
	item.Labels = append(item.Labels, label)
	return nil
}

func (f *completionFakeConnector) CreateEdge(_ context.Context, fromID string, toID string, edgeType string) error {
	if edgeType == "child-of" {
		f.childOf[fromID] = toID
	}
	return nil
}

func (f *completionFakeConnector) addItem(item *connector.WorkItem) {
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	f.items[item.ID] = item
}

func (f *completionFakeConnector) addChild(taskID, designID string) {
	f.childOf[taskID] = designID
}

type completionFakeStore struct {
	runs     map[string]*store.PipelineRun
	gates    map[string][]store.PipelineGateRecord
	tasks    map[string][]store.PipelineTaskRecord
	sessions map[string]*store.SessionRecord
}

func newCompletionFakeStore() *completionFakeStore {
	return &completionFakeStore{
		runs:     map[string]*store.PipelineRun{},
		gates:    map[string][]store.PipelineGateRecord{},
		tasks:    map[string][]store.PipelineTaskRecord{},
		sessions: map[string]*store.SessionRecord{},
	}
}

func (f *completionFakeStore) CreateRun(context.Context, string, string, string) (*store.PipelineRun, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *completionFakeStore) CreateRunWithMode(_ context.Context, designID, project, phase, mode string) (*store.PipelineRun, error) {
	run := &store.PipelineRun{ID: "run-" + designID, DesignID: designID, Project: project, CurrentPhase: phase, Status: "active", Mode: mode, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.runs[designID] = run
	return run, nil
}

func (f *completionFakeStore) GetRun(_ context.Context, designID string) (*store.PipelineRun, error) {
	run, ok := f.runs[designID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return run, nil
}

func (f *completionFakeStore) ListRuns(_ context.Context, _ string) ([]store.PipelineRunStatus, error) {
	return nil, nil
}

func (f *completionFakeStore) UpdateRunPhase(_ context.Context, designID, phase string) error {
	run, ok := f.runs[designID]
	if !ok {
		return fmt.Errorf("no pipeline run for %s", designID)
	}
	run.CurrentPhase = phase
	run.UpdatedAt = time.Now()
	return nil
}

func (f *completionFakeStore) CancelRunningSessions(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (f *completionFakeStore) CancelRunningSessionsForShard(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (f *completionFakeStore) MarkSessionEarlyDeath(_ context.Context, _, _ string) error {
	return nil
}

func (f *completionFakeStore) AdvancePhase(_ context.Context, designID, expectedCurrent, nextPhase string) error {
	run, ok := f.runs[designID]
	if !ok {
		return fmt.Errorf("no pipeline run for %s", designID)
	}
	if run.CurrentPhase != expectedCurrent {
		return fmt.Errorf("expected phase %q but pipeline is in %q: %w", expectedCurrent, run.CurrentPhase, store.ErrPhaseConflict)
	}
	run.CurrentPhase = nextPhase
	run.UpdatedAt = time.Now()
	return nil
}

func (f *completionFakeStore) UpdateRunStatus(_ context.Context, designID, status string) error {
	run, ok := f.runs[designID]
	if !ok {
		return fmt.Errorf("no pipeline run for %s", designID)
	}
	run.Status = status
	run.UpdatedAt = time.Now()
	return nil
}

func (f *completionFakeStore) SetRunMode(_ context.Context, designID, mode string) error {
	run, ok := f.runs[designID]
	if !ok {
		return fmt.Errorf("no pipeline run for %s", designID)
	}
	run.Mode = mode
	return nil
}

func (f *completionFakeStore) ResetRun(_ context.Context, designID, phase string) error {
	run, ok := f.runs[designID]
	if !ok {
		return fmt.Errorf("no pipeline run for %s", designID)
	}
	run.CurrentPhase = phase
	run.Status = "active"
	delete(f.gates, designID)
	delete(f.tasks, designID)
	return nil
}

func (f *completionFakeStore) RecordGate(_ context.Context, input store.PipelineGateInput) (*store.PipelineGateRecord, error) {
	rec := store.PipelineGateRecord{
		ID:            fmt.Sprintf("gate-%d", len(f.gates[input.DesignID])+1),
		PipelineID:    input.PipelineID,
		DesignID:      input.DesignID,
		GateName:      input.GateName,
		Phase:         input.Phase,
		Round:         len(f.gates[input.DesignID]) + 1,
		Verdict:       input.Verdict,
		Body:          input.Body,
		ReviewShardID: input.ReviewShardID,
		CreatedAt:     time.Now(),
	}
	f.gates[input.DesignID] = append(f.gates[input.DesignID], rec)
	return &rec, nil
}

func (f *completionFakeStore) GetGateHistory(_ context.Context, designID string) ([]store.PipelineGateRecord, error) {
	return append([]store.PipelineGateRecord(nil), f.gates[designID]...), nil
}

func (f *completionFakeStore) GetLatestGateRound(_ context.Context, pipelineID, gateName string) (int, error) {
	maxRound := 0
	for _, list := range f.gates {
		for _, gate := range list {
			if gate.PipelineID == pipelineID && gate.GateName == gateName && gate.Round > maxRound {
				maxRound = gate.Round
			}
		}
	}
	return maxRound, nil
}

func (f *completionFakeStore) AddTask(_ context.Context, pipelineID, taskShardID, designID string, wave *int) error {
	f.tasks[pipelineID] = append(f.tasks[pipelineID], store.PipelineTaskRecord{
		ID:          fmt.Sprintf("pt-%d", len(f.tasks[pipelineID])+1),
		PipelineID:  pipelineID,
		TaskShardID: taskShardID,
		DesignID:    designID,
		Wave:        wave,
		Status:      "pending",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	})
	return nil
}

func (f *completionFakeStore) ListTasks(_ context.Context, pipelineID string) ([]store.PipelineTaskRecord, error) {
	return append([]store.PipelineTaskRecord(nil), f.tasks[pipelineID]...), nil
}

func (f *completionFakeStore) ListTasksByDesign(_ context.Context, designID string) ([]store.PipelineTaskRecord, error) {
	var out []store.PipelineTaskRecord
	for _, tasks := range f.tasks {
		for _, task := range tasks {
			if task.DesignID == designID {
				out = append(out, task)
			}
		}
	}
	return out, nil
}

func (f *completionFakeStore) GetTaskByShardID(_ context.Context, taskShardID string) (*store.PipelineTaskRecord, error) {
	for _, tasks := range f.tasks {
		for _, task := range tasks {
			if task.TaskShardID == taskShardID {
				cp := task
				return &cp, nil
			}
		}
	}
	return nil, store.ErrNotFound
}

func (f *completionFakeStore) GetTasksByWave(_ context.Context, designID string, wave int) ([]store.PipelineTaskRecord, error) {
	var out []store.PipelineTaskRecord
	for _, tasks := range f.tasks {
		for _, task := range tasks {
			if task.DesignID == designID && task.Wave != nil && *task.Wave == wave {
				out = append(out, task)
			}
		}
	}
	return out, nil
}

func (f *completionFakeStore) IsWaveClosed(_ context.Context, designID string, wave int) (bool, error) {
	tasks, _ := f.GetTasksByWave(context.Background(), designID, wave)
	if len(tasks) == 0 {
		return false, nil
	}
	for _, task := range tasks {
		if task.Status != "closed" {
			return false, nil
		}
	}
	return true, nil
}

func (f *completionFakeStore) UpdateTaskStatus(_ context.Context, taskShardID, status string) error {
	for pipelineID := range f.tasks {
		for i := range f.tasks[pipelineID] {
			if f.tasks[pipelineID][i].TaskShardID == taskShardID {
				f.tasks[pipelineID][i].Status = status
				f.tasks[pipelineID][i].UpdatedAt = time.Now()
			}
		}
	}
	return nil
}

func (f *completionFakeStore) CreateSession(_ context.Context, input store.SessionInput) (*store.SessionRecord, error) {
	rec := &store.SessionRecord{ID: "sess-" + input.TaskID, TaskID: input.TaskID, Status: "running"}
	f.sessions[rec.ID] = rec
	return rec, nil
}

func (f *completionFakeStore) EndSession(_ context.Context, id string, result store.SessionResult) error {
	session, ok := f.sessions[id]
	if !ok {
		return fmt.Errorf("session not found")
	}
	now := time.Now()
	session.EndedAt = &now
	session.Status = result.Status
	sessionLog := result.SessionLog
	if sessionLog != "" {
		session.Error = &sessionLog
	}
	if result.PRURL != "" {
		session.PRURL = &result.PRURL
	} else {
		empty := ""
		session.PRURL = &empty
	}
	return nil
}

func (f *completionFakeStore) GetSession(_ context.Context, taskID string) (*store.SessionRecord, error) {
	for _, session := range f.sessions {
		if session.TaskID == taskID {
			return session, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *completionFakeStore) ListSessions(_ context.Context, _ string) ([]store.SessionRecord, error) {
	return nil, nil
}

func (f *completionFakeStore) ListRunningSessions(_ context.Context, _ string) ([]store.SessionRecord, error) {
	return nil, nil
}

func (f *completionFakeStore) GetRunStatusCounts(_ context.Context, _ string) (map[string]int, error) {
	return nil, nil
}

func (f *completionFakeStore) GetTaskStatusCounts(_ context.Context, _ string) (map[string]int, error) {
	return nil, nil
}

func (f *completionFakeStore) GetGatePassRates(_ context.Context, _ string) ([]store.GatePassRate, error) {
	return nil, nil
}

func (f *completionFakeStore) GetGateFailures(_ context.Context, _ string) ([]store.PipelineGateRecord, error) {
	return nil, nil
}

func (f *completionFakeStore) GetAvgTaskDuration(_ context.Context, _ string) (*float64, error) {
	return nil, nil
}

func (f *completionFakeStore) Migrate(context.Context) error { return nil }

func (f *completionFakeStore) Close() error { return nil }

func (f *completionFakeStore) taskStatus(pipelineID, taskID string) string {
	for _, task := range f.tasks[pipelineID] {
		if task.TaskShardID == taskID {
			return task.Status
		}
	}
	return ""
}

func cloneMetadata(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
