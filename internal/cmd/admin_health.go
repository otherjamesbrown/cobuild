package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/spf13/cobra"
)

var adminHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check system health — database, connector, skills, worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		repoRoot := findRepoRoot()
		pCfg, cfgErr := config.LoadConfig(repoRoot)

		errors := 0
		warnings := 0

		fmt.Println("CoBuild Health Check")
		fmt.Println("====================")

		// Database
		if cbStore != nil {
			runs, err := cbStore.ListRuns(ctx, projectName)
			if err != nil {
				fmt.Printf("✗ Database         Connection failed: %v\n", err)
				errors++
			} else {
				fmt.Printf("✓ Database         Connected (%d pipeline runs)\n", len(runs))
			}
		} else {
			fmt.Println("✗ Database         Not configured")
			errors++
		}

		// Connector
		if conn != nil {
			_, err := conn.List(ctx, connector.ListFilters{Limit: 1})
			if err != nil {
				fmt.Printf("✗ Connector        %s — query failed: %v\n", conn.Name(), err)
				errors++
			} else {
				fmt.Printf("✓ Connector        %s\n", conn.Name())
			}
		} else {
			fmt.Println("⚠ Connector        Not configured (wi commands won't work)")
			warnings++
		}

		// Config
		if cfgErr != nil {
			fmt.Printf("✗ Config           Parse error: %v\n", cfgErr)
			errors++
		} else if pCfg != nil {
			wfCount := 0
			if pCfg.Workflows != nil {
				wfCount = len(pCfg.Workflows)
			}
			fmt.Printf("✓ Config           Loaded (%d workflows, %d phases)\n", wfCount, len(pCfg.Phases))
		} else {
			fmt.Println("⚠ Config           Using defaults (no pipeline.yaml found)")
			warnings++
		}

		// Skills
		skillsDir := "skills"
		if pCfg != nil && pCfg.SkillsDir != "" {
			skillsDir = pCfg.SkillsDir
		}
		skillCount := 0
		missingFrontmatter := 0
		skillBase := filepath.Join(repoRoot, skillsDir)
		if entries, err := os.ReadDir(skillBase); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				subEntries, _ := os.ReadDir(filepath.Join(skillBase, e.Name()))
				for _, se := range subEntries {
					if strings.HasSuffix(se.Name(), ".md") {
						skillCount++
						data, _ := os.ReadFile(filepath.Join(skillBase, e.Name(), se.Name()))
						if !strings.HasPrefix(string(data), "---\n") {
							missingFrontmatter++
						}
					}
				}
			}
			if missingFrontmatter > 0 {
				fmt.Printf("⚠ Skills           %d skills (%d missing frontmatter)\n", skillCount, missingFrontmatter)
				warnings++
			} else {
				fmt.Printf("✓ Skills           %d skills (all have frontmatter)\n", skillCount)
			}
		} else {
			fmt.Printf("⚠ Skills           No skills directory at %s\n", skillBase)
			warnings++
		}

		// Hooks
		hooksPath := filepath.Join(repoRoot, "hooks", "hooks.json")
		scriptPath := filepath.Join(repoRoot, "hooks", "cobuild-event.sh")
		if _, err := os.Stat(hooksPath); err == nil {
			if info, err := os.Stat(scriptPath); err == nil && info.Mode()&0111 != 0 {
				fmt.Println("✓ Hooks            hooks.json + cobuild-event.sh (executable)")
			} else {
				fmt.Println("⚠ Hooks            hooks.json found but cobuild-event.sh not executable")
				warnings++
			}
		} else {
			fmt.Println("⚠ Hooks            No hooks directory (session events won't be tracked)")
			warnings++
		}

		// Anatomy
		anatomyPath := anatomyOutputPath(repoRoot)
		legacyPath := legacyAnatomyPath(repoRoot)
		if info, err := os.Stat(anatomyPath); err == nil {
			daysOld := int(time.Since(info.ModTime()).Hours() / 24)
			if daysOld > 7 {
				fmt.Printf("⚠ Anatomy          %d days old — run cobuild scan to refresh\n", daysOld)
				warnings++
			} else {
				fmt.Printf("✓ Anatomy          Current (%d days old)\n", daysOld)
			}
		} else if _, legacyErr := os.Stat(legacyPath); legacyErr == nil {
			fmt.Println("⚠ Anatomy          Legacy always/ location present — run cobuild scan to migrate")
			warnings++
		} else {
			fmt.Println("⚠ Anatomy          Not found — run cobuild scan to generate file index")
			warnings++
		}

		// Worktrees
		staleWorktrees := countStaleWorktrees(ctx, repoRoot)
		if staleWorktrees > 0 {
			fmt.Printf("✗ Worktrees        %d stale worktrees for closed tasks\n", staleWorktrees)
			errors++
		} else {
			fmt.Println("✓ Worktrees        Clean")
		}

		// Branches
		staleBranches := countStaleBranches(repoRoot)
		if staleBranches > 0 {
			fmt.Printf("✗ Branches         %d merged/orphan branches\n", staleBranches)
			errors++
		} else {
			fmt.Println("✓ Branches         Clean")
		}

		// Stuck sessions
		if cbStore != nil {
			stuckCount := countStuckSessions(ctx)
			if stuckCount > 0 {
				fmt.Printf("⚠ Sessions         %d sessions in 'running' state for >24h\n", stuckCount)
				warnings++
			} else {
				fmt.Println("✓ Sessions         No stuck sessions")
			}
		}

		// AGENTS.md freshness
		agentsPath := filepath.Join(repoRoot, "AGENTS.md")
		if data, err := os.ReadFile(agentsPath); err == nil {
			if strings.Contains(string(data), markerBegin) {
				fmt.Println("✓ AGENTS.md        Present with CoBuild markers")
			} else {
				fmt.Println("⚠ AGENTS.md        Exists but no CoBuild markers — run cobuild update-agents")
				warnings++
			}
		} else {
			fmt.Println("⚠ AGENTS.md        Not found — run cobuild update-agents")
			warnings++
		}

		// Summary
		fmt.Println()
		if errors == 0 && warnings == 0 {
			fmt.Println("All checks passed.")
		} else {
			fmt.Printf("Issues: %d error(s), %d warning(s)\n", errors, warnings)
			if errors > 0 {
				fmt.Println("Run 'cobuild admin cleanup' to fix worktree/branch issues.")
			}
		}

		return nil
	},
}

func countStaleWorktrees(ctx context.Context, repoRoot string) int {
	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") && !strings.HasSuffix(line, repoRoot) {
			// Check if the task is closed
			path := strings.TrimPrefix(line, "worktree ")
			taskID := filepath.Base(path)
			if conn != nil {
				item, err := conn.Get(ctx, taskID)
				if err == nil && (item.Status == "closed" || item.Status == domain.StatusNeedsReview) {
					count++
				}
			}
		}
	}
	return count
}

func countStaleBranches(repoRoot string) int {
	out, err := exec.Command("git", "-C", repoRoot, "branch", "--merged", "main").CombinedOutput()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		branch := strings.TrimSpace(line)
		if branch != "" && branch != "main" && !strings.HasPrefix(branch, "*") {
			count++
		}
	}
	return count
}

func countStuckSessions(ctx context.Context) int {
	if cbStore == nil {
		return 0
	}
	sessions, err := cbStore.ListSessions(ctx, "")
	if err != nil {
		return 0
	}
	count := 0
	for _, s := range sessions {
		if s.Status == "running" {
			count++
		}
	}
	return count
}

func init() {
	adminCmd.AddCommand(adminHealthCmd)
}
