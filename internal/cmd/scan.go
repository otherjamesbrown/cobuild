package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Generate a project anatomy file — file index with descriptions and token estimates",
	Long: `Scans the project directory and generates .cobuild/context/always/anatomy.md
containing a file-level index. Dispatched agents can use this to understand
the codebase structure without reading every file.

Each entry includes: file path, estimated token count, and auto-detected description.
Excludes: .git, node_modules, vendor, .cobuild/sessions, binary files.`,
	Example: `  cobuild scan                     # generate anatomy
  cobuild scan --check              # check if anatomy is stale
  cobuild scan --stdout             # print to stdout instead of writing`,
	RunE: func(cmd *cobra.Command, args []string) error {
		check, _ := cmd.Flags().GetBool("check")
		stdout, _ := cmd.Flags().GetBool("stdout")

		repoRoot := findRepoRoot()

		entries, err := scanProject(repoRoot)
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		content := formatAnatomy(entries)

		if check {
			anatomyPath := filepath.Join(repoRoot, ".cobuild", "context", "always", "anatomy.md")
			existing, err := os.ReadFile(anatomyPath)
			if err != nil {
				fmt.Println("anatomy.md not found — run cobuild scan to generate.")
				return nil
			}
			if string(existing) == content {
				fmt.Printf("anatomy.md is current (%d files)\n", len(entries))
			} else {
				fmt.Printf("anatomy.md is STALE — run cobuild scan to refresh (%d files in project)\n", len(entries))
			}
			return nil
		}

		if stdout {
			fmt.Print(content)
			return nil
		}

		outDir := filepath.Join(repoRoot, ".cobuild", "context", "always")
		os.MkdirAll(outDir, 0755)
		outPath := filepath.Join(outDir, "anatomy.md")
		if err := os.WriteFile(outPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("write anatomy: %w", err)
		}
		fmt.Printf("Generated %s (%d files, %d total estimated tokens)\n", outPath, len(entries), totalTokens(entries))

		return nil
	},
}

type fileEntry struct {
	Path        string
	Dir         string
	Name        string
	Lines       int
	Tokens      int
	Description string
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "__pycache__": true,
	".venv": true, "venv": true, ".tox": true, "dist": true, "build": true,
	".cobuild/sessions": true, ".claude": true, ".beads": true,
}

var skipExts = map[string]bool{
	".exe": true, ".bin": true, ".so": true, ".dylib": true, ".dll": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".zip": true, ".tar": true, ".gz": true, ".bz2": true,
	".pdf": true, ".lock": true,
}

func scanProject(repoRoot string) ([]fileEntry, error) {
	var entries []fileEntry

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(repoRoot, path)

		// Skip directories
		if info.IsDir() {
			if skipDirs[rel] || skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			// Skip nested .cobuild/sessions
			if strings.Contains(rel, ".cobuild/sessions") || strings.Contains(rel, ".cobuild\\sessions") {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip files
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if skipExts[ext] {
			return nil
		}
		if info.Size() > 1_000_000 { // skip files > 1MB
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") && info.Name() != ".cobuild.yaml" {
			return nil
		}

		// Read and analyse
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		content := string(data)
		lines := strings.Count(content, "\n") + 1
		tokens := estimateTokens(content, ext)
		desc := autoDescribe(rel, ext, content)

		entries = append(entries, fileEntry{
			Path:        rel,
			Dir:         filepath.Dir(rel),
			Name:        info.Name(),
			Lines:       lines,
			Tokens:      tokens,
			Description: desc,
		})

		return nil
	})

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	return entries, err
}

func estimateTokens(content, ext string) int {
	ratio := 3.75 // mixed default
	codeExts := map[string]bool{
		".go": true, ".py": true, ".ts": true, ".js": true, ".tsx": true, ".jsx": true,
		".rs": true, ".java": true, ".c": true, ".cpp": true, ".h": true,
		".css": true, ".scss": true, ".sql": true, ".sh": true,
	}
	proseExts := map[string]bool{
		".md": true, ".txt": true, ".rst": true,
	}
	dataExts := map[string]bool{
		".yaml": true, ".yml": true, ".json": true, ".toml": true, ".xml": true,
	}

	switch {
	case codeExts[ext]:
		ratio = 3.5
	case proseExts[ext]:
		ratio = 4.0
	case dataExts[ext]:
		ratio = 3.0
	}

	return int(float64(len(content)) / ratio)
}

func autoDescribe(relPath, ext string, content string) string {
	// Auto-detect description from file content
	lines := strings.Split(content, "\n")

	// Go files: look for package doc comment
	if ext == ".go" {
		for _, line := range lines[:min(10, len(lines))] {
			if strings.HasPrefix(line, "// Package ") {
				return strings.TrimPrefix(line, "// ")
			}
			if strings.HasPrefix(line, "package ") {
				return "Go package: " + strings.TrimPrefix(line, "package ")
			}
		}
	}

	// Python: look for module docstring
	if ext == ".py" {
		for i, line := range lines[:min(5, len(lines))] {
			if strings.HasPrefix(strings.TrimSpace(line), `"""`) || strings.HasPrefix(strings.TrimSpace(line), `'''`) {
				doc := strings.Trim(strings.TrimSpace(line), `"'`)
				if doc != "" {
					return doc
				}
				// Multi-line docstring — grab next line
				if i+1 < len(lines) {
					return strings.TrimSpace(lines[i+1])
				}
			}
		}
	}

	// Markdown: first heading
	if ext == ".md" {
		for _, line := range lines[:min(5, len(lines))] {
			if strings.HasPrefix(line, "# ") {
				return strings.TrimPrefix(line, "# ")
			}
		}
	}

	// YAML: first comment
	if ext == ".yaml" || ext == ".yml" {
		for _, line := range lines[:min(3, len(lines))] {
			if strings.HasPrefix(line, "# ") {
				return strings.TrimPrefix(line, "# ")
			}
		}
	}

	// SQL: first comment
	if ext == ".sql" {
		for _, line := range lines[:min(3, len(lines))] {
			if strings.HasPrefix(line, "-- ") {
				return strings.TrimPrefix(line, "-- ")
			}
		}
	}

	// Skill files: read frontmatter description
	if ext == ".md" && strings.HasPrefix(content, "---\n") {
		endIdx := strings.Index(content[4:], "\n---")
		if endIdx > 0 {
			fm := content[4 : 4+endIdx]
			for _, line := range strings.Split(fm, "\n") {
				if strings.HasPrefix(line, "description:") {
					return strings.TrimSpace(strings.TrimPrefix(line, "description:"))
				}
			}
		}
	}

	// Default: filename-based
	name := filepath.Base(relPath)
	dir := filepath.Dir(relPath)
	if dir != "." {
		return fmt.Sprintf("%s in %s/", name, dir)
	}
	return name
}

func formatAnatomy(entries []fileEntry) string {
	var sb strings.Builder

	sb.WriteString("# Project Anatomy\n\n")
	sb.WriteString("Auto-generated file index. Use this to understand the codebase without reading every file.\n")
	sb.WriteString("Token estimates help you decide what's worth reading vs what you can skip.\n\n")

	// Group by directory
	dirs := make(map[string][]fileEntry)
	var dirOrder []string
	for _, e := range entries {
		if _, ok := dirs[e.Dir]; !ok {
			dirOrder = append(dirOrder, e.Dir)
		}
		dirs[e.Dir] = append(dirs[e.Dir], e)
	}

	for _, dir := range dirOrder {
		files := dirs[dir]
		dirTokens := 0
		for _, f := range files {
			dirTokens += f.Tokens
		}

		if dir == "." {
			sb.WriteString(fmt.Sprintf("## Root (~%s tokens)\n\n", formatTokensShort(dirTokens)))
		} else {
			sb.WriteString(fmt.Sprintf("## %s/ (~%s tokens)\n\n", dir, formatTokensShort(dirTokens)))
		}

		for _, f := range files {
			sb.WriteString(fmt.Sprintf("- **%s** (%d lines, ~%s tok) — %s\n", f.Name, f.Lines, formatTokensShort(f.Tokens), f.Description))
		}
		sb.WriteString("\n")
	}

	total := totalTokens(entries)
	sb.WriteString(fmt.Sprintf("---\n\n%d files, ~%s tokens total\n", len(entries), formatTokensShort(total)))

	return sb.String()
}

func totalTokens(entries []fileEntry) int {
	total := 0
	for _, e := range entries {
		total += e.Tokens
	}
	return total
}

func formatTokensShort(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func init() {
	scanCmd.Flags().Bool("check", false, "Check if anatomy is stale")
	scanCmd.Flags().Bool("stdout", false, "Print to stdout instead of writing file")
	rootCmd.AddCommand(scanCmd)
}
