package review

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultExternalReviewer = "gemini-code-assist[bot]"

var externalPriorityRe = regexp.MustCompile(`(high|medium|low|critical)-priority\.svg`)

type commandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

type ExternalReviewerConfig struct {
	Timeout          time.Duration
	DefaultPRTimeout time.Duration
	Reviewers        []string
	Runner           commandRunner
	CurrentTime      func() time.Time
}

type ExternalReviewer struct {
	timeout   time.Duration
	reviewers map[string]struct{}
	runner    commandRunner
	now       func() time.Time
}

type PendingReviewError struct {
	Remaining time.Duration
}

func (e *PendingReviewError) Error() string {
	return fmt.Sprintf("external review pending; retry in %s", e.Remaining.Round(time.Second))
}

type ReviewTimeoutError struct {
	Age     time.Duration
	Timeout time.Duration
}

func (e *ReviewTimeoutError) Error() string {
	return fmt.Sprintf("external review timed out after %s (timeout %s)", e.Age.Round(time.Second), e.Timeout.Round(time.Second))
}

type ghReview struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

type ghComment struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

func NewExternalReviewer(cfg ExternalReviewerConfig) *ExternalReviewer {
	reviewers := cfg.Reviewers
	if len(reviewers) == 0 {
		reviewers = []string{defaultExternalReviewer}
	}
	reviewerSet := make(map[string]struct{}, len(reviewers))
	for _, reviewer := range reviewers {
		reviewer = strings.TrimSpace(reviewer)
		if reviewer != "" {
			reviewerSet[reviewer] = struct{}{}
		}
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = cfg.DefaultPRTimeout
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}

	runner := cfg.Runner
	if runner == nil {
		runner = execRunner{}
	}

	now := cfg.CurrentTime
	if now == nil {
		now = time.Now
	}

	return &ExternalReviewer{
		timeout:   timeout,
		reviewers: reviewerSet,
		runner:    runner,
		now:       now,
	}
}

func (r *ExternalReviewer) Review(ctx context.Context, input ReviewInput) (*ReviewResult, error) {
	repo, prNumber, err := parseReviewPRURL(input.PRURL)
	if err != nil {
		return nil, err
	}

	reviews, err := r.getReviews(ctx, repo, prNumber)
	if err != nil {
		return nil, err
	}
	if len(reviews) == 0 {
		prAge, err := r.getPRAge(ctx, input.PRURL)
		if err != nil {
			return nil, err
		}
		if prAge < r.timeout {
			return nil, &PendingReviewError{Remaining: r.timeout - prAge}
		}
		return nil, &ReviewTimeoutError{Age: prAge, Timeout: r.timeout}
	}

	findings, err := r.getFindings(ctx, repo, prNumber)
	if err != nil {
		return nil, err
	}

	result := &ReviewResult{
		Verdict:  VerdictApprove,
		Findings: findings,
		Summary:  defaultSummary(VerdictApprove, findings),
	}
	for _, finding := range findings {
		if finding.Severity == SeverityCritical {
			result.Verdict = VerdictRequestChanges
			result.Summary = defaultSummary(VerdictRequestChanges, findings)
			break
		}
	}

	return result, nil
}

func (r *ExternalReviewer) getReviews(ctx context.Context, repo string, prNumber int) ([]ghReview, error) {
	out, err := r.runner.Output(ctx, "gh", "api", fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, prNumber))
	if err != nil {
		return nil, fmt.Errorf("fetch external reviews: %w", err)
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, fmt.Errorf("parse external reviews: %w", err)
	}

	filtered := make([]ghReview, 0, len(reviews))
	for _, review := range reviews {
		if _, ok := r.reviewers[review.User.Login]; ok {
			filtered = append(filtered, review)
		}
	}
	return filtered, nil
}

func (r *ExternalReviewer) getFindings(ctx context.Context, repo string, prNumber int) ([]Finding, error) {
	out, err := r.runner.Output(ctx, "gh", "api", fmt.Sprintf("repos/%s/pulls/%d/comments", repo, prNumber))
	if err != nil {
		return nil, fmt.Errorf("fetch external review comments: %w", err)
	}

	var comments []ghComment
	if err := json.Unmarshal(out, &comments); err != nil {
		return nil, fmt.Errorf("parse external review comments: %w", err)
	}

	findings := make([]Finding, 0, len(comments))
	for _, comment := range comments {
		if _, ok := r.reviewers[comment.User.Login]; !ok {
			continue
		}

		priority := "low"
		if match := externalPriorityRe.FindStringSubmatch(comment.Body); len(match) > 1 {
			priority = match[1]
		}

		findings = append(findings, Finding{
			File:     comment.Path,
			Line:     comment.Line,
			Severity: normalizeSeverity("", priority),
			Body:     strings.TrimSpace(comment.Body),
		})
	}
	return findings, nil
}

func (r *ExternalReviewer) getPRAge(ctx context.Context, prURL string) (time.Duration, error) {
	out, err := r.runner.Output(ctx, "gh", "pr", "view", prURL, "--json", "createdAt", "--jq", ".createdAt")
	if err != nil {
		return 0, fmt.Errorf("fetch external review PR age: %w", err)
	}

	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse external review PR age: %w", err)
	}
	return r.now().Sub(createdAt), nil
}

func parseReviewPRURL(prURL string) (string, int, error) {
	prURL = strings.TrimRight(strings.TrimSpace(prURL), "/")
	if prURL == "" {
		return "", 0, fmt.Errorf("missing PR URL for review")
	}

	parts := strings.Split(prURL, "/")
	for i, part := range parts {
		if part == "pull" && i >= 2 && i+1 < len(parts) {
			number, err := strconv.Atoi(parts[i+1])
			if err != nil {
				return "", 0, fmt.Errorf("invalid PR number in %q", prURL)
			}
			return parts[i-2] + "/" + parts[i-1], number, nil
		}
	}

	return "", 0, fmt.Errorf("cannot parse PR URL %q", prURL)
}
