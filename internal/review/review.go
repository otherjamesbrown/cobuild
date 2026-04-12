package review

import "context"

const (
	VerdictApprove        = "approve"
	VerdictRequestChanges = "request-changes"

	SeverityCritical   = "critical"
	SeveritySuggestion = "suggestion"
	SeverityNit        = "nit"
)

// ReviewInput is the shared payload all reviewer implementations consume.
type ReviewInput struct {
	TaskID             string
	TaskTitle          string
	TaskSpec           string
	DesignContext      string
	Diff               string
	AcceptanceCriteria []string
	PRURL              string
}

// ReviewResult is the common structured output from all reviewer implementations.
type ReviewResult struct {
	Verdict  string
	Findings []Finding
	Summary  string
}

// Finding is a single actionable review item.
type Finding struct {
	File     string
	Line     int
	Severity string
	Body     string
}

// Reviewer runs a review and returns a structured verdict.
type Reviewer interface {
	Review(ctx context.Context, input ReviewInput) (*ReviewResult, error)
}
