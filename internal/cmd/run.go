package cmd

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
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
	Args: cobra.ExactArgs(1),
	Example: `  cobuild run pf-design-123         # hand to poller for full processing
  cobuild run pf-bug-456            # investigate → fix → review → done
  cobuild run pf-design-123 --inline # run in the foreground via orchestrate`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]
		inline, _ := cmd.Flags().GetBool("inline")

		if cbStore == nil {
			return fmt.Errorf("no store configured")
		}

		// Check if pipeline run exists
		run, err := cbStore.GetRun(ctx, id)
		if err != nil {
			modeLabel := "autonomous"
			if inline {
				modeLabel = "manual"
			}
			fmt.Printf("No pipeline run found — initialising in %s mode...\n", modeLabel)

			// Determine start phase
			startPhase := domain.PhaseDesign
			if conn != nil {
				item, err := conn.Get(ctx, id)
				if err == nil {
					repoRoot := findRepoRoot()
					pCfg, _ := config.LoadConfig(repoRoot)
					bootstrap, resolveErr := pipelinestate.ResolveBootstrap(item, pCfg)
					if resolveErr != nil {
						return fmt.Errorf("resolve pipeline bootstrap for %s: %w", id, resolveErr)
					}
					startPhase = bootstrap.StartPhase
					fmt.Printf("Work item type: %s → start phase: %s\n", item.Type, startPhase)
				}
			}

			runMode := "autonomous"
			if inline {
				runMode = "manual"
			}
			run, err = cbStore.CreateRunWithMode(ctx, id, projectName, startPhase, runMode)
			if err != nil {
				return fmt.Errorf("init pipeline: %w", err)
			}
			fmt.Printf("Initialised pipeline on %s (%s)\n", id, runMode)
			fmt.Printf("  Phase: %s\n", run.CurrentPhase)
		} else {
			runMode := "autonomous"
			if inline {
				runMode = "manual"
			}
			// Pipeline exists — switch mode to match the requested driver.
			if err := cbStore.SetRunMode(ctx, id, runMode); err != nil {
				return fmt.Errorf("set %s mode: %w", runMode, err)
			}
			fmt.Printf("Pipeline %s switched to %s mode\n", id, runMode)
			fmt.Printf("  Phase: %s\n", run.CurrentPhase)
		}

		if inline {
			fmt.Printf("Running %s in the foreground via `cobuild orchestrate %s`.\n", id, id)
			return orchestrateCmd.RunE(orchestrateCmd, []string{id})
		}

		printNextStep(id, "", "run")

		return nil
	},
}

func init() {
	runCmd.Flags().Bool("inline", false, "Run in the foreground via `cobuild orchestrate` instead of handing off to the poller")
}
