package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
)

func TestMergeSucceedsWhenLocalCleanupFails(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:     "cb-task",
		Type:   "task",
		Status: "open",
		Metadata: map[string]any{
			domain.MetaPRURL:        "https://github.com/acme/cobuild/pull/123",
			domain.MetaWorktreePath: t.TempDir(),
		},
	})

	restore := installTestGlobals(t, fc, nil, "test-project")
	defer restore()

	prevOutput := execCommandOutput
	prevCombined := execCommandCombinedOutput
	prevCleanup := mergeCleanupTaskResources
	prevConfigLoader := cleanupConfigLoader
	t.Cleanup(func() {
		execCommandOutput = prevOutput
		execCommandCombinedOutput = prevCombined
		mergeCleanupTaskResources = prevCleanup
		cleanupConfigLoader = prevConfigLoader
	})

	cleanupConfigLoader = func(string) *config.Config {
		return config.DefaultConfig()
	}
	execCommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "gh" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		call := strings.Join(args, " ")
		switch call {
		case "pr view https://github.com/acme/cobuild/pull/123 --json state,reviewDecision,mergeable --jq [.state, .reviewDecision, .mergeable] | join(\",\")":
			return []byte("OPEN,APPROVED,MERGEABLE"), nil
		default:
			return nil, fmt.Errorf("unexpected gh output call %q", call)
		}
	}
	execCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "gh" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		call := strings.Join(args, " ")
		if call != "pr merge https://github.com/acme/cobuild/pull/123 --squash --delete-branch" {
			return nil, fmt.Errorf("unexpected gh merge call %q", call)
		}
		return []byte("merged"), nil
	}
	mergeCleanupTaskResources = func(_ context.Context, taskID string) error {
		return fmt.Errorf("worktree busy")
	}

	out, err := runCommandWithOutputs(t, mergeCmd, []string{"cb-task"})
	if err != nil {
		t.Fatalf("merge returned error after successful remote merge: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Merging https://github.com/acme/cobuild/pull/123...",
		"  Merged.",
		"Warning: merge succeeded, but local cleanup failed: worktree busy",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("merge output missing %q:\n%s", want, out)
		}
	}
}

func TestMergeSkipsAutoCleanupWhenDisabled(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:     "cb-task",
		Type:   "task",
		Status: "open",
		Metadata: map[string]any{
			domain.MetaPRURL: "https://github.com/acme/cobuild/pull/123",
		},
	})

	restore := installTestGlobals(t, fc, nil, "test-project")
	defer restore()

	prevOutput := execCommandOutput
	prevCombined := execCommandCombinedOutput
	prevCleanup := mergeCleanupTaskResources
	prevConfigLoader := cleanupConfigLoader
	t.Cleanup(func() {
		execCommandOutput = prevOutput
		execCommandCombinedOutput = prevCombined
		mergeCleanupTaskResources = prevCleanup
		cleanupConfigLoader = prevConfigLoader
	})

	calledCleanup := false
	mergeCleanupTaskResources = func(_ context.Context, taskID string) error {
		calledCleanup = true
		return nil
	}
	cleanupConfigLoader = func(string) *config.Config {
		disabled := false
		return &config.Config{Cleanup: config.CleanupCfg{AutoOnMerge: &disabled}}
	}
	execCommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "gh" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		call := strings.Join(args, " ")
		if call == "pr view https://github.com/acme/cobuild/pull/123 --json state,reviewDecision,mergeable --jq [.state, .reviewDecision, .mergeable] | join(\",\")" {
			return []byte("OPEN,APPROVED,MERGEABLE"), nil
		}
		return nil, fmt.Errorf("unexpected gh output call %q", call)
	}
	execCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "gh" {
			return nil, fmt.Errorf("unexpected command %q", name)
		}
		call := strings.Join(args, " ")
		if call != "pr merge https://github.com/acme/cobuild/pull/123 --squash --delete-branch" {
			return nil, fmt.Errorf("unexpected gh merge call %q", call)
		}
		return []byte("merged"), nil
	}

	if _, err := runCommandWithOutputs(t, mergeCmd, []string{"cb-task"}); err != nil {
		t.Fatalf("merge returned error: %v", err)
	}
	if calledCleanup {
		t.Fatalf("cleanup was called with cleanup.auto_on_merge=false")
	}
}
