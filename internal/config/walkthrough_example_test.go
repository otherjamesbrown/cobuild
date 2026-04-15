package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWalkthroughExampleConfigLoads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}

	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	exampleRoot := filepath.Join(repoRoot, "examples", "walkthrough")

	cfg, err := LoadConfig(exampleRoot)
	if err != nil {
		t.Fatalf("LoadConfig(%q): %v", exampleRoot, err)
	}

	if got := cfg.GitHub.OwnerRepo; got != "example/cobuild-walkthrough" {
		t.Fatalf("GitHub.OwnerRepo = %q, want example/cobuild-walkthrough", got)
	}
	if got := cfg.Connectors.WorkItems.Type; got != "context-palace" {
		t.Fatalf("Connectors.WorkItems.Type = %q, want context-palace", got)
	}
	if got := cfg.ResolveRuntime("", ""); got != "codex" {
		t.Fatalf("ResolveRuntime() = %q, want codex", got)
	}
	if got := cfg.Dispatch.Runtimes["codex"].Model; got != "gpt-5.4-mini" {
		t.Fatalf("Dispatch.Runtimes[codex].Model = %q, want gpt-5.4-mini", got)
	}

	designWorkflow := cfg.Workflows["design"]
	if got := strings.Join(designWorkflow.Phases, ","); got != "design,decompose,implement,review,done" {
		t.Fatalf("design workflow = %q, want design,decompose,implement,review,done", got)
	}

	if len(cfg.Deploy.Services) != 1 || cfg.Deploy.Services[0].Name != "demo-service" {
		t.Fatalf("deploy services = %#v, want one demo-service entry", cfg.Deploy.Services)
	}

	skillPath, err := ResolveSkill(exampleRoot, "design/gate-readiness-review.md")
	if err != nil {
		t.Fatalf("ResolveSkill: %v", err)
	}
	if !strings.HasSuffix(skillPath, filepath.Join("skills", "design", "gate-readiness-review.md")) {
		t.Fatalf("ResolveSkill returned %q", skillPath)
	}

	contextText, err := AssembleContext(cfg, exampleRoot, "dispatch", "design", map[string]string{
		"dispatch-prompt": "# Dispatch Prompt\n\nWalk the design gate.",
		"parent-design":   "# Parent Design\n\nRelease banner design.",
	}, nil)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}
	for _, snippet := range []string{
		"# Skill: Walkthrough Readiness Review",
		"# Dispatch Prompt",
		"# Parent Design",
	} {
		if !strings.Contains(contextText, snippet) {
			t.Fatalf("assembled context missing %q\ncontext:\n%s", snippet, contextText)
		}
	}
	if strings.Contains(contextText, "Walkthrough Anatomy") {
		t.Fatalf("design-phase context should not include implement-only anatomy\ncontext:\n%s", contextText)
	}

	projectData, err := os.ReadFile(filepath.Join(exampleRoot, ".cobuild.yaml"))
	if err != nil {
		t.Fatalf("read .cobuild.yaml: %v", err)
	}

	var project struct {
		Project string `yaml:"project"`
		Prefix  string `yaml:"prefix"`
	}
	if err := yaml.Unmarshal(projectData, &project); err != nil {
		t.Fatalf("unmarshal .cobuild.yaml: %v", err)
	}
	if project.Project != "cobuild-walkthrough" || project.Prefix != "wx-" {
		t.Fatalf(".cobuild.yaml = %#v, want project=cobuild-walkthrough prefix=wx-", project)
	}
}
