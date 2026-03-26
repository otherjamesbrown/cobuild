package cmd

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run <work-item-id>",
	Short: "Submit a work item for autonomous processing by the poller",
	Long: `Marks an existing pipeline run as autonomous, so the poller will
process it through all remaining phases without manual intervention.

If the work item doesn't have a pipeline run yet, initialises one
in autonomous mode.

Use this when you want to hand a design or bug to CoBuild and walk away.
The poller will dispatch agents, wait for completion, advance phases,
merge PRs, and run retrospectives.`,
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild run pf-design-123         # hand to poller for full processing
  cobuild run pf-bug-456            # investigate → fix → review → done`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		if cbStore == nil {
			return fmt.Errorf("no store configured")
		}

		// Check if pipeline run exists
		run, err := cbStore.GetRun(ctx, id)
		if err != nil {
			// No pipeline run — init one in autonomous mode
			fmt.Printf("No pipeline run found — initialising in autonomous mode...\n")

			// Determine start phase
			startPhase := "design"
			if conn != nil {
				item, err := conn.Get(ctx, id)
				if err == nil {
					repoRoot := findRepoRoot()
					pCfg, _ := config.LoadConfig(repoRoot)
					if pCfg != nil {
						sp := pCfg.StartPhaseForType(item.Type)
						if sp != "" {
							startPhase = sp
						}
					}
					fmt.Printf("Work item type: %s → start phase: %s\n", item.Type, startPhase)
				}
			}

			run, err = cbStore.CreateRunWithMode(ctx, id, projectName, startPhase, "autonomous")
			if err != nil {
				return fmt.Errorf("init pipeline: %w", err)
			}
			fmt.Printf("Initialised pipeline on %s (autonomous)\n", id)
			fmt.Printf("  Phase: %s\n", run.CurrentPhase)
		} else {
			// Pipeline exists — switch to autonomous
			if err := cbStore.SetRunMode(ctx, id, "autonomous"); err != nil {
				return fmt.Errorf("set autonomous mode: %w", err)
			}
			fmt.Printf("Pipeline %s switched to autonomous mode\n", id)
			fmt.Printf("  Phase: %s\n", run.CurrentPhase)
		}

		printNextStep(id, "", "run")

		return nil
	},
}
