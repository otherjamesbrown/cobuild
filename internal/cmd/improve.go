package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/cliutil"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/insights"
	"github.com/spf13/cobra"
)

// ImprovementAction describes a suggested change to a skill or config file.
type ImprovementAction struct {
	File        string `json:"file"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
	Diff        string `json:"diff,omitempty"`
}

var improveCmd = &cobra.Command{
	Use:   "improve",
	Short: "Suggest pipeline improvements based on execution patterns",
	Long: `Analyzes pipeline execution data and proposes specific changes to
skills, config, and process files based on observed patterns.

Run with --apply to auto-apply the changes.`,
	Example: `  cobuild improve
  cobuild improve --apply
  cobuild improve -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		apply, _ := cmd.Flags().GetBool("apply")

		project := projectName
		repoRoot, _ := config.RepoForProject(project)
		if repoRoot == "" {
			cwd, _ := os.Getwd()
			repoRoot, _ = cliutil.GitRepoRoot(cwd)
		}

		pCfg, _ := config.LoadConfig(repoRoot)
		if pCfg == nil {
			pCfg = config.DefaultConfig()
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
			return fmt.Errorf("failed to get insights: %v", err)
		}

		actions := generateImprovements(stats, pCfg, repoRoot)

		if outputFormat == "json" {
			s, _ := cliutil.FormatJSON(actions)
			fmt.Println(s)
			return nil
		}

		if len(actions) == 0 {
			fmt.Println("No improvements suggested -- pipeline is running well.")
			return nil
		}

		fmt.Printf("Pipeline Improvements -- %s\n", project)
		fmt.Printf("%d suggestions based on execution data\n\n", len(actions))

		for i, a := range actions {
			fmt.Printf("%d. [%s] %s\n", i+1, a.Type, a.Description)
			fmt.Printf("   Reason: %s\n", a.Reason)
			if a.Diff != "" {
				fmt.Printf("   File: %s\n", a.File)
				if apply {
					if err := applyImprovement(a, repoRoot); err != nil {
						fmt.Printf("   FAILED: %v\n", err)
					} else {
						fmt.Printf("   APPLIED\n")
					}
				} else {
					fmt.Printf("   Change:\n")
					for _, line := range strings.Split(a.Diff, "\n") {
						fmt.Printf("     %s\n", line)
					}
				}
			}
			fmt.Println()
		}

		if !apply && hasApplyable(actions) {
			fmt.Println("Run with --apply to auto-apply these changes.")
		}

		return nil
	},
}

func generateImprovements(stats *insights.Stats, cfg *config.Config, repoRoot string) []ImprovementAction {
	var actions []ImprovementAction

	for _, gs := range stats.GateStats {
		if gs.GateName == "readiness-review" && gs.PassRate < 60 {
			actions = append(actions, ImprovementAction{
				File:        filepath.Join("skills", "create-design.md"),
				Type:        "skill",
				Description: "Strengthen code location requirements in create-design skill",
				Reason:      fmt.Sprintf("readiness-review first-try pass rate is %.0f%% -- most failures are missing code locations", gs.PassRate),
			})
		}
	}

	if cfg != nil {
		for _, phase := range cfg.Phases {
			if phase.Name == "review" && phase.Model == "" {
				actions = append(actions, ImprovementAction{
					File:        filepath.Join(".cobuild", "pipeline.yaml"),
					Type:        "config",
					Description: "Set review phase model to haiku",
					Reason:      "Review is a judgment task -- haiku is sufficient and cheaper",
				})
			}
		}
		if cfg.Dispatch.DefaultModel == "" {
			actions = append(actions, ImprovementAction{
				File:        filepath.Join(".cobuild", "pipeline.yaml"),
				Type:        "config",
				Description: "Set default model to sonnet",
				Reason:      "No default model configured -- agents may use opus unnecessarily",
			})
		}
	}

	if cfg != nil && cfg.Monitoring.StallTimeout == "" {
		actions = append(actions, ImprovementAction{
			File:        filepath.Join(".cobuild", "pipeline.yaml"),
			Type:        "config",
			Description: "Enable health monitoring for dispatched agents",
			Reason:      "No monitoring configured -- stalled/crashed agents won't be detected",
		})
	}

	skillsDir := "skills"
	if cfg != nil && cfg.SkillsDir != "" {
		skillsDir = cfg.SkillsDir
	}
	playbookPath := filepath.Join(repoRoot, skillsDir, "m-playbook.md")
	if _, err := os.Stat(playbookPath); os.IsNotExist(err) {
		actions = append(actions, ImprovementAction{
			Type:        "process",
			Description: "Initialize pipeline skills in this repo",
			Reason:      fmt.Sprintf("No %s/m-playbook.md found -- pipeline skills are not portable to this repo", skillsDir),
			Diff:        "Run: cobuild init-skills",
		})
	}

	return actions
}

func applyImprovement(a ImprovementAction, repoRoot string) error {
	if a.Type == "skill" {
		return fmt.Errorf("skill changes need human review -- not auto-applying")
	}
	if a.Diff == "" {
		return fmt.Errorf("no diff to apply")
	}
	return fmt.Errorf("auto-apply not yet implemented -- apply manually")
}

func hasApplyable(actions []ImprovementAction) bool {
	for _, a := range actions {
		if a.Diff != "" {
			return true
		}
	}
	return false
}

func init() {
	improveCmd.Flags().Bool("apply", false, "Auto-apply suggested changes")
	rootCmd.AddCommand(improveCmd)
}
