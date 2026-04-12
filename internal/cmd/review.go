package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/worktree"
	"github.com/spf13/cobra"
)

const geminiBotLogin = "gemini-code-assist[bot]"
const directReviewPassBody = "Direct-mode task, no PR review required"

var processReviewCmd = &cobra.Command{
	Use:   "process-review <task-id>",
	Short: "Process Gemini code review and merge or re-dispatch for fixes",
	Long: `Checks if a Gemini review exists on the task's PR, classifies findings
by priority, and decides whether to merge or send the task back for fixes.

If no review exists yet and the PR is younger than --review-timeout, exits cleanly
(the poller will retry). If the timeout is exceeded (e.g. Gemini quota exhausted),
falls back to CI-based review: approve if CI passes, request-changes if CI has new failures.

On approve: records gate verdict, squash-merges PR, closes task, cleans up worktree.
On request-changes: records verdict, appends feedback to task, re-dispatches agent.`,
	Args:    cobra.ExactArgs(1),
	Example: "  cobuild process-review pf-abc123\n  cobuild process-review pf-abc123 --dry-run",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		task, err := conn.Get(ctx, taskID)
		if err != nil {
			return fmt.Errorf("get task: %w", err)
		}

		// Get PR URL
		prURL, _ := conn.GetMetadata(ctx, taskID, "pr_url")
		if prURL == "" && task.Metadata != nil {
			if pr, ok := task.Metadata["pr_url"]; ok {
				prURL = fmt.Sprintf("%v", pr)
			}
		}
		if prURL == "" {
			fmt.Printf("Task %s has no PR URL. Treating as direct-mode review.\n", taskID)

			if dryRun {
				if task.Status == "closed" {
					fmt.Println("[dry-run] Task already closed. Would reconcile direct-mode state only.")
				} else {
					fmt.Printf("[dry-run] Would record synthetic pass gate: %q\n", directReviewPassBody)
					fmt.Println("[dry-run] Would close task without external PR review.")
				}
				return nil
			}

			if task.Status != "closed" && cbStore != nil {
				repoRoot := findRepoRoot()
				pCfg, _ := config.LoadConfig(repoRoot)
				_, gateErr := RecordGateVerdict(ctx, conn, cbStore, taskID, "review", "pass", directReviewPassBody, 0, pCfg)
				if gateErr != nil {
					fmt.Printf("Warning: failed to record synthetic gate verdict: %v\n", gateErr)
				}
			}

			reconciled, err := reconcileReviewedTask(ctx, taskID)
			if err != nil {
				return err
			}
			if reconciled {
				printNextStep(taskID, "merged", "process-review")
			}
			return nil
		}

		// Extract owner/repo and PR number from URL
		repo, prNumber, err := parsePRURL(prURL)
		if err != nil {
			return fmt.Errorf("parse PR URL: %w", err)
		}

		// Check PR state — if the PR is already merged, a previous
		// process-review run probably got halfway (merged on GitHub but
		// failed to clean up local state due to the branch/worktree
		// ordering bug, or similar). Reconcile local state to match:
		// close the shard, clean up the worktree, advance the pipeline
		// phase if appropriate. Previously this branch silently returned
		// nil, which left cp-64af0f wedged with status=needs-review even
		// though PR #3 was already merged on GitHub.
		stateOut, err := exec.CommandContext(ctx, "gh", "pr", "view", prURL,
			"--json", "state", "--jq", ".state").Output()
		if err == nil {
			state := strings.TrimSpace(string(stateOut))
			if state == "MERGED" {
				fmt.Printf("PR already merged for %s. Reconciling local state.\n", taskID)
				if _, err := reconcileReviewedTask(ctx, taskID); err != nil {
					return err
				}

				printNextStep(taskID, "merged", "process-review")
				return nil
			}
			if state == "CLOSED" {
				fmt.Printf("PR is closed (not merged) for %s, skipping.\n", taskID)
				return nil
			}
		}

		reviewTimeout, _ := cmd.Flags().GetInt("review-timeout")

		// Step 1: Check for Gemini review
		reviews, err := getGeminiReviews(ctx, repo, prNumber)
		if err != nil {
			return fmt.Errorf("check reviews: %w", err)
		}

		var findings []reviewFinding
		reviewSource := "gemini"

		if len(reviews) == 0 {
			// Check PR age to decide: wait or fallback
			prAge := getPRAge(ctx, prURL)
			timeoutDuration := time.Duration(reviewTimeout) * time.Minute

			if prAge < timeoutDuration {
				remaining := timeoutDuration - prAge
				fmt.Printf("No Gemini review yet for %s (PR #%d, %s old, timeout %dm). Waiting %s.\n",
					taskID, prNumber, formatDuration(prAge), reviewTimeout, formatDuration(remaining))
				printNextStep(taskID, "waiting", "process-review")
				return nil
			}

			// Timeout exceeded — fall back to orchestrator review (CI-based)
			fmt.Printf("No Gemini review after %s for %s (PR #%d). Falling back to CI-based review.\n",
				formatDuration(prAge), taskID, prNumber)
			reviewSource = "ci-fallback"
		} else {
			// Step 2: Get inline comments from Gemini
			findings = getGeminiFindings(ctx, repo, prNumber)
		}

		// Step 3: Check CI
		ciResult := checkCI(ctx, repo, prNumber)

		// CI still pending — wait regardless of review source
		if ciResult.summary == "pending" {
			fmt.Printf("CI checks still pending for %s (PR #%d). Waiting.\n", taskID, prNumber)
			printNextStep(taskID, "waiting", "process-review")
			return nil
		}

		// Step 4: Decide verdict
		mustFix := 0
		medium := 0
		low := 0
		for _, f := range findings {
			switch f.Priority {
			case "high", "critical":
				mustFix++
			case "medium":
				medium++
			default:
				low++
			}
		}

		hasNewCIFailures := len(ciResult.newFailures) > 0
		verdict := "approve"
		if mustFix > 0 || hasNewCIFailures {
			verdict = "request-changes"
		}

		// Build summary
		var summary string
		if reviewSource == "gemini" {
			summary = fmt.Sprintf("Gemini review: %d high, %d medium, %d low finding(s). CI: %s",
				mustFix, medium, low, ciResult.summary)
		} else {
			summary = fmt.Sprintf("CI-based review (Gemini unavailable): CI: %s", ciResult.summary)
		}

		fmt.Printf("Review for %s (PR #%d): %s → %s\n", taskID, prNumber, summary, verdict)

		if dryRun {
			if verdict == "request-changes" {
				fmt.Println("\n[dry-run] Would send back for fixes:")
				for _, f := range findings {
					if f.Priority == "high" || f.Priority == "critical" {
						fmt.Printf("  [%s] %s:%d — %s\n", f.Priority, f.Path, f.Line, truncate(f.Body, 100))
					}
				}
				for _, fail := range ciResult.newFailures {
					fmt.Printf("  [ci] %s\n", fail)
				}
			} else {
				fmt.Println("[dry-run] Would merge PR and close task.")
			}
			return nil
		}

		// Record gate verdict. process-review uses "approve"/"request-changes"
		// as its internal verdict vocabulary, but pipeline_gates.verdict has
		// a CHECK constraint of IN ('pass','fail'). Normalise before storing
		// so we don't hit "violates check constraint" on every review that
		// reaches this path (observed on cp-64af0f, 2026-04-11).
		gateVerdict := "fail"
		if verdict == "approve" {
			gateVerdict = "pass"
		}
		if cbStore != nil {
			repoRoot := findRepoRoot()
			pCfg, _ := config.LoadConfig(repoRoot)
			body := buildVerdictBody(verdict, findings, ciResult)
			_, gateErr := RecordGateVerdict(ctx, conn, cbStore, taskID, "review", gateVerdict, body, 0, pCfg)
			if gateErr != nil {
				fmt.Printf("Warning: failed to record gate verdict: %v\n", gateErr)
			}
		}

		if verdict == "approve" {
			if err := doMerge(ctx, taskID, prURL); err != nil {
				return err
			}
			printNextStep(taskID, "merged", "process-review")
			return nil
		}
		if err := doRequestChanges(ctx, taskID, findings, ciResult); err != nil {
			return err
		}
		printNextStep(taskID, "redispatched", "process-review")
		return nil
	},
}

// ghReview represents a GitHub PR review.
type ghReview struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	State       string `json:"state"`
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
}

// reviewFinding represents a classified Gemini comment.
type reviewFinding struct {
	Path     string
	Line     int
	Priority string // high, medium, low
	Body     string
}

type ciCheckResult struct {
	summary     string
	newFailures []string
}

func getGeminiReviews(ctx context.Context, repo string, prNumber int) ([]ghReview, error) {
	out, err := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, prNumber)).Output()
	if err != nil {
		return nil, err
	}

	var reviews []ghReview
	if err := json.Unmarshal(out, &reviews); err != nil {
		return nil, err
	}

	var gemini []ghReview
	for _, r := range reviews {
		if r.User.Login == geminiBotLogin {
			gemini = append(gemini, r)
		}
	}
	return gemini, nil
}

// ghComment represents a GitHub PR inline comment.
type ghComment struct {
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Path                string `json:"path"`
	Line                int    `json:"line"`
	Body                string `json:"body"`
	PullRequestReviewID int64  `json:"pull_request_review_id"`
}

var priorityRe = regexp.MustCompile(`(high|medium|low|critical)-priority\.svg`)

func getGeminiFindings(ctx context.Context, repo string, prNumber int) []reviewFinding {
	out, err := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/comments", repo, prNumber)).Output()
	if err != nil {
		return nil
	}

	var comments []ghComment
	if err := json.Unmarshal(out, &comments); err != nil {
		return nil
	}

	var findings []reviewFinding
	for _, c := range comments {
		if c.User.Login != geminiBotLogin {
			continue
		}

		priority := "low"
		if m := priorityRe.FindStringSubmatch(c.Body); len(m) > 1 {
			priority = m[1]
		}

		findings = append(findings, reviewFinding{
			Path:     c.Path,
			Line:     c.Line,
			Priority: priority,
			Body:     c.Body,
		})
	}
	return findings
}

func checkCI(ctx context.Context, repo string, prNumber int) ciCheckResult {
	// Get check runs via API (gh pr checks doesn't support all fields)
	headOut, err := exec.CommandContext(ctx, "gh", "pr", "view",
		strconv.Itoa(prNumber), "--repo", repo,
		"--json", "headRefOid", "--jq", ".headRefOid").Output()
	if err != nil {
		return ciCheckResult{summary: "no checks (could not get commit)"}
	}
	commitSHA := strings.TrimSpace(string(headOut))

	out, err := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/commits/%s/check-runs", repo, commitSHA),
		"--jq", ".check_runs").Output()
	if err != nil {
		return ciCheckResult{summary: "no checks (API error)"}
	}

	type check struct {
		Name       string `json:"name"`
		Status     string `json:"status"`     // queued, in_progress, completed
		Conclusion string `json:"conclusion"` // success, failure, neutral, etc.
	}
	var checks []check
	if err := json.Unmarshal(out, &checks); err != nil {
		return ciCheckResult{summary: "no checks (parse error)"}
	}

	if len(checks) == 0 {
		return ciCheckResult{summary: "no CI checks configured"}
	}

	// Check for pending
	for _, c := range checks {
		if c.Status == "queued" || c.Status == "in_progress" {
			return ciCheckResult{summary: "pending"}
		}
	}

	// Find failures
	var prFailures []string
	for _, c := range checks {
		if c.Conclusion == "failure" || c.Conclusion == "error" {
			prFailures = append(prFailures, c.Name)
		}
	}

	if len(prFailures) == 0 {
		return ciCheckResult{summary: fmt.Sprintf("%d checks passed", len(checks))}
	}

	// Compare against main to find NEW failures only
	mainOut, err := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/actions/runs?branch=main&status=completed&per_page=1", repo),
		"--jq", ".workflow_runs[0].id").Output()

	newFailures := prFailures // assume all are new if we can't check main
	if err == nil {
		runID := strings.TrimSpace(string(mainOut))
		if runID != "" {
			jobsOut, err := exec.CommandContext(ctx, "gh", "api",
				fmt.Sprintf("repos/%s/actions/runs/%s/jobs", repo, runID),
				"--jq", `.jobs[] | .name + ":" + .conclusion`).Output()
			if err == nil {
				mainFails := map[string]bool{}
				for _, line := range strings.Split(string(jobsOut), "\n") {
					parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
					if len(parts) == 2 && parts[1] == "failure" {
						mainFails[parts[0]] = true
					}
				}
				newFailures = nil
				for _, name := range prFailures {
					if !mainFails[name] {
						newFailures = append(newFailures, name)
					}
				}
			}
		}
	}

	preExisting := len(prFailures) - len(newFailures)
	if len(newFailures) == 0 {
		return ciCheckResult{
			summary: fmt.Sprintf("%d checks passed, %d pre-existing failures", len(checks)-len(prFailures), preExisting),
		}
	}
	return ciCheckResult{
		summary:     fmt.Sprintf("%d new failure(s): %s", len(newFailures), strings.Join(newFailures, ", ")),
		newFailures: newFailures,
	}
}

func buildVerdictBody(verdict string, findings []reviewFinding, ci ciCheckResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Automated Review\n\n")
	fmt.Fprintf(&b, "**CI:** %s\n\n", ci.summary)

	if len(findings) > 0 {
		fmt.Fprintf(&b, "**Gemini findings:**\n")
		for _, f := range findings {
			fmt.Fprintf(&b, "- [%s] `%s:%d` — %s\n", f.Priority, f.Path, f.Line, firstLine(f.Body))
		}
		fmt.Fprintln(&b)
	} else {
		fmt.Fprintf(&b, "**Gemini findings:** none\n\n")
	}

	fmt.Fprintf(&b, "**Verdict:** %s\n", verdict)
	return b.String()
}

func reconcileReviewedTask(ctx context.Context, taskID string) (bool, error) {
	reconciled := false

	item, err := conn.Get(ctx, taskID)
	if err != nil {
		return false, fmt.Errorf("reconcile task %s: %w", taskID, err)
	}
	if item != nil && item.Status != "closed" {
		if err := conn.UpdateStatus(ctx, taskID, "closed"); err != nil {
			fmt.Printf("  Warning: failed to close task: %v\n", err)
		} else {
			reconciled = true
			fmt.Printf("  Task %s → closed.\n", taskID)
		}
	}

	wtPath, _ := conn.GetMetadata(ctx, taskID, "worktree_path")
	if wtPath != "" {
		archiveSessionLogs(wtPath, taskID)
		repoForCleanup, _ := config.RepoForProject(projectName)
		if err := worktree.Remove(ctx, repoForCleanup, wtPath, taskID); err != nil {
			fmt.Printf("  Warning: failed to remove worktree: %v\n", err)
		} else {
			reconciled = true
			fmt.Println("  Worktree cleaned up.")
		}
	}

	edges, _ := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
	if len(edges) == 0 {
		return reconciled, nil
	}

	designID := edges[0].ItemID
	siblings, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return reconciled, nil
	}

	allDone := true
	for _, s := range siblings {
		if s.Status != "closed" {
			allDone = false
			break
		}
	}
	if allDone && cbStore != nil {
		fmt.Printf("\nAll tasks for %s are closed. Advancing to done phase.\n", designID)
		if err := cbStore.UpdateRunPhase(ctx, designID, "done"); err != nil {
			fmt.Printf("  Warning: failed to advance phase: %v\n", err)
		} else {
			reconciled = true
		}
	}

	return reconciled, nil
}

func doMerge(ctx context.Context, taskID, prURL string) error {
	// Clean up the worktree FIRST, before calling `gh pr merge --delete-branch`.
	// Otherwise the local branch deletion fails with "cannot delete branch X
	// used by worktree at Y" (observed on cp-64af0f, 2026-04-11) because the
	// worktree still has the branch checked out at merge time. Remove the
	// worktree, which frees the branch, then merge-and-delete succeeds.
	wtPath, _ := conn.GetMetadata(ctx, taskID, "worktree_path")
	if wtPath != "" {
		archiveSessionLogs(wtPath, taskID)
		repoForCleanup, _ := config.RepoForProject(projectName)
		if err := worktree.Remove(ctx, repoForCleanup, wtPath, taskID); err != nil {
			fmt.Printf("  Warning: failed to remove worktree pre-merge: %v\n", err)
			// Continue anyway — merge without --delete-branch if needed below
		} else {
			fmt.Println("  Worktree cleaned up.")
		}
	}

	fmt.Printf("Merging %s...\n", prURL)
	mergeOut, err := exec.CommandContext(ctx, "gh", "pr", "merge", prURL,
		"--squash", "--delete-branch").CombinedOutput()
	if err != nil {
		return fmt.Errorf("merge failed: %s\n%s", err, string(mergeOut))
	}
	fmt.Println("  Merged.")

	// Close task
	if err := conn.UpdateStatus(ctx, taskID, "closed"); err != nil {
		fmt.Printf("  Warning: failed to close task: %v\n", err)
	} else {
		fmt.Printf("  Task %s → closed.\n", taskID)
	}

	// Check if all sibling tasks are done → advance pipeline
	edges, err := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
	if err == nil && len(edges) > 0 {
		designID := edges[0].ItemID
		siblings, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
		if err == nil {
			allDone := true
			for _, s := range siblings {
				if s.Status != "closed" {
					allDone = false
					break
				}
			}
			if allDone {
				fmt.Printf("\nAll tasks for %s are closed. Advancing to done phase.\n", designID)
				if cbStore != nil {
					if err := cbStore.UpdateRunPhase(ctx, designID, "done"); err != nil {
						fmt.Printf("  Warning: failed to advance phase: %v\n", err)
					}
					if err := cbStore.UpdateRunStatus(ctx, designID, "completed"); err != nil {
						fmt.Printf("  Warning: failed to mark completed: %v\n", err)
					}
				}
			}
		}
	}

	return nil
}

func doRequestChanges(ctx context.Context, taskID string, findings []reviewFinding, ci ciCheckResult) error {
	// Build feedback for the agent
	var fb strings.Builder
	fmt.Fprintf(&fb, "## Review Feedback\n\n")

	if len(ci.newFailures) > 0 {
		fmt.Fprintf(&fb, "### CI Failures (new)\n")
		for _, f := range ci.newFailures {
			fmt.Fprintf(&fb, "- %s\n", f)
		}
		fmt.Fprintln(&fb)
	}

	actionable := false
	fmt.Fprintf(&fb, "### Gemini Findings\n")
	for _, f := range findings {
		if f.Priority == "high" || f.Priority == "critical" {
			actionable = true
			// Strip the priority badge image from the body for readability
			body := priorityRe.ReplaceAllString(f.Body, "")
			body = strings.TrimPrefix(body, "![](https://www.gstatic.com/codereviewagent/)")
			body = strings.TrimSpace(body)
			fmt.Fprintf(&fb, "\n**[%s] `%s:%d`**\n%s\n", f.Priority, f.Path, f.Line, body)
		}
	}
	if !actionable && len(ci.newFailures) == 0 {
		// Only medium findings — still send back but note it
		for _, f := range findings {
			if f.Priority == "medium" {
				body := strings.TrimSpace(priorityRe.ReplaceAllString(f.Body, ""))
				fmt.Fprintf(&fb, "\n**[%s] `%s:%d`**\n%s\n", f.Priority, f.Path, f.Line, body)
			}
		}
	}

	feedback := fb.String()

	// Append feedback to task
	if err := conn.AppendContent(ctx, taskID, feedback); err != nil {
		fmt.Printf("Warning: failed to append feedback: %v\n", err)
	} else {
		fmt.Println("  Feedback appended to task.")
	}

	// Set status back to in_progress for re-dispatch
	if err := conn.UpdateStatus(ctx, taskID, "in_progress"); err != nil {
		fmt.Printf("Warning: failed to set in_progress: %v\n", err)
	}

	// Re-dispatch
	fmt.Printf("Re-dispatching %s for fixes...\n", taskID)
	out, err := exec.CommandContext(ctx, "cobuild", "dispatch", taskID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("re-dispatch failed: %v\n%s", err, string(out))
	}
	fmt.Printf("  %s\n", strings.TrimSpace(string(out)))
	return nil
}

// parsePRURL extracts owner/repo and PR number from a GitHub PR URL.
// Handles: https://github.com/owner/repo/pull/123
func parsePRURL(prURL string) (string, int, error) {
	// Strip trailing slashes
	prURL = strings.TrimRight(prURL, "/")
	parts := strings.Split(prURL, "/")
	// Expected: [..., owner, repo, "pull", number]
	for i, p := range parts {
		if p == "pull" && i+1 < len(parts) && i >= 2 {
			num, err := strconv.Atoi(parts[i+1])
			if err != nil {
				return "", 0, fmt.Errorf("invalid PR number: %s", parts[i+1])
			}
			repo := parts[i-2] + "/" + parts[i-1]
			return repo, num, nil
		}
	}
	return "", 0, fmt.Errorf("cannot parse PR URL: %s", prURL)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func truncate(s string, n int) string {
	s = firstLine(s)
	if len(s) > n {
		return s[:n-3] + "..."
	}
	return s
}

func getPRAge(ctx context.Context, prURL string) time.Duration {
	out, err := exec.CommandContext(ctx, "gh", "pr", "view", prURL,
		"--json", "createdAt", "--jq", ".createdAt").Output()
	if err != nil {
		return 0
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return time.Since(t)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func init() {
	processReviewCmd.Flags().Bool("dry-run", false, "Show verdict without merging or re-dispatching")
	processReviewCmd.Flags().Int("review-timeout", 10, "Minutes to wait for Gemini review before falling back to CI-based review")
	rootCmd.AddCommand(processReviewCmd)
}
