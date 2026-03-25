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
	Version     string `yaml:"version"`
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

		// Workflows summary
		if pCfg.Workflows != nil {
			for wfName, wf := range pCfg.Workflows {
				explainWorkflow(wfName, wf.Phases, pCfg, skillsBase, skillsDir)
			}
		}

		// Build & test
		if len(pCfg.Build) > 0 || len(pCfg.Test) > 0 {
			fmt.Println("---\n")
			fmt.Println("## Build & Test\n")
			if len(pCfg.Build) > 0 {
				fmt.Println("Agents run these commands after implementing a task:\n")
				fmt.Println("| Step | Command |")
				fmt.Println("|------|---------|")
				for _, c := range pCfg.Build {
					fmt.Printf("| Build | `%s` |\n", c)
				}
				for _, c := range pCfg.Test {
					fmt.Printf("| Test | `%s` |\n", c)
				}
				fmt.Println()
			}
		}

		// Deploy
		if len(pCfg.Deploy.Services) > 0 {
			fmt.Println("---\n")
			fmt.Println("## Deploy\n")
			fmt.Println("After PRs merge, CoBuild deploys services whose files changed:\n")
			fmt.Println("| Service | Trigger | Deploy | Smoke Test | Rollback |")
			fmt.Println("|---------|---------|--------|------------|----------|")
			for _, svc := range pCfg.Deploy.Services {
				smoke := "—"
				if svc.SmokeTest != "" {
					smoke = "`" + svc.SmokeTest + "`"
				}
				rollback := "—"
				if svc.Rollback != "" {
					rollback = "`" + svc.Rollback + "`"
				}
				fmt.Printf("| **%s** | `%s` | `%s` | %s | %s |\n",
					svc.Name,
					strings.Join(svc.TriggerPaths, "`, `"),
					svc.Command,
					smoke,
					rollback)
			}
			fmt.Println()
		}

		return nil
	},
}

func explainWorkflow(name string, phases []string, pCfg *config.Config, skillsBase, skillsDir string) {
	fmt.Printf("## When you submit a %s\n\n", name)
	fmt.Printf("```\n%s\n```\n\n", strings.Join(phases, " → "))

	for i, phaseName := range phases {
		stepNum := i + 1
		fmt.Printf("### Step %d — %s\n\n", stepNum, strings.Title(phaseName))

		// Get phase config
		var gateName, skillPath, stallCheckPath, model string
		if pCfg.Phases != nil {
			if phase, ok := pCfg.Phases[phaseName]; ok {
				gateName = phase.Gate
				skillPath = phase.Skill
				stallCheckPath = phase.StallCheck
				model = phase.Model
			}
		}

		// Dispatch model override
		if phaseName == "implement" && model == "" && pCfg.Dispatch.DefaultModel != "" {
			model = pCfg.Dispatch.DefaultModel
		}

		// Description from skill summary
		if skillPath != "" {
			meta := readSkillMeta(skillsBase, skillPath)
			if meta.Summary != "" {
				fmt.Printf("%s\n\n", wrapText(meta.Summary, 80))
			} else {
				printPhaseDescription(phaseName)
				fmt.Println()
			}
		} else {
			printPhaseDescription(phaseName)
			fmt.Println()
		}

		// Details table
		fmt.Println("| | |")
		fmt.Println("|---|---|")

		if gateName != "" {
			gateDesc := describeGate(gateName)
			fmt.Printf("| **Gate** | `%s` — %s |\n", gateName, gateDesc)
		}

		if model != "" {
			fmt.Printf("| **Model** | %s |\n", model)
		}

		if skillPath != "" {
			meta := readSkillMeta(skillsBase, skillPath)
			version := "—"
			if meta.Version != "" {
				version = "v" + meta.Version
			}
			fmt.Printf("| **Skill** | [`%s`](%s/%s) %s |\n", skillPath, skillsDir, skillPath, version)
		}

		if stallCheckPath != "" {
			meta := readSkillMeta(skillsBase, stallCheckPath)
			version := ""
			if meta.Version != "" {
				version = " v" + meta.Version
			}
			fmt.Printf("| **Stall check** | [`%s`](%s/%s)%s |\n", stallCheckPath, skillsDir, stallCheckPath, version)
		}

		// Phase-specific details
		if phaseName == "implement" && pCfg.Dispatch.MaxConcurrent > 0 {
			fmt.Printf("| **Max concurrent** | %d agents |\n", pCfg.Dispatch.MaxConcurrent)
		}

		if phaseName == "review" && pCfg.Review.Strategy != "" {
			strategy := pCfg.Review.Strategy
			if len(pCfg.Review.ExternalReviewers) > 0 {
				strategy += " (" + strings.Join(pCfg.Review.ExternalReviewers, ", ") + ")"
			}
			fmt.Printf("| **Review strategy** | %s |\n", strategy)
		}

		// Context layers for this phase
		var phaseLayers []string
		for _, l := range pCfg.Context.Layers {
			if l.When == "phase:"+phaseName {
				phaseLayers = append(phaseLayers, fmt.Sprintf("`%s` (%s)", l.Name, l.Source))
			}
		}
		if len(phaseLayers) > 0 {
			fmt.Printf("| **Extra context** | %s |\n", strings.Join(phaseLayers, ", "))
		}

		fmt.Println()
	}
}

func describeGate(gateName string) string {
	switch gateName {
	case "readiness-review":
		return "Checks 5 readiness criteria + implementability. Blocks until the design is complete enough to decompose."
	case "decomposition-review":
		return "Verifies tasks are small enough for single sessions, dependencies are acyclic, and every acceptance criterion is covered."
	case "investigation":
		return "Verifies root cause is identified, affected files are mapped, and a fix specification exists for the implementing agent."
	case "review":
		return "Evaluates PR against task spec. Only real issues (security, bugs, logic errors) block — not style or pre-existing failures."
	case "retrospective":
		return "Records what worked, what failed, and feeds improvements back into skills and config."
	default:
		return "Quality check at this phase boundary."
	}
}

func printPhaseDescription(phaseName string) {
	switch phaseName {
	case "design":
		fmt.Print("Your design is checked for completeness. Is the problem clear? Are there\nacceptance criteria? Can an agent implement it without asking questions?")
	case "decompose":
		fmt.Print("The design is broken into small tasks that agents can complete independently.\nTasks are ordered by dependency — wave 1 has no blockers, wave 2 depends on wave 1.")
	case "investigate":
		fmt.Print("A read-only investigation. The agent traces the root cause, maps affected\nfiles, and writes a fix specification — but does not modify any code.")
	case "implement":
		fmt.Print("Agents are dispatched into isolated git worktrees, one per task. Each agent\ngets the task spec, parent design context, and project architecture.")
	case "review":
		fmt.Print("Each PR is reviewed before merging. Only real issues block — not style nitpicks\nor pre-existing CI failures.")
	case "done":
		fmt.Print("A retrospective reviews the full pipeline run: what worked, what failed, and\nwhat to change in the skills and config for next time.")
	}
}

func readSkillMeta(skillsBase, skillPath string) skillMeta {
	fullPath := filepath.Join(skillsBase, skillPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return skillMeta{}
	}

	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return skillMeta{}
	}

	endIdx := strings.Index(content[4:], "\n---")
	if endIdx == -1 {
		return skillMeta{}
	}

	frontmatter := content[4 : 4+endIdx]
	var fm skillMeta
	yaml.Unmarshal([]byte(frontmatter), &fm)
	return fm
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
