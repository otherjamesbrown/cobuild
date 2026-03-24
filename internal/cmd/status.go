package cmd

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all active pipelines and their state",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if cbClient == nil {
			return fmt.Errorf("no client configured (need database connection)")
		}

		runs, err := cbClient.ListPipelineRuns(ctx)
		if err != nil {
			return fmt.Errorf("list pipeline runs: %w", err)
		}

		if len(runs) == 0 {
			fmt.Println("No active pipelines.")
			return nil
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(runs)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("%-12s %-14s %-10s %-6s %s\n", "ID", "PHASE", "STATUS", "TASKS", "LAST ACTIVITY")
		fmt.Printf("%-12s %-14s %-10s %-6s %s\n", "----", "-----", "------", "-----", "-------------")
		for _, r := range runs {
			taskSummary := "-"
			if r.TaskTotal > 0 {
				taskSummary = fmt.Sprintf("%d/%d", r.TaskDone, r.TaskTotal)
			}

			lastActivity := client.TimeAgo(r.LastProgress)

			fmt.Printf("%-12s %-14s %-10s %-6s %s\n",
				r.DesignID,
				r.Phase,
				r.Status,
				taskSummary,
				lastActivity,
			)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
