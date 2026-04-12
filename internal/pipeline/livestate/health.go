package livestate

import (
	"fmt"
	"strings"
	"time"
)

type pipelineHealthInput struct {
	DesignID            string
	Idle                time.Duration
	WarnAfter           time.Duration
	StallTimeout        time.Duration
	TmuxTarget          string
	HasSession          bool
	HasTmux             bool
	HasProcess          bool
	TmuxMissingKnown    bool
	ProcessMissingKnown bool
	TmuxUnavailable     bool
	ProcessUnavailable  bool
}

type sessionHealthInput struct {
	TaskID             string
	Idle               time.Duration
	WarnAfter          time.Duration
	StallTimeout       time.Duration
	TmuxTarget         string
	HasTmux            bool
	HasProcess         bool
	TmuxUnavailable    bool
	ProcessUnavailable bool
}

type tmuxHealthInput struct {
	ID                 string
	Idle               time.Duration
	WarnAfter          time.Duration
	StallTimeout       time.Duration
	TmuxTarget         string
	HasSession         bool
	HasProcess         bool
	SessionUnavailable bool
	ProcessUnavailable bool
}

func resolvePipelineHealth(input pipelineHealthInput) (Health, string, []string) {
	var warnings []string
	if input.TmuxUnavailable {
		warnings = append(warnings, "tmux state unavailable")
	}
	if input.ProcessUnavailable {
		warnings = append(warnings, "process state unavailable")
	}

	if input.HasSession && input.TmuxMissingKnown && !input.HasTmux {
		return HealthOrphan, orphanSuggestion(input.DesignID, "running session has no tmux window", input.TmuxTarget), warnings
	}
	if input.HasSession && input.ProcessMissingKnown && !input.HasProcess {
		return HealthOrphan, orphanSuggestion(input.DesignID, "running session has no orchestrate process", input.TmuxTarget), warnings
	}
	return resolveIdleHealth(input.DesignID, input.Idle, input.WarnAfter, input.StallTimeout, input.TmuxTarget, warnings)
}

func resolveSessionHealth(input sessionHealthInput) (Health, string, []string) {
	var warnings []string
	if input.TmuxUnavailable {
		warnings = append(warnings, "tmux state unavailable")
	}
	if input.ProcessUnavailable {
		warnings = append(warnings, "process state unavailable")
	}
	if !input.HasTmux {
		if input.TmuxUnavailable {
			return HealthWarn, unavailableSuggestion(input.TaskID, warnings), warnings
		}
		return HealthOrphan, orphanSuggestion(input.TaskID, "DB session has no tmux window", input.TmuxTarget), warnings
	}
	if !input.HasProcess {
		if input.ProcessUnavailable {
			return HealthWarn, unavailableSuggestion(input.TaskID, warnings), warnings
		}
		return HealthOrphan, orphanSuggestion(input.TaskID, "DB session has no orchestrate process", input.TmuxTarget), warnings
	}
	return resolveIdleHealth(input.TaskID, input.Idle, input.WarnAfter, input.StallTimeout, input.TmuxTarget, warnings)
}

func resolveTmuxHealth(input tmuxHealthInput) (Health, string, []string) {
	var warnings []string
	if input.SessionUnavailable {
		warnings = append(warnings, "session state unavailable")
	}
	if input.ProcessUnavailable {
		warnings = append(warnings, "process state unavailable")
	}
	if !input.HasSession {
		if input.SessionUnavailable {
			return HealthWarn, unavailableSuggestion(input.ID, warnings), warnings
		}
		return HealthOrphan, orphanSuggestion(input.ID, "tmux window has no running DB session", input.TmuxTarget), warnings
	}
	if !input.HasProcess {
		if input.ProcessUnavailable {
			return HealthWarn, unavailableSuggestion(input.ID, warnings), warnings
		}
		return HealthOrphan, orphanSuggestion(input.ID, "tmux window has no orchestrate process", input.TmuxTarget), warnings
	}
	return resolveIdleHealth(input.ID, input.Idle, input.WarnAfter, input.StallTimeout, input.TmuxTarget, warnings)
}

func resolveIdleHealth(id string, idle, warnAfter, stallTimeout time.Duration, tmuxTarget string, warnings []string) (Health, string, []string) {
	if stallTimeout > 0 && idle >= stallTimeout {
		return HealthStale, staleSuggestion(id, idle, tmuxTarget), warnings
	}
	if warnAfter > 0 && idle >= warnAfter {
		if len(warnings) > 0 {
			return HealthWarn, unavailableSuggestion(id, warnings), warnings
		}
		return HealthWarn, warnSuggestion(id, idle, tmuxTarget), warnings
	}
	if len(warnings) > 0 {
		return HealthWarn, unavailableSuggestion(id, warnings), warnings
	}
	return HealthOK, "", warnings
}

func staleSuggestion(id string, idle time.Duration, tmuxTarget string) string {
	if tmuxTarget != "" {
		return fmt.Sprintf("%s stale %s — check %s or `cobuild reset %s`", id, formatDuration(idle), tmuxTarget, id)
	}
	return fmt.Sprintf("%s stale %s — check live state or `cobuild reset %s`", id, formatDuration(idle), id)
}

func warnSuggestion(id string, idle time.Duration, tmuxTarget string) string {
	if tmuxTarget != "" {
		return fmt.Sprintf("%s idle %s — inspect %s", id, formatDuration(idle), tmuxTarget)
	}
	return fmt.Sprintf("%s idle %s — inspect live state", id, formatDuration(idle))
}

func orphanSuggestion(id, reason, tmuxTarget string) string {
	if tmuxTarget != "" {
		return fmt.Sprintf("%s orphaned — %s; check %s or `cobuild reset %s`", id, reason, tmuxTarget, id)
	}
	return fmt.Sprintf("%s orphaned — %s; consider `cobuild reset %s`", id, reason, id)
}

func unavailableSuggestion(id string, warnings []string) string {
	return fmt.Sprintf("%s warning — %s", id, strings.Join(warnings, "; "))
}

func idleDuration(now, since time.Time) time.Duration {
	if since.IsZero() || now.Before(since) {
		return 0
	}
	return now.Sub(since)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d >= time.Minute {
		return strings.TrimSuffix(d.Round(time.Minute).String(), "0s")
	}
	return d.Round(time.Second).String()
}
