package contextaudit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInspect_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	r, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(r.Entries) != 0 || r.TotalBytes != 0 || r.FlaggedCount != 0 {
		t.Fatalf("expected empty report, got %+v", r)
	}
}

func TestInspect_SmallFileNoFlags(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".cobuild/context/always/notes.md"), "hello")
	r, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(r.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(r.Entries))
	}
	if len(r.Entries[0].Flags) != 0 {
		t.Fatalf("small file should have no flags, got %v", r.Entries[0].Flags)
	}
	if r.FlaggedCount != 0 {
		t.Fatalf("want 0 flagged, got %d", r.FlaggedCount)
	}
}

func TestInspect_OversizeFlag(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", LayerWarnBytes+100)
	mustWrite(t, filepath.Join(dir, ".cobuild/context/always/big.md"), big)
	r, _ := Inspect(dir)
	if len(r.Entries) != 1 || !hasFlag(r.Entries[0].Flags, "oversize") {
		t.Fatalf("want oversize flag, got %+v", r.Entries[0])
	}
}

func TestInspect_VeryLargeFlag(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("x", LayerHighBytes+100)
	mustWrite(t, filepath.Join(dir, ".cobuild/context/always/huge.md"), huge)
	r, _ := Inspect(dir)
	if !hasFlag(r.Entries[0].Flags, "very-large") {
		t.Fatalf("want very-large flag, got %+v", r.Entries[0])
	}
}

func TestInspect_CachePollutionFlag(t *testing.T) {
	dir := t.TempDir()
	// Needs to be ≥ LayerWarnBytes to trigger the content check.
	polluted := strings.Repeat("fill ", LayerWarnBytes/5+20) + "\n/repo/.pytest_cache/v/cache/\n"
	mustWrite(t, filepath.Join(dir, ".cobuild/context/always/anatomy.md"), polluted)
	r, _ := Inspect(dir)
	if !hasFlag(r.Entries[0].Flags, "cache-pollution") {
		t.Fatalf("want cache-pollution flag, got %+v", r.Entries[0])
	}
}

func TestInspect_SortedBySizeDesc(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".cobuild/context/always/small.md"), "s")
	mustWrite(t, filepath.Join(dir, ".cobuild/context/always/big.md"), strings.Repeat("x", 2000))
	r, _ := Inspect(dir)
	if r.Entries[0].RelPath != ".cobuild/context/always/big.md" {
		t.Fatalf("largest should sort first, got order: %v, %v", r.Entries[0].RelPath, r.Entries[1].RelPath)
	}
}

func TestInspect_AnnotatesCobuildOwnedAnatomy(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".cobuild.yaml"), "project: cobuild\n")
	mustWrite(t, filepath.Join(dir, ".cobuild/context/always/anatomy.md"), anatomyFixture())

	r, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(r.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(r.Entries))
	}
	entry := r.Entries[0]
	if entry.Annotation == nil {
		t.Fatalf("expected annotation for flagged anatomy entry")
	}
	if got := entry.Annotation.Owner; !strings.Contains(got, "cobuild") || !strings.Contains(got, "cobuild scan") {
		t.Fatalf("Owner = %q, want cobuild scan attribution", got)
	}
	if got := entry.Annotation.WhyLarge; !strings.Contains(got, "scanner indexed") || !strings.Contains(got, "node_modules/") {
		t.Fatalf("WhyLarge = %q, want scanner + skip hint", got)
	}
	if len(entry.Annotation.TryHere) != 2 || !strings.Contains(entry.Annotation.TryHere[0], "cobuild scan --skip") {
		t.Fatalf("TryHere = %v, want scan --skip guidance", entry.Annotation.TryHere)
	}
	if got := entry.Annotation.FileHere; !strings.Contains(got, "cobuild") {
		t.Fatalf("FileHere = %q, want cobuild filing hint", got)
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"annotation"`) || !strings.Contains(string(data), `"owner"`) {
		t.Fatalf("json output missing annotation fields: %s", data)
	}
}

func TestInspect_AnnotatesProjectOwnedMarkdown(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".cobuild.yaml"), "project: penfold\n")
	content := strings.Repeat("glossary line\n", 2200)
	path := filepath.Join(dir, ".cobuild/context/always/project-glossary.md")
	mustWrite(t, path, content)
	modTime := time.Date(2026, 4, 1, 9, 30, 0, 0, time.UTC)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	r, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	entry := r.Entries[0]
	if entry.Annotation == nil {
		t.Fatalf("expected annotation for project-authored markdown")
	}
	if got := entry.Annotation.Owner; got != "penfold (project-authored)" {
		t.Fatalf("Owner = %q, want project-authored", got)
	}
	if got := entry.Annotation.WhyLarge; !strings.Contains(got, "2201 lines") || !strings.Contains(got, "2026-04-01") {
		t.Fatalf("WhyLarge = %q, want line count + mod date", got)
	}
	if got := strings.Join(entry.Annotation.TryHere, "\n"); !strings.Contains(got, "Edit the file directly") && !strings.Contains(strings.ToLower(got), "edit the file directly") {
		t.Fatalf("TryHere = %v, want direct-edit guidance", entry.Annotation.TryHere)
	}
	if got := entry.Annotation.FileHere; got != "(N/A — local fix)" {
		t.Fatalf("FileHere = %q, want local fix", got)
	}
}

func TestInspect_AnnotatesAmbiguousMixedFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".cobuild.yaml"), "project: penfold\n")
	mustWrite(t, filepath.Join(dir, "skills/context-map.yaml"), strings.TrimSpace(`
generated_files:
  - path: .cobuild/context/always/mixed.md
    generator: context sync
    owner: ambiguous
    file_here: local project or cobuild
`))
	mustWrite(t, filepath.Join(dir, ".cobuild/context/always/mixed.md"), strings.Repeat("mixed ownership\n", 1500))

	r, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	entry := r.Entries[0]
	if entry.Annotation == nil {
		t.Fatalf("expected annotation for ambiguous file")
	}
	if got := entry.Annotation.Owner; !strings.Contains(got, "mixed ownership") || !strings.Contains(got, "context sync") {
		t.Fatalf("Owner = %q, want mixed ownership hint", got)
	}
	if got := strings.Join(entry.Annotation.TryHere, "\n"); !strings.Contains(got, "skills/context-map.yaml") {
		t.Fatalf("TryHere = %v, want context-map guidance", entry.Annotation.TryHere)
	}
	if got := entry.Annotation.FileHere; !strings.Contains(got, "local project") || !strings.Contains(got, "cobuild") {
		t.Fatalf("FileHere = %q, want both filing paths", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

func anatomyFixture() string {
	var sb strings.Builder
	sb.WriteString("# Project Anatomy\n\n")
	sb.WriteString(strings.Repeat("padding to trip size thresholds\n", 700))
	sb.WriteString("## internal/cmd/ (~57.3K tokens)\n\n")
	sb.WriteString("## node_modules/ (~41.0K tokens)\n\n")
	sb.WriteString("## vendor/ (~25.0K tokens)\n\n")
	sb.WriteString("## docs/ (~9.0K tokens)\n\n")
	return sb.String()
}
