package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AssembleContext builds the content for a CLAUDE.md from the configured context layers.
// mode is "interactive" or "dispatch" (or a gate name like "gate:readiness-review").
// repoRoot is the repo directory.
// extras are additional key-value pairs to inject.
// shardFetcher is an optional function to fetch shard content by ID.
func AssembleContext(cfg *Config, repoRoot, mode string, extras map[string]string, shardFetcher func(id string) (string, string, error)) (string, error) {
	if cfg == nil || len(cfg.Context.Layers) == 0 {
		return assembleDefaultContext(cfg, repoRoot, mode, extras)
	}

	var sections []string

	for _, layer := range cfg.Context.Layers {
		if !layerActive(layer.When, mode) {
			continue
		}

		content, err := resolveLayer(layer, cfg, repoRoot, extras, shardFetcher)
		if err != nil {
			sections = append(sections, fmt.Sprintf("<!-- context layer %q failed: %v -->", layer.Name, err))
			continue
		}
		if content == "" {
			continue
		}

		sections = append(sections, fmt.Sprintf("<!-- context: %s -->\n%s", layer.Name, content))
	}

	return strings.Join(sections, "\n\n---\n\n"), nil
}

func layerActive(when, mode string) bool {
	switch when {
	case "always", "":
		return true
	case "interactive":
		return mode == "interactive"
	case "dispatch":
		return mode == "dispatch"
	default:
		return when == mode
	}
}

func resolveLayer(layer ContextLayer, cfg *Config, repoRoot string, extras map[string]string, shardFetcher func(id string) (string, string, error)) (string, error) {
	source := layer.Source

	switch {
	case strings.HasPrefix(source, "file:"):
		path := strings.TrimPrefix(source, "file:")
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoRoot, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil

	case strings.HasPrefix(source, "shard:"):
		shardID := strings.TrimPrefix(source, "shard:")
		if shardFetcher == nil {
			return "", fmt.Errorf("no shard fetcher for shard lookup")
		}
		title, content, err := shardFetcher(shardID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("# %s\n\n%s", title, content), nil

	case strings.HasPrefix(source, "skills:"):
		skillName := strings.TrimPrefix(source, "skills:")
		skillPath, err := ResolveSkill(repoRoot, skillName)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(skillPath)
		if err != nil {
			return "", err
		}
		return string(data), nil

	case source == "skills-dir":
		return resolveSkillsDir(cfg, repoRoot, layer.Filter)

	case source == "claude-md":
		path := filepath.Join(repoRoot, "CLAUDE.md")
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil
		}
		return string(data), nil

	case source == "dispatch-prompt":
		return extras["dispatch-prompt"], nil

	case source == "parent-design":
		return extras["parent-design"], nil

	case strings.HasPrefix(source, "hook:"):
		return "", nil

	default:
		if val, ok := extras[source]; ok {
			return val, nil
		}
		return "", fmt.Errorf("unknown context source: %s", source)
	}
}

func resolveSkillsDir(cfg *Config, repoRoot string, filter []string) (string, error) {
	skillsDir := "skills"
	if cfg != nil && cfg.SkillsDir != "" {
		skillsDir = cfg.SkillsDir
	}
	dir := filepath.Join(repoRoot, skillsDir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil
	}

	filterSet := make(map[string]bool)
	for _, f := range filter {
		filterSet[f] = true
	}

	var parts []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if len(filterSet) > 0 && !filterSet[e.Name()] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		parts = append(parts, string(data))
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

func assembleDefaultContext(cfg *Config, repoRoot, mode string, extras map[string]string) (string, error) {
	var sections []string

	switch mode {
	case "dispatch":
		if prompt, ok := extras["dispatch-prompt"]; ok {
			sections = append(sections, prompt)
		}
		if design, ok := extras["parent-design"]; ok && design != "" {
			sections = append(sections, design)
		}
	case "interactive":
		claudeMD := filepath.Join(repoRoot, "CLAUDE.md")
		if data, err := os.ReadFile(claudeMD); err == nil {
			sections = append(sections, string(data))
		}
	}

	return strings.Join(sections, "\n\n---\n\n"), nil
}

// WriteWorktreeCLAUDEMD generates a CLAUDE.md for a worktree based on context config.
func WriteWorktreeCLAUDEMD(cfg *Config, repoRoot, worktreePath, mode string, extras map[string]string, shardFetcher func(id string) (string, string, error)) error {
	content, err := AssembleContext(cfg, repoRoot, mode, extras, shardFetcher)
	if err != nil {
		return fmt.Errorf("assembling context: %w", err)
	}

	if content == "" {
		return nil
	}

	path := filepath.Join(worktreePath, "CLAUDE.md")
	return os.WriteFile(path, []byte(content), 0644)
}
