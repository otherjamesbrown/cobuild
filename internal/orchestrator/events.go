package orchestrator

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Event describes a single observable orchestration event.
type Event struct {
	Time      time.Time
	ShardID   string
	Phase     string
	Kind      EventKind
	Message   string
	NextPhase string
}

// EventKind identifies the category of an emitted event.
type EventKind string

const (
	EventDispatch   EventKind = "dispatch"
	EventPoll       EventKind = "poll"
	EventReview     EventKind = "review"
	EventTransition EventKind = "transition"
	EventTerminal   EventKind = "terminal"
)

// Level is the filtered-output level assigned to each event kind.
// Operators use --log-level (default INFO) to drop routine DEBUG output
// from orchestrate and keep focus on state transitions (cb-fe3546).
type Level int

const (
	LevelDebug Level = 10
	LevelInfo  Level = 20
	LevelWarn  Level = 30
	LevelError Level = 40
)

// ParseLevel maps a string name to a Level. Unknown values default to
// LevelInfo so a typo in --log-level doesn't silently suppress output.
func ParseLevel(name string) Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// LevelFor maps an EventKind to its default level. Routine polling is
// demoted to DEBUG so the default INFO-level output scales to many
// parallel pipelines without being dominated by "still waiting" noise.
func LevelFor(kind EventKind) Level {
	switch kind {
	case EventPoll:
		return LevelDebug
	case EventDispatch, EventReview, EventTransition, EventTerminal:
		return LevelInfo
	default:
		return LevelInfo
	}
}

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	}
	return "INFO"
}

// EventHandler consumes orchestration events.
type EventHandler func(Event)

// FormatEvent renders the standard foreground log line for an event.
// The level prefix lets operators distinguish state transitions from
// polling noise at a glance.
func FormatEvent(event Event) string {
	return fmt.Sprintf("[%s] %s %s", event.Time.Format("15:04:05"), LevelFor(event.Kind), event.Message)
}

func writerEventHandler(w io.Writer) EventHandler {
	return LevelFilteredHandler(w, LevelInfo)
}

// LevelFilteredHandler writes events whose level is >= min. Events below
// min are dropped. A nil writer returns a nil handler (no-op). Exported
// so CLI callers can wire a user-selected --log-level into Options.OnEvent.
func LevelFilteredHandler(w io.Writer, min Level) EventHandler {
	if w == nil {
		return nil
	}
	return func(event Event) {
		if LevelFor(event.Kind) < min {
			return
		}
		fmt.Fprintln(w, FormatEvent(event))
	}
}
