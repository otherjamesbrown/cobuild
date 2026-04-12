package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

var completeCmd = &cobra.Command{
	Use:   "complete <task-id>",
	Short: "Post-agent completion: push, create PR, mark needs-review",
	Long: `Runs deterministic bookkeeping after an agent finishes implementing a task.

Steps:
  1. Check for uncommitted changes in worktree -> commit them
  2. Push the branch
  3. Create PR if it doesn't exist
  4. Append evidence (files changed, commit hash)
  5. Mark task needs-review`,
	Args:    cobra.ExactArgs(1),
	Example: "  cobuild complete pf-abc123",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]
		autoFlag, _ := cmd.Flags().GetBool("auto")

		task, err := conn.Get(ctx, taskID)
		if err != nil {
			return fmt.Errorf("task not found: %s", taskID)
		}

		if task.Status == "closed" {
			fmt.Printf("Task %s already closed, skipping\n", taskID)
			return nil
		}

		if task.Status == "needs-review" {
			if autoFlag {
				// Idempotent: Stop hook fired after dispatch script already completed it
				fmt.Printf("Task %s already needs-review, skipping (--auto)\n", taskID)
				return nil
			}
			return validateCompletionViaConnector(ctx, taskID, task)
		}

		worktreePath, _ := conn.GetMetadata(ctx, taskID, "worktree_path")
		directDecision, err := detectDirectCompletion(ctx, task, worktreePath)
		if err != nil {
			return fmt.Errorf("detect completion mode: %w", err)
		}
		if worktreePath == "" && !directDecision.direct {
			if autoFlag {
				fmt.Fprintf(os.Stderr, "Warning: --auto: no worktree_path for %s, skipping\n", taskID)
				return nil
			}
			return fmt.Errorf("no worktree_path in task metadata")
		}

		repoRoot, _ := config.RepoForProject(projectName)
		pCfg, _ := config.LoadConfig(repoRoot)

		// When triggered by Stop hook, verify the agent actually completed work before proceeding
		if autoFlag && worktreePath != "" && !directDecision.direct {
			baseBranch := "main"
			if refOut, err := exec.CommandContext(ctx, "git", "-C", worktreePath, "symbolic-ref", "refs/remotes/origin/HEAD").Output(); err == nil {
				// returns "refs/remotes/origin/main" — strip prefix
				ref := strings.TrimSpace(string(refOut))
				if idx := strings.LastIndex(ref, "/"); idx >= 0 {
					baseBranch = ref[idx+1:]
				}
			}
			commitOut, err := exec.CommandContext(ctx, "git", "-C", worktreePath, "log", "--oneline", baseBranch+"..HEAD").Output()
			if err != nil || len(strings.TrimSpace(string(commitOut))) == 0 {
				fmt.Fprintf(os.Stderr, "Warning: --auto: no commits on branch for %s, skipping (agent may not be done)\n", taskID)
				return nil
			}
			// Exclude dispatch-injected files (CLAUDE.md, .cobuild/) — they are always modified
			// by dispatch and do not represent uncommitted agent work
			statusOut, err := exec.CommandContext(ctx, "git", "-C", worktreePath, "status", "--porcelain", "--", ".", ":!CLAUDE.md", ":!.cobuild").Output()
			if err == nil && len(strings.TrimSpace(string(statusOut))) > 0 {
				fmt.Fprintf(os.Stderr, "Warning: --auto: dirty worktree for %s, skipping (uncommitted changes present)\n", taskID)
				return nil
			}
			fmt.Printf("Stop hook triggered completion for %s\n", taskID)
		}

		if directDecision.direct {
			return completeDirectTask(ctx, taskID, worktreePath, directDecision.reason, pCfg)
		}

		// Restore original CLAUDE.md
		exec.Command("git", "-C", worktreePath, "checkout", "main", "--", "CLAUDE.md").Run()

		// Commit uncommitted changes — exclude dispatch artifacts (.cobuild/, CLAUDE.md)
		// from both the dirty check and staging so they never appear in task commits.
		statusOut, err := exec.Command("git", "-C", worktreePath, "status", "--porcelain", "--", ".", ":!.cobuild", ":!CLAUDE.md").Output()
		if err == nil && len(strings.TrimSpace(string(statusOut))) > 0 {
			fmt.Println("Committing uncommitted changes...")
			exec.Command("git", "-C", worktreePath, "add", "--all", "--", ".", ":!.cobuild").Run()
			// Unstage previously-tracked .cobuild/ and CLAUDE.md files (belt-and-braces
			// for repos where these were committed by earlier buggy versions of cobuild).
			exec.Command("git", "-C", worktreePath, "reset", "HEAD", "--", ".cobuild", "CLAUDE.md").Run()
			// Re-check: if nothing is staged after exclusions, skip commit to avoid
			// "nothing to commit" error (happens when only .cobuild/ files changed).
			indexOut, err := exec.Command("git", "-C", worktreePath, "diff", "--cached", "--name-only").Output()
			if err == nil && len(strings.TrimSpace(string(indexOut))) > 0 {
				exec.Command("git", "-C", worktreePath, "commit", "-m",
					fmt.Sprintf("[%s] Auto-commit remaining changes", taskID)).Run()
			}
		}

		// Get branch name
		branchOut, err := exec.Command("git", "-C", worktreePath, "branch", "--show-current").Output()
		if err != nil {
			return fmt.Errorf("cannot get branch: %v", err)
		}
		branch := strings.TrimSpace(string(branchOut))
		if branch == "" {
			return fmt.Errorf("no branch found in worktree")
		}

		// Push branch
		fmt.Printf("Pushing branch %s...\n", branch)
		pushOut, err := exec.Command("git", "-C", worktreePath, "push", "-u", "origin", branch).CombinedOutput()
		if err != nil {
			fmt.Printf("Push warning: %s\n", strings.TrimSpace(string(pushOut)))
		}

		// Create PR — detect repo from the worktree's git remote (not pipeline config,
		// which may point to a different repo in multi-repo projects)
		prURL, _ := conn.GetMetadata(ctx, taskID, "pr_url")
		if prURL == "" {
			fmt.Println("Creating PR...")
			repo := ""
			// Primary: detect from worktree's git remote
			repoOut, err := exec.Command("git", "-C", worktreePath, "remote", "get-url", "origin").Output()
			if err == nil {
				url := strings.TrimSpace(string(repoOut))
				for _, prefix := range []string{"git@github.com:", "https://github.com/"} {
					if strings.HasPrefix(url, prefix) {
						repo = strings.TrimSuffix(strings.TrimPrefix(url, prefix), ".git")
						break
					}
				}
			}
			// Fallback: pipeline config
			if repo == "" && pCfg != nil && pCfg.GitHub.OwnerRepo != "" {
				repo = pCfg.GitHub.OwnerRepo
			}

			if repo != "" {
				prBody := fmt.Sprintf("## Task\n[%s] %s\n\n---\nPipeline task: %s", taskID, task.Title, taskID)
				ghArgs := []string{"pr", "create",
					"--repo", repo,
					"--head", branch,
					"--base", "main",
					"--title", fmt.Sprintf("%s (%s)", task.Title, taskID),
					"--body", prBody,
				}
				prOut, err := exec.Command("gh", ghArgs...).CombinedOutput()
				if err != nil {
					fmt.Printf("PR creation warning: %s\n", strings.TrimSpace(string(prOut)))
				} else {
					prURL = strings.TrimSpace(string(prOut))
					fmt.Printf("PR created: %s\n", prURL)
					conn.SetMetadata(ctx, taskID, "pr_url", prURL)
				}
			} else {
				fmt.Println("Could not detect GitHub repo -- skipping PR creation")
			}
		} else {
			fmt.Printf("PR already exists: %s\n", prURL)
		}

		// Append evidence
		commitOut, _ := exec.Command("git", "-C", worktreePath, "log", "--oneline", "-1").Output()
		commit := strings.TrimSpace(string(commitOut))

		diffOut, _ := exec.Command("git", "-C", worktreePath, "diff", "--stat", "main...HEAD").Output()
		filesChanged := strings.TrimSpace(string(diffOut))

		evidence := fmt.Sprintf("## Auto-completion evidence\n\nCommit: %s\nPR: %s\n\n### Files changed\n```\n%s\n```",
			commit, prURL, filesChanged)
		if conn != nil {
			if err := conn.AppendContent(ctx, taskID, evidence); err != nil {
				fmt.Printf("Warning: failed to append evidence: %v\n", err)
			}
		}

		// Mark needs-review
		fmt.Println("Marking needs-review...")
		if conn != nil {
			if err := conn.UpdateStatus(ctx, taskID, "needs-review"); err != nil {
				fmt.Printf("Warning: failed to set status: %v\n", err)
			}
		}
		syncPipelineTaskStatus(ctx, taskID, "needs-review")

		// Transition the pipeline run to the review phase so `cobuild status`
		// reflects where the work actually is (cb-2e5044). Best-effort — a missing
		// run is fine (direct dispatches without auto-create).
		if cbStore != nil {
			if err := cbStore.UpdateRunPhase(ctx, taskID, "review"); err != nil {
				// Silent on "no pipeline run" — happens for tasks with no tracked run
				if !strings.Contains(err.Error(), "no pipeline run") {
					fmt.Printf("Warning: failed to update pipeline run phase: %v\n", err)
				}
			}
		}

		// End session record in store
		filesCount := len(strings.Split(strings.TrimSpace(filesChanged), "\n"))
		if strings.TrimSpace(filesChanged) == "" {
			filesCount = 0
		}
		endTaskSession(ctx, taskID, worktreePath, store.SessionResult{
			ExitCode:     0,
			FilesChanged: filesCount,
			Commits:      1,
			PRURL:        prURL,
			Status:       "completed",
		})

		fmt.Printf("Task %s complete -> needs-review\n", taskID)
		printNextStep(taskID, "implement", "complete")
		return nil
	},
}

func validateCompletionViaConnector(ctx context.Context, taskID string, task *connector.WorkItem) error {
	issues := []string{}

	wtPath, _ := conn.GetMetadata(ctx, taskID, "worktree_path")

	if wtPath != "" {
		commitOut, err := exec.Command("git", "-C", wtPath, "log", "--oneline", "main..HEAD").Output()
		if err != nil || len(strings.TrimSpace(string(commitOut))) == 0 {
			issues = append(issues, "no commits on branch")
		}
	}

	prURL, _ := conn.GetMetadata(ctx, taskID, "pr_url")
	if prURL == "" {
		issues = append(issues, "no PR created")
		if wtPath != "" {
			fmt.Println("Validation: no PR -- creating one...")
			repoRoot, _ := config.RepoForProject(projectName)
			pCfg, _ := config.LoadConfig(repoRoot)
			repo := ""
			if pCfg != nil && pCfg.GitHub.OwnerRepo != "" {
				repo = pCfg.GitHub.OwnerRepo
			}
			if repo != "" {
				branch, _ := exec.Command("git", "-C", wtPath, "branch", "--show-current").Output()
				branchName := strings.TrimSpace(string(branch))
				exec.Command("git", "-C", wtPath, "push", "-u", "origin", branchName).Run()
				prBody := fmt.Sprintf("## Task\n[%s] %s\n\n---\nPipeline task: %s", taskID, task.Title, taskID)
				prOut, err := exec.Command("gh", "pr", "create",
					"--repo", repo, "--head", branchName, "--base", "main",
					"--title", fmt.Sprintf("%s (%s)", task.Title, taskID),
					"--body", prBody).CombinedOutput()
				if err == nil {
					prURL = strings.TrimSpace(string(prOut))
					fmt.Printf("Validation: PR created: %s\n", prURL)
					conn.SetMetadata(ctx, taskID, "pr_url", prURL)
				}
			}
		}
	}

	if len(issues) > 0 && prURL == "" {
		fmt.Printf("Validation issues for %s: %s\n", taskID, strings.Join(issues, ", "))
	} else {
		fmt.Printf("Task %s validation passed (needs-review with PR)\n", taskID)
	}
	return nil
}

func init() {
	completeCmd.Flags().Bool("auto", false, "Triggered by Stop hook — warn and skip if no commits exist")
	rootCmd.AddCommand(completeCmd)
}
