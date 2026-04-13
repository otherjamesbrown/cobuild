package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/pipeline/livestate"
	"github.com/spf13/cobra"
)

var liveCmd = &cobra.Command{
	Use:   "live",
	Short: "Show live CoBuild state across all projects",
	Long: `Snapshots active CoBuild state in one view: orchestrate/poller processes,
active pipelines, tmux windows, running sessions, and open PRs.

Each pipeline is health-checked by cross-referencing sources. Stale or orphan
state is flagged with an actionable suggestion.

Use this when you want to know "what is CoBuild doing right now?" The output
is one-shot — wrap with watch(1) for continuous updates.`,
	Example: `  cobuild live
  cobuild live -o json
  cobuild live --no-prs       # skip GitHub queries (faster, no network)
  watch -n 5 cobuild live     # refresh every 5 seconds`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		outFmt, _ := cmd.Flags().GetString("output")
		noPRs, _ := cmd.Flags().GetBool("no-prs")
		showAll, _ := cmd.Flags().GetBool("all")

		repos := []string{}
		if !noPRs {
			repos = resolveLiveRepos(ctx)
		}

		collector := livestate.Collector{
			Store:    cbStore,
			Sessions: cbStore,
			Repos:    repos,
		}

		snap, _ := collector.Collect(ctx)

		// By default, filter out done/completed pipelines — the live view
		// is for what's currently in flight. --all overrides.
		if !showAll {
			active := snap.Pipelines[:0]
			for _, p := range snap.Pipelines {
				if p.Phase != "done" && p.Status != "completed" {
					active = append(active, p)
				}
			}
			snap.Pipelines = active
		}
		// Collect returns a partial snapshot even when sources fail;
		// snap.Errors carries per-source failure messages.

		if outFmt == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(snap)
		}

		renderLiveDashboard(cmd.OutOrStdout(), snap)
		return nil
	},
}

func init() {
	liveCmd.Flags().StringP("output", "o", "text", "Output format: text or json")
	liveCmd.Flags().Bool("no-prs", false, "Skip the gh pr list queries (faster, no network)")
	liveCmd.Flags().Bool("all", false, "Include done/completed pipelines (default: in-flight only)")
	rootCmd.AddCommand(liveCmd)
}

// resolveLiveRepos enumerates known repos from ~/.cobuild/repos.yaml and
// returns owner/repo strings (e.g. "otherjamesbrown/cobuild") for `gh`.
func resolveLiveRepos(ctx context.Context) []string {
	reg, err := config.LoadRepoRegistry()
	if err != nil || reg == nil {
		return nil
	}
	out := make([]string, 0, len(reg.Repos))
	seen := map[string]bool{}
	for _, entry := range reg.Repos {
		path := strings.TrimSpace(entry.Path)
		if path == "" {
			continue
		}
		ownerRepo := remoteOwnerRepoFromPath(ctx, path)
		if ownerRepo == "" || seen[ownerRepo] {
			continue
		}
		seen[ownerRepo] = true
		out = append(out, ownerRepo)
	}
	sort.Strings(out)
	return out
}

func remoteOwnerRepoFromPath(ctx context.Context, repoRoot string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	for _, prefix := range []string{"git@github.com:", "https://github.com/"} {
		if strings.HasPrefix(url, prefix) {
			return strings.TrimSuffix(strings.TrimPrefix(url, prefix), ".git")
		}
	}
	return ""
}

func renderLiveDashboard(w io.Writer, snap livestate.Snapshot) {
	now := snap.GeneratedAt
	if now.IsZero() {
		now = time.Now()
	}
	fmt.Fprintf(w, "CoBuild live — %s\n\n", now.Format("2006-01-02 15:04:05"))

	health := livestate.ComputeHealth(snap, livestate.DefaultHealthThresholds())
	healthByID := map[string]livestate.PipelineHealth{}
	for _, h := range health {
		healthByID[h.DesignID] = h
	}

	if len(snap.Pipelines) > 0 {
		fmt.Fprintln(w, "PIPELINES")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  DESIGN\tPROJECT\tPHASE\tHEALTH\tTMUX\tSUGGESTION")
		ps := append([]livestate.PipelineInfo(nil), snap.Pipelines...)
		sort.Slice(ps, func(i, j int) bool {
			if ps[i].Project != ps[j].Project {
				return ps[i].Project < ps[j].Project
			}
			return ps[i].DesignID < ps[j].DesignID
		})
		for _, p := range ps {
			h := healthByID[p.DesignID]
			tmuxTarget := h.TmuxTarget
			if tmuxTarget == "" {
				tmuxTarget = "-"
			}
			suggestion := h.Suggestion
			if suggestion == "" {
				suggestion = "-"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n",
				p.DesignID, p.Project, p.Phase, h.Health, tmuxTarget, suggestion)
		}
		tw.Flush()
		fmt.Fprintln(w)
	}

	if len(snap.Processes) > 0 {
		fmt.Fprintln(w, "PROCESSES")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  PID\tKIND\tPROJECT\tTARGET\tAGE")
		for _, p := range snap.Processes {
			age := "-"
			if p.AgeSeconds > 0 {
				age = formatLiveDuration(time.Duration(p.AgeSeconds) * time.Second)
			}
			target := p.TargetID
			if target == "" {
				target = "-"
			}
			project := p.Project
			if project == "" {
				project = "-"
			}
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\t%s\n", p.PID, p.Kind, project, target, age)
		}
		tw.Flush()
		fmt.Fprintln(w)
	}

	if len(snap.Sessions) > 0 {
		fmt.Fprintln(w, "SESSIONS")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  SESSION\tPROJECT\tDESIGN\tPHASE\tRUNTIME\tIDLE")
		for _, s := range snap.Sessions {
			age := formatLiveDuration(time.Duration(s.AgeSeconds) * time.Second)
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n",
				truncateLive(s.ID, 30), s.Project, s.DesignID, s.Phase, s.Runtime, age)
		}
		tw.Flush()
		fmt.Fprintln(w)
	}

	if len(snap.Tmux) > 0 {
		fmt.Fprintln(w, "TMUX WINDOWS")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  SESSION\tWINDOW\tTARGET")
		for _, t := range snap.Tmux {
			target := t.TargetID
			if target == "" {
				target = "-"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", t.SessionName, t.WindowName, target)
		}
		tw.Flush()
		fmt.Fprintln(w)
	}

	if len(snap.PRs) > 0 {
		fmt.Fprintln(w, "OPEN PRs")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  REPO\tPR\tBRANCH\tMERGEABLE\tTITLE")
		for _, pr := range snap.PRs {
			fmt.Fprintf(tw, "  %s\t#%d\t%s\t%s\t%s\n",
				pr.Repo, pr.Number, pr.Branch, pr.Mergeable, truncateLive(pr.Title, 60))
		}
		tw.Flush()
		fmt.Fprintln(w)
	}

	if orphans := livestate.OrphanTmux(snap); len(orphans) > 0 {
		fmt.Fprintln(w, "ORPHAN TMUX WINDOWS (no matching pipeline or session)")
		for _, o := range orphans {
			fmt.Fprintf(w, "  %s:%s — `tmux kill-window -t %s:%s`\n",
				o.SessionName, o.WindowName, o.SessionName, o.WindowName)
		}
		fmt.Fprintln(w)
	}

	if len(snap.Errors) > 0 {
		fmt.Fprintln(w, "SOURCE ERRORS (partial snapshot)")
		for _, e := range snap.Errors {
			fmt.Fprintf(w, "  [%s] %s\n", e.Source, e.Message)
		}
		fmt.Fprintln(w)
	}

	if len(snap.Pipelines) == 0 && len(snap.Processes) == 0 &&
		len(snap.Sessions) == 0 && len(snap.Tmux) == 0 && len(snap.PRs) == 0 {
		fmt.Fprintln(w, "No active CoBuild state.")
	}
}

func formatLiveDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func truncateLive(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
