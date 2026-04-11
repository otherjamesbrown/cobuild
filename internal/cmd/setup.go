package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Register current repo for pipeline automation",
	Long: `Run in a git repo to register it for pipeline automation.

Auto-detects language, build commands, GitHub remote, and project name.
Creates local .cobuild/pipeline.yaml and updates ~/.cobuild/repos.yaml registry.`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().String("project", "", "Override project name detection")
	setupCmd.Flags().Bool("force", false, "Overwrite existing .cobuild/pipeline.yaml")
	setupCmd.Flags().Bool("dry-run", false, "Show what would be written without writing files")

	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	projectOverride, _ := cmd.Flags().GetString("project")

	gitRootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("not a git repository (or git is not installed)")
	}
	repoRoot := strings.TrimSpace(string(gitRootOut))

	project := detectSetupProject(projectOverride, repoRoot)
	if project == "" {
		return fmt.Errorf("could not detect project name. Use --project <name>")
	}

	ownerRepo := detectOwnerRepo()
	buildCmds, testCmds := detectBuildSystem(repoRoot)
	defaultBranch := detectDefaultBranch()

	pipelineDir := filepath.Join(repoRoot, ".cobuild")
	pipelinePath := filepath.Join(pipelineDir, "pipeline.yaml")
	if _, err := os.Stat(pipelinePath); err == nil && !force {
		return fmt.Errorf("already configured. Use --force to overwrite")
	}

	pipelineYAML, err := buildPipelineYAML(project, ownerRepo, buildCmds, testCmds)
	if err != nil {
		return err
	}

	// Also prepare a minimal .cobuild.yaml at the repo root so every future
	// cobuild command can resolve projectName without hitting the
	// repos.yaml/github.owner_repo fallback chain in root.go (cb-11a464).
	// This is the file root.go:PersistentPreRunE reads via
	// readProjectConfigFromYAML.
	projectYAMLPath := filepath.Join(repoRoot, ".cobuild.yaml")
	projectYAMLContent := fmt.Sprintf("# Project identity for cobuild\n# Created by `cobuild setup`\nproject: %s\n", project)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %v", err)
	}
	reposPath := filepath.Join(homeDir, ".cobuild", "repos.yaml")

	reg, err := config.LoadRepoRegistry()
	if err != nil {
		reg = &config.RepoRegistry{Repos: make(map[string]config.RepoEntry)}
	}
	reg.Repos[project] = config.RepoEntry{
		Path:          repoRoot,
		DefaultBranch: defaultBranch,
	}

	reposData, err := yaml.Marshal(reg)
	if err != nil {
		return fmt.Errorf("failed to marshal repos registry: %v", err)
	}

	if dryRun {
		fmt.Printf("Dry run -- no files written.\n\n")
		fmt.Printf("Would write %s:\n", pipelinePath)
		fmt.Printf("---\n%s\n", pipelineYAML)
		fmt.Printf("Would write %s:\n", projectYAMLPath)
		fmt.Printf("---\n%s\n", projectYAMLContent)
		fmt.Printf("Would write %s:\n", reposPath)
		fmt.Printf("---\n%s\n", string(reposData))
		return nil
	}

	if err := os.MkdirAll(pipelineDir, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %v", pipelineDir, err)
	}
	if err := os.WriteFile(pipelinePath, []byte(pipelineYAML), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %v", pipelinePath, err)
	}

	// Write .cobuild.yaml only if it doesn't already exist OR --force was
	// passed. The user may have placed their own project identity file
	// already; don't stomp it without an explicit opt-in.
	if _, err := os.Stat(projectYAMLPath); os.IsNotExist(err) || force {
		if err := os.WriteFile(projectYAMLPath, []byte(projectYAMLContent), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %v", projectYAMLPath, err)
		}
	}

	if err := config.SaveRepoRegistry(reg); err != nil {
		return fmt.Errorf("failed to save repo registry: %v", err)
	}

	fmt.Printf("Pipeline configured for %s\n", project)
	fmt.Printf("  Config:   %s\n", pipelinePath)
	fmt.Printf("  Identity: %s\n", projectYAMLPath)
	fmt.Printf("  Registry: %s\n", reposPath)
	fmt.Printf("  Build:    %s\n", formatCmdList(buildCmds))
	fmt.Printf("  Test:     %s\n", formatCmdList(testCmds))
	fmt.Printf("  GitHub:   %s\n", ownerRepo)
	fmt.Printf("  Branch:   %s\n", defaultBranch)

	return nil
}

func detectSetupProject(flagValue, repoRoot string) string {
	if flagValue != "" {
		return flagValue
	}
	for _, name := range []string{".cobuild.yaml", ".cxp.yaml", ".cp.yaml"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			continue
		}
		var parsed struct {
			Project string `yaml:"project"`
		}
		if err := yaml.Unmarshal(data, &parsed); err == nil && parsed.Project != "" {
			return parsed.Project
		}
	}
	if projectName != "" {
		return projectName
	}
	return filepath.Base(repoRoot)
}

func detectOwnerRepo() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	remote := strings.TrimSpace(string(out))
	sshRe := regexp.MustCompile(`git@github\.com:([^/]+/[^/]+?)(?:\.git)?$`)
	if m := sshRe.FindStringSubmatch(remote); len(m) > 1 {
		return m[1]
	}
	httpsRe := regexp.MustCompile(`https://github\.com/([^/]+/[^/]+?)(?:\.git)?$`)
	if m := httpsRe.FindStringSubmatch(remote); len(m) > 1 {
		return m[1]
	}
	return ""
}

func detectBuildSystem(repoRoot string) (build []string, test []string) {
	findMarker := func(name string) string {
		if _, err := os.Stat(filepath.Join(repoRoot, name)); err == nil {
			return "."
		}
		entries, err := os.ReadDir(repoRoot)
		if err != nil {
			return ""
		}
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				if _, err := os.Stat(filepath.Join(repoRoot, e.Name(), name)); err == nil {
					return e.Name()
				}
			}
		}
		return ""
	}

	prefix := func(dir string) string {
		if dir == "." {
			return ""
		}
		return "cd " + dir + " && "
	}

	type langDef struct {
		marker string
		build  func(string) []string
		test   func(string) []string
	}

	langs := []langDef{
		{"go.mod",
			func(d string) []string { return []string{prefix(d) + "go build ./..."} },
			func(d string) []string { return []string{prefix(d) + "go test ./...", prefix(d) + "go vet ./..."} }},
		{"package.json",
			func(d string) []string { return []string{prefix(d) + "npm run build"} },
			func(d string) []string { return []string{prefix(d) + "npm test"} }},
		{"Cargo.toml",
			func(d string) []string { return []string{prefix(d) + "cargo build"} },
			func(d string) []string { return []string{prefix(d) + "cargo test"} }},
		{"pyproject.toml",
			func(string) []string { return nil },
			func(d string) []string { return []string{prefix(d) + "pytest"} }},
	}

	for _, lang := range langs {
		if dir := findMarker(lang.marker); dir != "" {
			return lang.build(dir), lang.test(dir)
		}
	}
	return nil, nil
}

func detectDefaultBranch() string {
	out, err := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		parts := strings.Split(ref, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	return "main"
}

func buildPipelineYAML(project, ownerRepo string, buildCmds, testCmds []string) (string, error) {
	cfg := config.Config{
		Build: buildCmds,
		Test:  testCmds,
		Agents: map[string]config.AgentCfg{
			"agent-steve":   {Domains: []string{"cli", "migrations"}},
			"agent-mycroft": {Domains: []string{"backend", "services"}},
		},
		Dispatch: config.DispatchCfg{
			MaxConcurrent: 3,
		},
		GitHub: config.GitHubCfg{
			OwnerRepo: ownerRepo,
		},
		SkillsDir: "skills",
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshaling pipeline config: %w", err)
	}
	header := fmt.Sprintf("# Pipeline configuration for %s\n# See: cobuild setup --help\n\n", project)
	return header + string(data), nil
}

func formatCmdList(cmds []string) string {
	if len(cmds) == 0 {
		return "(none)"
	}
	return strings.Join(cmds, ", ")
}
