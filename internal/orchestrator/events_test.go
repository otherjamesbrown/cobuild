package orchestrator

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"":        LevelInfo, // default when unknown
		"debug":   LevelDebug,
		"DEBUG":   LevelDebug,
		" info ":  LevelInfo,
		"warn":    LevelWarn,
		"warning": LevelWarn,
		"error":   LevelError,
		"banana":  LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLevelForMapsKindsCorrectly(t *testing.T) {
	cases := map[EventKind]Level{
		EventDispatch:   LevelInfo,
		EventPoll:       LevelDebug,
		EventReview:     LevelInfo,
		EventTransition: LevelInfo,
		EventTerminal:   LevelInfo,
	}
	for kind, want := range cases {
		if got := LevelFor(kind); got != want {
			t.Errorf("LevelFor(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestLevelFilteredHandlerDropsBelowMin(t *testing.T) {
	var buf bytes.Buffer
	handler := LevelFilteredHandler(&buf, LevelInfo)

	now := time.Now()
	handler(Event{Time: now, Kind: EventPoll, Message: "polling"})        // DEBUG — dropped
	handler(Event{Time: now, Kind: EventDispatch, Message: "dispatched"}) // INFO — kept
	handler(Event{Time: now, Kind: EventTerminal, Message: "complete"})   // INFO — kept

	out := buf.String()
	if strings.Contains(out, "polling") {
		t.Errorf("LevelInfo handler kept DEBUG event: %q", out)
	}
	if !strings.Contains(out, "dispatched") || !strings.Contains(out, "complete") {
		t.Errorf("LevelInfo handler dropped INFO events: %q", out)
	}
}

func TestLevelFilteredHandlerKeepsEverythingAtDebug(t *testing.T) {
	var buf bytes.Buffer
	handler := LevelFilteredHandler(&buf, LevelDebug)

	now := time.Now()
	handler(Event{Time: now, Kind: EventPoll, Message: "polling"})
	handler(Event{Time: now, Kind: EventDispatch, Message: "dispatched"})

	out := buf.String()
	if !strings.Contains(out, "polling") || !strings.Contains(out, "dispatched") {
		t.Errorf("LevelDebug handler should keep both: %q", out)
	}
}
