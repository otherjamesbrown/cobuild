package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/worktree"
	"github.com/spf13/cobra"
)

var adminCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove stale worktrees, branches, and old session data",
	Long: `Age-based cleanup of CoBuild artifacts:
  - Worktrees for closed/completed tasks
  - Local branches that have been merged or have no active pipeline
  - Session logs, prompts, and events older than retention period
  - Session archive directories on disk

Pipeline runs and gate records are NEVER deleted (audit trail).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		yes, _ := cmd.Flags().GetBool("yes")
		keepDays, _ := cmd.Flags().GetInt("keep-days")

		repoRoot := findRepoRoot()
		cutoff := time.Now().AddDate(0, 0, -keepDays)

		fmt.Println("CoBuild Cleanup")
		fmt.Println("===============")
		fmt.Printf("Retention: %d days (before %s)\n\n", keepDays, cutoff.Format("2006-01-02"))

		// 1. Worktrees
		wtCleaned := cleanWorktrees(ctx, repoRoot, dryRun)

		// 2. Branches
		brCleaned := cleanBranches(ctx, repoRoot, dryRun)

		// 3. Remote prune
		if !dryRun {
			exec.CommandContext(ctx, "git", "-C", repoRoot, "fetch", "--prune").Run()
			fmt.Println("Pruned remote-tracking branches.")
		} else {
			fmt.Println("[dry-run] Would prune remote-tracking branches.")
		}

		// 4. Session data in DB
		sessionsCleaned := 0
		eventsCleaned := 0
		if cbStore != nil {
			sessionsCleaned, eventsCleaned = cleanSessionData(ctx, cutoff, dryRun)
		}

		// 5. Session archives on disk
		archivesCleaned := cleanSessionArchives(repoRoot, cutoff, dryRun)

		// Summary
		fmt.Printf("\n=== Cleanup Summary ===\n")
		fmt.Printf("Worktrees removed:  %d\n", wtCleaned)
		fmt.Printf("Branches deleted:   %d\n", brCleaned)
		fmt.Printf("Sessions trimmed:   %d\n", sessionsCleaned)
		fmt.Printf("Events deleted:     %d\n", eventsCleaned)
		fmt.Printf("Archives removed:   %d\n", archivesCleaned)

		if dryRun {
			fmt.Println("\n[dry-run] No changes made. Remove --dry-run to execute.")
		}

		_ = yes // TODO: use for non-interactive confirmation
		return nil
	},
}

func cleanWorktrees(ctx context.Context, repoRoot string, dryRun bool) int {
	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		return 0
	}

	cleaned := 0
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		path := strings.TrimPrefix(line, "worktree ")
		if path == repoRoot || strings.Contains(path, "beads") {
			continue // skip main repo and beads worktrees
		}

		taskID := filepath.Base(path)
		shouldClean := false

		if conn != nil {
			item, err := conn.Get(ctx, taskID)
			if err != nil {
				shouldClean = true // can't find task — orphaned worktree
			} else if item.Status == "closed" || item.Status == "needs-review" {
				shouldClean = true
			}
		}

		if shouldClean {
			if dryRun {
				fmt.Printf("  [dry-run] Remove worktree: %s (%s)\n", path, taskID)
			} else {
				worktree.Remove(ctx, repoRoot, path, taskID)
				fmt.Printf("  Removed worktree: %s\n", taskID)
			}
			cleaned++
		}
	}

	if cleaned == 0 {
		fmt.Println("Worktrees: clean")
	}
	return cleaned
}

func cleanBranches(ctx context.Context, repoRoot string, dryRun bool) int {
	// Delete merged branches
	out, err := exec.Command("git", "-C", repoRoot, "branch", "--merged", "main").CombinedOutput()
	if err != nil {
		return 0
	}

	cleaned := 0
	for _, line := range strings.Split(string(out), "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" || branch == "main" || strings.HasPrefix(branch, "*") || branch == "beads-sync" {
			continue
		}
		if dryRun {
			fmt.Printf("  [dry-run] Delete branch: %s (merged)\n", branch)
		} else {
			exec.Command("git", "-C", repoRoot, "branch", "-d", branch).Run()
			fmt.Printf("  Deleted branch: %s\n", branch)
		}
		cleaned++
	}

	// Prune worktree references
	exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "prune").Run()

	if cleaned == 0 {
		fmt.Println("Branches: clean")
	}
	return cleaned
}

func cleanSessionData(ctx context.Context, cutoff time.Time, dryRun bool) (int, int) {
	if cbStore == nil {
		return 0, 0
	}

	// Count old sessions and events via direct SQL
	// We trim bulk text (prompt, assembled_context, session_log) but keep the row
	if storeDSN == "" {
		return 0, 0
	}

	// For now, just report what would be cleaned
	// Full implementation would use pgx directly
	fmt.Printf("Session data: retention cutoff %s\n", cutoff.Format("2006-01-02"))
	fmt.Println("  (Session data cleanup via SQL — implementation pending)")

	return 0, 0
}

func cleanSessionArchives(repoRoot string, cutoff time.Time, dryRun bool) int {
	archiveDir := filepath.Join(repoRoot, ".cobuild", "sessions")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		return 0
	}

	cleaned := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(archiveDir, e.Name())
			if dryRun {
				fmt.Printf("  [dry-run] Remove archive: %s (modified %s)\n", e.Name(), info.ModTime().Format("2006-01-02"))
			} else {
				os.RemoveAll(path)
				fmt.Printf("  Removed archive: %s\n", e.Name())
			}
			cleaned++
		}
	}

	if cleaned == 0 {
		fmt.Println("Archives: clean")
	}
	return cleaned
}

func init() {
	adminCleanupCmd.Flags().Bool("dry-run", false, "Show what would be cleaned without executing")
	adminCleanupCmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	adminCleanupCmd.Flags().Int("keep-days", 90, "Retention period in days for session data")
	adminCmd.AddCommand(adminCleanupCmd)
}
