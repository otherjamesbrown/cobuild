package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/domain"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

// GateVerdictResult holds the outcome of recording a gate verdict.
type GateVerdictResult struct {
	DesignID      string `json:"design_id"`
	PipelineID    string `json:"pipeline_id"`
	GateName      string `json:"gate_name"`
	Phase         string `json:"phase"`
	Round         int    `json:"round"`
	Verdict       string `json:"verdict"`
	ReviewShardID string `json:"review_shard_id"`
	FindingsHash  string `json:"findings_hash,omitempty"`
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

	// Validate the gate matches the current phase. Without this, a stale
	// gate command (e.g. runner script's auto-record running after the
	// operator already recorded manually) could cascade through phases:
	// recording decomposition-review while phase=implement would advance
	// implement→review. (cb-1660be hit this and short-circuited to done.)
	if expected := expectedPhaseForGate(gateName); expected != "" && currentPhase != expected {
		return nil, fmt.Errorf(
			"gate %q expects phase %q but pipeline is in %q (likely a stale or duplicate gate call — verify the agent's verdict was already recorded)",
			gateName, expected, currentPhase,
		)
	}

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

	// cb-f55aa0: compute findings hash for fail verdicts so review
	// escalation can detect repeated identical findings.
	var hashPtr *string
	findingsHash := ""
	if verdict != "pass" && body != "" {
		findingsHash = computeFindingsHash(body)
		hashPtr = &findingsHash
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
		FindingsHash:   hashPtr,
	})
	if err != nil {
		return nil, fmt.Errorf("record gate: %w", err)
	}

	// 5. Advance phase on pass using the centralized transition helper.
	// This uses AdvancePhase with optimistic locking — if the pipeline
	// phase has already moved (concurrent gate, poller, etc.) the call
	// fails with ErrPhaseConflict instead of silently overwriting.
	nextPhase := ""
	resultPhase := currentPhase
	if verdict == "pass" {
		advanced, err := advancePipelinePhase(ctx, st, cn, pCfg, designID, currentPhase)
		if err != nil {
			return nil, fmt.Errorf("advance phase: %w", err)
		}
		nextPhase = advanced
		resultPhase = advanced
	}

	return &GateVerdictResult{
		DesignID:      designID,
		PipelineID:    run.ID,
		GateName:      gateName,
		Phase:         resultPhase,
		Round:         round,
		Verdict:       verdict,
		ReviewShardID: reviewID,
		FindingsHash:  findingsHash,
		NextPhase:     nextPhase,
	}, nil
}

// ValidateDecompositionTaskRepos ensures every child task of a design resolves
// to exactly one repo before a passing decomposition gate is recorded.
func ValidateDecompositionTaskRepos(ctx context.Context, cn connector.Connector, designID, fallbackProject string) error {
	if cn == nil {
		return fmt.Errorf("no connector configured")
	}

	if fallbackProject == "" {
		design, err := cn.Get(ctx, designID)
		if err == nil && design != nil {
			fallbackProject = design.Project
		}
	}

	edges, err := cn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return fmt.Errorf("list child tasks for %s: %w", designID, err)
	}

	var problems []string
	for _, edge := range edges {
		if edge.Type != "" && edge.Type != "task" {
			continue
		}

		task, err := cn.Get(ctx, edge.ItemID)
		if err != nil {
			return fmt.Errorf("load child task %s: %w", edge.ItemID, err)
		}
		// Skip closed tasks — they won't be dispatched, so they don't need
		// repo metadata. Also skip non-task children (review shards, etc).
		if task == nil || task.Type != "task" || task.Status == "closed" {
			continue
		}

		if _, err := resolveTaskTargetRepo(ctx, cn, task, fallbackProject); err != nil {
			problems = append(problems, fmt.Sprintf("%s (%s)", task.ID, err))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("decomposition pass blocked: child tasks must resolve to exactly one repo: %s", strings.Join(problems, ", "))
	}
	return nil
}

func resolveTaskTargetRepo(ctx context.Context, cn connector.Connector, task *connector.WorkItem, fallbackProject string) (string, error) {
	if task == nil {
		return "", fmt.Errorf("missing task")
	}

	if repoValue, ok := metadataValue(task, domain.MetaRepo); ok {
		repos, err := normalizeRepoTargets(repoValue)
		if err != nil {
			return "", err
		}
		if len(repos) != 1 {
			return "", fmt.Errorf("%s", withTryHint("repo metadata is ambiguous", repoMetadataHint(task.ID, nil)))
		}
		if _, err := config.RepoForProject(repos[0]); err != nil {
			return "", fmt.Errorf("%s", withTryHint(fmt.Sprintf("repo metadata references unknown repo %q", repos[0]), repoMetadataHint(task.ID, nil)))
		}
		return repos[0], nil
	}

	if cn != nil {
		repo, err := cn.GetMetadata(ctx, task.ID, domain.MetaRepo)
		if err != nil {
			return "", fmt.Errorf("read repo metadata: %w", err)
		}
		repo = strings.TrimSpace(repo)
		if repo != "" {
			repos, err := normalizeRepoTargets(repo)
			if err != nil {
				return "", err
			}
			if len(repos) != 1 {
				return "", fmt.Errorf("%s", withTryHint("repo metadata is ambiguous", repoMetadataHint(task.ID, nil)))
			}
			if _, err := config.RepoForProject(repos[0]); err != nil {
				return "", fmt.Errorf("%s", withTryHint(fmt.Sprintf("repo metadata references unknown repo %q", repos[0]), repoMetadataHint(task.ID, nil)))
			}
			return repos[0], nil
		}
	}

	project := strings.TrimSpace(task.Project)
	if project == "" {
		project = strings.TrimSpace(fallbackProject)
	}
	if project == "" {
		return "", fmt.Errorf("%s", withTryHint("missing repo metadata and no project fallback is available", repoMetadataHint(task.ID, nil)))
	}

	reg, err := config.LoadRepoRegistry()
	if err != nil {
		return "", fmt.Errorf("resolve repos for project %q: %w", project, err)
	}
	repos := reposForProject(reg, project)
	switch len(repos) {
	case 0:
		return "", fmt.Errorf("%s", withTryHint(fmt.Sprintf("missing repo metadata and project %q has no registered repos", project), repoMetadataHint(task.ID, nil)))
	case 1:
		return repos[0], nil
	default:
		return "", fmt.Errorf("%s", withTryHint(
			fmt.Sprintf("missing repo metadata and project %q maps to multiple repos (%s)", project, strings.Join(repos, ", ")),
			repoMetadataHint(task.ID, repos),
		))
	}
}

func metadataValue(item *connector.WorkItem, key string) (any, bool) {
	if item == nil || item.Metadata == nil {
		return nil, false
	}
	value, ok := item.Metadata[key]
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}

func normalizeRepoTargets(raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		return normalizeRepoString(v)
	case []string:
		return cleanRepoList(v), nil
	case []any:
		values := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(fmt.Sprintf("%v", item))
			if s != "" {
				values = append(values, s)
			}
		}
		return cleanRepoList(values), nil
	default:
		s := strings.TrimSpace(fmt.Sprintf("%v", raw))
		if s == "" {
			return nil, nil
		}
		return []string{s}, nil
	}
}

func normalizeRepoString(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}

	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		var parsed []string
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			return cleanRepoList(parsed), nil
		}
		var parsedAny []any
		if err := json.Unmarshal([]byte(value), &parsedAny); err == nil {
			return normalizeRepoTargets(parsedAny)
		}
	}

	var values []string
	if strings.Contains(value, ",") {
		values = strings.Split(value, ",")
	} else {
		values = strings.Fields(value)
	}
	return cleanRepoList(values), nil
}

func cleanRepoList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

// expectedPhaseForGate returns the pipeline phase a given gate is recorded
// from. Used to reject stale or duplicate gate calls that would cascade
// the pipeline through phases incorrectly (cb-1660be observed implement→
// review→done from a duplicate decomposition-review).
//
// Empty string means "no validation" — used for retrospective which
// runs at done and other gates without a strict phase mapping.
func expectedPhaseForGate(gateName string) string {
	switch gateName {
	case domain.GateReadinessReview:
		return domain.PhaseDesign
	case domain.GateDecompositionReview:
		return domain.PhaseDecompose
	case "investigation":
		return domain.PhaseInvestigate
	case domain.GateReview:
		// "review" gate covers tasks completing review phase. Tasks have
		// their own pipeline runs in implement/fix/review phases.
		// Skip strict validation here — the gate fires from process-review
		// for tasks, and the design's review phase sits separately.
		return ""
	case domain.GateRetrospective:
		return domain.PhaseDone
	default:
		return ""
	}
}
