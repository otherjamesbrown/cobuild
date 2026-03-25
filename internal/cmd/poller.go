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
	"github.com/otherjamesbrown/cobuild/internal/worktree"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

var pollerCmd = &cobra.Command{
	Use:   "poller",
	Short: "Poll for actionable pipeline state and spawn M sessions",
	Long: `Runs a polling loop that checks for three trigger conditions every interval:

  1. New design ready -- open designs without pipeline metadata
  2. Task needs review -- tasks in needs-review with pipeline parent in implement phase
  3. Waiting condition satisfied -- pipeline waiting_for shards all closed

For each trigger, spawns an M session in a tmux window (unless --dry-run).`,
	Example: `  cobuild poller
  cobuild poller --interval 60
  cobuild poller --once --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		interval, _ := cmd.Flags().GetInt("interval")
		once, _ := cmd.Flags().GetBool("once")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		allProjects, _ := cmd.Flags().GetBool("all-projects")

		if interval < 1 {
			interval = 30
		}

		type projectEntry struct {
			name   string
			root   string
			config *config.Config
		}
		var projects []projectEntry

		if allProjects {
			reg, err := config.LoadRepoRegistry()
			if err != nil {
				return fmt.Errorf("failed to load repo registry: %v", err)
			}
			for name, entry := range reg.Repos {
				cfg, _ := config.LoadConfig(entry.Path)
				if cfg == nil {
					cfg = config.DefaultConfig()
				}
				projects = append(projects, projectEntry{name: name, root: entry.Path, config: cfg})
			}
			if len(projects) == 0 {
				return fmt.Errorf("no projects registered. Run `cobuild setup` in each repo.")
			}
			fmt.Printf("[poller] Polling %d projects: ", len(projects))
			for i, p := range projects {
				if i > 0 {
					fmt.Print(", ")
				}
				fmt.Print(p.name)
			}
			fmt.Println()
		} else {
			repoRoot, err := config.RepoForProject(projectName)
			if err != nil {
				repoRoot = findRepoRoot()
				fmt.Fprintf(os.Stderr, "[poller] No repo registered for project %q, using %s\n", projectName, repoRoot)
			}
			cfg, cfgErr := config.LoadConfig(repoRoot)
			if cfgErr != nil {
				cfg = config.DefaultConfig()
			}
			projects = append(projects, projectEntry{name: projectName, root: repoRoot, config: cfg})
		}

		for {
			for _, p := range projects {
				if len(projects) > 1 {
					fmt.Printf("[%s] ", p.name)
				}
				runTriggerChecks(ctx, p.root, p.config, dryRun)
				runHealthChecks(ctx, p.root, p.config, dryRun)
			}
			if once {
				break
			}
			time.Sleep(time.Duration(interval) * time.Second)
		}
		return nil
	},
}

func runTriggerChecks(ctx context.Context, repoRoot string, cfg *config.Config, dryRun bool) {
	fmt.Printf("[%s] Polling for triggers...\n", time.Now().Format("15:04:05"))
	triggerNewDesigns(ctx, repoRoot, cfg, dryRun)
	triggerTasksNeedingReview(ctx, repoRoot, cfg, dryRun)
	triggerSatisfiedWaits(ctx, repoRoot, cfg, dryRun)
}

func triggerNewDesigns(ctx context.Context, repoRoot string, cfg *config.Config, dryRun bool) {
	designs, err := cbClient.FindNewDesigns(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [trigger:new-design] error: %v\n", err)
		return
	}
	for _, d := range designs {
		if !shouldSpawn(ctx, d.ID, dryRun) {
			continue
		}
		if dryRun {
			fmt.Printf("  [trigger:new-design] Would init pipeline and spawn M for %s (%s)\n", d.ID, d.Title)
			continue
		}
		if _, err := cbClient.PipelineInit(ctx, d.ID); err != nil {
			fmt.Fprintf(os.Stderr, "  [trigger:new-design] pipeline init %s: %v\n", d.ID, err)
			continue
		}
		spawnM(ctx, repoRoot, cfg, d.ID, "new-design")
	}
}

func triggerTasksNeedingReview(ctx context.Context, repoRoot string, cfg *config.Config, dryRun bool) {
	tasks, err := cbClient.FindTasksNeedingReview(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [trigger:needs-review] error: %v\n", err)
		return
	}
	for _, t := range tasks {
		designID := findParentDesign(ctx, t.ID)
		if designID == "" {
			fmt.Fprintf(os.Stderr, "  [trigger:needs-review] no parent design for %s\n", t.ID)
			continue
		}
		if !shouldSpawn(ctx, designID, dryRun) {
			continue
		}
		if dryRun {
			fmt.Printf("  [trigger:needs-review] Would spawn M for review of %s (design: %s)\n", t.ID, designID)
			continue
		}
		spawnM(ctx, repoRoot, cfg, designID, "needs-review")
	}
}

func triggerSatisfiedWaits(ctx context.Context, repoRoot string, cfg *config.Config, dryRun bool) {
	waits, err := cbClient.FindSatisfiedWaits(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [trigger:wait-satisfied] error: %v\n", err)
		return
	}
	for _, w := range waits {
		if !shouldSpawn(ctx, w.DesignID, dryRun) {
			continue
		}
		if dryRun {
			fmt.Printf("  [trigger:wait-satisfied] Would spawn M for %s (%s), all %d waiting_for closed\n",
				w.DesignID, w.Title, len(w.WaitingFor))
			continue
		}
		spawnM(ctx, repoRoot, cfg, w.DesignID, "wait-satisfied")
	}
}

func shouldSpawn(ctx context.Context, designID string, dryRun bool) bool {
	status, _, err := cbClient.PipelineLockCheck(ctx, designID)
	if err != nil {
		return true
	}
	if status == "locked" {
		if dryRun {
			fmt.Printf("  [lock] %s is locked, skipping\n", designID)
		}
		return false
	}
	return true
}

func findParentDesign(ctx context.Context, taskID string) string {
	edges, err := conn.GetEdges(ctx, taskID, "outgoing", []string{"child-of"})
	if err != nil {
		return ""
	}
	for _, e := range edges {
		if e.Type == "design" {
			return e.ItemID
		}
	}
	return ""
}

func spawnM(ctx context.Context, repoRoot string, cfg *config.Config, designID, trigger string) {
	skillsDir := cfg.SkillsDir
	if skillsDir == "" {
		skillsDir = "skills"
	}
	playbookPath := filepath.Join(skillsDir, "shared", "playbook.md")

	tmuxSession := cfg.Dispatch.TmuxSession
	if tmuxSession == "" {
		tmuxSession = fmt.Sprintf("cobuild-%s", projectName)
	}

	// Ensure tmux session exists
	if err := exec.CommandContext(ctx, "tmux", "has-session", "-t", tmuxSession).Run(); err != nil {
		if createErr := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", tmuxSession).Run(); createErr != nil {
			fmt.Fprintf(os.Stderr, "  [spawn] failed to create tmux session %q: %v\n", tmuxSession, createErr)
			return
		}
	}
	claudeFlags := cfg.Dispatch.ClaudeFlags
	if claudeFlags == "" {
		claudeFlags = "--print"
	}

	prompt := fmt.Sprintf(
		"You are M. Read your playbook at %s. Your pipeline shard is %s. Current trigger: %s.",
		playbookPath, designID, trigger,
	)

	windowName := fmt.Sprintf("m-%s", designID)
	tmuxArgs := []string{
		"new-window",
		"-c", repoRoot,
		"-n", windowName,
		"-t", tmuxSession,
		"claude",
	}
	if claudeFlags != "" {
		tmuxArgs = append(tmuxArgs, strings.Fields(claudeFlags)...)
	}
	tmuxArgs = append(tmuxArgs, prompt)

	out, err := exec.CommandContext(ctx, "tmux", tmuxArgs...).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [spawn] failed for %s: %v\n%s\n", designID, err, string(out))
		return
	}
	fmt.Printf("  [spawn] M session started: window=%s trigger=%s\n", windowName, trigger)
}

func runHealthChecks(ctx context.Context, repoRoot string, cfg *config.Config, dryRun bool) {
	mon := cfg.Monitoring
	if mon.StallTimeout == "" && !mon.CrashCheck {
		return
	}

	stallTimeout, _ := time.ParseDuration(mon.StallTimeout)
	if stallTimeout == 0 {
		stallTimeout = 30 * time.Minute
	}

	tmuxSession := cfg.Dispatch.TmuxSession
	if tmuxSession == "" {
		tmuxSession = fmt.Sprintf("cobuild-%s", projectName)
	}

	tasks, err := cbClient.FindInProgressTasks(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [health] error finding tasks: %v\n", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	fmt.Printf("  [health] checking %d in-progress tasks\n", len(tasks))

	for _, task := range tasks {
		if mon.CrashCheck {
			windowExists := checkTmuxWindow(tmuxSession, task.ID)
			if !windowExists {
				handleCrash(ctx, task, mon, repoRoot, cfg, dryRun)
				continue
			}
		}
		if time.Since(task.UpdatedAt) > stallTimeout {
			handleStall(ctx, task, mon, repoRoot, cfg, tmuxSession, dryRun)
		}
	}
}

func checkTmuxWindow(session, taskID string) bool {
	out, err := exec.Command("tmux", "list-windows", "-t", session).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), taskID)
}

func handleCrash(ctx context.Context, task client.ShardSummary, mon config.MonitoringCfg, repoRoot string, cfg *config.Config, dryRun bool) {
	retryCount := getRetryCount(ctx, task.ID)

	if mon.MaxRetries > 0 && retryCount >= mon.MaxRetries {
		if dryRun {
			fmt.Printf("  [health:crash] %s -- max retries (%d) exceeded, would escalate\n", task.ID, mon.MaxRetries)
			return
		}
		fmt.Printf("  [health:crash] %s -- max retries (%d) exceeded, escalating\n", task.ID, mon.MaxRetries)
		action := mon.Actions.OnMaxRetries
		if action == "escalate" || action == "" {
			if conn != nil {
				_ = conn.AppendContent(ctx, task.ID, fmt.Sprintf("\n\n## Health Check -- Crash\nMax retries (%d) exceeded. Escalating.", mon.MaxRetries))
				_ = conn.AddLabel(ctx, task.ID, "blocked")
			}
		}
		return
	}

	action := mon.Actions.OnCrash
	if dryRun {
		fmt.Printf("  [health:crash] %s -- window gone, retry %d/%d, would %s\n", task.ID, retryCount+1, mon.MaxRetries, action)
		return
	}

	fmt.Printf("  [health:crash] %s -- window gone, retry %d/%d, action: %s\n", task.ID, retryCount+1, mon.MaxRetries, action)

	switch {
	case action == "redispatch" || action == "":
		if conn != nil {
			_ = conn.AppendContent(ctx, task.ID, fmt.Sprintf("\n\n## Health Check -- Crash detected\nRetry %d/%d. Re-dispatching.", retryCount+1, mon.MaxRetries))
		}
		setRetryCount(ctx, task.ID, retryCount+1)
		if conn != nil {
			_ = conn.UpdateStatus(ctx, task.ID, "open")
		}
		wtPath, _ := conn.GetMetadata(ctx, task.ID, "worktree_path"); if wtPath != "" { _ = worktree.Remove(ctx, wtPath) }
		_ = exec.Command("cobuild", "dispatch", task.ID).Run()
	case strings.HasPrefix(action, "skill:"):
		if conn != nil {
			_ = conn.AppendContent(ctx, task.ID, fmt.Sprintf("\n\n## Health Check -- Crash detected\nRetry %d/%d. Spawning skill %s.", retryCount+1, mon.MaxRetries, action))
		}
		setRetryCount(ctx, task.ID, retryCount+1)
		spawnM(ctx, repoRoot, cfg, task.ID, "health-crash")
	case action == "escalate":
		if conn != nil {
			_ = conn.AppendContent(ctx, task.ID, "\n\n## Health Check -- Crash detected\nEscalating.")
			_ = conn.AddLabel(ctx, task.ID, "blocked")
		}
	}
}

func handleStall(ctx context.Context, task client.ShardSummary, mon config.MonitoringCfg, repoRoot string, cfg *config.Config, tmuxSession string, dryRun bool) {
	action := mon.Actions.OnStall
	if dryRun {
		fmt.Printf("  [health:stall] %s -- no progress for %s, would %s\n", task.ID, mon.StallTimeout, action)
		return
	}

	fmt.Printf("  [health:stall] %s -- no progress for %s, action: %s\n", task.ID, mon.StallTimeout, action)

	switch {
	case strings.HasPrefix(action, "skill:"):
		if conn != nil {
			_ = conn.AppendContent(ctx, task.ID, fmt.Sprintf("\n\n## Health Check -- Stall detected\nNo progress for %s. Spawning skill %s.", mon.StallTimeout, action))
		}
		spawnM(ctx, repoRoot, cfg, task.ID, "health-stall")
	case action == "escalate":
		if conn != nil {
			_ = conn.AppendContent(ctx, task.ID, fmt.Sprintf("\n\n## Health Check -- Stall detected\nNo progress for %s. Escalating.", mon.StallTimeout))
			_ = conn.AddLabel(ctx, task.ID, "blocked")
		}
	case action == "redispatch":
		retryCount := getRetryCount(ctx, task.ID)
		if conn != nil {
			_ = conn.AppendContent(ctx, task.ID, fmt.Sprintf("\n\n## Health Check -- Stall detected\nNo progress for %s. Re-dispatching (retry %d/%d).", mon.StallTimeout, retryCount+1, mon.MaxRetries))
		}
		setRetryCount(ctx, task.ID, retryCount+1)
		if conn != nil {
			_ = conn.UpdateStatus(ctx, task.ID, "open")
		}
		wtPath, _ := conn.GetMetadata(ctx, task.ID, "worktree_path"); if wtPath != "" { _ = worktree.Remove(ctx, wtPath) }
		_ = exec.Command("cobuild", "dispatch", task.ID).Run()
	}
}

func getRetryCount(ctx context.Context, taskID string) int {
	shard, err := conn.Get(ctx, taskID)
	if err != nil || shard.Metadata == nil {
		return 0
	}
	count, _ := shard.Metadata["dispatch_retries"].(float64)
	return int(count)
}

func setRetryCount(ctx context.Context, taskID string, count int) {
	_ = conn.SetMetadata(ctx, taskID, "dispatch_retries", count)
}

func init() {
	pollerCmd.Flags().Int("interval", 30, "Polling interval in seconds")
	pollerCmd.Flags().Bool("once", false, "Run one check cycle and exit")
	pollerCmd.Flags().Bool("dry-run", false, "Print what would be spawned without executing")
	pollerCmd.Flags().Bool("all-projects", false, "Poll all registered projects from ~/.cobuild/repos.yaml")

	rootCmd.AddCommand(pollerCmd)
}
