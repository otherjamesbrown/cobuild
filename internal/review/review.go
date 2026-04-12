package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/connector"
)

const (
	VerdictApprove        = "approve"
	VerdictRequestChanges = "request-changes"

	SeverityCritical   = "critical"
	SeveritySuggestion = "suggestion"
	SeverityNit        = "nit"
)

// ReviewResult is the structured outcome returned by a reviewer.
type ReviewResult struct {
	Verdict  string    `json:"verdict"`
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
}

// Finding is a single review issue.
type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Body     string `json:"body"`
}

// Reviewer evaluates a PR against its task and design context.
type Reviewer interface {
	Review(ctx context.Context, input ReviewInput) (*ReviewResult, error)
}

// ReviewInput is the provider-neutral payload used by built-in reviewers.
// Fields from the original implementation (Diff, PRURL, DesignContext) are
// retained for backward compatibility with existing reviewer implementations.
// The newer fields (PRDiff, ParentDesign*) are used by BuildInput and Prompt.
type ReviewInput struct {
	TaskID             string
	TaskTitle          string
	TaskSpec           string
	DesignContext      string
	Diff               string
	AcceptanceCriteria []string
	PRURL              string

	// Newer fields used by BuildInput / Prompt.
	ParentDesignID      string
	ParentDesignTitle   string
	ParentDesignContext string
	PRDiff              string
}

// BuildInput loads the task and its parent design through the connector and
// combines them with the PR diff into a provider-neutral review input.
func BuildInput(ctx context.Context, conn connector.Connector, taskID, prDiff string) (ReviewInput, error) {
	if conn == nil {
		return ReviewInput{}, fmt.Errorf("nil connector")
	}

	task, err := conn.Get(ctx, taskID)
	if err != nil {
		return ReviewInput{}, fmt.Errorf("get task %s: %w", taskID, err)
	}

	input := ReviewInput{
		TaskID:             task.ID,
		TaskTitle:          task.Title,
		TaskSpec:           strings.TrimSpace(task.Content),
		AcceptanceCriteria: ExtractAcceptanceCriteria(task.Content),
		PRDiff:             strings.TrimSpace(prDiff),
	}

	parentEdges, err := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
	if err == nil && len(parentEdges) > 0 {
		parent, parentErr := conn.Get(ctx, parentEdges[0].ItemID)
		if parentErr == nil {
			input.ParentDesignID = parent.ID
			input.ParentDesignTitle = parent.Title
			input.ParentDesignContext = strings.TrimSpace(parent.Content)
		}
	}

	return input, nil
}

// Prompt returns the structured review prompt shared by built-in providers.
func Prompt(input ReviewInput) string {
	var b strings.Builder

	b.WriteString("You are reviewing a PR for a CoBuild pipeline task.\n\n")
	b.WriteString("Return JSON only.\n")
	b.WriteString("Use this exact shape:\n")
	b.WriteString("{\"verdict\":\"approve\"|\"request-changes\",\"findings\":[{\"file\":\"path/to/file\",\"line\":123,\"severity\":\"critical\"|\"suggestion\"|\"nit\",\"body\":\"issue and fix\"}],\"summary\":\"short summary\"}\n\n")

	b.WriteString("## Task\n")
	if input.TaskTitle != "" {
		b.WriteString(input.TaskTitle)
		b.WriteString("\n")
	}
	if input.TaskID != "" {
		b.WriteString("Task ID: ")
		b.WriteString(input.TaskID)
		b.WriteString("\n")
	}
	if input.TaskSpec != "" {
		b.WriteString("\n")
		b.WriteString(input.TaskSpec)
		b.WriteString("\n")
	}

	b.WriteString("\n## Acceptance Criteria\n")
	if len(input.AcceptanceCriteria) == 0 {
		b.WriteString("None provided.\n")
	} else {
		for _, criterion := range input.AcceptanceCriteria {
			b.WriteString("- ")
			b.WriteString(criterion)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n## Parent Design\n")
	if input.ParentDesignTitle != "" {
		b.WriteString(input.ParentDesignTitle)
		b.WriteString("\n")
	}
	if input.ParentDesignID != "" {
		b.WriteString("Design ID: ")
		b.WriteString(input.ParentDesignID)
		b.WriteString("\n")
	}
	if input.ParentDesignContext != "" {
		b.WriteString("\n")
		b.WriteString(input.ParentDesignContext)
		b.WriteString("\n")
	} else {
		b.WriteString("None provided.\n")
	}

	b.WriteString("\n## PR Diff\n")
	if input.PRDiff != "" {
		b.WriteString(input.PRDiff)
		b.WriteString("\n")
	} else {
		b.WriteString("No diff provided.\n")
	}

	b.WriteString("\n## Instructions\n")
	b.WriteString("Review the PR against the task spec, acceptance criteria, and parent design.\n")
	b.WriteString("Approve only if the implementation matches the spec and there are no critical issues.\n")
	b.WriteString("Each finding must include file, line, severity, and a concrete explanation of what should change.\n")

	return strings.TrimSpace(b.String())
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
