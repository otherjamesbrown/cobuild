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
// Returns the worktree path.
func Create(ctx context.Context, taskID, repoRoot, project string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %v", err)
	}

	worktreeBase := filepath.Join(home, "worktrees", project)
	if err := os.MkdirAll(worktreeBase, 0755); err != nil {
		return "", fmt.Errorf("create worktree base dir: %v", err)
	}

	worktreePath := filepath.Join(worktreeBase, taskID)
	branch := taskID

	// Create branch from main (may already exist)
	if out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "branch", branch, "main").CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "already exists") {
			return "", fmt.Errorf("create branch %s: %s\n%s", branch, err, string(out))
		}
	}

	// Create worktree
	if out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "add", worktreePath, branch).CombinedOutput(); err != nil {
		return "", fmt.Errorf("create worktree: %s\n%s", err, string(out))
	}

	return worktreePath, nil
}

// Remove removes a git worktree by path.
func Remove(ctx context.Context, worktreePath string) error {
	if worktreePath == "" {
		return fmt.Errorf("worktree path is empty")
	}
	if out, err := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", worktreePath).CombinedOutput(); err != nil {
		return fmt.Errorf("remove worktree %s: %s\n%s", worktreePath, err, string(out))
	}
	return nil
}
