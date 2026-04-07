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

		// Write CoBuild section into AGENTS.md at the repo root.
		// Uses markers to coexist with other tools (Beads, Gastown, etc.)
		// that also write to AGENTS.md.
		agentsPath := filepath.Join(repoRoot, "AGENTS.md")

		existing, _ := os.ReadFile(agentsPath)
		var finalContent string

		if len(existing) > 0 && strings.Contains(string(existing), markerBegin) {
			// Replace existing CoBuild section between markers
			content := string(existing)
			beginIdx := strings.Index(content, markerBegin)
			endIdx := strings.Index(content, markerEnd)
			if beginIdx >= 0 && endIdx > beginIdx {
				endIdx += len(markerEnd)
				if endIdx < len(content) && content[endIdx] == '\n' {
					endIdx++
				}
				finalContent = content[:beginIdx] + agentsContent + content[endIdx:]
				fmt.Printf("Replaced CoBuild section in %s (hash: %s)\n", agentsPath, hash[:8])
			} else {
				// Malformed markers — append
				finalContent = string(existing) + "\n" + agentsContent
				fmt.Printf("Appended CoBuild section to %s (malformed markers) (hash: %s)\n", agentsPath, hash[:8])
			}
		} else if len(existing) > 0 {
			// File exists (maybe from beads/gastown) — append our section
			content := string(existing)
			if !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			finalContent = content + "\n" + agentsContent
			fmt.Printf("Appended CoBuild section to %s (hash: %s)\n", agentsPath, hash[:8])
		} else {
			// No AGENTS.md — create with our section
			finalContent = agentsContent
			fmt.Printf("Created %s (hash: %s)\n", agentsPath, hash[:8])
		}

		if err := os.WriteFile(agentsPath, []byte(finalContent), 0644); err != nil {
			return fmt.Errorf("write AGENTS.md: %w", err)
		}

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
		"design":      "design → decompose → implement → review → done",
		"bug":         "fix → review → done",
		"bug-complex": "investigate → implement → review → done",
		"task":        "implement → review → done",
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
	sb.WriteString("| `cobuild scan` | Refresh project anatomy (file index for agents) |\n")
	sb.WriteString("| `cobuild explain` | Show pipeline in human-readable form |\n")
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

	// How to run pipelines
	sb.WriteString("## How to Run Pipelines\n\n")
	sb.WriteString("There are two ways to advance each phase:\n\n")
	sb.WriteString("### Option A: You already did the work (interactive session)\n\n")
	sb.WriteString("If you've already reviewed the design, decomposed into tasks, or done investigation\n")
	sb.WriteString("in the current session with the developer, just record the gate verdict:\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("cobuild review <id> --verdict pass --readiness 5 --body \"<findings>\"   # record design review\n")
	sb.WriteString("cobuild decompose <id> --verdict pass --body \"<task summary>\"          # record decomposition\n")
	sb.WriteString("cobuild investigate <id> --verdict pass --body \"<root cause>\"           # record investigation\n")
	sb.WriteString("```\n\n")
	sb.WriteString("The gate command records the verdict and advances the phase. No dispatch needed.\n\n")
	sb.WriteString("### Option B: Delegate to a separate agent (dispatch)\n\n")
	sb.WriteString("If you want a fresh agent to handle a phase in its own context:\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("cobuild dispatch <id>   # spawns agent in tmux for the current phase\n")
	sb.WriteString("cobuild wait <id>       # blocks until the agent completes\n")
	sb.WriteString("```\n\n")
	sb.WriteString("`cobuild dispatch` is phase-aware — it generates the right prompt automatically.\n")
	sb.WriteString("Use this for implementation (agents write code) and when you want a clean context.\n\n")
	sb.WriteString("### Which to use?\n\n")
	sb.WriteString("| Situation | Use |\n")
	sb.WriteString("|-----------|-----|\n")
	sb.WriteString("| You just reviewed the design with the developer | Option A — record the gate |\n")
	sb.WriteString("| You need an agent to write code | Option B — dispatch |\n")
	sb.WriteString("| You decomposed tasks in conversation | Option A — record the gate |\n")
	sb.WriteString("| You want investigation in a clean context | Option B — dispatch |\n")
	sb.WriteString("| Phase needs multiple file reads/edits | Option B — saves your context |\n\n")

	// Design workflow
	sb.WriteString("### Design Workflow\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("cobuild init <design-id>                     # enters design phase\n")
	sb.WriteString("cobuild dispatch <design-id>                 # spawns readiness review agent\n")
	sb.WriteString("cobuild wait <design-id>                     # wait for review to complete\n")
	sb.WriteString("# Agent records gate → advances to decompose\n")
	sb.WriteString("cobuild dispatch <design-id>                 # spawns decomposition agent\n")
	sb.WriteString("cobuild wait <design-id>                     # wait for decomposition\n")
	sb.WriteString("# Agent creates tasks, records gate → advances to implement\n")
	sb.WriteString("cobuild dispatch-wave <design-id>            # dispatch ready tasks\n")
	sb.WriteString("cobuild wait <task-1> <task-2> ...           # wait for implementation\n")
	sb.WriteString("# Repeat dispatch-wave/wait for each wave\n")
	sb.WriteString("cobuild merge-design <design-id> --dry-run   # preview merge plan\n")
	sb.WriteString("cobuild merge-design <design-id>             # merge all PRs\n")
	sb.WriteString("cobuild deploy <design-id>                   # deploy affected services\n")
	sb.WriteString("cobuild retro <design-id>                    # run retrospective\n")
	sb.WriteString("```\n\n")

	// Bug workflow
	sb.WriteString("### Bug Workflow\n\n")
	sb.WriteString("**Default (most bugs):** single `fix` session — agent investigates and fixes together.\n\n")
	sb.WriteString("**Escalation path:** if the bug is complex, label it `needs-investigation` first — it routes to a read-only investigation phase that produces a fix spec before any code is changed.\n\n")
	sb.WriteString("#### When to add `needs-investigation`\n\n")
	sb.WriteString("Apply the label if **any** of these are true:\n\n")
	sb.WriteString("1. Root cause unknown (symptom visible, mechanism unclear)\n")
	sb.WriteString("2. Bug spans multiple services, modules, or repos\n")
	sb.WriteString("3. Data or security implications — need blast radius assessment before fixing\n")
	sb.WriteString("4. This area has broken before, or the fix might have unintended side effects\n")
	sb.WriteString("5. Reproduces inconsistently — needs investigation to find the trigger\n")
	sb.WriteString("6. Fix shape is non-obvious (can't describe it in 1-2 sentences)\n")
	sb.WriteString("7. Investigation produces options that require a stakeholder decision\n\n")
	sb.WriteString("If none apply → omit the label. The fix agent will investigate as it fixes.\n\n")
	sb.WriteString("#### Default bug flow\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("cobuild init <bug-id>                        # enters fix phase\n")
	sb.WriteString("cobuild dispatch <bug-id>                    # spawns fix agent (investigate + implement)\n")
	sb.WriteString("cobuild wait <bug-id>                        # wait for fix\n")
	sb.WriteString("cobuild merge <bug-id>                       # merge the fix PR\n")
	sb.WriteString("cobuild deploy <bug-id>                      # deploy if needed\n")
	sb.WriteString("```\n\n")
	sb.WriteString("#### Complex bug flow (needs-investigation label)\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("cobuild wi label add <bug-id> needs-investigation\n")
	sb.WriteString("cobuild init <bug-id>                        # enters investigate phase\n")
	sb.WriteString("cobuild dispatch <bug-id>                    # spawns investigation agent (READ-ONLY)\n")
	sb.WriteString("cobuild wait <bug-id>                        # wait for investigation\n")
	sb.WriteString("# Agent records investigation report + gate → creates fix task → advances to implement\n")
	sb.WriteString("cobuild dispatch <fix-task-id>               # spawns implementing agent\n")
	sb.WriteString("cobuild wait <fix-task-id>                   # wait for fix\n")
	sb.WriteString("cobuild merge <fix-task-id>                  # merge the fix PR\n")
	sb.WriteString("cobuild deploy <bug-id>                      # deploy if needed\n")
	sb.WriteString("```\n\n")

	// Task workflow
	sb.WriteString("### Task Workflow\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("cobuild init <task-id>                       # enters implement phase\n")
	sb.WriteString("cobuild dispatch <task-id>                   # spawns implementing agent\n")
	sb.WriteString("cobuild wait <task-id>                       # wait for completion\n")
	sb.WriteString("cobuild merge <task-id>                      # merge PR\n")
	sb.WriteString("```\n\n")

	// Key point
	sb.WriteString("**Key:** `cobuild dispatch` is phase-aware. It reads the current pipeline phase and generates the right prompt automatically — investigation prompt for bugs, readiness review for designs, implementation for tasks. You don't need different commands for different phases.\n\n")

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
	sb.WriteString("The Stop hook will run `cobuild complete` automatically when you finish.\n")
	sb.WriteString("If it fails, run it manually as your last action.\n\n")

	// Orchestrator lifecycle
	sb.WriteString("## Orchestrator Protocol\n\n")
	sb.WriteString("If you are the orchestrating agent (dispatching tasks, not implementing them),\n")
	sb.WriteString("**follow through the full lifecycle. Do not stop after dispatch.**\n\n")
	sb.WriteString("After dispatching tasks:\n\n")
	sb.WriteString("1. **Monitor** — use `cobuild audit <id>` or `cobuild status` for instant checks (do NOT use `cobuild wait` as a background task — it's a 2-hour blocking command)\n")
	sb.WriteString("2. **Review** — when tasks reach `review` phase (PRs created), check Gemini review findings via `gh api repos/<owner>/<repo>/pulls/<pr>/comments`\n")
	sb.WriteString("3. **Address blockers** — send HIGH findings back to the agent (via tmux send-keys) or fix directly\n")
	sb.WriteString("4. **Merge** — `cobuild merge <task-id>` (or `gh pr merge <pr> --admin --squash` if cobuild merge fails)\n")
	sb.WriteString("5. **Close** — update work item status to closed\n")
	sb.WriteString("6. **Report** — tell the user what shipped, not \"want me to review?\"\n")
	sb.WriteString("7. **Deploy** — do NOT deploy automatically. Run `cobuild deploy <id> --dry-run` to show which services would be affected, then **ask the user** for approval. On approval, run `cobuild deploy <id>` (triggers deploy commands from pipeline config with smoke tests and auto-rollback). Deploy touches production and is always a human decision.\n\n")
	sb.WriteString("Only pause for user input if there is an actual blocker: merge conflict, critical Gemini finding you can't resolve, a design decision, or deploy approval.\n\n")

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
		"fix":         "Single-session bug fix (investigate + implement)",
		"investigate": "Root cause analysis for needs-investigation bugs",
		"implement":   "Task dispatch and monitoring",
		"review":      "Code review",
		"done":        "Post-delivery retrospective",
		"shared":      "Cross-phase reference",
	}

	phaseOrder := []string{"design", "decompose", "fix", "investigate", "implement", "review", "done", "shared"}
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
	agentsPath := filepath.Join(repoRoot, "AGENTS.md")
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

**Read ` + "`AGENTS.md`" + ` for pipeline instructions, commands, and task completion protocol.**
`

	data, err := os.ReadFile(claudePath)
	if err != nil {
		// CLAUDE.md doesn't exist — create with pointer
		return os.WriteFile(claudePath, []byte(pointer), 0644)
	}

	content := string(data)

	// Fix stale references: .cobuild/AGENTS.md → AGENTS.md (root)
	if strings.Contains(content, ".cobuild/AGENTS.md") {
		content = strings.ReplaceAll(content, "`.cobuild/AGENTS.md`", "`AGENTS.md`")
		content = strings.ReplaceAll(content, ".cobuild/AGENTS.md", "AGENTS.md")
		return os.WriteFile(claudePath, []byte(content), 0644)
	}

	// Already has the correct pointer
	if strings.Contains(content, "`AGENTS.md`") {
		return nil
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
