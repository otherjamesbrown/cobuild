package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	llmreview "github.com/otherjamesbrown/cobuild/internal/review"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/worktree"
	"github.com/spf13/cobra"
)

const geminiBotLogin = "gemini-code-assist[bot]"
const directReviewPassBody = "Direct-mode task, no PR review required"

var (
	reviewWarningWriter io.Writer = os.Stderr
	reviewConfigLoader            = func() *config.Config {
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
	reviewCleanupTaskResources = cleanupTaskResources
)

func reviewWarnf(format string, args ...any) {
	fmt.Fprintf(reviewWarningWriter, format, args...)
}

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

		// cb-d95bcd: skip already-blocked pipelines. Without this,
		// the poller re-runs process-review every cycle, recording
		// another gate round each time.
		if cbStore != nil {
			if run, runErr := cbStore.GetRun(ctx, taskID); runErr == nil && run.Status == domain.StatusBlocked {
				fmt.Printf("Pipeline %s is blocked — skipping review. Run `cobuild reset %s` to unblock.\n", taskID, taskID)
				return nil
			}
		}

		// Get PR URL — try metadata first, then fall back to looking up
		// the PR by branch name. cb-bb760c: cobuild complete writes pr_url
		// metadata, but when the agent dies before complete runs (or
		// complete's PR lookup silently fails), the metadata is missing.
		// The branch-name fallback keeps process-review working; if found,
		// we backfill the metadata so the fallback doesn't fire again.
		prURL, _ := conn.GetMetadata(ctx, taskID, domain.MetaPRURL)
		if prURL == "" && task.Metadata != nil {
			if pr, ok := task.Metadata[domain.MetaPRURL]; ok {
				prURL = fmt.Sprintf("%v", pr)
			}
		}
		if prURL == "" {
			wtPath, _ := conn.GetMetadata(ctx, taskID, domain.MetaWorktreePath)
			prURL = lookupOpenPRForWorktree(ctx, wtPath)
			if prURL == "" {
				// Last resort: branch name = task ID (CoBuild convention)
				prURL = lookupPRByBranch(ctx, taskID)
			}
			if prURL != "" {
				fmt.Printf("Recovered PR URL from branch lookup: %s\n", prURL)
				_ = conn.SetMetadata(ctx, taskID, domain.MetaPRURL, prURL)
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
		stateOut, err := execCommandOutput(ctx, "gh", "pr", "view", prURL,
			"--json", "state", "--jq", ".state")
		if err == nil {
			// Validate against the known GitHub PR state set rather than
			// silently treating unexpected values as "not merged" — the
			// prior implementation would skip the MERGED/CLOSED fast-paths
			// on any typo, version skew, or future state (cb-6d598a row 18).
			state := strings.TrimSpace(string(stateOut))
			switch state {
			case "MERGED":
				fmt.Printf("PR already merged for %s. Reconciling local state.\n", taskID)
				if _, err := reconcileReviewedTask(ctx, taskID); err != nil {
					return err
				}
				printNextStep(taskID, domain.OutcomeMerged, domain.ActionProcessReview)
				return nil
			case "CLOSED":
				fmt.Printf("PR is closed (not merged) for %s, skipping.\n", taskID)
				return nil
			case "OPEN":
				// fall through to review flow below
			default:
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Warning: unexpected PR state %q from `gh pr view --json state`; "+
						"proceeding as if OPEN. Check gh version / GitHub API if this repeats.\n",
					state)
			}
		}

		reviewTimeout, _ := cmd.Flags().GetInt("review-timeout")
		pCfg := reviewConfigLoader()
		session := getReviewSession(ctx, taskID)

		// review.mode=dispatched (default): spawn a fresh agent to read the
		// PR diff, design, and task scope and write a verdict. Same pattern
		// as every other gate phase. Avoids the Anthropic API path entirely
		// (cb-482378). The agent's verdict gets recorded by its runner script.
		//
		// cb-9792e7: this branch always returns — it MUST NOT fall through
		// to the builtin/external path below. On repos without
		// ANTHROPIC_API_KEY (the whole point of dispatched mode), the
		// builtin claude reviewer 401s and records a spurious review/fail
		// gate. Any ambiguous state from dispatchReviewAgent resolves to
		// "waiting, retry next poll" — never to "try the builtin path".
		if pCfg.Review.EffectiveMode() == "dispatched" {
			// cb-4a6799: --dry-run must be side-effect free. The dispatched
			// branch previously ignored the flag entirely — dispatching agents,
			// creating sessions, and mutating shard status.
			if dryRun {
				fmt.Printf("[dry-run] Would dispatch review agent for %s (mode=dispatched)\n", taskID)
				fmt.Printf("[dry-run] PR: %s\n", prURL)
				fmt.Printf("[dry-run] No agents spawned, no status changes.\n")
				return nil
			}

			// Fallback verdict reader for cb-3b091b. If a prior dispatched
			// agent already wrote .cobuild/gate-verdict.json but the runner
			// script's review arm didn't run (or ran pre-fix), consume the
			// verdict here: record the gate, merge or redispatch, and move
			// on. Safe to run alongside the runner script: if `cobuild review`
			// already recorded the gate, RecordGateVerdict below is skipped.
			consumed, cErr := consumeDispatchedReviewVerdict(ctx, taskID, prURL, pCfg)
			if cErr != nil {
				return cErr
			}
			if consumed {
				return nil
			}

			if _, err := dispatchReviewAgent(ctx, cmd, taskID); err != nil {
				return err
			}
			// Always wait for the dispatched agent's verdict. The prior
			// code path returned waiting only when the dispatcher said so
			// and otherwise fell through to builtin; that fall-through is
			// exactly cb-9792e7.
			printNextStep(taskID, domain.OutcomeWaiting, domain.ActionProcessReview)
			return nil
		}

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
				printNextStep(taskID, domain.OutcomeWaiting, domain.ActionProcessReview)
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
			printNextStep(taskID, domain.OutcomeWaiting, domain.ActionProcessReview)
			return nil
		}

		// Step 4: Decide verdict
		verdict := determineReviewVerdict(reviewResult, findings, ciResult)

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

		// cb-ff01fe: check CI BEFORE recording the gate verdict. RecordGateVerdict
		// advances phase on pass — if we record pass then block the merge, the
		// pipeline ends up in phase=done with the PR still open (observed on
		// pf-021ed9). By checking CI first, we skip the gate record and return
		// waiting so the pipeline stays in review phase and the poller retries.
		if verdict == "approve" {
			allowRedCI, _ := cmd.Flags().GetBool("allow-red-ci")
			if !allowRedCI {
				if reason := ciBlocksMerge(ciResult, pCfg); reason != "" {
					fmt.Printf("Merge blocked: %s. Use --allow-red-ci to override.\n", reason)
					fmt.Println("Review verdict was PASS but merge is held until CI is green. Pipeline stays in review phase.")
					printNextStep(taskID, domain.OutcomeWaiting, domain.ActionProcessReview)
					return nil
				}
			}
		}

		if cbStore != nil {
			body := buildVerdictBody(verdict, reviewSource, reviewResult, findings, ciResult, reviewWarning)
			gateResult, gateErr := RecordGateVerdict(ctx, conn, cbStore, taskID, domain.GateReview, gateVerdict, body, 0, pCfg)
			if gateErr != nil {
				fmt.Printf("Warning: failed to record gate verdict: %v\n", gateErr)
			}

			// cb-f55aa0 + cb-e20e84/cb-4c9241: block the loop when
			// findings repeat or the round cap is exceeded.
			if gateResult != nil && gateVerdict == "fail" {
				if reason := shouldEscalateReview(ctx, cbStore, gateResult); reason != "" {
					fmt.Printf("Review loop blocked: %s. Escalating to orchestrator.\n", reason)
					markPipelineBlocked(ctx, cbStore, taskID, reason)
					notifyReviewBlocked(ctx, taskID, gateResult, reason)
					printNextStep(taskID, domain.OutcomeBlocked, domain.ActionProcessReview)
					return nil
				}
			}
		}

		if verdict == "approve" {
			if err := doMerge(ctx, taskID, prURL); err != nil {
				return err
			}
			printNextStep(taskID, domain.OutcomeMerged, domain.ActionProcessReview)
			return nil
		}
		if err := doRequestChanges(ctx, taskID, findings, ciResult, reviewSource); err != nil {
			return err
		}
		printNextStep(taskID, domain.OutcomeRedispatched, domain.ActionProcessReview)
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
	allFailures []string // cb-7e1fc6: all failing check names, including pre-existing
}

func getGeminiReviews(ctx context.Context, repo string, prNumber int) ([]ghReview, error) {
	out, err := execCommandOutput(ctx, "gh", "api",
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
	out, err := execCommandOutput(ctx, "gh", "api",
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
	headOut, err := execCommandOutput(ctx, "gh", "pr", "view",
		strconv.Itoa(prNumber), "--repo", repo,
		"--json", "headRefOid", "--jq", ".headRefOid")
	if err != nil {
		return ciCheckResult{summary: "no checks (could not get commit)"}
	}
	commitSHA := strings.TrimSpace(string(headOut))

	out, err := execCommandOutput(ctx, "gh", "api",
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
	mainOut, err := execCommandOutput(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/actions/runs?branch=main&status=completed&per_page=1", repo),
		"--jq", ".workflow_runs[0].id")

	newFailures := prFailures // assume all are new if we can't check main
	if err == nil {
		runID := strings.TrimSpace(string(mainOut))
		if runID != "" {
			jobsOut, err := execCommandOutput(ctx, "gh", "api",
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
			summary:     fmt.Sprintf("%d checks passed, %d pre-existing failures", len(checks)-len(prFailures), preExisting),
			allFailures: prFailures,
		}
	}
	return ciCheckResult{
		summary:     fmt.Sprintf("%d new failure(s): %s", len(newFailures), strings.Join(newFailures, ", ")),
		newFailures: newFailures,
		allFailures: prFailures,
	}
}

// ciBlocksMerge checks whether CI failures should prevent merging.
// Returns a non-empty reason string when the merge should be blocked.
// When wait_for_ci is true (default), ANY failing check blocks —
// including pre-existing failures that also fail on main (cb-7e1fc6).
func ciBlocksMerge(ci ciCheckResult, pCfg *config.Config) string {
	waitForCI := pCfg != nil && pCfg.Review.WaitForCI != nil && *pCfg.Review.WaitForCI

	if ci.summary == "pending" && waitForCI {
		return "CI checks still pending"
	}

	if waitForCI && len(ci.allFailures) > 0 {
		return fmt.Sprintf("CI failing: %s", strings.Join(ci.allFailures, ", "))
	}

	// Even without wait_for_ci, new failures always block.
	if len(ci.newFailures) > 0 {
		return fmt.Sprintf("new CI failures: %s", strings.Join(ci.newFailures, ", "))
	}
	return ""
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

	wtPath, _ := conn.GetMetadata(ctx, taskID, domain.MetaWorktreePath)
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

// mergeMaxRetries caps how many times process-review may attempt `gh pr
// merge` for the same task across polling cycles before escalating. Beyond
// this, the orchestrator would spin forever on an unmergeable PR (cb-d5e1dd
// #10). Override via the MERGE_MAX_RETRIES env var for power-user debugging.
const mergeMaxRetries = 3

// recordMergeFailure increments the persistent merge_retry_count metadata
// and, once it crosses mergeMaxRetries, appends an operator-actionable note
// to the task and applies a `merge-blocked` label. The caller should still
// return the error — this function just persists the audit trail.
func recordMergeFailure(ctx context.Context, taskID, prURL, detail string) {
	if conn == nil {
		return
	}
	existing, _ := conn.GetMetadata(ctx, taskID, domain.MetaMergeRetryCount)
	count := 0
	if existing != "" {
		_, _ = fmt.Sscanf(existing, "%d", &count)
	}
	count++
	if err := conn.SetMetadata(ctx, taskID, domain.MetaMergeRetryCount, fmt.Sprintf("%d", count)); err != nil {
		reviewWarnf("Warning: failed to record merge retry count for %s: %v\n", taskID, err)
	}

	if count < mergeMaxRetries {
		return
	}
	// At or past the cap: append a persistent note so the user can see it
	// via `cobuild show <task>` and apply a label so list views can filter.
	note := fmt.Sprintf(
		"\n\n## Merge blocked after %d attempts\n\nPR: %s\n\nLast error:\n```\n%s\n```\n\n"+
			"Resolve the conflict manually, then either:\n"+
			"  - clear `merge_retry_count` metadata and re-run `cobuild process-review %s`, or\n"+
			"  - close the PR and let the orchestrator re-dispatch the task.",
		count, prURL, strings.TrimSpace(detail), taskID,
	)
	if err := conn.AppendContent(ctx, taskID, note); err != nil {
		reviewWarnf("Warning: failed to append merge-blocked note to %s: %v\n", taskID, err)
	}
	if err := conn.AddLabel(ctx, taskID, "merge-blocked"); err != nil {
		reviewWarnf("Warning: failed to add merge-blocked label to %s: %v\n", taskID, err)
	}
}

// mergeIsBlocked reports whether the merge retry cap has already been hit
// for this task. Callers use this to refuse further merge attempts and
// surface the blocked state to the orchestrator.
func mergeIsBlocked(ctx context.Context, taskID string) bool {
	if conn == nil {
		return false
	}
	v, _ := conn.GetMetadata(ctx, taskID, domain.MetaMergeRetryCount)
	if v == "" {
		return false
	}
	var count int
	_, _ = fmt.Sscanf(v, "%d", &count)
	return count >= mergeMaxRetries
}

func doMerge(ctx context.Context, taskID, prURL string) error {
	// Refuse immediately if the retry cap is hit — prior attempts already
	// appended diagnostics and applied the merge-blocked label. Spinning
	// further wastes time and GitHub API calls (cb-d5e1dd #10).
	if mergeIsBlocked(ctx, taskID) {
		return fmt.Errorf(
			"merge for %s blocked: retry cap %d reached; see task content for last error. "+
				"Resolve manually, then clear merge_retry_count metadata or close the PR.",
			taskID, mergeMaxRetries,
		)
	}
	// Auto-rebase before merge: if the PR is mergeable_state=behind/dirty
	// (stale base, no real conflicts), rebase onto origin/main and force-push.
	// Three outcomes (cb-c6091a):
	//   1. mergeable already → proceed
	//   2. stale base, clean rebase → rebase + force-push, retry merge
	//   3. real content conflict → return error so caller stops; needs human
	if err := tryAutoRebaseBeforeMerge(ctx, taskID, prURL); err != nil {
		recordMergeFailure(ctx, taskID, prURL, err.Error())
		return err
	}

	fmt.Printf("Merging %s...\n", prURL)
	mergeOut, mergedWithWarning, err := ghMergePR(ctx, prURL)
	if err != nil {
		recordMergeFailure(ctx, taskID, prURL, string(mergeOut))
		return fmt.Errorf("merge failed: %w\n%s", err, string(mergeOut))
	}
	// Clear retry counter on success so a re-merged/re-opened PR starts fresh.
	if err := conn.SetMetadata(ctx, taskID, domain.MetaMergeRetryCount, ""); err != nil {
		// Best-effort cleanup; a stale retry counter is recoverable by clearing metadata manually.
		reviewWarnf("Warning: failed to clear merge retry count for %s: %v\n", taskID, err)
	}
	fmt.Println("  Merged.")
	if mergedWithWarning {
		reviewWarnf("Warning: GitHub merged %s but gh reported a local cleanup error:\n%s\n", prURL, strings.TrimSpace(string(mergeOut)))
	}

	onMergeSuccess(
		ctx,
		taskID,
		reviewCleanupTaskResources,
		func(taskID string, err error) {
			reviewWarnf("Warning: merge succeeded, but local cleanup failed for %s: %v\n", taskID, err)
		},
		reviewWarnf,
	)

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
	out, err := execCommandCombinedOutput(ctx, "cobuild", args...)
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
	if err := conn.UpdateStatus(ctx, taskID, domain.StatusInProgress); err != nil {
		fmt.Printf("Warning: failed to set in_progress: %v\n", err)
	}
	syncPipelineTaskStatus(ctx, taskID, domain.StatusInProgress)

	// Re-dispatch
	fmt.Printf("Re-dispatching %s for fixes...\n", taskID)
	out, err := execCommandCombinedOutput(ctx, "cobuild", "dispatch", taskID)
	if err != nil {
		return fmt.Errorf("re-dispatch failed: %w\n%s", err, string(out))
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
	out, err := execCommandOutput(ctx, "gh", "pr", "view", prURL,
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
	diffOut, err := execCommandOutput(ctx, "gh", "pr", "diff", strconv.Itoa(prNumber), "--repo", repo)
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

func determineReviewVerdict(reviewResult *llmreview.ReviewResult, findings []reviewFinding, ci ciCheckResult) string {
	hasNewCIFailures := len(ci.newFailures) > 0
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
	// No LLM review result, no findings, no CI failures. Before approving,
	// require that we had SOME meaningful signal. If both the LLM review
	// failed AND there are no CI checks, a plain "approve" is silent
	// rubber-stamping (cb-d5e1dd #2). Escalate to request-changes so the
	// operator is forced to wire up at least one review path.
	if ciProvidedNoSignal(ci) {
		return "request-changes"
	}
	return "approve"
}

// ciProvidedNoSignal reports whether the CI check yielded no usable outcome:
// either the repo has zero configured checks, or the GitHub API failed before
// any check status could be parsed. "pending" and "N passed" both count as
// signal. Used by determineReviewVerdict to fail-loud when the overall
// review has no basis for approval.
func ciProvidedNoSignal(ci ciCheckResult) bool {
	s := strings.ToLower(strings.TrimSpace(ci.summary))
	switch {
	case strings.HasPrefix(s, "no ci checks configured"),
		strings.HasPrefix(s, "no checks (could not get commit)"),
		strings.HasPrefix(s, "no checks (api error)"),
		strings.HasPrefix(s, "no checks (parse error)"):
		return true
	}
	return false
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
	out, err := execCommandCombinedOutput(ctx, "gh", "pr", "comment", prURL, "--body", body)
	if err != nil {
		return fmt.Errorf("gh pr comment failed: %w\n%s", err, strings.TrimSpace(string(out)))
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

// tryAutoRebaseBeforeMerge checks the PR's mergeable state and, if it's
// behind/dirty due to stale base, rebases the branch onto origin/main and
// force-pushes so the subsequent gh pr merge succeeds. Returns an error
// (terminal — caller should stop) when there's a real content conflict that
// requires human resolution. Returns nil for already-mergeable PRs (no-op)
// and successfully-rebased PRs (ready to merge).
func tryAutoRebaseBeforeMerge(ctx context.Context, taskID, prURL string) error {
	repo, prNumber, err := parsePRURL(prURL)
	if err != nil {
		return nil // can't parse — let merge fail naturally
	}

	// Check current mergeable state
	out, err := execCommandOutput(ctx, "gh", "api",
		fmt.Sprintf("repos/%s/pulls/%d", repo, prNumber),
		"--jq", ".mergeable_state + \"|\" + .head.ref")
	if err != nil {
		return nil // gh api failed — proceed and let merge attempt fail with clearer error
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(parts) != 2 {
		return nil
	}
	state, branch := parts[0], parts[1]

	// "clean" / "unstable" → mergeable, no rebase needed
	// "behind" → stale base, rebase will help
	// "dirty" → either stale base or real conflict; try rebase to find out
	// "blocked" / "draft" / "" → not our problem
	if state != "behind" && state != "dirty" {
		return nil
	}

	repoRoot, err := config.RepoForProject(projectName)
	if err != nil || repoRoot == "" {
		return nil // can't find repo; let merge fail naturally
	}

	fmt.Printf("PR #%d is %s — attempting rebase onto origin/main...\n", prNumber, state)

	// Fetch latest main
	if out, err := execCommandCombinedOutput(ctx, "git", "-C", repoRoot, "fetch", "origin", "main"); err != nil {
		return fmt.Errorf("rebase failed at fetch: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	// Use a temp worktree so we don't mess with the main repo's HEAD
	tmpWT, err := os.MkdirTemp("", "cobuild-rebase-"+branch+"-")
	if err != nil {
		return fmt.Errorf("rebase failed creating temp worktree: %w", err)
	}
	defer os.RemoveAll(tmpWT)
	defer execCommandCombinedOutput(ctx, "git", "-C", repoRoot, "worktree", "remove", "--force", tmpWT)

	if out, err := execCommandCombinedOutput(ctx, "git", "-C", repoRoot, "worktree", "add", tmpWT, branch); err != nil {
		return fmt.Errorf("rebase failed creating worktree: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	// Attempt rebase
	rebaseOut, rebaseErr := execCommandCombinedOutput(ctx, "git", "-C", tmpWT, "rebase", "origin/main")
	if rebaseErr != nil {
		// Conflict — abort and return a terminal error
		execCommandCombinedOutput(ctx, "git", "-C", tmpWT, "rebase", "--abort")
		return fmt.Errorf(
			"PR #%d has merge conflicts that need human resolution:\n%s\nResolve manually: gh pr checkout %d && git rebase origin/main",
			prNumber, strings.TrimSpace(string(rebaseOut)), prNumber,
		)
	}

	// Force-push the rebased branch
	if out, err := execCommandCombinedOutput(ctx, "git", "-C", tmpWT, "push", "--force-with-lease"); err != nil {
		return fmt.Errorf("rebase succeeded but push failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("  Rebased PR #%d onto origin/main.\n", prNumber)
	return nil
}

// consumeDispatchedReviewVerdict consumes a dispatched review agent's verdict
// from <worktree>/.cobuild/gate-verdict.json and completes the review flow
// in-line. Returns consumed=true when a verdict was handled — the caller
// should return immediately. Returns (false, nil) when no verdict file is
// present (normal first-call path; caller should dispatch the agent).
//
// Defense-in-depth for cb-3b091b: the runner script's review arm already
// calls `cobuild review` to record the gate and advance the phase. This
// function kicks in when the runner script didn't run (pre-fix worktree)
// or when the runner recorded the gate but never got a chance to merge.
// RecordGateVerdict is skipped when the pipeline phase has already moved
// past "review" (i.e. the runner script got there first), so we don't
// double-record. doMerge / doRequestChanges are idempotent via their own
// guards (PR state, retry caps).
func consumeDispatchedReviewVerdict(ctx context.Context, taskID, prURL string, pCfg *config.Config) (bool, error) {
	if conn == nil {
		return false, nil
	}
	wtPath, _ := conn.GetMetadata(ctx, taskID, domain.MetaWorktreePath)
	if wtPath == "" {
		return false, nil
	}
	verdictFile := filepath.Join(wtPath, ".cobuild", "gate-verdict.json")
	data, err := os.ReadFile(verdictFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read gate-verdict.json: %w", err)
	}

	var v struct {
		Gate    string `json:"gate"`
		ShardID string `json:"shard_id"`
		Verdict string `json:"verdict"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		// Park the corrupt file and log; don't loop on it forever.
		_ = os.Rename(verdictFile, verdictFile+".corrupted")
		return false, fmt.Errorf("parse gate-verdict.json: %w", err)
	}
	if v.Gate != domain.GateReview || v.ShardID != taskID {
		// Not our verdict. Leave the file alone — a different caller owns it.
		return false, nil
	}
	verdict, err := normalizeGateVerdict(v.Verdict)
	if err != nil {
		return false, fmt.Errorf("invalid verdict %q in %s", v.Verdict, verdictFile)
	}

	// cb-ff01fe: for pass verdicts, check CI BEFORE consuming the verdict
	// file or recording the gate. RecordGateVerdict advances phase on pass —
	// if we advance then block the merge, the pipeline ends up in phase=done
	// with the PR still open (observed on pf-021ed9). By checking CI first
	// and leaving the verdict file in place, the poller retries on the next
	// cycle and merges once CI is green.
	if verdict == "pass" {
		repo, prNumber, parseErr := parsePRURL(prURL)
		if parseErr == nil {
			ci := checkCI(ctx, repo, prNumber)
			if reason := ciBlocksMerge(ci, pCfg); reason != "" {
				fmt.Printf("Merge blocked after PASS verdict: %s\n", reason)
				fmt.Println("Review verdict was PASS but merge is held until CI is green. Pipeline stays in review phase.")
				fmt.Println("Verdict file kept for next poll. Re-run process-review after CI passes, or use --allow-red-ci.")
				printNextStep(taskID, domain.OutcomeWaiting, domain.ActionProcessReview)
				return true, nil
			}
		}
	}

	// CI is green (or verdict is fail) — safe to consume the verdict file
	// and advance the pipeline.
	processedFile := verdictFile + ".processed"
	if err := os.Rename(verdictFile, processedFile); err != nil {
		return false, fmt.Errorf("rename gate-verdict.json: %w", err)
	}

	// Mark any still-running review sessions complete. Without this, the
	// next dispatchReviewAgent call would hit the "already running" guard
	// even though the agent has exited.
	if cbStore != nil {
		if sessions, lerr := cbStore.ListSessions(ctx, taskID); lerr == nil {
			for _, s := range sessions {
				if s.Status == "running" && s.Phase == domain.PhaseReview {
					if err := cbStore.EndSession(ctx, s.ID, store.SessionResult{
						ExitCode:       0,
						Status:         domain.StatusCompleted,
						CompletionNote: "dispatched review verdict consumed by process-review",
					}); err != nil {
						reviewWarnf("Warning: failed to close review session %s for %s: %v\n", s.ID, taskID, err)
					}
				}
			}
		}
	}

	// Record the gate unless the pipeline has already advanced past review
	// (runner script's `cobuild review` arm beat us to it).
	var gateResult *GateVerdictResult
	if cbStore != nil {
		run, runErr := cbStore.GetRun(ctx, taskID)
		if runErr == nil && run != nil && run.CurrentPhase == domain.PhaseReview {
			var rerr error
			gateResult, rerr = RecordGateVerdict(ctx, conn, cbStore, taskID, domain.GateReview, verdict, v.Body, 0, pCfg)
			if rerr != nil {
				return true, fmt.Errorf("record review gate: %w", rerr)
			}
		}
	}

	if verdict == "pass" {
		if err := doMerge(ctx, taskID, prURL); err != nil {
			return true, err
		}
		printNextStep(taskID, domain.OutcomeMerged, domain.ActionProcessReview)
		return true, nil
	}

	// cb-f55aa0 + cb-e20e84/cb-4c9241: block the loop when
	// findings repeat or the round cap is exceeded.
	if gateResult != nil && cbStore != nil {
		if reason := shouldEscalateReview(ctx, cbStore, gateResult); reason != "" {
			fmt.Printf("Review loop blocked: %s. Escalating to orchestrator.\n", reason)
			markPipelineBlocked(ctx, cbStore, taskID, reason)
			notifyReviewBlocked(ctx, taskID, gateResult, reason)
			printNextStep(taskID, domain.OutcomeBlocked, domain.ActionProcessReview)
			return true, nil
		}
	}

	// verdict=fail — append the agent's findings as synthetic feedback and
	// re-dispatch the implement agent.
	findings := []reviewFinding{{
		Priority: "high",
		Body:     strings.TrimSpace(v.Body),
	}}
	if err := doRequestChanges(ctx, taskID, findings, ciCheckResult{}, "dispatched"); err != nil {
		return true, err
	}
	printNextStep(taskID, domain.OutcomeRedispatched, domain.ActionProcessReview)
	return true, nil
}

// dispatchReviewAgent spawns a dispatched review agent for a needs-review task
// when review.mode=dispatched (cb-392091). The agent reads the PR diff + design
// + task scope and writes a verdict to .cobuild/gate-verdict.json which the
// runner script records via `cobuild review` after the agent exits.
//
// Returns (waiting=true, nil) on successful dispatch, on "agent already
// running" for this task, or on transient dispatch refusal — all three cases
// mean "stay at review, retry next poll". Previously a dispatch error
// returned (false, nil) which caused the caller to fall through to the
// builtin/external path; on repos without ANTHROPIC_API_KEY that path 401s
// and records a spurious review/fail gate against a healthy PR (cb-6f9ed6).
func dispatchReviewAgent(ctx context.Context, cmd *cobra.Command, taskID string) (bool, error) {
	// If a review-phase agent is already running for this task, leave it.
	// The dispatch guard would refuse anyway; check explicitly so we can
	// surface a clear waiting message without printing a scary error.
	if cbStore != nil {
		sessions, err := cbStore.ListSessions(ctx, taskID)
		if err == nil {
			for _, s := range sessions {
				if s.Status == "running" && s.Phase == domain.PhaseReview {
					fmt.Printf("Review agent already running for %s (session %s).\n", taskID, s.ID)
					return true, nil
				}
			}
		}
	}

	// Dispatch — uses the task's current pipeline_run.phase, which should be
	// "review" by now (cobuild complete advances implement→review on PR open).
	// The dispatch.go writePhasePrompt review-case path uses the dispatch-review
	// skill and tells the agent to write .cobuild/gate-verdict.json.
	subCmd, _, err := rootCmd.Find([]string{"dispatch"})
	if err != nil || subCmd == nil {
		return false, fmt.Errorf("dispatch command not found")
	}
	subCmd.SetArgs([]string{taskID})
	if err := subCmd.RunE(subCmd, []string{taskID}); err != nil {
		// Dispatch refused (orphan tmux window, stale session conflict,
		// worktree issue, etc.). Stay at review and retry next poll —
		// falling through to builtin claude on an API-key-less repo would
		// 401 and record a spurious review/fail gate (cb-6f9ed6).
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: dispatched review skipped for %s: %v — will retry\n", taskID, err)
		return true, nil
	}
	fmt.Printf("Dispatched review agent for %s. Will record verdict on agent exit.\n", taskID)
	return true, nil
}

func init() {
	processReviewCmd.Flags().Bool("dry-run", false, "Show verdict without merging or re-dispatching")
	processReviewCmd.Flags().Int("review-timeout", 0, "Minutes to wait for external review before falling back (0 = skip external review entirely)")
	processReviewCmd.Flags().Bool("allow-red-ci", false, "Merge even when CI checks are failing (cb-7e1fc6)")
	rootCmd.AddCommand(processReviewCmd)
}
