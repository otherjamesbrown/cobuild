package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	cobuildruntime "github.com/otherjamesbrown/cobuild/internal/runtime"
	_ "github.com/otherjamesbrown/cobuild/internal/runtime/claudecode" // register claude-code runtime
	_ "github.com/otherjamesbrown/cobuild/internal/runtime/codex"      // register codex runtime
	_ "github.com/otherjamesbrown/cobuild/internal/runtime/stub"       // register stub runtime
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/otherjamesbrown/cobuild/internal/worktree"
	"github.com/spf13/cobra"
)

// dispatchContextWarnBytes is the assembled-context size above which dispatch
// emits a warning pointing the user at `cobuild context audit`. Large contexts
// correlate with degraded agent output and wasted tokens; see cb-4cd9c6.
const dispatchContextWarnBytes = 30 * 1024

var dispatchCmd = &cobra.Command{
	Use:   "dispatch <task-id>",
	Short: "Dispatch a task to an agent via tmux",
	Long: `Spawns an agent session (Claude Code or OpenAI Codex) in a tmux window
with full context from the task and its parent design shard. The runtime
is chosen by --runtime flag > task metadata "dispatch_runtime" > pipeline
config "dispatch.default_runtime" > "claude-code".`,
	Args: cobra.ExactArgs(1),
	Example: `  cobuild dispatch pf-abc123
  cobuild dispatch pf-abc123 --agent mycroft
  cobuild dispatch pf-abc123 --runtime codex
  cobuild dispatch pf-abc123 --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		taskID := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		foreground, _ := cmd.Flags().GetBool("foreground")
		agentOverride, _ := cmd.Flags().GetString("agent")

		agent := agentFlag
		if agentOverride != "" {
			agent = agentOverride
		}

		task, err := conn.Get(ctx, taskID)
		if err != nil {
			return fmt.Errorf("task not found: %s", taskID)
		}

		resolvedState, err := pipelinestate.Resolve(ctx, taskID)
		if err != nil && !errors.Is(err, pipelinestate.ErrNotFound) {
			return fmt.Errorf("resolve pipeline state for %s: %w", taskID, err)
		}
		dispatchStatus := task.Status
		if resolvedState != nil && resolvedState.WorkItem != nil && resolvedState.WorkItem.Status != "" {
			dispatchStatus = resolvedState.WorkItem.Status
		}
		if conflict := dispatchConflictFromResolvedState(taskID, resolvedState); conflict != nil {
			// Self-heal: a conflict on a session that has no live tmux window
			// and no heartbeat is stale state from a prior orchestrator that
			// died. Recover it in place rather than forcing the operator to
			// run `cobuild recover` manually (cb-d5e1dd #4).
			recovered, reason, rerr := recoverDeadAgent(ctx, taskID)
			if rerr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: self-heal probe for %s failed: %v\n", taskID, rerr)
			}
			if !recovered {
				return fmt.Errorf("task not dispatchable: conflict in %s: %s", conflict.Source, conflict.Detail)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Self-healed stale conflict on %s: %s\n", taskID, reason)
			// Re-resolve so the rest of dispatch sees the cleaned state.
			resolvedState, _ = pipelinestate.Resolve(ctx, taskID)
			if resolvedState != nil && resolvedState.WorkItem != nil && resolvedState.WorkItem.Status != "" {
				dispatchStatus = resolvedState.WorkItem.Status
			}
		}

		if dispatchStatus == "in_progress" {
			fmt.Printf("Re-dispatching %s (status was in_progress, no live session).\n", taskID)
		} else if dispatchStatus == "needs-review" {
			// Expected state for review-phase dispatches: cobuild complete
			// sets status=needs-review before handing off to review, so the
			// dispatched review agent's target IS exactly this. Rejecting
			// needs-review blocked every review-phase retry (cb-2fd178).
			fmt.Printf("Dispatching %s for review (status was needs-review).\n", taskID)
		} else if dispatchStatus != "open" && dispatchStatus != "ready" {
			return fmt.Errorf("task not dispatchable (status: %s, must be open, ready, in_progress, or needs-review)", dispatchStatus)
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

		taskProject := resolveTaskProject(task)
		repoTarget, err := resolveDispatchTargetRepo(ctx, task, taskID, taskProject, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		repoRootForWT := repoTarget.Root
		fmt.Printf("Target repo: %s (from %s)\n", repoRootForWT, repoTarget.Source)

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
				worktreePath = fmt.Sprintf("~/worktrees/%s/%s", taskProject, taskID)
			} else {
				fmt.Printf("Creating worktree for %s...\n", taskID)
				var wtErr error
				worktreePath, wtErr = worktree.Create(ctx, taskID, repoRootForWT, taskProject)
				if wtErr != nil {
					return fmt.Errorf("failed to create worktree: %v", wtErr)
				}
				fmt.Printf("Worktree created: %s\n", worktreePath)
				if err := conn.SetMetadata(ctx, taskID, "worktree_path", worktreePath); err != nil {
					fmt.Printf("Warning: worktree created but failed to record path: %v\n", err)
				}
			}
		}

		// Runtime-specific pre-dispatch hook runs later, after we've resolved
		// which runtime to use (needs pCfg). See rt.PreDispatch below.

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

		// Build prompt.
		// For gate phases (design, decompose, review, done, investigate) the
		// instructions go BEFORE the content so non-interactive runtimes like
		// Codex see them first. Implementation phases keep the original order
		// (content → instructions) because the agent needs to read the spec
		// before seeing "implement this".
		var promptBuilder strings.Builder
		promptBuilder.WriteString(fmt.Sprintf("# Task: %s\n\n", task.Title))
		promptBuilder.WriteString(fmt.Sprintf("**Task ID:** %s\n", task.ID))
		promptBuilder.WriteString(fmt.Sprintf("**Agent:** %s\n\n", agent))

		repoRoot, _ := config.RepoForProject(taskProject)
		pCfg, _ := config.LoadConfig(repoRoot)
		if pCfg == nil {
			pCfg, _ = config.LoadConfig(worktreePath)
		}
		if pCfg == nil {
			pCfg = config.DefaultConfig()
		}

		// Resolve which runtime will handle this dispatch.
		// Priority: --runtime flag > task metadata > pCfg.Dispatch.DefaultRuntime > "claude-code".
		runtimeFlag, _ := cmd.Flags().GetString("runtime")
		runtimeMeta := ""
		if conn != nil {
			runtimeMeta, _ = conn.GetMetadata(ctx, taskID, "dispatch_runtime")
		}
		rtName := pCfg.ResolveRuntime(runtimeFlag, runtimeMeta)
		rt, err := cobuildruntime.Get(rtName)
		if err != nil {
			return fmt.Errorf("invalid runtime %q: %v", rtName, err)
		}
		fmt.Printf("Runtime: %s\n", rt.Name())

		// Runtime-specific pre-dispatch: for claude-code this pre-accepts
		// the workspace trust dialog in ~/.claude.json; for codex it's a
		// no-op. Failures are logged as warnings — the dispatch still
		// proceeds so the operator can see the agent-side behavior.
		if !dryRun && worktreePath != "" {
			if err := rt.PreDispatch(ctx, worktreePath); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s pre-dispatch failed for %s: %v\n", rt.Name(), worktreePath, err)
			}
		}

		// Detect current phase from pipeline state; auto-create run if missing.
		// Phase detection must happen before prompt assembly because gate phases
		// put instructions BEFORE content (so Codex reads them first).
		currentPhase := ""
		if cbStore != nil {
			run, err := cbStore.GetRun(ctx, task.ID)
			if err == nil && run != nil {
				currentPhase = run.CurrentPhase
			} else if errors.Is(err, store.ErrNotFound) {
				// No pipeline run — create one on the fly
				bootstrap, resolveErr := pipelinestate.ResolveBootstrap(task, pCfg)
				if resolveErr != nil {
					return fmt.Errorf("resolve pipeline bootstrap for %s: %w", task.ID, resolveErr)
				}
				newRun, createErr := cbStore.CreateRunWithMode(ctx, task.ID, taskProject, bootstrap.StartPhase, "manual")
				if createErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to auto-create pipeline run: %v\n", createErr)
				} else {
					currentPhase = newRun.CurrentPhase
					fmt.Printf("Auto-created pipeline run for %s (workflow: %s, phase: %s)\n", task.ID, bootstrap.Workflow, currentPhase)
				}
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to look up pipeline run: %v\n", err)
			}
		}
		// Fallback if store unavailable or run creation failed
		if currentPhase == "" {
			bootstrap, err := pipelinestate.ResolveBootstrap(task, pCfg)
			if err != nil {
				return fmt.Errorf("resolve pipeline bootstrap for %s: %w", task.ID, err)
			}
			currentPhase = bootstrap.StartPhase
		}

		var mergedTasks []MergedTask
		if currentPhase == "decompose" {
			mergedTasks, err = collectMergedTasks(ctx, conn, task.ID, repoRootForWT)
			if err != nil {
				return fmt.Errorf("load merged work for decompose prompt: %w", err)
			}
		}

		// Belt-and-braces: if the bug body already contains investigation content,
		// downgrade from investigate to fix regardless of phase inference.
		// Also persist the override to pipeline_runs so `cobuild status` reflects
		// the phase the agent actually ran (cb-eab697).
		if currentPhase == "investigate" && hasInvestigationContent(task.Content) {
			fmt.Printf("Notice: bug %s already has investigation content — routing to fix phase instead\n", task.ID)
			currentPhase = "fix"
			if cbStore != nil {
				if err := advancePipelinePhaseTo(ctx, cbStore, task.ID, "investigate", "fix"); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not update pipeline run phase to fix: %v\n", err)
				}
			}
		}

		// For gate phases, put instructions FIRST so non-interactive runtimes
		// (Codex) see "evaluate this, run cobuild review" before the long
		// design content. For implementation phases, content comes first so
		// the agent reads the spec before the "implement this" instructions.
		if cobuildruntime.IsGatePhase(currentPhase) {
			promptBuilder.WriteString("## Instructions\n\n")
			writePhasePrompt(&promptBuilder, currentPhase, task.ID, taskID, pCfg)
			promptBuilder.WriteString("\n## Content to Evaluate\n\n")
			promptBuilder.WriteString(task.Content)
			promptBuilder.WriteString("\n\n")
			if designContext != "" {
				promptBuilder.WriteString(designContext)
				promptBuilder.WriteString("\n\n")
			}
			if currentPhase == "decompose" {
				promptBuilder.WriteString(renderMergedWorkSection(mergedTasks))
				promptBuilder.WriteString("\n")
			}
		} else {
			promptBuilder.WriteString("## Task Content\n\n")
			promptBuilder.WriteString(task.Content)
			promptBuilder.WriteString("\n\n")
			if designContext != "" {
				promptBuilder.WriteString(designContext)
				promptBuilder.WriteString("\n\n")
			}
			promptBuilder.WriteString("## Instructions\n\n")
			writePhasePrompt(&promptBuilder, currentPhase, task.ID, taskID, pCfg)
		}

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
		if len(assembledContext) > dispatchContextWarnBytes {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"Warning: dispatch context is %d KB (threshold %d KB). Run `cobuild context audit` for per-layer breakdown. See docs/guides/context-optimization.md for trimming guidance.\n",
				len(assembledContext)/1024, dispatchContextWarnBytes/1024,
			)
		}
		if !dryRun && assembledContext != "" {
			contextDir := filepath.Join(worktreePath, ".cobuild")
			if err := os.MkdirAll(contextDir, 0755); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not create %s: %v\n", contextDir, err)
			} else {
				// Belt-and-braces: prevent git from tracking any dispatch artifacts.
				// complete.go also excludes .cobuild/ via pathspec, but this gitignore
				// means manual `git add .` in the worktree still won't pick them up.
				gitignorePath := filepath.Join(contextDir, ".gitignore")
				if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
					if err := os.WriteFile(gitignorePath, []byte("*\n"), 0644); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not write .cobuild/.gitignore: %v\n", err)
					}
				}
				contextPath := filepath.Join(contextDir, "dispatch-context.md")
				if err := os.WriteFile(contextPath, []byte(assembledContext), 0644); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not write dispatch context: %v\n", err)
				}
			}

			// Append a CoBuild dispatch section to the worktree's runtime-specific
			// context file (CLAUDE.md for claude-code, AGENTS.md for codex).
			// Distinguish "file does not exist" (fine, start from empty) from real
			// read errors (e.g., permission denied) — the latter must NOT silently
			// overwrite the file. Idempotent: skip append if the section already
			// exists (worktree re-dispatch).
			contextFilePath := filepath.Join(worktreePath, rt.ContextFile())
			existing, readErr := os.ReadFile(contextFilePath)
			dispatchSectionHeader := []byte("## CoBuild Dispatch Context")
			if readErr != nil && !os.IsNotExist(readErr) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not read %s (%v) — leaving untouched to avoid data loss\n", contextFilePath, readErr)
			} else if !bytes.Contains(existing, dispatchSectionHeader) {
				// Only prefix newlines if the file already has content; avoids
				// leading blank lines in a fresh context file.
				prefix := ""
				if len(existing) > 0 {
					prefix = "\n\n"
				}
				dispatchSection := prefix + "## CoBuild Dispatch Context\n\n" +
					"You are a dispatched CoBuild agent. Your task prompt was passed as the initial message.\n" +
					"Additional context is in `.cobuild/dispatch-context.md` — read it if you need architecture, " +
					"design context, or project anatomy.\n"
				if err := os.WriteFile(contextFilePath, append(existing, []byte(dispatchSection)...), 0644); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not update worktree %s: %v\n", rt.ContextFile(), err)
				}
			}
		}

		// Write runtime-specific agent-settings files into the worktree (if any).
		// claude-code writes .claude/settings.local.json with Stop hook + deny list;
		// codex is a no-op.
		if !dryRun {
			if err := rt.WriteSettings(worktreePath); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: could not write %s settings: %v\n", rt.Name(), err)
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

		tmuxSession := pCfg.ResolveTmuxSession(taskProject)
		tmuxWindow := dispatchTmuxWindowName(currentPhase, taskID)

		// Ensure tmux session exists, create if not
		if err := tmuxRun(ctx, pCfg, "has-session", "-t", tmuxSession); err != nil {
			if createErr := tmuxRun(ctx, pCfg, "new-session", "-d", "-s", tmuxSession); createErr != nil {
				return fmt.Errorf("failed to create tmux session %q: %v", tmuxSession, createErr)
			}
		}
		// Resolve the model for the current phase, runtime-aware. Phase/gate
		// overrides still take precedence; the runtime-specific default is
		// used as a fallback so claude-code gets "sonnet" and codex gets
		// "gpt-5.4" without any phase-level override.
		resolvedModel := pCfg.ModelForPhaseRuntime(currentPhase, "", rtName)

		pipelineID := ""
		designID := ""
		parentEdges, _ = conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
		if len(parentEdges) > 0 {
			designID = parentEdges[0].ItemID
			if cbStore != nil {
				if run, err := cbStore.GetRun(ctx, designID); err == nil {
					pipelineID = run.ID
				}
			}
		}
		if designID == "" {
			designID = taskID // bug dispatched directly
		}

		// Create the session record before spawning tmux so fast runtimes can
		// deterministically close the correct session during completion.
		sessionID := ""
		if !dryRun && cbStore != nil {
			session, err := cbStore.CreateSession(ctx, store.SessionInput{
				PipelineID:       pipelineID,
				DesignID:         designID,
				TaskID:           taskID,
				Phase:            currentPhase,
				Project:          taskProject,
				Runtime:          rt.Name(),
				Model:            resolvedModel,
				PromptChars:      len(prompt),
				Prompt:           prompt,
				AssembledContext: assembledContext,
				WorktreePath:     worktreePath,
				TmuxSession:      tmuxSession,
				TmuxWindow:       tmuxWindow,
			})
			if err != nil {
				fmt.Printf("Warning: failed to record session: %v\n", err)
			} else {
				sessionID = session.ID
				_ = conn.SetMetadata(ctx, taskID, "session_id", session.ID)
			}
		}

		// Build the tmux runner script via the runtime. Each runtime owns the
		// CLI-specific bash template for spawning the agent and running
		// cobuild complete on exit.
		scriptBody, err := rt.BuildRunnerScript(cobuildruntime.RunnerInput{
			WorktreePath: worktreePath,
			RepoRoot:     repoRootForWT,
			TaskID:       taskID,
			PromptFile:   promptPath,
			Model:        resolvedModel,
			ExtraFlags:   pCfg.FlagsForRuntime(rtName),
			SessionID:    sessionID,
			HooksDir:     filepath.Join(findRepoRoot(), "hooks"),
			Phase:        currentPhase,
		})
		if err != nil {
			return fmt.Errorf("build runner script: %v", err)
		}

		// Write the script to a temp file; the script self-deletes via $0
		// after the agent session ends, so we don't need to track the path
		// for cleanup here.
		scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("cobuild-run-%s.sh", taskID))
		if err := os.WriteFile(scriptPath, []byte(scriptBody), 0755); err != nil {
			return fmt.Errorf("failed to write dispatch script: %v", err)
		}
		tmuxArgs := []string{"new-window", "-n", tmuxWindow, "-t", tmuxSession, "bash", scriptPath}

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
		syncPipelineTaskStatus(ctx, taskID, "in_progress")

		// Foreground mode (cb-f1b0e9): run the runner script in the calling
		// terminal with inherited stdio. Skips tmux + window cleanup path +
		// early-death probe. Agent output streams live; Ctrl-C terminates it.
		// Intended for debugging silent-death scenarios, not production.
		if foreground {
			fmt.Fprintf(cmd.ErrOrStderr(), "Running %s in foreground (runtime=%s, model=%s). Ctrl-C to abort.\n",
				taskID, rt.Name(), resolvedModel)
			fgCmd := exec.CommandContext(ctx, "bash", scriptPath)
			fgCmd.Stdin = os.Stdin
			fgCmd.Stdout = cmd.OutOrStdout()
			fgCmd.Stderr = cmd.ErrOrStderr()
			if err := fgCmd.Run(); err != nil {
				if cbStore != nil && sessionID != "" {
					_ = cbStore.EndSession(ctx, sessionID, store.SessionResult{
						ExitCode:       -1,
						Status:         "failed",
						Error:          fmt.Sprintf("foreground dispatch exited with error: %v", err),
						CompletionNote: "Foreground dispatch exited non-zero",
					})
				}
				return fmt.Errorf("foreground dispatch: %w", err)
			}
			// Script exited cleanly — runner scripts run `cobuild complete`
			// or record the gate verdict at the end, so pipeline state is
			// already up to date.
			return nil
		}

		// Spawn tmux
		tmuxOut, err := tmuxCombinedOutput(ctx, pCfg, tmuxArgs...)
		if err != nil {
			if conn != nil {
				_ = conn.UpdateStatus(ctx, taskID, task.Status)
			}
			syncPipelineTaskStatus(ctx, taskID, task.Status)
			if cbStore != nil && sessionID != "" {
				_ = cbStore.EndSession(ctx, sessionID, store.SessionResult{
					ExitCode:       -1,
					Status:         "cancelled",
					Error:          fmt.Sprintf("dispatch spawn failed: %v", err),
					CompletionNote: "Dispatch failed before tmux window started",
				})
			}
			return fmt.Errorf("failed to spawn tmux window: %s\n%s", err, string(tmuxOut))
		}

		// If anything after window creation fails, the window would be left
		// behind and the next dispatch attempt would refuse on state-resolver
		// grounds ("window still present"), causing a self-blocking loop
		// (cb-699bf2). Guarantee cleanup on error by keying cleanup off a
		// success flag set at the end of RunE.
		//
		// The session row is created later (line ~407) and also needs the
		// same cleanup — without it, a dispatch failure between session
		// insert and successful return leaves the session marked 'running'
		// forever (cb-31ac56).
		dispatchSucceeded := false
		defer func() {
			if dispatchSucceeded {
				return
			}
			target := fmt.Sprintf("%s:%s", tmuxSession, tmuxWindow)
			// Use a fresh background context: parent ctx may be the source of
			// the error that got us here (e.g. cancellation).
			cleanupCtx := context.Background()
			_ = tmuxRun(cleanupCtx, pCfg, "kill-window", "-t", target)
			if cbStore != nil && sessionID != "" {
				_ = cbStore.EndSession(cleanupCtx, sessionID, store.SessionResult{
					ExitCode:       -1,
					Status:         "cancelled",
					Error:          "dispatch failed after tmux window created",
					CompletionNote: "Dispatch returned non-nil error after window spawn — auto-cancelled by defer",
				})
			}
		}()

		// Capture output
		logDir := filepath.Join(worktreePath, ".cobuild")
		os.MkdirAll(logDir, 0755)
		logFile := filepath.Join(logDir, "session.log")
		_ = tmuxRun(ctx, pCfg, "pipe-pane", "-t", fmt.Sprintf("%s:%s", tmuxSession, tmuxWindow),
			fmt.Sprintf("cat >> '%s'", strings.ReplaceAll(logFile, "'", "'\\''")))

		// Post-dispatch liveness probe (cb-1d8abc). Async: sample the tmux
		// window at 10s and 60s. If the window is gone before the agent had
		// time to produce meaningful work, flag early_death on the session
		// and capture session.log into .cobuild/dispatch-error.log so the
		// operator can see what happened without hunting for the artefact.
		if sessionID != "" {
			startEarlyDeathProbe(pCfg, sessionID, tmuxSession, tmuxWindow, worktreePath)
		}

		// Record dispatch metadata
		dispatchInfo := map[string]any{
			"dispatched_at":    time.Now().UTC().Format(time.RFC3339),
			"agent":            agent,
			"dispatch_runtime": rt.Name(),
			"tmux_window":      tmuxWindow,
			"log_file":         logFile,
		}
		if err := conn.UpdateMetadataMap(ctx, taskID, dispatchInfo); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: dispatched but failed to update metadata: %v\n", err)
		}

		if outputFormat == "json" {
			out := map[string]any{
				"task_id":       taskID,
				"agent":         agent,
				"runtime":       rt.Name(),
				"model":         resolvedModel,
				"tmux_session":  tmuxSession,
				"worktree_path": worktreePath,
				"tmux_window":   tmuxWindow,
				"dispatched_at": dispatchInfo["dispatched_at"],
			}
			s, _ := client.FormatJSON(out)
			fmt.Println(s)
			dispatchSucceeded = true
			return nil
		}

		fmt.Printf("Dispatched %s to %s (runtime: %s, model: %s)\n", taskID, agent, rt.Name(), resolvedModel)
		fmt.Printf("  Session:  %s\n", tmuxSession)
		fmt.Printf("  Worktree: %s\n", worktreePath)
		fmt.Printf("  Window:   %s\n", tmuxWindow)
		printNextStep(taskID, currentPhase, "dispatch")
		dispatchSucceeded = true
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
		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)

		// Get all children of the design — includes tasks AND gate review
		// sub-shards (type=review) AND any other child types. We must filter
		// to only tasks; dispatching a gate record as if it were
		// implementation work wastes tokens at best and corrupts the gate
		// audit trail at worst (observed during cp-c2ec47's wave 1 — a
		// readiness-review gate record got dispatched as a task because the
		// filter wasn't here).
		edges, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
		if err != nil {
			return fmt.Errorf("get child tasks: %w", err)
		}

		var ready []dispatchWaveCandidate
		var inProgress []string
		var blocked []string
		readyWave := map[string]int{}
		activeWave := 0

		for _, e := range edges {
			// Filter by work-item type: only dispatch tasks, not review
			// sub-shards, investigation reports, or anything else that might
			// be child-of the design. Skip the edge on any lookup error
			// rather than fail the whole wave.
			item, err := conn.Get(ctx, e.ItemID)
			if err != nil || item == nil {
				continue
			}
			if item.Type != "task" {
				continue
			}
			wave := taskWave(item)

			if e.Status == "closed" {
				continue // fully merged/complete
			}

			if resolveWaveStrategy(pCfg) == "serial" {
				if activeWave == 0 || (wave > 0 && wave < activeWave) {
					activeWave = wave
				}
			}

			if e.Status == "in_progress" {
				inProgress = append(inProgress, e.ItemID)
				continue
			}
			if e.Status == "needs-review" {
				// Serial mode must wait for closure/merge, not merely review-ready.
				blocked = append(blocked, e.ItemID)
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
				ready = append(ready, dispatchWaveCandidate{
					TaskID: e.ItemID,
					Wave:   dispatchWaveMetadata(item.Metadata),
				})
				readyWave[e.ItemID] = wave
			} else {
				blocked = append(blocked, e.ItemID)
			}
		}

		// Wave filtering is handled below by filterDispatchWaveCandidates.

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

		ready = filterDispatchWaveCandidates(ready, pCfg.Dispatch.WaveStrategy)

		// Per-runtime concurrency: count in-progress tasks by their
		// dispatched runtime, then cap each runtime independently
		// (cb-0a0762). Codex is pinned at 2 by default; claude-code at 4.
		// Using only the global cap would force codex's ceiling onto
		// claude-code, throttling mixed workloads unnecessarily.
		inProgressByRuntime := map[string]int{}
		for _, inProgressID := range inProgress {
			rt := resolveRuntimeForTask(ctx, pCfg, inProgressID)
			inProgressByRuntime[rt]++
		}

		// Filter ready candidates by per-runtime headroom.
		selected := make([]dispatchWaveCandidate, 0, len(ready))
		dispatchedByRuntime := map[string]int{}
		var deferredByRuntime []string
		for _, cand := range ready {
			rt := resolveRuntimeForTask(ctx, pCfg, cand.TaskID)
			cap := pCfg.MaxConcurrentForRuntime(rt)
			running := inProgressByRuntime[rt] + dispatchedByRuntime[rt]
			if running >= cap {
				deferredByRuntime = append(deferredByRuntime, fmt.Sprintf("%s (%s at cap %d)", cand.TaskID, rt, cap))
				continue
			}
			selected = append(selected, cand)
			dispatchedByRuntime[rt]++
		}
		ready = selected

		if len(deferredByRuntime) > 0 {
			fmt.Printf("Deferred %d task(s) by per-runtime cap: %s\n",
				len(deferredByRuntime), strings.Join(deferredByRuntime, ", "))
		}

		if len(ready) == 0 {
			fmt.Printf("At capacity (per-runtime). In-progress by runtime: %s\n",
				formatRuntimeCounts(inProgressByRuntime))
			return nil
		}

		if resolveWaveStrategy(pCfg) == "serial" {
			fmt.Printf("Dispatching %d tasks (serial wave) for %s:\n", len(ready), designID)
		} else {
			fmt.Printf("Dispatching %d tasks (parallel ready set) for %s:\n", len(ready), designID)
		}
		// Look up the parent design's pipeline run so we can register tasks
		var pipelineID string
		if cbStore != nil {
			if run, err := cbStore.GetRun(ctx, designID); err == nil {
				pipelineID = run.ID
			}
		}

		for i, candidate := range ready {
			taskID := candidate.TaskID
			if dryRun {
				fmt.Printf("  [dry-run] %s\n", taskID)
				continue
			}

			// Register the task in pipeline_tasks so the orchestrator's
			// implement loop can track it via ListTasksByDesign.
			if cbStore != nil && pipelineID != "" {
				wave := candidate.Wave
				var wavePtr *int
				if wave > 0 {
					wavePtr = &wave
				}
				if err := cbStore.AddTask(ctx, pipelineID, taskID, designID, wavePtr); err != nil {
					// Ignore duplicates — task may already be registered from a prior run
					if !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "already exists") {
						fmt.Printf("  Warning: failed to register task %s: %v\n", taskID, err)
					}
				}
			}

			// Run dispatch for each task via the existing dispatch command logic.
			// Stagger dispatches by 3 seconds to avoid overwhelming the Codex
			// app-server — simultaneous codex exec processes contend for the
			// local server and later ones silently fail to start (cb-357c42).
			if i > 0 {
				time.Sleep(3 * time.Second)
			}
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
		printNextStep(designID, "implement", "dispatch-wave")
		return nil
	},
}

type dispatchWaveCandidate struct {
	TaskID string
	Wave   int
}

// resolveRuntimeForTask returns the runtime name that will handle this task
// using the same precedence as single-task dispatch (task metadata >
// config default > "claude-code"). Used by dispatch-wave's per-runtime
// concurrency check (cb-0a0762).
func resolveRuntimeForTask(ctx context.Context, pCfg *config.Config, taskID string) string {
	meta := ""
	if conn != nil {
		meta, _ = conn.GetMetadata(ctx, taskID, "dispatch_runtime")
	}
	return pCfg.ResolveRuntime("", meta)
}

// formatRuntimeCounts renders a "codex=2 claude-code=3" summary string.
func formatRuntimeCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "(none)"
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, " ")
}

func filterDispatchWaveCandidates(candidates []dispatchWaveCandidate, strategy string) []dispatchWaveCandidate {
	if len(candidates) == 0 {
		return nil
	}
	if strings.EqualFold(strategy, "parallel") {
		return candidates
	}

	selectedWave := candidates[0].Wave
	for _, candidate := range candidates[1:] {
		if candidate.Wave < selectedWave {
			selectedWave = candidate.Wave
		}
	}

	filtered := make([]dispatchWaveCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Wave == selectedWave {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func dispatchWaveMetadata(metadata map[string]any) int {
	if metadata == nil {
		return 0
	}

	switch v := metadata["wave"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		wave, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return wave
		}
	}

	return 0
}

// writePhasePrompt writes phase-appropriate instructions into the dispatch prompt.
func writePhasePrompt(b *strings.Builder, phase, workItemID, taskID string, pCfg *config.Config) {
	switch phase {
	case "design":
		b.WriteString("**Evaluate this design for pipeline readiness.**\n\n")
		b.WriteString("Follow the gate-readiness-review skill:\n")
		b.WriteString("1. Read the design content below\n")
		b.WriteString("2. Check 5 readiness criteria: problem stated, user identified, success criteria, scope boundaries, links to parent\n")
		b.WriteString("3. Run implementability check — can an agent build this without asking questions?\n")
		b.WriteString("4. Score readiness (1-5) and determine verdict\n")
		b.WriteString("5. **Write your verdict to `.cobuild/gate-verdict.json`** with this exact format:\n")
		b.WriteString("   ```json\n")
		b.WriteString(fmt.Sprintf("   {\"gate\": \"readiness-review\", \"shard_id\": \"%s\", \"verdict\": \"pass\", \"readiness\": 4, \"body\": \"Your findings here\"}\n", workItemID))
		b.WriteString("   ```\n")
		b.WriteString("   Set verdict to \"pass\" or \"fail\". The orchestrator records the gate after your session ends.\n")
		b.WriteString("   **Do NOT run `cobuild review` or `cobuild complete` yourself** — the runner handles it.\n")

	case "decompose":
		b.WriteString("**Break this design into implementable tasks.**\n\n")
		b.WriteString("Follow the decompose-design skill:\n")
		b.WriteString("1. Read the design content above\n")
		b.WriteString("2. **For EACH spec/section in the design, identify its target project AND target repo.** The design may explicitly name them (\"Spec 3 → penfold\") or reference files under a specific path (\"pf-34494b\", \"penf-cli MEMORY.md\"). Write down a spec → (project, repo) map BEFORE creating any tasks. If the design is unclear on target, fail the decomposition gate and ask for clarification.\n")
		b.WriteString("3. Identify discrete tasks — each completable in a single agent session (1-5 files, ~100-300 lines). **One task = one repo. Never create a task that requires edits in multiple repos.**\n")
		b.WriteString("4. Decide completion path per task: set `completion_mode: direct` only for non-code tasks whose output is expected outside the repo/worktree (KB updates, config/data changes, user-global state). Use `completion_mode: code` for normal repo changes. If unsure, leave it unset and let `cobuild complete` auto-detect.\n")
		b.WriteString("5. Order by dependency — assign wave numbers (wave 1 has no blockers, wave 2 depends on wave 1)\n")
		b.WriteString("6. For each task, create a work item in the **correct target project** with scope, acceptance criteria, and code locations:\n")
		b.WriteString(fmt.Sprintf("   `cobuild wi create --project <target-project> --type task --title \"<title>\" --body \"<spec>\" --parent %s`\n", workItemID))
		b.WriteString("7. For each task, set `repo` metadata to the target repo name:\n")
		b.WriteString("   `cxp shard metadata set <task-id> repo <repo-name>`\n")
		b.WriteString("8. If a task is explicitly non-code, set `completion_mode` metadata after creation:\n")
		b.WriteString("   `cxp shard metadata set <task-id> completion_mode direct`\n")
		b.WriteString("9. Link dependencies between tasks:\n")
		b.WriteString("   `cobuild wi links add <task-id> <blocker-id> blocked-by`\n")
		b.WriteString("   Do NOT re-create tasks listed in the `Work already merged` section below. If new work depends on one of those merged tasks, create only the new task and add a `blocked-by` edge to the merged task instead.\n")
		b.WriteString("10. **Write your verdict to `.cobuild/gate-verdict.json`** with this exact format:\n")
		b.WriteString("   ```json\n")
		b.WriteString(fmt.Sprintf("   {\"gate\": \"decomposition-review\", \"shard_id\": \"%s\", \"verdict\": \"pass\", \"body\": \"<summary with spec-to-project-repo map and any tasks tagged `completion_mode: direct`>\"}\n", workItemID))
		b.WriteString("   ```\n")
		b.WriteString("   **Do NOT run `cobuild decompose` yourself** — the runner handles it after your session ends.\n\n")
		b.WriteString("**CRITICAL — multi-project vs multi-repo (do not confuse these):**\n\n")
		b.WriteString("- **`--project <name>`** controls the shard's home project — which project's namespace the task shard lives in (determines the ID prefix, which project backlog lists it, etc.). Required when a task will end up owned by a different project's team/pipeline than the parent design's project.\n")
		b.WriteString("- **`repo` metadata** controls which git repo `cobuild dispatch` will create a worktree in. Required any time a task's code changes land in a repo different from the parent design's default repo.\n\n")
		b.WriteString("A task may need BOTH (a penfold-owned task targeting the penfold repo), or just repo metadata (a context-palace-owned task targeting the penf-cli repo), or just --project (very rare). **If the design mentions any other project (pf-, my-, etc.) or any other repo, you almost certainly need one or both.** The default (no --project, no repo metadata) means: shard in the CURRENT project, worktree in the CURRENT repo. Do not leave tasks that should target another project/repo with the defaults.\n\n")
		b.WriteString("Worked example: if the design says \"edit `penfold/internal/session_hook.go` and `context-palace/CLAUDE.md`\", that is NOT one task. Create one penfold task with `repo=penfold`, one context-palace task with `repo=context-palace`, and add a `blocked-by` edge only if the second change depends on the first.\n\n")
		b.WriteString("Lesson from cp-c2ec47 (2026-04-11): a decompose agent read a multi-project design (specs targeting context-palace, penfold, and penf-cli), created all 8 tasks in context-palace, and set `repo` metadata on only half of them. The decomposition gate passed but the result was unusable — tasks had to be manually re-tagged. **Do not repeat this.** Read every spec and explicitly assign project + repo before creating tasks.\n\n")
		b.WriteString("**Also:** Assign migration numbers explicitly if multiple tasks create DB migrations.\n")

	case "fix":
		b.WriteString("**Fix this bug.**\n\n")
		b.WriteString("Follow the fix-bug skill:\n")
		b.WriteString("1. Read the bug report\n")
		b.WriteString("2. Check escalation criteria — if any apply, add `needs-investigation` label and stop\n")
		b.WriteString("3. Reproduce, investigate lightly, append findings to the bug\n")
		b.WriteString("4. Implement the fix, run tests, build\n")
		b.WriteString("5. Commit — the Stop hook will run `cobuild complete`\n")
		b.WriteString("6. After the Stop hook completes, your work is done — stop. The dispatch runner cleans up the session itself; do NOT type `/exit` (it's a REPL-only command and will be rendered as text in your final message rather than terminating the process — see cb-e619cb / cb-eaef03).\n")

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
		b.WriteString("7. Create a fix task with the exact changes needed:\n")
		b.WriteString(fmt.Sprintf("   `cobuild wi create --type task --title \"Fix: ...\" --body \"...\" --parent %s`\n", workItemID))
		b.WriteString("8. **Write your verdict to `.cobuild/gate-verdict.json`** with this exact format:\n")
		b.WriteString("   ```json\n")
		b.WriteString(fmt.Sprintf("   {\"gate\": \"investigation\", \"shard_id\": \"%s\", \"verdict\": \"pass\", \"body\": \"<summary>\"}\n", workItemID))
		b.WriteString("   ```\n")
		b.WriteString("   **Do NOT run `cobuild investigate` yourself** — the runner handles it after your session ends.\n")

	case "review":
		b.WriteString("**Review this PR against its task spec and parent design.**\n\n")
		b.WriteString(fmt.Sprintf("Follow the configured review skill `%s`:\n", phaseSkillForPrompt(pCfg, "review", "review/dispatch-review.md")))
		b.WriteString("1. Read the task spec and parent design above\n")
		b.WriteString("2. Check CI status: `gh pr checks <pr-number>`\n")
		b.WriteString("3. Read the PR diff: `gh pr diff <pr-number>`\n")
		b.WriteString("4. Evaluate: does it match the spec? Does it fit the design? Will it break anything?\n")
		b.WriteString("5. Check for hardcoded values that should be configurable (project-specific constraints)\n")
		b.WriteString("6. **Write your verdict to `.cobuild/gate-verdict.json`** with this exact format:\n")
		b.WriteString("   ```json\n")
		b.WriteString(fmt.Sprintf("   {\"gate\": \"review\", \"shard_id\": \"%s\", \"verdict\": \"pass\", \"body\": \"<findings>\"}\n", workItemID))
		b.WriteString("   ```\n")
		b.WriteString("   **Do NOT run gate commands or merge PRs yourself** — the runner handles it.\n")

	case "done":
		b.WriteString("**Run a pipeline retrospective.**\n\n")
		b.WriteString("Follow the gate-retrospective skill:\n")
		b.WriteString("1. Gather execution data: `cobuild audit " + workItemID + "`\n")
		b.WriteString("2. Review each gate — how many rounds, what failed, was it avoidable?\n")
		b.WriteString("3. Review implementation — did agents complete without intervention?\n")
		b.WriteString("4. Identify patterns — repeated failures, agent gaps, process friction\n")
		b.WriteString("5. **Write your findings to `.cobuild/gate-verdict.json`** with this exact format:\n")
		b.WriteString("   ```json\n")
		b.WriteString(fmt.Sprintf("   {\"gate\": \"retrospective\", \"shard_id\": \"%s\", \"verdict\": \"pass\", \"body\": \"<findings>\"}\n", workItemID))
		b.WriteString("   ```\n")
		b.WriteString("   **Do NOT run `cobuild retro` yourself** — the runner handles it.\n")

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
		b.WriteString("After `cobuild complete` returns successfully, your work is done — stop. The dispatch runner cleans up the session itself; you do NOT need to type `/exit` (it's a REPL-only command and will be rendered as text in your final message, not interpreted as an exit signal — see cb-e619cb / cb-eaef03).\n\n")
		b.WriteString("**IMPORTANT RULES:**\n")
		b.WriteString("- NEVER use raw `git merge` or `git push` to main — always use `cobuild complete` which creates a PR\n")
		b.WriteString("- NEVER merge PRs yourself — the orchestrating agent handles merge via `cobuild merge` after review\n")
		b.WriteString("- If a reviewer (Gemini, human) leaves a critical comment on your PR, you MUST address it before the PR can merge\n")
		b.WriteString("- Check review comments: `gh pr view <pr-number> --comments`\n")
	}
}

func dispatchTmuxWindowName(phase, taskID string) string {
	if phase == "review" {
		return "review-" + taskID
	}
	return taskID
}

func phaseSkillForPrompt(pCfg *config.Config, phaseName, fallback string) string {
	if pCfg != nil {
		if phase := pCfg.FindPhase(phaseName); phase != nil && strings.TrimSpace(phase.Skill) != "" {
			return strings.TrimSpace(phase.Skill)
		}
	}
	return fallback
}

func renderMergedWorkSection(mergedTasks []MergedTask) string {
	var b strings.Builder
	b.WriteString("## Work already merged\n\n")
	if len(mergedTasks) == 0 {
		b.WriteString("None.\n\n")
	} else {
		for _, task := range mergedTasks {
			files := "none"
			if len(task.FilesChanged) > 0 {
				files = strings.Join(task.FilesChanged, ", ")
			}
			b.WriteString(fmt.Sprintf("- `%s` — commit `%s` — files: %s\n", task.TaskID, task.CommitSHA, files))
		}
		b.WriteString("\n")
	}
	b.WriteString("Do NOT re-create these tasks. If new work depends on them, use `blocked-by` edges to link the new task to the merged task.\n")
	b.WriteString("Command pattern: `cobuild wi links add <new-task> <merged-task> blocked-by`\n")
	return b.String()
}

type dispatchRepoTarget struct {
	Root   string
	Source string
}

func resolveDispatchTargetRepo(ctx context.Context, task *connector.WorkItem, taskID, project string, stderr io.Writer) (dispatchRepoTarget, error) {
	targetRepo := metadataString(task.Metadata, "repo")
	if conn != nil {
		if repo, err := conn.GetMetadata(ctx, taskID, "repo"); err == nil && repo != "" {
			targetRepo = strings.TrimSpace(repo)
		}
	}
	if targetRepo != "" {
		repoRoot, err := config.RepoForProject(targetRepo)
		if err != nil {
			return dispatchRepoTarget{}, fmt.Errorf("task specifies repo %q but it's not in the registry (~/.cobuild/repos.yaml): %v", targetRepo, err)
		}
		return dispatchRepoTarget{
			Root:   repoRoot,
			Source: fmt.Sprintf("task metadata: repo=%s", targetRepo),
		}, nil
	}

	if reg, err := config.LoadRepoRegistry(); err == nil {
		parentID, referencedRepos, refErr := parentDesignReferencedRepos(ctx, taskID, reg)
		if refErr == nil && len(referencedRepos) > 1 {
			return dispatchRepoTarget{}, fmt.Errorf(
				"task %s is missing `repo` metadata, and parent design %s references multiple repos (%s); set `repo` metadata before dispatching",
				taskID, parentID, strings.Join(referencedRepos, ", "),
			)
		}

		reposForProject := reposForProject(reg, project)
		if len(reposForProject) > 1 {
			fmt.Fprintf(stderr, "\nWARNING: Multi-repo project (%s) but task %s has no `repo` metadata.\n", project, taskID)
			fmt.Fprintf(stderr, "Repos in this project: %s\n", strings.Join(reposForProject, ", "))
			fmt.Fprintf(stderr, "Defaulting to %s — this may be wrong.\n", project)
			fmt.Fprintf(stderr, "Fix: `cxp shard metadata set %s repo <correct-repo>`\n\n", taskID)
		}
	}

	repoRoot, _ := config.RepoForProject(project)
	if repoRoot == "" {
		repoRoot = findRepoRoot()
	}
	return dispatchRepoTarget{
		Root:   repoRoot,
		Source: fmt.Sprintf("project: %s", project),
	}, nil
}

func parentDesignReferencedRepos(ctx context.Context, taskID string, reg *config.RepoRegistry) (string, []string, error) {
	if conn == nil || reg == nil {
		return "", nil, nil
	}
	parentEdges, err := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
	if err != nil || len(parentEdges) == 0 {
		return "", nil, err
	}
	parentID := parentEdges[0].ItemID
	parentItem, err := conn.Get(ctx, parentID)
	if err != nil {
		return parentID, nil, err
	}
	if parentItem == nil || parentItem.Type != "design" {
		return parentID, nil, nil
	}
	return parentID, referencedReposInWorkItem(parentItem, reg), nil
}

func referencedReposInWorkItem(item *connector.WorkItem, reg *config.RepoRegistry) []string {
	if item == nil || reg == nil {
		return nil
	}

	seen := make(map[string]struct{})
	addRepo := func(repo string) {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			return
		}
		if _, ok := reg.Repos[repo]; ok {
			seen[repo] = struct{}{}
		}
	}

	addRepo(metadataString(item.Metadata, "repo"))
	for _, repo := range metadataRepos(item.Metadata["repos"]) {
		addRepo(repo)
	}

	corpus := strings.ToLower(item.Title + "\n" + item.Content)
	for repo := range reg.Repos {
		if strings.Contains(corpus, strings.ToLower(repo)) {
			seen[repo] = struct{}{}
		}
	}

	repos := make([]string, 0, len(seen))
	for repo := range seen {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos
}

func metadataRepos(value any) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		repos := make([]string, 0, len(v))
		for _, item := range v {
			repos = append(repos, fmt.Sprintf("%v", item))
		}
		return repos
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		var repos []string
		if strings.HasPrefix(trimmed, "[") && json.Unmarshal([]byte(trimmed), &repos) == nil {
			return repos
		}
		parts := strings.Split(trimmed, ",")
		repos = make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				repos = append(repos, part)
			}
		}
		return repos
	default:
		return nil
	}
}

func reposForProject(reg *config.RepoRegistry, project string) []string {
	if reg == nil {
		return nil
	}
	var repos []string
	for name, entry := range reg.Repos {
		projYAML := readProjectConfigFromYAML(entry.Path)
		if projYAML.Project == project {
			repos = append(repos, name)
		}
	}
	sort.Strings(repos)
	return repos
}

// resolveTaskProject returns the project name for a task, used for tmux
// session naming and config resolution. Checks (in order): task.Project,
// pipeline run project (from store), global projectName fallback.
func resolveTaskProject(task *connector.WorkItem) string {
	if task != nil && task.Project != "" {
		return task.Project
	}
	// Check the pipeline run — it always has the correct project
	if cbStore != nil && task != nil {
		if run, err := cbStore.GetRun(context.Background(), task.ID); err == nil && run.Project != "" {
			return run.Project
		}
		// Task might be a child — check parent design's run
		if conn != nil {
			if edges, err := conn.GetEdges(context.Background(), task.ID, "outgoing", []string{"child-of"}); err == nil && len(edges) > 0 {
				if run, err := cbStore.GetRun(context.Background(), edges[0].ItemID); err == nil && run.Project != "" {
					return run.Project
				}
			}
		}
	}
	return projectName
}

type dispatchConflict struct {
	Source string
	Detail string
}

func dispatchConflictFromResolvedState(taskID string, state *pipelinestate.PipelineState) *dispatchConflict {
	if state == nil {
		return nil
	}

	for _, session := range state.Sessions {
		if session.Status != "running" {
			continue
		}
		if session.TaskID == "" || session.TaskID == taskID {
			return &dispatchConflict{
				Source: "pipeline session",
				Detail: fmt.Sprintf("session %s is still running", session.ID),
			}
		}
	}

	for _, window := range state.Tmux {
		if window.TargetID == taskID || window.WindowName == taskID {
			return &dispatchConflict{
				Source: "tmux",
				Detail: fmt.Sprintf("window %s:%s is still present", window.SessionName, window.WindowName),
			}
		}
	}

	switch state.Health {
	case pipelinestate.HealthInconsistent, pipelinestate.HealthZombie:
		if len(state.Inconsistencies) > 0 {
			return &dispatchConflict{
				Source: classifyResolvedConflictSource(state.Inconsistencies[0]),
				Detail: state.Inconsistencies[0],
			}
		}
		return &dispatchConflict{
			Source: "resolver",
			Detail: fmt.Sprintf("pipeline health is %s", state.Health),
		}
	default:
		return nil
	}
}

func classifyResolvedConflictSource(detail string) string {
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "tmux"):
		return "tmux"
	case strings.Contains(lower, "session"):
		return "pipeline session"
	case strings.Contains(lower, "work item"):
		return "work item"
	case strings.Contains(lower, "pipeline run"):
		return "pipeline run"
	default:
		return "resolver"
	}
}

// hasInvestigationContent returns true if the content already contains
// investigation findings — indicating an investigate phase has already run
// (or investigation happened in a prior conversation). The check is
// case-insensitive and looks for any of the standard section headings.
func hasInvestigationContent(content string) bool {
	lower := strings.ToLower(content)
	for _, heading := range []string{
		"## investigation report",
		"## root cause",
		"## fix applied",
		"## fix",
	} {
		if strings.Contains(lower, heading) {
			return true
		}
	}
	return false
}

func init() {
	dispatchCmd.Flags().String("agent", "", "Override agent (default: from config)")
	dispatchCmd.Flags().String("runtime", "", "Agent runtime to use (claude-code, codex). Defaults to task metadata, then pipeline.yaml dispatch.default_runtime, then claude-code.")
	dispatchCmd.Flags().Bool("dry-run", false, "Show what would be done without executing")
	dispatchCmd.Flags().Bool("foreground", false, "Run the agent in the current terminal instead of tmux (debug aid for silent-death scenarios)")
	dispatchWaveCmd.Flags().Bool("dry-run", false, "Show what would be done without executing")
	rootCmd.AddCommand(dispatchCmd)
	rootCmd.AddCommand(dispatchWaveCmd)
}
