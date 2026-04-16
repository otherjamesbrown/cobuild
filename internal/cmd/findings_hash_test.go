package cmd

import "testing"

func TestComputeFindingsHash_StructuredBody(t *testing.T) {
	body := `## Review Findings
- [high] dispatch.go:459 — straggler phase check
- [critical] merge.go:12 — missing --delete-branch`
	h := computeFindingsHash(body)
	if h == "" {
		t.Fatal("hash should not be empty")
	}
	if len(h) != 16 {
		t.Fatalf("hash length = %d, want 16 hex chars", len(h))
	}
}

func TestComputeFindingsHash_UnstructuredFallback(t *testing.T) {
	body := "No blocking issues found. The PR matches the task spec."
	h := computeFindingsHash(body)
	if h == "" || len(h) != 16 {
		t.Fatalf("unstructured body should produce a 16-char hash, got %q", h)
	}
}

func TestComputeFindingsHash_SameContentSameHash(t *testing.T) {
	a := "- [high] dispatch.go:459 — straggler phase check\n- [critical] merge.go:12 — missing flag"
	b := "- [critical] merge.go:12 — missing flag\n- [high] dispatch.go:459 — straggler phase check"
	if computeFindingsHash(a) != computeFindingsHash(b) {
		t.Fatal("reordered findings should produce the same hash")
	}
}

func TestComputeFindingsHash_WhitespaceNormalisation(t *testing.T) {
	a := "- [high]  dispatch.go:459  —  straggler   phase check"
	b := "- [high] dispatch.go:459 — straggler phase check"
	if computeFindingsHash(a) != computeFindingsHash(b) {
		t.Fatal("extra whitespace should be normalised")
	}
}

func TestComputeFindingsHash_DifferentFindingsDifferentHash(t *testing.T) {
	a := "- [high] dispatch.go:459 — straggler phase check"
	b := "- [high] review.go:100 — missing merge call"
	if computeFindingsHash(a) == computeFindingsHash(b) {
		t.Fatal("different findings should produce different hashes")
	}
}

func TestComputeFindingsHash_LongBodyTruncated(t *testing.T) {
	body := ""
	for i := 0; i < 200; i++ {
		body += "word "
	}
	h := computeFindingsHash(body)
	if h == "" || len(h) != 16 {
		t.Fatalf("long body should be truncated and hashed, got %q", h)
	}
}
