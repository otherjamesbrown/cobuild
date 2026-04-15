package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/spf13/cobra"
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Generate a project anatomy file — file index with descriptions and token estimates",
	Long: `Scans the project directory and generates .cobuild/context/implement/anatomy.md
containing a file-level index. Dispatched agents can use this to understand
the codebase structure without reading every file.

Each entry includes: file path, estimated token count, and auto-detected description.
Excludes: VCS dirs, deps, caches, build output, coverage, binaries. Large
directories are collapsed to a one-line summary to keep anatomy.md small. Scan
also skips recursive context output and common generated protobuf stubs.

Project-specific excludes: put one path per line in .cobuild/scan-exclude
(supports comments with #). Paths are matched against the directory's
relative path OR its basename.`,
	Example: `  cobuild scan                     # generate anatomy
  cobuild scan --check              # check if anatomy is stale
  cobuild scan --stdout             # print to stdout instead of writing
  cobuild scan --verbose            # do not collapse large directories`,
	RunE: func(cmd *cobra.Command, args []string) error {
		check, _ := cmd.Flags().GetBool("check")
		stdout, _ := cmd.Flags().GetBool("stdout")
		verbose, _ := cmd.Flags().GetBool("verbose")

		repoRoot := findRepoRoot()

		entries, err := scanProject(repoRoot)
		if err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		content := formatAnatomy(entries, verbose)

		if check {
			anatomyPath := anatomyOutputPath(repoRoot)
			existing, err := os.ReadFile(anatomyPath)
			if err != nil {
				if _, legacyErr := os.Stat(legacyAnatomyPath(repoRoot)); legacyErr == nil {
					fmt.Printf("anatomy.md not found at %s — run cobuild scan to migrate from legacy always/ path.\n", filepath.ToSlash(strings.TrimPrefix(anatomyPath, repoRoot+string(filepath.Separator))))
					return nil
				}
				fmt.Printf("anatomy.md not found at %s — run cobuild scan to generate.\n", filepath.ToSlash(strings.TrimPrefix(anatomyPath, repoRoot+string(filepath.Separator))))
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

		outPath := anatomyOutputPath(repoRoot)
		outDir := filepath.Dir(outPath)
		os.MkdirAll(outDir, 0755)
		if err := os.WriteFile(outPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("write anatomy: %w", err)
		}
		if err := removeLegacyAnatomy(repoRoot); err != nil {
			return fmt.Errorf("remove legacy anatomy: %w", err)
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

// skipDirs are directory names (or repo-relative paths) that scan never
// descends into. Every entry is a well-known convention across Go/Python/
// JS/Rust/Ruby — tool caches, language build output, dependency trees,
// coverage reports. Project-specific dirs go in .cobuild/scan-exclude.
var skipDirs = map[string]bool{
	// VCS + deps
	".git": true, "node_modules": true, "vendor": true,
	// Python caches/envs
	"__pycache__": true, ".venv": true, "venv": true, ".tox": true,
	".pytest_cache": true, ".ruff_cache": true, ".mypy_cache": true,
	".eggs": true,
	// Build / dist output
	"dist": true, "build": true, "target": true, "out": true,
	// Coverage / test artifacts
	"coverage": true, "htmlcov": true, ".nyc_output": true,
	// CoBuild / Claude / Beads / tool internals
	".cobuild/sessions": true, ".cobuild/context": true, ".claude": true, ".beads": true, ".specify": true,
}

var skipExts = map[string]bool{
	".exe": true, ".bin": true, ".so": true, ".dylib": true, ".dll": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".zip": true, ".tar": true, ".gz": true, ".bz2": true,
	".pdf": true, ".lock": true,
}

var skipFileSuffixes = []string{
	".pb.go",
}

var skipFilePaths = map[string]bool{
	".cobuild/dispatch-context.md": true,
	".cobuild/dispatch.log":        true,
	".cobuild/gate-verdict.json":   true,
	".cobuild/last-prompt.md":      true,
	".cobuild/session.err":         true,
	".cobuild/session.log":         true,
	".cobuild/session_id":          true,
}

func anatomyOutputPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".cobuild", "context", "implement", "anatomy.md")
}

func legacyAnatomyPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".cobuild", "context", "always", "anatomy.md")
}

func removeLegacyAnatomy(repoRoot string) error {
	legacyPath := legacyAnatomyPath(repoRoot)
	if legacyPath == anatomyOutputPath(repoRoot) {
		return nil
	}
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func scanProject(repoRoot string) ([]fileEntry, error) {
	var entries []fileEntry

	projectSkips := loadProjectScanExcludes(repoRoot)

	// Merge pipeline.yaml scan.skip_dirs into the project skip set. Config
	// entries can be repo-relative paths or directory basenames; both
	// forms are keyed into projectSkips for O(1) lookup during the walk.
	if cfg, err := config.LoadConfig(repoRoot); err == nil && cfg != nil {
		for _, entry := range cfg.Scan.SkipDirs {
			entry = strings.TrimSpace(entry)
			entry = strings.TrimRight(entry, "/")
			if entry == "" {
				continue
			}
			projectSkips[entry] = true
		}
	}

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
			if projectSkips[rel] || projectSkips[info.Name()] {
				return filepath.SkipDir
			}
			// Skip nested .cobuild/sessions
			if strings.Contains(rel, ".cobuild/sessions") || strings.Contains(rel, ".cobuild\\sessions") {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip files
		relSlash := filepath.ToSlash(rel)
		if skipFilePaths[relSlash] {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if skipExts[ext] {
			return nil
		}
		lowerName := strings.ToLower(info.Name())
		for _, suffix := range skipFileSuffixes {
			if strings.HasSuffix(lowerName, suffix) {
				return nil
			}
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

// loadProjectScanExcludes reads .cobuild/scan-exclude (one path per line,
// # for comments). Returned map keys can be matched against either the
// directory's repo-relative path or its basename. Missing file → empty map.
func loadProjectScanExcludes(repoRoot string) map[string]bool {
	out := map[string]bool{}
	path := filepath.Join(repoRoot, ".cobuild", "scan-exclude")
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[strings.TrimRight(line, "/")] = true
	}
	return out
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

// Directory collapse thresholds. Anatomy.md is supposed to be a short
// index, not an exhaustive listing. Directories that cross either bound
// get summarized to one line unless --verbose is set.
const (
	collapseDirTokens = 10_000
	collapseDirFiles  = 30
)

func formatAnatomy(entries []fileEntry, verbose bool) string {
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

		if !verbose && (dirTokens >= collapseDirTokens || len(files) >= collapseDirFiles) {
			sb.WriteString(fmt.Sprintf("%d files, ~%s tokens — summarized. Use `cobuild scan --verbose` to expand.\n\n",
				len(files), formatTokensShort(dirTokens)))
			continue
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
	scanCmd.Flags().Bool("verbose", false, "Do not collapse large directories into one-line summaries")
	rootCmd.AddCommand(scanCmd)
}
