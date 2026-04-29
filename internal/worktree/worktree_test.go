package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newTestRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "worktree-test@example.com")
	runGit(t, dir, "config", "user.name", "worktree-test")
	runGit(t, dir, "config", "commit.gpgsign", "false")

	writeFile(t, dir, "README.md", "base\n")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-q", "-m", "base")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestCreateAndVerifyWorktree(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := newTestRepo(t)

	worktreePath, err := Create(ctx, "cb-worktree", repo, "cobuild")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if want := filepath.Join(home, "worktrees", "cobuild", "cb-worktree"); worktreePath != want {
		t.Fatalf("worktree path = %q, want %q", worktreePath, want)
	}
	if err := Verify(ctx, worktreePath); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if branch := gitOutput(t, worktreePath, "branch", "--show-current"); branch != "cb-worktree" {
		t.Fatalf("branch = %q, want cb-worktree", branch)
	}
}

func TestVerifyRejectsFullRepo(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	err := Verify(ctx, repo)
	if err == nil {
		t.Fatal("Verify returned nil error")
	}
	if !strings.Contains(err.Error(), "full repo, not a worktree") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveDeletesUnmergedBranchAndDirectory(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := newTestRepo(t)

	worktreePath, err := Create(ctx, "cb-remove", repo, "cobuild")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	writeFile(t, worktreePath, "feature.txt", "change\n")
	runGit(t, worktreePath, "add", "feature.txt")
	runGit(t, worktreePath, "commit", "-q", "-m", "unmerged work")

	if branches := gitOutput(t, repo, "branch", "--list", "cb-remove"); !strings.Contains(branches, "cb-remove") {
		t.Fatalf("expected branch cb-remove to exist, got %q", branches)
	}

	if err := Remove(ctx, repo, worktreePath, "cb-remove"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists, stat err = %v", err)
	}
	if branches := gitOutput(t, repo, "branch", "--list", "cb-remove"); branches != "" {
		t.Fatalf("branch still exists after remove: %q", branches)
	}
	if err := Verify(ctx, worktreePath); err == nil {
		t.Fatal("Verify succeeded after Remove")
	}
}

func TestCreateRecoversFromStaleState(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := newTestRepo(t)

	worktreePath := filepath.Join(home, "worktrees", "cobuild", "cb-stale")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir stale worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "stale.txt"), []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	runGit(t, repo, "branch", "cb-stale", "main")

	createdPath, err := Create(ctx, "cb-stale", repo, "cobuild")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if createdPath != worktreePath {
		t.Fatalf("created path = %q, want %q", createdPath, worktreePath)
	}
	if err := Verify(ctx, createdPath); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, err := os.Stat(filepath.Join(createdPath, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale file still exists, stat err = %v", err)
	}
}

func TestCreateRejectsNonGitRepo(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := Create(ctx, "cb-bad", t.TempDir(), "cobuild")
	if err == nil {
		t.Fatal("Create returned nil error")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestInstallPrePushHookRejectsMainPush verifies that the pre-push hook
// installed by InstallPrePushHook rejects pushes to refs/heads/main.
// cb-fb94f9: a dispatched agent pushed directly to main, bypassing review.
func TestInstallPrePushHookRejectsMainPush(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := newTestRepo(t)

	worktreePath, err := Create(ctx, "cb-hooktest", repo, "cobuild")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := InstallPrePushHook(ctx, worktreePath, "cb-hooktest"); err != nil {
		t.Fatalf("InstallPrePushHook: %v", err)
	}

	// Verify hook file exists and is executable
	gitDir := gitOutput(t, worktreePath, "rev-parse", "--git-dir")
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreePath, gitDir)
	}
	hookPath := filepath.Join(gitDir, "hooks", "pre-push")
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("hook not found: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Fatalf("hook is not executable: %v", info.Mode())
	}

	// Simulate the hook receiving a push to refs/heads/main
	cmd := exec.CommandContext(ctx, hookPath)
	cmd.Stdin = strings.NewReader("refs/heads/cb-hooktest abc123 refs/heads/main def456\n")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("pre-push hook should reject push to main, but exited 0")
	}
	if !strings.Contains(string(out), "BLOCKED") {
		t.Fatalf("hook output should mention BLOCKED, got: %s", string(out))
	}

	// Verify push to the task branch is allowed
	cmd2 := exec.CommandContext(ctx, hookPath)
	cmd2.Stdin = strings.NewReader("refs/heads/cb-hooktest abc123 refs/heads/cb-hooktest def456\n")
	if out2, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("pre-push hook should allow push to task branch, got error: %v\n%s", err, string(out2))
	}
}
