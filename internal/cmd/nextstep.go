package cmd

import (
	"fmt"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/domain"
)

// printNextStep outputs guidance for what to do after a command completes.
// Called at the end of every state-changing command.
func printNextStep(workItemID, phase, action string) {
	fmt.Println()
	fmt.Println("Next step:")

	switch action {
	case domain.ActionInit:
		fmt.Printf("  cobuild dispatch %s\n", workItemID)
		fmt.Printf("  (Spawns an agent for the %s phase)\n", phase)

	case domain.ActionDispatch:
		fmt.Printf("  cobuild wait %s\n", workItemID)
		fmt.Println("  (Blocks until the dispatched CoBuild agent completes)")
		fmt.Printf("  OR: cobuild audit %s  (instant check, non-blocking)\n", workItemID)

	case domain.ActionDispatchWave:
		fmt.Printf("  cobuild audit %s\n", workItemID)
		if currentWaveStrategy() == "serial" {
			fmt.Println("  (Check progress of the current serial wave — wave N+1 waits until wave N closes, avoiding the unsafe dispatch-everything-then-rebase-later path)")
		} else {
			fmt.Println("  (Check progress of the dispatched wave — all currently ready tasks run in parallel)")
		}
		fmt.Printf("  When all tasks reach needs-review: run `cobuild process-review <task-id>` for each")

	case domain.ActionWaitComplete:
		switch phase {
		case domain.PhaseInvestigate:
			fmt.Printf("  Check for fix task: cobuild wi links %s\n", workItemID)
			fmt.Println("  Then: cobuild dispatch <fix-task-id>")
		case domain.PhaseDesign:
			fmt.Printf("  cobuild dispatch %s\n", workItemID)
			fmt.Println("  (Decomposition phase — break design into tasks)")
		case domain.PhaseDecompose:
			fmt.Printf("  cobuild dispatch-wave %s\n", workItemID)
			fmt.Println("  (Dispatch ready tasks for implementation)")
		case domain.PhaseImplement:
			fmt.Printf("  cobuild merge %s  OR  cobuild merge-design %s\n", workItemID, workItemID)
			fmt.Println("  (Review and merge the PR)")
		case domain.PhaseReview:
			fmt.Printf("  cobuild deploy %s\n", workItemID)
			fmt.Println("  (Deploy affected services)")
		case domain.PhaseDone:
			fmt.Printf("  cobuild retro %s\n", workItemID)
			fmt.Println("  (Run retrospective)")
		default:
			fmt.Printf("  cobuild status\n")
			fmt.Println("  (Check pipeline progress)")
		}

	case domain.ActionComplete:
		fmt.Println("  Wait for the orchestrating agent to review and merge your PR.")
		fmt.Println("  Do NOT merge it yourself.")

	case domain.ActionMerge:
		fmt.Printf("  cobuild deploy %s  (if deploy is configured)\n", workItemID)
		fmt.Printf("  cobuild status     (check if more tasks need merging)\n")

	case domain.ActionMergeDesign:
		fmt.Printf("  cobuild deploy %s\n", workItemID)
		fmt.Println("  (Deploy affected services)")

	case domain.ActionDeploy:
		fmt.Printf("  cobuild retro %s\n", workItemID)
		fmt.Println("  (Run retrospective to capture lessons learned)")

	case domain.ActionRetro:
		fmt.Println("  Pipeline complete. Run cobuild status to see other active pipelines.")

	case domain.ActionGatePass:
		fmt.Printf("  cobuild dispatch %s\n", workItemID)
		fmt.Printf("  (Agent for the next phase: %s)\n", phase)

	case domain.ActionGateFail:
		fmt.Printf("  Fix the issues, then re-run the gate:\n")
		fmt.Printf("  cobuild dispatch %s\n", workItemID)

	case domain.ActionRun:
		fmt.Println("  cobuild poller")
		fmt.Println("  (Start the poller — it will process this item through all phases)")

	case domain.ActionProcessReview:
		// phase param carries the outcome: "merged", "redispatched", or "waiting"
		switch phase {
		case domain.OutcomeMerged:
			fmt.Printf("  cobuild next %s\n", workItemID)
			fmt.Println("  (PR merged and task closed. Check next action for this task or its parent design.)")
		case domain.OutcomeRedispatched:
			fmt.Printf("  cobuild wait %s  OR  cobuild audit %s\n", workItemID, workItemID)
			fmt.Println("  (Agent re-dispatched to address review feedback. Wait for completion, then re-run process-review.)")
		case domain.OutcomeWaiting:
			fmt.Printf("  cobuild process-review %s\n", workItemID)
			fmt.Println("  (Review data not ready yet — retry after a few minutes.)")
		default:
			fmt.Printf("  cobuild next %s\n", workItemID)
		}

	default:
		fmt.Printf("  cobuild status\n")
	}

	// Always show how to check progress
	if action != "status" && action != domain.ActionRetro {
		fmt.Printf("\n  Check progress: cobuild status\n")
	}
}

// nextPhaseDescription returns a human-readable description of a phase.
func nextPhaseDescription(phase string) string {
	switch strings.ToLower(phase) {
	case domain.PhaseDesign:
		return "readiness review"
	case domain.PhaseDecompose:
		return "task decomposition"
	case domain.PhaseInvestigate:
		return "bug investigation"
	case domain.PhaseImplement:
		return "implementation"
	case domain.PhaseReview:
		return "PR review"
	case domain.PhaseDone:
		return "retrospective"
	default:
		return phase
	}
}
