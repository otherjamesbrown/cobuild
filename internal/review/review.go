package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

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

type rawReviewResult struct {
	Verdict  string       `json:"verdict"`
	Findings []rawFinding `json:"findings"`
	Summary  string       `json:"summary"`
}

type rawFinding struct {
	File     string `json:"file"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Priority string `json:"priority"`
	Body     string `json:"body"`
	Message  string `json:"message"`
}

func buildReviewPrompt(input ReviewInput) string {
	var b strings.Builder
	b.WriteString("You are reviewing a PR for a CoBuild pipeline task.\n\n")

	if input.TaskTitle != "" || input.TaskID != "" {
		b.WriteString("## Task\n")
		if input.TaskTitle != "" {
			b.WriteString(input.TaskTitle)
			b.WriteByte('\n')
		}
		if input.TaskID != "" {
			fmt.Fprintf(&b, "Task ID: %s\n", input.TaskID)
		}
		if input.TaskSpec != "" {
			b.WriteByte('\n')
			b.WriteString(strings.TrimSpace(input.TaskSpec))
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if len(input.AcceptanceCriteria) > 0 {
		b.WriteString("## Acceptance Criteria\n")
		for _, item := range input.AcceptanceCriteria {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if input.DesignContext != "" {
		b.WriteString("## Parent Design\n")
		b.WriteString(strings.TrimSpace(input.DesignContext))
		b.WriteString("\n\n")
	}

	if input.PRURL != "" {
		b.WriteString("## Pull Request\n")
		b.WriteString(strings.TrimSpace(input.PRURL))
		b.WriteString("\n\n")
	}

	b.WriteString("## PR Diff\n")
	b.WriteString(strings.TrimSpace(input.Diff))
	b.WriteString("\n\n")

	b.WriteString("## Instructions\n")
	b.WriteString("Review this PR against its task spec. Return JSON only with the shape ")
	b.WriteString(`{"verdict":"approve"|"request-changes","findings":[{"file":"...","line":123,"severity":"critical"|"suggestion"|"nit","body":"..."}],"summary":"..."}`)
	b.WriteString(". If there are no blocking issues, set verdict to approve.")

	return b.String()
}

func parseReviewResult(raw string) (*ReviewResult, error) {
	payload, err := extractJSONPayload(raw)
	if err != nil {
		return nil, err
	}

	var decoded rawReviewResult
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return nil, fmt.Errorf("parse review result json: %w", err)
	}

	verdict, err := normalizeVerdict(decoded.Verdict)
	if err != nil {
		return nil, err
	}

	findings := make([]Finding, 0, len(decoded.Findings))
	for _, finding := range decoded.Findings {
		file := strings.TrimSpace(finding.File)
		if file == "" {
			file = strings.TrimSpace(finding.Path)
		}

		body := strings.TrimSpace(finding.Body)
		if body == "" {
			body = strings.TrimSpace(finding.Message)
		}

		findings = append(findings, Finding{
			File:     file,
			Line:     finding.Line,
			Severity: normalizeSeverity(finding.Severity, finding.Priority),
			Body:     body,
		})
	}

	summary := strings.TrimSpace(decoded.Summary)
	if summary == "" {
		summary = defaultSummary(verdict, findings)
	}

	return &ReviewResult{
		Verdict:  verdict,
		Findings: findings,
		Summary:  summary,
	}, nil
}

func extractJSONPayload(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("empty review response")
	}
	if json.Valid([]byte(trimmed)) {
		return trimmed, nil
	}

	if start := strings.Index(trimmed, "```"); start >= 0 {
		rest := trimmed[start+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
			if end := strings.Index(rest, "```"); end >= 0 {
				candidate := strings.TrimSpace(rest[:end])
				if json.Valid([]byte(candidate)) {
					return candidate, nil
				}
			}
		}
	}

	start := strings.IndexByte(trimmed, '{')
	end := strings.LastIndexByte(trimmed, '}')
	if start >= 0 && end > start {
		candidate := strings.TrimSpace(trimmed[start : end+1])
		if json.Valid([]byte(candidate)) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("review response did not contain valid json")
}

func normalizeVerdict(verdict string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(verdict)) {
	case VerdictApprove, "approved", "pass", "passed":
		return VerdictApprove, nil
	case VerdictRequestChanges, "reject", "rejected", "fail", "failed", "changes-requested":
		return VerdictRequestChanges, nil
	default:
		return "", fmt.Errorf("invalid review verdict %q", verdict)
	}
}

func normalizeSeverity(severity, priority string) string {
	value := strings.ToLower(strings.TrimSpace(severity))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(priority))
	}

	switch value {
	case SeverityCritical, "high", "blocker", "error":
		return SeverityCritical
	case SeveritySuggestion, "medium", "warning", "warn":
		return SeveritySuggestion
	case SeverityNit, "low", "info":
		return SeverityNit
	default:
		return SeveritySuggestion
	}
}

func defaultSummary(verdict string, findings []Finding) string {
	var critical, suggestion, nit int
	for _, finding := range findings {
		switch finding.Severity {
		case SeverityCritical:
			critical++
		case SeverityNit:
			nit++
		default:
			suggestion++
		}
	}

	return fmt.Sprintf("%s with %d critical, %d suggestion, %d nit finding(s)", verdict, critical, suggestion, nit)
}
