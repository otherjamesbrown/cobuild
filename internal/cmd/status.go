package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/client"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all active pipelines and their state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if cbStore == nil {
			return fmt.Errorf("no store configured (need database connection)")
		}

		runs, err := cbStore.ListRuns(ctx, projectName)
		if err != nil {
			return fmt.Errorf("list pipeline runs: %w", err)
		}

		if len(runs) == 0 {
			fmt.Println("No active pipelines.")
			return nil
		}

		rows := make([]resolvedStatusRow, 0, len(runs))
		for _, run := range runs {
			state, resolveErr := pipelinestate.Resolve(ctx, run.DesignID)
			rows = append(rows, resolvedStatusRow{
				Run:        run,
				State:      state,
				ResolveErr: resolveErr,
			})
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(rows)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("%-12s %-14s %-10s %-12s %-6s %s\n", "ID", "PHASE", "STATUS", "HEALTH", "TASKS", "LAST ACTIVITY")
		fmt.Printf("%-12s %-14s %-10s %-12s %-6s %s\n", "----", "-----", "------", "------", "-----", "-------------")
		for _, row := range rows {
			fmt.Printf("%-12s %-14s %-10s %-12s %-6s %s\n",
				row.Run.DesignID,
				row.phase(),
				row.status(),
				row.health(),
				row.taskSummary(),
				client.TimeAgo(row.Run.LastProgress),
			)
			for _, line := range row.details() {
				fmt.Printf("  %s\n", line)
			}
		}
		return nil
	},
}

type resolvedStatusRow struct {
	Run        store.PipelineRunStatus      `json:"run"`
	State      *pipelinestate.PipelineState `json:"state,omitempty"`
	ResolveErr error                        `json:"-"`
}

func (r resolvedStatusRow) phase() string {
	if r.State != nil && r.State.Run != nil && r.State.Run.Phase != "" {
		return r.State.Run.Phase
	}
	return r.Run.Phase
}

func (r resolvedStatusRow) status() string {
	if r.State != nil && r.State.Run != nil && r.State.Run.Status != "" {
		return r.State.Run.Status
	}
	return r.Run.Status
}

func (r resolvedStatusRow) health() string {
	if r.State != nil && r.State.Health != "" {
		return string(r.State.Health)
	}
	if r.ResolveErr != nil {
		return "ERROR"
	}
	return "-"
}

func (r resolvedStatusRow) taskSummary() string {
	if r.Run.TaskTotal == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", r.Run.TaskDone, r.Run.TaskTotal)
}

func (r resolvedStatusRow) details() []string {
	lines := []string{}
	if r.ResolveErr != nil && !(r.State != nil && r.State.Health == pipelinestate.HealthMissing) {
		lines = append(lines, fmt.Sprintf("resolver error: %v", r.ResolveErr))
	}
	if r.State == nil {
		return lines
	}
	for _, inconsistency := range r.State.Inconsistencies {
		lines = append(lines, fmt.Sprintf("! %s", inconsistency))
	}
	for _, sourceErr := range r.State.SourceErrors {
		lines = append(lines, fmt.Sprintf("? %s: %s", sourceErr.Source, sourceErr.Message))
	}
	if len(lines) == 0 && r.State.Health != pipelinestate.HealthOK {
		lines = append(lines, fmt.Sprintf("health=%s", r.State.Health))
	}
	return dedupeStrings(lines)
}

func dedupeStrings(items []string) []string {
	if len(items) < 2 {
		return items
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
