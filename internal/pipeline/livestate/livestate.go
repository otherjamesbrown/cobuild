package livestate

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

const defaultWarnIdle = 10 * time.Minute

type Health string

const (
	HealthOK     Health = "OK"
	HealthWarn   Health = "WARN"
	HealthStale  Health = "STALE"
	HealthOrphan Health = "ORPHAN"
)

type Snapshot struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Processes   []ProcessInfo    `json:"processes"`
	Pipelines   []PipelineInfo   `json:"pipelines"`
	Tmux        []TmuxWindowInfo `json:"tmux"`
	Sessions    []SessionInfo    `json:"sessions"`
	Warnings    []SectionWarning `json:"warnings,omitempty"`
}

type SectionWarning struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

type BuildInput struct {
	Now        time.Time
	Config     *config.Config
	Runs       []store.PipelineRunStatus
	Sessions   []store.SessionRecord
	Processes  []ProcessInfo
	Tmux       []TmuxWindowInfo
	RunErr     error
	SessionErr error
	ProcessErr error
	TmuxErr    error
}

type ProcessInfo struct {
	PID         int       `json:"pid"`
	Kind        string    `json:"kind"`
	Project     string    `json:"project,omitempty"`
	DesignID    string    `json:"design_id,omitempty"`
	TaskID      string    `json:"task_id,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	TmuxTarget  string    `json:"tmux_target,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	Health      Health    `json:"health"`
	Suggestion  string    `json:"suggestion,omitempty"`
	Idle        string    `json:"idle,omitempty"`
	Description string    `json:"description,omitempty"`
}

type PipelineInfo struct {
	DesignID        string    `json:"design_id"`
	Project         string    `json:"project"`
	Phase           string    `json:"phase"`
	Status          string    `json:"status"`
	LastProgress    time.Time `json:"last_progress"`
	Health          Health    `json:"health"`
	TmuxTarget      string    `json:"tmux_target,omitempty"`
	SessionID       string    `json:"session_id,omitempty"`
	OrchestratePID  int       `json:"orchestrate_pid,omitempty"`
	Suggestion      string    `json:"suggestion,omitempty"`
	Idle            string    `json:"idle,omitempty"`
	TaskTotal       int       `json:"task_total,omitempty"`
	TaskDone        int       `json:"task_done,omitempty"`
	TaskBlocked     int       `json:"task_blocked,omitempty"`
	SourceWarnings  []string  `json:"source_warnings,omitempty"`
	MatchedTaskID   string    `json:"matched_task_id,omitempty"`
	MatchedTmuxName string    `json:"matched_tmux_name,omitempty"`
}

type SessionInfo struct {
	ID             string    `json:"id"`
	PipelineID     string    `json:"pipeline_id,omitempty"`
	DesignID       string    `json:"design_id"`
	TaskID         string    `json:"task_id"`
	Project        string    `json:"project"`
	Phase          string    `json:"phase"`
	Status         string    `json:"status"`
	StartedAt      time.Time `json:"started_at"`
	Health         Health    `json:"health"`
	TmuxTarget     string    `json:"tmux_target,omitempty"`
	OrchestratePID int       `json:"orchestrate_pid,omitempty"`
	Suggestion     string    `json:"suggestion,omitempty"`
	Idle           string    `json:"idle,omitempty"`
	SourceWarnings []string  `json:"source_warnings,omitempty"`
}

type TmuxWindowInfo struct {
	SessionName    string    `json:"session_name"`
	WindowName     string    `json:"window_name"`
	Target         string    `json:"target"`
	Project        string    `json:"project,omitempty"`
	DesignID       string    `json:"design_id,omitempty"`
	TaskID         string    `json:"task_id,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	Health         Health    `json:"health"`
	SessionID      string    `json:"session_id,omitempty"`
	OrchestratePID int       `json:"orchestrate_pid,omitempty"`
	Suggestion     string    `json:"suggestion,omitempty"`
	Idle           string    `json:"idle,omitempty"`
	SourceWarnings []string  `json:"source_warnings,omitempty"`
}

func BuildSnapshot(input BuildInput) Snapshot {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}

	snapshot := Snapshot{
		GeneratedAt: now,
		Processes:   slices.Clone(input.Processes),
		Tmux:        slices.Clone(input.Tmux),
	}
	appendWarning := func(source string, err error) {
		if err == nil {
			return
		}
		snapshot.Warnings = append(snapshot.Warnings, SectionWarning{
			Source:  source,
			Message: err.Error(),
		})
	}
	appendWarning("runs", input.RunErr)
	appendWarning("sessions", input.SessionErr)
	appendWarning("processes", input.ProcessErr)
	appendWarning("tmux", input.TmuxErr)

	stallTimeout := resolveStallTimeout(input.Config)

	sessionByID := make(map[string]store.SessionRecord, len(input.Sessions))
	sessionsByDesign := make(map[string][]store.SessionRecord)
	sessionsByTask := make(map[string]store.SessionRecord)
	for _, session := range input.Sessions {
		sessionByID[session.ID] = session
		sessionsByTask[normalize(session.TaskID)] = session
		key := designProjectKey(session.DesignID, session.Project)
		sessionsByDesign[key] = append(sessionsByDesign[key], session)
	}
	for key := range sessionsByDesign {
		slices.SortFunc(sessionsByDesign[key], func(a, b store.SessionRecord) int {
			return b.StartedAt.Compare(a.StartedAt)
		})
	}

	tmuxByTarget := make(map[string]TmuxWindowInfo, len(input.Tmux))
	tmuxByTask := make(map[string]TmuxWindowInfo, len(input.Tmux))
	for _, window := range input.Tmux {
		if target := normalizedTarget(window.SessionName, window.WindowName); target != "" {
			tmuxByTarget[target] = window
		}
		if window.TaskID != "" {
			tmuxByTask[normalize(window.TaskID)] = window
		} else if window.WindowName != "" {
			tmuxByTask[normalize(window.WindowName)] = window
		}
	}

	processesByTask := make(map[string]ProcessInfo)
	processesByDesign := make(map[string]ProcessInfo)
	for _, process := range input.Processes {
		if process.TaskID != "" {
			processesByTask[normalize(process.TaskID)] = process
		}
		if process.DesignID != "" {
			processesByDesign[designProjectKey(process.DesignID, process.Project)] = process
		}
	}

	for _, run := range input.Runs {
		pipeline := PipelineInfo{
			DesignID:     run.DesignID,
			Project:      run.Project,
			Phase:        run.Phase,
			Status:       run.Status,
			LastProgress: run.LastProgress,
			TaskTotal:    run.TaskTotal,
			TaskDone:     run.TaskDone,
			TaskBlocked:  run.TaskBlocked,
		}
		idle := idleDuration(now, run.LastProgress)
		if idle > 0 {
			pipeline.Idle = formatDuration(idle)
		}

		var session store.SessionRecord
		var hasSession bool
		if matches := sessionsByDesign[designProjectKey(run.DesignID, run.Project)]; len(matches) > 0 {
			session = matches[0]
			hasSession = true
			pipeline.SessionID = session.ID
			pipeline.MatchedTaskID = session.TaskID
		}

		var tmuxWindow TmuxWindowInfo
		var hasTmux bool
		if hasSession {
			if window, ok := tmuxByTarget[normalizedTarget(deref(session.TmuxSession), deref(session.TmuxWindow))]; ok {
				tmuxWindow = window
				hasTmux = true
			} else if window, ok := tmuxByTask[normalize(session.TaskID)]; ok {
				tmuxWindow = window
				hasTmux = true
			}
		}
		if hasTmux {
			pipeline.TmuxTarget = tmuxWindow.Target
			pipeline.MatchedTmuxName = tmuxWindow.WindowName
		} else if hasSession {
			pipeline.TmuxTarget = sessionTarget(session)
		}

		var process ProcessInfo
		var hasProcess bool
		if hasSession {
			process, hasProcess = processesByTask[normalize(session.TaskID)]
		}
		if !hasProcess {
			process, hasProcess = processesByDesign[designProjectKey(run.DesignID, run.Project)]
		}
		if hasProcess {
			pipeline.OrchestratePID = process.PID
		}

		pipelineHealth := pipelineHealthInput{
			DesignID:            run.DesignID,
			Idle:                idle,
			StallTimeout:        stallTimeout,
			WarnAfter:           defaultWarnIdle,
			TmuxTarget:          pipeline.TmuxTarget,
			HasSession:          hasSession,
			HasTmux:             hasTmux,
			HasProcess:          hasProcess,
			TmuxMissingKnown:    hasSession && input.TmuxErr == nil,
			ProcessMissingKnown: hasSession && input.ProcessErr == nil,
			TmuxUnavailable:     input.TmuxErr != nil,
			ProcessUnavailable:  input.ProcessErr != nil,
		}
		pipeline.Health, pipeline.Suggestion, pipeline.SourceWarnings = resolvePipelineHealth(pipelineHealth)
		snapshot.Pipelines = append(snapshot.Pipelines, pipeline)
	}

	for _, record := range input.Sessions {
		info := SessionInfo{
			ID:         record.ID,
			PipelineID: record.PipelineID,
			DesignID:   record.DesignID,
			TaskID:     record.TaskID,
			Project:    record.Project,
			Phase:      record.Phase,
			Status:     record.Status,
			StartedAt:  record.StartedAt,
		}

		var tmuxWindow TmuxWindowInfo
		var hasTmux bool
		if window, ok := tmuxByTarget[normalizedTarget(deref(record.TmuxSession), deref(record.TmuxWindow))]; ok {
			tmuxWindow = window
			hasTmux = true
		} else if window, ok := tmuxByTask[normalize(record.TaskID)]; ok {
			tmuxWindow = window
			hasTmux = true
		}
		if hasTmux {
			info.TmuxTarget = tmuxWindow.Target
		} else {
			info.TmuxTarget = sessionTarget(record)
		}

		process, hasProcess := processesByTask[normalize(record.TaskID)]
		if !hasProcess {
			process, hasProcess = processesByDesign[designProjectKey(record.DesignID, record.Project)]
		}
		if hasProcess {
			info.OrchestratePID = process.PID
		}

		lastProgress := record.StartedAt
		for _, run := range input.Runs {
			if sameDesign(run.DesignID, record.DesignID) && sameProject(run.Project, record.Project) {
				lastProgress = latestTime(lastProgress, run.LastProgress)
				break
			}
		}
		idle := idleDuration(now, lastProgress)
		if idle > 0 {
			info.Idle = formatDuration(idle)
		}

		sessionHealth := sessionHealthInput{
			TaskID:             record.TaskID,
			Idle:               idle,
			WarnAfter:          defaultWarnIdle,
			StallTimeout:       stallTimeout,
			TmuxTarget:         info.TmuxTarget,
			HasTmux:            hasTmux,
			HasProcess:         hasProcess,
			TmuxUnavailable:    input.TmuxErr != nil,
			ProcessUnavailable: input.ProcessErr != nil,
		}
		info.Health, info.Suggestion, info.SourceWarnings = resolveSessionHealth(sessionHealth)
		snapshot.Sessions = append(snapshot.Sessions, info)
	}

	for i, window := range snapshot.Tmux {
		process, hasProcess := processesByTask[normalize(window.TaskID)]
		if !hasProcess && window.DesignID != "" {
			process, hasProcess = processesByDesign[designProjectKey(window.DesignID, window.Project)]
		}
		if hasProcess {
			snapshot.Tmux[i].OrchestratePID = process.PID
		}

		var session store.SessionRecord
		var hasSession bool
		if matched, ok := sessionByID[window.SessionID]; ok {
			session = matched
			hasSession = true
		} else if matched, ok := sessionsByTask[normalize(window.TaskID)]; ok {
			session = matched
			hasSession = true
			snapshot.Tmux[i].SessionID = matched.ID
		}

		lastProgress := window.StartedAt
		if hasSession {
			lastProgress = latestTime(lastProgress, session.StartedAt)
		}
		for _, run := range input.Runs {
			if sameDesign(run.DesignID, window.DesignID) && sameProject(run.Project, window.Project) {
				lastProgress = latestTime(lastProgress, run.LastProgress)
				break
			}
		}
		idle := idleDuration(now, lastProgress)
		if idle > 0 {
			snapshot.Tmux[i].Idle = formatDuration(idle)
		}

		tmuxHealth := tmuxHealthInput{
			ID:                 firstNonEmpty(window.TaskID, window.WindowName, window.Target),
			Idle:               idle,
			WarnAfter:          defaultWarnIdle,
			StallTimeout:       stallTimeout,
			TmuxTarget:         window.Target,
			HasSession:         hasSession,
			HasProcess:         hasProcess,
			SessionUnavailable: input.SessionErr != nil,
			ProcessUnavailable: input.ProcessErr != nil,
		}
		snapshot.Tmux[i].Health, snapshot.Tmux[i].Suggestion, snapshot.Tmux[i].SourceWarnings = resolveTmuxHealth(tmuxHealth)
	}

	for i, process := range snapshot.Processes {
		snapshot.Processes[i].Health = HealthOK
		idle := idleDuration(now, process.StartedAt)
		if idle > 0 {
			snapshot.Processes[i].Idle = formatDuration(idle)
		}
		if process.Kind != "orchestrate" {
			continue
		}
		if process.TaskID != "" {
			if _, ok := sessionsByTask[normalize(process.TaskID)]; !ok && input.SessionErr == nil {
				snapshot.Processes[i].Health = HealthOrphan
				snapshot.Processes[i].Suggestion = orphanSuggestion(process.TaskID, "orchestrate process has no running DB session", process.TmuxTarget)
			}
		}
	}

	return snapshot
}

func resolveStallTimeout(cfg *config.Config) time.Duration {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	timeout := strings.TrimSpace(cfg.Monitoring.StallTimeout)
	if timeout == "" {
		timeout = config.DefaultConfig().Monitoring.StallTimeout
	}
	d, err := time.ParseDuration(timeout)
	if err != nil || d <= 0 {
		return 30 * time.Minute
	}
	return d
}

func designProjectKey(designID, project string) string {
	return normalize(project) + "::" + normalize(designID)
}

func normalizedTarget(sessionName, windowName string) string {
	if sessionName == "" || windowName == "" {
		return ""
	}
	return normalize(sessionName) + ":" + normalize(windowName)
}

func sessionTarget(session store.SessionRecord) string {
	sessionName := deref(session.TmuxSession)
	if sessionName == "" {
		if session.Project != "" {
			sessionName = fmt.Sprintf("cobuild-%s", session.Project)
		} else {
			sessionName = "cobuild"
		}
	}
	windowName := deref(session.TmuxWindow)
	if windowName == "" {
		windowName = session.TaskID
	}
	if sessionName == "" || windowName == "" {
		return ""
	}
	return sessionName + ":" + windowName
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func deref(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return strings.TrimSpace(*ptr)
}

func firstNonEmpty(parts ...string) string {
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			return strings.TrimSpace(part)
		}
	}
	return ""
}

func sameDesign(a, b string) bool {
	return normalize(a) == normalize(b)
}

func sameProject(a, b string) bool {
	return normalize(a) == normalize(b)
}

func latestTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.After(a) {
		return b
	}
	return a
}
