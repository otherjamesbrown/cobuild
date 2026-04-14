package contextaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestRecommendation(t *testing.T) {
	tests := []struct {
		flags   []string
		wantSub string
	}{
		{[]string{"cache-pollution"}, "cobuild scan"},
		{[]string{"very-large"}, "split by phase"},
		{[]string{"oversize"}, "redundancy"},
		{nil, ""},
	}
	for _, tc := range tests {
		got := Recommendation(LayerEntry{Flags: tc.flags})
		if tc.wantSub == "" {
			if got != "" {
				t.Errorf("flags=%v want empty, got %q", tc.flags, got)
			}
			continue
		}
		if !strings.Contains(got, tc.wantSub) {
			t.Errorf("flags=%v want contains %q, got %q", tc.flags, tc.wantSub, got)
		}
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
