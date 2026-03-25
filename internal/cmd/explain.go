package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// skillFrontmatter holds the human-readable fields from a skill's YAML frontmatter.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

var explainCmd = &cobra.Command{
	Use:   "explain",
	Short: "Show the full pipeline in human-readable form",
	Long: `Reads the pipeline config, skills, and deploy settings and presents
the complete pipeline as a developer would understand it — what happens
at each phase, what gates enforce, what agents do, and what deploys.

This is the "what does my pipeline actually do?" command.`,
	Example: `  cobuild explain               # full pipeline overview
  cobuild explain --phase design # explain one phase`,
	RunE: func(cmd *cobra.Command, args []string) error {
		phaseFilter, _ := cmd.Flags().GetString("phase")

		repoRoot := findRepoRoot()
		pCfg, err := config.LoadConfig(repoRoot)
		if err != nil {
			return fmt.Errorf("load config: %v", err)
		}
		if pCfg == nil {
			pCfg = config.DefaultConfig()
		}

		skillsDir := pCfg.SkillsDir
		if skillsDir == "" {
			skillsDir = "skills"
		}
		skillsBase := filepath.Join(repoRoot, skillsDir)

		fmt.Printf("# Pipeline: %s\n\n", projectName)

		// Workflows
		fmt.Println("## Workflows\n")
		if pCfg.Workflows != nil {
			for name, wf := range pCfg.Workflows {
				fmt.Printf("**%s:** %s\n", name, strings.Join(wf.Phases, " → "))
			}
		} else {
			fmt.Println("**default:** design → decompose → implement → review → done")
		}
		fmt.Println()

		// Walk through each phase
		fmt.Println("## Phases\n")

		phaseOrder := []string{"design", "decompose", "investigate", "implement", "review", "done"}
		if pCfg.Workflows != nil {
			// Collect all unique phases from workflows
			seen := make(map[string]bool)
			var ordered []string
			for _, wf := range pCfg.Workflows {
				for _, p := range wf.Phases {
					if !seen[p] {
						seen[p] = true
						ordered = append(ordered, p)
					}
				}
			}
			if len(ordered) > 0 {
				phaseOrder = ordered
			}
		}

		for _, phaseName := range phaseOrder {
			if phaseFilter != "" && phaseName != phaseFilter {
				continue
			}

			fmt.Printf("### %s\n\n", strings.Title(phaseName))

			// Which workflows include this phase
			var usedBy []string
			if pCfg.Workflows != nil {
				for name, wf := range pCfg.Workflows {
					for _, p := range wf.Phases {
						if p == phaseName {
							usedBy = append(usedBy, name)
							break
						}
					}
				}
			}
			if len(usedBy) > 0 {
				fmt.Printf("Used by: %s\n\n", strings.Join(usedBy, ", "))
			}

			// Phase config
			if pCfg.Phases != nil {
				if phase, ok := pCfg.Phases[phaseName]; ok {
					if phase.Gate != "" {
						fmt.Printf("**Gate:** %s\n", phase.Gate)
					}
					if phase.Skill != "" {
						fmt.Printf("**Skill:** %s\n", phase.Skill)
						// Read skill description
						desc := readSkillDescription(skillsBase, phase.Skill)
						if desc != "" {
							fmt.Printf("  → %s\n", desc)
						}
					}
					if phase.StallCheck != "" {
						fmt.Printf("**Stall check:** %s\n", phase.StallCheck)
						desc := readSkillDescription(skillsBase, phase.StallCheck)
						if desc != "" {
							fmt.Printf("  → %s\n", desc)
						}
					}
					if phase.Model != "" {
						fmt.Printf("**Model:** %s\n", phase.Model)
					}
				}
			}

			// Phase-specific context layers
			if len(pCfg.Context.Layers) > 0 {
				var phaseLayers []string
				for _, l := range pCfg.Context.Layers {
					if l.When == "phase:"+phaseName {
						phaseLayers = append(phaseLayers, fmt.Sprintf("%s (%s)", l.Name, l.Source))
					}
				}
				if len(phaseLayers) > 0 {
					fmt.Printf("**Context:** %s\n", strings.Join(phaseLayers, ", "))
				}
			}

			// Phase-specific description
			switch phaseName {
			case "design":
				fmt.Println("\nA design is evaluated for completeness and implementability.")
				fmt.Println("The gate checks 5 criteria: problem stated, user identified, success criteria,")
				fmt.Println("scope boundaries, and links to parent. Plus an implementability check.")
			case "decompose":
				fmt.Println("\nThe design is broken into discrete tasks with dependency ordering.")
				fmt.Println("Tasks are grouped into waves (wave 1 has no blockers, wave 2 depends on wave 1).")
			case "investigate":
				fmt.Println("\nBugs are investigated before implementation. A read-only agent analyses")
				fmt.Println("root cause, affected areas, and fragility. Produces a fix specification")
				fmt.Println("that the implementing agent works from.")
			case "implement":
				fmt.Println("\nAgents are dispatched into isolated git worktrees, one per task.")
				fmt.Println("Each gets a CLAUDE.md with task spec, design context, and project architecture.")
				if pCfg.Dispatch.MaxConcurrent > 0 {
					fmt.Printf("Max concurrent: %d agents\n", pCfg.Dispatch.MaxConcurrent)
				}
				if pCfg.Dispatch.DefaultModel != "" {
					fmt.Printf("Default model: %s\n", pCfg.Dispatch.DefaultModel)
				}
			case "review":
				fmt.Println("\nPRs are reviewed before merging.")
				if pCfg.Review.Strategy != "" {
					fmt.Printf("Strategy: %s\n", pCfg.Review.Strategy)
				}
				if len(pCfg.Review.ExternalReviewers) > 0 {
					fmt.Printf("External reviewers: %s\n", strings.Join(pCfg.Review.ExternalReviewers, ", "))
				}
			case "done":
				fmt.Println("\nA retrospective captures lessons learned and feeds them back into skills.")
			}
			fmt.Println()
		}

		// Build & test
		if len(pCfg.Build) > 0 || len(pCfg.Test) > 0 {
			fmt.Println("## Build & Test\n")
			if len(pCfg.Build) > 0 {
				fmt.Printf("**Build:** `%s`\n", strings.Join(pCfg.Build, " && "))
			}
			if len(pCfg.Test) > 0 {
				fmt.Printf("**Test:** `%s`\n", strings.Join(pCfg.Test, " && "))
			}
			fmt.Println()
		}

		// Deploy
		if len(pCfg.Deploy.Services) > 0 {
			fmt.Println("## Deploy\n")
			for _, svc := range pCfg.Deploy.Services {
				fmt.Printf("**%s**\n", svc.Name)
				fmt.Printf("  Trigger: %s\n", strings.Join(svc.TriggerPaths, ", "))
				fmt.Printf("  Command: `%s`\n", svc.Command)
				if svc.SmokeTest != "" {
					fmt.Printf("  Smoke test: `%s`\n", svc.SmokeTest)
				}
				if svc.Rollback != "" {
					fmt.Printf("  Rollback: `%s`\n", svc.Rollback)
				}
				fmt.Println()
			}
		}

		// Context layers
		if len(pCfg.Context.Layers) > 0 {
			fmt.Println("## Context Layers\n")
			fmt.Printf("| Layer | Source | When |\n")
			fmt.Printf("|-------|--------|------|\n")
			for _, l := range pCfg.Context.Layers {
				fmt.Printf("| %s | %s | %s |\n", l.Name, l.Source, l.When)
			}
			fmt.Println()
		}

		// Available skills
		fmt.Println("## Available Skills\n")
		skills := detectSkills(repoRoot, pCfg)
		for _, phase := range []string{"design", "decompose", "investigate", "implement", "review", "done", "shared"} {
			names, ok := skills[phase]
			if !ok || len(names) == 0 {
				continue
			}
			fmt.Printf("**%s/**\n", phase)
			for _, name := range names {
				desc := readSkillDescription(skillsBase, phase+"/"+name+".md")
				if desc != "" {
					fmt.Printf("  %s — %s\n", name, desc)
				} else {
					fmt.Printf("  %s\n", name)
				}
			}
			fmt.Println()
		}

		return nil
	},
}

// readSkillDescription reads the YAML frontmatter description from a skill file.
func readSkillDescription(skillsBase, skillPath string) string {
	fullPath := filepath.Join(skillsBase, skillPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return ""
	}

	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return ""
	}

	endIdx := strings.Index(content[4:], "\n---")
	if endIdx == -1 {
		return ""
	}

	frontmatter := content[4 : 4+endIdx]
	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		return ""
	}
	return fm.Description
}

func init() {
	explainCmd.Flags().String("phase", "", "Explain a specific phase only")
	rootCmd.AddCommand(explainCmd)
}
