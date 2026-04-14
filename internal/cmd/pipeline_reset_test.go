package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/testutil/pgtest"
)

func TestRunPipelineResetPerformsFullCleanupAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fc := newFakeConnector()
	fs := newFakeStore()

	designID := "cb-reset"
	taskID := "cb-task-1"
	worktreePath := filepath.Join(t.TempDir(), taskID)
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	fs.runs[designID] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     designID,
		CurrentPhase: "review",
		Status:       "active",
	}
	wave := 1
	fs.tasks = []store.PipelineTaskRecord{
		{PipelineID: "run-1", TaskShardID: taskID, DesignID: designID, Wave: &wave, Status: "needs-review"},
	}
	fs.sessions = []store.SessionRecord{
		{
			ID:           "ps-1",
			PipelineID:   "run-1",
			DesignID:     designID,
			TaskID:       taskID,
			Status:       "running",
			StartedAt:    time.Now().Add(-10 * time.Minute),
			WorktreePath: strPtr(worktreePath),
		},
	}

	fc.addItem(&connector.WorkItem{ID: designID, Title: "Reset design", Type: "design", Status: "needs-review"})
	fc.addItem(&connector.WorkItem{
		ID:     taskID,
		Title:  "Child task",
		Type:   "task",
		Status: "needs-review",
		Metadata: map[string]any{
			"pr_url":        "https://github.com/acme/cobuild/pull/42",
			"worktree_path": worktreePath,
		},
	})

	restore := installTestGlobals(t, fc, fs, "cobuild")
	defer restore()

	prevOutput := pipelineCommandOutput
	prevCombined := pipelineCommandCombinedOutput
	prevRun := pipelineCommandRun
	prevConfig := pipelineConfigLoader
	t.Cleanup(func() {
		pipelineCommandOutput = prevOutput
		pipelineCommandCombinedOutput = prevCombined
		pipelineCommandRun = prevRun
		pipelineConfigLoader = prevConfig
	})

	var killedPIDs []string
	var tmuxKills []string
	var closedPRs []string
	prStillOpen := true
	socketPath := filepath.Join(t.TempDir(), "reset.sock")

	pipelineConfigLoader = func() *config.Config {
		cfg := config.DefaultConfig()
		cfg.Dispatch.TmuxSocket = socketPath
		return cfg
	}
	pipelineCommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "git" {
			return nil, fmt.Errorf("no git metadata")
		}
		return nil, fmt.Errorf("unexpected output command %s %s", name, strings.Join(args, " "))
	}
	pipelineCommandRun = func(ctx context.Context, name string, args ...string) error {
		switch {
		case name == "kill":
			killedPIDs = append(killedPIDs, strings.Join(args, " "))
			return nil
		default:
			return fmt.Errorf("unexpected run command %s %s", name, strings.Join(args, " "))
		}
	}
	pipelineCommandCombinedOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		switch call {
		case "ps auxww":
			return []byte("USER PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND\njames 321 0.0 0.1 100 100 ?? S 10:30 0:00.01 cobuild orchestrate cb-reset --project cobuild\n"), nil
		case "tmux -S " + socketPath + " list-sessions -F #{session_name}":
			return []byte("cobuild-cobuild\n"), nil
		case "tmux -S " + socketPath + " list-windows -t cobuild-cobuild -F #{window_id}\t#{window_name}":
			return []byte("@7\tcb-reset\n"), nil
		case "tmux -S " + socketPath + " kill-window -t @7":
			tmuxKills = append(tmuxKills, "@7")
			return []byte(""), nil
		case "gh pr list --repo acme/cobuild --state open --json number,title,headRefName,mergeable,url --limit 100":
			if prStillOpen {
				return []byte(`[{"number":42,"title":"Reset task","headRefName":"cb-task-1","mergeable":"MERGEABLE","url":"https://github.com/acme/cobuild/pull/42"}]`), nil
			}
			return []byte("[]"), nil
		case "gh api repos/acme/cobuild/branches/cb-task-1":
			return []byte(`{"name":"cb-task-1"}`), nil
		case "gh pr close 42 --repo acme/cobuild --comment Closed by cobuild reset cb-reset":
			prStillOpen = false
			closedPRs = append(closedPRs, "42")
			return []byte("closed"), nil
		default:
			return nil, fmt.Errorf("unexpected combined command %s", call)
		}
	}

	if err := runPipelineReset(ctx, designID, resetOptions{Phase: "implement", ForceClosePRs: true}); err != nil {
		t.Fatalf("runPipelineReset() error = %v", err)
	}

	if got := fs.runs[designID].CurrentPhase; got != "implement" {
		t.Fatalf("run phase = %q, want implement", got)
	}
	if got := fs.runs[designID].Status; got != "active" {
		t.Fatalf("run status = %q, want active", got)
	}
	if fs.resetCalls != 1 {
		t.Fatalf("resetCalls = %d, want 1", fs.resetCalls)
	}
	if len(fs.tasks) != 1 || fs.tasks[0].TaskShardID != taskID || fs.tasks[0].Status != "needs-review" {
		t.Fatalf("tasks after reset = %#v, want preserved task", fs.tasks)
	}
	if len(fs.ended) != 1 || fs.ended["ps-1"].Status != "cancelled" {
		t.Fatalf("ended sessions = %#v, want cancelled running session", fs.ended)
	}
	if note := fs.ended["ps-1"].CompletionNote; !strings.Contains(note, "cb-reset") {
		t.Fatalf("completion note = %q, want reset note", note)
	}
	if fc.items[designID].Status != "open" {
		t.Fatalf("design status = %q, want open", fc.items[designID].Status)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after reset: err = %v", err)
	}
	if got, _ := fc.GetMetadata(ctx, taskID, "worktree_path"); got != "" {
		t.Fatalf("worktree_path metadata = %q, want cleared", got)
	}
	if len(killedPIDs) != 1 || killedPIDs[0] != "321" {
		t.Fatalf("killed pids = %#v, want [321]", killedPIDs)
	}
	if len(tmuxKills) != 1 || tmuxKills[0] != "@7" {
		t.Fatalf("tmux kills = %#v, want [@7]", tmuxKills)
	}
	if len(closedPRs) != 1 || closedPRs[0] != "42" {
		t.Fatalf("closed PRs = %#v, want [42]", closedPRs)
	}

	if err := runPipelineReset(ctx, designID, resetOptions{Phase: "implement", ForceClosePRs: true}); err != nil {
		t.Fatalf("second runPipelineReset() error = %v", err)
	}
	if fs.resetCalls != 2 {
		t.Fatalf("resetCalls after second run = %d, want 2", fs.resetCalls)
	}
	if len(fs.tasks) != 1 || fs.tasks[0].TaskShardID != taskID || fs.tasks[0].Status != "needs-review" {
		t.Fatalf("tasks after second reset = %#v, want same preserved task", fs.tasks)
	}
	if len(closedPRs) != 1 {
		t.Fatalf("closed PRs after second reset = %#v, want unchanged", closedPRs)
	}
}

func TestResetCommandIntegrationCleansTmuxWorktreeAndStoreState(t *testing.T) {
	ctx := context.Background()
	tmux := withTestTmux(t)
	pgtest.Skip(t, ctx)
	pg := pgtest.New(t, ctx)

	designID := fmt.Sprintf("cb-reset-int-%d", time.Now().UnixNano())
	project := "test-project"
	sessionName := "cobuild-" + project
	worktreeRepo := t.TempDir()
	worktreePath := filepath.Join(t.TempDir(), designID)

	runGit(t, worktreeRepo, "init", "-b", "main")
	runGit(t, worktreeRepo, "config", "user.email", "test@example.com")
	runGit(t, worktreeRepo, "config", "user.name", "Test User")
	writeFile(t, filepath.Join(worktreeRepo, "README.md"), "initial\n")
	runGit(t, worktreeRepo, "add", "README.md")
	runGit(t, worktreeRepo, "commit", "-m", "initial")
	runGit(t, worktreeRepo, "worktree", "add", "-b", designID, worktreePath, "main")

	run, err := pg.Store.CreateRun(ctx, designID, project, "review")
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	t.Cleanup(func() {
		pg.CleanupDesign(t, ctx, designID)
	})
	if _, err := pg.Store.CreateSession(ctx, store.SessionInput{
		PipelineID:   run.ID,
		DesignID:     designID,
		TaskID:       designID,
		Phase:        "review",
		Project:      project,
		WorktreePath: worktreePath,
		TmuxSession:  sessionName,
		TmuxWindow:   designID,
	}); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	tmux.mustRun(t, "new-session", "-d", "-s", sessionName, "-n", designID, "sleep", "300")

	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:      designID,
		Title:   "Reset integration task",
		Type:    "task",
		Status:  "in_progress",
		Content: "Reset this task pipeline",
		Metadata: map[string]any{
			"worktree_path": worktreePath,
		},
	})

	restore := installTestGlobals(t, fc, pg.Store, project)
	defer restore()

	setCommandFlag(t, resetCmd, "phase", "implement")
	t.Cleanup(func() {
		setCommandFlag(t, resetCmd, "phase", "")
		setCommandFlag(t, resetCmd, "keep-worktree", "false")
		setCommandFlag(t, resetCmd, "force-close-prs", "false")
	})

	if _, err := runCommandWithOutputs(t, resetCmd, []string{designID}); err != nil {
		t.Fatalf("resetCmd failed: %v", err)
	}
	if _, err := runCommandWithOutputs(t, resetCmd, []string{designID}); err != nil {
		t.Fatalf("second resetCmd failed: %v", err)
	}

	if tmux.hasWindow(t, sessionName, designID) {
		t.Fatalf("tmux window %s:%s still exists after reset", sessionName, designID)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after reset: err = %v", err)
	}
	sessions, err := pg.Store.ListSessions(ctx, designID)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions after reset = %#v, want none", sessions)
	}
	runState, err := pg.Store.GetRun(ctx, designID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if runState.CurrentPhase != "implement" || runState.Status != "active" {
		t.Fatalf("run after reset = %#v, want implement/active", runState)
	}
	if fc.items[designID].Status != "open" {
		t.Fatalf("work item status = %q, want open", fc.items[designID].Status)
	}
}
