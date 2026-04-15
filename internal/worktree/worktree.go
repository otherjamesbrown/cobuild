// Package worktree manages git worktrees for pipeline task dispatch.
package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Create creates a git worktree for a task, branching from main.
// It handles pre-existing branches and directories from failed previous attempts.
// Returns the worktree path.
func Create(ctx context.Context, taskID, repoRoot, project string) (string, error) {
	if repoRoot == "" {
		return "", fmt.Errorf("repoRoot is empty — cannot create worktree without a repo")
	}
	if taskID == "" {
		return "", fmt.Errorf("taskID is empty")
	}

	// Verify repoRoot is a git repo
	if out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "--git-dir").CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s is not a git repository: %w\n%s", repoRoot, err, string(out))
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	worktreeBase := filepath.Join(home, "worktrees", project)
	if err := os.MkdirAll(worktreeBase, 0755); err != nil {
		return "", fmt.Errorf("create worktree base dir %s: %w", worktreeBase, err)
	}

	worktreePath := filepath.Join(worktreeBase, taskID)
	branch := taskID

	// Clean up stale state from failed previous attempts
	if err := cleanupStale(ctx, repoRoot, worktreePath, branch); err != nil {
		return "", fmt.Errorf("cleanup stale worktree state: %w", err)
	}

	// Fetch latest origin/main so the worktree is branched from current
	// remote state, not from a stale local main (cb-4d08ca). Without this,
	// when wave N PRs merge to origin, wave N+1 worktrees branch from a
	// local main that doesn't have the merged work — agents then write
	// duplicates of files that already exist on origin.
	if out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "fetch", "origin", "main").CombinedOutput(); err != nil {
		// Non-fatal: log but proceed. If fetch fails (offline, no remote),
		// fall back to local main rather than blocking dispatch.
		fmt.Fprintf(os.Stderr, "Warning: fetch origin main failed (using local main): %s\n", strings.TrimSpace(string(out)))
	}

	// Create branch from origin/main (with fallback to local main if origin/main
	// doesn't exist — happens in test repos with no remote)
	base := "origin/main"
	if err := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "--verify", "origin/main").Run(); err != nil {
		base = "main"
	}
	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "branch", branch, base).CombinedOutput()
	if err != nil {
		if !strings.Contains(string(out), "already exists") {
			return "", fmt.Errorf("create branch %s in %s from %s: %w\n%s", branch, repoRoot, base, err, string(out))
		}
	}

	// Create worktree
	out, err = exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "add", worktreePath, branch).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("create worktree at %s from %s: %w\n%s", worktreePath, repoRoot, err, string(out))
	}

	// Verify the worktree is valid — it should have files from the repo
	if err := Verify(ctx, worktreePath); err != nil {
		// Worktree was "created" but is invalid — clean up
		_ = Remove(ctx, repoRoot, worktreePath, branch)
		return "", fmt.Errorf("worktree created but invalid: %w", err)
	}

	return worktreePath, nil
}

// Verify checks that a worktree path is a valid git worktree with repo content.
func Verify(ctx context.Context, worktreePath string) error {
	// Check it's a directory
	info, err := os.Stat(worktreePath)
	if err != nil {
		return fmt.Errorf("worktree path does not exist: %s", worktreePath)
	}
	if !info.IsDir() {
		return fmt.Errorf("worktree path is not a directory: %s", worktreePath)
	}

	// Check it's a git worktree (has .git file pointing to main repo)
	gitPath := filepath.Join(worktreePath, ".git")
	gitInfo, err := os.Stat(gitPath)
	if err != nil {
		return fmt.Errorf("no .git in worktree — not a valid git worktree: %s", worktreePath)
	}
	// Worktrees have a .git FILE (not directory) pointing to the main repo
	if gitInfo.IsDir() {
		return fmt.Errorf(".git is a directory — this is a full repo, not a worktree: %s", worktreePath)
	}

	// Check we can run git commands in it
	out, err := exec.CommandContext(ctx, "git", "-C", worktreePath, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commands fail in worktree: %w\n%s", err, string(out))
	}

	// Check it has actual files (not just .git and .cobuild)
	entries, err := os.ReadDir(worktreePath)
	if err != nil {
		return fmt.Errorf("cannot read worktree directory: %w", err)
	}
	realFiles := 0
	for _, e := range entries {
		if e.Name() != ".git" && e.Name() != ".cobuild" {
			realFiles++
		}
	}
	if realFiles == 0 {
		return fmt.Errorf("worktree has no repo content — only .git and .cobuild: %s", worktreePath)
	}

	return nil
}

// Remove removes a git worktree and optionally its branch.
func Remove(ctx context.Context, repoRoot, worktreePath, branch string) error {
	if worktreePath == "" {
		return fmt.Errorf("worktree path is empty")
	}

	// Remove worktree registration from git
	exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "remove", "--force", worktreePath).Run()

	// Remove the directory if it still exists
	if _, err := os.Stat(worktreePath); err == nil {
		os.RemoveAll(worktreePath)
	}

	// Prune stale worktree references
	exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "prune").Run()

	// Delete the branch if specified
	if branch != "" {
		exec.CommandContext(ctx, "git", "-C", repoRoot, "branch", "-D", branch).Run()
	}

	return nil
}

// cleanupStale removes pre-existing worktree state from failed attempts.
func cleanupStale(ctx context.Context, repoRoot, worktreePath, branch string) error {
	// Check if directory exists (could be from a failed previous attempt)
	if _, err := os.Stat(worktreePath); err == nil {
		fmt.Printf("  Cleaning up stale worktree directory: %s\n", worktreePath)
		// Try to remove as a git worktree first
		exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "remove", "--force", worktreePath).Run()
		// If directory still exists, remove it
		if _, err := os.Stat(worktreePath); err == nil {
			if err := os.RemoveAll(worktreePath); err != nil {
				return fmt.Errorf("cannot remove stale directory %s: %w", worktreePath, err)
			}
		}
	}

	// Check if branch exists but isn't checked out anywhere
	out, _ := exec.CommandContext(ctx, "git", "-C", repoRoot, "branch", "--list", branch).CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		// Branch exists — check if it's checked out in another worktree
		wtOut, _ := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "list", "--porcelain").CombinedOutput()
		if strings.Contains(string(wtOut), branch) {
			return fmt.Errorf("branch %s is checked out in another worktree — cannot reuse", branch)
		}
		// Branch exists but not checked out — delete it so we get a fresh one from main
		fmt.Printf("  Deleting stale branch: %s\n", branch)
		exec.CommandContext(ctx, "git", "-C", repoRoot, "branch", "-D", branch).Run()
	}

	// Prune any stale worktree references
	exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "prune").Run()

	return nil
}
