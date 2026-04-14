package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/client"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	"github.com/spf13/cobra"
)

type dashboardPipelineResolver func(ctx context.Context, designID string) (*pipelinestate.PipelineState, error)

var resolveDashboardPipelineState dashboardPipelineResolver = pipelinestate.Resolve

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Pipeline analytics dashboard (historical aggregates)",
	Long: `Shows pipeline analytics across all projects: totals, per-project
breakdowns, gate pass rates, session stats, and recent activity.

For a snapshot of LIVE state (processes, tmux, sessions, PRs, per-pipeline
health + suggestions), use ` + "`cobuild live`" + ` instead. dashboard is the
historical/aggregates view; live is the in-flight view.

Use --project to filter to one project. Use --json for structured output.`,
	Example: `  cobuild dashboard                   # all projects
  cobuild dashboard --project penfold  # one project
  cobuild dashboard --json             # structured output`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		projectFilter, _ := cmd.Flags().GetString("project")

		if cbClient == nil {
			return fmt.Errorf("no database connection")
		}

		conn, err := cbClient.Connect(ctx)
		if err != nil {
			return fmt.Errorf("connect: %v", err)
		}
		defer conn.Close(ctx)

		filterArgs := []any{}
		if projectFilter != "" {
			filterArgs = append(filterArgs, projectFilter)
		}

		// === Totals ===
		fmt.Println("# CoBuild Dashboard")
		fmt.Println()

		// Projects summary
		rows, err := conn.Query(ctx, `
			SELECT project,
				COUNT(*) as pipelines,
				COUNT(*) FILTER (WHERE status='active') as active,
				COUNT(*) FILTER (WHERE status='completed') as completed,
				COALESCE(mode, 'manual') as primary_mode
			FROM pipeline_runs
			`+conditionalWhere(projectFilter, "project")+`
			GROUP BY project, COALESCE(mode, 'manual')
			ORDER BY pipelines DESC
		`, filterArgs...)
		if err == nil {
			fmt.Println("## Projects")
			fmt.Println()
			fmt.Printf("%-12s %8s %8s %10s %10s\n", "PROJECT", "TOTAL", "ACTIVE", "COMPLETED", "MODE")
			fmt.Printf("%-12s %8s %8s %10s %10s\n", "-------", "-----", "------", "---------", "----")
			for rows.Next() {
				var proj, mode string
				var total, active, completed int
				rows.Scan(&proj, &total, &active, &completed, &mode)
				fmt.Printf("%-12s %8d %8d %10d %10s\n", proj, total, active, completed, mode)
			}
			rows.Close()
			fmt.Println()
		}

		// Gate verdicts
		rows, err = conn.Query(ctx, `
			SELECT pr.project, pg.gate_name,
				COUNT(*) as total,
				COUNT(*) FILTER (WHERE pg.verdict='pass') as passed,
				COUNT(*) FILTER (WHERE pg.verdict='fail') as failed,
				ROUND(COUNT(*) FILTER (WHERE pg.verdict='pass')::numeric / GREATEST(COUNT(*), 1) * 100) as pass_rate
			FROM pipeline_gates pg
			JOIN pipeline_runs pr ON pg.pipeline_id = pr.id
			`+conditionalWhere(projectFilter, "pr.project")+`
			GROUP BY pr.project, pg.gate_name
			ORDER BY pr.project, total DESC
		`, filterArgs...)
		if err == nil {
			fmt.Println("## Gate Verdicts")
			fmt.Println()
			fmt.Printf("%-12s %-24s %6s %6s %6s %8s\n", "PROJECT", "GATE", "TOTAL", "PASS", "FAIL", "RATE")
			fmt.Printf("%-12s %-24s %6s %6s %6s %8s\n", "-------", "----", "-----", "----", "----", "----")
			for rows.Next() {
				var proj, gate string
				var total, passed, failed, rate int
				rows.Scan(&proj, &gate, &total, &passed, &failed, &rate)
				fmt.Printf("%-12s %-24s %6d %6d %6d %7d%%\n", proj, gate, total, passed, failed, rate)
			}
			rows.Close()
			fmt.Println()
		}

		// Session stats
		rows, err = conn.Query(ctx, `
			SELECT project, phase,
				COUNT(*) as sessions,
				COUNT(*) FILTER (WHERE status='completed') as completed,
				COUNT(*) FILTER (WHERE status='running') as running,
				COUNT(*) FILTER (WHERE status='failed') as failed,
				COALESCE(AVG(duration_seconds) FILTER (WHERE duration_seconds > 0), 0)::int as avg_sec,
				COALESCE(MAX(duration_seconds), 0) as max_sec,
				COALESCE(SUM(files_changed) FILTER (WHERE files_changed > 0), 0) as total_files,
				COALESCE(SUM(commits) FILTER (WHERE commits > 0), 0) as total_commits
			FROM pipeline_sessions
			`+conditionalWhere(projectFilter, "project")+`
			GROUP BY project, phase
			ORDER BY project, sessions DESC
		`, filterArgs...)
		if err == nil {
			fmt.Println("## Agent Sessions")
			fmt.Println()
			fmt.Printf("%-12s %-14s %5s %5s %5s %5s %8s %8s %6s %7s\n",
				"PROJECT", "PHASE", "TOTAL", "DONE", "RUN", "FAIL", "AVG(s)", "MAX(s)", "FILES", "COMMITS")
			fmt.Printf("%-12s %-14s %5s %5s %5s %5s %8s %8s %6s %7s\n",
				"-------", "-----", "-----", "----", "---", "----", "------", "------", "-----", "-------")
			for rows.Next() {
				var proj, phase string
				var sessions, completed, running, failed, avgSec, maxSec, files, commits int
				rows.Scan(&proj, &phase, &sessions, &completed, &running, &failed, &avgSec, &maxSec, &files, &commits)
				fmt.Printf("%-12s %-14s %5d %5d %5d %5d %8d %8d %6d %7d\n",
					proj, phase, sessions, completed, running, failed, avgSec, maxSec, files, commits)
			}
			rows.Close()
			fmt.Println()
		}

		// Active pipelines
		rows, err = conn.Query(ctx, `
			SELECT pr.design_id, pr.project, pr.current_phase, pr.status,
				COALESCE(pr.mode, 'manual') as mode,
				COALESCE(tc.total, 0) as task_total,
				COALESCE(tc.done, 0) as task_done,
				pr.updated_at
			FROM pipeline_runs pr
			LEFT JOIN (
				SELECT pipeline_id,
					COUNT(*) as total,
					COUNT(*) FILTER (WHERE status = 'completed') as done
				FROM pipeline_tasks GROUP BY pipeline_id
			) tc ON tc.pipeline_id = pr.id
			WHERE pr.status = 'active'
			`+conditionalAnd(projectFilter, "pr.project")+`
			ORDER BY pr.updated_at DESC
			LIMIT 20
		`, filterArgs...)
		if err == nil {
			activeRows, buildErr := buildDashboardActivePipelineRows(ctx, rows, resolveDashboardPipelineState)
			rows.Close()
			if buildErr != nil {
				return fmt.Errorf("build active pipelines: %w", buildErr)
			}
			renderDashboardActivePipelines(activeRows)
		}

		// Token usage
		rows, err = conn.Query(ctx, `
			SELECT project,
				COUNT(*) as sessions,
				COALESCE(SUM(output_tokens), 0) as total_output,
				COALESCE(SUM(cache_read_tokens), 0) as total_cache_read,
				COALESCE(SUM(estimated_cost_usd), 0)::numeric(10,2) as total_cost,
				COALESCE(AVG(max_context_tokens) FILTER (WHERE max_context_tokens > 0), 0)::int as avg_context,
				COALESCE(SUM(turns), 0) as total_turns
			FROM pipeline_sessions
			WHERE output_tokens IS NOT NULL
			`+conditionalAnd(projectFilter, "project")+`
			GROUP BY project
			ORDER BY total_cost DESC
		`, filterArgs...)
		if err == nil {
			fmt.Println("## Token Usage")
			fmt.Println()
			fmt.Printf("%-12s %6s %10s %12s %10s %10s %8s\n",
				"PROJECT", "SESS", "OUTPUT", "CACHE READ", "AVG CTX", "TURNS", "COST")
			fmt.Printf("%-12s %6s %10s %12s %10s %10s %8s\n",
				"-------", "----", "------", "----------", "-------", "-----", "----")
			for rows.Next() {
				var proj string
				var sessions, totalOutput, totalCacheRead, avgContext, totalTurns int
				var cost float64
				rows.Scan(&proj, &sessions, &totalOutput, &totalCacheRead, &cost, &avgContext, &totalTurns)
				fmt.Printf("%-12s %6d %10s %12s %10s %10d $%7.2f\n",
					proj, sessions,
					formatTokens(totalOutput),
					formatTokens(totalCacheRead),
					formatTokens(avgContext),
					totalTurns, cost)
			}
			rows.Close()
			fmt.Println()
		}

		// Quick totals
		var totalPipelines, totalGates, totalSessions int
		conn.QueryRow(ctx, "SELECT COUNT(*) FROM pipeline_runs"+conditionalWhere(projectFilter, "project"), filterArgs...).Scan(&totalPipelines)
		conn.QueryRow(ctx, `SELECT COUNT(*) FROM pipeline_gates pg JOIN pipeline_runs pr ON pg.pipeline_id = pr.id`+conditionalWhere(projectFilter, "pr.project"), filterArgs...).Scan(&totalGates)
		conn.QueryRow(ctx, "SELECT COUNT(*) FROM pipeline_sessions"+conditionalWhere(projectFilter, "project"), filterArgs...).Scan(&totalSessions)

		fmt.Println("---")
		fmt.Printf("Totals: %d pipelines, %d gate verdicts, %d agent sessions\n", totalPipelines, totalGates, totalSessions)

		return nil
	},
}

func conditionalWhere(filter, column string) string {
	if filter != "" {
		return fmt.Sprintf(" WHERE %s = $1", column)
	}
	return ""
}

func conditionalAnd(filter, column string) string {
	if filter != "" {
		return fmt.Sprintf(" AND %s = $1", column)
	}
	return ""
}

func init() {
	dashboardCmd.Flags().String("project", "", "Filter to a specific project")
	rootCmd.AddCommand(dashboardCmd)
}

type dashboardActivePipelineSeed struct {
	DesignID  string
	Project   string
	Phase     string
	Mode      string
	TaskTotal int
	TaskDone  int
	UpdatedAt time.Time
}

type dashboardActivePipelineRow struct {
	ID           string
	Project      string
	Phase        string
	Mode         string
	Tasks        string
	Health       string
	Signals      string
	LastActivity string
}

func buildDashboardActivePipelineRows(ctx context.Context, rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}, resolver dashboardPipelineResolver) ([]dashboardActivePipelineRow, error) {
	var output []dashboardActivePipelineRow
	for rows.Next() {
		var seed dashboardActivePipelineSeed
		if err := rows.Scan(
			&seed.DesignID,
			&seed.Project,
			&seed.Phase,
			new(string), // raw pipeline_runs status is intentionally ignored here
			&seed.Mode,
			&seed.TaskTotal,
			&seed.TaskDone,
			&seed.UpdatedAt,
		); err != nil {
			return nil, err
		}
		output = append(output, buildDashboardActivePipelineRow(ctx, seed, resolver))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return output, nil
}

func buildDashboardActivePipelineRow(ctx context.Context, seed dashboardActivePipelineSeed, resolver dashboardPipelineResolver) dashboardActivePipelineRow {
	row := dashboardActivePipelineRow{
		ID:           client.Truncate(seed.DesignID, 16),
		Project:      seed.Project,
		Phase:        seed.Phase,
		Mode:         seed.Mode,
		Tasks:        formatDashboardTasks(seed.TaskDone, seed.TaskTotal),
		Health:       string(pipelinestate.HealthMissing),
		Signals:      "-",
		LastActivity: client.TimeAgo(seed.UpdatedAt),
	}

	if resolver == nil {
		row.Signals = "resolver unavailable"
		return row
	}

	state, err := resolver(ctx, seed.DesignID)
	if err != nil && !errors.Is(err, pipelinestate.ErrNotFound) {
		row.Signals = truncateDashboardSignals(err.Error())
		return row
	}
	if state == nil {
		row.Signals = "pipeline state not found"
		return row
	}

	if state.Project != "" {
		row.Project = state.Project
	}
	if state.Run != nil {
		if state.Run.Phase != "" {
			row.Phase = state.Run.Phase
		}
		if state.Run.Mode != "" {
			row.Mode = state.Run.Mode
		}
		if !state.Run.UpdatedAt.IsZero() {
			row.LastActivity = client.TimeAgo(state.Run.UpdatedAt)
		}
	}

	row.Health = string(state.Health)
	signals := dashboardSignalsFromState(state)
	if signals != "" {
		row.Signals = truncateDashboardSignals(signals)
	}
	return row
}

func renderDashboardActivePipelines(rows []dashboardActivePipelineRow) {
	fmt.Println("## Active Pipelines")
	fmt.Println()
	fmt.Printf("%-16s %-12s %-14s %-10s %6s %-13s %-70s %s\n",
		"ID", "PROJECT", "PHASE", "MODE", "TASKS", "HEALTH", "SIGNALS", "LAST ACTIVITY")
	fmt.Printf("%-16s %-12s %-14s %-10s %6s %-13s %-70s %s\n",
		"----", "-------", "-----", "----", "-----", "------", "-------", "-------------")
	for _, row := range rows {
		fmt.Printf("%-16s %-12s %-14s %-10s %6s %-13s %-70s %s\n",
			row.ID, row.Project, row.Phase, row.Mode, row.Tasks, row.Health, row.Signals, row.LastActivity)
	}
	fmt.Println()
}

func dashboardSignalsFromState(state *pipelinestate.PipelineState) string {
	if state == nil {
		return ""
	}

	parts := make([]string, 0, len(state.Inconsistencies)+len(state.SourceErrors))
	for _, issue := range state.Inconsistencies {
		if issue == "" {
			continue
		}
		parts = append(parts, issue)
	}
	for _, sourceErr := range state.SourceErrors {
		if sourceErr.Source == "" && sourceErr.Message == "" {
			continue
		}
		if sourceErr.Message == "" {
			parts = append(parts, fmt.Sprintf("degraded source: %s", sourceErr.Source))
			continue
		}
		parts = append(parts, fmt.Sprintf("degraded source %s: %s", sourceErr.Source, sourceErr.Message))
	}

	return strings.Join(parts, "; ")
}

func formatDashboardTasks(done, total int) string {
	if total <= 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", done, total)
}

func truncateDashboardSignals(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return client.Truncate(value, 120)
}
