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

// shouldEscalateReview checks whether the just-recorded gate verdict repeats
// the same findings as the previous round. Returns true when the pipeline
// should be blocked instead of re-dispatching.
func shouldEscalateReview(ctx context.Context, st store.Store, result *GateVerdictResult) bool {
	if result == nil || result.FindingsHash == "" || result.Round < reviewEscalationThreshold {
		return false
	}
	prevHash, err := st.GetPreviousGateHash(ctx, result.PipelineID, result.GateName, result.Round)
	if err != nil || prevHash == nil {
		return false
	}
	return *prevHash == result.FindingsHash
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
