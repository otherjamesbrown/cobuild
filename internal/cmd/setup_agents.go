package cmd

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// detectWorkflows returns workflow name → rendered phase chain, derived
// directly from the merged pipeline config. It used to maintain a duplicate
// hardcoded fallback map, which drifted from DefaultConfig and papered over
// a MergeConfig bug where override workflows would wipe the base map
// wholesale (cb-11a464). Post-fix, pCfg.Workflows always has the full set
// after merge, so we can trust it as the single source of truth.
func detectWorkflows(pCfg *config.Config) map[string]string {
	if pCfg == nil || len(pCfg.Workflows) == 0 {
		// Only reached if the caller passed a nil-or-empty config AND
		// DefaultConfig wasn't applied — in practice update-agents always
		// calls LoadConfig/DefaultConfig first. Fall back to DefaultConfig()
		// rather than a separately-maintained hardcoded list.
		pCfg = config.DefaultConfig()
	}
	workflows := make(map[string]string, len(pCfg.Workflows))
	for name, wf := range pCfg.Workflows {
		workflows[name] = strings.Join(wf.Phases, " → ")
	}
	return workflows
}

func generateAgentsContent(project, prefix string, workflows map[string]string, skills map[string][]string, pCfg *config.Config) string {
	var sb strings.Builder

	hash := "" // placeholder, computed after
	sb.WriteString(fmt.Sprintf("%s v:1 hash:%s -->\n", markerBegin, hash))
	sb.WriteString("# CoBuild Pipeline Instructions\n\n")
	sb.WriteString("This project uses CoBuild for pipeline automation. If you are a dispatched CoBuild agent working on a task, follow these instructions.\n\n")

	// TLDR — the most-important thing agents must know, pinned at the very
	// top of the file. The rest of this document is reference material; an
	// agent that reads ONLY this section and follows it mechanically will
	// be right every time. The failure mode we're preventing is agents
	// "reasoning from the docs" about which phase needs what — the CLI
	// already tells them, they just need to follow its output.
	sb.WriteString("## For orchestrators — the ONLY thing you need to know\n\n")
	sb.WriteString("Every CoBuild command prints a `Next step:` line telling you exactly what to run next. **Follow it mechanically.** Do not reason about which phase needs which command — that's what the CLI is for.\n\n")
	sb.WriteString("The loop:\n\n")
	sb.WriteString("1. `cobuild init <id>` (if the pipeline doesn't exist yet)\n")
	sb.WriteString("2. `cobuild dispatch <id>` — spawns a dispatched CoBuild agent for the current phase\n")
	sb.WriteString("3. Read the `Next step:` line printed by the command — run that\n")
	sb.WriteString("4. Repeat step 3 until phase = done\n")
	sb.WriteString("5. **Report back to the user with what shipped.** Not \"dispatched, let me know when you're ready\" — wait for completion, then summarise the outcome.\n\n")
	sb.WriteString("If you are ever unsure what to run next, run `cobuild next <id>` — it prints the single concrete command for the current state.\n\n")
	sb.WriteString("Do NOT execute phase work yourself (decompose, review, investigate, etc.) just because you could. **Every phase has a skill and a dispatched CoBuild agent runs it.** Your only job as orchestrator is to type the commands, follow the output, and report the result when it's done.\n\n")
	sb.WriteString("**When a user asks you to run/orchestrate/complete a shard through CoBuild end-to-end, load the `skills/orchestrate/run-pipeline.md` skill and follow it.** That skill is the single source of truth for the orchestrator loop, per-phase actions, failure modes, and the structured report format. Do not reason about what to do from memory — load the skill and follow it mechanically.\n\n")

	// Explicit rule — dispatch is NOT the end of your turn
	sb.WriteString("## Dispatch is not a handoff to the user\n\n")
	sb.WriteString("**Common failure mode:** an orchestrator agent runs `cobuild dispatch <id>`, sees \"Dispatched\" in the output, and stops. This is wrong. Dispatch spawns a **separate** dispatched CoBuild agent in a tmux worktree that runs asynchronously — CoBuild does not block your session while it runs. Your job is not done until that agent has completed and you have reported back to the user.\n\n")
	sb.WriteString("**After every `cobuild dispatch` or `cobuild dispatch-wave`:**\n\n")
	sb.WriteString("1. Follow the `Next step:` line — usually `cobuild audit <id>` or `cobuild wait <id>`\n")
	sb.WriteString("2. Poll with `cobuild audit <id>` every ~30-60 seconds (do NOT use `cobuild wait` — it's a 2h blocker). You can use a short `sleep` between polls or just retry manually.\n")
	sb.WriteString("3. When the dispatched agent completes (status = `needs-review`, or the pipeline phase has advanced), **inspect what happened** via `cobuild audit <id>` and `cobuild wi show <child-id>` for any new shards\n")
	sb.WriteString("4. **Then, and only then, report back to the user** with a concrete summary: which shards were created, which PRs were opened, which gates passed or failed, and what the next concrete action is\n\n")
	sb.WriteString("**Never return to the user with just \"Dispatched\" and nothing else.** The user has to chase you for the outcome every time, and that's exactly the manual overhead CoBuild exists to eliminate. If the dispatched agent will take a long time (implementation waves, review cycles), it is still your job to wait — CoBuild is designed so orchestrators follow through the full lifecycle. The only legitimate reasons to return to the user before completion are:\n\n")
	sb.WriteString("- The dispatched agent is genuinely blocked (gate failed, critical review finding, merge conflict) and needs a human decision\n")
	sb.WriteString("- Deploy phase reached — deploy always requires human approval\n")
	sb.WriteString("- The pipeline has hit a true dead-end (max retries exceeded, infrastructure error)\n\n")
	sb.WriteString("In any of those cases, explain WHY you're stopping and WHAT the user needs to decide. Don't just drop the ball.\n\n")

	// Terminology — pinned at the top so every agent reading this file has
	// the shared vocabulary before it hits commands or workflow details.
	sb.WriteString("## Terminology\n\n")
	sb.WriteString("Two roles show up throughout CoBuild's docs, skills, and commit messages. Use these terms consistently:\n\n")
	sb.WriteString("- **orchestrator agent** — whoever invokes `cobuild dispatch`, `cobuild run`, or any other pipeline CLI. Stays lightweight and delegates work. Can be an interactive Claude/Codex session, the `cobuild poller` daemon, a cron job, or a human at a shell prompt.\n")
	sb.WriteString("- **dispatched CoBuild agent** — the fresh Claude Code or Codex process CoBuild spawns in a tmux window inside a git worktree to execute a phase's skill. Does all the real reading, editing, and committing. Exits when the skill is done.\n\n")
	sb.WriteString("If you see \"M\", \"parent session\", \"calling agent\", \"fresh session\", or \"implementing agent\" in older docs, they all map onto one of these two terms — prefer the canonical terms above.\n\n")

	// Project
	sb.WriteString("## Project\n\n")
	sb.WriteString(fmt.Sprintf("- **Name:** %s\n", project))
	if prefix != "" {
		sb.WriteString(fmt.Sprintf("- **Prefix:** %s\n", prefix))
	}
	sb.WriteString("- **Workflows:**\n")
	// Sort by name so the output is deterministic across runs (Go map
	// iteration is randomized, which used to flip the computed hash on
	// every regen).
	workflowNames := make([]string, 0, len(workflows))
	for name := range workflows {
		workflowNames = append(workflowNames, name)
	}
	sort.Strings(workflowNames)
	for _, name := range workflowNames {
		sb.WriteString(fmt.Sprintf("  - %s: %s\n", name, workflows[name]))
	}
	sb.WriteString("\n")

	// Commands
	sb.WriteString("## Commands\n\n")
	sb.WriteString("### Pipeline\n\n")
	sb.WriteString("| Command | When to use |\n")
	sb.WriteString("|---------|------------|\n")
	sb.WriteString("| `cobuild init <id>` | Submit a design/bug/task to the pipeline |\n")
	sb.WriteString("| `cobuild dispatch <task-id>` | Dispatch to a dispatched CoBuild agent (works for every phase) |\n")
	sb.WriteString("| **`cobuild next <id>`** | **Print the single next command to run for a pipeline — use when confused** |\n")
	sb.WriteString("| `cobuild dispatch-wave <design-id>` | Dispatch all ready tasks in a wave |\n")
	sb.WriteString("| `cobuild process-review <task-id>` | Process Gemini review → merge or re-dispatch |\n")
	sb.WriteString("| `cobuild merge <task-id>` | Merge an approved PR manually |\n")
	sb.WriteString("| `cobuild merge-design <design-id>` | Smart merge all PRs (conflict detection) |\n")
	sb.WriteString("| `cobuild deploy <design-id>` | Deploy affected services |\n")
	sb.WriteString("| `cobuild retro <design-id>` | Run retrospective |\n")
	sb.WriteString("| `cobuild status` | Show all active pipelines |\n")
	sb.WriteString("| `cobuild audit <id>` | View gate history and timeline |\n")
	sb.WriteString("| `cobuild show <id>` | Compact current state for one pipeline |\n")
	sb.WriteString("| `cobuild scan` | Refresh project anatomy (file index for agents) |\n")
	sb.WriteString("| `cobuild wait <id> [id...]` | Block until tasks reach target status (2h max) |\n")
	sb.WriteString("| `cobuild complete <task-id>` | **Run as your LAST action** if you ARE the dispatched agent |\n")
	sb.WriteString("\n")
	sb.WriteString("**Manual gate recording (Advanced — see below):** `cobuild review / decompose / investigate / gate` — use only when the gate work happened outside a dispatched agent session.\n\n")

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

	// How to run pipelines — canonical, minimal, no-Option-A framing.
	// The whole section is about "dispatch is the default, follow the
	// Next step line, if unsure run cobuild next". Everything else is in
	// the Advanced section below.
	sb.WriteString("## How to Run a Pipeline\n\n")
	sb.WriteString("**Default and only flow: dispatch.** Every phase has a skill, and `cobuild dispatch` spawns a dispatched CoBuild agent that reads the skill and executes it. You never do the work yourself.\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("cobuild init <id>          # if the pipeline doesn't exist yet\n")
	sb.WriteString("cobuild dispatch <id>      # start — spawns a dispatched CoBuild agent for the current phase\n")
	sb.WriteString("# → follow the `Next step:` line it prints\n")
	sb.WriteString("# → repeat until phase = done\n")
	sb.WriteString("```\n\n")
	sb.WriteString("**`cobuild dispatch` is phase-aware.** It reads the current pipeline phase and generates the right prompt automatically — readiness review for design, decomposition for decompose, investigation for investigate, implementation for tasks, and so on. One command advances the entire pipeline.\n\n")
	sb.WriteString("**If you are ever confused:** run `cobuild next <id>`. It prints the single concrete command for the current state. Do not try to infer it from the workflow table or your memory of which phase comes next — let the CLI tell you.\n\n")
	sb.WriteString("**Do not own any phase yourself.** Even if you (the orchestrator agent) *could* do the work inline — read the design, break it into tasks, record the gate — **don't**. That pattern exists to be used in genuinely exceptional cases (see Advanced below), not as a default shortcut. The dispatched agent model keeps your context lean and produces a clean audit trail.\n\n")

	// Bug workflow — single compact reference
	sb.WriteString("### Bug workflow note\n\n")
	sb.WriteString("**Most bugs** use the `fix` workflow — a single dispatched CoBuild agent investigates and fixes together in one session. **Complex bugs** (root cause unknown, multi-repo, data/security implications, non-obvious fix shape) should be labeled `needs-investigation` before `cobuild init` — this routes them to the `bug-complex` workflow with a read-only investigation phase first.\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("cobuild wi label add <bug-id> needs-investigation   # only if complex\n")
	sb.WriteString("cobuild init <bug-id>\n")
	sb.WriteString("cobuild dispatch <bug-id>    # follow the Next step: output from here\n")
	sb.WriteString("```\n\n")

	// Advanced — the old Option A, retained but clearly marked exceptional
	sb.WriteString("## Advanced: recording a gate without dispatching\n\n")
	sb.WriteString("**This is an exceptional path, not the default.** Use it only when the gate work genuinely happened outside a dispatched CoBuild agent session — for example, a design that was reviewed live with the developer in a meeting, or an investigation that was done by a human. For anything the pipeline can do, prefer `cobuild dispatch`.\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("cobuild review <id> --verdict pass --readiness 5 --body \"<findings>\"   # record design review\n")
	sb.WriteString("cobuild decompose <id> --verdict pass --body \"<task summary>\"          # record decomposition\n")
	sb.WriteString("cobuild investigate <id> --verdict pass --body \"<root cause>\"           # record investigation\n")
	sb.WriteString("```\n\n")
	sb.WriteString("These commands record the gate and advance the phase without dispatching. **If you find yourself reaching for them because you weren't sure whether decompose had a skill, stop and run `cobuild dispatch <id>` instead — every phase has one.**\n\n")

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
	sb.WriteString("### Non-code tasks\n\n")
	sb.WriteString("If the task was tagged `completion_mode: direct`, it is a non-code task. Use this only for work expected outside the repo/worktree, such as KB updates, config/data changes, or user-global state.\n\n")
	sb.WriteString("Your completion step does not change: still run `cobuild complete <task-id>` as the last action. CoBuild will use the direct path for `completion_mode: direct`; otherwise `code` remains the normal path, and if no mode was set CoBuild falls back to auto-detection. This does not change deploy behavior.\n\n")

	// Orchestrator lifecycle
	sb.WriteString("## Orchestrator Protocol\n\n")
	sb.WriteString("If you are the orchestrator agent (dispatching tasks, not executing them yourself),\n")
	sb.WriteString("**follow through the full lifecycle. Do not stop after dispatch.** See the \"Dispatch is not a handoff to the user\" section above for the common failure mode.\n\n")
	sb.WriteString("After dispatching tasks:\n\n")
	sb.WriteString("1. **Monitor** — use `cobuild audit <id>` or `cobuild status` for instant checks (do NOT use `cobuild wait` as a background task — it's a 2-hour blocking command)\n")
	sb.WriteString("2. **Process reviews** — run `cobuild process-review <task-id>` for each needs-review task. This automatically: waits for Gemini review, classifies findings, merges clean PRs, or re-dispatches agents for fixes. If it says \"Waiting\" — Gemini hasn't reviewed yet, retry after a few minutes.\n")
	sb.WriteString("3. **Report** — when the pipeline has advanced or completed, tell the user **what shipped** with specifics (shard IDs, PR URLs, gate verdicts). Not \"dispatched, let me know\". Not \"want me to review?\". Concrete outcome.\n")
	sb.WriteString("4. **Deploy** — do NOT deploy automatically. Run `cobuild deploy <id> --dry-run` to show which services would be affected, then **ask the user** for approval. On approval, run `cobuild deploy <id>` (triggers deploy commands from pipeline config with smoke tests and auto-rollback). Deploy touches production and is always a human decision.\n\n")
	sb.WriteString("Only pause for user input if there is an actual blocker: merge conflict, critical Gemini finding you can't resolve, a design decision, or deploy approval.\n\n")

	sb.WriteString("### Report format when work completes\n\n")
	sb.WriteString("When a dispatched agent's work has actually landed, return to the user with a short structured summary:\n\n")
	sb.WriteString("```\n")
	sb.WriteString("Completed <phase> for <work-item-id>.\n\n")
	sb.WriteString("- Child shards created: <cb-xxx, cb-yyy, ...>  (if decompose)\n")
	sb.WriteString("- PRs opened: <url1, url2, ...>                (if implement)\n")
	sb.WriteString("- PRs merged: <url1, url2, ...>                (if review/merge)\n")
	sb.WriteString("- Gate verdict: pass|fail round N              (always)\n")
	sb.WriteString("- Pipeline phase: <old> → <new>                (always)\n")
	sb.WriteString("- Next concrete action: cobuild <...>          (always)\n")
	sb.WriteString("```\n\n")
	sb.WriteString("Omit rows that don't apply. Do not embellish. Do not ask \"want me to continue?\" — if the next action isn't a deploy or a blocked state, just continue automatically.\n\n")

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
		"orchestrate": "Orchestrator-side pipeline driver (run-pipeline.md) — load this when asked to run a shard through CoBuild end-to-end",
		"design":      "Design evaluation",
		"decompose":   "Break designs into tasks",
		"fix":         "Single-session bug fix (investigate + implement)",
		"investigate": "Root cause analysis for needs-investigation bugs",
		"implement":   "Task dispatch and monitoring",
		"review":      "Code review",
		"done":        "Post-delivery retrospective",
		"shared":      "Cross-phase reference",
	}

	phaseOrder := []string{"orchestrate", "design", "decompose", "fix", "investigate", "implement", "review", "done", "shared"}
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
