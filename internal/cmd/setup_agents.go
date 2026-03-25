package cmd

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

const (
	markerBegin = "<!-- BEGIN COBUILD INTEGRATION"
	markerEnd   = "<!-- END COBUILD INTEGRATION -->"
)

var setupAgentsCmd = &cobra.Command{
	Use:   "update-agents",
	Short: "Generate or update AGENTS.md from current skills and config",
	Long: `Generates .cobuild/AGENTS.md with pipeline instructions from the current
skills and config, and adds a pointer section to CLAUDE.md.

Re-run to update after adding new phases, skills, or commands.
Use --check to see if the integration is stale without modifying files.`,
	Example: `  cobuild update-agents              # regenerate AGENTS.md
  cobuild update-agents --check      # check if stale
  cobuild update-agents --print      # show what would be written`,
	RunE: func(cmd *cobra.Command, args []string) error {
		check, _ := cmd.Flags().GetBool("check")
		printOnly, _ := cmd.Flags().GetBool("print")

		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)
		if pCfg == nil {
			pCfg = config.DefaultConfig()
		}

		// Detect available skills
		skills := detectSkills(repoRoot, pCfg)

		// Detect workflows
		workflows := detectWorkflows(pCfg)

		// Generate AGENTS.md content
		agentsContent := generateAgentsContent(projectName, projectPrefix, workflows, skills, pCfg)

		hash := computeHash(agentsContent)

		if check {
			return checkFreshness(repoRoot, hash)
		}

		if printOnly {
			fmt.Print(agentsContent)
			return nil
		}

		// Write .cobuild/AGENTS.md
		agentsPath := filepath.Join(repoRoot, ".cobuild", "AGENTS.md")
		os.MkdirAll(filepath.Dir(agentsPath), 0755)
		if err := os.WriteFile(agentsPath, []byte(agentsContent), 0644); err != nil {
			return fmt.Errorf("write AGENTS.md: %w", err)
		}
		fmt.Printf("Updated %s (hash: %s)\n", agentsPath, hash[:8])

		// Update CLAUDE.md with pointer section
		claudePath := filepath.Join(repoRoot, "CLAUDE.md")
		if err := updateClaudePointer(claudePath); err != nil {
			fmt.Printf("Warning: could not update CLAUDE.md: %v\n", err)
		} else {
			fmt.Printf("Updated %s\n", claudePath)
		}

		return nil
	},
}

func detectSkills(repoRoot string, pCfg *config.Config) map[string][]string {
	skillsDir := "skills"
	if pCfg != nil && pCfg.SkillsDir != "" {
		skillsDir = pCfg.SkillsDir
	}

	skills := make(map[string][]string)
	base := filepath.Join(repoRoot, skillsDir)

	entries, err := os.ReadDir(base)
	if err != nil {
		return skills
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		phase := e.Name()
		subEntries, err := os.ReadDir(filepath.Join(base, phase))
		if err != nil {
			continue
		}
		for _, se := range subEntries {
			if strings.HasSuffix(se.Name(), ".md") {
				name := strings.TrimSuffix(se.Name(), ".md")
				skills[phase] = append(skills[phase], name)
			}
		}
	}
	return skills
}

func detectWorkflows(pCfg *config.Config) map[string]string {
	workflows := map[string]string{
		"design": "design → decompose → implement → review → done",
		"bug":    "investigate → implement → review → done",
		"task":   "implement → review → done",
	}

	if pCfg != nil && pCfg.Workflows != nil {
		for name, wf := range pCfg.Workflows {
			workflows[name] = strings.Join(wf.Phases, " → ")
		}
	}
	return workflows
}

func generateAgentsContent(project, prefix string, workflows map[string]string, skills map[string][]string, pCfg *config.Config) string {
	var sb strings.Builder

	hash := "" // placeholder, computed after
	sb.WriteString(fmt.Sprintf("%s v:1 hash:%s -->\n", markerBegin, hash))
	sb.WriteString("# CoBuild Pipeline Instructions\n\n")
	sb.WriteString("This project uses CoBuild for pipeline automation. If you are an agent working on a task dispatched by CoBuild, follow these instructions.\n\n")

	// Project
	sb.WriteString("## Project\n\n")
	sb.WriteString(fmt.Sprintf("- **Name:** %s\n", project))
	if prefix != "" {
		sb.WriteString(fmt.Sprintf("- **Prefix:** %s\n", prefix))
	}
	sb.WriteString("- **Workflows:**\n")
	for name, phases := range workflows {
		sb.WriteString(fmt.Sprintf("  - %s: %s\n", name, phases))
	}
	sb.WriteString("\n")

	// Commands
	sb.WriteString("## Commands\n\n")
	sb.WriteString("### Pipeline\n\n")
	sb.WriteString("| Command | When to use |\n")
	sb.WriteString("|---------|------------|\n")
	sb.WriteString("| `cobuild init <id>` | Submit a design/bug/task to the pipeline |\n")
	sb.WriteString("| `cobuild gate <id> <gate> --verdict pass\\|fail` | Record a gate verdict |\n")
	sb.WriteString("| `cobuild investigate <id> --verdict pass` | Record bug investigation verdict |\n")
	sb.WriteString("| `cobuild dispatch <task-id>` | Dispatch a task to an implementing agent |\n")
	sb.WriteString("| `cobuild dispatch-wave <design-id>` | Dispatch all ready tasks |\n")
	sb.WriteString("| `cobuild wait <id> [id...]` | Wait for tasks to complete |\n")
	sb.WriteString("| `cobuild complete <task-id>` | **Run as your LAST action** after implementing |\n")
	sb.WriteString("| `cobuild merge <task-id>` | Merge an approved PR |\n")
	sb.WriteString("| `cobuild merge-design <design-id>` | Smart merge all PRs (conflict detection) |\n")
	sb.WriteString("| `cobuild deploy <design-id>` | Deploy affected services |\n")
	sb.WriteString("| `cobuild retro <design-id>` | Run retrospective |\n")
	sb.WriteString("| `cobuild status` | Show all active pipelines |\n")
	sb.WriteString("| `cobuild audit <id>` | View gate history |\n")
	sb.WriteString("\n")

	sb.WriteString("### Work Items\n\n")
	sb.WriteString("| Command | Purpose |\n")
	sb.WriteString("|---------|--------|\n")
	sb.WriteString("| `cobuild wi show <id>` | Read a design, task, or bug |\n")
	sb.WriteString("| `cobuild wi list --type <type>` | List work items |\n")
	sb.WriteString("| `cobuild wi links <id>` | See relationships |\n")
	sb.WriteString("| `cobuild wi status <id> <status>` | Update status |\n")
	sb.WriteString("| `cobuild wi append <id> --body \"...\"` | Append content |\n")
	sb.WriteString("| `cobuild wi create --type <type> --title \"...\"` | Create work item |\n")
	sb.WriteString("\n")

	// Bug workflow
	sb.WriteString("## Bug Workflow\n\n")
	sb.WriteString("Bugs go through investigation before implementation:\n\n")
	sb.WriteString("1. `cobuild init <bug-id>` — enters investigate phase\n")
	sb.WriteString("2. Investigation agent analyses root cause (read-only, does NOT fix)\n")
	sb.WriteString("3. `cobuild investigate <bug-id> --verdict pass` — advances to implement\n")
	sb.WriteString("4. Fix task created as child with implementation spec\n")
	sb.WriteString("5. `cobuild dispatch <fix-task-id>` → review → merge → done\n\n")

	// Task completion
	sb.WriteString("## Task Completion Protocol\n\n")
	sb.WriteString("When you have completed your implementation:\n\n")
	step := 1
	if pCfg != nil && len(pCfg.Test) > 0 {
		sb.WriteString(fmt.Sprintf("%d. Run tests: `%s`\n", step, strings.Join(pCfg.Test, " && ")))
		step++
	}
	if pCfg != nil && len(pCfg.Build) > 0 {
		sb.WriteString(fmt.Sprintf("%d. Build: `%s`\n", step, strings.Join(pCfg.Build, " && ")))
		step++
	}
	sb.WriteString(fmt.Sprintf("%d. **Run `cobuild complete <task-id>`**\n\n", step))
	sb.WriteString("**Do this as your LAST action. Do not skip it.**\n\n")

	// Agent clarity
	sb.WriteString("## What CoBuild manages vs what you do directly\n\n")
	sb.WriteString("Be explicit when reporting status. State clearly whether an action is:\n")
	sb.WriteString("- **A CoBuild pipeline action** — \"CoBuild will handle this: `cobuild merge-design <id>`\"\n")
	sb.WriteString("- **A direct action you'll take** — \"I'll run the deploy command now\"\n")
	sb.WriteString("- **A human action needed** — \"You need to approve this PR\"\n\n")

	// Skills
	sb.WriteString("## Skills\n\n")
	sb.WriteString("| Directory | Skills | Purpose |\n")
	sb.WriteString("|-----------|--------|---------|\n")

	phaseDescriptions := map[string]string{
		"design":      "Design evaluation",
		"decompose":   "Break designs into tasks",
		"investigate":  "Root cause analysis for bugs",
		"implement":   "Task dispatch and monitoring",
		"review":      "Code review",
		"done":        "Post-delivery retrospective",
		"shared":      "Cross-phase reference",
	}

	phaseOrder := []string{"design", "decompose", "investigate", "implement", "review", "done", "shared"}
	for _, phase := range phaseOrder {
		names, ok := skills[phase]
		if !ok || len(names) == 0 {
			continue
		}
		desc := phaseDescriptions[phase]
		if desc == "" {
			desc = phase
		}
		sb.WriteString(fmt.Sprintf("| `%s/` | %s | %s |\n", phase, strings.Join(names, ", "), desc))
	}
	sb.WriteString("\n")
	sb.WriteString(markerEnd + "\n")

	// Now compute hash and replace placeholder
	content := sb.String()
	realHash := computeHash(content)
	content = strings.Replace(content, "hash:", "hash:"+realHash, 1)

	return content
}

func computeHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h[:4])
}

func checkFreshness(repoRoot, currentHash string) error {
	agentsPath := filepath.Join(repoRoot, ".cobuild", "AGENTS.md")
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		fmt.Println("AGENTS.md not found — run `cobuild setup` to install.")
		return nil
	}

	content := string(data)
	if !strings.Contains(content, markerBegin) {
		fmt.Println("AGENTS.md exists but has no CoBuild markers — run `cobuild setup` to update.")
		return nil
	}

	// Extract hash from marker
	idx := strings.Index(content, "hash:")
	if idx == -1 {
		fmt.Println("AGENTS.md has markers but no hash — run `cobuild setup` to update.")
		return nil
	}

	// Read hash until space or end of line
	hashStart := idx + 5
	hashEnd := strings.IndexAny(content[hashStart:], " \n-")
	if hashEnd == -1 {
		hashEnd = len(content) - hashStart
	}
	existingHash := content[hashStart : hashStart+hashEnd]

	if existingHash == currentHash {
		fmt.Printf("AGENTS.md is current (hash: %s)\n", currentHash[:8])
	} else {
		fmt.Printf("AGENTS.md is STALE (installed: %s, current: %s)\n", existingHash[:8], currentHash[:8])
		fmt.Println("Run `cobuild setup` to update.")
	}
	return nil
}

func updateClaudePointer(claudePath string) error {
	pointer := `## CoBuild

This project uses [CoBuild](https://github.com/otherjamesbrown/cobuild) for pipeline automation — designs flow through structured phases with quality gates.

**Read ` + "`.cobuild/AGENTS.md`" + ` for full pipeline instructions, commands, and task completion protocol.**
`

	data, err := os.ReadFile(claudePath)
	if err != nil {
		// CLAUDE.md doesn't exist — create with pointer
		return os.WriteFile(claudePath, []byte(pointer), 0644)
	}

	content := string(data)
	if strings.Contains(content, ".cobuild/AGENTS.md") {
		return nil // already has pointer
	}

	// Append pointer
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n" + pointer
	return os.WriteFile(claudePath, []byte(content), 0644)
}

func init() {
	setupAgentsCmd.Flags().Bool("check", false, "Check if integration is stale")
	setupAgentsCmd.Flags().Bool("print", false, "Print generated AGENTS.md without writing")
	rootCmd.AddCommand(setupAgentsCmd)
}
