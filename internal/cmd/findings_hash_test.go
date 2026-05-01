package cmd

import (
	"context"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

// escalationStore is a minimal store stub for shouldEscalateReview tests.
// Only GetPreviousGateHash is used; everything else panics.
type escalationStore struct {
	store.Store // embed to satisfy interface; unused methods panic
	hash        *string
	err         error
}

func (s *escalationStore) GetPreviousGateHash(_ context.Context, _, _ string, _ int) (*string, error) {
	return s.hash, s.err
}

func hashStrPtr(s string) *string { return &s }

// --- shouldEscalateReview tests ---

func TestShouldEscalateReview_NilResult(t *testing.T) {
	if reason := shouldEscalateReview(context.Background(), &escalationStore{}, nil); reason != "" {
		t.Fatalf("nil result should not escalate, got %q", reason)
	}
}

func TestShouldEscalateReview_SameHashAtThreshold(t *testing.T) {
	st := &escalationStore{hash: hashStrPtr("abcdef0123456789")}
	result := &GateVerdictResult{
		PipelineID:   "p1",
		GateName:     "review",
		Round:        2,
		FindingsHash: "abcdef0123456789",
	}
	reason := shouldEscalateReview(context.Background(), st, result)
	if reason == "" {
		t.Fatal("same hash at threshold should escalate")
	}
	if reason == "" || len(reason) < 10 {
		t.Fatalf("reason should be descriptive, got %q", reason)
	}
}

func TestShouldEscalateReview_DifferentHashBelowCap(t *testing.T) {
	st := &escalationStore{hash: hashStrPtr("different_hash__")}
	result := &GateVerdictResult{
		PipelineID:   "p1",
		GateName:     "review",
		Round:        3,
		FindingsHash: "abcdef0123456789",
	}
	reason := shouldEscalateReview(context.Background(), st, result)
	if reason != "" {
		t.Fatalf("different hash below cap should not escalate, got %q", reason)
	}
}

func TestShouldEscalateReview_RoundCapTriggersRegardlessOfHash(t *testing.T) {
	// Different hash each round — hash-based check won't fire.
	// But round >= reviewMaxRounds should still block.
	st := &escalationStore{hash: hashStrPtr("different_hash__")}
	result := &GateVerdictResult{
		PipelineID:   "p1",
		GateName:     "review",
		Round:        reviewMaxRounds,
		FindingsHash: "abcdef0123456789",
	}
	reason := shouldEscalateReview(context.Background(), st, result)
	if reason == "" {
		t.Fatalf("round cap (%d) should trigger escalation regardless of hash", reviewMaxRounds)
	}
}

func TestShouldEscalateReview_RoundCapTriggersWithoutHash(t *testing.T) {
	// No findings hash at all — hash-based check can't fire.
	// Round cap should still block.
	st := &escalationStore{}
	result := &GateVerdictResult{
		PipelineID: "p1",
		GateName:   "review",
		Round:      reviewMaxRounds,
	}
	reason := shouldEscalateReview(context.Background(), st, result)
	if reason == "" {
		t.Fatalf("round cap should trigger even without findings hash")
	}
}

func TestShouldEscalateReview_BelowCapNoPrevHash(t *testing.T) {
	// Round 2 with no previous hash record — shouldn't escalate.
	st := &escalationStore{hash: nil}
	result := &GateVerdictResult{
		PipelineID:   "p1",
		GateName:     "review",
		Round:        2,
		FindingsHash: "abcdef0123456789",
	}
	reason := shouldEscalateReview(context.Background(), st, result)
	if reason != "" {
		t.Fatalf("no previous hash should not escalate, got %q", reason)
	}
}

func TestShouldEscalateReview_Round1NeverEscalates(t *testing.T) {
	st := &escalationStore{hash: hashStrPtr("abcdef0123456789")}
	result := &GateVerdictResult{
		PipelineID:   "p1",
		GateName:     "review",
		Round:        1,
		FindingsHash: "abcdef0123456789",
	}
	reason := shouldEscalateReview(context.Background(), st, result)
	if reason != "" {
		t.Fatalf("round 1 should never escalate, got %q", reason)
	}
}

// --- computeFindingsHash tests ---

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
