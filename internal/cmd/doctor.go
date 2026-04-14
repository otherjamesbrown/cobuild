package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/cliutil"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor [shard-id]",
	Short: "Check pipeline health and optionally reconcile stale state",
	Long: `Without an argument, scans every pipeline in the project.

With a shard-id argument, produces a deep per-design diagnostic: orchestrate
processes, tmux windows, running sessions, PR state, review-auth config,
gate history, and task status. Use this first when a pipeline is stuck
(cb-d5e1dd #7).`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		if cbStore == nil {
			return fmt.Errorf("no store configured (need database connection)")
		}

		pipelineID, _ := cmd.Flags().GetString("pipeline")
		fix, _ := cmd.Flags().GetBool("fix")

		// Positional arg wins over --pipeline flag.
		if len(args) == 1 && pipelineID == "" {
			pipelineID = args[0]
		}

		// Deep per-design diagnostic: only when a specific shard is named.
		// It runs BEFORE the reconcile pass so operators see the problem
		// state before any auto-fix, and is read-only itself.
		if pipelineID != "" {
			runDoctorDiagnose(ctx, cmd.OutOrStdout(), pipelineID)
			fmt.Fprintln(cmd.OutOrStdout())
		}

		report, err := runDoctor(ctx, doctorOptions{
			PipelineID: pipelineID,
			Fix:        fix,
		})
		if err != nil {
			return err
		}

		if outputFormat == "json" {
			s, err := cliutil.FormatJSON(report)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), s)
		} else {
			writeDoctorTable(cmd.OutOrStdout(), report)
		}

		if report.IssueCount > 0 && !fix {
			return commandErrorWithExitCodeAndPrint(errors.New("pipeline health issues found"), 1, false)
		}
		return nil
	},
}

type doctorOptions struct {
	PipelineID string
	Fix        bool
}

type doctorReport struct {
	Pipelines  []doctorPipelineReport `json:"pipelines"`
	IssueCount int                    `json:"issue_count"`
	FixedCount int                    `json:"fixed_count,omitempty"`
}

type doctorPipelineReport struct {
	DesignID        string            `json:"design_id"`
	Project         string            `json:"project,omitempty"`
	Health          string            `json:"health"`
	Inconsistencies []string          `json:"inconsistencies"`
	Recommended     string            `json:"recommended"`
	Changes         []doctorChange    `json:"changes,omitempty"`
	SourceErrors    []doctorSourceErr `json:"source_errors,omitempty"`
}

type doctorChange struct {
	Action  string `json:"action"`
	Reason  string `json:"reason"`
	Changed bool   `json:"changed"`
}

type doctorSourceErr struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

func runDoctor(ctx context.Context, opts doctorOptions) (*doctorReport, error) {
	designIDs, err := doctorDesignIDs(ctx, opts.PipelineID)
	if err != nil {
		return nil, err
	}

	report := &doctorReport{
		Pipelines: make([]doctorPipelineReport, 0, len(designIDs)),
	}

	for _, designID := range designIDs {
		resolved, err := pipelinestate.Resolve(ctx, designID)
		if err != nil && !errors.Is(err, pipelinestate.ErrNotFound) {
			return nil, fmt.Errorf("resolve %s: %w", designID, err)
		}
		if resolved == nil {
			continue
		}

		entry, entryErr := doctorEntryFromState(ctx, resolved, opts.Fix)
		if entryErr != nil {
			return nil, entryErr
		}
		if entry.Health != string(pipelinestate.HealthOK) {
			report.IssueCount++
		}
		for _, change := range entry.Changes {
			if change.Changed {
				report.FixedCount++
			}
		}
		report.Pipelines = append(report.Pipelines, entry)
	}

	sort.Slice(report.Pipelines, func(i, j int) bool {
		return report.Pipelines[i].DesignID < report.Pipelines[j].DesignID
	})

	return report, nil
}

func doctorDesignIDs(ctx context.Context, pipelineID string) ([]string, error) {
	if pipelineID != "" {
		if _, err := cbStore.GetRun(ctx, pipelineID); err != nil {
			return nil, fmt.Errorf("get pipeline %s: %w", pipelineID, err)
		}
		return []string{pipelineID}, nil
	}

	runs, err := cbStore.ListRuns(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list pipeline runs: %w", err)
	}

	seen := map[string]struct{}{}
	designIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		if run.DesignID == "" {
			continue
		}
		if _, exists := seen[run.DesignID]; exists {
			continue
		}
		seen[run.DesignID] = struct{}{}
		designIDs = append(designIDs, run.DesignID)
	}
	return designIDs, nil
}

func doctorEntryFromState(ctx context.Context, resolved *pipelinestate.PipelineState, fix bool) (doctorPipelineReport, error) {
	entry := doctorPipelineReport{
		DesignID:        resolved.DesignID,
		Project:         resolved.Project,
		Health:          string(resolved.Health),
		Inconsistencies: append([]string(nil), resolved.Inconsistencies...),
		Recommended:     doctorRecommendation(resolved),
	}
	if len(entry.Inconsistencies) == 0 {
		entry.Inconsistencies = []string{}
	}
	for _, sourceErr := range resolved.SourceErrors {
		entry.SourceErrors = append(entry.SourceErrors, doctorSourceErr{
			Source:  sourceErr.Source,
			Message: sourceErr.Message,
		})
	}

	if !fix {
		return entry, nil
	}

	for _, recommendation := range pipelinestate.RecommendRecoveries(resolved) {
		change, err := applyDoctorRecommendation(ctx, recommendation, resolved)
		if err != nil {
			return entry, err
		}
		entry.Changes = append(entry.Changes, change)
	}
	return entry, nil
}

func doctorRecommendation(resolved *pipelinestate.PipelineState) string {
	actions := make([]string, 0)
	seen := map[string]struct{}{}
	for _, recommendation := range pipelinestate.RecommendRecoveries(resolved) {
		action := doctorRecoveryAction(recommendation.Kind)
		if action == "" {
			continue
		}
		if _, exists := seen[action]; exists {
			continue
		}
		seen[action] = struct{}{}
		actions = append(actions, action)
	}
	if len(actions) > 0 {
		return strings.Join(actions, "; ")
	}

	switch resolved.Health {
	case pipelinestate.HealthOK:
		return "-"
	case pipelinestate.HealthStale:
		return "redispatch pipeline"
	case pipelinestate.HealthMissing:
		return "inspect pipeline sources"
	default:
		return "inspect manually"
	}
}

func doctorRecoveryAction(kind pipelinestate.RecoveryKind) string {
	switch kind {
	case pipelinestate.RecoveryCancelOrphanedSession:
		return "cancel session"
	case pipelinestate.RecoveryKillOrphanTmuxWindow:
		return "kill tmux window"
	case pipelinestate.RecoveryCompleteStaleRun:
		return "complete run"
	default:
		return ""
	}
}

func applyDoctorRecommendation(ctx context.Context, recommendation pipelinestate.RecoveryRecommendation, resolved *pipelinestate.PipelineState) (doctorChange, error) {
	var (
		result pipelinestate.RecoveryResult
		err    error
	)

	deps := pipelinestate.RecoveryDependencies{Store: cbStore}
	switch recommendation.Kind {
	case pipelinestate.RecoveryCancelOrphanedSession:
		if recommendation.Session == nil {
			return doctorChange{}, fmt.Errorf("cancel session recovery missing session state for %s", resolved.DesignID)
		}
		result, err = pipelinestate.CancelOrphanedSession(ctx, deps, *recommendation.Session)
	case pipelinestate.RecoveryKillOrphanTmuxWindow:
		if recommendation.Window == nil {
			return doctorChange{}, fmt.Errorf("kill tmux window recovery missing window state for %s", resolved.DesignID)
		}
		result, err = pipelinestate.KillOrphanTmuxWindow(ctx, deps, *recommendation.Window)
	case pipelinestate.RecoveryCompleteStaleRun:
		result, err = pipelinestate.CompleteStaleRun(ctx, deps, resolved)
	default:
		return doctorChange{}, fmt.Errorf("unknown recovery kind %q", recommendation.Kind)
	}
	if err != nil {
		return doctorChange{}, fmt.Errorf("apply %s for %s: %w", recommendation.Kind, resolved.DesignID, err)
	}

	return doctorChange{
		Action:  doctorRecoveryAction(result.Kind),
		Reason:  result.Reason,
		Changed: result.Changed,
	}, nil
}

func writeDoctorTable(w io.Writer, report *doctorReport) {
	if report == nil {
		return
	}
	if len(report.Pipelines) == 0 {
		fmt.Fprintln(w, "All pipelines healthy.")
		return
	}

	fmt.Fprintf(w, "%-12s %-14s %-12s %-40s %s\n", "DESIGN", "PROJECT", "HEALTH", "INCONSISTENCIES", "RECOMMENDED")
	fmt.Fprintf(w, "%-12s %-14s %-12s %-40s %s\n", "------", "-------", "------", "---------------", "-----------")
	for _, pipeline := range report.Pipelines {
		fmt.Fprintf(w, "%-12s %-14s %-12s %-40s %s\n",
			pipeline.DesignID,
			doctorDisplay(pipeline.Project),
			pipeline.Health,
			doctorDisplay(strings.Join(pipeline.Inconsistencies, "; ")),
			doctorDisplay(pipeline.Recommended),
		)
	}

	if report.FixedCount > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Applied %d reconciliation change(s).\n", report.FixedCount)
		for _, pipeline := range report.Pipelines {
			for _, change := range pipeline.Changes {
				if !change.Changed {
					continue
				}
				fmt.Fprintf(w, "  %s: %s (%s)\n", pipeline.DesignID, change.Action, change.Reason)
			}
		}
	}
	if report.IssueCount == 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "All pipelines healthy.")
	}
}

func doctorDisplay(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func init() {
	doctorCmd.Flags().Bool("fix", false, "Apply reconciliation actions for unhealthy pipelines")
	doctorCmd.Flags().String("pipeline", "", "Scope the health check to a single pipeline")
	rootCmd.AddCommand(doctorCmd)
}
