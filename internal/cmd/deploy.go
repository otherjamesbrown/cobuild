package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

// DeployResult tracks the outcome of deploying a service.
type DeployResult struct {
	Service    string `json:"service"`
	Triggered  bool   `json:"triggered"`
	Deployed   bool   `json:"deployed"`
	SmokePassed bool  `json:"smoke_passed"`
	RolledBack bool   `json:"rolled_back"`
	Error      string `json:"error,omitempty"`
	Duration   string `json:"duration,omitempty"`
}

var deployCmd = &cobra.Command{
	Use:   "deploy <design-id>",
	Short: "Deploy services affected by a design's merged changes",
	Long: `Checks which files were changed by a design's tasks, matches them against
deploy trigger_paths in pipeline config, and runs the corresponding deploy
commands. After each deploy, runs the smoke test. On failure, runs rollback.

Only deploys services whose trigger_paths match the changed files.`,
	Args: cobra.ExactArgs(1),
	Example: `  cobuild deploy pf-6e38e9 --dry-run    # show what would deploy
  cobuild deploy pf-6e38e9              # deploy affected services
  cobuild deploy pf-6e38e9 --service gateway  # deploy specific service only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		designID := args[0]
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		serviceFilter, _ := cmd.Flags().GetString("service")

		if conn == nil {
			return fmt.Errorf("no connector configured")
		}

		// Load deploy config
		repoRoot := ""
		targetRepo := ""
		if conn != nil {
			// Check first task for repo metadata
			edges, _ := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
			for _, e := range edges {
				r, _ := conn.GetMetadata(ctx, e.ItemID, "repo")
				if r != "" {
					targetRepo = r
					break
				}
			}
		}
		if targetRepo != "" {
			repoRoot, _ = config.RepoForProject(targetRepo)
		}
		if repoRoot == "" {
			repoRoot, _ = config.RepoForProject(projectName)
		}
		if repoRoot == "" {
			repoRoot = findRepoRoot()
		}

		pCfg, cfgErr := config.LoadConfig(repoRoot)
		if cfgErr != nil {
			return fmt.Errorf("load pipeline config from %s: %v", repoRoot, cfgErr)
		}
		if pCfg == nil {
			return fmt.Errorf("no pipeline config found in %s", repoRoot)
		}

		if len(pCfg.Deploy.Services) == 0 && pCfg.Deploy.PreDeploy == "" {
			fmt.Println("No deploy services configured in pipeline.yaml.")
			fmt.Println("Add a deploy section with services, trigger_paths, commands, and smoke_tests.")
			return nil
		}

		// Run pre-deploy step (e.g., database migrations)
		if pCfg.Deploy.PreDeploy != "" {
			if dryRun {
				fmt.Printf("[dry-run] Would run pre-deploy: %s\n\n", pCfg.Deploy.PreDeploy)
			} else {
				fmt.Printf("Running pre-deploy: %s\n", pCfg.Deploy.PreDeploy)
				out, err := exec.CommandContext(ctx, "bash", "-c", pCfg.Deploy.PreDeploy).CombinedOutput()
				if err != nil {
					return fmt.Errorf("pre-deploy failed: %s\n%s", err, string(out))
				}
				fmt.Printf("Pre-deploy complete.\n\n")
			}
		}

		// Get all changed files from the design's tasks
		changedFiles, err := getDesignChangedFiles(ctx, repoRoot, designID)
		if err != nil {
			return fmt.Errorf("get changed files: %w", err)
		}

		if len(changedFiles) == 0 {
			fmt.Println("No file changes detected for this design.")
			return nil
		}

		fmt.Printf("Design %s changed %d files.\n\n", designID, len(changedFiles))

		// Match files against deploy trigger_paths
		var results []DeployResult

		for _, svc := range pCfg.Deploy.Services {
			if serviceFilter != "" && svc.Name != serviceFilter {
				continue
			}

			triggered := matchesTriggerPaths(changedFiles, svc.TriggerPaths)

			result := DeployResult{
				Service:   svc.Name,
				Triggered: triggered,
			}

			if !triggered {
				fmt.Printf("  %-20s  SKIP (no matching files)\n", svc.Name)
				results = append(results, result)
				continue
			}

			matchedFiles := getMatchingFiles(changedFiles, svc.TriggerPaths)
			fmt.Printf("  %-20s  TRIGGERED (%d files match)\n", svc.Name, len(matchedFiles))
			for _, f := range matchedFiles {
				fmt.Printf("    %s\n", f)
			}

			if dryRun {
				fmt.Printf("    [dry-run] Would run: %s\n", svc.Command)
				if svc.SmokeTest != "" {
					fmt.Printf("    [dry-run] Smoke test: %s\n", svc.SmokeTest)
				}
				result.Deployed = true
				result.SmokePassed = true
				results = append(results, result)
				continue
			}

			// Deploy
			timeout := 5 * time.Minute
			if svc.Timeout != "" {
				if parsed, err := time.ParseDuration(svc.Timeout); err == nil {
					timeout = parsed
				}
			}

			start := time.Now()
			deployCtx, cancel := context.WithTimeout(ctx, timeout)

			fmt.Printf("    Deploying: %s\n", svc.Command)
			parts := []string{"bash", "-c", svc.Command}
			deployOut, deployErr := exec.CommandContext(deployCtx, parts[0], parts[1:]...).CombinedOutput()
			cancel()

			result.Duration = time.Since(start).Round(time.Second).String()

			if deployErr != nil {
				result.Error = fmt.Sprintf("deploy failed: %s\n%s", deployErr, string(deployOut))
				fmt.Printf("    DEPLOY FAILED: %s\n", deployErr)
				fmt.Printf("    %s\n", string(deployOut))
				results = append(results, result)
				continue
			}

			result.Deployed = true
			fmt.Printf("    Deploy succeeded (%s)\n", result.Duration)

			// Smoke test
			if svc.SmokeTest != "" {
				fmt.Printf("    Smoke test: %s\n", svc.SmokeTest)

				// Wait a moment for service to stabilize
				time.Sleep(3 * time.Second)

				smokeCtx, smokeCancel := context.WithTimeout(ctx, 30*time.Second)
				smokeOut, smokeErr := exec.CommandContext(smokeCtx, "bash", "-c", svc.SmokeTest).CombinedOutput()
				smokeCancel()

				if smokeErr != nil {
					result.SmokePassed = false
					result.Error = fmt.Sprintf("smoke test failed: %s\n%s", smokeErr, string(smokeOut))
					fmt.Printf("    SMOKE TEST FAILED: %s\n", smokeErr)

					// Rollback
					if svc.Rollback != "" {
						fmt.Printf("    Rolling back: %s\n", svc.Rollback)
						rollbackOut, rollbackErr := exec.CommandContext(ctx, "bash", "-c", svc.Rollback).CombinedOutput()
						if rollbackErr != nil {
							fmt.Printf("    ROLLBACK FAILED: %s\n%s\n", rollbackErr, string(rollbackOut))
						} else {
							result.RolledBack = true
							fmt.Printf("    Rolled back successfully.\n")
						}
					} else {
						fmt.Printf("    No rollback command configured — manual intervention needed.\n")
					}

					results = append(results, result)
					continue
				}

				result.SmokePassed = true
				fmt.Printf("    Smoke test passed.\n")
			} else {
				result.SmokePassed = true // no smoke test = assume ok
			}

			results = append(results, result)
		}

		if outputFormat == "json" {
			s, _ := client.FormatJSON(results)
			fmt.Println(s)
			return nil
		}

		// Summary
		fmt.Printf("\n=== Deploy Summary ===\n")
		deployed := 0
		skipped := 0
		failed := 0
		rolledBack := 0
		for _, r := range results {
			if !r.Triggered {
				skipped++
			} else if r.Error != "" {
				failed++
				if r.RolledBack {
					rolledBack++
				}
			} else {
				deployed++
			}
		}
		fmt.Printf("Deployed: %d | Skipped: %d | Failed: %d | Rolled back: %d\n", deployed, skipped, failed, rolledBack)

		if failed > 0 {
			return fmt.Errorf("%d service(s) failed to deploy", failed)
		}
		return nil
	},
}

// getDesignChangedFiles returns all files changed by a design's tasks.
// Tries three strategies:
// 1. Branch diff (if branch still exists and hasn't been merged)
// 2. Merge commit on main (find commit with task ID in message)
// 3. PR metadata (if available, get files from GitHub)
func getDesignChangedFiles(ctx context.Context, repoRoot, designID string) ([]string, error) {
	if conn == nil {
		return nil, fmt.Errorf("no connector")
	}

	edges, err := conn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return nil, err
	}

	fileSet := make(map[string]bool)
	for _, e := range edges {
		item, err := conn.Get(ctx, e.ItemID)
		if err != nil || (item.Type != "task" && item.Type != "bug") {
			continue
		}

		taskID := e.ItemID
		var files []string

		// Strategy 1: find merge commit on main containing the task ID
		// This is the most reliable — works after merge when branches are stale
		{
			// Search git log for commits mentioning this task ID
			logOut, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "log", "--all", "--grep="+taskID, "--format=%H", "-5").CombinedOutput()
			if err == nil {
				for _, hash := range strings.Split(strings.TrimSpace(string(logOut)), "\n") {
					hash = strings.TrimSpace(hash)
					if hash == "" {
						continue
					}
					// Get files changed in this commit
					diffOut, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "diff-tree", "--no-commit-id", "-r", "--name-only", hash).CombinedOutput()
					if err == nil {
						for _, line := range strings.Split(strings.TrimSpace(string(diffOut)), "\n") {
							line = strings.TrimSpace(line)
							if line != "" && !strings.HasPrefix(line, ".cobuild/") {
								files = append(files, line)
							}
						}
					}
				}
			}
		}

		// Strategy 2: branch diff fallback (works before merge)
		if len(files) == 0 {
			out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "diff", "--name-only", "main.."+taskID).CombinedOutput()
			if err == nil {
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					line = strings.TrimSpace(line)
					if line != "" && !strings.HasPrefix(line, ".cobuild/") {
						files = append(files, line)
					}
				}
			}
		}

		for _, f := range files {
			fileSet[f] = true
		}
	}

	var files []string
	for f := range fileSet {
		files = append(files, f)
	}
	return files, nil
}

// matchesTriggerPaths checks if any changed files match the service's trigger paths.
func matchesTriggerPaths(changedFiles []string, triggerPaths []string) bool {
	for _, cf := range changedFiles {
		for _, tp := range triggerPaths {
			if matchGlob(cf, tp) {
				return true
			}
		}
	}
	return false
}

// getMatchingFiles returns changed files that match trigger paths.
func getMatchingFiles(changedFiles []string, triggerPaths []string) []string {
	var matched []string
	for _, cf := range changedFiles {
		for _, tp := range triggerPaths {
			if matchGlob(cf, tp) {
				matched = append(matched, cf)
				break
			}
		}
	}
	return matched
}

// matchGlob does simple path matching supporting ** for recursive directories.
func matchGlob(path, pattern string) bool {
	// Handle ** (match any path depth)
	if strings.Contains(pattern, "**") {
		prefix := strings.Split(pattern, "**")[0]
		suffix := strings.Split(pattern, "**")[1]
		suffix = strings.TrimPrefix(suffix, "/")

		if !strings.HasPrefix(path, prefix) {
			return false
		}
		if suffix == "" {
			return true
		}
		// Check if the remaining path matches the suffix pattern
		remaining := strings.TrimPrefix(path, prefix)
		matched, _ := filepath.Match(suffix, filepath.Base(remaining))
		if matched {
			return true
		}
		// Also try matching the full remaining path
		return strings.HasSuffix(remaining, suffix) || strings.Contains(remaining, suffix)
	}

	// Simple glob
	matched, _ := filepath.Match(pattern, path)
	return matched
}

func init() {
	deployCmd.Flags().Bool("dry-run", false, "Show what would deploy without executing")
	deployCmd.Flags().String("service", "", "Deploy specific service only")
	rootCmd.AddCommand(deployCmd)
}
