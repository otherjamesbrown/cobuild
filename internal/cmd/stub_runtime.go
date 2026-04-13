package cmd

import (
	"context"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/runtime"
	stubruntime "github.com/otherjamesbrown/cobuild/internal/runtime/stub"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

var stubRuntimeCmd = &cobra.Command{
	Use:    "stub-runtime",
	Short:  "Internal helper for the stub dispatch runtime",
	Hidden: true,
}

var stubRuntimeExecCmd = &cobra.Command{
	Use:    "exec",
	Short:  "Execute a stub runtime fixture",
	Args:   cobra.NoArgs,
	Hidden: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := context.Background()
		phase, _ := cmd.Flags().GetString("phase")
		taskID, _ := cmd.Flags().GetString("task-id")
		worktreePath, _ := cmd.Flags().GetString("worktree")
		fixturesDir, _ := cmd.Flags().GetString("fixtures-dir")

		res, err := stubruntime.Execute(ctx, stubruntime.ExecInput{
			FixturesDir:  fixturesDir,
			WorktreePath: worktreePath,
			Phase:        phase,
			TaskID:       taskID,
			Stdout:       cmd.OutOrStdout(),
		})
		if err != nil {
			return err
		}
		if err := applyStubFixtureChanges(ctx, res.Fixture); err != nil {
			return err
		}

		if runtime.IsGatePhase(phase) {
			if err := applyStubGate(cmd, res.Fixture.GateVerdict); err != nil {
				return err
			}
			endTaskSession(ctx, taskID, worktreePath, store.SessionResult{
				ExitCode:       0,
				FilesChanged:   0,
				Commits:        0,
				Status:         "completed",
				CompletionNote: "stub gate phase completed",
			})
			return nil
		}
		return completeCmd.RunE(completeCmd, []string{taskID})
	},
}

func applyStubGate(cmd *cobra.Command, verdict *stubruntime.GateVerdictFixture) error {
	if verdict == nil {
		return fmt.Errorf("stub gate fixture missing verdict")
	}
	switch verdict.Gate {
	case "readiness-review":
		return runGateCommand(reviewCmd, verdict.ShardID, map[string]string{
			"verdict":   verdict.Verdict,
			"readiness": fmt.Sprintf("%d", verdict.Readiness),
			"body":      verdict.Body,
			"body-file": "",
		})
	case "decomposition-review":
		return runGateCommand(decomposeCmd, verdict.ShardID, map[string]string{
			"verdict":   verdict.Verdict,
			"body":      verdict.Body,
			"body-file": "",
		})
	case "investigation":
		return runGateCommand(investigateCmd, verdict.ShardID, map[string]string{
			"verdict":   verdict.Verdict,
			"body":      verdict.Body,
			"body-file": "",
		})
	case "retrospective":
		return runGateCommand(retroCmd, verdict.ShardID, map[string]string{
			"body": verdict.Body,
		})
	default:
		return fmt.Errorf("unsupported stub gate %q", verdict.Gate)
	}
}

func runGateCommand(command *cobra.Command, shardID string, flags map[string]string) error {
	for name, value := range flags {
		if err := command.Flags().Set(name, value); err != nil {
			return err
		}
	}
	defer func() {
		for name := range flags {
			_ = command.Flags().Set(name, "")
		}
	}()
	return command.RunE(command, []string{shardID})
}

func applyStubFixtureChanges(ctx context.Context, fixture stubruntime.Fixture) error {
	if conn == nil {
		return fmt.Errorf("no connector configured")
	}
	for _, item := range fixture.CreateItems {
		metadata := map[string]any{"_id": item.ID}
		for key, value := range item.Metadata {
			metadata[key] = value
		}
		if item.Project != "" {
			metadata["project"] = item.Project
		}
		id, err := conn.Create(ctx, connector.CreateRequest{
			Title:    item.Title,
			Content:  item.Content,
			Type:     item.Type,
			Labels:   item.Labels,
			Metadata: metadata,
			ParentID: item.ParentID,
		})
		if err != nil {
			return fmt.Errorf("create stub work item %s: %w", item.ID, err)
		}
		if item.Status != "" && item.Status != "open" {
			if err := conn.UpdateStatus(ctx, id, item.Status); err != nil {
				return fmt.Errorf("set status for %s: %w", id, err)
			}
		}
	}
	for _, edge := range fixture.CreateEdges {
		if err := conn.CreateEdge(ctx, edge.FromID, edge.ToID, edge.EdgeType); err != nil {
			return fmt.Errorf("create stub edge %s -> %s (%s): %w", edge.FromID, edge.ToID, edge.EdgeType, err)
		}
	}
	return nil
}

func init() {
	stubRuntimeExecCmd.Flags().String("phase", "", "Pipeline phase")
	stubRuntimeExecCmd.Flags().String("task-id", "", "Task or shard ID")
	stubRuntimeExecCmd.Flags().String("worktree", "", "Worktree path")
	stubRuntimeExecCmd.Flags().String("repo-root", "", "Repo root path")
	stubRuntimeExecCmd.Flags().String("fixtures-dir", "", "Stub fixture root")
	stubRuntimeCmd.AddCommand(stubRuntimeExecCmd)
	rootCmd.AddCommand(stubRuntimeCmd)
}
