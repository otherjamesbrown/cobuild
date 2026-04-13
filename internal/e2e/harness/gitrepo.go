package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

type GitRepoOptions struct {
	RootDir       string
	Name          string
	DefaultBranch string
	Files         map[string]string
}

type GitRepo struct {
	Root          string
	Remote        string
	DefaultBranch string
}

func newGitRepo(ctx context.Context, opts GitRepoOptions) (*GitRepo, error) {
	if opts.RootDir == "" {
		return nil, fmt.Errorf("git repo root dir is required")
	}
	name := opts.Name
	if name == "" {
		name = "repo"
	}
	branch := opts.DefaultBranch
	if branch == "" {
		branch = "main"
	}

	root := filepath.Join(opts.RootDir, name)
	remote := filepath.Join(opts.RootDir, name+".git")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create repo root: %w", err)
	}
	if err := runGit(ctx, opts.RootDir, "init", "--bare", remote); err != nil {
		return nil, err
	}
	if err := runGit(ctx, root, "init", "-b", branch); err != nil {
		return nil, err
	}
	if err := runGit(ctx, root, "config", "user.email", "e2e@example.com"); err != nil {
		return nil, err
	}
	if err := runGit(ctx, root, "config", "user.name", "CoBuild E2E"); err != nil {
		return nil, err
	}

	files := map[string]string{
		"README.md": "# Disposable e2e repo\n",
	}
	for path, body := range opts.Files {
		files[path] = body
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(files[path]), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", fullPath, err)
		}
	}

	if err := runGit(ctx, root, "add", "."); err != nil {
		return nil, err
	}
	if err := runGit(ctx, root, "commit", "-m", "initial commit"); err != nil {
		return nil, err
	}
	if err := runGit(ctx, root, "remote", "add", "origin", remote); err != nil {
		return nil, err
	}
	if err := runGit(ctx, root, "push", "-u", "origin", branch); err != nil {
		return nil, err
	}
	if err := runGit(ctx, opts.RootDir, "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		return nil, err
	}

	return &GitRepo{
		Root:          root,
		Remote:        remote,
		DefaultBranch: branch,
	}, nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v in %s: %w\n%s", args, dir, err, string(out))
	}
	return nil
}
