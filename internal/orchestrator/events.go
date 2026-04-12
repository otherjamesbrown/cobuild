package orchestrator

import (
	"fmt"
	"io"
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
	EventTransition EventKind = "transition"
	EventTerminal   EventKind = "terminal"
)

// EventHandler consumes orchestration events.
type EventHandler func(Event)

// FormatEvent renders the standard foreground log line for an event.
func FormatEvent(event Event) string {
	return fmt.Sprintf("[%s] %s", event.Time.Format("15:04:05"), event.Message)
}

func writerEventHandler(w io.Writer) EventHandler {
	if w == nil {
		return nil
	}
	return func(event Event) {
		fmt.Fprintln(w, FormatEvent(event))
	}
}
