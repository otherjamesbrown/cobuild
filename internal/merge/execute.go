package merge

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ExecuteResult tracks the outcome of each merge step.
type ExecuteResult struct {
	TaskID  string `json:"task_id"`
	Action  string `json:"action"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Commit  string `json:"commit,omitempty"`
}

// ExecutePlan runs the merge plan step by step.
// testCmd is the command to run after each merge (e.g., "go test ./...").
// If dryRun is true, logs actions without executing.
func ExecutePlan(ctx context.Context, repoRoot string, plan *MergePlan, testCmds []string, dryRun bool) ([]ExecuteResult, error) {
	var results []ExecuteResult

	for _, entry := range plan.Entries {
		fmt.Printf("\n[%s] %s %s\n", entry.Action, entry.TaskID, entry.Branch)

		var result ExecuteResult
		result.TaskID = entry.TaskID
		result.Action = string(entry.Action)

		switch entry.Action {
		case ActionSkip:
			fmt.Printf("  Skipping: %s\n", entry.Note)
			if !dryRun && entry.PR != "" {
				// Close the PR
				out, err := exec.CommandContext(ctx, "gh", "pr", "close", entry.PR).CombinedOutput()
				if err != nil {
					fmt.Printf("  Warning: failed to close PR %s: %s\n", entry.PR, string(out))
				} else {
					fmt.Printf("  PR %s closed.\n", entry.PR)
				}
			}
			result.Success = true
			results = append(results, result)
			continue

		case ActionPartialMerge:
			if dryRun {
				fmt.Printf("  [dry-run] Would cherry-pick files: %s\n", strings.Join(entry.IncludeFiles, ", "))
				fmt.Printf("  [dry-run] Would skip files: %s\n", strings.Join(entry.SkipFiles, ", "))
				result.Success = true
				results = append(results, result)
				continue
			}

			err := partialMerge(ctx, repoRoot, entry)
			if err != nil {
				result.Error = err.Error()
				fmt.Printf("  ERROR: %v\n", err)
				results = append(results, result)
				return results, fmt.Errorf("partial merge failed for %s: %w", entry.TaskID, err)
			}
			result.Success = true

		case ActionMerge:
			if dryRun {
				fmt.Printf("  [dry-run] Would rebase and squash merge via PR\n")
				result.Success = true
				results = append(results, result)
				continue
			}

			err := fullMerge(ctx, repoRoot, entry)
			if err != nil {
				result.Error = err.Error()
				fmt.Printf("  ERROR: %v\n", err)
				results = append(results, result)
				return results, fmt.Errorf("merge failed for %s: %w", entry.TaskID, err)
			}
			result.Success = true
		}

		// Get the merge commit
		commitOut, _ := exec.CommandContext(ctx, "git", "-C", repoRoot, "log", "--oneline", "-1").CombinedOutput()
		result.Commit = strings.TrimSpace(string(commitOut))

		// Run tests after each merge
		if len(testCmds) > 0 && !dryRun {
			fmt.Printf("  Running tests...\n")
			testFailed := false
			for _, testCmd := range testCmds {
				parts := strings.Fields(testCmd)
				out, err := exec.CommandContext(ctx, parts[0], parts[1:]...).CombinedOutput()
				if err != nil {
					fmt.Printf("  TEST FAILED: %s\n%s\n", testCmd, string(out))
					testFailed = true
					break
				}
			}
			if testFailed {
				// Revert the merge
				fmt.Printf("  Reverting merge...\n")
				exec.CommandContext(ctx, "git", "-C", repoRoot, "revert", "--no-edit", "HEAD").Run()
				exec.CommandContext(ctx, "git", "-C", repoRoot, "push").Run()
				result.Success = false
				result.Error = "post-merge tests failed, reverted"
				results = append(results, result)
				return results, fmt.Errorf("post-merge tests failed for %s, reverted", entry.TaskID)
			}
			fmt.Printf("  Tests passed.\n")
		}

		results = append(results, result)
	}

	return results, nil
}

func fullMerge(ctx context.Context, repoRoot string, entry MergePlanEntry) error {
	// Rebase onto main
	fmt.Printf("  Rebasing %s onto main...\n", entry.Branch)
	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "rebase", "main", entry.Branch).CombinedOutput()
	if err != nil {
		// Abort the rebase
		exec.CommandContext(ctx, "git", "-C", repoRoot, "rebase", "--abort").Run()
		if strings.Contains(string(out), "CONFLICT") {
			return fmt.Errorf("rebase conflict — manual resolution needed:\n%s", string(out))
		}
		return fmt.Errorf("rebase failed: %s\n%s", err, string(out))
	}

	// Push the rebased branch
	exec.CommandContext(ctx, "git", "-C", repoRoot, "push", "--force-with-lease", "origin", entry.Branch).Run()

	// Merge via gh (squash)
	if entry.PR != "" {
		fmt.Printf("  Merging PR %s...\n", entry.PR)
		out, err := exec.CommandContext(ctx, "gh", "pr", "merge", entry.PR, "--squash", "--delete-branch").CombinedOutput()
		if err != nil {
			return fmt.Errorf("gh pr merge failed: %s\n%s", err, string(out))
		}
	} else {
		// No PR — merge directly
		fmt.Printf("  Merging branch directly...\n")
		exec.CommandContext(ctx, "git", "-C", repoRoot, "checkout", "main").Run()
		out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "merge", "--squash", entry.Branch).CombinedOutput()
		if err != nil {
			return fmt.Errorf("direct merge failed: %s\n%s", err, string(out))
		}
		exec.CommandContext(ctx, "git", "-C", repoRoot, "commit", "-m",
			fmt.Sprintf("Merge %s: %s", entry.TaskID, entry.Branch)).Run()
	}

	// Pull to get the merged state
	exec.CommandContext(ctx, "git", "-C", repoRoot, "checkout", "main").Run()
	exec.CommandContext(ctx, "git", "-C", repoRoot, "pull").Run()

	fmt.Printf("  Merged.\n")
	return nil
}

func partialMerge(ctx context.Context, repoRoot string, entry MergePlanEntry) error {
	// Checkout main
	exec.CommandContext(ctx, "git", "-C", repoRoot, "checkout", "main").Run()

	// Cherry-pick only the included files
	fmt.Printf("  Cherry-picking files: %s\n", strings.Join(entry.IncludeFiles, ", "))
	for _, file := range entry.IncludeFiles {
		out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "checkout", entry.Branch, "--", file).CombinedOutput()
		if err != nil {
			return fmt.Errorf("checkout %s from %s: %s\n%s", file, entry.Branch, err, string(out))
		}
	}

	// Commit
	exec.CommandContext(ctx, "git", "-C", repoRoot, "add", "-A").Run()
	msg := fmt.Sprintf("Partial merge from %s: %s", entry.TaskID, strings.Join(entry.IncludeFiles, ", "))
	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "commit", "-m", msg).CombinedOutput()
	if err != nil {
		return fmt.Errorf("commit partial merge: %s\n%s", err, string(out))
	}

	// Push
	exec.CommandContext(ctx, "git", "-C", repoRoot, "push").Run()

	// Close the PR since we cherry-picked instead of merging
	if entry.PR != "" {
		exec.CommandContext(ctx, "gh", "pr", "close", entry.PR).Run()
		fmt.Printf("  PR %s closed (partial merge).\n", entry.PR)
	}

	fmt.Printf("  Partial merge committed.\n")
	return nil
}
