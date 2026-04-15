package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/spf13/cobra"
)

var (
	cleanupLogWriter    io.Writer = os.Stderr
	cleanupNow                    = time.Now
	cleanupConfigLoader           = func(repoRoot string) *config.Config {
		pCfg, err := config.LoadConfig(repoRoot)
		if err != nil || pCfg == nil {
			return config.DefaultConfig()
		}
		return pCfg
	}
	cleanupCommandRunner = func(ctx context.Context, pCfg *config.Config, name string, args ...string) ([]byte, error) {
		if name == "tmux" {
			args = tmuxCommandArgs(pCfg, args...)
		}
		return execCommandCombinedOutput(ctx, name, args...)
	}
)

type cleanupTaskOptions struct {
	DryRun           bool
	WorktreeOverride string
}

type cleanupCandidate struct {
	TaskID       string
	WorktreePath string
	Reason       string
}

var cleanupCmd = &cobra.Command{
	Use:   "cleanup [task-id]",
	Short: "Clean task worktrees, branches, and tmux windows",
	Long: `Best-effort cleanup for task-local resources.

Single-task mode:
  cobuild cleanup <task-id>

Bulk modes:
  cobuild cleanup --orphaned --dry-run
  cobuild cleanup --older-than 7d --dry-run`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		orphaned, _ := cmd.Flags().GetBool("orphaned")
		olderThanRaw, _ := cmd.Flags().GetString("older-than")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if len(args) == 1 && (orphaned || olderThanRaw != "") {
			return fmt.Errorf("use either a task ID or a bulk flag, not both")
		}
		if len(args) == 0 && !orphaned && olderThanRaw == "" {
			return fmt.Errorf("provide a task ID, or use --orphaned or --older-than")
		}
		if orphaned && olderThanRaw != "" {
			return fmt.Errorf("--orphaned and --older-than are mutually exclusive")
		}

		if len(args) == 1 {
			return cleanupTaskResourcesWithOptions(ctx, args[0], cleanupTaskOptions{DryRun: dryRun})
		}

		repoRoot := findRepoRoot()
		candidates, err := selectCleanupCandidates(ctx, repoRoot, orphaned, olderThanRaw)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No cleanup candidates found.")
			return nil
		}

		for _, candidate := range candidates {
			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "[dry-run] Would clean %s: %s\n", candidate.TaskID, candidate.Reason)
				continue
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Cleaning %s: %s\n", candidate.TaskID, candidate.Reason)
			if err := cleanupTaskResourcesWithOptions(ctx, candidate.TaskID, cleanupTaskOptions{
				WorktreeOverride: candidate.WorktreePath,
			}); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: cleanup for %s completed with issues: %v\n", candidate.TaskID, err)
			}
		}
		return nil
	},
}

func cleanupAutoOnMergeEnabled() bool {
	return cleanupConfigLoader(findRepoRoot()).AutoOnMergeEnabled()
}

func cleanupTaskResources(ctx context.Context, taskID string) error {
	return cleanupTaskResourcesWithOptions(ctx, taskID, cleanupTaskOptions{})
}

func cleanupTaskResourcesWithOptions(ctx context.Context, taskID string, opts cleanupTaskOptions) error {
	taskProject := cleanupTaskProject(ctx, taskID)
	worktreePath := cleanupTaskWorktreePath(ctx, taskID, opts.WorktreeOverride)
	repoRoot := cleanupTaskRepoRoot(ctx, taskProject, worktreePath)
	pCfg := cleanupConfigLoader(cleanupConfigRoot(repoRoot, worktreePath))
	branch := cleanupTaskBranch(ctx, pCfg, repoRoot, taskID, worktreePath)
	tmuxTarget := cleanupTaskTmuxTarget(ctx, pCfg, taskID, taskProject)

	var failures []string
	var pendingBranchFailure string

	branchDeleted := false
	if branch != "" && repoRoot != "" {
		step := fmt.Sprintf("git branch -D %s", branch)
		if opts.DryRun {
			cleanupLogf("[dry-run] %s\n", step)
		} else if out, err := cleanupCommandRunner(ctx, pCfg, "git", "-C", repoRoot, "branch", "-D", branch); err != nil {
			if !cleanupBranchExists(ctx, pCfg, repoRoot, branch) {
				cleanupLogf("cleanup branch %s: already absent\n", branch)
				branchDeleted = true
			} else {
				pendingBranchFailure = fmt.Sprintf("delete branch %s: %s", branch, cleanupCommandError(err, out))
				cleanupLogf("cleanup branch %s: failed: %s\n", branch, cleanupCommandError(err, out))
			}
		} else {
			branchDeleted = true
			cleanupLogf("cleanup branch %s: deleted\n", branch)
		}
	} else if branch == "" {
		cleanupLogf("cleanup branch: skipped (branch unknown)\n")
	} else {
		cleanupLogf("cleanup branch %s: skipped (repo root unknown)\n", branch)
	}

	worktreeRemoved := false
	if worktreePath != "" {
		if opts.DryRun {
			cleanupLogf("[dry-run] git worktree remove --force %s\n", worktreePath)
			worktreeRemoved = true
		} else {
			archiveSessionLogs(worktreePath, taskID)
			removed, err := cleanupRemoveWorktree(ctx, pCfg, repoRoot, worktreePath)
			if err != nil {
				failures = append(failures, fmt.Sprintf("remove worktree %s: %s", worktreePath, err))
				cleanupLogf("cleanup worktree %s: failed: %s\n", worktreePath, err)
			} else if removed {
				worktreeRemoved = true
				cleanupLogf("cleanup worktree %s: removed\n", worktreePath)
				cleanupClearMetadata(ctx, taskID, domain.MetaWorktreePath)
			} else {
				worktreeRemoved = true
				cleanupLogf("cleanup worktree %s: already absent\n", worktreePath)
				cleanupClearMetadata(ctx, taskID, domain.MetaWorktreePath)
			}
		}
	} else {
		cleanupLogf("cleanup worktree: skipped (no worktree path)\n")
	}

	if !opts.DryRun && !branchDeleted && worktreeRemoved && branch != "" && repoRoot != "" {
		if out, err := cleanupCommandRunner(ctx, pCfg, "git", "-C", repoRoot, "branch", "-D", branch); err == nil {
			branchDeleted = true
			pendingBranchFailure = ""
			cleanupLogf("cleanup branch %s: deleted after worktree removal\n", branch)
		} else if !cleanupBranchExists(ctx, pCfg, repoRoot, branch) {
			branchDeleted = true
			pendingBranchFailure = ""
			cleanupLogf("cleanup branch %s: already absent after worktree removal\n", branch)
		} else {
			pendingBranchFailure = fmt.Sprintf("delete branch %s after worktree removal: %s", branch, cleanupCommandError(err, out))
			cleanupLogf("cleanup branch %s: retry failed: %s\n", branch, cleanupCommandError(err, out))
		}
	}
	if pendingBranchFailure != "" {
		failures = append(failures, pendingBranchFailure)
	}

	if tmuxTarget != "" {
		if opts.DryRun {
			cleanupLogf("[dry-run] tmux kill-window -t %s\n", tmuxTarget)
		} else if out, err := cleanupCommandRunner(ctx, pCfg, "tmux", "kill-window", "-t", tmuxTarget); err != nil {
			if cleanupTmuxAlreadyGone(out) {
				cleanupLogf("cleanup tmux %s: already absent\n", tmuxTarget)
				cleanupClearMetadata(ctx, taskID, domain.MetaTmuxWindow)
			} else {
				failures = append(failures, fmt.Sprintf("kill tmux window %s: %s", tmuxTarget, cleanupCommandError(err, out)))
				cleanupLogf("cleanup tmux %s: failed: %s\n", tmuxTarget, cleanupCommandError(err, out))
			}
		} else {
			cleanupLogf("cleanup tmux %s: killed\n", tmuxTarget)
			cleanupClearMetadata(ctx, taskID, domain.MetaTmuxWindow)
		}
	} else {
		cleanupLogf("cleanup tmux: skipped (no tmux window)\n")
	}

	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func selectCleanupCandidates(ctx context.Context, repoRoot string, orphaned bool, olderThanRaw string) ([]cleanupCandidate, error) {
	worktrees, err := listRepoWorktrees(ctx, repoRoot)
	if err != nil {
		return nil, err
	}

	var cutoff time.Time
	if olderThanRaw != "" {
		d, err := parseCleanupOlderThan(olderThanRaw)
		if err != nil {
			return nil, err
		}
		cutoff = cleanupNow().Add(-d)
	}

	candidates := make([]cleanupCandidate, 0, len(worktrees))
	for _, worktreePath := range worktrees {
		taskID := filepath.Base(worktreePath)
		if projectPrefix != "" && !strings.HasPrefix(taskID, projectPrefix) {
			continue
		}
		reason, ok := cleanupCandidateReason(ctx, taskID, orphaned, cutoff)
		if !ok {
			continue
		}
		candidates = append(candidates, cleanupCandidate{
			TaskID:       taskID,
			WorktreePath: worktreePath,
			Reason:       reason,
		})
	}
	return candidates, nil
}

func cleanupCandidateReason(ctx context.Context, taskID string, orphaned bool, cutoff time.Time) (string, bool) {
	if conn == nil {
		return "", false
	}

	item, err := conn.Get(ctx, taskID)
	if orphaned {
		if err != nil || item == nil {
			return "worktree has no matching task", true
		}
		if item.Status == "closed" {
			return "task is already closed", true
		}
		// Deliberately do NOT treat a missing task-level pipeline_run as "orphaned":
		// child tasks share their parent design's run and have no row of their own
		// (see internal/cmd/task_completion_helpers.go). Using GetRun here would
		// mark every live implement task as cleanup-eligible.
		return "", false
	}

	if !cutoff.IsZero() {
		if err != nil || item == nil || item.Status != "closed" {
			return "", false
		}
		closedAt := item.UpdatedAt
		if closedAt.IsZero() {
			closedAt = item.CreatedAt
		}
		if closedAt.IsZero() || closedAt.After(cutoff) {
			return "", false
		}
		return fmt.Sprintf("task closed at %s", closedAt.UTC().Format(time.RFC3339)), true
	}

	return "", false
}

func listRepoWorktrees(ctx context.Context, repoRoot string) ([]string, error) {
	out, err := execCommandCombinedOutput(ctx, "git", "-C", repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		if path == "" || path == repoRoot {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func cleanupTaskProject(ctx context.Context, taskID string) string {
	if conn != nil {
		if item, err := conn.Get(ctx, taskID); err == nil && item != nil && item.Project != "" {
			return item.Project
		}
	}
	if cbStore != nil {
		if run, err := cbStore.GetRun(ctx, taskID); err == nil && run != nil && run.Project != "" {
			return run.Project
		}
	}
	return projectName
}

func cleanupTaskWorktreePath(ctx context.Context, taskID, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	if conn != nil {
		if wtPath, _ := conn.GetMetadata(ctx, taskID, domain.MetaWorktreePath); strings.TrimSpace(wtPath) != "" {
			return strings.TrimSpace(wtPath)
		}
	}
	if cbStore != nil {
		if session, err := cbStore.GetSession(ctx, taskID); err == nil && session != nil && session.WorktreePath != nil {
			return strings.TrimSpace(*session.WorktreePath)
		}
	}
	return ""
}

func cleanupTaskRepoRoot(ctx context.Context, taskProject, worktreePath string) string {
	if strings.TrimSpace(worktreePath) != "" {
		if repoRoot := repoRootForWorktree(ctx, worktreePath); repoRoot != "" {
			return repoRoot
		}
	}
	if taskProject != "" {
		if repoRoot, err := config.RepoForProject(taskProject); err == nil && repoRoot != "" {
			return repoRoot
		}
	}
	return findRepoRoot()
}

func cleanupConfigRoot(repoRoot, worktreePath string) string {
	if strings.TrimSpace(repoRoot) != "" {
		return repoRoot
	}
	if strings.TrimSpace(worktreePath) != "" {
		if repoRoot := repoRootForWorktree(context.Background(), worktreePath); repoRoot != "" {
			return repoRoot
		}
	}
	return findRepoRoot()
}

func cleanupTaskBranch(ctx context.Context, pCfg *config.Config, repoRoot, taskID, worktreePath string) string {
	if strings.TrimSpace(worktreePath) != "" {
		if out, err := cleanupCommandRunner(ctx, pCfg, "git", "-C", worktreePath, "branch", "--show-current"); err == nil {
			if branch := strings.TrimSpace(string(out)); branch != "" {
				return branch
			}
		}
	}
	if strings.TrimSpace(repoRoot) != "" {
		if cleanupBranchExists(ctx, pCfg, repoRoot, taskID) {
			return taskID
		}
	}
	return taskID
}

func cleanupTaskTmuxTarget(ctx context.Context, pCfg *config.Config, taskID, taskProject string) string {
	var tmuxWindow string
	var tmuxSession string

	if conn != nil {
		tmuxWindow, _ = conn.GetMetadata(ctx, taskID, domain.MetaTmuxWindow)
	}
	if cbStore != nil {
		if session, err := cbStore.GetSession(ctx, taskID); err == nil && session != nil {
			if session.TmuxWindow != nil && strings.TrimSpace(tmuxWindow) == "" {
				tmuxWindow = strings.TrimSpace(*session.TmuxWindow)
			}
			if session.TmuxSession != nil {
				tmuxSession = strings.TrimSpace(*session.TmuxSession)
			}
		}
	}

	tmuxWindow = strings.TrimSpace(tmuxWindow)
	if tmuxWindow == "" {
		return ""
	}
	if tmuxSession == "" {
		tmuxSession = pCfg.ResolveTmuxSession(taskProject)
	}
	return fmt.Sprintf("%s:%s", tmuxSession, tmuxWindow)
}

func cleanupBranchExists(ctx context.Context, pCfg *config.Config, repoRoot, branch string) bool {
	out, err := cleanupCommandRunner(ctx, pCfg, "git", "-C", repoRoot, "branch", "--list", branch)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func cleanupRemoveWorktree(ctx context.Context, pCfg *config.Config, repoRoot, worktreePath string) (bool, error) {
	worktreePath = strings.TrimSpace(worktreePath)
	if worktreePath == "" {
		return false, nil
	}
	if _, err := os.Stat(worktreePath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	if repoRoot != "" {
		if out, err := cleanupCommandRunner(ctx, pCfg, "git", "-C", repoRoot, "worktree", "remove", "--force", worktreePath); err == nil {
			_, _ = cleanupCommandRunner(ctx, pCfg, "git", "-C", repoRoot, "worktree", "prune")
			return true, nil
		} else if _, statErr := os.Stat(worktreePath); os.IsNotExist(statErr) {
			_, _ = cleanupCommandRunner(ctx, pCfg, "git", "-C", repoRoot, "worktree", "prune")
			return false, nil
		} else if removeErr := os.RemoveAll(worktreePath); removeErr == nil {
			_, _ = cleanupCommandRunner(ctx, pCfg, "git", "-C", repoRoot, "worktree", "prune")
			return true, nil
		} else {
			return false, fmt.Errorf("%s", cleanupCommandError(err, out))
		}
	}

	if err := os.RemoveAll(worktreePath); err != nil {
		return false, err
	}
	return true, nil
}

func cleanupClearMetadata(ctx context.Context, taskID, key string) {
	if conn == nil {
		return
	}
	if err := conn.SetMetadata(ctx, taskID, key, ""); err != nil {
		cleanupLogf("cleanup metadata %s on %s: failed: %v\n", key, taskID, err)
	}
}

func cleanupTmuxAlreadyGone(out []byte) bool {
	msg := strings.ToLower(strings.TrimSpace(string(out)))
	return strings.Contains(msg, "can't find window") ||
		strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "failed to connect to server")
}

func cleanupCommandError(err error, out []byte) string {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return err.Error()
	}
	return fmt.Sprintf("%v (%s)", err, msg)
}

func cleanupLogf(format string, args ...any) {
	fmt.Fprintf(cleanupLogWriter, format, args...)
}

func parseCleanupOlderThan(raw string) (time.Duration, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if strings.HasSuffix(value, "d") {
		days := strings.TrimSuffix(value, "d")
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --older-than value %q", raw)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid --older-than value %q", raw)
	}
	return d, nil
}

func init() {
	cleanupCmd.Flags().Bool("orphaned", false, "Clean worktrees for closed tasks or tasks without pipeline runs")
	cleanupCmd.Flags().String("older-than", "", "Clean worktrees for tasks closed longer than this ago (for example 7d)")
	cleanupCmd.Flags().Bool("dry-run", false, "Show what would be cleaned without executing it")
	rootCmd.AddCommand(cleanupCmd)
}
