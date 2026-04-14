// Package cliutil provides small output and filesystem helpers used across
// cobuild's command layer. These used to live in internal/client alongside
// the legacy database client; they moved here in the cb-3f5be6 / cb-b2f3ac
// big-bang migration so internal/client could be retired.
package cliutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// FormatJSON marshals data as indented JSON.
func FormatJSON(data any) (string, error) {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// FormatYAML marshals data as YAML.
func FormatYAML(data any) (string, error) {
	b, err := yaml.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// FormatOutput formats data according to the output format string.
func FormatOutput(data any, format string) (string, error) {
	switch format {
	case "json":
		return FormatJSON(data)
	case "yaml":
		return FormatYAML(data)
	default:
		return fmt.Sprintf("%v", data), nil
	}
}

// Truncate truncates a string to maxLen characters with "..." suffix.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// TimeAgo returns a human-readable relative time string (e.g. "3h ago").
func TimeAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	}
}

// GitRepoRoot returns the root of the git repository containing dir, or
// an error if dir is not inside a git repo.
func GitRepoRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a git repository")
		}
		dir = parent
	}
}
