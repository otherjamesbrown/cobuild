package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssembleContext_AutoDiscoversPhaseContextWithoutYAML(t *testing.T) {
	dir := t.TempDir()
	mustWriteContextFile(t, dir, ".cobuild/context/always/architecture.md", "# Architecture\n\nAlways on.")
	mustWriteContextFile(t, dir, ".cobuild/context/implement/anatomy.md", "# Project Anatomy\n\nImplement only.")

	got, err := AssembleContext(nil, dir, "dispatch", "implement", map[string]string{
		"dispatch-prompt": "# Dispatch Prompt\n\nDo the task.",
		"parent-design":   "# Parent Design\n\nDesign context.",
	}, nil)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}

	for _, want := range []string{
		"# Architecture",
		"# Project Anatomy",
		"# Dispatch Prompt",
		"# Parent Design",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("assembled context missing %q\ncontext:\n%s", want, got)
		}
	}
}

func TestAssembleContext_AutoDiscoveryRespectsPhaseBoundaries(t *testing.T) {
	dir := t.TempDir()
	mustWriteContextFile(t, dir, ".cobuild/context/always/architecture.md", "# Architecture\n\nAlways on.")
	mustWriteContextFile(t, dir, ".cobuild/context/design/domain.md", "# Design Domain\n\nDesign only.")
	mustWriteContextFile(t, dir, ".cobuild/context/implement/anatomy.md", "# Project Anatomy\n\nImplement only.")

	got, err := AssembleContext(&Config{}, dir, "dispatch", "design", map[string]string{
		"dispatch-prompt": "# Dispatch Prompt\n\nDo the task.",
	}, nil)
	if err != nil {
		t.Fatalf("AssembleContext: %v", err)
	}

	for _, want := range []string{"# Architecture", "# Design Domain", "# Dispatch Prompt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("assembled context missing %q\ncontext:\n%s", want, got)
		}
	}
	if strings.Contains(got, "# Project Anatomy") {
		t.Fatalf("implement-only anatomy leaked into design context:\n%s", got)
	}
}

func mustWriteContextFile(t *testing.T, repoRoot, relPath, content string) {
	t.Helper()
	full := filepath.Join(repoRoot, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
