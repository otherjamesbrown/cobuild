package cmd

import (
	"fmt"
	"slices"
	"strings"
)

func withTryHint(message, hint string) string {
	message = strings.TrimSpace(message)
	hint = strings.TrimSpace(hint)
	if message == "" || hint == "" || strings.Contains(message, "(Try:") {
		return message
	}
	return fmt.Sprintf("%s (Try: `%s`)", message, hint)
}

func repoMetadataHint(taskID string, repos []string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}

	choices := make([]string, 0, len(repos))
	seen := map[string]struct{}{}
	for _, repo := range repos {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			continue
		}
		if _, ok := seen[repo]; ok {
			continue
		}
		seen[repo] = struct{}{}
		choices = append(choices, repo)
	}
	slices.Sort(choices)

	target := "repo-name"
	if len(choices) > 0 {
		target = strings.Join(choices, "|")
	}
	return fmt.Sprintf("cxp shard metadata set %s repo <%s>", taskID, target)
}
