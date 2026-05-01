package cmd

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

// reviewEscalationThreshold is the number of consecutive fail gates with the
// same findings hash that triggers escalation. N=2 means: first fail
// dispatches a fix attempt; second fail with same hash → blocked.
const reviewEscalationThreshold = 2

// reviewMaxRounds is the hard cap on review-fix iterations. If the review
// gate has failed this many times, the pipeline blocks regardless of whether
// the findings hash changed between rounds. This prevents runaway loops
// where the review body varies slightly each round (defeating the hash-based
// check) but the fix agent is not converging. (cb-e20e84, cb-4c9241)
const reviewMaxRounds = 5

// shouldEscalateReview checks whether the review-fix loop should be stopped.
// Returns a non-empty reason string when the pipeline should block.
//
// Two triggers:
//  1. Same findings hash on consecutive rounds (cb-f55aa0, threshold=2)
//  2. Hard round cap exceeded (cb-e20e84/cb-4c9241, cap=5)
func shouldEscalateReview(ctx context.Context, st store.Store, result *GateVerdictResult) string {
	if result == nil {
		return ""
	}

	// Hard round cap — catches non-converging loops even when the findings
	// hash varies between rounds (the 33-round case from pf-33ad83).
	if result.Round >= reviewMaxRounds {
		return fmt.Sprintf("review gate failed %d times without converging (max %d rounds)", result.Round, reviewMaxRounds)
	}

	// Same-hash detection — catches identical findings early (round 2).
	if result.FindingsHash == "" || result.Round < reviewEscalationThreshold {
		return ""
	}
	prevHash, err := st.GetPreviousGateHash(ctx, result.PipelineID, result.GateName, result.Round)
	if err != nil || prevHash == nil {
		return ""
	}
	if *prevHash == result.FindingsHash {
		return fmt.Sprintf("same review finding repeated %d consecutive times", reviewEscalationThreshold)
	}
	return ""
}

// findingsHashMaxBody caps the body slice used when no structured findings
// lines are found. Keeps hashes comparable without memory-expensive diffs.
const findingsHashMaxBody = 500

var findingsLineRe = regexp.MustCompile(`(?m)^-\s*\[(high|critical|must[- ]fix|blocking)\]`)

// computeFindingsHash extracts structured review findings from the gate body
// and returns a truncated SHA-256 hex digest. If the body contains no
// recognisable structured lines, falls back to the first 500 characters.
//
// The hash is intentionally coarse: it normalises whitespace and sorts lines
// so that reordered or reformatted findings from different reviewer rounds
// still match.
func computeFindingsHash(body string) string {
	lines := extractFindingsLines(body)
	if len(lines) == 0 {
		b := strings.TrimSpace(body)
		if len(b) > findingsHashMaxBody {
			b = b[:findingsHashMaxBody]
		}
		lines = []string{b}
	}
	sort.Strings(lines)
	normalised := collapseWhitespace(strings.Join(lines, "\n"))
	h := sha256.Sum256([]byte(normalised))
	return fmt.Sprintf("%x", h[:8])
}

func extractFindingsLines(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if findingsLineRe.MatchString(trimmed) {
			out = append(out, collapseWhitespace(trimmed))
		}
	}
	return out
}

func collapseWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// markPipelineBlocked sets the pipeline run status to "blocked" so the
// poller and process-review skip it on subsequent cycles (cb-d95bcd).
func markPipelineBlocked(ctx context.Context, st store.Store, taskID, reason string) {
	if st == nil {
		return
	}
	if err := st.UpdateRunStatus(ctx, taskID, domain.StatusBlocked); err != nil {
		fmt.Printf("Warning: failed to mark pipeline blocked: %v\n", err)
	}
}

// notifyReviewBlocked sends a CXP message to the project's agent so the
// operator learns about the blocked pipeline without polling (cb-d95bcd).
// Best-effort — a failed notification doesn't block the circuit-break.
func notifyReviewBlocked(ctx context.Context, taskID string, result *GateVerdictResult, reason string) {
	if result == nil {
		return
	}

	// Derive recipient from the pipeline's project. Convention: agent-<project>.
	project := projectName
	if project == "" {
		return
	}
	recipient := "agent-" + project

	subject := fmt.Sprintf("Pipeline blocked: %s (round %d)", taskID, result.Round)
	body := fmt.Sprintf("Review-fix loop circuit-break fired.\n\nTask: %s\nRound: %d\nReason: %s\n\nAction required:\n  cobuild audit %s\n  cobuild reset %s",
		taskID, result.Round, reason, taskID, taskID)

	// Shell out to cxp — best-effort, don't fail the command.
	cmd := exec.CommandContext(ctx, "cxp", "message", "send", recipient, subject, "--body", body, "--kind", "circuit-break")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("Warning: failed to send circuit-break notification: %v (%s)\n", err, strings.TrimSpace(string(out)))
	} else {
		fmt.Printf("Notification sent to %s.\n", recipient)
	}
}
