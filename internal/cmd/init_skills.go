package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

var defaultSkills = []string{
	"shared/create-design.md",
	"shared/playbook.md",
	"design/gate-readiness-review.md",
	"design/implementability.md",
	"implement/dispatch-task.md",
	"implement/stall-check.md",
	"review/gate-review-pr.md",
	"review/gate-process-review.md",
	"review/merge-and-verify.md",
	"done/gate-retrospective.md",
}

var initSkillsCmd = &cobra.Command{
	Use:   "init-skills",
	Short: "Copy default pipeline skills into the repo",
	Long: `Copies default skill files into the repo's skills directory.
Skills can then be customized per-repo. Existing files are not overwritten
unless --force is specified.`,
	Example: `  cobuild init-skills
  cobuild init-skills --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
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

		home, _ := os.UserHomeDir()
		sourceDirs := []string{
			filepath.Join(home, ".cobuild", "skills"),
		}
		cpRoot, _ := config.RepoForProject("context-palace")
		if cpRoot != "" {
			sourceDirs = append(sourceDirs, filepath.Join(cpRoot, "skills"))
		}

		if dryRun {
			fmt.Printf("Would create: %s/\n", destDir)
		} else {
			os.MkdirAll(destDir, 0755)
		}

		copied := 0
		skipped := 0
		for _, skill := range defaultSkills {
			destPath := filepath.Join(destDir, skill)

			if !force {
				if _, err := os.Stat(destPath); err == nil {
					if dryRun {
						fmt.Printf("  SKIP  %s (exists)\n", skill)
					}
					skipped++
					continue
				}
			}

			srcPath := ""
			for _, dir := range sourceDirs {
				candidate := filepath.Join(dir, skill)
				if _, err := os.Stat(candidate); err == nil {
					srcPath = candidate
					break
				}
			}

			if srcPath == "" {
				fmt.Printf("  MISS  %s (not found in source dirs)\n", skill)
				continue
			}

			if dryRun {
				fmt.Printf("  COPY  %s -> %s\n", srcPath, destPath)
			} else {
				if err := copyFile(srcPath, destPath); err != nil {
					fmt.Printf("  FAIL  %s: %v\n", skill, err)
					continue
				}
				fmt.Printf("  COPY  %s\n", skill)
			}
			copied++
		}

		fmt.Printf("\n%d copied, %d skipped (use --force to overwrite)\n", copied, skipped)
		return nil
	},
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

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
	initSkillsCmd.Flags().Bool("dry-run", false, "Show what would be copied")
	rootCmd.AddCommand(initSkillsCmd)
}
