package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
)

func TestDashboardBuildActivePipelineRowsUsesResolverState(t *testing.T) {
	t.Parallel()

	now := time.Now()
	rows := &dashboardTestRows{
		values: [][]any{
			{"cb-1234567890abcdef", "cobuild", "implement", "active", "manual", 3, 1, now.Add(-2 * time.Hour)},
		},
	}

	got, err := buildDashboardActivePipelineRows(context.Background(), rows, func(context.Context, string) (*pipelinestate.PipelineState, error) {
		return &pipelinestate.PipelineState{
			DesignID: "cb-1234567890abcdef",
			Project:  "cobuild",
			Run: &pipelinestate.RunState{
				Phase:     "review",
				Mode:      "autonomous",
				UpdatedAt: now.Add(-90 * time.Minute),
			},
			Health: pipelinestate.HealthInconsistent,
			Inconsistencies: []string{
				"pipeline run is active but work item is closed",
			},
			SourceErrors: []pipelinestate.SourceError{
				{Source: "tmux", Message: "tmux unavailable"},
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("buildDashboardActivePipelineRows() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(got))
	}

	row := got[0]
	if row.Phase != "review" {
		t.Fatalf("Phase = %q, want review", row.Phase)
	}
	if row.Mode != "autonomous" {
		t.Fatalf("Mode = %q, want autonomous", row.Mode)
	}
	if row.Health != string(pipelinestate.HealthInconsistent) {
		t.Fatalf("Health = %q, want %q", row.Health, pipelinestate.HealthInconsistent)
	}
	if row.Tasks != "1/3" {
		t.Fatalf("Tasks = %q, want 1/3", row.Tasks)
	}
	for _, want := range []string{
		"pipeline run is active but work item is closed",
		"degraded source tmux: tmux unavailable",
	} {
		if !strings.Contains(row.Signals, want) {
			t.Fatalf("Signals = %q, want substring %q", row.Signals, want)
		}
	}
	if row.LastActivity == "-" || row.LastActivity == "2h ago" {
		t.Fatalf("LastActivity = %q, want resolver-backed timestamp", row.LastActivity)
	}
}

func TestDashboardBuildActivePipelineRowsHandlesMissingState(t *testing.T) {
	t.Parallel()

	rows := &dashboardTestRows{
		values: [][]any{
			{"cb-missing", "cobuild", "implement", "active", "manual", 0, 0, time.Now()},
		},
	}

	got, err := buildDashboardActivePipelineRows(context.Background(), rows, func(context.Context, string) (*pipelinestate.PipelineState, error) {
		return nil, pipelinestate.ErrNotFound
	})
	if err != nil {
		t.Fatalf("buildDashboardActivePipelineRows() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(got))
	}
	if got[0].Health != string(pipelinestate.HealthMissing) {
		t.Fatalf("Health = %q, want %q", got[0].Health, pipelinestate.HealthMissing)
	}
	if got[0].Signals != "pipeline state not found" {
		t.Fatalf("Signals = %q, want pipeline state not found", got[0].Signals)
	}
}

func TestDashboardRenderActivePipelinesShowsHealthAndSignals(t *testing.T) {
	output := captureDashboardStdout(t, func() {
		renderDashboardActivePipelines([]dashboardActivePipelineRow{
			{
				ID:           "cb-123",
				Project:      "cobuild",
				Phase:        "review",
				Mode:         "manual",
				Tasks:        "1/2",
				Health:       "ZOMBIE",
				Signals:      "session s-1 is running but no tmux window exists",
				LastActivity: "1h ago",
			},
		})
	})

	for _, want := range []string{
		"## Active Pipelines",
		"HEALTH",
		"SIGNALS",
		"ZOMBIE",
		"no tmux window exists",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

type dashboardTestRows struct {
	values [][]any
	index  int
	err    error
}

func (r *dashboardTestRows) Next() bool {
	return r.index < len(r.values)
}

func (r *dashboardTestRows) Scan(dest ...any) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	row := r.values[r.index]
	r.index++
	if len(dest) != len(row) {
		return fmt.Errorf("scan dest count = %d, want %d", len(dest), len(row))
	}
	for i := range dest {
		target := reflect.ValueOf(dest[i])
		if target.Kind() != reflect.Ptr || target.IsNil() {
			return fmt.Errorf("dest %d is not a pointer", i)
		}
		value := reflect.ValueOf(row[i])
		target.Elem().Set(value)
	}
	return nil
}

func (r *dashboardTestRows) Err() error {
	return r.err
}

func captureDashboardStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	return <-done
}

func TestDashboardSignalsFromStateOmitsEmptyEntries(t *testing.T) {
	t.Parallel()

	got := dashboardSignalsFromState(&pipelinestate.PipelineState{
		Inconsistencies: []string{"pipeline run exists but work item is missing"},
		SourceErrors: []pipelinestate.SourceError{
			{},
			{Source: "tmux"},
		},
	})

	for _, want := range []string{
		"pipeline run exists but work item is missing",
		"degraded source: tmux",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("dashboardSignalsFromState() = %q, want substring %q", got, want)
		}
	}
}
