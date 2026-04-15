package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/pipeline/livestate"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

// runDoctorDiagnose prints a deep, read-only diagnostic for a single design
// or bug shard. Sections cover the "four places you'd otherwise look by
// hand" per cb-d5e1dd #7: pipeline state, orchestrate processes, tmux
// windows, sessions, PRs, review-auth config, gate history, task status.
func runDoctorDiagnose(ctx context.Context, w io.Writer, errW io.Writer, shardID string) {
	fmt.Fprintf(w, "=== doctor diagnose %s ===\n", shardID)

	// 1. Pipeline run
	run, err := cbStore.GetRun(ctx, shardID)
	switch {
	case err != nil:
		fmt.Fprintf(w, "Pipeline run: not found (%v)\n", err)
	case run == nil:
		fmt.Fprintln(w, "Pipeline run: not found")
	default:
		age := time.Since(run.UpdatedAt).Round(time.Second)
		fmt.Fprintf(w, "Pipeline run: phase=%s status=%s mode=%s updated %s ago (%s)\n",
			run.CurrentPhase, run.Status, run.Mode, age, run.UpdatedAt.Format(time.RFC3339))
	}

	// 2. Orchestrate processes driving this shard
	procs, err := livestate.CollectProcesses(ctx, pipelineCommandCombinedOutput, time.Now())
	if err != nil {
		fmt.Fprintf(w, "Orchestrate processes: query error: %v\n", err)
	} else {
		matched := 0
		for _, p := range procs {
			if p.Kind == "orchestrate" && p.TargetID == shardID {
				ageStr := "unknown"
				if p.StartedAt != nil {
					ageStr = time.Since(*p.StartedAt).Round(time.Second).String()
				}
				fmt.Fprintf(w, "Orchestrate process: PID %d (age %s)\n", p.PID, ageStr)
				matched++
			}
		}
		if matched == 0 {
			fmt.Fprintln(w, "Orchestrate process: none running for this shard")
		}
	}

	// 3. Tmux windows + pipeline sessions (via pipelinestate.Resolve which
	// merges both sources).
	state, rerr := pipelinestate.Resolve(ctx, shardID)
	if rerr != nil {
		fmt.Fprintf(w, "Resolver: %v\n", rerr)
	} else if state != nil {
		fmt.Fprintf(w, "Tmux windows: %d\n", len(state.Tmux))
		for _, tm := range state.Tmux {
			fmt.Fprintf(w, "  - %s:%s (target=%s)\n", tm.SessionName, tm.WindowName, tm.TargetID)
		}
		running := 0
		for _, s := range state.Sessions {
			if s.Status == "running" {
				running++
			}
		}
		fmt.Fprintf(w, "Sessions: %d total, %d running\n", len(state.Sessions), running)
		for _, s := range state.Sessions {
			if s.Status == "running" {
				fmt.Fprintf(w, "  - %s task=%s phase=%s age=%ds\n", s.ID, s.TaskID, s.Phase, s.AgeSeconds)
			}
		}
		if len(state.Inconsistencies) > 0 {
			fmt.Fprintf(w, "Inconsistencies (%s):\n", state.Health)
			for _, inc := range state.Inconsistencies {
				fmt.Fprintf(w, "  - %s\n", inc)
			}
		}
	}

	// 3a. Recent early-death sessions (cb-1d8abc) — scanning the flat
	// ListSessions rather than state.Sessions because we want dead rows too.
	if run != nil {
		if recs, err := cbStore.ListSessions(ctx, shardID); err == nil {
			earlyDeaths := 0
			for _, s := range recs {
				if s.EarlyDeath {
					earlyDeaths++
				}
			}
			if earlyDeaths > 0 {
				fmt.Fprintf(w, "Early-death sessions: %d (agent died <60s after dispatch — see .cobuild/dispatch-error.log in the worktree)\n", earlyDeaths)
			}
		}
	}

	// 4. Child tasks
	var tasks []store.PipelineTaskRecord
	tasksLoaded := false
	if run != nil {
		records, err := cbStore.ListTasks(ctx, run.ID)
		if err == nil && len(records) > 0 {
			tasksLoaded = true
			tasks = records
			counts := map[string]int{}
			for _, t := range tasks {
				counts[t.Status]++
			}
			var parts []string
			for status, n := range counts {
				parts = append(parts, fmt.Sprintf("%s=%d", status, n))
			}
			fmt.Fprintf(w, "Tasks: %d (%s)\n", len(tasks), strings.Join(parts, ", "))
		} else if err == nil {
			tasksLoaded = true
			tasks = records
			fmt.Fprintln(w, "Tasks: none tracked in pipeline_tasks")
		} else {
			fmt.Fprintf(errW, "Warning: failed to load tasks for %s: %v\n", shardID, err)
		}
	}

	// 5. Recent gate history
	if run != nil {
		gates, err := cbStore.GetGateHistory(ctx, shardID)
		if err == nil && len(gates) > 0 {
			latest := gates[len(gates)-1]
			fmt.Fprintf(w, "Gates: %d total, latest=%s/%s %s (%s)\n",
				len(gates), latest.GateName, latest.Verdict, latest.CreatedAt.Format(time.RFC3339),
				humanAgo(latest.CreatedAt))
		} else if err == nil {
			fmt.Fprintln(w, "Gates: none recorded")
		}
	}

	// 6. Review auth configured?
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		fmt.Fprintln(w, "Review auth: ANTHROPIC_API_KEY set")
	} else {
		fmt.Fprintln(w, "Review auth: ANTHROPIC_API_KEY NOT set — built-in Claude review will 401")
	}
	repoRoot := findRepoRoot()
	if pCfg, _ := config.LoadConfig(repoRoot); pCfg != nil {
		if pCfg.Review.Provider != "" {
			fmt.Fprintf(w, "Review provider (config): %s\n", pCfg.Review.Provider)
		}
	}

	// 7. PR state — best-effort via gh pr list for task worktree branches.
	// We don't have a direct shardID→PR mapping here, but we can at least
	// surface the count via conn metadata if the tasks are known.
	if conn != nil && run != nil && tasksLoaded {
		prCount := 0
		for _, t := range tasks {
			if pr, _ := conn.GetMetadata(ctx, t.TaskShardID, "pr_url"); pr != "" {
				prCount++
			}
		}
		if prCount > 0 {
			fmt.Fprintf(w, "PRs on tasks: %d (check mergeability with `gh pr list`)\n", prCount)
		}
	}
}

// humanAgo returns "5m ago" / "2h ago" style strings for a past timestamp.
func humanAgo(t time.Time) string {
	d := time.Since(t).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%.1fh ago", d.Hours())
	default:
		return fmt.Sprintf("%.1fd ago", d.Hours()/24)
	}
}
