package cmd

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

var retroCmd = &cobra.Command{
	Use:   "retro <design-id>",
	Short: "Run a pipeline retrospective on a completed design",
	Long: `Gathers pipeline data (gate history, task durations, issues encountered)
and records a retrospective gate verdict. This is the final pipeline phase.

In manual mode, run this after all tasks are merged.
In autonomous mode, the poller triggers this automatically.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if cbStore == nil {
			return fmt.Errorf("no store configured (need database connection)")
		}

		designID := args[0]

		// Get pipeline state
		run, err := cbStore.GetRun(ctx, designID)
		if err != nil {
			return fmt.Errorf("get pipeline: %w", err)
		}

		if run.CurrentPhase != "done" && run.CurrentPhase != "review" {
			fmt.Printf("Warning: pipeline is in %q phase (expected: done).\n", run.CurrentPhase)
		}

		// Gather data
		gates, err := cbStore.GetGateHistory(ctx, designID)
		if err != nil {
			return fmt.Errorf("get gate history: %w", err)
		}

		if outputFormat == "json" {
			data := map[string]any{
				"design_id": designID,
				"phase":     run.CurrentPhase,
				"status":    run.Status,
				"gates":     gates,
			}
			s, _ := client.FormatJSON(data)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Retrospective: %s\n", designID)
		fmt.Printf("  Phase:  %s\n", run.CurrentPhase)
		fmt.Printf("  Status: %s\n\n", run.Status)

		if len(gates) > 0 {
			fmt.Println("Gate History:")
			fmt.Printf("  %-24s %-8s %-8s %s\n", "GATE", "ROUND", "VERDICT", "BODY")
			fmt.Printf("  %-24s %-8s %-8s %s\n", "----", "-----", "-------", "----")
			for _, g := range gates {
				bodyStr := ""
				if g.Body != nil {
					bodyStr = client.Truncate(*g.Body, 50)
				}
				fmt.Printf("  %-24s %-8d %-8s %s\n", g.GateName, g.Round, g.Verdict, bodyStr)
			}
		}

		// Record the retrospective gate
		body, _ := cmd.Flags().GetString("body")
		if body != "" {
			repoRoot := findRepoRoot()
			pCfg, _ := config.LoadConfig(repoRoot)
			if pCfg == nil {
				pCfg = config.DefaultConfig()
			}

			result, err := RecordGateVerdict(ctx, conn, cbStore, designID, "retrospective", "pass", body, 0, pCfg)
			if err != nil {
				return fmt.Errorf("record retrospective: %w", err)
			}

			fmt.Printf("\nRetrospective recorded: %s\n", result.ReviewShardID)

			// Mark pipeline as completed
			if err := cbStore.UpdateRunStatus(ctx, designID, "completed"); err != nil {
				fmt.Printf("Warning: failed to mark pipeline as completed: %v\n", err)
			} else {
				fmt.Println("Pipeline marked as completed.")
			}
		} else {
			fmt.Println("\nTo record the retrospective: cobuild retro <design-id> --body \"<findings>\"")
		}

		return nil
	},
}

func init() {
	retroCmd.Flags().String("body", "", "Retrospective findings and lessons learned")
	rootCmd.AddCommand(retroCmd)
}
