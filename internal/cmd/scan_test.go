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

func TestLoadProjectScanYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".cobuild"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cobuild", "scan.yaml"), []byte("skip:\n  - tmp/\nskip_dirs:\n  - data/raw\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := loadProjectScanYAML(dir)
	for _, want := range []string{"tmp", "data/raw"} {
		if !containsScanString(got, want) {
			t.Fatalf("scan.yaml entries = %v, want %q", got, want)
		}
	}
}

func TestScanProject_RespectsScanYAMLSkipDirsAndCLIOverrides(t *testing.T) {
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
	must("tmp/generated.txt", "temp")
	must("reports/archive.md", "old")
	must(".cobuild/scan.yaml", "skip_dirs:\n  - tmp\n")

	entries, err := scanProject(dir, "reports")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Path, "tmp") || strings.HasPrefix(e.Path, "reports") {
			t.Fatalf("scan skip sources not honored; saw %s", e.Path)
		}
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

func TestScanProject_DefaultSkipsRecursiveContextAndGeneratedFiles(t *testing.T) {
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

	must("src/main.go", "package main")
	must(".cobuild/context/always/anatomy.md", "# old anatomy")
	must(".cobuild/dispatch-context.md", "# transient dispatch context")
	must(".specify/memory/constitution.md", "# constitution")
	must("api/proto/foo/v1/foo.pb.go", "package foov1")
	must("api/proto/foo/v1/foo.proto", "syntax = \"proto3\";")

	entries, err := scanProject(dir)
	if err != nil {
		t.Fatal(err)
	}

	var sawMain, sawProto bool
	for _, e := range entries {
		switch {
		case e.Path == "src/main.go":
			sawMain = true
		case e.Path == "api/proto/foo/v1/foo.proto":
			sawProto = true
		case strings.HasPrefix(e.Path, ".cobuild/context"):
			t.Fatalf("recursive context output should be excluded, got %s", e.Path)
		case e.Path == ".cobuild/dispatch-context.md":
			t.Fatalf("transient dispatch context should be excluded, got %s", e.Path)
		case strings.HasPrefix(e.Path, ".specify"):
			t.Fatalf(".specify should be excluded by default, got %s", e.Path)
		case strings.HasSuffix(e.Path, ".pb.go"):
			t.Fatalf("generated protobuf stubs should be excluded by default, got %s", e.Path)
		}
	}
	if !sawMain {
		t.Fatal("expected src/main.go in scan output")
	}
	if !sawProto {
		t.Fatal("expected foo.proto in scan output")
	}
}

func TestRemoveLegacyAnatomy(t *testing.T) {
	dir := t.TempDir()
	legacyPath := legacyAnatomyPath(dir)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeLegacyAnatomy(dir); err != nil {
		t.Fatalf("removeLegacyAnatomy: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy anatomy should be removed, stat err=%v", err)
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
	if !strings.Contains(out, "summarized") {
		t.Fatalf("expected large dir to be collapsed, output:\n%s", out)
	}
	if !strings.Contains(out, "one.go") {
		t.Fatalf("small directory should still be listed in full, output:\n%s", out)
	}

	verbose := formatAnatomy(entries, true)
	if strings.Contains(verbose, "summarized") {
		t.Fatalf("--verbose should not collapse, got:\n%s", verbose)
	}
}

func containsScanString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
