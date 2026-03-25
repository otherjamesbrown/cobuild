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

type skillMeta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Summary     string `yaml:"summary"`
}

var explainCmd = &cobra.Command{
	Use:   "explain",
	Short: "Show the full pipeline in human-readable form",
	Long: `Reads the pipeline config and skills and explains what happens at each
stage, in plain language. This is "what does my pipeline actually do?"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)
		if pCfg == nil {
			pCfg = config.DefaultConfig()
		}

		skillsDir := pCfg.SkillsDir
		if skillsDir == "" {
			skillsDir = "skills"
		}
		skillsBase := filepath.Join(repoRoot, skillsDir)

		fmt.Printf("# %s — Pipeline Overview\n\n", projectName)

		// Tell the story for each workflow
		if pCfg.Workflows != nil {
			for wfName, wf := range pCfg.Workflows {
				explainWorkflow(wfName, wf.Phases, pCfg, skillsBase)
			}
		} else {
			explainWorkflow("default", []string{"design", "decompose", "implement", "review", "done"}, pCfg, skillsBase)
		}

		// Build & test
		if len(pCfg.Build) > 0 || len(pCfg.Test) > 0 {
			fmt.Println("---\n")
			fmt.Println("## Build & Test\n")
			if len(pCfg.Build) > 0 {
				fmt.Printf("Agents run these commands to build:\n")
				for _, c := range pCfg.Build {
					fmt.Printf("    %s\n", c)
				}
				fmt.Println()
			}
			if len(pCfg.Test) > 0 {
				fmt.Printf("Agents run these commands to test:\n")
				for _, c := range pCfg.Test {
					fmt.Printf("    %s\n", c)
				}
				fmt.Println()
			}
		}

		// Deploy
		if len(pCfg.Deploy.Services) > 0 {
			fmt.Println("---\n")
			fmt.Println("## After Merge — Deploy\n")
			fmt.Println("When PRs are merged, CoBuild checks which files changed and deploys the affected services:\n")
			for _, svc := range pCfg.Deploy.Services {
				fmt.Printf("  **%s**\n", svc.Name)
				fmt.Printf("    Deploys when files change in: %s\n", strings.Join(svc.TriggerPaths, ", "))
				fmt.Printf("    Runs: `%s`\n", svc.Command)
				if svc.SmokeTest != "" {
					fmt.Printf("    Then verifies: `%s`\n", svc.SmokeTest)
				}
				if svc.Rollback != "" {
					fmt.Printf("    On failure, rolls back: `%s`\n", svc.Rollback)
				}
				fmt.Println()
			}
		}

		return nil
	},
}

func explainWorkflow(name string, phases []string, pCfg *config.Config, skillsBase string) {
	// Header
	fmt.Printf("## When you submit a %s\n\n", name)
	fmt.Printf("    %s\n\n", strings.Join(phases, " → "))

	for i, phaseName := range phases {
		stepNum := i + 1
		fmt.Printf("**Step %d: %s**\n", stepNum, strings.Title(phaseName))

		// Get skill summary if configured
		if pCfg.Phases != nil {
			if phase, ok := pCfg.Phases[phaseName]; ok {
				if phase.Skill != "" {
					summary := readSkillSummary(skillsBase, phase.Skill)
					if summary != "" {
						fmt.Printf("%s\n", wrapText(summary, 80))
					}
				} else if phase.Gate != "" {
					// No skill but has a gate — use the phase description
					printPhaseDescription(phaseName)
				}

				if phase.Gate != "" {
					fmt.Printf("Must pass the **%s** gate to proceed.\n", phase.Gate)
				}
			} else {
				printPhaseDescription(phaseName)
			}
		} else {
			printPhaseDescription(phaseName)
		}

		// Add dispatch details for implement phase
		if phaseName == "implement" {
			if pCfg.Dispatch.MaxConcurrent > 0 {
				fmt.Printf("Up to %d agents work in parallel.\n", pCfg.Dispatch.MaxConcurrent)
			}
			if pCfg.Dispatch.DefaultModel != "" {
				fmt.Printf("Agents use %s.\n", pCfg.Dispatch.DefaultModel)
			}
		}

		// Add review details
		if phaseName == "review" && pCfg.Review.Strategy != "" {
			switch pCfg.Review.Strategy {
			case "external":
				reviewers := "an external reviewer"
				if len(pCfg.Review.ExternalReviewers) > 0 {
					reviewers = strings.Join(pCfg.Review.ExternalReviewers, ", ")
				}
				fmt.Printf("PRs are reviewed by %s, then CoBuild processes the verdict.\n", reviewers)
			case "agent":
				fmt.Printf("A CoBuild agent reviews each PR directly.\n")
			}
		}

		fmt.Println()
	}
}

func printPhaseDescription(phaseName string) {
	switch phaseName {
	case "design":
		fmt.Println("Your design is checked for completeness. Is the problem clear? Are there")
		fmt.Println("acceptance criteria? Can an agent implement it without asking questions?")
	case "decompose":
		fmt.Println("The design is broken into small tasks that agents can complete independently.")
		fmt.Println("Tasks are ordered by dependency — wave 1 has no blockers, wave 2 depends on wave 1.")
	case "investigate":
		fmt.Println("A read-only investigation. The agent traces the root cause, maps affected")
		fmt.Println("files, and writes a fix specification — but does not modify any code.")
	case "implement":
		fmt.Println("Agents are dispatched into isolated git worktrees, one per task. Each agent")
		fmt.Println("gets the task spec, parent design context, and project architecture.")
	case "review":
		fmt.Println("Each PR is reviewed before merging. Only real issues block — not style nitpicks")
		fmt.Println("or pre-existing CI failures.")
	case "done":
		fmt.Println("A retrospective reviews the full pipeline run: what worked, what failed, and")
		fmt.Println("what to change in the skills and config for next time.")
	}
}

func readSkillSummary(skillsBase, skillPath string) string {
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
	var fm skillMeta
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		return ""
	}

	// Prefer summary (human-readable), fall back to description
	if fm.Summary != "" {
		return fm.Summary
	}
	return fm.Description
}

func wrapText(text string, width int) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
		} else {
			line += " " + w
		}
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

func init() {
	rootCmd.AddCommand(explainCmd)
}
