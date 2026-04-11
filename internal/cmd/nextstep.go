package cmd

import (
	"fmt"
	"strings"
)

// printNextStep outputs guidance for what to do after a command completes.
// Called at the end of every state-changing command.
func printNextStep(workItemID, phase, action string) {
	fmt.Println()
	fmt.Println("Next step:")

	switch action {
	case "init":
		fmt.Printf("  cobuild dispatch %s\n", workItemID)
		fmt.Printf("  (Spawns an agent for the %s phase)\n", phase)

	case "dispatch":
		fmt.Printf("  cobuild wait %s\n", workItemID)
		fmt.Println("  (Blocks until the dispatched CoBuild agent completes)")
		fmt.Printf("  OR: cobuild audit %s  (instant check, non-blocking)\n", workItemID)

	case "dispatch-wave":
		fmt.Printf("  cobuild audit %s\n", workItemID)
		fmt.Println("  (Check progress of the dispatched wave — dispatched agents run in parallel)")
		fmt.Printf("  When all tasks reach needs-review: run `cobuild process-review <task-id>` for each")

	case "wait-complete":
		switch phase {
		case "investigate":
			fmt.Printf("  Check for fix task: cobuild wi links %s\n", workItemID)
			fmt.Println("  Then: cobuild dispatch <fix-task-id>")
		case "design":
			fmt.Printf("  cobuild dispatch %s\n", workItemID)
			fmt.Println("  (Decomposition phase — break design into tasks)")
		case "decompose":
			fmt.Printf("  cobuild dispatch-wave %s\n", workItemID)
			fmt.Println("  (Dispatch ready tasks for implementation)")
		case "implement":
			fmt.Printf("  cobuild merge %s  OR  cobuild merge-design %s\n", workItemID, workItemID)
			fmt.Println("  (Review and merge the PR)")
		case "review":
			fmt.Printf("  cobuild deploy %s\n", workItemID)
			fmt.Println("  (Deploy affected services)")
		case "done":
			fmt.Printf("  cobuild retro %s\n", workItemID)
			fmt.Println("  (Run retrospective)")
		default:
			fmt.Printf("  cobuild status\n")
			fmt.Println("  (Check pipeline progress)")
		}

	case "complete":
		fmt.Println("  Wait for the orchestrating agent to review and merge your PR.")
		fmt.Println("  Do NOT merge it yourself.")

	case "merge":
		fmt.Printf("  cobuild deploy %s  (if deploy is configured)\n", workItemID)
		fmt.Printf("  cobuild status     (check if more tasks need merging)\n")

	case "merge-design":
		fmt.Printf("  cobuild deploy %s\n", workItemID)
		fmt.Println("  (Deploy affected services)")

	case "deploy":
		fmt.Printf("  cobuild retro %s\n", workItemID)
		fmt.Println("  (Run retrospective to capture lessons learned)")

	case "retro":
		fmt.Println("  Pipeline complete. Run cobuild status to see other active pipelines.")

	case "gate-pass":
		fmt.Printf("  cobuild dispatch %s\n", workItemID)
		fmt.Printf("  (Agent for the next phase: %s)\n", phase)

	case "gate-fail":
		fmt.Printf("  Fix the issues, then re-run the gate:\n")
		fmt.Printf("  cobuild dispatch %s\n", workItemID)

	case "run":
		fmt.Println("  cobuild poller")
		fmt.Println("  (Start the poller — it will process this item through all phases)")

	case "process-review":
		// phase param carries the outcome: "merged", "redispatched", or "waiting"
		switch phase {
		case "merged":
			fmt.Printf("  cobuild next %s\n", workItemID)
			fmt.Println("  (PR merged and task closed. Check next action for this task or its parent design.)")
		case "redispatched":
			fmt.Printf("  cobuild wait %s  OR  cobuild audit %s\n", workItemID, workItemID)
			fmt.Println("  (Agent re-dispatched to address review feedback. Wait for completion, then re-run process-review.)")
		case "waiting":
			fmt.Printf("  cobuild process-review %s\n", workItemID)
			fmt.Println("  (Review data not ready yet — retry after a few minutes.)")
		default:
			fmt.Printf("  cobuild next %s\n", workItemID)
		}

	default:
		fmt.Printf("  cobuild status\n")
	}

	// Always show how to check progress
	if action != "status" && action != "retro" {
		fmt.Printf("\n  Check progress: cobuild status\n")
	}
}

// nextPhaseDescription returns a human-readable description of a phase.
func nextPhaseDescription(phase string) string {
	switch strings.ToLower(phase) {
	case "design":
		return "readiness review"
	case "decompose":
		return "task decomposition"
	case "investigate":
		return "bug investigation"
	case "implement":
		return "implementation"
	case "review":
		return "PR review"
	case "done":
		return "retrospective"
	default:
		return phase
	}
}
