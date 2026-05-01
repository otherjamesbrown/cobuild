package cmd

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"

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
