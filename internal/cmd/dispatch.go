package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/worktree"
	"github.com/spf13/cobra"
)

var dispatchCmd = &cobra.Command{
	Use:   "dispatch <task-id>",
	Short: "Dispatch a task to an agent via tmux",
	Long:  `Spawns a Claude Code session in a tmux window with full context from the task and its parent design shard.`,
	Args:  cobra.ExactArgs(1),
	Example: `  cobuild dispatch pf-abc123
  cobuild dispatch pf-abc123 --agent mycroft
  cobuild dispatch pf-abc123 --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		agentOverride, _ := cmd.Flags().GetString("agent")

		agent := agentFlag
		if agentOverride != "" {
			agent = agentOverride
		}

		task, err := conn.Get(ctx, taskID)
		if err != nil {
			return fmt.Errorf("task not found: %s", taskID)
		}

		if task.Status == "in_progress" {
			return fmt.Errorf("task already dispatched (status: in_progress)")
		}
		if task.Status != "open" && task.Status != "ready" {
			return fmt.Errorf("task not dispatchable (status: %s, must be open or ready)", task.Status)
		}

		// Check blocked-by edges
		blockerEdges, err := conn.GetEdges(ctx, taskID, "outgoing", []string{"blocked-by"})
		if err != nil {
			return fmt.Errorf("failed to check blockers: %w", err)
		}
		var unsatisfied []string
		for _, e := range blockerEdges {
			if e.Status != "closed" {
				unsatisfied = append(unsatisfied, fmt.Sprintf("%s (%s)", e.ItemID, e.Status))
			}
		}
		if len(unsatisfied) > 0 {
			return fmt.Errorf("blockers not satisfied:\n  %s", strings.Join(unsatisfied, "\n  "))
		}

		// Determine target repo — multi-repo tasks may specify which repo they target
		targetRepo, _ := conn.GetMetadata(ctx, taskID, "repo")
		repoRootForWT := ""
		if targetRepo != "" {
			var repoErr error
			repoRootForWT, repoErr = config.RepoForProject(targetRepo)
			if repoErr != nil {
				return fmt.Errorf("task specifies repo %q but it's not in the registry (~/.cobuild/repos.yaml): %v", targetRepo, repoErr)
			}
			fmt.Printf("Target repo: %s (from task metadata: repo=%s)\n", repoRootForWT, targetRepo)
		} else {
			repoRootForWT, _ = config.RepoForProject(projectName)
			if repoRootForWT == "" {
				repoRootForWT = findRepoRoot()
			}
			fmt.Printf("Target repo: %s (from project: %s)\n", repoRootForWT, projectName)
		}

		// Get or create worktree
		worktreePath, _ := conn.GetMetadata(ctx, taskID, "worktree_path")
		if worktreePath != "" {
			// Verify existing worktree is still valid
			if err := worktree.Verify(ctx, worktreePath); err != nil {
				fmt.Printf("Existing worktree invalid (%v), recreating...\n", err)
				worktree.Remove(ctx, repoRootForWT, worktreePath, taskID)
				conn.SetMetadata(ctx, taskID, "worktree_path", "")
				worktreePath = ""
			}
		}
		if worktreePath == "" {
			if dryRun {
				fmt.Println("[dry-run] Would create worktree for " + taskID + " in " + repoRootForWT)
				worktreePath = fmt.Sprintf("~/worktrees/%s/%s", projectName, taskID)
			} else {
				fmt.Printf("Creating worktree for %s...\n", taskID)
				var wtErr error
				worktreePath, wtErr = worktree.Create(ctx, taskID, repoRootForWT, projectName)
				if wtErr != nil {
					return fmt.Errorf("failed to create worktree: %v", wtErr)
				}
				fmt.Printf("Worktree created: %s\n", worktreePath)
				if err := conn.SetMetadata(ctx, taskID, "worktree_path", worktreePath); err != nil {
					fmt.Printf("Warning: worktree created but failed to record path: %v\n", err)
				}
			}
		}

		// Pre-accept Claude Code's workspace trust dialog for the worktree.
		// Otherwise the agent blocks on "Is this a project you created or one you trust?"
		// See ~/.claude.json → projects → <path> → hasTrustDialogAccepted
		if !dryRun && worktreePath != "" {
			if err := ensureClaudeTrust(worktreePath); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not pre-accept workspace trust for %s: %v\n", worktreePath, err)
			}
		}

		// Get parent design context
		var designContext string
		parentEdges, err := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
		if err == nil && len(parentEdges) > 0 {
			parentID := parentEdges[0].ItemID
			parentItem, err := conn.Get(ctx, parentID)
			if err == nil {
				designContext = fmt.Sprintf("## Design Context (from %s)\n\n**%s**\n\n%s",
					parentItem.ID, parentItem.Title, parentItem.Content)
			}
		}

		// Build prompt
		var promptBuilder strings.Builder
		promptBuilder.WriteString(fmt.Sprintf("# Task: %s\n\n", task.Title))
		promptBuilder.WriteString(fmt.Sprintf("**Task ID:** %s\n", task.ID))
		promptBuilder.WriteString(fmt.Sprintf("**Agent:** %s\n\n", agent))
		promptBuilder.WriteString("## Task Content\n\n")
		promptBuilder.WriteString(task.Content)
		promptBuilder.WriteString("\n\n")
		if designContext != "" {
			promptBuilder.WriteString(designContext)
			promptBuilder.WriteString("\n\n")
		}

		repoRoot, _ := config.RepoForProject(projectName)
		pCfg, _ := config.LoadConfig(repoRoot)
		if pCfg == nil {
			pCfg, _ = config.LoadConfig(worktreePath)
		}
		if pCfg == nil {
			pCfg = config.DefaultConfig()
		}

		// Phase-aware instructions
		promptBuilder.WriteString("## Instructions\n\n")

		// Detect current phase from pipeline state; auto-create run if missing
		currentPhase := ""
		if cbStore != nil {
			run, err := cbStore.GetRun(ctx, task.ID)
			if err == nil && run != nil {
				currentPhase = run.CurrentPhase
			} else if errors.Is(err, store.ErrNotFound) {
				// No pipeline run — create one on the fly
				workflow := inferWorkflowFromType(task.Type)
				firstPhase := firstPhaseOf(workflow, pCfg)
				newRun, createErr := cbStore.CreateRunWithMode(ctx, task.ID, projectName, firstPhase, "manual")
				if createErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to auto-create pipeline run: %v\n", createErr)
				} else {
					currentPhase = newRun.CurrentPhase
					fmt.Printf("Auto-created pipeline run for %s (workflow: %s, phase: %s)\n", task.ID, workflow, currentPhase)
				}
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to look up pipeline run: %v\n", err)
			}
		}
		// Fallback if store unavailable or run creation failed
		if currentPhase == "" {
			switch task.Type {
			case "bug":
				currentPhase = "investigate"
			case "design":
				currentPhase = "design"
			default:
				currentPhase = "implement"
			}
		}

		writePhasePrompt(&promptBuilder, currentPhase, task.ID, taskID, pCfg)

		prompt := promptBuilder.String()

		// Generate worktree CLAUDE.md
		extras := map[string]string{
			"dispatch-prompt": prompt,
			"parent-design":   designContext,
		}
		workItemFetcher := func(id string) (string, string, error) {
			s, err := conn.Get(ctx, id)
			if err != nil {
				return "", "", err
			}
			return s.Title, s.Content, nil
		}
		// Assemble context and write to worktree as a separate file.
		// IMPORTANT: Do NOT overwrite CLAUDE.md — the repo's original CLAUDE.md has
		// project instructions (build, deploy, etc.) that agents need. Overwriting it
		// with assembled context confuses agents into thinking the context dump IS the
		// project instructions. Instead, write to .cobuild/dispatch-context.md and
		// append a pointer to the worktree's CLAUDE.md.
		//
		// Skip entirely in dry-run mode: worktreePath is a literal "~/..." string
		// in dry-run and MkdirAll would create a directory literally named "~".
		assembledContext, _ := config.AssembleContext(pCfg, repoRoot, "dispatch", currentPhase, extras, workItemFetcher)
		if !dryRun && assembledContext != "" {
			contextDir := filepath.Join(worktreePath, ".cobuild")
			if err := os.MkdirAll(contextDir, 0755); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not create %s: %v\n", contextDir, err)
			} else {
				contextPath := filepath.Join(contextDir, "dispatch-context.md")
				if err := os.WriteFile(contextPath, []byte(assembledContext), 0644); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not write dispatch context: %v\n", err)
				}
			}

			// Append a CoBuild dispatch section to the worktree CLAUDE.md (preserving original).
			// Distinguish "file does not exist" (fine, start from empty) from real read errors
			// (e.g., permission denied) — the latter must NOT silently overwrite the file.
			claudeMDPath := filepath.Join(worktreePath, "CLAUDE.md")
			existing, readErr := os.ReadFile(claudeMDPath)
			if readErr != nil && !os.IsNotExist(readErr) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not read %s (%v) — leaving untouched to avoid data loss\n", claudeMDPath, readErr)
			} else {
				dispatchSection := "\n\n## CoBuild Dispatch Context\n\n" +
					"You are a dispatched CoBuild agent. Your task prompt was passed as the initial message.\n" +
					"Additional context is in `.cobuild/dispatch-context.md` — read it if you need architecture, " +
					"design context, or project anatomy.\n"
				if err := os.WriteFile(claudeMDPath, append(existing, []byte(dispatchSection)...), 0644); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not update worktree CLAUDE.md: %v\n", err)
				}
			}
		}

		// Write Stop hook settings so cobuild complete runs automatically when agent stops
		if !dryRun {
			settingsDir := filepath.Join(worktreePath, ".claude")
			if mkErr := os.MkdirAll(settingsDir, 0755); mkErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not create .claude/ directory: %v\n", mkErr)
			} else {
				settingsContent := `{
  "hooks": {
    "Stop": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "cobuild complete \"$COBUILD_TASK_ID\" --auto"
      }]
    }]
  }
}`
				if err := os.WriteFile(filepath.Join(settingsDir, "settings.local.json"), []byte(settingsContent), 0644); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not write .claude/settings.local.json: %v\n", err)
				}
			}
		}

		// Write prompt to temp file
		promptFile, err := os.CreateTemp("", fmt.Sprintf("cobuild-dispatch-%s-*.md", taskID))
		if err != nil {
			return fmt.Errorf("failed to create prompt file: %v", err)
		}
		if _, err := promptFile.WriteString(prompt); err != nil {
			promptFile.Close()
			return fmt.Errorf("failed to write prompt file: %v", err)
		}
		promptFile.Close()
		promptPath := promptFile.Name()

		tmuxSession := fmt.Sprintf("cobuild-%s", projectName)
		if pCfg.Dispatch.TmuxSession != "" {
			tmuxSession = pCfg.Dispatch.TmuxSession
		}

		// Ensure tmux session exists, create if not
		if err := exec.CommandContext(ctx, "tmux", "has-session", "-t", tmuxSession).Run(); err != nil {
			if createErr := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", tmuxSession).Run(); createErr != nil {
				return fmt.Errorf("failed to create tmux session %q: %v", tmuxSession, createErr)
			}
		}
		// Use interactive mode by default — proven to work for multi-turn implementation.
		// -p mode works but was unreliable earlier. Keep interactive as the safe default.
		claudeFlags := "--dangerously-skip-permissions"
		if pCfg.Dispatch.ClaudeFlags != "" {
			claudeFlags = pCfg.Dispatch.ClaudeFlags
		}
		if model := pCfg.ModelForPhase("implement", ""); model != "" {
			claudeFlags += " --model " + model
		} else if pCfg.Dispatch.DefaultModel != "" {
			claudeFlags += " --model " + pCfg.Dispatch.DefaultModel
		}

		completeCmd := fmt.Sprintf("cobuild complete '%s'", strings.ReplaceAll(taskID, "'", "'\\''"))

		// Write a dispatch script — tmux new-window can't handle pipes/stdin
		// The prompt must be a positional argument, not piped via stdin
		scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("cobuild-run-%s.sh", taskID))
		// Get session ID if it was stored
		sessionID := ""
		if conn != nil {
			sessionID, _ = conn.GetMetadata(ctx, taskID, "session_id")
		}

		scriptContent := fmt.Sprintf(`#!/bin/bash
cd '%s'
export COBUILD_DISPATCH=true
export COBUILD_SESSION_ID='%s'
export COBUILD_HOOKS_DIR='%s'
export COBUILD_TASK_ID='%s'
LOGFILE=".cobuild/dispatch.log"
mkdir -p .cobuild
echo "$COBUILD_SESSION_ID" > .cobuild/session_id
echo "[$(date)] Dispatch starting (session: $COBUILD_SESSION_ID)" >> "$LOGFILE"

# Load prompt from temp file
PROMPT_FILE='%s'
if [ ! -f "$PROMPT_FILE" ]; then
    echo "[$(date)] ERROR: Prompt file not found: $PROMPT_FILE" >> "$LOGFILE"
    exit 1
fi

# Save a copy for debugging
cp "$PROMPT_FILE" .cobuild/last-prompt.md
PROMPT=$(cat "$PROMPT_FILE")
echo "[$(date)] Prompt loaded (${#PROMPT} chars)" >> "$LOGFILE"
rm -f "$PROMPT_FILE"

# Run claude in interactive mode (proven reliable for multi-turn work)
claude %s "$PROMPT"
echo "[$(date)] Claude session ended" >> "$LOGFILE"

# Parse transcript for token/cost data after session ends
# The transcript JSONL has usage data in each API response
TRANSCRIPT_DIR="$HOME/.claude/projects"
TRANSCRIPT=$(find "$TRANSCRIPT_DIR" -name "*.jsonl" -newer "$LOGFILE" -type f 2>/dev/null | head -1)
if [ -n "$TRANSCRIPT" ] && command -v jq &>/dev/null; then
    # Extract usage from the last result message in the transcript
    USAGE=$(tail -100 "$TRANSCRIPT" | grep '"usage"' | tail -1 | jq -c '.usage // empty' 2>/dev/null)
    if [ -n "$USAGE" ]; then
        echo "[$(date)] Transcript usage: $USAGE" >> "$LOGFILE"
    fi
fi`,
			strings.ReplaceAll(worktreePath, "'", "'\\''"),
			sessionID,
			filepath.Join(findRepoRoot(), "hooks"),
			strings.ReplaceAll(taskID, "'", "'\\''"),
			strings.ReplaceAll(promptPath, "'", "'\\''"),
			claudeFlags,
		)
		scriptContent += fmt.Sprintf(`

# Cleanup script
rm -f '%s'

# Run completion (commit, push, create PR, mark needs-review)
%s
`,
			strings.ReplaceAll(scriptPath, "'", "'\\''"),
			completeCmd,
		)
		if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
			return fmt.Errorf("failed to write dispatch script: %v", err)
		}
		tmuxArgs := []string{"new-window", "-n", taskID, "-t", tmuxSession, "bash", scriptPath}

		if dryRun {
			fmt.Printf("=== Task ===\n")
			fmt.Printf("ID:     %s\n", task.ID)
			fmt.Printf("Title:  %s\n", task.Title)
			fmt.Printf("Status: %s\n", task.Status)
			fmt.Printf("Agent:  %s\n\n", agent)
			fmt.Printf("=== Worktree ===\n")
			fmt.Printf("Path: %s\n\n", worktreePath)
			if designContext != "" {
				fmt.Printf("=== Design Context ===\n")
				fmt.Printf("%s\n\n", designContext)
			}
			fmt.Printf("=== Prompt ===\n")
			fmt.Printf("%s\n\n", prompt)
			fmt.Printf("=== tmux Command ===\n")
			fmt.Printf("tmux %s\n", strings.Join(tmuxArgs, " "))
			return nil
		}

		// Set task status
		if conn != nil {
			if err := conn.UpdateStatus(ctx, taskID, "in_progress"); err != nil {
				return fmt.Errorf("failed to set status to in_progress: %v", err)
			}
		}

		// Spawn tmux
		tmuxOut, err := exec.CommandContext(ctx, "tmux", tmuxArgs...).CombinedOutput()
		if err != nil {
			if conn != nil {
				_ = conn.UpdateStatus(ctx, taskID, task.Status)
			}
			return fmt.Errorf("failed to spawn tmux window: %s\n%s", err, string(tmuxOut))
		}

		// Capture output
		logDir := filepath.Join(worktreePath, ".cobuild")
		os.MkdirAll(logDir, 0755)
		logFile := filepath.Join(logDir, "session.log")
		exec.CommandContext(ctx, "tmux", "pipe-pane", "-t", fmt.Sprintf("%s:%s", tmuxSession, taskID),
			fmt.Sprintf("cat >> '%s'", strings.ReplaceAll(logFile, "'", "'\\''"))).Run()

		// Record session in store for analytics
		if cbStore != nil {
			pipelineID := ""
			designID := ""
			// Find parent design for this task
			parentEdges, _ := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
			if len(parentEdges) > 0 {
				designID = parentEdges[0].ItemID
				if run, err := cbStore.GetRun(ctx, designID); err == nil {
					pipelineID = run.ID
				}
			}
			if designID == "" {
				designID = taskID // bug dispatched directly
			}

			session, err := cbStore.CreateSession(ctx, store.SessionInput{
				PipelineID:       pipelineID,
				DesignID:         designID,
				TaskID:           taskID,
				Phase:            currentPhase,
				Project:          projectName,
				Model:            pCfg.Dispatch.DefaultModel,
				PromptChars:      len(prompt),
				Prompt:           prompt,
				AssembledContext: assembledContext,
				WorktreePath:     worktreePath,
				TmuxSession:      tmuxSession,
				TmuxWindow:       taskID,
			})
			if err != nil {
				fmt.Printf("Warning: failed to record session: %v\n", err)
			} else {
				// Store session ID in work item metadata so complete can find it
				conn.SetMetadata(ctx, taskID, "session_id", session.ID)
			}
		}

		// Record dispatch metadata
		dispatchInfo := map[string]any{
			"dispatched_at": time.Now().UTC().Format(time.RFC3339),
			"agent":         agent,
			"tmux_window":   taskID,
			"log_file":      logFile,
		}
		if err := conn.UpdateMetadataMap(ctx, taskID, dispatchInfo); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: dispatched but failed to update metadata: %v\n", err)
		}

		if outputFormat == "json" {
			out := map[string]any{
				"task_id":       taskID,
				"agent":         agent,
				"tmux_session":  tmuxSession,
				"worktree_path": worktreePath,
				"tmux_window":   taskID,
				"dispatched_at": dispatchInfo["dispatched_at"],
			}
			s, _ := client.FormatJSON(out)
			fmt.Println(s)
			return nil
		}

		fmt.Printf("Dispatched %s to %s\n", taskID, agent)
		fmt.Printf("  Session:  %s\n", tmuxSession)
		fmt.Printf("  Worktree: %s\n", worktreePath)
		fmt.Printf("  Window:   %s\n", taskID)
		return nil
	},
}

var dispatchWaveCmd = &cobra.Command{
	Use:   "dispatch-wave <design-id>",
	Short: "Dispatch the next wave of ready tasks for a design",
	Long: `Finds all tasks for a design whose blockers are satisfied and dispatches them.
Tasks are dispatched up to the max_concurrent limit from pipeline config.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if cbClient == nil {
			return fmt.Errorf("no client configured")
		}
		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		designID := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		// Get all child tasks
		edges, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
		if err != nil {
			return fmt.Errorf("get child tasks: %w", err)
		}

		var ready []string
		var inProgress []string
		var blocked []string

		for _, e := range edges {
			if e.Status == "closed" || e.Status == "needs-review" {
				continue // already done or in review
			}
			if e.Status == "in_progress" {
				inProgress = append(inProgress, e.ItemID)
				continue
			}

			// Check if blockers are satisfied
			blockerEdges, err := conn.GetEdges(ctx, e.ItemID, "outgoing", []string{"blocked-by"})
			if err != nil {
				continue
			}
			allSatisfied := true
			for _, b := range blockerEdges {
				if b.Status != "closed" {
					allSatisfied = false
					break
				}
			}
			if allSatisfied {
				ready = append(ready, e.ItemID)
			} else {
				blocked = append(blocked, e.ItemID)
			}
		}

		if len(ready) == 0 {
			if len(inProgress) > 0 {
				fmt.Printf("No new tasks to dispatch. %d still in progress.\n", len(inProgress))
			} else if len(blocked) > 0 {
				fmt.Printf("No tasks ready. %d blocked.\n", len(blocked))
			} else {
				fmt.Println("All tasks complete.")
			}
			return nil
		}

		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)
		maxConcurrent := 3
		if pCfg != nil && pCfg.Dispatch.MaxConcurrent > 0 {
			maxConcurrent = pCfg.Dispatch.MaxConcurrent
		}

		// Limit by max_concurrent minus currently running
		available := maxConcurrent - len(inProgress)
		if available <= 0 {
			fmt.Printf("At capacity: %d tasks in progress (max %d).\n", len(inProgress), maxConcurrent)
			return nil
		}
		if len(ready) > available {
			ready = ready[:available]
		}

		fmt.Printf("Dispatching %d tasks (wave) for %s:\n", len(ready), designID)
		for _, taskID := range ready {
			if dryRun {
				fmt.Printf("  [dry-run] %s\n", taskID)
				continue
			}
			// Run dispatch for each task via the existing dispatch command logic
			fmt.Printf("  %s ", taskID)
			dispatchArgs := []string{"dispatch", taskID}
			subCmd, _, err := rootCmd.Find(dispatchArgs)
			if err != nil || subCmd == nil {
				fmt.Printf("— failed to find dispatch command\n")
				continue
			}
			subCmd.SetArgs([]string{taskID})
			if err := subCmd.RunE(subCmd, []string{taskID}); err != nil {
				fmt.Printf("— error: %v\n", err)
			}
		}

		if len(blocked) > 0 {
			fmt.Printf("\n%d tasks still blocked.\n", len(blocked))
		}
		return nil
	},
}

// writePhasePrompt writes phase-appropriate instructions into the dispatch prompt.
func writePhasePrompt(b *strings.Builder, phase, workItemID, taskID string, pCfg *config.Config) {
	switch phase {
	case "design":
		b.WriteString("**Evaluate this design for pipeline readiness.**\n\n")
		b.WriteString("Follow the gate-readiness-review skill:\n")
		b.WriteString("1. Read the design content above\n")
		b.WriteString("2. Check 5 readiness criteria: problem stated, user identified, success criteria, scope boundaries, links to parent\n")
		b.WriteString("3. Run implementability check — can an agent build this without asking questions?\n")
		b.WriteString("4. Score readiness (1-5) and determine verdict\n")
		b.WriteString("5. Record the review:\n")
		b.WriteString(fmt.Sprintf("   `cobuild review %s --verdict pass|fail --readiness <N> --body \"<findings>\"`\n", workItemID))

	case "decompose":
		b.WriteString("**Break this design into implementable tasks.**\n\n")
		b.WriteString("Follow the decompose-design skill:\n")
		b.WriteString("1. Read the design content above\n")
		b.WriteString("2. Identify discrete tasks — each completable in a single agent session (1-5 files, ~100-300 lines)\n")
		b.WriteString("3. Order by dependency — assign wave numbers (wave 1 has no blockers, wave 2 depends on wave 1)\n")
		b.WriteString("4. For each task, create a work item with scope, acceptance criteria, and code locations:\n")
		b.WriteString(fmt.Sprintf("   `cobuild wi create --type task --title \"<title>\" --body \"<spec>\" --parent %s`\n", workItemID))
		b.WriteString("5. Link dependencies between tasks:\n")
		b.WriteString("   `cobuild wi links add <task-id> <blocker-id> blocked-by`\n")
		b.WriteString("6. Record the decomposition gate:\n")
		b.WriteString(fmt.Sprintf("   `cobuild decompose %s --verdict pass --body \"<summary>\"`\n", workItemID))
		b.WriteString("\n**Important:** Assign migration numbers explicitly if multiple tasks create DB migrations. Set `repo` metadata on tasks for multi-repo projects.\n")

	case "investigate":
		b.WriteString("**This is a READ-ONLY investigation. Do NOT modify source code.**\n\n")
		b.WriteString("Follow the bug-investigation skill:\n")
		b.WriteString("1. Understand the bug report above\n")
		b.WriteString("2. Reproduce and verify the bug\n")
		b.WriteString("3. Trace the root cause — check code, git blame, database state\n")
		b.WriteString("4. Map all affected files and related patterns\n")
		b.WriteString("5. Assess fragility — why did this area break?\n")
		b.WriteString("6. Write an investigation report and append to the bug:\n")
		b.WriteString(fmt.Sprintf("   `cobuild wi append %s --body \"## Investigation Report\\n...\"`\n", workItemID))
		b.WriteString("7. Record the investigation gate:\n")
		b.WriteString(fmt.Sprintf("   `cobuild investigate %s --verdict pass --body \"<summary>\"`\n", workItemID))
		b.WriteString("8. Create a fix task with the exact changes needed:\n")
		b.WriteString(fmt.Sprintf("   `cobuild wi create --type task --title \"Fix: ...\" --body \"...\" --parent %s`\n", workItemID))

	case "review":
		b.WriteString("**Review this PR against its task spec and parent design.**\n\n")
		b.WriteString("Follow the gate-process-review or gate-review-pr skill:\n")
		b.WriteString("1. Read the task spec and parent design above\n")
		b.WriteString("2. Check CI status: `gh pr checks <pr-number>`\n")
		b.WriteString("3. Read the PR diff: `gh pr diff <pr-number>`\n")
		b.WriteString("4. Evaluate: does it match the spec? Does it fit the design? Will it break anything?\n")
		b.WriteString("5. Check for hardcoded values that should be configurable (project-specific constraints)\n")
		b.WriteString("6. Record the verdict:\n")
		b.WriteString(fmt.Sprintf("   `cobuild gate %s review --verdict pass|fail --body \"<findings>\"`\n", workItemID))

	case "done":
		b.WriteString("**Run a pipeline retrospective.**\n\n")
		b.WriteString("Follow the gate-retrospective skill:\n")
		b.WriteString("1. Gather execution data: `cobuild audit " + workItemID + "`\n")
		b.WriteString("2. Review each gate — how many rounds, what failed, was it avoidable?\n")
		b.WriteString("3. Review implementation — did agents complete without intervention?\n")
		b.WriteString("4. Identify patterns — repeated failures, agent gaps, process friction\n")
		b.WriteString("5. Record the retrospective:\n")
		b.WriteString(fmt.Sprintf("   `cobuild retro %s --body \"<findings>\"`\n", workItemID))

	default:
		// Implementation (default for tasks and unknown phases)
		b.WriteString("Implement this task following the acceptance criteria above.\n\n")
		b.WriteString("### On completion\n\n")

		step := 1
		if pCfg != nil && len(pCfg.Test) > 0 {
			b.WriteString(fmt.Sprintf("%d. Run tests: `%s`\n", step, strings.Join(pCfg.Test, " && ")))
			step++
		}
		if pCfg != nil && len(pCfg.Build) > 0 {
			b.WriteString(fmt.Sprintf("%d. Build: `%s`\n", step, strings.Join(pCfg.Build, " && ")))
			step++
		}
		b.WriteString(fmt.Sprintf("%d. **Run `cobuild complete %s`** -- this commits remaining changes, pushes, creates the PR, appends evidence, and marks the task needs-review. Do this as your LAST action.\n\n", step, taskID))
		b.WriteString("**IMPORTANT RULES:**\n")
		b.WriteString("- NEVER use raw `git merge` or `git push` to main — always use `cobuild complete` which creates a PR\n")
		b.WriteString("- NEVER merge PRs yourself — the orchestrating agent handles merge via `cobuild merge` after review\n")
		b.WriteString("- If a reviewer (Gemini, human) leaves a critical comment on your PR, you MUST address it before the PR can merge\n")
		b.WriteString("- Check review comments: `gh pr view <pr-number> --comments`\n")
	}
}

// inferWorkflowFromType maps a work item type to a workflow name.
func inferWorkflowFromType(wiType string) string {
	switch wiType {
	case "bug", "design", "task":
		return wiType
	default:
		return "task"
	}
}

// firstPhaseOf returns the first phase of the named workflow from config.
// Falls back to hardcoded defaults if the workflow is not defined in config.
func firstPhaseOf(workflow string, cfg *config.Config) string {
	if cfg != nil {
		if wf, ok := cfg.Workflows[workflow]; ok && len(wf.Phases) > 0 {
			return wf.Phases[0]
		}
	}
	switch workflow {
	case "bug":
		return "investigate"
	case "design":
		return "design"
	default:
		return "implement"
	}
}

// ensureClaudeTrust pre-accepts Claude Code's workspace trust dialog for a
// directory by editing ~/.claude.json. This avoids dispatched agents blocking
// on "Is this a project you created or one you trust?" in fresh worktrees.
//
// Concurrency: read-modify-write is guarded by an flock on a sibling lock
// file so concurrent `cobuild dispatch` processes (e.g. from dispatch-wave)
// cannot clobber each other's updates. The lock is released automatically
// when the function returns (file close).
//
// The file is read, the specific project entry is added/updated, and the
// whole file is written back atomically (temp file + rename). If the file
// doesn't exist or can't be parsed, we return an error rather than creating
// one from scratch — that's Claude Code's job.
func ensureClaudeTrust(worktreePath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	configPath := filepath.Join(home, ".claude.json")
	lockPath := configPath + ".cobuild-lock"

	// Acquire exclusive advisory lock for the whole read-modify-write cycle.
	// Using a sibling .cobuild-lock file (not the config itself) avoids any
	// interaction with Claude Code's own file handles on .claude.json.
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}

	// Decode into a generic map so we preserve unknown fields on write-back.
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}

	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		projects = make(map[string]any)
		cfg["projects"] = projects
	}

	entry, _ := projects[worktreePath].(map[string]any)
	if entry == nil {
		entry = make(map[string]any)
		projects[worktreePath] = entry
	}

	// Only write if not already trusted — avoids gratuitous file churn
	if accepted, _ := entry["hasTrustDialogAccepted"].(bool); accepted {
		return nil
	}

	entry["hasTrustDialogAccepted"] = true
	// Provide reasonable defaults for a fresh entry so onboarding doesn't trigger either
	if _, ok := entry["allowedTools"]; !ok {
		entry["allowedTools"] = []any{}
	}
	if _, ok := entry["hasCompletedProjectOnboarding"]; !ok {
		entry["hasCompletedProjectOnboarding"] = true
	}
	if _, ok := entry["projectOnboardingSeenCount"]; !ok {
		entry["projectOnboardingSeenCount"] = 1
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	// Atomic write: temp file in the same directory, then rename
	tmp, err := os.CreateTemp(filepath.Dir(configPath), ".claude.json.tmp.*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, configPath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func init() {
	dispatchCmd.Flags().String("agent", "", "Override agent (default: from config)")
	dispatchCmd.Flags().Bool("dry-run", false, "Show what would be done without executing")
	dispatchWaveCmd.Flags().Bool("dry-run", false, "Show what would be done without executing")
	rootCmd.AddCommand(dispatchCmd)
	rootCmd.AddCommand(dispatchWaveCmd)
}
