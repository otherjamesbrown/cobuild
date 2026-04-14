// Package insights gathers aggregate pipeline statistics for `cobuild
// insights` and `cobuild improve`. Ported from internal/client/insights.go
// in the cb-3f5be6 / cb-b2f3ac big-bang migration — behavior unchanged,
// just no longer coupled to the legacy client.
package insights

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Stats holds all data needed for the pipeline insights report.
type Stats struct {
	Project   string    `json:"project"`
	Generated time.Time `json:"generated"`

	Overview       Overview           `json:"overview"`
	GateStats      []GatePassRate     `json:"gate_stats"`
	FailureReasons []GateFailureGroup `json:"failure_reasons"`
	AgentPerf      AgentPerformance   `json:"agent_performance"`
	FrictionPoints []string           `json:"friction_points"`
	Improvements   []string           `json:"improvements"`
}

// Overview holds pipeline run and task counts.
type Overview struct {
	DesignsCompleted  int `json:"designs_completed"`
	DesignsInProgress int `json:"designs_in_progress"`
	DesignsBlocked    int `json:"designs_blocked"`
	TasksCompleted    int `json:"tasks_completed"`
	TasksInProgress   int `json:"tasks_in_progress"`
	PRsMerged         int `json:"prs_merged"`
}

// GatePassRate holds first-try pass stats for a gate.
type GatePassRate struct {
	GateName     string  `json:"gate_name"`
	FirstTryPass int     `json:"first_try_pass"`
	TotalDesigns int     `json:"total_designs"`
	PassRate     float64 `json:"pass_rate"`
}

// GateFailureGroup groups failure reasons for a gate.
type GateFailureGroup struct {
	GateName string          `json:"gate_name"`
	Reasons  []FailureReason `json:"reasons"`
}

// FailureReason holds a single failure reason with count.
type FailureReason struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
	Total  int    `json:"total"`
}

// AgentPerformance holds agent-level stats.
type AgentPerformance struct {
	TasksCompleted int     `json:"tasks_completed"`
	AvgTaskMinutes float64 `json:"avg_task_minutes"`
	TasksWithPR    int     `json:"tasks_with_pr"`
	TasksNoPR      int     `json:"tasks_no_pr"`
	RebaseNeeded   int     `json:"rebase_needed"`
}

// Get gathers all pipeline insights data for a project. Caller is
// responsible for the pgx connection lifecycle; Get will not close it.
func Get(ctx context.Context, conn *pgx.Conn, project string) (*Stats, error) {
	stats := &Stats{
		Project:   project,
		Generated: time.Now(),
	}

	tableExists := true
	rows, err := conn.Query(ctx, `
		SELECT status, COUNT(*) FROM pipeline_runs WHERE project = $1 GROUP BY status
	`, project)
	if err != nil {
		if isTableMissing(err) {
			tableExists = false
		} else {
			return nil, fmt.Errorf("query pipeline_runs: %v", err)
		}
	}
	if tableExists && rows != nil {
		for rows.Next() {
			var status string
			var count int
			if err := rows.Scan(&status, &count); err != nil {
				rows.Close()
				return nil, err
			}
			switch status {
			case "completed", "done":
				stats.Overview.DesignsCompleted += count
			case "active", "in_progress":
				stats.Overview.DesignsInProgress += count
			case "blocked":
				stats.Overview.DesignsBlocked += count
			}
		}
		rows.Close()
	}

	if tableExists {
		taskRows, err := conn.Query(ctx, `
			SELECT pt.status, COUNT(*)
			FROM pipeline_tasks pt
			JOIN pipeline_runs pr ON pt.pipeline_id = pr.id
			WHERE pr.project = $1
			GROUP BY pt.status
		`, project)
		if err != nil {
			if !isTableMissing(err) {
				return nil, fmt.Errorf("query pipeline_tasks: %v", err)
			}
		} else {
			for taskRows.Next() {
				var status string
				var count int
				if err := taskRows.Scan(&status, &count); err != nil {
					taskRows.Close()
					return nil, err
				}
				switch status {
				case "completed", "done", "closed":
					stats.Overview.TasksCompleted += count
				case "in_progress", "active", "pending":
					stats.Overview.TasksInProgress += count
				}
			}
			taskRows.Close()
		}
	}

	prRow := conn.QueryRow(ctx, `
		SELECT COUNT(*) FROM shards
		WHERE project = $1 AND type = 'task' AND status = 'closed'
		  AND metadata->>'pr_url' IS NOT NULL
	`, project)
	if err := prRow.Scan(&stats.Overview.PRsMerged); err != nil {
		stats.Overview.PRsMerged = 0
	}

	if tableExists {
		gateRows, err := conn.Query(ctx, `
			SELECT gate_name,
				COUNT(*) FILTER (WHERE round = 1 AND verdict = 'pass') as first_try_pass,
				COUNT(DISTINCT (pipeline_id, gate_name)) as total_designs
			FROM pipeline_gates
			WHERE design_id IN (SELECT design_id FROM pipeline_runs WHERE project = $1)
			GROUP BY gate_name
			ORDER BY gate_name
		`, project)
		if err != nil {
			if !isTableMissing(err) {
				return nil, fmt.Errorf("query gate pass rates: %v", err)
			}
		} else {
			for gateRows.Next() {
				var g GatePassRate
				if err := gateRows.Scan(&g.GateName, &g.FirstTryPass, &g.TotalDesigns); err != nil {
					gateRows.Close()
					return nil, err
				}
				if g.TotalDesigns > 0 {
					g.PassRate = float64(g.FirstTryPass) / float64(g.TotalDesigns) * 100
				}
				stats.GateStats = append(stats.GateStats, g)
			}
			gateRows.Close()
		}
	}

	if tableExists {
		failRows, err := conn.Query(ctx, `
			SELECT gate_name, body FROM pipeline_gates
			WHERE design_id IN (SELECT design_id FROM pipeline_runs WHERE project = $1)
			  AND verdict != 'pass' AND body IS NOT NULL
			ORDER BY gate_name
		`, project)
		if err != nil {
			if !isTableMissing(err) {
				return nil, fmt.Errorf("query gate failures: %v", err)
			}
		} else {
			gateFailures := map[string]map[string]int{}
			gateTotals := map[string]int{}
			for failRows.Next() {
				var gateName string
				var body *string
				if err := failRows.Scan(&gateName, &body); err != nil {
					failRows.Close()
					return nil, err
				}
				gateTotals[gateName]++
				if body != nil {
					reasons := extractFailureReasons(*body)
					if gateFailures[gateName] == nil {
						gateFailures[gateName] = map[string]int{}
					}
					for _, r := range reasons {
						gateFailures[gateName][r]++
					}
				}
			}
			failRows.Close()

			for gate, reasons := range gateFailures {
				group := GateFailureGroup{GateName: gate}
				total := gateTotals[gate]
				for reason, count := range reasons {
					group.Reasons = append(group.Reasons, FailureReason{
						Reason: reason,
						Count:  count,
						Total:  total,
					})
				}
				stats.FailureReasons = append(stats.FailureReasons, group)
			}
		}
	}

	if tableExists {
		var avgMinutes *float64
		err = conn.QueryRow(ctx, `
			SELECT AVG(EXTRACT(EPOCH FROM (pt.updated_at - pt.created_at)) / 60)
			FROM pipeline_tasks pt
			JOIN pipeline_runs pr ON pt.pipeline_id = pr.id
			WHERE pr.project = $1 AND pt.status IN ('completed', 'done', 'closed')
		`, project).Scan(&avgMinutes)
		if err == nil && avgMinutes != nil {
			stats.AgentPerf.AvgTaskMinutes = *avgMinutes
		}
	}

	stats.AgentPerf.TasksCompleted = stats.Overview.TasksCompleted
	stats.AgentPerf.TasksWithPR = stats.Overview.PRsMerged
	if stats.Overview.TasksCompleted > stats.Overview.PRsMerged {
		stats.AgentPerf.TasksNoPR = stats.Overview.TasksCompleted - stats.Overview.PRsMerged
	}

	rebaseRow := conn.QueryRow(ctx, `
		SELECT COUNT(*) FROM shards
		WHERE project = $1 AND type = 'task'
		  AND (metadata->>'rebase_needed' = 'true' OR metadata->>'rebase_count' IS NOT NULL)
	`, project)
	if err := rebaseRow.Scan(&stats.AgentPerf.RebaseNeeded); err != nil {
		stats.AgentPerf.RebaseNeeded = 0
	}

	stats.FrictionPoints = detectFrictionPoints(stats)
	stats.Improvements = suggestImprovements(stats)

	return stats, nil
}

func extractFailureReasons(body string) []string {
	var reasons []string
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			reason := strings.TrimPrefix(line, "- ")
			reason = strings.TrimPrefix(reason, "* ")
			reason = strings.TrimSpace(reason)
			if reason != "" && len(reason) < 200 {
				reasons = append(reasons, reason)
			}
		}
	}
	return reasons
}

func detectFrictionPoints(stats *Stats) []string {
	var points []string
	if stats.AgentPerf.RebaseNeeded > 0 {
		points = append(points, fmt.Sprintf(
			"Squash merge + dependent branches -> manual rebase (%d PRs affected)",
			stats.AgentPerf.RebaseNeeded))
	}
	if stats.AgentPerf.TasksNoPR > 0 {
		pct := 0.0
		if stats.AgentPerf.TasksCompleted > 0 {
			pct = float64(stats.AgentPerf.TasksNoPR) / float64(stats.AgentPerf.TasksCompleted) * 100
		}
		points = append(points, fmt.Sprintf(
			"Agents not creating PRs -> %.0f%% of completed tasks missing PR", pct))
	}
	for _, g := range stats.GateStats {
		if g.TotalDesigns > 0 && g.PassRate < 50 {
			points = append(points, fmt.Sprintf(
				"%s gate has %.0f%% first-try pass rate -- review gate criteria",
				g.GateName, g.PassRate))
		}
	}
	return points
}

func suggestImprovements(stats *Stats) []string {
	var improvements []string
	for _, fg := range stats.FailureReasons {
		for _, r := range fg.Reasons {
			reasonLower := strings.ToLower(r.Reason)
			if strings.Contains(reasonLower, "code location") {
				improvements = append(improvements, fmt.Sprintf(
					"create-design.md: emphasize code locations (%d/%d %s failures mention this)",
					r.Count, r.Total, fg.GateName))
			}
			if strings.Contains(reasonLower, "integration test") {
				improvements = append(improvements, fmt.Sprintf(
					"Decomposition: always verify integration tests are included (%d/%d %s failures)",
					r.Count, r.Total, fg.GateName))
			}
		}
	}
	if stats.AgentPerf.TasksNoPR > 0 {
		improvements = append(improvements, "Task completion: ensure task-complete hook always runs to create PRs")
	}
	return improvements
}

func isTableMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "does not exist") || strings.Contains(msg, "relation") || strings.Contains(msg, "table not found")
}
