package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/orchestrator"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

func TestOrchestrateCommandRunsForegroundPipelineWithStructuredLogs(t *testing.T) {
	storeStub := newCompletionFakeStore()
	storeStub.runs["cb-orch"] = &store.PipelineRun{
		ID:           "run-cb-orch",
		DesignID:     "cb-orch",
		CurrentPhase: "design",
		Status:       "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	restore := installCommandTestGlobals(t, nil, storeStub, "cobuild")
	defer restore()

	resetFactory := stubOrchestrateRunnerFactory(t, func(opts orchestrator.Options) orchestrateCommandRunner {
		return stubOrchestrateRunner(func(_ context.Context, shardID string) error {
			fmt.Fprintln(opts.Output, "[18:45:01] Phase: design -> dispatching")
			fmt.Fprintln(opts.Output, "[18:45:33] Phase: design -> done")
			fmt.Fprintln(opts.Output, "[18:45:34] Pipeline complete.")
			return nil
		})
	})
	defer resetFactory()

	setCommandFlag(t, orchestrateCmd, "timeout", "45m")
	setCommandFlag(t, orchestrateCmd, "poll-interval", "5s")
	setCommandFlag(t, orchestrateCmd, "step", "true")
	setCommandFlag(t, orchestrateCmd, "interactive", "false")

	out, err := runCommandWithOutputs(t, orchestrateCmd, []string{"cb-orch"})
	if err != nil {
		t.Fatalf("orchestrate failed: %v", err)
	}

	for _, want := range []string{
		"[18:45:01] Phase: design -> dispatching",
		"[18:45:33] Phase: design -> done",
		"[18:45:34] Pipeline complete.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}

	if captured := lastOrchestrateOptions; captured == nil {
		t.Fatal("expected orchestrate options to be captured")
	} else {
		if captured.PhaseTimeout != 45*time.Minute {
			t.Fatalf("timeout = %s, want 45m", captured.PhaseTimeout)
		}
		if captured.PollInterval != 5*time.Second {
			t.Fatalf("poll interval = %s, want 5s", captured.PollInterval)
		}
		if !captured.StepMode {
			t.Fatal("expected step mode to be enabled")
		}
		if captured.BeforeStep == nil {
			t.Fatal("expected step prompt hook to be installed")
		}
	}
}

func TestOrchestrateCommandDeployStopMapsToExitCodeTwo(t *testing.T) {
	storeStub := newCompletionFakeStore()
	restore := installCommandTestGlobals(t, nil, storeStub, "cobuild")
	defer restore()

	resetFactory := stubOrchestrateRunnerFactory(t, func(opts orchestrator.Options) orchestrateCommandRunner {
		return stubOrchestrateRunner(func(_ context.Context, _ string) error {
			fmt.Fprintln(opts.Output, "[19:14:21] Deploy requires human approval.")
			return &orchestrator.DeployRequiredError{ShardID: "cb-deploy", Phase: "deploy"}
		})
	})
	defer resetFactory()

	err := orchestrateCmd.RunE(orchestrateCmd, []string{"cb-deploy"})
	if err == nil {
		t.Fatal("expected deploy stop error")
	}
	if code := commandExitCode(err); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if shouldPrintCommandError(err) {
		t.Fatal("deploy stop should not print a duplicate error line")
	}
	if !errors.Is(err, orchestrator.ErrDeployApprovalRequired) {
		t.Fatalf("error = %v, want deploy approval sentinel", err)
	}
}

func TestCommandExitCodeDefaultsToOneForRealFailures(t *testing.T) {
	if code := commandExitCode(errors.New("boom")); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestRunInlineHandsOffToForegroundOrchestrate(t *testing.T) {
	storeStub := newCompletionFakeStore()
	storeStub.runs["cb-inline"] = &store.PipelineRun{
		ID:           "run-cb-inline",
		DesignID:     "cb-inline",
		CurrentPhase: "design",
		Status:       "active",
		Mode:         "autonomous",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	restore := installCommandTestGlobals(t, nil, storeStub, "cobuild")
	defer restore()

	var calledWith string
	resetFactory := stubOrchestrateRunnerFactory(t, func(opts orchestrator.Options) orchestrateCommandRunner {
		return stubOrchestrateRunner(func(_ context.Context, shardID string) error {
			calledWith = shardID
			fmt.Fprintln(opts.Output, "[18:45:01] Phase: design -> dispatching")
			return nil
		})
	})
	defer resetFactory()

	setCommandFlag(t, runCmd, "inline", "true")

	out, err := runCommandWithOutputs(t, runCmd, []string{"cb-inline"})
	if err != nil {
		t.Fatalf("run --inline failed: %v", err)
	}
	if calledWith != "cb-inline" {
		t.Fatalf("foreground runner called with %q, want cb-inline", calledWith)
	}
	if storeStub.runs["cb-inline"].Mode != "manual" {
		t.Fatalf("run mode = %q, want manual", storeStub.runs["cb-inline"].Mode)
	}
	for _, want := range []string{
		"Pipeline cb-inline switched to manual mode",
		"Running cb-inline in the foreground via `cobuild orchestrate cb-inline`.",
		"[18:45:01] Phase: design -> dispatching",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

type stubOrchestrateRunner func(ctx context.Context, shardID string) error

func (f stubOrchestrateRunner) Run(ctx context.Context, shardID string) error {
	return f(ctx, shardID)
}

var lastOrchestrateOptions *orchestrator.Options

func stubOrchestrateRunnerFactory(t *testing.T, fn func(opts orchestrator.Options) orchestrateCommandRunner) func() {
	t.Helper()
	prev := newOrchestrateRunner
	lastOrchestrateOptions = nil
	newOrchestrateRunner = func(opts orchestrator.Options) orchestrateCommandRunner {
		copied := opts
		lastOrchestrateOptions = &copied
		return fn(opts)
	}
	return func() {
		newOrchestrateRunner = prev
		lastOrchestrateOptions = nil
	}
}

func setCommandFlag(t *testing.T, command *cobra.Command, name, value string) {
	t.Helper()
	flags := command.Flags()
	flag := flags.Lookup(name)
	if flag == nil {
		t.Fatalf("missing flag %q", name)
	}
	original := flag.Value.String()
	if err := flags.Set(name, value); err != nil {
		t.Fatalf("set flag %s=%s: %v", name, value, err)
	}
	t.Cleanup(func() {
		_ = flags.Set(name, original)
		flag.Changed = false
	})
}
