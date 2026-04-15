package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestRebaseSiblingsCommandUpdatesStatusesAndWarnsOnConflict(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()
	designID := "cb-design"

	openWT := t.TempDir()
	conflictWT := t.TempDir()

	fc.addItem(&connector.WorkItem{ID: "cb-task-merged", Type: "task", Status: "closed"})
	fc.addItem(&connector.WorkItem{ID: "cb-task-open", Type: "task", Status: "needs-review"})
	fc.addItem(&connector.WorkItem{ID: "cb-task-conflict", Type: "task", Status: "in_progress"})
	fc.metadata["cb-task-open"] = map[string]string{domain.MetaWorktreePath: openWT}
	fc.metadata["cb-task-conflict"] = map[string]string{domain.MetaWorktreePath: conflictWT}

	fs.tasks = []store.PipelineTaskRecord{
		{TaskShardID: "cb-task-merged", DesignID: designID, Status: "closed"},
		{TaskShardID: "cb-task-open", DesignID: designID, Status: domain.StatusNeedsReview},
		{TaskShardID: "cb-task-conflict", DesignID: designID, Status: domain.StatusInProgress},
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevOutput := execCommandOutput
	prevCombined := execCommandCombinedOutput
	prevWarnings := rebaseWarningWriter
	t.Cleanup(func() {
		execCommandOutput = prevOutput
		execCommandCombinedOutput = prevCombined
		rebaseWarningWriter = prevWarnings
	})

	var warnings bytes.Buffer
	rebaseWarningWriter = &warnings

	execCommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "git" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		call := strings.Join(args, " ")
		switch call {
		case "-C " + openWT + " branch --show-current":
			return []byte("cb-task-open\n"), nil
		case "-C " + conflictWT + " branch --show-current":
			return []byte("cb-task-conflict\n"), nil
		default:
			return nil, fmt.Errorf("unexpected git output call %q", call)
		}
	}
	execCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "git" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		call := strings.Join(args, " ")
		switch call {
		case "-C " + openWT + " fetch origin main":
			return []byte(""), nil
		case "-C " + openWT + " rebase origin/main":
			return []byte(""), nil
		case "-C " + openWT + " push --force-with-lease":
			return []byte(""), nil
		case "-C " + conflictWT + " fetch origin main":
			return []byte(""), nil
		case "-C " + conflictWT + " rebase origin/main":
			return []byte("conflict"), fmt.Errorf("conflict")
		case "-C " + conflictWT + " rebase --abort":
			return []byte(""), nil
		default:
			return nil, fmt.Errorf("unexpected git combined call %q", call)
		}
	}

	out, err := runCommandWithOutputs(t, rebaseSiblingsCmd, []string{designID})
	if err != nil {
		t.Fatalf("rebase-siblings returned error: %v\n%s", err, out)
	}

	if !strings.Contains(out, "Sibling rebase complete for cb-design: 1 rebased, 1 conflict(s), 0 warning(s).") {
		t.Fatalf("output = %q, want summary", out)
	}
	if fs.tasks[1].RebaseStatus != domain.RebaseStatusRebased {
		t.Fatalf("open task rebase status = %q, want %q", fs.tasks[1].RebaseStatus, domain.RebaseStatusRebased)
	}
	if fs.tasks[2].RebaseStatus != domain.RebaseStatusConflict {
		t.Fatalf("conflict task rebase status = %q, want %q", fs.tasks[2].RebaseStatus, domain.RebaseStatusConflict)
	}
	if !strings.Contains(warnings.String(), "Sibling cb-task-conflict has rebase conflict with main; manual resolution needed.") {
		t.Fatalf("warnings = %q, want conflict warning", warnings.String())
	}
}

func TestRebaseCommandResolvesDesignFromTask(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()
	designID := "cb-design"
	worktreePath := t.TempDir()

	fc.addItem(&connector.WorkItem{ID: "cb-task-merged", Type: "task", Status: "closed"})
	fc.addItem(&connector.WorkItem{ID: "cb-task-open", Type: "task", Status: "needs-review"})
	fc.metadata["cb-task-open"] = map[string]string{domain.MetaWorktreePath: worktreePath}

	fs.tasks = []store.PipelineTaskRecord{
		{TaskShardID: "cb-task-merged", DesignID: designID, Status: "closed"},
		{TaskShardID: "cb-task-open", DesignID: designID, Status: domain.StatusNeedsReview},
	}

	restore := installTestGlobals(t, fc, fs, "test-project")
	defer restore()

	prevOutput := execCommandOutput
	prevCombined := execCommandCombinedOutput
	t.Cleanup(func() {
		execCommandOutput = prevOutput
		execCommandCombinedOutput = prevCombined
	})

	execCommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "git" || strings.Join(args, " ") != "-C "+worktreePath+" branch --show-current" {
			return nil, fmt.Errorf("unexpected command %q %q", name, strings.Join(args, " "))
		}
		return []byte("cb-task-open\n"), nil
	}
	execCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "git" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		return []byte(""), nil
	}

	out, err := runCommandWithOutputs(t, rebaseCmd, []string{"cb-task-merged"})
	if err != nil {
		t.Fatalf("rebase returned error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Sibling rebase complete for cb-design: 1 rebased, 0 conflict(s), 0 warning(s).") {
		t.Fatalf("output = %q, want task-based rebase summary", out)
	}
}
