package cmd

import (
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func resetReviewGateFlags(t *testing.T) {
	t.Helper()
	_ = reviewCmd.Flags().Set("verdict", "")
	_ = reviewCmd.Flags().Set("readiness", "0")
	_ = reviewCmd.Flags().Set("body", "")
	_ = reviewCmd.Flags().Set("body-file", "")
}

func TestReviewCmdNormalizesNeedsFixVerdictToFail(t *testing.T) {
	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-task"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-task",
		CurrentPhase: "review",
		Status:       "active",
	}

	restore := installTestGlobals(t, fc, fs, "cobuild")
	defer restore()

	resetReviewGateFlags(t)
	t.Cleanup(func() { resetReviewGateFlags(t) })

	_ = reviewCmd.Flags().Set("verdict", "needs-fix")
	_ = reviewCmd.Flags().Set("body", "Missing regression coverage.")

	if err := reviewCmd.RunE(reviewCmd, []string{"cb-task"}); err != nil {
		t.Fatalf("review returned error: %v", err)
	}

	if len(fs.gates) != 1 {
		t.Fatalf("recorded %d gates, want 1", len(fs.gates))
	}
	if got := fs.gates[0].Verdict; got != "fail" {
		t.Fatalf("stored verdict = %q, want fail", got)
	}
	if got := fs.runs["cb-task"].CurrentPhase; got != "review" {
		t.Fatalf("phase = %q, want review after fail verdict", got)
	}
}

// TestReviewCmdRefusesPassWhenTaskHasOpenPR verifies the cb-465d17 guard:
// `cobuild review --verdict pass` on a task with pr_url metadata must fail,
// because `cobuild review` doesn't merge. A PASS there would advance phase=done
// while leaving the PR open — the exact failure mode observed on cb-b78c67,
// where three PASS reviews in the same minute left PR #94 unmerged.
//
// The correct caller in that situation is `cobuild process-review`, which
// consumes .cobuild/gate-verdict.json, records the gate, AND runs gh pr merge
// before advancing the phase.
func TestReviewCmdRefusesPassWhenTaskHasOpenPR(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:     "cb-taskpr",
		Title:  "task with PR",
		Type:   "task",
		Status: "needs-review",
		Metadata: map[string]any{
			domain.MetaPRURL: "https://github.com/otherjamesbrown/cobuild/pull/94",
		},
	})
	fs := newFakeStore()
	fs.runs["cb-taskpr"] = &store.PipelineRun{
		ID:           "run-2",
		DesignID:     "cb-taskpr",
		CurrentPhase: "review",
		Status:       "active",
	}

	restore := installTestGlobals(t, fc, fs, "cobuild")
	defer restore()

	resetReviewGateFlags(t)
	t.Cleanup(func() { resetReviewGateFlags(t) })

	_ = reviewCmd.Flags().Set("verdict", "pass")
	_ = reviewCmd.Flags().Set("body", "LGTM.")

	err := reviewCmd.RunE(reviewCmd, []string{"cb-taskpr"})
	if err == nil {
		t.Fatal("review --verdict pass on task with pr_url should error (cb-465d17), got nil")
	}
	for _, want := range []string{"cb-465d17", "process-review", "pull/94"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	if len(fs.gates) != 0 {
		t.Fatalf("refused review should not record a gate, got %d", len(fs.gates))
	}
	if got := fs.runs["cb-taskpr"].CurrentPhase; got != "review" {
		t.Fatalf("phase = %q after refused pass, want review (must not advance)", got)
	}
}

// TestReviewCmdAllowsFailForTaskWithOpenPR verifies the guard is narrow: a FAIL
// verdict must still flow through `cobuild review` because doRequestChanges
// (re-dispatch loop) runs inside process-review on fail, and FAIL doesn't
// advance phase anyway. Only pass needs the guard.
func TestReviewCmdAllowsFailForTaskWithOpenPR(t *testing.T) {
	fc := newFakeConnector()
	fc.addItem(&connector.WorkItem{
		ID:     "cb-taskpr-fail",
		Title:  "task with PR",
		Type:   "task",
		Status: "needs-review",
		Metadata: map[string]any{
			domain.MetaPRURL: "https://github.com/otherjamesbrown/cobuild/pull/94",
		},
	})
	fs := newFakeStore()
	fs.runs["cb-taskpr-fail"] = &store.PipelineRun{
		ID:           "run-3",
		DesignID:     "cb-taskpr-fail",
		CurrentPhase: "review",
		Status:       "active",
	}

	restore := installTestGlobals(t, fc, fs, "cobuild")
	defer restore()

	resetReviewGateFlags(t)
	t.Cleanup(func() { resetReviewGateFlags(t) })

	_ = reviewCmd.Flags().Set("verdict", "fail")
	_ = reviewCmd.Flags().Set("body", "Missing coverage.")

	if err := reviewCmd.RunE(reviewCmd, []string{"cb-taskpr-fail"}); err != nil {
		t.Fatalf("review --verdict fail on task with pr_url should record gate, got %v", err)
	}
	if len(fs.gates) != 1 || fs.gates[0].Verdict != "fail" {
		t.Fatalf("expected one fail gate, got %+v", fs.gates)
	}
	if got := fs.runs["cb-taskpr-fail"].CurrentPhase; got != "review" {
		t.Fatalf("phase = %q, want review after fail verdict", got)
	}
}

func TestReviewCmdRejectsInvalidVerdictValue(t *testing.T) {
	resetReviewGateFlags(t)
	t.Cleanup(func() { resetReviewGateFlags(t) })

	_ = reviewCmd.Flags().Set("verdict", "invalidvalue")
	_ = reviewCmd.Flags().Set("body", "test")

	err := reviewCmd.RunE(reviewCmd, []string{"cb-task"})
	if err == nil {
		t.Fatal("review with invalid verdict should fail, got nil")
	}
	if !strings.Contains(err.Error(), "--verdict must be 'pass' or 'fail'") {
		t.Fatalf("error = %q, want invalid-verdict error", err.Error())
	}
}

// TestReviewCmdGateInferenceFromReadinessFlag verifies that `cobuild review`
// chooses the gate name from the readiness flag rather than from
// pipeline_run.CurrentPhase. The earlier (cb-3b091b) phase-derivation
// approach broke for tasks whose phase had advanced past "review" (e.g.
// phase=done after a previous successful review) and for direct calls
// where the store lookup returned ErrNotFound — both cases fell back to
// readiness-review and rejected `--readiness 0`. cb-118954 (slice 2 of
// cb-663873) is the regression marker.
func TestReviewCmdGateInferenceFromReadinessFlag(t *testing.T) {
	t.Run("readiness omitted does not error on the readiness check", func(t *testing.T) {
		resetReviewGateFlags(t)
		_ = reviewCmd.Flags().Set("verdict", "fail")
		_ = reviewCmd.Flags().Set("body", "test")
		t.Cleanup(func() { resetReviewGateFlags(t) })
		// The store lookup will fail (no test setup), but the function should
		// pass the readiness check (gate=review, no required score) and
		// instead error later on "no store configured" or similar. Anything
		// EXCEPT the readiness error means the gate inference is correct.
		err := reviewCmd.RunE(reviewCmd, []string{"cb-test"})
		if err != nil && strings.Contains(err.Error(), "readiness must be 1-5") {
			t.Fatalf("review with omitted readiness failed the readiness check (cb-3b091b regression): %v", err)
		}
	})

	t.Run("readiness=3 takes the readiness-review path", func(t *testing.T) {
		resetReviewGateFlags(t)
		_ = reviewCmd.Flags().Set("verdict", "pass")
		_ = reviewCmd.Flags().Set("readiness", "3")
		_ = reviewCmd.Flags().Set("body", "test")
		t.Cleanup(func() { resetReviewGateFlags(t) })
		err := reviewCmd.RunE(reviewCmd, []string{"cb-test"})
		// We don't expect success without a configured store; we just want to
		// make sure the readiness check accepts a valid 1-5 value.
		if err != nil && strings.Contains(err.Error(), "readiness must be 1-5") {
			t.Fatalf("review with readiness=3 failed validation: %v", err)
		}
	})

	t.Run("readiness=7 fails validation explicitly", func(t *testing.T) {
		resetReviewGateFlags(t)
		_ = reviewCmd.Flags().Set("verdict", "pass")
		_ = reviewCmd.Flags().Set("readiness", "7")
		_ = reviewCmd.Flags().Set("body", "test")
		t.Cleanup(func() { resetReviewGateFlags(t) })
		err := reviewCmd.RunE(reviewCmd, []string{"cb-test"})
		if err == nil {
			t.Fatalf("review with readiness=7 should fail validation, got nil")
		}
		if !strings.Contains(err.Error(), "readiness must be 1-5") {
			t.Fatalf("error = %q, want readiness-validation error", err.Error())
		}
	})
}
