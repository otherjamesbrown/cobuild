package codex

import (
	"encoding/json"
	"os"
	"testing"
)

// TestE2ESessionLogParse is a one-shot harness used to verify that
// ParseSessionStats produces correct results against a REAL session.log
// captured from a live `codex exec --json` run. Set COBUILD_CODEX_SMOKE_LOG
// to the log path to run it; skipped otherwise.
//
// This file is intentionally kept under build-tag-free regular tests so it
// can be invoked from the phase-8 smoke flow via `go test -run E2E`; it's
// safe to delete after the dispatch refactor ships.
func TestE2ESessionLogParse(t *testing.T) {
	logPath := os.Getenv("COBUILD_CODEX_SMOKE_LOG")
	if logPath == "" {
		t.Skip("COBUILD_CODEX_SMOKE_LOG not set; skipping live-log smoke check")
	}
	stats, err := New().ParseSessionStats(logPath)
	if err != nil {
		t.Fatalf("ParseSessionStats: %v", err)
	}
	j, _ := json.MarshalIndent(stats, "", "  ")
	t.Logf("stats from %s:\n%s", logPath, string(j))

	if stats.SessionUUID == "" {
		t.Error("expected non-empty SessionUUID")
	}
	if stats.TurnCount == 0 {
		t.Error("expected at least one turn")
	}
	if stats.InputTokens == 0 {
		t.Error("expected non-zero InputTokens")
	}
	if stats.OutputTokens == 0 {
		t.Error("expected non-zero OutputTokens")
	}
}
