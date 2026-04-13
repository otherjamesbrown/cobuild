package livestate

import (
	"fmt"
	"time"
)

// Health is the resolved state of a pipeline/session/window after
// cross-referencing live sources.
type Health string

const (
	HealthOK     Health = "OK"
	HealthWarn   Health = "WARN"   // idle past warn threshold
	HealthStale  Health = "STALE"  // idle past stall threshold
	HealthOrphan Health = "ORPHAN" // exists in one source, missing from another
)

// HealthThresholds configure when sessions/pipelines flip from OK→WARN→STALE.
type HealthThresholds struct {
	WarnIdle  time.Duration
	StaleIdle time.Duration
}

// DefaultHealthThresholds returns sensible defaults: 10m for WARN, 30m for STALE.
func DefaultHealthThresholds() HealthThresholds {
	return HealthThresholds{
		WarnIdle:  10 * time.Minute,
		StaleIdle: 30 * time.Minute,
	}
}

// PipelineHealth resolves a pipeline row's health by cross-referencing its
// session age, tmux presence, and orchestrate process presence.
type PipelineHealth struct {
	DesignID    string
	Project     string
	Phase       string
	Health      Health
	TmuxTarget  string // "cobuild-context-palace:cp-b25138" or ""
	SessionID   string
	OrchestPID  int
	Suggestion  string
	IdleSeconds int64
}

// ComputeHealth produces per-pipeline health rows by cross-referencing
// the snapshot's pipelines, sessions, tmux windows, and processes.
func ComputeHealth(snap Snapshot, t HealthThresholds) []PipelineHealth {
	if t.WarnIdle == 0 && t.StaleIdle == 0 {
		t = DefaultHealthThresholds()
	}

	// Index sessions by design ID for lookup
	sessionsByDesign := map[string][]SessionInfo{}
	for _, s := range snap.Sessions {
		sessionsByDesign[s.DesignID] = append(sessionsByDesign[s.DesignID], s)
	}

	// Index tmux windows by target ID
	tmuxByTarget := map[string][]TmuxWindow{}
	for _, w := range snap.Tmux {
		if w.TargetID != "" {
			tmuxByTarget[w.TargetID] = append(tmuxByTarget[w.TargetID], w)
		}
	}

	// Index orchestrate processes by target ID
	procsByTarget := map[string][]ProcessInfo{}
	for _, p := range snap.Processes {
		if p.TargetID != "" {
			procsByTarget[p.TargetID] = append(procsByTarget[p.TargetID], p)
		}
	}

	out := make([]PipelineHealth, 0, len(snap.Pipelines))
	for _, p := range snap.Pipelines {
		ph := PipelineHealth{
			DesignID: p.DesignID,
			Project:  p.Project,
			Phase:    p.Phase,
			Health:   HealthOK,
		}

		sessions := sessionsByDesign[p.DesignID]
		windows := tmuxByTarget[p.DesignID]
		procs := procsByTarget[p.DesignID]

		// Session present → check idle and tmux cross-ref
		if len(sessions) > 0 {
			s := sessions[0] // most recent if multiple
			ph.SessionID = s.ID
			ph.IdleSeconds = s.AgeSeconds
			if s.TmuxSession != "" && s.TmuxWindow != "" {
				ph.TmuxTarget = s.TmuxSession + ":" + s.TmuxWindow
			}

			idle := time.Duration(s.AgeSeconds) * time.Second
			switch {
			case len(windows) == 0:
				ph.Health = HealthOrphan
				ph.Suggestion = fmt.Sprintf("session %s running but no tmux window — `cobuild reset %s`", s.ID, p.DesignID)
			case idle >= t.StaleIdle:
				ph.Health = HealthStale
				ph.Suggestion = fmt.Sprintf("session idle %s — check %s or `cobuild reset %s`", formatDuration(idle), ph.TmuxTarget, p.DesignID)
			case idle >= t.WarnIdle:
				ph.Health = HealthWarn
				ph.Suggestion = fmt.Sprintf("session idle %s — check %s", formatDuration(idle), ph.TmuxTarget)
			}
		} else if len(windows) > 0 {
			// tmux window with no session — orphan window
			ph.Health = HealthOrphan
			ph.Suggestion = fmt.Sprintf("tmux window exists but no session — `tmux kill-window -t %s:%s`", windows[0].SessionName, windows[0].WindowName)
			if windows[0].SessionName != "" && windows[0].WindowName != "" {
				ph.TmuxTarget = windows[0].SessionName + ":" + windows[0].WindowName
			}
		}

		if len(procs) > 0 {
			ph.OrchestPID = procs[0].PID
		}

		out = append(out, ph)
	}
	return out
}

// OrphanTmux returns tmux windows that don't correspond to any session or
// pipeline run — likely leftovers from killed orchestrate processes.
func OrphanTmux(snap Snapshot) []TmuxWindow {
	pipelinesByID := map[string]bool{}
	for _, p := range snap.Pipelines {
		pipelinesByID[p.DesignID] = true
	}
	sessionsByID := map[string]bool{}
	for _, s := range snap.Sessions {
		if s.DesignID != "" {
			sessionsByID[s.DesignID] = true
		}
		if s.TaskID != "" {
			sessionsByID[s.TaskID] = true
		}
	}

	var orphans []TmuxWindow
	for _, w := range snap.Tmux {
		if w.TargetID == "" {
			continue
		}
		if pipelinesByID[w.TargetID] || sessionsByID[w.TargetID] {
			continue
		}
		orphans = append(orphans, w)
	}
	return orphans
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
