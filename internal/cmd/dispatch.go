package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
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

		// Get or create worktree
		worktreePath, _ := conn.GetMetadata(ctx, taskID, "worktree_path")
		if worktreePath == "" {
			if dryRun {
				fmt.Println("[dry-run] Would create worktree for " + taskID)
				worktreePath = fmt.Sprintf("~/worktrees/<project>/%s", taskID)
			} else {
				repoRootForWT, _ := config.RepoForProject(projectName)
				if repoRootForWT == "" {
					return fmt.Errorf("no repo registered for project %q — run cobuild setup first", projectName)
				}
				var wtErr error
				worktreePath, wtErr = cbClient.CreateWorktree(ctx, taskID, repoRootForWT, projectName)
				if wtErr != nil {
					return fmt.Errorf("failed to create worktree: %v", wtErr)
				}
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

		promptBuilder.WriteString("## Instructions\n\n")
		promptBuilder.WriteString("Implement this task following the acceptance criteria above.\n\n")
		promptBuilder.WriteString("### On completion\n\n")

		step := 1
		if len(pCfg.Test) > 0 {
			promptBuilder.WriteString(fmt.Sprintf("%d. Run tests: `%s`\n", step, strings.Join(pCfg.Test, " && ")))
			step++
		}
		if len(pCfg.Build) > 0 {
			promptBuilder.WriteString(fmt.Sprintf("%d. Build: `%s`\n", step, strings.Join(pCfg.Build, " && ")))
			step++
		}
		promptBuilder.WriteString(fmt.Sprintf("%d. **Run `cobuild complete %s`** -- this commits remaining changes, pushes, creates the PR, appends evidence, and marks the task needs-review. Do this as your LAST action.\n", step, taskID))

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
		if err := config.WriteWorktreeCLAUDEMD(pCfg, repoRoot, worktreePath, "dispatch", "implement", extras, workItemFetcher); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not generate worktree CLAUDE.md: %v\n", err)
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
		claudeFlags := "--print"
		if pCfg.Dispatch.ClaudeFlags != "" {
			claudeFlags = pCfg.Dispatch.ClaudeFlags
		}

		if model := pCfg.ModelForPhase("implement", ""); model != "" {
			claudeFlags += " --model " + model
		}

		completeCmd := fmt.Sprintf("cobuild complete '%s'", strings.ReplaceAll(taskID, "'", "'\\''"))

		shellCmd := fmt.Sprintf("cd '%s' && COBUILD_DISPATCH=true claude %s \"$(cat '%s')\" ; rm -f '%s' ; %s",
			strings.ReplaceAll(worktreePath, "'", "'\\''"),
			claudeFlags,
			strings.ReplaceAll(promptPath, "'", "'\\''"),
			strings.ReplaceAll(promptPath, "'", "'\\''"),
			completeCmd)
		tmuxArgs := []string{"new-window", "-n", taskID, "-t", tmuxSession, shellCmd}

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

func init() {
	dispatchCmd.Flags().String("agent", "", "Override agent (default: from config)")
	dispatchCmd.Flags().Bool("dry-run", false, "Show what would be done without executing")
	dispatchWaveCmd.Flags().Bool("dry-run", false, "Show what would be done without executing")
	rootCmd.AddCommand(dispatchCmd)
	rootCmd.AddCommand(dispatchWaveCmd)
}
