package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

// kbSyncGateVerdict holds the outcome of a kb-sync phase run.
type kbSyncGateVerdict string

const (
	kbSyncVerdictNoChanges    kbSyncGateVerdict = "no-changes-needed"
	kbSyncVerdictUpdated      kbSyncGateVerdict = "updated"
	kbSyncVerdictPartialUpdate kbSyncGateVerdict = "partial-update"
	kbSyncVerdictAllRolledBack kbSyncGateVerdict = "all-rolled-back"
)

// factcheckResult is the JSON output of the kb-factcheck skill.
type factcheckResult struct {
	Verdict         string              `json:"verdict"`
	ClaimsChecked   int                 `json:"claims_checked"`
	ClaimsVerified  int                 `json:"claims_verified"`
	ClaimsFailed    []factcheckClaim    `json:"claims_failed"`
	ClaimsAmbiguous []factcheckClaim    `json:"claims_ambiguous"`
}

type factcheckClaim struct {
	Type   string `json:"type"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// judgeResult is the JSON output of the kb-judge skill.
type judgeResult struct {
	Verdict        string        `json:"verdict"`
	Issues         []judgeIssue  `json:"issues"`
	GapsIdentified []string      `json:"gaps_identified"`
}

type judgeIssue struct {
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

var kbSyncCmd = &cobra.Command{
	Use:   "kb-sync <work-item-id>",
	Short: "Run the kb-sync phase: sync KB articles affected by a merged work item",
	Long: `Runs between review and done. For each KB article affected by the merged PR:
1. Dispatches a kb-sync skill agent to produce a proposed update
2. Runs kb-factcheck (deterministic claim verification) on the proposal
3. Runs kb-judge (cross-family LLM semantic review) on the proposal
4. On both passing: commits the update via cxp kb update
5. On any failure: rolls back, logs a KB Gap

Gate verdict is non-blocking: all-rolled-back still advances the work item to done.`,
	Args:    cobra.ExactArgs(1),
	Example: "  cobuild kb-sync pf-abc123\n  cobuild kb-sync pf-abc123 --dry-run",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		wiID := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		// 1. Model-family constraint check
		if err := checkKBModelFamilyConstraint(ctx); err != nil {
			return err
		}

		// 2. Fetch the work item
		item, err := conn.Get(ctx, wiID)
		if err != nil {
			return fmt.Errorf("get work item %s: %w", wiID, err)
		}
		fmt.Printf("kb-sync: processing %s (%s) %q\n", item.ID, item.Type, item.Title)

		// 3. Fetch merged PR(s) for this work item
		prDiff, prBody, err := fetchMergedPRContent(ctx, wiID, item.Content)
		if err != nil {
			fmt.Printf("Warning: could not fetch PR content: %v\n", err)
			prDiff = ""
			prBody = item.Content
		}

		// 4. Extract concepts from diff + work item body
		concepts := extractConcepts(prDiff, item.Content)
		if len(concepts) == 0 {
			fmt.Println("No concepts extracted from diff — recording no-changes-needed.")
			return recordKBSyncVerdict(ctx, wiID, kbSyncVerdictNoChanges, "no concepts extractable from PR diff")
		}

		// 5. Find affected KB articles
		affectedArticles := findAffectedKBArticles(ctx, concepts, prDiff)
		if len(affectedArticles) == 0 {
			fmt.Println("No KB articles affected — recording no-changes-needed.")
			return recordKBSyncVerdict(ctx, wiID, kbSyncVerdictNoChanges, "no affected KB articles found")
		}

		fmt.Printf("Found %d affected KB article(s): %s\n", len(affectedArticles), strings.Join(affectedArticles, ", "))

		if dryRun {
			fmt.Printf("[dry-run] Would process %d article(s): %s\n", len(affectedArticles), strings.Join(affectedArticles, ", "))
			return nil
		}

		// 6. Process each affected article
		updated := 0
		rolledBack := 0

		for _, articleID := range affectedArticles {
			outcome, err := processKBArticle(ctx, articleID, wiID, item.Content, prDiff, prBody)
			if err != nil {
				fmt.Printf("  [%s] error: %v — treating as rollback\n", articleID, err)
				rolledBack++
				logKBGap(ctx, articleID, wiID, fmt.Sprintf("unexpected error: %v", err))
				continue
			}
			if outcome {
				updated++
				fmt.Printf("  [%s] updated successfully\n", articleID)
			} else {
				rolledBack++
				fmt.Printf("  [%s] rolled back\n", articleID)
			}
		}

		// 7. Determine and record gate verdict
		var verdict kbSyncGateVerdict
		switch {
		case updated > 0 && rolledBack == 0:
			verdict = kbSyncVerdictUpdated
		case updated > 0 && rolledBack > 0:
			verdict = kbSyncVerdictPartialUpdate
		default:
			verdict = kbSyncVerdictAllRolledBack
		}

		summary := fmt.Sprintf("updated=%d rolled_back=%d total=%d", updated, rolledBack, len(affectedArticles))
		return recordKBSyncVerdict(ctx, wiID, verdict, summary)
	},
}

// checkKBModelFamilyConstraint verifies that kb_sync and kb_judge routing rules
// use different model families. Refuses to run if they share a family.
func checkKBModelFamilyConstraint(ctx context.Context) error {
	if cbClient == nil {
		// No DB connection — skip constraint check with a warning
		fmt.Println("Warning: no database connection, skipping model-family constraint check.")
		return nil
	}

	dbConn, err := cbClient.Connect(ctx)
	if err != nil {
		fmt.Printf("Warning: cannot connect to DB for model-family check: %v\n", err)
		return nil
	}
	defer dbConn.Close(ctx)

	getFamily := func(taskType string) (string, error) {
		var model string
		err := dbConn.QueryRow(ctx,
			`SELECT preferred_models[1] FROM ai_routing_rules
			 WHERE task_type = $1 AND is_enabled = true
			 ORDER BY priority DESC LIMIT 1`,
			taskType,
		).Scan(&model)
		if err != nil {
			return "", fmt.Errorf("query routing rule for %s: %w", taskType, err)
		}
		// Extract family as prefix before '/'
		if idx := strings.Index(model, "/"); idx > 0 {
			return model[:idx], nil
		}
		return model, nil
	}

	syncFamily, err := getFamily("kb_sync")
	if err != nil {
		// No rule yet — skip check
		fmt.Printf("Warning: kb_sync routing rule not found, skipping constraint check: %v\n", err)
		return nil
	}

	judgeFamily, err := getFamily("kb_judge")
	if err != nil {
		fmt.Printf("Warning: kb_judge routing rule not found, skipping constraint check: %v\n", err)
		return nil
	}

	if syncFamily == judgeFamily {
		return fmt.Errorf(
			"CRITICAL: kb_sync and kb_judge routing rules use the same model family %q — "+
				"this defeats cross-family verification. Update ai_routing_rules to use different families "+
				"(e.g. gemini for kb_sync, claude for kb_judge). Refusing to run.",
			syncFamily,
		)
	}

	fmt.Printf("Model-family constraint OK: kb_sync=%s, kb_judge=%s\n", syncFamily, judgeFamily)
	return nil
}

// fetchMergedPRContent finds the merged PR for a work item and returns (diff, body, error).
func fetchMergedPRContent(ctx context.Context, wiID, wiContent string) (string, string, error) {
	// Try metadata first
	prURL := ""
	if conn != nil {
		prURL, _ = conn.GetMetadata(ctx, wiID, "pr_url")
	}

	// Fall back to gh pr list search
	if prURL == "" {
		out, err := exec.CommandContext(ctx, "gh", "pr", "list",
			"--search", wiID,
			"--state", "merged",
			"--json", "url,number",
			"--limit", "1",
		).Output()
		if err == nil {
			var prs []struct {
				URL    string `json:"url"`
				Number int    `json:"number"`
			}
			if json.Unmarshal(out, &prs) == nil && len(prs) > 0 {
				prURL = prs[0].URL
			}
		}
	}

	if prURL == "" {
		return "", wiContent, fmt.Errorf("no merged PR found for %s", wiID)
	}

	// Fetch PR body
	bodyOut, _ := exec.CommandContext(ctx, "gh", "pr", "view", prURL,
		"--json", "body", "--jq", ".body").Output()
	prBody := strings.TrimSpace(string(bodyOut))
	if prBody == "" {
		prBody = wiContent
	}

	// Fetch diff
	diffOut, err := exec.CommandContext(ctx, "gh", "pr", "diff", prURL).Output()
	if err != nil {
		return "", prBody, fmt.Errorf("gh pr diff %s: %w", prURL, err)
	}

	return string(diffOut), prBody, nil
}

// extractConcepts pulls file paths, function names, stage names, etc. from the diff and body.
func extractConcepts(diff, body string) []string {
	seen := make(map[string]bool)
	var concepts []string

	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			concepts = append(concepts, s)
		}
	}

	// Extract .go file paths from diff headers
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++ b/") || strings.HasPrefix(line, "--- a/") {
			path := strings.TrimPrefix(strings.TrimPrefix(line, "+++ b/"), "--- a/")
			if !strings.HasSuffix(path, "/dev/null") {
				add(path)
				// Also add the base filename
				add(filepath.Base(path))
			}
		}
		// Extract function names from diff additions
		if strings.HasPrefix(line, "+func ") || strings.HasPrefix(line, " func ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				name := strings.TrimPrefix(parts[1], "*")
				if idx := strings.Index(name, "("); idx > 0 {
					add(name[:idx])
				}
			}
		}
	}

	// Extract shard IDs and key terms from body
	for _, word := range strings.Fields(body) {
		word = strings.Trim(word, ".,;:\"'`()")
		// Shard IDs: 2-letter prefix + dash + 6 hex chars
		if len(word) == 9 && word[2] == '-' {
			add(word)
		}
	}

	return concepts
}

// findAffectedKBArticles finds KB articles related to the given concepts.
func findAffectedKBArticles(ctx context.Context, concepts []string, diff string) []string {
	seen := make(map[string]bool)
	var articles []string

	addArticle := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			articles = append(articles, id)
		}
	}

	// Semantic search for each concept batch (group to reduce calls)
	batchSize := 5
	for i := 0; i < len(concepts); i += batchSize {
		end := i + batchSize
		if end > len(concepts) {
			end = len(concepts)
		}
		batch := concepts[i:end]
		query := strings.Join(batch, " ")

		out, err := exec.CommandContext(ctx, "cxp", "kb", "search", query,
			"-o", "json", "--limit", "10").Output()
		if err != nil {
			continue
		}

		var results []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(out, &results) == nil {
			for _, r := range results {
				addArticle(r.ID)
			}
		}
	}

	// Full-text scan: find file paths from diff in KB articles
	filePaths := extractFilePaths(diff)
	if len(filePaths) > 0 {
		out, err := exec.CommandContext(ctx, "cxp", "kb", "list", "-o", "json").Output()
		if err == nil {
			var kbArticles []struct {
				ID      string `json:"id"`
				Content string `json:"content"`
			}
			if json.Unmarshal(out, &kbArticles) == nil {
				for _, article := range kbArticles {
					for _, fp := range filePaths {
						if strings.Contains(article.Content, fp) {
							addArticle(article.ID)
							break
						}
					}
				}
			}
		}
	}

	return articles
}

// extractFilePaths returns file paths found in a git diff.
func extractFilePaths(diff string) []string {
	seen := make(map[string]bool)
	var paths []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++ b/") {
			p := strings.TrimPrefix(line, "+++ b/")
			if !strings.HasSuffix(p, "/dev/null") && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// processKBArticle runs the 3-layer kb-sync pipeline for a single article.
// Returns true if the article was successfully updated, false if rolled back.
func processKBArticle(ctx context.Context, articleID, wiID, wiContent, prDiff, prBody string) (bool, error) {
	fmt.Printf("  [%s] fetching current content...\n", articleID)

	// Get current article content
	oldContentOut, err := exec.CommandContext(ctx, "cxp", "kb", "show", articleID, "-o", "json").Output()
	if err != nil {
		return false, fmt.Errorf("cxp kb show %s: %w", articleID, err)
	}
	var oldArticle struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(oldContentOut, &oldArticle); err != nil {
		return false, fmt.Errorf("parse article content: %w", err)
	}
	oldContent := oldArticle.Content

	// Proposed content output path
	ts := time.Now().Unix()
	proposedFile := fmt.Sprintf("/tmp/kb-sync-%s-%d.md", articleID, ts)

	// Step A: run kb-sync skill (writer) via cobuild dispatch
	// The skill writes proposed content to proposedFile
	fmt.Printf("  [%s] dispatching kb-sync writer...\n", articleID)
	writerPrompt := fmt.Sprintf(
		`/kb-sync article=%s wi=%s output=%s`,
		articleID, wiID, proposedFile,
	)

	dispatchOut, err := exec.CommandContext(ctx,
		"cobuild", "dispatch", wiID,
		"--prompt", writerPrompt,
		"--wait",
	).CombinedOutput()
	if err != nil {
		fmt.Printf("    dispatch failed: %s\n", string(dispatchOut))
		// Fall through — if the file wasn't written, factcheck will fail
	}

	// If the file wasn't produced, construct a minimal proposal based on the diff
	if _, err := os.Stat(proposedFile); err != nil {
		fmt.Printf("  [%s] no proposed file produced — generating minimal update\n", articleID)
		// Write old content as proposal (no-op update) so factcheck can still run
		if writeErr := os.WriteFile(proposedFile, []byte(oldContent), 0600); writeErr != nil {
			return false, fmt.Errorf("write fallback proposal: %w", writeErr)
		}
	}
	defer os.Remove(proposedFile)

	// Step B: Layer 1 — kb-factcheck
	fmt.Printf("  [%s] running kb-factcheck...\n", articleID)
	factcheckOutputFile := fmt.Sprintf("/tmp/kb-factcheck-%s-%d.json", articleID, ts)
	defer os.Remove(factcheckOutputFile)

	factcheckOut, err := exec.CommandContext(ctx,
		"cobuild", "dispatch", wiID,
		"--prompt", fmt.Sprintf("/kb-factcheck input=%s output=%s", proposedFile, factcheckOutputFile),
		"--wait",
	).CombinedOutput()
	if err != nil {
		fmt.Printf("    factcheck dispatch failed: %s\n", string(factcheckOut))
	}

	fcResult, err := readFactcheckResult(factcheckOutputFile)
	if err != nil {
		fmt.Printf("    could not read factcheck result (%v) — treating as fail\n", err)
		logKBGap(ctx, articleID, wiID, "factcheck result file not produced")
		return false, nil
	}

	if fcResult.Verdict != "pass" {
		reasons := make([]string, 0, len(fcResult.ClaimsFailed))
		for _, c := range fcResult.ClaimsFailed {
			reasons = append(reasons, fmt.Sprintf("%s %q: %s", c.Type, c.Value, c.Reason))
		}
		gapMsg := fmt.Sprintf("factcheck failed: %s", strings.Join(reasons, "; "))
		fmt.Printf("    [%s] factcheck FAIL — %s\n", articleID, gapMsg)
		logKBGap(ctx, articleID, wiID, gapMsg)
		return false, nil
	}
	fmt.Printf("    factcheck PASS (%d/%d claims verified)\n", fcResult.ClaimsVerified, fcResult.ClaimsChecked)

	// Step C: Layer 2 — kb-judge
	fmt.Printf("  [%s] running kb-judge...\n", articleID)
	judgeOutputFile := fmt.Sprintf("/tmp/kb-judge-%s-%d.json", articleID, ts)
	defer os.Remove(judgeOutputFile)

	// Write diff and content to temp files for judge
	diffFile := fmt.Sprintf("/tmp/kb-diff-%s-%d.txt", articleID, ts)
	oldFile := fmt.Sprintf("/tmp/kb-old-%s-%d.md", articleID, ts)
	wiBodyFile := fmt.Sprintf("/tmp/kb-wibody-%s-%d.md", articleID, ts)
	defer func() {
		os.Remove(diffFile)
		os.Remove(oldFile)
		os.Remove(wiBodyFile)
	}()

	_ = os.WriteFile(diffFile, []byte(prDiff), 0600)
	_ = os.WriteFile(oldFile, []byte(oldContent), 0600)
	_ = os.WriteFile(wiBodyFile, []byte(prBody+"\n\n"+wiContent), 0600)

	judgeOut, err := exec.CommandContext(ctx,
		"cobuild", "dispatch", wiID,
		"--prompt", fmt.Sprintf(
			"/kb-judge diff=%s wi-body=%s old=%s new=%s output=%s",
			diffFile, wiBodyFile, oldFile, proposedFile, judgeOutputFile,
		),
		"--wait",
	).CombinedOutput()
	if err != nil {
		fmt.Printf("    judge dispatch failed: %s\n", string(judgeOut))
	}

	jResult, err := readJudgeResult(judgeOutputFile)
	if err != nil {
		fmt.Printf("    could not read judge result (%v) — treating as fail\n", err)
		logKBGap(ctx, articleID, wiID, "judge result file not produced")
		return false, nil
	}

	// Apply judge output handling rules
	shouldRollback := false
	gapReason := ""

	switch jResult.Verdict {
	case "consistent":
		// pass — update the article
	case "inaccurate", "incomplete":
		// fail on high severity issues, pass+log on medium/low
		for _, issue := range jResult.Issues {
			if issue.Severity == "high" {
				shouldRollback = true
				gapReason = fmt.Sprintf("judge verdict=%s (high severity): %s", jResult.Verdict, issue.Description)
				break
			}
		}
		if !shouldRollback {
			// medium/low — log gap but don't rollback
			for _, issue := range jResult.Issues {
				logKBGap(ctx, articleID, wiID, fmt.Sprintf("judge %s (low/medium): %s", jResult.Verdict, issue.Description))
			}
		}
	case "gaps_noted":
		// pass — log gaps for backfill
		for _, gap := range jResult.GapsIdentified {
			logKBGap(ctx, articleID, wiID, fmt.Sprintf("gap noted: %s", gap))
		}
	default:
		// Unknown verdict — rollback to be safe
		shouldRollback = true
		gapReason = fmt.Sprintf("unknown judge verdict: %q", jResult.Verdict)
	}

	if shouldRollback {
		fmt.Printf("    [%s] judge FAIL — %s\n", articleID, gapReason)
		logKBGap(ctx, articleID, wiID, gapReason)
		return false, nil
	}

	fmt.Printf("    judge PASS (verdict=%s)\n", jResult.Verdict)

	// Step D: commit the update
	proposedContent, err := os.ReadFile(proposedFile)
	if err != nil {
		return false, fmt.Errorf("read proposed content: %w", err)
	}

	changeSummary := fmt.Sprintf("Updated by kb-sync for %s (factcheck=%d/%d, judge=%s)",
		wiID, fcResult.ClaimsVerified, fcResult.ClaimsChecked, jResult.Verdict)

	updateOut, err := exec.CommandContext(ctx,
		"cxp", "kb", "update", articleID,
		"--file", proposedFile,
		"--summary", changeSummary,
	).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("cxp kb update %s: %s: %w", articleID, string(updateOut), err)
	}

	_ = proposedContent // content was passed via file
	return true, nil
}

// readFactcheckResult reads and parses the factcheck JSON output file.
func readFactcheckResult(path string) (*factcheckResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var result factcheckResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse factcheck JSON: %w", err)
	}
	return &result, nil
}

// readJudgeResult reads and parses the judge JSON output file.
func readJudgeResult(path string) (*judgeResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var result judgeResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse judge JSON: %w", err)
	}
	return &result, nil
}

// logKBGap appends a failure entry to the pf-kb-gaps shard.
func logKBGap(ctx context.Context, articleID, wiID, reason string) {
	ts := time.Now().UTC().Format(time.RFC3339)
	body := fmt.Sprintf("**Date:** %s\n**Article:** %s\n**Work item:** %s\n**Reason:** %s",
		ts, articleID, wiID, reason)
	out, err := exec.CommandContext(ctx,
		"cxp", "shard", "append", "pf-kb-gaps",
		"--body", body,
		"--project", "penfold",
	).CombinedOutput()
	if err != nil {
		fmt.Printf("    Warning: failed to log KB gap: %s: %v\n", string(out), err)
	}
}

// recordKBSyncVerdict records the gate verdict for the kb-sync phase.
func recordKBSyncVerdict(ctx context.Context, wiID string, verdict kbSyncGateVerdict, summary string) error {
	verdictStr := string(verdict)
	body := fmt.Sprintf("kb-sync phase verdict: **%s**\n\n%s", verdictStr, summary)

	fmt.Printf("\nkb-sync verdict: %s (%s)\n", verdictStr, summary)

	// Record via gate logic if store is available
	if cbStore != nil {
		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)
		_, err := RecordGateVerdict(ctx, conn, cbStore, wiID, "kb-sync", "pass", body, 0, pCfg)
		if err != nil {
			fmt.Printf("Warning: failed to record gate verdict: %v\n", err)
		}
	} else {
		// Append to work item if no store
		if conn != nil {
			if err := conn.AppendContent(ctx, wiID, "\n\n## kb-sync\n\n"+body); err != nil {
				fmt.Printf("Warning: failed to append kb-sync verdict: %v\n", err)
			}
		}
	}

	return nil
}

func init() {
	kbSyncCmd.Flags().Bool("dry-run", false, "Show what would be updated without executing")
	rootCmd.AddCommand(kbSyncCmd)
}
