package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

// GateVerdictResult holds the outcome of recording a gate verdict.
type GateVerdictResult struct {
	DesignID      string `json:"design_id"`
	GateName      string `json:"gate_name"`
	Phase         string `json:"phase"`
	Round         int    `json:"round"`
	Verdict       string `json:"verdict"`
	ReviewShardID string `json:"review_shard_id"`
	NextPhase     string `json:"next_phase,omitempty"`
}

// RecordGateVerdict orchestrates a gate verdict using the connector (work items)
// and store (pipeline state). This replaces the monolithic cbClient.PipelineGatePass.
func RecordGateVerdict(
	ctx context.Context,
	cn connector.Connector,
	st store.Store,
	designID, gateName, verdict, body string,
	readiness int,
	pCfg *config.Config,
) (*GateVerdictResult, error) {

	// 1. Get pipeline run from store
	run, err := st.GetRun(ctx, designID)
	if err != nil {
		return nil, fmt.Errorf("get pipeline run: %w", err)
	}
	currentPhase := run.CurrentPhase

	// 2. Compute round
	round, err := st.GetLatestGateRound(ctx, run.ID, gateName)
	if err != nil {
		round = 0
	}
	round++

	// 3. Create review work item via connector
	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339)
	verdictUpper := "FAIL"
	if verdict == "pass" {
		verdictUpper = "PASS"
	}

	content := fmt.Sprintf("# Gate: %s — Round %d\n\n**Design:** %s\n**Timestamp:** %s\n**Verdict:** %s\n",
		gateName, round, designID, timestamp, verdictUpper)
	if readiness > 0 {
		content += fmt.Sprintf("**Readiness Score:** %d/5\n", readiness)
	}
	content += fmt.Sprintf("\n## Findings\n\n%s", body)

	title := fmt.Sprintf("Gate: %s — Round %d — %s", gateName, round, verdictUpper)
	reviewID := ""
	if cn != nil {
		id, err := cn.Create(ctx, connector.CreateRequest{
			Title:   title,
			Content: content,
			Type:    "review",
			Labels:  []string{gateName},
		})
		if err != nil {
			return nil, fmt.Errorf("create review work item: %w", err)
		}
		reviewID = id

		// Link review to design
		if err := cn.CreateEdge(ctx, reviewID, designID, "child-of"); err != nil {
			fmt.Printf("Warning: failed to link review to design: %v\n", err)
		}
	}

	// 4. Record gate in store
	var bodyPtr *string
	if body != "" {
		bodyPtr = &body
	}
	var readinessPtr *int
	if readiness > 0 {
		readinessPtr = &readiness
	}
	var reviewPtr *string
	if reviewID != "" {
		reviewPtr = &reviewID
	}

	_, err = st.RecordGate(ctx, store.PipelineGateInput{
		PipelineID:     run.ID,
		DesignID:       designID,
		GateName:       gateName,
		Phase:          currentPhase,
		Verdict:        verdict,
		ReadinessScore: readinessPtr,
		Body:           bodyPtr,
		ReviewShardID:  reviewPtr,
	})
	if err != nil {
		return nil, fmt.Errorf("record gate: %w", err)
	}

	// 5. Advance phase on pass.
	//
	// We used to call pCfg.NextPhase(currentPhase) here, but that method
	// falls back to ValidPipelinePhases — a FLAT list that interleaves
	// design-workflow and bug-workflow phases as
	// [design, decompose, investigate, fix, implement, review, deploy, done].
	// NextPhase walked that list sequentially, so NextPhase("decompose")
	// returned "investigate" instead of "implement" for design shards.
	// This silently advanced cp-c2ec47 to the wrong phase on a perfectly
	// valid decomposition-review PASS — the hardcoded fallback switch
	// below had the right answer but never ran because NextPhase returned
	// non-empty.
	//
	// Fix: resolve the next phase from the WORKFLOW that applies to this
	// shard type (design / bug / bug-complex / task), not from a flat
	// phase list. NextPhaseInWorkflow walks the workflow's ordered Phases
	// array correctly. We still keep the gate-name hardcoded switch as a
	// last-resort fallback for configs that have no workflow definition.
	nextPhase := ""
	resultPhase := currentPhase
	if verdict == "pass" {
		if pCfg != nil && cn != nil {
			// Fetch the shard type and map it to a workflow name
			// (inferWorkflowFromType handles the bug vs bug-complex
			// escalation based on the needs-investigation label).
			if item, err := cn.Get(ctx, designID); err == nil && item != nil {
				workflow := inferWorkflowFromType(item)
				nextPhase = pCfg.NextPhaseInWorkflow(workflow, currentPhase)
			}
		}
		if nextPhase == "" {
			// Last-resort hardcoded fallback when the workflow lookup
			// couldn't resolve a next phase (no connector, no config, or
			// a gate in an unrecognized phase).
			switch gateName {
			case "readiness-review":
				nextPhase = "decompose"
			case "decomposition-review":
				nextPhase = "implement"
			case "investigation":
				nextPhase = "implement"
			case "review":
				nextPhase = "done"
			case "retrospective":
				nextPhase = "" // done is terminal
			}
		}
		if nextPhase != "" {
			if err := st.UpdateRunPhase(ctx, designID, nextPhase); err != nil {
				return nil, fmt.Errorf("advance phase: %w", err)
			}
			resultPhase = nextPhase
		}
	}

	return &GateVerdictResult{
		DesignID:      designID,
		GateName:      gateName,
		Phase:         resultPhase,
		Round:         round,
		Verdict:       verdict,
		ReviewShardID: reviewID,
		NextPhase:     nextPhase,
	}, nil
}
