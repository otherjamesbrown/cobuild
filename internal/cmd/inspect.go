package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/cliutil"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:     "inspect <shard-id>",
	Short:   "Aggregated pipeline, shard, session, PR, and gate state",
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild inspect cb-abc123`,
	RunE:    runInspect,
}

func runInspect(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	shardID := args[0]

	data, err := gatherInspectData(ctx, shardID)
	if err != nil {
		return err
	}

	if outputFormat == "json" {
		s, _ := cliutil.FormatJSON(data)
		fmt.Println(s)
		return nil
	}

	printInspectText(data)
	return nil
}

// inspectData holds everything the inspect command collects, used for both
// text and JSON output.
type inspectData struct {
	Shard    *inspectShard    `json:"shard"`
	Pipeline *inspectPipeline `json:"pipeline,omitempty"`
	Sessions []inspectSession `json:"sessions,omitempty"`
	PR       *inspectPR       `json:"pr,omitempty"`
	Gates    []inspectGate    `json:"gates,omitempty"`
}

type inspectShard struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Parent *inspectParent `json:"parent,omitempty"`
}

type inspectParent struct {
	ID     string `json:"id"`
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
}

type inspectPipeline struct {
	Phase        string `json:"phase"`
	Status       string `json:"status"`
	Activity     string `json:"activity,omitempty"`
	LastActivity string `json:"last_activity,omitempty"`
}

type inspectSession struct {
	ID           string `json:"id"`
	StartedAt    string `json:"started_at"`
	EndedAt      string `json:"ended_at,omitempty"`
	Duration     string `json:"duration,omitempty"`
	Runtime      string `json:"runtime"`
	Model        string `json:"model,omitempty"`
	Running      bool   `json:"running,omitempty"`
	StaleContext bool   `json:"stale_context,omitempty"`
}

type inspectPR struct {
	Number    string `json:"number,omitempty"`
	URL       string `json:"url"`
	State     string `json:"state,omitempty"`
	Mergeable string `json:"mergeable,omitempty"`
	CI        string `json:"ci,omitempty"`
}

type inspectGate struct {
	Time          string `json:"time"`
	GateName      string `json:"gate_name"`
	Round         int    `json:"round"`
	Verdict       string `json:"verdict"`
	ReviewShardID string `json:"review_shard_id,omitempty"`
	FindingsHash  string `json:"findings_hash,omitempty"`
}

func gatherInspectData(ctx context.Context, shardID string) (*inspectData, error) {
	if conn == nil {
		return nil, fmt.Errorf("no connector configured")
	}

	// --- Shard ---
	item, err := conn.Get(ctx, shardID)
	if err != nil {
		return nil, fmt.Errorf("get shard %s: %w", shardID, err)
	}

	shard := &inspectShard{
		ID:     item.ID,
		Type:   item.Type,
		Status: item.Status,
		Title:  item.Title,
	}

	// Parent edge (child-of)
	edges, edgeErr := conn.GetEdges(ctx, shardID, "outgoing", []string{"child-of"})
	if edgeErr == nil && len(edges) > 0 {
		shard.Parent = &inspectParent{
			ID:     edges[0].ItemID,
			Type:   edges[0].Type,
			Status: edges[0].Status,
		}
	}

	data := &inspectData{Shard: shard}

	// --- Pipeline ---
	if cbStore != nil {
		// GetRun returns the static run; ListRuns returns PipelineRunStatus
		// which includes the derived Activity field.
		run, runErr := cbStore.GetRun(ctx, shardID)
		if runErr == nil && run != nil {
			pip := &inspectPipeline{
				Phase:  run.CurrentPhase,
				Status: run.Status,
			}

			// Get activity from ListRuns (it's computed server-side).
			runs, listErr := cbStore.ListRuns(ctx, "")
			if listErr == nil {
				for _, r := range runs {
					if r.DesignID == shardID {
						pip.Activity = r.Activity
						if !r.LastProgress.IsZero() {
							pip.LastActivity = cliutil.TimeAgo(r.LastProgress)
						}
						break
					}
				}
			}
			data.Pipeline = pip
		}

		// --- Sessions (most recent 3) ---
		sessions, sessErr := cbStore.ListSessions(ctx, shardID)
		if sessErr == nil && len(sessions) > 0 {
			start := 0
			if len(sessions) > 3 {
				start = len(sessions) - 3
			}
			recent := sessions[start:]
			for _, s := range recent {
				is := inspectSession{
					ID:        s.ID,
					StartedAt: s.StartedAt.Format("15:04"),
					Runtime:   s.Runtime,
				}
				if s.Model != nil {
					is.Model = *s.Model
				}
				if s.EndedAt != nil {
					is.EndedAt = s.EndedAt.Format("15:04")
					is.Duration = formatDuration(s.EndedAt.Sub(s.StartedAt))
				} else {
					is.Running = true
					// cb-44a9d7: flag running sessions whose context may be stale
					// because the shard was updated after dispatch.
					if !item.UpdatedAt.IsZero() && item.UpdatedAt.After(s.StartedAt) {
						is.StaleContext = true
					}
				}
				data.Sessions = append(data.Sessions, is)
			}
		}

		// --- PR ---
		prURL, prErr := conn.GetMetadata(ctx, shardID, domain.MetaPRURL)
		if prErr == nil && prURL != "" {
			pr := &inspectPR{URL: prURL}
			pr.Number = extractPRNumber(prURL)

			// Best-effort: fetch live PR state via gh
			ghOut, ghErr := execCommandOutput(ctx, "gh", "pr", "view", prURL,
				"--json", "state,mergeable,statusCheckRollup")
			if ghErr == nil {
				parsePRDetails(pr, ghOut)
			}
			data.PR = pr
		}

		// --- Gates (last 5) ---
		gates, gateErr := cbStore.GetGateHistory(ctx, shardID)
		if gateErr == nil && len(gates) > 0 {
			start := 0
			if len(gates) > 5 {
				start = len(gates) - 5
			}
			for _, g := range gates[start:] {
				ig := inspectGate{
					Time:     g.CreatedAt.Format("15:04"),
					GateName: g.GateName,
					Round:    g.Round,
					Verdict:  strings.ToUpper(g.Verdict),
				}
				if g.ReviewShardID != nil {
					ig.ReviewShardID = *g.ReviewShardID
				}
				if g.FindingsHash != nil {
					ig.FindingsHash = *g.FindingsHash
				}
				data.Gates = append(data.Gates, ig)
			}
		}
	}

	return data, nil
}

func printInspectText(data *inspectData) {
	// SHARD
	fmt.Println("SHARD")
	fmt.Printf("  %s (%s) — %s\n", data.Shard.ID, data.Shard.Type, data.Shard.Status)
	fmt.Printf("  %s\n", data.Shard.Title)
	if data.Shard.Parent != nil {
		parentInfo := data.Shard.Parent.ID
		if data.Shard.Parent.Type != "" || data.Shard.Parent.Status != "" {
			parts := []string{}
			if data.Shard.Parent.Type != "" {
				parts = append(parts, data.Shard.Parent.Type)
			}
			if data.Shard.Parent.Status != "" {
				parts = append(parts, data.Shard.Parent.Status)
			}
			parentInfo += " (" + strings.Join(parts, ", ") + ")"
		}
		fmt.Printf("  child-of: %s\n", parentInfo)
	}

	// PIPELINE
	if data.Pipeline != nil {
		fmt.Println()
		fmt.Println("PIPELINE")
		parts := []string{
			fmt.Sprintf("Phase: %s", data.Pipeline.Phase),
			fmt.Sprintf("Status: %s", data.Pipeline.Status),
		}
		if data.Pipeline.Activity != "" {
			parts = append(parts, fmt.Sprintf("Activity: %s", data.Pipeline.Activity))
		}
		if data.Pipeline.LastActivity != "" {
			parts = append(parts, fmt.Sprintf("Last activity: %s", data.Pipeline.LastActivity))
		}
		fmt.Printf("  %s\n", strings.Join(parts, " | "))
	} else {
		fmt.Println()
		fmt.Println("PIPELINE")
		fmt.Println("  No pipeline run")
	}

	// SESSIONS
	if len(data.Sessions) > 0 {
		fmt.Println()
		fmt.Println("SESSIONS (most recent 3)")
		for _, s := range data.Sessions {
			if s.Running {
				fmt.Printf("  %s  started %s  (running)            runtime: %-8s model: %s\n",
					s.ID, s.StartedAt, s.Runtime, s.Model)
				if s.StaleContext {
					fmt.Printf("    WARNING: shard updated after dispatch — agent may have stale context\n")
					fmt.Printf("    Run: cobuild redispatch --reset-context %s\n", data.Shard.ID)
				}
			} else {
				fmt.Printf("  %s  started %s  ended %s (%s)  runtime: %-8s model: %s\n",
					s.ID, s.StartedAt, s.EndedAt, s.Duration, s.Runtime, s.Model)
			}
		}
	}

	// PR
	if data.PR != nil {
		fmt.Println()
		fmt.Println("PR")
		label := data.PR.URL
		if data.PR.Number != "" {
			state := ""
			if data.PR.State != "" {
				state = fmt.Sprintf(" [%s]", strings.ToLower(data.PR.State))
			}
			label = fmt.Sprintf("#%s%s %s", data.PR.Number, state, data.PR.URL)
		}
		fmt.Printf("  %s\n", label)
		if data.PR.Mergeable != "" || data.PR.CI != "" {
			parts := []string{}
			if data.PR.Mergeable != "" {
				parts = append(parts, fmt.Sprintf("Mergeable: %s", data.PR.Mergeable))
			}
			if data.PR.CI != "" {
				parts = append(parts, fmt.Sprintf("CI: %s", data.PR.CI))
			}
			fmt.Printf("  %s\n", strings.Join(parts, " | "))
		}
	}

	// GATES
	if len(data.Gates) > 0 {
		fmt.Println()
		fmt.Println("GATES (last 5)")
		for _, g := range data.Gates {
			extra := ""
			if g.ReviewShardID != "" {
				extra += "  " + g.ReviewShardID
			}
			if g.FindingsHash != "" {
				hash := g.FindingsHash
				if len(hash) > 8 {
					hash = hash[:8]
				}
				extra += "  hash:" + hash
			}
			fmt.Printf("  %s  %-8s  Round %d  %-4s%s\n",
				g.Time, g.GateName, g.Round, g.Verdict, extra)
		}
	}
}

// extractPRNumber extracts the PR number from a GitHub PR URL.
func extractPRNumber(url string) string {
	// https://github.com/org/repo/pull/123
	parts := strings.Split(url, "/")
	if len(parts) >= 2 && parts[len(parts)-2] == "pull" {
		return parts[len(parts)-1]
	}
	return ""
}

// parsePRDetails parses gh pr view --json output into the inspectPR struct.
func parsePRDetails(pr *inspectPR, data []byte) {
	var result struct {
		State              string `json:"state"`
		Mergeable          string `json:"mergeable"`
		StatusCheckRollup  []struct {
			State      string `json:"state"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return
	}
	pr.State = result.State
	pr.Mergeable = result.Mergeable

	if len(result.StatusCheckRollup) > 0 {
		pr.CI = summarizeCIStatus(result.StatusCheckRollup)
	}
}

// summarizeCIStatus produces a single word from the list of status checks.
func summarizeCIStatus(checks []struct {
	State      string `json:"state"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}) string {
	hasFailure := false
	hasPending := false
	for _, c := range checks {
		conclusion := strings.ToUpper(c.Conclusion)
		state := strings.ToUpper(c.State)
		if conclusion == "FAILURE" || conclusion == "ERROR" || state == "FAILURE" || state == "ERROR" {
			hasFailure = true
		}
		if conclusion == "" || state == "PENDING" || strings.ToUpper(c.Status) == "IN_PROGRESS" {
			hasPending = true
		}
	}
	switch {
	case hasFailure:
		return "failing"
	case hasPending:
		return "pending"
	default:
		return "passing"
	}
}

func init() {
	rootCmd.AddCommand(inspectCmd)
}
