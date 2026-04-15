package cmd

import (
	"fmt"
	"strings"
)

// normalizeGateVerdict accepts the canonical pass/fail vocabulary plus the
// historical "needs-fix" synonym, which is stored as "fail".
func normalizeGateVerdict(verdict string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(verdict))
	if normalized == "" {
		return "", fmt.Errorf("--verdict is required")
	}

	switch normalized {
	case "pass", "fail":
		return normalized, nil
	case "needs-fix":
		return "fail", nil
	default:
		return "", fmt.Errorf("--verdict must be 'pass' or 'fail', got %q", verdict)
	}
}
