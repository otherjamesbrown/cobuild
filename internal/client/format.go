package client

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// FormatJSON marshals data as indented JSON.
func FormatJSON(data interface{}) (string, error) {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// FormatYAML marshals data as YAML.
func FormatYAML(data interface{}) (string, error) {
	b, err := yaml.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// FormatOutput formats data according to the output format.
func FormatOutput(data interface{}, format string) (string, error) {
	switch format {
	case "json":
		return FormatJSON(data)
	case "yaml":
		return FormatYAML(data)
	default:
		return fmt.Sprintf("%v", data), nil
	}
}

// Truncate truncates a string to maxLen with "..." suffix.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
