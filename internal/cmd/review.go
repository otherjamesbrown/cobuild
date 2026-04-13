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
	"github.com/otherjamesbrown/cobuild/internal/connector"
	llmreview "github.com/otherjamesbrown/cobuild/internal/review"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/worktree"
	"github.com/spf13/cobra"
)

const geminiBotLogin = "gemini-code-assist[bot]"
const directReviewPassBody = "Direct-mode task, no PR review required"

var (
	reviewCommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).Output()
	}
	reviewCommandCombinedOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	reviewConfigLoader = func() *config.Config {
		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)
		if pCfg == nil {
			pCfg = config.DefaultConfig()
		}
		return pCfg
	}
	reviewerFactory = func(provider string, cfg llmreview.ProviderConfig) (llmreview.Reviewer, error) {
		return llmreview.NewReviewer(provider, cfg)
	}
)

var processReviewCmd = &cobra.Command{
	Use:   "process-review <task-id>",
	Short: "Review a task's PR and merge or re-dispatch for fixes",
	Long: `Reviews a task's PR using the configured provider (auto, claude, openai, or external),
classifies findings by priority, and decides whether to merge or send the task back for fixes.

With provider=auto (default), uses cross-model review: code written by Codex is reviewed by
Claude, and vice versa. With provider=external, waits for an external review (e.g. Gemini
Code Assist) up to --review-timeout minutes, then falls back to CI-based review.

If kb_sync is enabled for the project, runs cobuild kb-sync after a successful merge to
update any affected KB articles.

On approve: records gate verdict, squash-merges PR, runs kb-sync, closes task, cleans up worktree.
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
			handled, err := maybeProcessDirectReview(ctx, taskID, task, dryRun)
			if handled {
				return err
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("no PR URL for task %s", taskID)
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
		stateOut, err := reviewCommandOutput(ctx, "gh", "pr", "view", prURL,
			"--json", "state", "--jq", ".state")
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
		pCfg := reviewConfigLoader()
		session := getReviewSession(ctx, taskID)

		var (
			findings      []reviewFinding
			reviewResult  *llmreview.ReviewResult
			reviewSource  string
			reviewWarning string
		)

		writerRuntime, writerModel := sessionRuntimeModel(session)
		spec := llmreview.ResolveReviewer(pCfg.Review, writerRuntime, writerModel)
		if spec.Provider == "external" {
			external, err := runExternalReview(ctx, repo, prNumber, taskID, prURL, reviewTimeout)
			if err != nil {
				return err
			}
			if external.waiting {
				printNextStep(taskID, "waiting", "process-review")
				return nil
			}
			findings = external.findings
			reviewSource = external.source
			reviewWarning = external.warning
		} else {
			input, err := buildReviewInput(ctx, taskID, task, repo, prNumber)
			if err != nil {
				return fmt.Errorf("build review input: %w", err)
			}
			providerCfg := llmreview.ProviderConfig{
				Model:   spec.Model,
				Timeout: pCfg.Review.ReviewTimeout(),
			}
			reviewer, err := reviewerFactory(spec.Provider, providerCfg)
			if err != nil {
				return fmt.Errorf("configure reviewer: %w", err)
			}
			if reviewer == nil {
				return fmt.Errorf("review provider %q did not return a reviewer", spec.Provider)
			}
			reviewResult, err = reviewer.Review(ctx, input)
			if err != nil {
				reviewSource = "ci-fallback"
				reviewWarning = fmt.Sprintf("built-in %s review failed: %v", spec.Provider, err)
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s. Falling back to CI-only review.\n", reviewWarning)
			} else {
				reviewSource = spec.Provider
				findings = reviewResultToFindings(reviewResult)
				if pCfg.Review.PostCommentsEnabled() {
					if err := postReviewComment(ctx, prURL, reviewResult); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to post review comment: %v\n", err)
					}
				}
			}
		}

		// Step 3: Check CI
		ciResult := checkCI(ctx, repo, prNumber)

		// CI still pending — wait regardless of review source
		if pCfg.Review.WaitForCI != nil && *pCfg.Review.WaitForCI && ciResult.summary == "pending" {
			fmt.Printf("CI checks still pending for %s (PR #%d). Waiting.\n", taskID, prNumber)
			printNextStep(taskID, "waiting", "process-review")
			return nil
		}

		// Step 4: Decide verdict
		hasNewCIFailures := len(ciResult.newFailures) > 0
		verdict := determineReviewVerdict(reviewResult, findings, hasNewCIFailures)

		// Build summary
		summary := buildReviewSummary(reviewSource, reviewResult, findings, ciResult, reviewWarning)

		fmt.Printf("Review for %s (PR #%d): %s → %s\n", taskID, prNumber, summary, verdict)

		if dryRun {
			if verdict == "request-changes" {
				fmt.Println("\n[dry-run] Would send back for fixes:")
				for _, f := range findings {
					if f.Priority == "high" || f.Priority == "critical" || reviewSource == "claude" || reviewSource == "openai" {
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
			body := buildVerdictBody(verdict, reviewSource, reviewResult, findings, ciResult, reviewWarning)
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
		if err := doRequestChanges(ctx, taskID, findings, ciResult, reviewSource); err != nil {
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
	out, err := reviewCommandOutput(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, prNumber))
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
	out, err := reviewCommandOutput(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d/comments", repo, prNumber))
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
	headOut, err := reviewCommandOutput(ctx, "gh", "pr", "view",
		strconv.Itoa(prNumber), "--repo", repo,
		"--json", "headRefOid", "--jq", ".headRefOid")
	if err != nil {
		return ciCheckResult{summary: "no checks (could not get commit)"}
	}
	commitSHA := strings.TrimSpace(string(headOut))

	out, err := reviewCommandOutput(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/commits/%s/check-runs", repo, commitSHA),
		"--jq", ".check_runs")
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
	mainOut, err := reviewCommandOutput(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/actions/runs?branch=main&status=completed&per_page=1", repo),
		"--jq", ".workflow_runs[0].id")

	newFailures := prFailures // assume all are new if we can't check main
	if err == nil {
		runID := strings.TrimSpace(string(mainOut))
		if runID != "" {
			jobsOut, err := reviewCommandOutput(ctx, "gh", "api",
				fmt.Sprintf("repos/%s/actions/runs/%s/jobs", repo, runID),
				"--jq", `.jobs[] | .name + ":" + .conclusion`)
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

func buildVerdictBody(verdict, reviewSource string, reviewResult *llmreview.ReviewResult, findings []reviewFinding, ci ciCheckResult, warning string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Automated Review\n\n")
	if reviewSource != "" {
		fmt.Fprintf(&b, "**Reviewer:** %s\n", reviewSource)
	}
	fmt.Fprintf(&b, "**CI:** %s\n\n", ci.summary)
	if warning != "" {
		fmt.Fprintf(&b, "**Warning:** %s\n\n", warning)
	}
	if reviewResult != nil {
		if reviewResult.Summary != "" {
			fmt.Fprintf(&b, "**LLM summary:** %s\n\n", reviewResult.Summary)
		}
		fmt.Fprintf(&b, "**LLM verdict:** %s\n\n", reviewResult.Verdict)
	}

	if len(findings) > 0 {
		fmt.Fprintf(&b, "**Findings:**\n")
		for _, f := range findings {
			fmt.Fprintf(&b, "- [%s] `%s:%d` — %s\n", f.Priority, f.Path, f.Line, firstLine(f.Body))
		}
		fmt.Fprintln(&b)
	} else {
		fmt.Fprintf(&b, "**Findings:** none\n\n")
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
		if s.Type != "" && s.Type != "task" {
			continue
		}
		if s.Status != "closed" {
			allDone = false
			break
		}
	}
	if allDone && cbStore != nil {
		fmt.Printf("\nAll tasks for %s are closed. Advancing to done phase.\n", designID)
		if run, err := cbStore.GetRun(ctx, designID); err == nil {
			repoRoot, _ := config.RepoForProject(projectName)
			pCfg, _ := config.LoadConfig(repoRoot)
			advanceDesignToCompleted(ctx, cbStore, conn, pCfg, designID, run.CurrentPhase)
			reconciled = true
		} else {
			fmt.Printf("  Warning: no pipeline run for %s: %v\n", designID, err)
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
	cleanupTaskWorktree(ctx, taskID, wtPath)

	fmt.Printf("Merging %s...\n", prURL)
	mergeOut, err := reviewCommandCombinedOutput(ctx, "gh", "pr", "merge", prURL,
		"--squash", "--delete-branch")
	if err != nil {
		return fmt.Errorf("merge failed: %s\n%s", err, string(mergeOut))
	}
	fmt.Println("  Merged.")

	// Rebase sibling branches onto updated main to prevent merge conflicts
	// on subsequent wave PRs (cb-7dd0d4).
	rebaseSiblingBranches(ctx, taskID)

	// Run kb-sync if the project has it enabled
	maybeRunKBSync(ctx, taskID)

	// Close task
	closeTaskAndAdvance(ctx, taskID)

	return nil
}

// maybeRunKBSync checks if the project has kb_sync enabled and runs it
// after a successful PR merge. Non-blocking — failures are logged but
// don't prevent task closure.
// maybeRunKBSync checks if the project has kb_sync enabled and runs it
// after a successful PR merge. Non-blocking — failures are logged but
// don't prevent task closure.
func maybeRunKBSync(ctx context.Context, taskID string) {
	cfg := reviewConfigLoader()
	if cfg == nil || !cfg.KBSync.Enabled {
		return
	}
	fmt.Printf("  Running kb-sync for %s...\n", taskID)
	args := []string{"kb-sync", taskID}
	if cfg.KBSync.RootArticle != "" {
		args = append(args, "--root", cfg.KBSync.RootArticle)
	}
	out, err := reviewCommandCombinedOutput(ctx, "cobuild", args...)
	if err != nil {
		fmt.Printf("  kb-sync warning: %v\n%s\n", err, string(out))
		return
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	start := len(lines) - 3
	if start < 0 {
		start = 0
	}
	for _, l := range lines[start:] {
		fmt.Printf("  kb-sync: %s\n", l)
	}
}

// rebaseSiblingBranches rebases open sibling branches onto main after a PR
// merge to prevent merge conflicts on subsequent waves (cb-7dd0d4).
// Best-effort: failures are logged but don't block task closure.
func rebaseSiblingBranches(ctx context.Context, mergedTaskID string) {
	if conn == nil {
		return
	}

	// Find parent design
	edges, err := conn.GetEdges(ctx, mergedTaskID, "outgoing", []string{"child-of"})
	if err != nil || len(edges) == 0 {
		return
	}
	designID := edges[0].ItemID

	// Find sibling tasks
	siblings, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return
	}

	// Resolve the repo for this design
	repoRoot := ""
	if run, err := cbStore.GetRun(ctx, designID); err == nil {
		repoRoot, _ = config.RepoForProject(run.Project)
	}
	if repoRoot == "" {
		return
	}

	// Fetch latest main
	exec.CommandContext(ctx, "git", "-C", repoRoot, "fetch", "origin", "main").Run()

	rebased := 0
	for _, s := range siblings {
		if s.ItemID == mergedTaskID || s.Status == "closed" {
			continue
		}
		if s.Type != "" && s.Type != "task" {
			continue
		}

		// Check if this sibling has a worktree with a branch
		wtPath := ""
		if conn != nil {
			wtPath, _ = conn.GetMetadata(ctx, s.ItemID, "worktree_path")
		}
		if wtPath == "" {
			continue
		}

		// Get the branch name
		branchOut, err := exec.CommandContext(ctx, "git", "-C", wtPath, "branch", "--show-current").Output()
		if err != nil {
			continue
		}
		branch := strings.TrimSpace(string(branchOut))
		if branch == "" || branch == "main" {
			continue
		}

		// Rebase onto origin/main
		rebaseOut, err := exec.CommandContext(ctx, "git", "-C", wtPath, "rebase", "origin/main").CombinedOutput()
		if err != nil {
			// Abort failed rebase and skip — agent will need to handle conflicts
			exec.CommandContext(ctx, "git", "-C", wtPath, "rebase", "--abort").Run()
			fmt.Printf("  rebase %s: conflict (skipped)\n", s.ItemID)
			continue
		}
		_ = rebaseOut

		// Force-push the rebased branch
		pushOut, err := exec.CommandContext(ctx, "git", "-C", wtPath, "push", "--force-with-lease").CombinedOutput()
		if err != nil {
			fmt.Printf("  rebase %s: push failed: %s\n", s.ItemID, strings.TrimSpace(string(pushOut)))
			continue
		}

		rebased++
		fmt.Printf("  rebased %s (%s)\n", s.ItemID, branch)
	}

	if rebased > 0 {
		fmt.Printf("  Rebased %d sibling branch(es) onto main.\n", rebased)
	}
}

func doRequestChanges(ctx context.Context, taskID string, findings []reviewFinding, ci ciCheckResult, reviewSource string) error {
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
	fmt.Fprintf(&fb, "### %s Findings\n", reviewSectionTitle(reviewSource))
	for _, f := range findings {
		if f.Priority == "high" || f.Priority == "critical" || reviewSource == "claude" || reviewSource == "openai" {
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
	syncPipelineTaskStatus(ctx, taskID, "in_progress")

	// Re-dispatch
	fmt.Printf("Re-dispatching %s for fixes...\n", taskID)
	out, err := reviewCommandCombinedOutput(ctx, "cobuild", "dispatch", taskID)
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
	out, err := reviewCommandOutput(ctx, "gh", "pr", "view", prURL,
		"--json", "createdAt", "--jq", ".createdAt")
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

type providerReviewResult struct {
	findings []reviewFinding
	source   string
	warning  string
	waiting  bool
}

func getReviewSession(ctx context.Context, taskID string) *store.SessionRecord {
	if cbStore == nil {
		return nil
	}
	rec, err := cbStore.GetSession(ctx, taskID)
	if err != nil {
		return nil
	}
	return rec
}

func sessionRuntimeModel(session *store.SessionRecord) (string, string) {
	if session == nil {
		return "", ""
	}
	model := ""
	if session.Model != nil {
		model = *session.Model
	}
	return session.Runtime, model
}

func runExternalReview(ctx context.Context, repo string, prNumber int, taskID, prURL string, reviewTimeout int) (providerReviewResult, error) {
	reviews, err := getGeminiReviews(ctx, repo, prNumber)
	if err != nil {
		return providerReviewResult{}, fmt.Errorf("check reviews: %w", err)
	}
	if len(reviews) == 0 {
		prAge := getPRAge(ctx, prURL)
		timeoutDuration := time.Duration(reviewTimeout) * time.Minute
		if prAge < timeoutDuration {
			remaining := timeoutDuration - prAge
			fmt.Printf("No Gemini review yet for %s (PR #%d, %s old, timeout %dm). Waiting %s.\n",
				taskID, prNumber, formatDuration(prAge), reviewTimeout, formatDuration(remaining))
			return providerReviewResult{source: "gemini", waiting: true}, nil
		}
		fmt.Printf("No Gemini review after %s for %s (PR #%d). Falling back to CI-based review.\n",
			formatDuration(prAge), taskID, prNumber)
		return providerReviewResult{
			source:  "ci-fallback",
			warning: "external review unavailable; using CI-only fallback",
		}, nil
	}
	return providerReviewResult{
		findings: getGeminiFindings(ctx, repo, prNumber),
		source:   "gemini",
	}, nil
}

func buildReviewInput(ctx context.Context, taskID string, task *connector.WorkItem, repo string, prNumber int) (llmreview.ReviewInput, error) {
	diffOut, err := reviewCommandOutput(ctx, "gh", "pr", "diff", strconv.Itoa(prNumber), "--repo", repo)
	if err != nil {
		return llmreview.ReviewInput{}, fmt.Errorf("gh pr diff %d: %w", prNumber, err)
	}

	input := llmreview.ReviewInput{
		TaskID:             taskID,
		TaskTitle:          task.Title,
		TaskSpec:           strings.TrimSpace(task.Content),
		AcceptanceCriteria: extractAcceptanceCriteria(task.Content),
		Diff:               strings.TrimSpace(string(diffOut)),
		PRDiff:             strings.TrimSpace(string(diffOut)),
	}

	designID, err := parentDesignID(ctx, taskID)
	if err != nil {
		return llmreview.ReviewInput{}, err
	}
	if designID == "" || conn == nil {
		return input, nil
	}
	parent, err := conn.Get(ctx, designID)
	if err != nil {
		return llmreview.ReviewInput{}, fmt.Errorf("get parent design %s: %w", designID, err)
	}
	input.ParentDesignID = parent.ID
	input.ParentDesignTitle = parent.Title
	input.ParentDesignContext = strings.TrimSpace(parent.Content)
	return input, nil
}

func extractAcceptanceCriteria(content string) []string {
	lines := strings.Split(content, "\n")
	var criteria []string
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			inSection = strings.EqualFold(trimmed, "## Acceptance Criteria")
			continue
		}
		if !inSection {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "- [ ] "):
			criteria = append(criteria, strings.TrimSpace(strings.TrimPrefix(trimmed, "- [ ] ")))
		case strings.HasPrefix(trimmed, "- "):
			criteria = append(criteria, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
		}
	}
	return criteria
}

func reviewResultToFindings(result *llmreview.ReviewResult) []reviewFinding {
	if result == nil {
		return nil
	}
	findings := make([]reviewFinding, 0, len(result.Findings))
	for _, f := range result.Findings {
		findings = append(findings, reviewFinding{
			Path:     f.File,
			Line:     f.Line,
			Priority: mapSeverityToPriority(f.Severity),
			Body:     f.Body,
		})
	}
	return findings
}

func mapSeverityToPriority(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "high":
		return "critical"
	case "suggestion", "medium":
		return "medium"
	default:
		return "low"
	}
}

func determineReviewVerdict(reviewResult *llmreview.ReviewResult, findings []reviewFinding, hasNewCIFailures bool) string {
	if hasNewCIFailures {
		return "request-changes"
	}
	if reviewResult != nil {
		if strings.EqualFold(reviewResult.Verdict, "request-changes") {
			return "request-changes"
		}
		return "approve"
	}
	for _, f := range findings {
		if f.Priority == "high" || f.Priority == "critical" {
			return "request-changes"
		}
	}
	return "approve"
}

func buildReviewSummary(reviewSource string, reviewResult *llmreview.ReviewResult, findings []reviewFinding, ci ciCheckResult, warning string) string {
	var summary string
	switch reviewSource {
	case "claude", "openai":
		llmSummary := ""
		if reviewResult != nil {
			llmSummary = strings.TrimSpace(reviewResult.Summary)
		}
		if llmSummary == "" {
			llmSummary = fmt.Sprintf("%d finding(s)", len(findings))
		}
		summary = fmt.Sprintf("%s review: %s. CI: %s", strings.Title(reviewSource), llmSummary, ci.summary)
	case "gemini":
		critical, medium, low := countFindings(findings)
		summary = fmt.Sprintf("Gemini review: %d high, %d medium, %d low finding(s). CI: %s", critical, medium, low, ci.summary)
	default:
		summary = fmt.Sprintf("CI-based review: CI: %s", ci.summary)
	}
	if warning != "" {
		summary += fmt.Sprintf(" (%s)", warning)
	}
	return summary
}

func countFindings(findings []reviewFinding) (critical, medium, low int) {
	for _, f := range findings {
		switch f.Priority {
		case "high", "critical":
			critical++
		case "medium":
			medium++
		default:
			low++
		}
	}
	return critical, medium, low
}

func formatPRComment(result *llmreview.ReviewResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Automated Review\n\n")
	if result.Summary != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(result.Summary))
	}
	fmt.Fprintf(&b, "**Verdict:** %s\n\n", result.Verdict)
	if len(result.Findings) == 0 {
		b.WriteString("No findings.\n")
		return b.String()
	}
	b.WriteString("**Findings**\n")
	for _, f := range result.Findings {
		ref := f.File
		if f.Line > 0 {
			ref = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		fmt.Fprintf(&b, "- [%s] `%s` %s\n", f.Severity, ref, strings.TrimSpace(f.Body))
	}
	return b.String()
}

func postReviewComment(ctx context.Context, prURL string, result *llmreview.ReviewResult) error {
	body := formatPRComment(result)
	if strings.TrimSpace(body) == "" {
		return nil
	}
	out, err := reviewCommandCombinedOutput(ctx, "gh", "pr", "comment", prURL, "--body", body)
	if err != nil {
		return fmt.Errorf("gh pr comment failed: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func reviewSectionTitle(source string) string {
	switch source {
	case "claude", "openai":
		return "Built-in Review"
	case "gemini":
		return "Gemini"
	default:
		return "Review"
	}
}

func init() {
	processReviewCmd.Flags().Bool("dry-run", false, "Show verdict without merging or re-dispatching")
	processReviewCmd.Flags().Int("review-timeout", 0, "Minutes to wait for external review before falling back (0 = skip external review entirely)")
	rootCmd.AddCommand(processReviewCmd)
}
