package cmd

import (
	"strings"
	"testing"
)

// TestReviewCmdGateInferenceFromReadinessFlag verifies that `cobuild review`
// chooses the gate name from the readiness flag rather than from
// pipeline_run.CurrentPhase. The earlier (cb-3b091b) phase-derivation
// approach broke for tasks whose phase had advanced past "review" (e.g.
// phase=done after a previous successful review) and for direct calls
// where the store lookup returned ErrNotFound — both cases fell back to
// readiness-review and rejected `--readiness 0`. cb-118954 (slice 2 of
// cb-663873) is the regression marker.
func TestReviewCmdGateInferenceFromReadinessFlag(t *testing.T) {
	// Reset the flag set between subtests; cobra flags are sticky.
	resetFlags := func() {
		_ = reviewCmd.Flags().Set("verdict", "")
		_ = reviewCmd.Flags().Set("readiness", "0")
		_ = reviewCmd.Flags().Set("body", "")
		_ = reviewCmd.Flags().Set("body-file", "")
	}

	t.Run("readiness omitted does not error on the readiness check", func(t *testing.T) {
		resetFlags()
		_ = reviewCmd.Flags().Set("verdict", "fail")
		_ = reviewCmd.Flags().Set("body", "test")
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
		resetFlags()
		_ = reviewCmd.Flags().Set("verdict", "pass")
		_ = reviewCmd.Flags().Set("readiness", "3")
		_ = reviewCmd.Flags().Set("body", "test")
		err := reviewCmd.RunE(reviewCmd, []string{"cb-test"})
		// We don't expect success without a configured store; we just want to
		// make sure the readiness check accepts a valid 1-5 value.
		if err != nil && strings.Contains(err.Error(), "readiness must be 1-5") {
			t.Fatalf("review with readiness=3 failed validation: %v", err)
		}
	})

	t.Run("readiness=7 fails validation explicitly", func(t *testing.T) {
		resetFlags()
		_ = reviewCmd.Flags().Set("verdict", "pass")
		_ = reviewCmd.Flags().Set("readiness", "7")
		_ = reviewCmd.Flags().Set("body", "test")
		err := reviewCmd.RunE(reviewCmd, []string{"cb-test"})
		if err == nil {
			t.Fatalf("review with readiness=7 should fail validation, got nil")
		}
		if !strings.Contains(err.Error(), "readiness must be 1-5") {
			t.Fatalf("error = %q, want readiness-validation error", err.Error())
		}
	})
}
