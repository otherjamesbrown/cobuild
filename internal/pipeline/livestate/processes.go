package livestate

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CollectProcesses returns active cobuild orchestrate/poller processes from ps.
func CollectProcesses(ctx context.Context, execFn CommandRunner, now time.Time) ([]ProcessInfo, error) {
	if execFn == nil {
		execFn = defaultCommandRunner
	}

	out, err := execFn(ctx, "ps", "auxww")
	if err != nil {
		return nil, fmt.Errorf("ps auxww: %w", err)
	}
	return ParseProcesses(string(out), now)
}

// ParseProcesses parses ps auxww output into live CoBuild process rows.
func ParseProcesses(raw string, now time.Time) ([]ProcessInfo, error) {
	lines := strings.Split(raw, "\n")
	rows := make([]ProcessInfo, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}
		if _, err := strconv.Atoi(fields[1]); err != nil {
			continue
		}

		commandFields := fields[10:]
		commandIndex, subcommand, ok := findCobuildSubcommand(commandFields)
		if !ok {
			continue
		}

		pid, _ := strconv.Atoi(fields[1])
		command := strings.Join(commandFields, " ")
		project := findProjectFlag(commandFields)
		target := findSubcommandTarget(commandFields[commandIndex:])

		var startedAt *time.Time
		var ageSeconds int64
		if started, ok := parsePSStart(fields[8], now); ok {
			startedAt = &started
			ageSeconds = maxInt64(0, int64(now.Sub(started).Seconds()))
		}

		rows = append(rows, ProcessInfo{
			PID:        pid,
			Kind:       subcommand,
			Project:    project,
			TargetID:   target,
			StartedAt:  startedAt,
			AgeSeconds: ageSeconds,
			Command:    command,
		})
	}

	return rows, nil
}

func findCobuildSubcommand(fields []string) (int, string, bool) {
	for i, field := range fields {
		if !looksLikeCobuildBinary(field) {
			continue
		}
		for j := i + 1; j < len(fields); j++ {
			switch fields[j] {
			case "orchestrate", "poller":
				return j, fields[j], true
			}
		}
		return -1, "", false
	}
	return -1, "", false
}

func looksLikeCobuildBinary(token string) bool {
	base := filepath.Base(token)
	return base == "cobuild"
}

func findProjectFlag(fields []string) string {
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == "--project" && i+1 < len(fields) {
			return fields[i+1]
		}
		if value, ok := strings.CutPrefix(field, "--project="); ok {
			return value
		}
	}
	return ""
}

func findSubcommandTarget(fields []string) string {
	if len(fields) == 0 || fields[0] != "orchestrate" {
		return ""
	}
	for _, field := range fields[1:] {
		if field == "" || strings.HasPrefix(field, "-") {
			continue
		}
		return extractTargetID(field)
	}
	return ""
}

func parsePSStart(token string, now time.Time) (time.Time, bool) {
	location := now.Location()
	parsers := []func(string, time.Time) (time.Time, bool){
		parseSameDayClock,
		parseMonthDay,
		parseMonthDayYear,
	}
	for _, parser := range parsers {
		if ts, ok := parser(token, now.In(location)); ok {
			return ts, true
		}
	}
	return time.Time{}, false
}

func parseSameDayClock(token string, now time.Time) (time.Time, bool) {
	for _, layout := range []string{"15:04", "3:04PM", "3:04pm"} {
		parsed, err := time.ParseInLocation(layout, token, now.Location())
		if err != nil {
			continue
		}
		result := time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), 0, 0, now.Location())
		if result.After(now) {
			result = result.Add(-24 * time.Hour)
		}
		return result, true
	}
	return time.Time{}, false
}

func parseMonthDay(token string, now time.Time) (time.Time, bool) {
	for _, layout := range []string{"Jan2", "Jan02"} {
		parsed, err := time.ParseInLocation(layout, token, now.Location())
		if err != nil {
			continue
		}
		result := time.Date(now.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, now.Location())
		if result.After(now) {
			result = result.AddDate(-1, 0, 0)
		}
		return result, true
	}
	return time.Time{}, false
}

func parseMonthDayYear(token string, now time.Time) (time.Time, bool) {
	for _, layout := range []string{"2Jan06", "02Jan06", "Jan2,2006", "Jan02,2006"} {
		parsed, err := time.ParseInLocation(layout, token, now.Location())
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

var targetIDPattern = regexp.MustCompile(`\b[a-z][a-z0-9]*-[a-z0-9]+\b`)

func extractTargetID(value string) string {
	return targetIDPattern.FindString(strings.ToLower(value))
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
