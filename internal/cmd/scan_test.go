package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProjectScanExcludes(t *testing.T) {
	dir := t.TempDir()
	exclude := "# comment\n\ndata/raw\nSpecs\ntmp/\n"
	if err := os.MkdirAll(filepath.Join(dir, ".cobuild"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cobuild", "scan-exclude"), []byte(exclude), 0o644); err != nil {
		t.Fatal(err)
	}

	got := loadProjectScanExcludes(dir)
	for _, want := range []string{"data/raw", "Specs", "tmp"} {
		if !got[want] {
			t.Errorf("expected %q in excludes, got map %v", want, got)
		}
	}
	if got["# comment"] {
		t.Error("comment line should not be in excludes")
	}
}

func TestScanProject_RespectsProjectExcludes(t *testing.T) {
	dir := t.TempDir()
	must := func(path, content string) {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	must("keep/a.go", "package keep")
	must("data/raw/huge.txt", "big")
	must("Specs/01.md", "# spec")
	must(".cobuild/scan-exclude", "data/raw\nSpecs\n")

	entries, err := scanProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Path, "data/raw") || strings.HasPrefix(e.Path, "Specs") {
			t.Errorf("expected %s to be excluded, got entry %+v", e.Path, e)
		}
	}
	var sawKeep bool
	for _, e := range entries {
		if e.Path == "keep/a.go" {
			sawKeep = true
		}
	}
	if !sawKeep {
		t.Error("expected keep/a.go in scan output")
	}
}

func TestScanProject_RespectsPipelineYAMLSkipDirs(t *testing.T) {
	dir := t.TempDir()
	must := func(path, content string) {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	must("src/a.go", "package src")
	must("DD/diligence.md", "private")
	must("datalake/dump.csv", "x,y,z")
	must(".cobuild/pipeline.yaml", "scan:\n  skip_dirs:\n    - DD\n    - datalake\n")

	entries, err := scanProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Path, "DD") || strings.HasPrefix(e.Path, "datalake") {
			t.Errorf("config skip_dirs not honored; saw %s", e.Path)
		}
	}
	var sawSrc bool
	for _, e := range entries {
		if e.Path == "src/a.go" {
			sawSrc = true
		}
	}
	if !sawSrc {
		t.Error("expected src/a.go in output")
	}
}

func TestScanProject_DefaultSkipsPythonCaches(t *testing.T) {
	dir := t.TempDir()
	must := func(path string) {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("src/main.py")
	must(".pytest_cache/v/cache/lastfailed")
	must(".ruff_cache/0.1/abc")
	must(".mypy_cache/3.11/foo.json")
	must("target/debug/binary.txt")
	must("htmlcov/index.html")

	entries, _ := scanProject(dir)
	for _, e := range entries {
		for _, banned := range []string{".pytest_cache", ".ruff_cache", ".mypy_cache", "target", "htmlcov"} {
			if strings.HasPrefix(e.Path, banned) {
				t.Errorf("%s should be excluded by default, got %+v", banned, e)
			}
		}
	}
}

func TestFormatAnatomy_CollapsesLargeDirs(t *testing.T) {
	var entries []fileEntry
	// synthesize a directory with enough tokens to trip collapseDirTokens
	for i := 0; i < 5; i++ {
		entries = append(entries, fileEntry{
			Path:   "big/f" + string(rune('a'+i)) + ".txt",
			Dir:    "big",
			Name:   "f.txt",
			Lines:  100,
			Tokens: collapseDirTokens, // 5 * collapseDirTokens ≫ threshold
		})
	}
	entries = append(entries, fileEntry{
		Path: "small/one.go", Dir: "small", Name: "one.go", Tokens: 100,
	})

	out := formatAnatomy(entries, false)
	if !strings.Contains(out, "listing suppressed") {
		t.Fatalf("expected large dir to be collapsed, output:\n%s", out)
	}
	if !strings.Contains(out, "one.go") {
		t.Fatalf("small directory should still be listed in full, output:\n%s", out)
	}

	verbose := formatAnatomy(entries, true)
	if strings.Contains(verbose, "listing suppressed") {
		t.Fatalf("--verbose should not collapse, got:\n%s", verbose)
	}
}
