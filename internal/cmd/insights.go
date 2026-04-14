package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/cliutil"
	"github.com/otherjamesbrown/cobuild/internal/insights"
	"github.com/spf13/cobra"
)

var insightsCmd = &cobra.Command{
	Use:   "insights",
	Short: "Analyze pipeline execution data and produce a report",
	Long: `Analyzes pipeline gate pass/fail rates, design completion times,
task statuses, and agent performance to produce an insights report.`,
	Example: `  cobuild insights
  cobuild insights --project penfold
  cobuild insights -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		project := projectName
		if p, _ := cmd.Flags().GetString("project"); p != "" {
			project = p
		}

		if storeDSN == "" {
			return fmt.Errorf("no database connection — set up ~/.cobuild/config.yaml or COBUILD_* env vars")
		}
		dbConn, err := cliutil.ConnectPostgres(ctx, storeDSN)
		if err != nil {
			return fmt.Errorf("connect: %v", err)
		}
		defer dbConn.Close(ctx)

		stats, err := insights.Get(ctx, dbConn, project)
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, _ := cliutil.FormatJSON(stats)
			fmt.Println(s)
			return nil
		}

		if outputFormat == "yaml" {
			s, _ := cliutil.FormatYAML(stats)
			fmt.Print(s)
			return nil
		}

		printInsightsText(stats)
		return nil
	},
}

func printInsightsText(stats *insights.Stats) {
	fmt.Printf("Pipeline Insights -- %s\n", stats.Project)
	fmt.Printf("Generated: %s\n", stats.Generated.Format(time.DateOnly))
	fmt.Println()

	fmt.Println("OVERVIEW")
	fmt.Printf("  Designs:  %d completed, %d in progress, %d blocked\n",
		stats.Overview.DesignsCompleted, stats.Overview.DesignsInProgress, stats.Overview.DesignsBlocked)
	fmt.Printf("  Tasks:    %d completed, %d in progress\n",
		stats.Overview.TasksCompleted, stats.Overview.TasksInProgress)
	fmt.Printf("  PRs:      %d merged\n", stats.Overview.PRsMerged)
	fmt.Println()

	fmt.Println("GATE PASS RATES")
	if len(stats.GateStats) == 0 {
		fmt.Println("  (no gate data)")
	} else {
		for _, g := range stats.GateStats {
			fmt.Printf("  %-25s %d/%d first-try pass (%.0f%%)\n",
				g.GateName+":", g.FirstTryPass, g.TotalDesigns, g.PassRate)
		}
	}
	fmt.Println()

	fmt.Println("COMMON FAILURE REASONS")
	if len(stats.FailureReasons) == 0 {
		fmt.Println("  (no failure data)")
	} else {
		for _, fg := range stats.FailureReasons {
			fmt.Printf("  %s failures:\n", fg.GateName)
			for _, r := range fg.Reasons {
				fmt.Printf("    - %s (%d/%d failures)\n", r.Reason, r.Count, r.Total)
			}
		}
	}
	fmt.Println()

	fmt.Println("AGENT PERFORMANCE")
	fmt.Printf("  Tasks completed:     %d\n", stats.AgentPerf.TasksCompleted)
	if stats.AgentPerf.TasksCompleted > 0 {
		prPct := 0.0
		if stats.AgentPerf.TasksWithPR > 0 {
			prPct = float64(stats.AgentPerf.TasksWithPR) / float64(stats.AgentPerf.TasksCompleted) * 100
		}
		fmt.Printf("  PRs created:         %d (%.0f%%)\n", stats.AgentPerf.TasksWithPR, prPct)
		if stats.AgentPerf.TasksNoPR > 0 {
			fmt.Printf("  PRs missing:         %d\n", stats.AgentPerf.TasksNoPR)
		}
	}
	if stats.AgentPerf.AvgTaskMinutes > 0 {
		fmt.Printf("  Avg task time:       %.0f min\n", stats.AgentPerf.AvgTaskMinutes)
	}
	if stats.AgentPerf.RebaseNeeded > 0 {
		fmt.Printf("  Rebase needed:       %d PRs\n", stats.AgentPerf.RebaseNeeded)
	}
	fmt.Println()

	fmt.Println("FRICTION POINTS")
	if len(stats.FrictionPoints) == 0 {
		fmt.Println("  (none detected)")
	} else {
		for i, fp := range stats.FrictionPoints {
			fmt.Printf("  %d. %s\n", i+1, fp)
		}
	}
	fmt.Println()

	fmt.Println("SUGGESTED IMPROVEMENTS")
	if len(stats.Improvements) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, imp := range stats.Improvements {
			fmt.Printf("  - %s\n", imp)
		}
	}
}

func init() {
	insightsCmd.Flags().String("project", "", "Filter by project (default: from config)")
	rootCmd.AddCommand(insightsCmd)
}
