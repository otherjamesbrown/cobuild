package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

// nextCmd is the "what do I run right now" escape hatch for orchestrator
// agents. Given any work-item id, it reads the pipeline state and prints a
// single concrete next command. Designed to be idiot-proof: if an agent is
// ever unsure what to do, running `cobuild next <id>` removes the ambiguity.
//
// This is the counterpart to printNextStep — printNextStep is called at the
// end of every state-changing command to tell you "what comes after *this*",
// whereas `cobuild next` answers "what should I run *now*" from cold-start,
// without having to remember which command you last ran.
var nextCmd = &cobra.Command{
	Use:   "next <work-item-id>",
	Short: "Print the single next concrete command for a pipeline",
	Long: `Tells you exactly what to run next for a given work item.

Reads pipeline_runs / pipeline_gates / pipeline_tasks and emits a compact
status + one concrete next command. Use this whenever you're unsure what
to do — it's the orchestrator's "you are here" signal.

Exit codes:
  0 — printed guidance
  1 — no pipeline run exists for this ID (or other lookup error)`,
	Args:    cobra.ExactArgs(1),
	Example: `  cobuild next cp-c2ec47`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		id := args[0]

		if cbStore == nil {
			return fmt.Errorf("no store configured — cobuild next requires a pipeline backend")
		}

		run, err := cbStore.GetRun(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				fmt.Fprintf(cmd.ErrOrStderr(), "No pipeline run for %s.\n", id)
				fmt.Fprintf(cmd.ErrOrStderr(), "\nNext step:\n  cobuild init %s\n", id)
				fmt.Fprintf(cmd.ErrOrStderr(), "  (Initialise a pipeline for this work item)\n")
				return fmt.Errorf("no pipeline run for %s", id)
			}
			return fmt.Errorf("lookup pipeline: %w", err)
		}

		// Fetch enrichment context — title from connector, gate and task
		// counts from the store. All optional; partial failure still returns
		// useful output.
		title := ""
		if conn != nil {
			if item, err := conn.Get(ctx, id); err == nil && item != nil {
				title = item.Title
			}
		}
		var gates []store.PipelineGateRecord
		if records, err := cbStore.GetGateHistory(ctx, id); err != nil {
			warnCommandReadError(cmd, "read gate history", err)
		} else {
			gates = records
		}
		var tasks []store.PipelineTaskRecord
		var sessions []store.SessionRecord
		if run != nil {
			if records, err := cbStore.ListTasks(ctx, run.ID); err != nil {
				warnCommandReadError(cmd, "read pipeline tasks", err)
			} else {
				tasks = records
			}
			if records, err := cbStore.ListSessions(ctx, id); err != nil {
				warnCommandReadError(cmd, "read session history", err)
			} else {
				sessions = records
			}
		}
		latestSessions := latestSessionByTask(sessions)

		// Compact header — "you are here"
		if title != "" {
			fmt.Printf("%s: %s\n", id, title)
		} else {
			fmt.Println(id)
		}
		fmt.Printf("  Phase:   %s\n", run.CurrentPhase)
		fmt.Printf("  Status:  %s\n", run.Status)
		if run.Mode != "" {
			fmt.Printf("  Mode:    %s\n", run.Mode)
		}
		if len(gates) > 0 {
			last := gates[len(gates)-1]
			fmt.Printf("  Latest:  %s round %d %s\n", last.GateName, last.Round, last.Verdict)
		}
		if len(tasks) > 0 {
			byStatus := map[string]int{}
			for _, t := range tasks {
				byStatus[t.Status]++
			}
			parts := ""
			for s, n := range byStatus {
				if parts != "" {
					parts += ", "
				}
				parts += fmt.Sprintf("%d %s", n, s)
			}
			fmt.Printf("  Tasks:   %d (%s)\n", len(tasks), parts)
		}

		// Terminal states — pipeline is done, there's no "next".
		if run.Status == "completed" || run.CurrentPhase == "done" {
			fmt.Println()
			fmt.Println("Pipeline complete. Nothing to do.")
			return nil
		}
		if run.Status == "blocked" {
			fmt.Println()
			fmt.Println("Pipeline is BLOCKED. Inspect with:")
			fmt.Printf("  cobuild audit %s\n", id)
			fmt.Printf("  cobuild wi show %s\n", id)
			return nil
		}

		// Emit the next concrete command for the CURRENT phase.
		// Semantics: "you're sitting in phase X, what do you run to advance
		// out of it?" — which is distinct from printNextStep's "wait-complete"
		// case (that one assumes the phase has just advanced into X and tells
		// you what to do at the start of X). For most phases the answer is
		// "dispatch" (every phase with a skill is dispatchable); implement is
		// the exception (wave dispatch + process-review lifecycle).
		fmt.Println()
		fmt.Println("Next step:")
		switch run.CurrentPhase {
		case "design", "decompose", "investigate", "fix":
			fmt.Printf("  cobuild dispatch %s\n", id)
			fmt.Printf("  (Spawns a dispatched CoBuild agent to run the %s skill)\n", run.CurrentPhase)
		case "implement":
			strategy := currentWaveStrategy()
			// pipeline_tasks is populated by `cobuild dispatch-wave` when
			// it starts a wave — NOT by `cobuild wi create` from the
			// decompose agent. So an empty pipeline_tasks slice in the
			// implement phase means "no wave has been dispatched yet",
			// not "all tasks closed" (which is what the default case
			// below would incorrectly claim).
			if len(tasks) == 0 {
				fmt.Printf("  cobuild dispatch-wave %s\n", id)
				if strategy == "serial" {
					fmt.Println("  (Implement phase entered — dispatch the first serial wave, not every future-ready task)")
				} else {
					fmt.Println("  (Implement phase entered — dispatch all currently ready tasks in parallel)")
				}
				break
			}
			// If there are pending tasks, dispatch the wave; if tasks are
			// in needs-review, process them; if all closed, advance to merge.
			pending := 0
			inReview := 0
			closed := 0
			redispatch := 0
			for _, t := range tasks {
				switch t.Status {
				case "pending":
					pending++
					if redispatchableSession(sessionPtr(latestSessions[t.TaskShardID])) {
						redispatch++
					}
				case "in_progress":
					if shouldMarkTaskForRedispatch(t.Status, sessionPtr(latestSessions[t.TaskShardID])) {
						pending++
						redispatch++
					} else {
						pending++
					}
				case "needs-review":
					inReview++
				case "completed", "closed":
					closed++
				}
			}
			switch {
			case pending > 0:
				fmt.Printf("  cobuild dispatch-wave %s\n", id)
				if strategy == "serial" {
					fmt.Printf("  (%d task(s) still pending — dispatch only the current serial wave after earlier tasks close, avoiding the unsafe dispatch-everything-then-rebase-later path)\n", pending)
				} else {
					fmt.Printf("  (%d task(s) still pending — dispatch all currently ready tasks in parallel)\n", pending)
				}
				if redispatch > 0 {
					fmt.Printf("  (%d task(s) are pending redispatch after a stale-killed/orphaned session)\n", redispatch)
				}
			case inReview > 0:
				fmt.Printf("  cobuild process-review <task-id>\n")
				fmt.Printf("  (%d task(s) awaiting review — run for each)\n", inReview)
				fmt.Printf("  Tasks in review: ")
				first := true
				for _, t := range tasks {
					if t.Status == "needs-review" {
						if !first {
							fmt.Printf(", ")
						}
						fmt.Printf("%s", t.TaskShardID)
						first = false
					}
				}
				fmt.Println()
			case closed == len(tasks):
				fmt.Printf("  cobuild merge-design %s\n", id)
				fmt.Println("  (All tasks closed — run smart merge to merge PRs in dependency order)")
			default:
				fmt.Printf("  cobuild audit %s\n", id)
				fmt.Printf("  (%d task(s) in unknown/mixed states — inspect the audit trail)\n", len(tasks))
			}
		case "review":
			fmt.Printf("  cobuild process-review %s\n", id)
			fmt.Println("  (Process the review for this task — merges or re-dispatches based on verdict)")
		case "deploy":
			fmt.Printf("  cobuild deploy %s --dry-run  (preview)\n", id)
			fmt.Printf("  cobuild deploy %s            (execute)\n", id)
		case "retrospective":
			fmt.Printf("  cobuild retro %s --body \"<findings>\"\n", id)
			fmt.Println("  (Record the retrospective and mark the pipeline complete)")
		default:
			fmt.Printf("  cobuild dispatch %s\n", id)
			fmt.Printf("  (Generic fallback — spawn a dispatched CoBuild agent for phase %q)\n", run.CurrentPhase)
		}

		fmt.Println()
		fmt.Printf("  Check progress: cobuild audit %s\n", id)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(nextCmd)
}
