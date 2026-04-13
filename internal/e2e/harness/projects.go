package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"gopkg.in/yaml.v3"
)

func (h *Harness) CommandContextInRepo(ctx context.Context, repo *GitRepo, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	if repo != nil && strings.TrimSpace(repo.Root) != "" {
		cmd.Dir = repo.Root
	} else if h.Repo != nil {
		cmd.Dir = h.Repo.Root
	}
	cmd.Env = h.Env()
	return cmd
}

func (h *Harness) RunCobuildForProject(ctx context.Context, project string, args ...string) (string, error) {
	repo, err := h.repoForProject(project)
	if err != nil {
		return "", err
	}
	cmd := h.CommandContextInRepo(ctx, repo, h.BinaryPath, args...)
	out, runErr := cmd.CombinedOutput()
	return string(out), runErr
}

func (h *Harness) AddProject(ctx context.Context, project string, files map[string]string) (*GitRepo, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}
	if existing := h.Repos[project]; existing != nil {
		return existing, nil
	}

	repo, err := newGitRepo(ctx, GitRepoOptions{
		RootDir:       filepath.Join(h.RootDir, "repos"),
		Name:          project,
		DefaultBranch: "main",
		Files:         files,
	})
	if err != nil {
		return nil, fmt.Errorf("create project repo %s: %w", project, err)
	}
	if err := h.seedProjectRepo(ctx, project, repo); err != nil {
		return nil, err
	}
	h.Repos[project] = repo
	if err := h.writeRepoRegistry(); err != nil {
		return nil, err
	}
	return repo, nil
}

func (h *Harness) repoForProject(project string) (*GitRepo, error) {
	project = strings.TrimSpace(project)
	if project == "" || project == h.Project {
		return h.Repo, nil
	}
	repo := h.Repos[project]
	if repo == nil {
		return nil, fmt.Errorf("project repo %q is not registered", project)
	}
	return repo, nil
}

func (h *Harness) seedProjectRepo(ctx context.Context, project string, repo *GitRepo) error {
	cfgCopy := *h.Config
	cfgCopy.GitHub.OwnerRepo = "acme/" + project

	cfgData, err := yaml.Marshal(&cfgCopy)
	if err != nil {
		return fmt.Errorf("marshal project pipeline config: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(repo.Root, ".cobuild"), 0o755); err != nil {
		return fmt.Errorf("mkdir %s/.cobuild: %w", repo.Root, err)
	}
	if err := os.WriteFile(filepath.Join(repo.Root, ".cobuild", "pipeline.yaml"), cfgData, 0o644); err != nil {
		return fmt.Errorf("write %s/.cobuild/pipeline.yaml: %w", repo.Root, err)
	}

	projectData, err := yaml.Marshal(map[string]string{
		"project": project,
		"prefix":  h.Prefix,
	})
	if err != nil {
		return fmt.Errorf("marshal project yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(repo.Root, ".cobuild.yaml"), projectData, 0o644); err != nil {
		return fmt.Errorf("write %s/.cobuild.yaml: %w", repo.Root, err)
	}

	if err := runGit(ctx, repo.Root, "add", ".cobuild/pipeline.yaml", ".cobuild.yaml"); err != nil {
		return fmt.Errorf("stage project repo config: %w", err)
	}
	if err := runGit(ctx, repo.Root, "commit", "-m", "add e2e harness config"); err != nil {
		return fmt.Errorf("commit project repo config: %w", err)
	}
	if err := runGit(ctx, repo.Root, "push", "origin", repo.DefaultBranch); err != nil {
		return fmt.Errorf("push project repo config: %w", err)
	}
	return nil
}

func (h *Harness) writeRepoRegistry() error {
	reg := &config.RepoRegistry{Repos: map[string]config.RepoEntry{}}
	projects := make([]string, 0, len(h.Repos))
	for project := range h.Repos {
		projects = append(projects, project)
	}
	sort.Strings(projects)
	for _, project := range projects {
		repo := h.Repos[project]
		reg.Repos[project] = config.RepoEntry{
			Path:          repo.Root,
			DefaultBranch: repo.DefaultBranch,
		}
	}

	regData, err := yaml.Marshal(reg)
	if err != nil {
		return fmt.Errorf("marshal repo registry: %w", err)
	}
	if err := os.WriteFile(filepath.Join(h.HomeDir, ".cobuild", "repos.yaml"), regData, 0o644); err != nil {
		return fmt.Errorf("save repo registry: %w", err)
	}
	return nil
}
