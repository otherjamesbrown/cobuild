package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

var defaultSkills = []string{
	"design/gate-readiness-review.md",
	"design/implementability.md",
	"decompose/decompose-design.md",
	"investigate/bug-investigation.md",
	"implement/dispatch-task.md",
	"implement/stall-check.md",
	"review/dispatch-review.md",
	"review/gate-review-pr.md",
	"review/gate-process-review.md",
	"review/merge-and-verify.md",
	"done/gate-retrospective.md",
	"shared/playbook.md",
	"shared/playbook/startup.md",
	"shared/playbook/phase-design.md",
	"shared/playbook/phase-decompose.md",
	"shared/playbook/phase-implement.md",
	"shared/playbook/phase-review.md",
	"shared/playbook/phase-done.md",
	"shared/playbook/escalation.md",
	"shared/create-design.md",
	"shared/design-review.md",
}

var initSkillsCmd = &cobra.Command{
	Use:   "init-skills",
	Short: "Copy or update default pipeline skills in the repo",
	Long: `Copies default skill files into the repo's skills directory.

With --update: refreshes skills from CoBuild defaults while preserving
your customizations (Gotchas sections). Use this after updating CoBuild
to get new skills and skill improvements.

Existing files are not overwritten unless --force or --update is specified.`,
	Example: `  cobuild init-skills              # first-time copy
  cobuild init-skills --update     # refresh, preserve gotchas
  cobuild init-skills --force      # overwrite everything
  cobuild init-skills --dry-run    # show what would change`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		update, _ := cmd.Flags().GetBool("update")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		repoRoot, err := config.RepoForProject(projectName)
		if err != nil {
			cwd, _ := os.Getwd()
			repoRoot, err = client.GitRepoRoot(cwd)
			if err != nil {
				return fmt.Errorf("not in a git repo and project not registered")
			}
		}

		pCfg, _ := config.LoadConfig(repoRoot)
		skillsDir := "skills"
		if pCfg != nil && pCfg.SkillsDir != "" {
			skillsDir = pCfg.SkillsDir
		}

		destDir := filepath.Join(repoRoot, skillsDir)

		// Source directories — prefer the current git worktree when available,
		// then fall back to the registered cobuild repo and global installs.
		home, _ := os.UserHomeDir()
		sourceDirs := []string{}
		cwd, _ := os.Getwd()
		if cwd != "" {
			if cwdRepo, err := client.GitRepoRoot(cwd); err == nil {
				sourceDirs = append(sourceDirs, filepath.Join(cwdRepo, "skills"))
			}
		}
		// Add cobuild repo itself as source (for development)
		cobuildRepo, _ := config.RepoForProject("cobuild")
		if cobuildRepo != "" {
			sourcePath := filepath.Join(cobuildRepo, "skills")
			seen := false
			for _, existing := range sourceDirs {
				if existing == sourcePath {
					seen = true
					break
				}
			}
			if !seen {
				sourceDirs = append(sourceDirs, sourcePath)
			}
		}
		sourceDirs = append(sourceDirs, filepath.Join(home, ".cobuild", "skills"))

		if dryRun {
			fmt.Printf("Would create: %s/\n", destDir)
			fmt.Printf("Source dirs: %s\n", strings.Join(sourceDirs, ", "))
		} else {
			os.MkdirAll(destDir, 0755)
		}

		copied := 0
		updated := 0
		skipped := 0
		added := 0

		for _, skill := range defaultSkills {
			destPath := filepath.Join(destDir, skill)
			exists := false
			if _, err := os.Stat(destPath); err == nil {
				exists = true
			}

			if exists && !force && !update {
				if dryRun {
					fmt.Printf("  SKIP    %s (exists)\n", skill)
				}
				skipped++
				continue
			}

			// Find source
			srcPath := ""
			for _, dir := range sourceDirs {
				candidate := filepath.Join(dir, skill)
				if _, err := os.Stat(candidate); err == nil {
					srcPath = candidate
					break
				}
			}

			if srcPath == "" {
				fmt.Printf("  MISS    %s (not found in source dirs)\n", skill)
				continue
			}

			if exists && update {
				// Preserve gotchas section from existing file
				gotchas := extractGotchas(destPath)
				if dryRun {
					fmt.Printf("  UPDATE  %s", skill)
					if gotchas != "" {
						fmt.Printf(" (preserving %d chars of gotchas)", len(gotchas))
					}
					fmt.Println()
				} else {
					if err := copyFile(srcPath, destPath); err != nil {
						fmt.Printf("  FAIL    %s: %v\n", skill, err)
						continue
					}
					if gotchas != "" {
						replaceGotchas(destPath, gotchas)
					}
					fmt.Printf("  UPDATE  %s\n", skill)
				}
				updated++
			} else {
				// Fresh copy
				if dryRun {
					fmt.Printf("  COPY    %s\n", skill)
				} else {
					os.MkdirAll(filepath.Dir(destPath), 0755)
					if err := copyFile(srcPath, destPath); err != nil {
						fmt.Printf("  FAIL    %s: %v\n", skill, err)
						continue
					}
					fmt.Printf("  COPY    %s\n", skill)
				}
				if exists {
					copied++
				} else {
					added++
				}
			}
		}

		fmt.Printf("\n%d copied, %d updated, %d added, %d skipped\n", copied, updated, added, skipped)
		if skipped > 0 && !update && !force {
			fmt.Println("Use --update to refresh skills (preserves gotchas) or --force to overwrite.")
		}
		return nil
	},
}

// extractGotchas reads a skill file and returns the Gotchas section content.
func extractGotchas(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := string(data)
	idx := strings.Index(content, "## Gotchas")
	if idx == -1 {
		return ""
	}
	return content[idx:]
}

// replaceGotchas replaces the Gotchas section in a skill file with preserved content.
func replaceGotchas(path, gotchas string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)
	idx := strings.Index(content, "## Gotchas")
	if idx == -1 {
		// Append gotchas if the new default doesn't have a gotchas section
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + gotchas
	} else {
		// Replace the gotchas section
		content = content[:idx] + gotchas
	}
	os.WriteFile(path, []byte(content), 0644)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	os.MkdirAll(filepath.Dir(dst), 0755)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func init() {
	initSkillsCmd.Flags().Bool("force", false, "Overwrite existing skill files")
	initSkillsCmd.Flags().Bool("update", false, "Refresh skills from defaults, preserving Gotchas sections")
	initSkillsCmd.Flags().Bool("dry-run", false, "Show what would be changed")
	rootCmd.AddCommand(initSkillsCmd)
}
