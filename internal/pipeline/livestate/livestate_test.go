package livestate

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestCollectProcessesParsesOrchestrateAndPoller(t *testing.T) {
	now := time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)

	raw := `USER PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND
james 101 0.0 0.1 100 100 ?? S 10:30 0:00.01 /usr/local/bin/cobuild orchestrate cb-137bcf --project cobuild
james 202 0.0 0.1 100 100 ?? S Apr10 0:00.01 cobuild --project penfold poller --once
james 303 0.0 0.1 100 100 ?? S 11:55 0:00.01 cobuild dispatch cb-ignore
`

	got, err := ParseProcesses(raw, now)
	if err != nil {
		t.Fatalf("ParseProcesses() error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len(ParseProcesses()) = %d, want 2", len(got))
	}

	if got[0].PID != 101 || got[0].Kind != "orchestrate" {
		t.Fatalf("first row = %#v, want orchestrate pid 101", got[0])
	}
	if got[0].Project != "cobuild" {
		t.Fatalf("first project = %q, want cobuild", got[0].Project)
	}
	if got[0].TargetID != "cb-137bcf" {
		t.Fatalf("first target = %q, want cb-137bcf", got[0].TargetID)
	}
	if got[0].AgeSeconds != 90*60 {
		t.Fatalf("first age_seconds = %d, want %d", got[0].AgeSeconds, 90*60)
	}

	if got[1].PID != 202 || got[1].Kind != "poller" {
		t.Fatalf("second row = %#v, want poller pid 202", got[1])
	}
	if got[1].Project != "penfold" {
		t.Fatalf("second project = %q, want penfold", got[1].Project)
	}
	if got[1].TargetID != "" {
		t.Fatalf("second target = %q, want empty", got[1].TargetID)
	}
	if got[1].AgeSeconds != 60*60*60 {
		t.Fatalf("second age_seconds = %d, want %d", got[1].AgeSeconds, 60*60*60)
	}
}

func TestCollectTmuxParsesSessionsAndWindows(t *testing.T) {
	ctx := context.Background()
	var calls []string

	execFn := func(_ context.Context, name string, args ...string) ([]byte, error) {
		call := name
		for _, arg := range args {
			call += " " + arg
		}
		calls = append(calls, call)

		switch call {
		case "tmux list-sessions -F #{session_name}":
			return []byte("cobuild-cobuild\nmisc\ncobuild-penfold\n"), nil
		case "tmux list-windows -t cobuild-cobuild -F #{window_id}\t#{window_name}":
			return []byte("@1\tcb-137bcf\n@2\tlogs\n"), nil
		case "tmux list-windows -t cobuild-penfold -F #{window_id}\t#{window_name}":
			return []byte("@9\tcb-20aa9d.review\n"), nil
		default:
			return nil, errors.New("unexpected command: " + call)
		}
	}

	got, err := CollectTmux(ctx, execFn)
	if err != nil {
		t.Fatalf("CollectTmux() error = %v", err)
	}

	want := []TmuxWindow{
		{SessionName: "cobuild-cobuild", Project: "cobuild", WindowID: "@1", WindowName: "cb-137bcf", TargetID: "cb-137bcf"},
		{SessionName: "cobuild-cobuild", Project: "cobuild", WindowID: "@2", WindowName: "logs"},
		{SessionName: "cobuild-penfold", Project: "penfold", WindowID: "@9", WindowName: "cb-20aa9d.review", TargetID: "cb-20aa9d"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectTmux() = %#v, want %#v", got, want)
	}

	if len(calls) != 3 {
		t.Fatalf("command count = %d, want 3", len(calls))
	}
}

func TestCollectorAssemblesSnapshotAndCapturesPartialFailures(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 12, 15, 0, 0, 0, time.UTC)

	execFn := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "ps" {
			return []byte("USER PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND\njames 404 0.0 0.1 100 100 ?? S 14:45 0:00.01 cobuild orchestrate cb-5005a0 --project cobuild\n"), nil
		}
		if name == "tmux" {
			return nil, errors.New("no server running")
		}
		return nil, errors.New("unexpected command")
	}

	snapshot, err := (Collector{
		Exec: execFn,
		Store: &stubPipelineRunLister{
			runs: []store.PipelineRunStatus{
				{
					DesignID:     "cb-25b0a4",
					Project:      "cobuild",
					Phase:        "implement",
					Status:       "active",
					LastProgress: now.Add(-5 * time.Minute),
				},
			},
		},
		Now: func() time.Time { return now },
	}).Collect(ctx)
	if err == nil {
		t.Fatal("Collect() error = nil, want joined partial failure")
	}

	if !snapshot.GeneratedAt.Equal(now) {
		t.Fatalf("GeneratedAt = %v, want %v", snapshot.GeneratedAt, now)
	}
	if len(snapshot.Processes) != 1 {
		t.Fatalf("len(Processes) = %d, want 1", len(snapshot.Processes))
	}
	if snapshot.Processes[0].TargetID != "cb-5005a0" {
		t.Fatalf("target = %q, want cb-5005a0", snapshot.Processes[0].TargetID)
	}
	if len(snapshot.Tmux) != 0 {
		t.Fatalf("len(Tmux) = %d, want 0 on failure", len(snapshot.Tmux))
	}
	if len(snapshot.Pipelines) != 1 {
		t.Fatalf("len(Pipelines) = %d, want 1", len(snapshot.Pipelines))
	}
	if snapshot.Pipelines[0].DesignID != "cb-25b0a4" {
		t.Fatalf("pipeline design_id = %q, want cb-25b0a4", snapshot.Pipelines[0].DesignID)
	}
	if len(snapshot.Errors) != 1 {
		t.Fatalf("len(Errors) = %d, want 1", len(snapshot.Errors))
	}
	if snapshot.Errors[0].Source != "tmux" {
		t.Fatalf("error source = %q, want tmux", snapshot.Errors[0].Source)
	}
}

func TestCollectorCapturesPipelineFailuresWithoutDroppingOtherSources(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 12, 16, 0, 0, 0, time.UTC)

	execFn := func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "ps":
			return []byte("USER PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND\njames 505 0.0 0.1 100 100 ?? S 15:50 0:00.01 cobuild orchestrate cb-25b0a4 --project cobuild\n"), nil
		case "tmux":
			if len(args) > 0 && args[0] == "list-sessions" {
				return []byte("cobuild-cobuild\n"), nil
			}
			return []byte("@1\tcb-25b0a4\n"), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}

	snapshot, err := (Collector{
		Exec:  execFn,
		Store: &stubPipelineRunLister{err: errors.New("db offline")},
		Now:   func() time.Time { return now },
	}).Collect(ctx)
	if err == nil {
		t.Fatal("Collect() error = nil, want joined partial failure")
	}

	if len(snapshot.Processes) != 1 {
		t.Fatalf("len(Processes) = %d, want 1", len(snapshot.Processes))
	}
	if len(snapshot.Tmux) != 1 {
		t.Fatalf("len(Tmux) = %d, want 1", len(snapshot.Tmux))
	}
	if len(snapshot.Pipelines) != 0 {
		t.Fatalf("len(Pipelines) = %d, want 0 on failure", len(snapshot.Pipelines))
	}
	if len(snapshot.Errors) != 1 {
		t.Fatalf("len(Errors) = %d, want 1", len(snapshot.Errors))
	}
	if snapshot.Errors[0].Source != "pipelines" {
		t.Fatalf("error source = %q, want pipelines", snapshot.Errors[0].Source)
	}
}
