package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// findRepoRoot returns the git repo root, falling back to cwd.
func findRepoRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	cwd, _ := os.Getwd()
	return cwd
}

// projectYAML holds values from .cobuild.yaml.
type projectYAML struct {
	Project string
	Prefix  string
}

// readProjectConfigFromYAML reads project config from .cobuild.yaml in the repo root.
func readProjectConfigFromYAML(repoRoot string) projectYAML {
	var result projectYAML
	// Only .cobuild.yaml is recognised — the .cxp.yaml / .cp.yaml legacy
	// fallbacks were retired in cb-3f5be6 / cb-9a336c. Repos that still
	// have a legacy config should rename it.
	for _, name := range []string{".cobuild.yaml"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			continue
		}
		var cfg struct {
			Project string `yaml:"project"`
			Prefix  string `yaml:"prefix"`
		}
		if yaml.Unmarshal(data, &cfg) == nil {
			if cfg.Project != "" {
				result.Project = cfg.Project
			}
			if cfg.Prefix != "" {
				result.Prefix = cfg.Prefix
			}
			return result
		}
	}
	return result
}

// resolveBody resolves body content from --body or --body-file flags.
func resolveBody(body, bodyFile string) (string, error) {
	if bodyFile != "" {
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", fmt.Errorf("cannot read body file %q: %w", bodyFile, err)
		}
		return string(data), nil
	}
	return body, nil
}

// timeAgo returns a human-readable time difference.
func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}
