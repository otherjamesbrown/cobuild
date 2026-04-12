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
		if task == nil || task.Type != "task" {
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

	if repoValue, ok := metadataValue(task, "repo"); ok {
		repos, err := normalizeRepoTargets(repoValue)
		if err != nil {
			return "", err
		}
		if len(repos) != 1 {
			return "", fmt.Errorf("repo metadata is ambiguous")
		}
		if _, err := config.RepoForProject(repos[0]); err != nil {
			return "", fmt.Errorf("repo metadata references unknown repo %q", repos[0])
		}
		return repos[0], nil
	}

	if cn != nil {
		repo, err := cn.GetMetadata(ctx, task.ID, "repo")
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
				return "", fmt.Errorf("repo metadata is ambiguous")
			}
			if _, err := config.RepoForProject(repos[0]); err != nil {
				return "", fmt.Errorf("repo metadata references unknown repo %q", repos[0])
			}
			return repos[0], nil
		}
	}

	project := strings.TrimSpace(task.Project)
	if project == "" {
		project = strings.TrimSpace(fallbackProject)
	}
	if project == "" {
		return "", fmt.Errorf("missing repo metadata and no project fallback is available")
	}

	repos, err := reposForProject(project)
	if err != nil {
		return "", fmt.Errorf("resolve repos for project %q: %w", project, err)
	}
	switch len(repos) {
	case 0:
		return "", fmt.Errorf("missing repo metadata and project %q has no registered repos", project)
	case 1:
		return repos[0], nil
	default:
		return "", fmt.Errorf("missing repo metadata and project %q maps to multiple repos (%s)", project, strings.Join(repos, ", "))
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

func reposForProject(project string) ([]string, error) {
	reg, err := config.LoadRepoRegistry()
	if err != nil {
		return nil, err
	}

	var repos []string
	for name, entry := range reg.Repos {
		if readProjectConfigFromYAML(entry.Path).Project == project {
			repos = append(repos, name)
		}
	}
	slices.Sort(repos)
	return repos, nil
}
