package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/merge"
	"github.com/spf13/cobra"
)

var mergeDesignCmd = &cobra.Command{
	Use:   "merge-design <design-id>",
	Short: "Analyse conflicts and merge all task PRs for a design",
	Long: `Analyses all task branches for a design, detects file conflicts and
superseded tasks, generates a merge plan, and optionally executes it.

The plan orders merges by wave, handles partial merges for superseded
files, skips fully superseded tasks, and runs tests after each merge.`,
	Args: cobra.ExactArgs(1),
	Example: `  cobuild merge-design pf-6e38e9 --dry-run    # show plan only
  cobuild merge-design pf-6e38e9 --auto       # execute without confirmation
  cobuild merge-design pf-6e38e9              # show plan, ask to execute`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		designID := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		auto, _ := cmd.Flags().GetBool("auto")
		skipTests, _ := cmd.Flags().GetBool("skip-tests")

		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		// Get all child tasks
		edges, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
		if err != nil {
			return fmt.Errorf("get child tasks: %w", err)
		}

		if len(edges) == 0 {
			return fmt.Errorf("no child tasks found for %s", designID)
		}

		// Build branch info from tasks
		var tasks []merge.BranchInfo
		for _, e := range edges {
			item, err := conn.Get(ctx, e.ItemID)
			if err != nil {
				continue
			}
			if item.Type != "task" && item.Type != "bug" {
				continue
			}
			if item.Status != "closed" && item.Status != "needs-review" {
				fmt.Printf("  Skipping %s (status: %s, not ready for merge)\n", item.ID, item.Status)
				continue
			}

			wave := 0
			if item.Metadata != nil {
				if w, ok := item.Metadata["wave"]; ok {
					if wf, ok := w.(float64); ok {
						wave = int(wf)
					}
				}
			}

			pr := ""
			if item.Metadata != nil {
				if p, ok := item.Metadata["pr_url"]; ok {
					pr = fmt.Sprintf("%v", p)
				}
			}

			tasks = append(tasks, merge.BranchInfo{
				TaskID: item.ID,
				Branch: item.ID, // convention: branch name = task ID
				Wave:   wave,
				PR:     pr,
			})
		}

		if len(tasks) == 0 {
			return fmt.Errorf("no mergeable tasks found")
		}

		// Determine repo root
		repoRoot := ""
		targetRepo, _ := conn.GetMetadata(ctx, tasks[0].TaskID, "repo")
		if targetRepo != "" {
			repoRoot, _ = config.RepoForProject(targetRepo)
		}
		if repoRoot == "" {
			repoRoot, _ = config.RepoForProject(projectName)
		}
		if repoRoot == "" {
			repoRoot = findRepoRoot()
		}

		fmt.Printf("Analysing %d task branches for %s in %s...\n\n", len(tasks), designID, repoRoot)

		// Phase 1: Conflict analysis
		cm, err := merge.AnalyseBranches(ctx, repoRoot, tasks)
		if err != nil {
			return fmt.Errorf("conflict analysis: %w", err)
		}

		if cm.Clean {
			fmt.Println("No file conflicts detected — all branches touch different files.")
		} else {
			fmt.Printf("Found %d file conflict(s):\n", len(cm.Conflicts))
			for _, c := range cm.Conflicts {
				scope := "cross-wave"
				if c.SameWave {
					scope = "same-wave"
				}
				fmt.Printf("  %-50s  %s  [%s]\n", c.File, strings.Join(c.Branches, ", "), scope)
			}
			fmt.Println()
		}

		// Phase 2: Supersession detection
		sr, err := merge.DetectSupersessions(ctx, repoRoot, cm)
		if err != nil {
			return fmt.Errorf("supersession detection: %w", err)
		}

		if len(sr.FullySuperseded) > 0 {
			fmt.Printf("Fully superseded tasks (will skip): %s\n", strings.Join(sr.FullySuperseded, ", "))
		}
		if len(sr.PartiallySuperseded) > 0 {
			fmt.Println("Partially superseded tasks (will cherry-pick):")
			for tid, info := range sr.PartiallySuperseded {
				fmt.Printf("  %s — include: %s, skip: %s\n", tid, strings.Join(info.IncludeFiles, ", "), strings.Join(info.SkipFiles, ", "))
			}
		}
		if len(sr.FullySuperseded) > 0 || len(sr.PartiallySuperseded) > 0 {
			fmt.Println()
		}

		// Phase 3: Generate plan
		plan := merge.GeneratePlan(designID, cm, sr)

		if outputFormat == "json" {
			s, _ := client.FormatJSON(plan)
			fmt.Println(s)
			return nil
		}

		fmt.Println(merge.FormatPlan(plan))

		if dryRun {
			return nil
		}

		// Confirm
		if !auto {
			fmt.Print("Execute this merge plan? (yes/no): ")
			var answer string
			fmt.Scanln(&answer)
			if !strings.HasPrefix(strings.ToLower(answer), "y") {
				fmt.Println("Aborted.")
				return nil
			}
		}

		// Phase 4: Execute
		var testCmds []string
		if !skipTests {
			pCfg, _ := config.LoadConfig(repoRoot)
			if pCfg != nil {
				testCmds = append(testCmds, pCfg.Test...)
			}
		}

		results, err := merge.ExecutePlan(ctx, repoRoot, plan, testCmds, false)
		if err != nil {
			fmt.Printf("\nMerge stopped: %v\n", err)
		}

		// Summary
		fmt.Printf("\n=== Merge Summary ===\n")
		merged := 0
		skipped := 0
		failed := 0
		for _, r := range results {
			status := "OK"
			if !r.Success {
				status = "FAILED"
				failed++
			} else if r.Action == "skip" {
				skipped++
			} else {
				merged++
			}
			fmt.Printf("  %-8s %-12s %s\n", status, r.TaskID, r.Commit)
		}
		fmt.Printf("\nMerged: %d | Skipped: %d | Failed: %d\n", merged, skipped, failed)

		// Advance pipeline if all succeeded
		if failed == 0 && err == nil && cbStore != nil {
			if advErr := cbStore.UpdateRunPhase(ctx, designID, "done"); advErr == nil {
				fmt.Println("Pipeline advanced to done phase.")
				if err := cbStore.UpdateRunStatus(ctx, designID, "completed"); err != nil {
					fmt.Printf("  Warning: failed to mark completed: %v\n", err)
				}
			}
		}

		return err
	},
}

func init() {
	mergeDesignCmd.Flags().Bool("dry-run", false, "Show merge plan without executing")
	mergeDesignCmd.Flags().Bool("auto", false, "Execute without confirmation (autonomous mode)")
	mergeDesignCmd.Flags().Bool("skip-tests", false, "Skip post-merge tests (dangerous)")
	rootCmd.AddCommand(mergeDesignCmd)
}
