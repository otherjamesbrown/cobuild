package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/worktree"
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
		repoMetadata, _ := conn.GetMetadata(ctx, taskID, "repo")

		// When triggered by Stop hook, verify the agent actually completed work before proceeding
		if autoFlag && worktreePath != "" {
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

		decision, err := determineCompletionPath(ctx, task, taskID, worktreePath, repoMetadata)
		if err != nil {
			if autoFlag && worktreePath == "" {
				fmt.Fprintf(os.Stderr, "Warning: --auto: no worktree_path for %s, skipping\n", taskID)
				return nil
			}
			return err
		}
		if decision.Direct {
			fmt.Printf("Direct completion selected: %s\n", decision.Note)
			if err := completeDirectTask(ctx, taskID, worktreePath, decision.Note); err != nil {
				return err
			}
			fmt.Printf("Task %s complete -> closed\n", taskID)
			printNextStep(taskID, "implement", "complete")
			return nil
		}

		if worktreePath == "" {
			if autoFlag {
				fmt.Fprintf(os.Stderr, "Warning: --auto: no worktree_path for %s, skipping\n", taskID)
				return nil
			}
			return fmt.Errorf("no worktree_path in task metadata")
		}

		repoRoot, _ := config.RepoForProject(projectName)
		pCfg, _ := config.LoadConfig(repoRoot)

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
		if cbStore != nil && conn != nil {
			filesCount := len(strings.Split(strings.TrimSpace(filesChanged), "\n"))
			if strings.TrimSpace(filesChanged) == "" {
				filesCount = 0
			}
			if err := endCompletionSession(ctx, taskID, worktreePath, filesCount, 1, prURL, ""); err != nil {
				fmt.Printf("Warning: failed to end session: %v\n", err)
			}
		}

		fmt.Printf("Task %s complete -> needs-review\n", taskID)
		printNextStep(taskID, "implement", "complete")
		return nil
	},
}

type completionDecision struct {
	Direct bool
	Note   string
}

func determineCompletionPath(ctx context.Context, task *connector.WorkItem, taskID, worktreePath, repoMetadata string) (*completionDecision, error) {
	mode := strings.ToLower(strings.TrimSpace(taskMetadata(task, "completion_mode")))
	if mode == "" && conn != nil {
		mode, _ = conn.GetMetadata(ctx, taskID, "completion_mode")
		mode = strings.ToLower(strings.TrimSpace(mode))
	}

	switch mode {
	case "direct":
		note := "completion_mode=direct"
		if repoMetadata == "" {
			note += " and repo metadata is unset"
		}
		return &completionDecision{
			Direct: true,
			Note:   fmt.Sprintf("direct path chosen because %s", note),
		}, nil
	case "", "code":
		// continue to fallback detection
	default:
		return nil, fmt.Errorf("unsupported completion_mode %q", mode)
	}

	if worktreePath == "" {
		if repoMetadata == "" {
			return &completionDecision{
				Direct: true,
				Note:   "direct path chosen because repo metadata is unset and no worktree was recorded",
			}, nil
		}
		return nil, fmt.Errorf("no worktree_path in task metadata")
	}

	empty, err := isCompletionWorktreeEmpty(ctx, worktreePath)
	if err != nil {
		return nil, fmt.Errorf("inspect worktree: %w", err)
	}
	if empty {
		note := "direct path chosen because the git worktree has no tracked changes after excluding CoBuild artifacts"
		if repoMetadata == "" {
			note += " and repo metadata is unset"
		}
		return &completionDecision{Direct: true, Note: note}, nil
	}

	return &completionDecision{}, nil
}

func taskMetadata(task *connector.WorkItem, key string) string {
	if task == nil || task.Metadata == nil {
		return ""
	}
	value, ok := task.Metadata[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func isCompletionWorktreeEmpty(ctx context.Context, worktreePath string) (bool, error) {
	statusOut, err := exec.CommandContext(ctx, "git", "-C", worktreePath, "status", "--porcelain", "--", ".", ":!.cobuild", ":!CLAUDE.md").Output()
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(statusOut))) == 0, nil
}

func completeDirectTask(ctx context.Context, taskID, worktreePath, note string) error {
	if err := recordDirectCompletion(ctx, taskID, note); err != nil {
		return err
	}
	if err := closeTaskAndCleanup(ctx, taskID, worktreePath); err != nil {
		return err
	}
	if err := endCompletionSession(ctx, taskID, worktreePath, 0, 0, "", note); err != nil {
		return err
	}
	return nil
}

func recordDirectCompletion(ctx context.Context, taskID, note string) error {
	if cbStore == nil {
		return nil
	}
	run, err := cbStore.GetRun(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get pipeline run: %w", err)
	}
	body := note
	if _, err := cbStore.RecordGate(ctx, store.PipelineGateInput{
		PipelineID: run.ID,
		DesignID:   taskID,
		GateName:   "review",
		Phase:      run.CurrentPhase,
		Verdict:    "pass",
		Body:       &body,
	}); err != nil {
		return fmt.Errorf("record direct completion gate: %w", err)
	}
	if err := cbStore.UpdateRunPhase(ctx, taskID, "done"); err != nil {
		return fmt.Errorf("advance task run to done: %w", err)
	}
	if err := cbStore.UpdateRunStatus(ctx, taskID, "completed"); err != nil {
		return fmt.Errorf("mark task run completed: %w", err)
	}
	return nil
}

func closeTaskAndCleanup(ctx context.Context, taskID, worktreePath string) error {
	if conn != nil {
		if err := conn.UpdateStatus(ctx, taskID, "closed"); err != nil {
			return fmt.Errorf("close task: %w", err)
		}
	}

	if worktreePath != "" {
		archiveSessionLogs(worktreePath, taskID)
		repoForCleanup, _ := config.RepoForProject(projectName)
		if repoForCleanup == "" {
			if commonDirOut, err := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "--path-format=absolute", "--git-common-dir").Output(); err == nil {
				repoForCleanup = filepath.Dir(strings.TrimSpace(string(commonDirOut)))
			}
		}
		if repoForCleanup == "" {
			repoForCleanup = findRepoRoot()
		}
		if err := worktree.Remove(ctx, repoForCleanup, worktreePath, taskID); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}

	if conn == nil || cbStore == nil {
		return nil
	}
	edges, err := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
	if err != nil || len(edges) == 0 {
		return nil
	}
	designID := edges[0].ItemID
	siblings, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return nil
	}
	allDone := true
	for _, s := range siblings {
		if s.Status != "closed" {
			allDone = false
			break
		}
	}
	if !allDone {
		return nil
	}
	if err := cbStore.UpdateRunPhase(ctx, designID, "done"); err != nil {
		return fmt.Errorf("advance parent run to done: %w", err)
	}
	if err := cbStore.UpdateRunStatus(ctx, designID, "completed"); err != nil {
		return fmt.Errorf("mark parent run completed: %w", err)
	}
	return nil
}

func endCompletionSession(ctx context.Context, taskID, worktreePath string, filesChanged, commits int, prURL, completionNote string) error {
	if cbStore == nil || conn == nil {
		return nil
	}
	sessionID, _ := conn.GetMetadata(ctx, taskID, "session_id")
	if sessionID == "" {
		return nil
	}
	sessionLog := ""
	if worktreePath != "" {
		logData, err := os.ReadFile(filepath.Join(worktreePath, ".cobuild", "session.log"))
		if err == nil {
			sessionLog = string(logData)
		}
	}
	return cbStore.EndSession(ctx, sessionID, store.SessionResult{
		ExitCode:       0,
		FilesChanged:   filesChanged,
		Commits:        commits,
		PRURL:          prURL,
		CompletionNote: completionNote,
		Status:         "completed",
		SessionLog:     sessionLog,
	})
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
