package state

import (
	"fmt"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/domain"
)

// staleReviewWindow is how long a pipeline may sit in review with no
// activity before being flagged stale. The doctor doesn't auto-recover
// these — they need a human decision (reset, close, or let it finish).
// See cb-09c328.
const staleReviewWindow = 24 * time.Hour

func computeHealth(state *PipelineState) (Health, []string) {
	if state == nil {
		return HealthMissing, nil
	}
	if state.WorkItem == nil && state.Run == nil && len(state.Sessions) == 0 && len(state.Tmux) == 0 {
		return HealthMissing, nil
	}

	inconsistencies := newInconsistencySet()

	if state.Run != nil && state.WorkItem != nil && state.Run.Status == "active" && state.WorkItem.Status == "closed" {
		inconsistencies.addHard("pipeline run is active but work item is closed")
	}
	if state.Run != nil && state.WorkItem == nil {
		inconsistencies.addHard("pipeline run exists but work item is missing")
	}

	runningSessions := make([]SessionState, 0, len(state.Sessions))
	for _, session := range state.Sessions {
		if session.Status == "running" {
			runningSessions = append(runningSessions, session)
		}
		if state.Run != nil && state.Run.Status == domain.StatusCompleted && session.Status == "running" {
			inconsistencies.addHard("pipeline run is completed but a session is still running")
		}
	}

	matchedWindows := map[int]bool{}
	hasZombie := false
	tmuxAvailable := !hasSourceError(state, "tmux")

	if tmuxAvailable {
		for _, session := range runningSessions {
			matchIndex := findMatchingWindow(session, state.Tmux)
			if matchIndex < 0 {
				hasZombie = true
				inconsistencies.add(fmt.Sprintf("session %s is running but no tmux window exists", session.ID))
				continue
			}
			matchedWindows[matchIndex] = true
		}

		for index, window := range state.Tmux {
			if matchedWindows[index] {
				continue
			}
			hasZombie = true
			inconsistencies.add(fmt.Sprintf("tmux window %s:%s exists but no matching pipeline session exists", window.SessionName, window.WindowName))
		}
	}

	if inconsistencies.hasHardConflict() {
		return HealthInconsistent, inconsistencies.list()
	}
	if hasZombie {
		return HealthZombie, inconsistencies.list()
	}
	if isStale(state, runningSessions) {
		return HealthStale, inconsistencies.list()
	}
	return HealthOK, inconsistencies.list()
}

func findMatchingWindow(session SessionState, windows []TmuxWindow) int {
	for i, window := range windows {
		if session.TmuxSession != "" && session.TmuxWindow != "" &&
			window.SessionName == session.TmuxSession && window.WindowName == session.TmuxWindow {
			return i
		}
	}
	for i, window := range windows {
		if window.TargetID == session.DesignID {
			return i
		}
	}
	return -1
}

func isStale(state *PipelineState, runningSessions []SessionState) bool {
	if state.Run == nil || state.Run.Status != "active" {
		return false
	}
	if len(runningSessions) > 0 {
		return false
	}
	for _, session := range state.Sessions {
		switch session.Status {
		case "orphaned", domain.StatusCancelled, domain.StatusCompleted, domain.StatusFailed, "timeout":
			return true
		}
	}
	// Review-stuck (cb-09c328): a pipeline that's been in review for longer
	// than staleReviewWindow with nothing running is waiting on a human
	// decision (merge, fail, close). Flag it so live/status can filter these
	// out of the "actually active" view. No auto-fix — operator decides.
	if state.Run.Phase == domain.PhaseReview && !state.ResolvedAt.IsZero() &&
		state.ResolvedAt.Sub(state.Run.UpdatedAt) > staleReviewWindow {
		return true
	}
	return false
}

func hasSourceError(state *PipelineState, source string) bool {
	for _, sourceErr := range state.SourceErrors {
		if sourceErr.Source == source {
			return true
		}
	}
	return false
}

type inconsistencySet struct {
	items []string
	seen  map[string]struct{}
	hard  bool
}

func newInconsistencySet() *inconsistencySet {
	return &inconsistencySet{seen: map[string]struct{}{}}
}

func (s *inconsistencySet) add(message string) {
	if _, exists := s.seen[message]; exists {
		return
	}
	s.seen[message] = struct{}{}
	s.items = append(s.items, message)
}

func (s *inconsistencySet) addHard(message string) {
	s.hard = true
	s.add(message)
}

func (s *inconsistencySet) hasHardConflict() bool {
	return s.hard
}

func (s *inconsistencySet) list() []string {
	return append([]string(nil), s.items...)
}
