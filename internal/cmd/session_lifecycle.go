package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

type sessionLifecycleEvent struct {
	TaskID    string
	Phase     string
	Status    string
	Timestamp time.Time
	Note      string
}

func lifecycleEventStatus(status string) bool {
	switch status {
	case "stale-killed", "orphaned":
		return true
	default:
		return false
	}
}

func sessionEventTimestamp(rec store.SessionRecord) time.Time {
	if rec.EndedAt != nil && !rec.EndedAt.IsZero() {
		return *rec.EndedAt
	}
	return rec.StartedAt
}

func sessionLifecycleEvents(sessions []store.SessionRecord) []sessionLifecycleEvent {
	events := make([]sessionLifecycleEvent, 0, len(sessions))
	for _, rec := range sessions {
		if !lifecycleEventStatus(rec.Status) {
			continue
		}
		note := ""
		if rec.CompletionNote != nil {
			note = strings.TrimSpace(*rec.CompletionNote)
		}
		if note == "" && rec.Error != nil {
			note = strings.TrimSpace(*rec.Error)
		}
		events = append(events, sessionLifecycleEvent{
			TaskID:    rec.TaskID,
			Phase:     rec.Phase,
			Status:    rec.Status,
			Timestamp: sessionEventTimestamp(rec),
			Note:      note,
		})
	}
	return events
}

func latestSessionByTask(sessions []store.SessionRecord) map[string]store.SessionRecord {
	latest := make(map[string]store.SessionRecord, len(sessions))
	for _, rec := range sessions {
		existing, ok := latest[rec.TaskID]
		if !ok || sessionEventTimestamp(rec).After(sessionEventTimestamp(existing)) {
			latest[rec.TaskID] = rec
		}
	}
	return latest
}

func sessionPtr(rec store.SessionRecord) *store.SessionRecord {
	return &rec
}

func redispatchableSession(rec *store.SessionRecord) bool {
	if rec == nil {
		return false
	}
	return lifecycleEventStatus(rec.Status)
}

func redispatchReason(rec *store.SessionRecord) string {
	if rec == nil {
		return ""
	}
	if rec.CompletionNote != nil && strings.TrimSpace(*rec.CompletionNote) != "" {
		return strings.TrimSpace(*rec.CompletionNote)
	}
	if rec.Error != nil && strings.TrimSpace(*rec.Error) != "" {
		return strings.TrimSpace(*rec.Error)
	}
	return rec.Status
}

func shouldMarkTaskForRedispatch(taskStatus string, rec *store.SessionRecord) bool {
	return taskStatus == domain.StatusInProgress && redispatchableSession(rec)
}

func markTaskPendingForRedispatch(ctx context.Context, taskID string, rec *store.SessionRecord) error {
	if conn != nil {
		if err := conn.UpdateStatus(ctx, taskID, "open"); err != nil {
			return fmt.Errorf("set connector task open: %w", err)
		}
	}
	if cbStore != nil {
		if err := cbStore.UpdateTaskStatus(ctx, taskID, domain.StatusPending); err != nil {
			return fmt.Errorf("set pipeline task pending: %w", err)
		}
	}
	return nil
}
