package review

import "strings"

// ExtractAcceptanceCriteria returns the checklist or bullet items from the
// Acceptance Criteria section in a deterministic order.
func ExtractAcceptanceCriteria(taskContent string) []string {
	lines := strings.Split(strings.ReplaceAll(taskContent, "\r\n", "\n"), "\n")
	inSection := false
	var criteria []string

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if isAcceptanceHeading(line) {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if isHeading(line) {
			break
		}
		if item, ok := parseAcceptanceItem(line); ok {
			criteria = append(criteria, item)
		}
	}

	return criteria
}

func isAcceptanceHeading(line string) bool {
	if !isHeading(line) {
		return false
	}
	trimmed := strings.TrimSpace(strings.TrimLeft(line, "#"))
	return strings.EqualFold(trimmed, "Acceptance Criteria")
}

func isHeading(line string) bool {
	if line == "" || line[0] != '#' {
		return false
	}
	return len(strings.TrimLeft(line, "#")) != len(line)
}

func parseAcceptanceItem(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false
	}

	switch {
	case strings.HasPrefix(line, "- [ ] "), strings.HasPrefix(line, "- [x] "), strings.HasPrefix(line, "- [X] "):
		return strings.TrimSpace(line[6:]), true
	case strings.HasPrefix(line, "* [ ] "), strings.HasPrefix(line, "* [x] "), strings.HasPrefix(line, "* [X] "):
		return strings.TrimSpace(line[6:]), true
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
		return strings.TrimSpace(line[2:]), true
	}

	for i := 0; i < len(line); i++ {
		if line[i] < '0' || line[i] > '9' {
			if i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' ' {
				return strings.TrimSpace(line[i+2:]), true
			}
			break
		}
	}

	return "", false
}
