package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/config"
)

// Shard represents a shard record from the Context Palace DB.
type Shard struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Content  string          `json:"content"`
	Type     string          `json:"type"`
	Status   string          `json:"status"`
	Project  string          `json:"project"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// ShardSummary is a lightweight shard representation used by poller queries.
type ShardSummary struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	UpdatedAt time.Time       `json:"updated_at,omitempty"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

// EdgeInfo represents an edge relationship.
type EdgeInfo struct {
	ShardID string `json:"shard_id"`
	Title   string `json:"title"`
	Type    string `json:"type"`
	Status  string `json:"status"`
}

// ShardDetail includes labels.
type ShardDetail struct {
	Shard
	Labels []string `json:"labels"`
}

// PipelineWait represents a design whose waiting_for condition may be satisfied.
type PipelineWait struct {
	DesignID   string   `json:"design_id"`
	Title      string   `json:"title"`
	WaitingFor []string `json:"waiting_for"`
}

// ReviewHistoryEntry records a single review round.
type ReviewHistoryEntry struct {
	Round     int    `json:"round"`
	Verdict   string `json:"verdict"`
	ShardID   string `json:"shard_id"`
	Timestamp string `json:"timestamp"`
}

// ReviewState tracks cumulative review history on a pipeline.
type ReviewState struct {
	Round           int                  `json:"round"`
	LastVerdict     string               `json:"last_verdict"`
	LastReviewShard string               `json:"last_review_shard"`
	History         []ReviewHistoryEntry `json:"history"`
}

// DecomposeHistoryEntry records a single decomposition round.
type DecomposeHistoryEntry struct {
	Round     int    `json:"round"`
	Verdict   string `json:"verdict"`
	ShardID   string `json:"shard_id"`
	TaskCount int    `json:"task_count"`
	Timestamp string `json:"timestamp"`
}

// DecomposeState tracks cumulative decomposition history.
type DecomposeState struct {
	Round              int                     `json:"round"`
	LastVerdict        string                  `json:"last_verdict"`
	LastDecomposeShard string                  `json:"last_decompose_shard"`
	History            []DecomposeHistoryEntry `json:"history"`
}

// PipelineState represents the pipeline metadata stored on a design shard.
type PipelineState struct {
	Phase            string         `json:"phase"`
	LockedBy         *string        `json:"locked_by"`
	LockExpires      *time.Time     `json:"lock_expires"`
	WaitingFor       []string       `json:"waiting_for"`
	LastProgress     string         `json:"last_progress"`
	TaskShards       []string       `json:"task_shards"`
	CumulativeTokens int            `json:"cumulative_tokens"`
	IterationCounts  map[string]int `json:"iteration_counts"`
	Review           *ReviewState   `json:"review,omitempty"`
	Decompose        *DecomposeState `json:"decompose,omitempty"`
}

// DefaultPipelineState returns a new PipelineState with default values.
func DefaultPipelineState(startPhase ...string) PipelineState {
	phase := "design"
	if len(startPhase) > 0 && startPhase[0] != "" {
		phase = startPhase[0]
	}
	return PipelineState{
		Phase:            phase,
		WaitingFor:       []string{},
		LastProgress:     time.Now().UTC().Format(time.RFC3339),
		TaskShards:       []string{},
		CumulativeTokens: 0,
		IterationCounts:  map[string]int{},
	}
}

// PipelineGateResult holds the outcome of a generic pipeline gate operation.
type PipelineGateResult struct {
	DesignID      string `json:"design_id"`
	GateName      string `json:"gate_name"`
	Phase         string `json:"phase"`
	Round         int    `json:"round"`
	Verdict       string `json:"verdict"`
	ReviewShardID string `json:"review_shard_id"`
	NextPhase     string `json:"next_phase,omitempty"`
}

// PipelineAuditEntry represents a single entry in the pipeline audit trail.
type PipelineAuditEntry struct {
	Timestamp     time.Time `json:"timestamp"`
	GateName      string    `json:"gate_name"`
	Round         int       `json:"round"`
	Verdict       string    `json:"verdict"`
	ReviewShardID string    `json:"review_shard_id"`
	Body          string    `json:"body,omitempty"`
}

// LockTTL is the duration a pipeline lock is held before it becomes stale.
const LockTTL = 5 * time.Minute

// --- Shard CRUD helpers (thin wrappers over CP database) ---

// GetShard fetches a shard by ID.
func (c *Client) GetShard(ctx context.Context, id string) (*Shard, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	var s Shard
	err = conn.QueryRow(ctx, `
		SELECT id, title, content, type, status, project, metadata
		FROM shards WHERE id = $1
	`, id).Scan(&s.ID, &s.Title, &s.Content, &s.Type, &s.Status, &s.Project, &s.Metadata)
	if err != nil {
		return nil, fmt.Errorf("shard not found: %s", id)
	}
	return &s, nil
}

// GetShardDetail fetches a shard with its labels.
func (c *Client) GetShardDetail(ctx context.Context, id string) (*ShardDetail, error) {
	shard, err := c.GetShard(ctx, id)
	if err != nil {
		return nil, err
	}

	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `SELECT label FROM shard_labels WHERE shard_id = $1`, id)
	if err != nil {
		return &ShardDetail{Shard: *shard}, nil
	}
	defer rows.Close()

	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err == nil {
			labels = append(labels, label)
		}
	}

	return &ShardDetail{Shard: *shard, Labels: labels}, nil
}

// GetTask fetches a task shard by ID.
func (c *Client) GetTask(ctx context.Context, id string) (*Shard, error) {
	return c.GetShard(ctx, id)
}

// GetShardEdges returns edges for a shard.
func (c *Client) GetShardEdges(ctx context.Context, id, direction string, edgeTypes []string) ([]EdgeInfo, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	var query string
	if direction == "outgoing" {
		query = `
			SELECT s.id, s.title, s.type, s.status
			FROM edges e JOIN shards s ON s.id = e.to_id
			WHERE e.from_id = $1 AND e.edge_type = ANY($2)
		`
	} else {
		query = `
			SELECT s.id, s.title, s.type, s.status
			FROM edges e JOIN shards s ON s.id = e.from_id
			WHERE e.to_id = $1 AND e.edge_type = ANY($2)
		`
	}

	rows, err := conn.Query(ctx, query, id, edgeTypes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []EdgeInfo
	for rows.Next() {
		var e EdgeInfo
		if err := rows.Scan(&e.ShardID, &e.Title, &e.Type, &e.Status); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// CreateEdgeSimple creates a simple edge between two shards.
func (c *Client) CreateEdgeSimple(ctx context.Context, fromID, toID, edgeType string) error {
	conn, err := c.Connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `
		INSERT INTO edges (from_id, to_id, edge_type) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, fromID, toID, edgeType)
	return err
}

// CreateShardWithMetadata creates a new shard with metadata.
func (c *Client) CreateShardWithMetadata(ctx context.Context, title, content, shardType string, priority *int, labels []string, metadata json.RawMessage) (string, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)

	var id string
	err = conn.QueryRow(ctx, `
		INSERT INTO shards (title, content, type, priority, project, agent, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, title, content, shardType, priority, c.Config.Project, c.Config.Agent, metadata).Scan(&id)
	if err != nil {
		return "", err
	}

	for _, label := range labels {
		conn.Exec(ctx, `INSERT INTO shard_labels (shard_id, label) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, label)
	}

	return id, nil
}

// SetMetadataPath sets a value at a JSONB path in shard metadata.
func (c *Client) SetMetadataPath(ctx context.Context, id string, path []string, value json.RawMessage) (json.RawMessage, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	var result json.RawMessage
	err = conn.QueryRow(ctx, `
		UPDATE shards
		SET metadata = jsonb_set(COALESCE(metadata, '{}'), $2, $3, true),
		    updated_at = NOW()
		WHERE id = $1
		RETURNING metadata
	`, id, path, value).Scan(&result)
	if err != nil {
		return nil, fmt.Errorf("failed to set metadata path: %v", err)
	}
	return result, nil
}

// GetMetadataField reads a single text field from shard metadata.
func (c *Client) GetMetadataField(ctx context.Context, id string, path []string) (string, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)

	// Build path expression
	expr := "metadata"
	for _, p := range path {
		expr += fmt.Sprintf("->>'%s'", p)
	}
	// Actually we need the ->> only on the last element
	expr = "metadata"
	for i, p := range path {
		if i == len(path)-1 {
			expr += fmt.Sprintf("->>'%s'", p)
		} else {
			expr += fmt.Sprintf("->'%s'", p)
		}
	}

	var val *string
	err = conn.QueryRow(ctx, fmt.Sprintf(`SELECT %s FROM shards WHERE id = $1`, expr), id).Scan(&val)
	if err != nil || val == nil {
		return "", err
	}
	return *val, nil
}

// UpdateMetadata merges a JSON patch into shard metadata.
func (c *Client) UpdateMetadata(ctx context.Context, id string, patch json.RawMessage) (json.RawMessage, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	var result json.RawMessage
	err = conn.QueryRow(ctx, `
		UPDATE shards
		SET metadata = COALESCE(metadata, '{}') || $2,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING metadata
	`, id, patch).Scan(&result)
	if err != nil {
		return nil, fmt.Errorf("failed to update metadata: %v", err)
	}
	return result, nil
}

// AppendShardContent appends content to a shard.
func (c *Client) AppendShardContent(ctx context.Context, id, newContent string) error {
	conn, err := c.Connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `
		UPDATE shards SET content = content || $2, updated_at = NOW() WHERE id = $1
	`, id, newContent)
	return err
}

// --- Pipeline state methods ---

// PipelineInit initialises the pipeline metadata on a shard.
func (c *Client) PipelineInit(ctx context.Context, id string) (*PipelineState, error) {
	shard, err := c.GetShard(ctx, id)
	if err != nil {
		return nil, err
	}

	validTypes := map[string]bool{"design": true, "bug": true, "task": true}
	if !validTypes[shard.Type] {
		return nil, fmt.Errorf("shard %s is type %q -- pipeline supports design, bug, and task", id, shard.Type)
	}

	if shard.Metadata != nil && len(shard.Metadata) > 2 {
		var meta map[string]json.RawMessage
		if json.Unmarshal(shard.Metadata, &meta) == nil {
			if _, exists := meta["pipeline"]; exists {
				return nil, fmt.Errorf("shard %s already has pipeline metadata", id)
			}
		}
	}

	repoRoot, _ := config.RepoForProject(c.Config.Project)
	pCfg, _ := config.LoadConfig(repoRoot)
	startPhase := "design"
	if pCfg != nil {
		startPhase = pCfg.StartPhaseForType(shard.Type)
	}

	state := DefaultPipelineState(startPhase)
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pipeline state: %v", err)
	}

	_, err = c.SetMetadataPath(ctx, id, []string{"pipeline"}, stateJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to init pipeline: %v", err)
	}

	return &state, nil
}

// PipelineGet retrieves the pipeline state from a shard's metadata.
func (c *Client) PipelineGet(ctx context.Context, id string) (*PipelineState, error) {
	shard, err := c.GetShard(ctx, id)
	if err != nil {
		return nil, err
	}

	if shard.Metadata == nil || len(shard.Metadata) <= 2 {
		return nil, fmt.Errorf("shard %s has no pipeline metadata; run `cobuild init %s` first", id, id)
	}

	var meta map[string]json.RawMessage
	if err := json.Unmarshal(shard.Metadata, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %v", err)
	}

	raw, exists := meta["pipeline"]
	if !exists {
		return nil, fmt.Errorf("shard %s has no pipeline metadata; run `cobuild init %s` first", id, id)
	}

	var state PipelineState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("failed to parse pipeline metadata: %v", err)
	}

	return &state, nil
}

// PipelineUpdate applies incremental updates to the pipeline state.
func (c *Client) PipelineUpdate(ctx context.Context, id string, phase *string, waitingFor *json.RawMessage, addTask *string, addTokens *int) (*PipelineState, error) {
	state, err := c.PipelineGet(ctx, id)
	if err != nil {
		return nil, err
	}

	if phase != nil {
		if err := config.ValidatePipelinePhase(*phase); err != nil {
			return nil, err
		}
		state.Phase = *phase
	}

	if waitingFor != nil {
		var wf []string
		if err := json.Unmarshal(*waitingFor, &wf); err != nil {
			return nil, fmt.Errorf("--waiting-for must be a JSON array of strings: %v", err)
		}
		state.WaitingFor = wf
	}

	if addTask != nil {
		for _, existing := range state.TaskShards {
			if existing == *addTask {
				return nil, fmt.Errorf("task shard %s already in pipeline", *addTask)
			}
		}
		state.TaskShards = append(state.TaskShards, *addTask)
	}

	if addTokens != nil {
		state.CumulativeTokens += *addTokens
	}

	state.LastProgress = time.Now().UTC().Format(time.RFC3339)
	if state.IterationCounts == nil {
		state.IterationCounts = map[string]int{}
	}
	state.IterationCounts[state.Phase]++

	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pipeline state: %v", err)
	}

	_, err = c.SetMetadataPath(ctx, id, []string{"pipeline"}, stateJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to update pipeline: %v", err)
	}

	return state, nil
}

// PipelineLock acquires a lock on the pipeline.
func (c *Client) PipelineLock(ctx context.Context, id, sessionID string) (*PipelineState, error) {
	state, err := c.PipelineGet(ctx, id)
	if err != nil {
		return nil, err
	}

	if state.LockedBy != nil && state.LockExpires != nil && state.LockExpires.After(time.Now().UTC()) {
		return nil, fmt.Errorf("pipeline locked by %s", *state.LockedBy)
	}

	state.LockedBy = &sessionID
	expires := time.Now().UTC().Add(LockTTL)
	state.LockExpires = &expires

	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pipeline state: %v", err)
	}

	_, err = c.SetMetadataPath(ctx, id, []string{"pipeline"}, stateJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to lock pipeline: %v", err)
	}

	return state, nil
}

// PipelineUnlock releases the lock on the pipeline.
func (c *Client) PipelineUnlock(ctx context.Context, id string) (*PipelineState, error) {
	state, err := c.PipelineGet(ctx, id)
	if err != nil {
		return nil, err
	}

	state.LockedBy = nil
	state.LockExpires = nil

	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pipeline state: %v", err)
	}

	_, err = c.SetMetadataPath(ctx, id, []string{"pipeline"}, stateJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to unlock pipeline: %v", err)
	}

	return state, nil
}

// PipelineLockCheck returns the lock status and the current pipeline state.
func (c *Client) PipelineLockCheck(ctx context.Context, id string) (string, *PipelineState, error) {
	state, err := c.PipelineGet(ctx, id)
	if err != nil {
		return "", nil, err
	}

	if state.LockedBy == nil {
		return "unlocked", state, nil
	}

	if state.LockExpires != nil && state.LockExpires.After(time.Now().UTC()) {
		return "locked", state, nil
	}

	return "stale", state, nil
}

// isTableMissing returns true if the error indicates the table doesn't exist.
func isTableMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "does not exist") || strings.Contains(msg, "relation") || strings.Contains(msg, "table not found")
}

// PipelineGatePass is a generic gate method that records a verdict at any pipeline phase.
func (c *Client) PipelineGatePass(ctx context.Context, designID, gateName, verdict, body string, readiness int, cfg *config.Config) (*PipelineGateResult, error) {
	state, err := c.PipelineGet(ctx, designID)
	if err != nil {
		return nil, err
	}
	currentPhase := state.Phase

	var gateConfig *config.GateConfig
	if cfg != nil {
		gateConfig = cfg.FindGate(currentPhase, gateName)
	}

	// Check requires_label
	if verdict == "pass" && gateConfig != nil && gateConfig.RequiresLabel != "" {
		edges, err := c.GetShardEdges(ctx, designID, "incoming", []string{"child-of"})
		if err != nil {
			return nil, fmt.Errorf("failed to get child edges: %v", err)
		}
		hasLabel := false
		for _, e := range edges {
			if e.Type == "task" {
				detail, err := c.GetShardDetail(ctx, e.ShardID)
				if err != nil {
					continue
				}
				for _, label := range detail.Labels {
					if label == gateConfig.RequiresLabel {
						hasLabel = true
						break
					}
				}
				if hasLabel {
					break
				}
			}
		}
		if !hasLabel {
			return nil, fmt.Errorf("cannot pass gate %q: no child task with required label %q found", gateName, gateConfig.RequiresLabel)
		}
	}

	// Try DB pipeline_run
	var pipelineID string
	dbAvailable := true
	pipelineRun, err := c.GetPipelineRun(ctx, designID)
	if err != nil {
		if isTableMissing(err) {
			dbAvailable = false
		} else if strings.Contains(err.Error(), "no pipeline run found") {
			shard, shardErr := c.GetShard(ctx, designID)
			project := ""
			if shardErr == nil {
				project = shard.Project
			}
			pipelineRun, err = c.CreatePipelineRun(ctx, designID, project, currentPhase)
			if err != nil {
				if isTableMissing(err) {
					dbAvailable = false
				} else {
					return nil, fmt.Errorf("failed to create pipeline run: %v", err)
				}
			}
		} else {
			return nil, fmt.Errorf("failed to get pipeline run: %v", err)
		}
	}
	if pipelineRun != nil {
		pipelineID = pipelineRun.ID
	}

	// Compute round
	round := 1
	if dbAvailable && pipelineID != "" {
		latestRound, err := c.GetLatestGateRound(ctx, pipelineID, gateName)
		if err != nil {
			if isTableMissing(err) {
				dbAvailable = false
			}
		} else {
			round = latestRound + 1
		}
	} else {
		if state.Review != nil && gateName == "readiness-review" {
			round = state.Review.Round + 1
		} else if state.Decompose != nil && gateName == "decomposition-review" {
			round = state.Decompose.Round + 1
		}
	}

	// Create review sub-shard
	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339)
	verdictUpper := "FAIL"
	if verdict == "pass" {
		verdictUpper = "PASS"
	}

	content := fmt.Sprintf("# Gate: %s -- Round %d\n\n**Design:** %s\n**Reviewer:** %s\n**Timestamp:** %s\n**Verdict:** %s\n",
		gateName, round, designID, c.Config.Agent, timestamp, verdictUpper)
	if readiness > 0 {
		content += fmt.Sprintf("**Readiness Score:** %d/5\n", readiness)
	}
	content += fmt.Sprintf("\n## Findings\n\n%s", body)

	title := fmt.Sprintf("Gate: %s -- Round %d -- %s", gateName, round, verdictUpper)
	metaMap := map[string]any{
		"design_id": designID,
		"gate_name": gateName,
		"round":     round,
		"verdict":   verdict,
	}
	if readiness > 0 {
		metaMap["readiness"] = readiness
	}
	meta, _ := json.Marshal(metaMap)
	reviewID, err := c.CreateShardWithMetadata(ctx, title, content, "review", nil, []string{gateName}, json.RawMessage(meta))
	if err != nil {
		return nil, fmt.Errorf("failed to create gate review shard: %v", err)
	}

	if err := c.CreateEdgeSimple(ctx, reviewID, designID, "child-of"); err != nil {
		return nil, fmt.Errorf("failed to create edge: %v", err)
	}

	// Record gate in DB
	if dbAvailable && pipelineID != "" {
		var readinessPtr *int
		if readiness > 0 {
			readinessPtr = &readiness
		}
		var bodyPtr *string
		if body != "" {
			bodyPtr = &body
		}
		reviewer := c.Config.Agent
		_, err := c.RecordGate(ctx, PipelineGateInput{
			PipelineID:     pipelineID,
			DesignID:       designID,
			GateName:       gateName,
			Phase:          currentPhase,
			Verdict:        verdict,
			Reviewer:       &reviewer,
			ReadinessScore: readinessPtr,
			Body:           bodyPtr,
			ReviewShardID:  &reviewID,
		})
		if err != nil && !isTableMissing(err) {
			return nil, fmt.Errorf("failed to record gate in DB: %v", err)
		}
	}

	// Dual-write to shard metadata
	gateState := map[string]any{
		"round":        round,
		"last_verdict": verdict,
		"last_shard":   reviewID,
		"history": []map[string]any{{
			"round":     round,
			"verdict":   verdict,
			"shard_id":  reviewID,
			"timestamp": timestamp,
		}},
	}
	gateJSON, err := json.Marshal(gateState)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gate state: %v", err)
	}
	if _, err := c.SetMetadataPath(ctx, designID, []string{"pipeline", gateName}, gateJSON); err != nil {
		return nil, fmt.Errorf("failed to update gate metadata: %v", err)
	}

	// Backward-compat fields
	if gateName == "readiness-review" {
		reviewState := &ReviewState{
			Round:           round,
			LastVerdict:     verdict,
			LastReviewShard: reviewID,
			History: append(func() []ReviewHistoryEntry {
				if state.Review != nil {
					return state.Review.History
				}
				return nil
			}(), ReviewHistoryEntry{
				Round:     round,
				Verdict:   verdict,
				ShardID:   reviewID,
				Timestamp: timestamp,
			}),
		}
		reviewJSON, _ := json.Marshal(reviewState)
		c.SetMetadataPath(ctx, designID, []string{"pipeline", "review"}, reviewJSON)
	} else if gateName == "decomposition-review" {
		decomposeState := &DecomposeState{
			Round:              round,
			LastVerdict:        verdict,
			LastDecomposeShard: reviewID,
			History: append(func() []DecomposeHistoryEntry {
				if state.Decompose != nil {
					return state.Decompose.History
				}
				return nil
			}(), DecomposeHistoryEntry{
				Round:     round,
				Verdict:   verdict,
				ShardID:   reviewID,
				Timestamp: timestamp,
			}),
		}
		decomposeJSON, _ := json.Marshal(decomposeState)
		c.SetMetadataPath(ctx, designID, []string{"pipeline", "decompose"}, decomposeJSON)
	}

	// Advance phase on pass
	nextPhase := ""
	resultPhase := currentPhase
	if verdict == "pass" {
		if cfg != nil {
			nextPhase = cfg.NextPhase(currentPhase)
		}
		if nextPhase == "" {
			switch gateName {
			case "readiness-review":
				nextPhase = "decompose"
			case "decomposition-review":
				nextPhase = "implement"
			}
		}
		if nextPhase != "" {
			_, err := c.PipelineUpdate(ctx, designID, &nextPhase, nil, nil, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to advance phase: %v", err)
			}
			resultPhase = nextPhase
			if dbAvailable && pipelineID != "" {
				_ = c.UpdatePipelineRunPhase(ctx, designID, nextPhase)
			}
		}
	}

	return &PipelineGateResult{
		DesignID:      designID,
		GateName:      gateName,
		Phase:         resultPhase,
		Round:         round,
		Verdict:       verdict,
		ReviewShardID: reviewID,
		NextPhase:     nextPhase,
	}, nil
}

// PipelineAudit retrieves the audit trail for a design.
func (c *Client) PipelineAudit(ctx context.Context, designID string) ([]PipelineAuditEntry, error) {
	records, err := c.GetGateHistory(ctx, designID)
	if err == nil && len(records) > 0 {
		entries := make([]PipelineAuditEntry, len(records))
		for i, r := range records {
			body := ""
			if r.Body != nil {
				body = *r.Body
			}
			shardID := ""
			if r.ReviewShardID != nil {
				shardID = *r.ReviewShardID
			}
			entries[i] = PipelineAuditEntry{
				Timestamp:     r.CreatedAt,
				GateName:      r.GateName,
				Round:         r.Round,
				Verdict:       r.Verdict,
				ReviewShardID: shardID,
				Body:          body,
			}
		}
		return entries, nil
	}

	// Fallback to shard metadata
	state, err := c.PipelineGet(ctx, designID)
	if err != nil {
		return nil, err
	}

	var entries []PipelineAuditEntry
	if state.Review != nil {
		for _, h := range state.Review.History {
			ts, _ := time.Parse(time.RFC3339, h.Timestamp)
			entries = append(entries, PipelineAuditEntry{
				Timestamp:     ts,
				GateName:      "readiness-review",
				Round:         h.Round,
				Verdict:       h.Verdict,
				ReviewShardID: h.ShardID,
			})
		}
	}
	if state.Decompose != nil {
		for _, h := range state.Decompose.History {
			ts, _ := time.Parse(time.RFC3339, h.Timestamp)
			entries = append(entries, PipelineAuditEntry{
				Timestamp:     ts,
				GateName:      "decomposition-review",
				Round:         h.Round,
				Verdict:       h.Verdict,
				ReviewShardID: h.ShardID,
			})
		}
	}
	return entries, nil
}

// UpdateShardStatus sets the status of a shard.
func (c *Client) UpdateShardStatus(ctx context.Context, id, status string) error {
	conn, err := c.Connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	result, err := conn.Exec(ctx, `UPDATE shards SET status = $2, updated_at = NOW() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("failed to update shard status: %v", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("shard not found: %s", id)
	}
	return nil
}

// AddShardLabel adds a label to a shard.
func (c *Client) AddShardLabel(ctx context.Context, id, label string) error {
	conn, err := c.Connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `INSERT INTO shard_labels (shard_id, label) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, label)
	if err != nil {
		return fmt.Errorf("failed to add label: %v", err)
	}
	return nil
}

// CreateWorktree creates a git worktree for a task and records the path in metadata.
func (c *Client) CreateWorktree(ctx context.Context, taskID, repoRoot, project string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %v", err)
	}

	worktreeBase := filepath.Join(home, "worktrees", project)
	if err := os.MkdirAll(worktreeBase, 0755); err != nil {
		return "", fmt.Errorf("failed to create worktree base dir: %v", err)
	}

	worktreePath := filepath.Join(worktreeBase, taskID)
	branch := taskID

	// Create the branch from main
	if out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "branch", branch, "main").CombinedOutput(); err != nil {
		// Branch may already exist, that's OK
		if !strings.Contains(string(out), "already exists") {
			return "", fmt.Errorf("failed to create branch %s: %s\n%s", branch, err, string(out))
		}
	}

	// Create the worktree
	if out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "worktree", "add", worktreePath, branch).CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to create worktree: %s\n%s", err, string(out))
	}

	// Record worktree_path in metadata
	pathJSON, _ := json.Marshal(worktreePath)
	if _, err := c.SetMetadataPath(ctx, taskID, []string{"worktree_path"}, pathJSON); err != nil {
		return worktreePath, fmt.Errorf("worktree created at %s but failed to update metadata: %v", worktreePath, err)
	}

	return worktreePath, nil
}

// RemoveWorktree removes a git worktree for a task.
func (c *Client) RemoveWorktree(ctx context.Context, taskID string) error {
	worktreePath, err := c.GetMetadataField(ctx, taskID, []string{"worktree_path"})
	if err != nil || worktreePath == "" {
		return fmt.Errorf("no worktree_path in metadata for %s", taskID)
	}

	if out, err := exec.Command("git", "worktree", "remove", "--force", worktreePath).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to remove worktree %s: %s\n%s", worktreePath, err, string(out))
	}

	return nil
}

// --- Poller queries ---

// FindNewDesigns returns open design shards that do NOT have pipeline metadata.
func (c *Client) FindNewDesigns(ctx context.Context) ([]ShardSummary, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `
		SELECT id, title, type, status
		FROM shards
		WHERE project = $1
			AND type = 'design'
			AND status = 'open'
			AND (metadata IS NULL OR NOT (metadata ? 'pipeline'))
		ORDER BY created_at ASC
	`, c.Config.Project)
	if err != nil {
		return nil, fmt.Errorf("FindNewDesigns: %w", err)
	}
	defer rows.Close()

	var results []ShardSummary
	for rows.Next() {
		var s ShardSummary
		if err := rows.Scan(&s.ID, &s.Title, &s.Type, &s.Status); err != nil {
			return nil, err
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// FindTasksNeedingReview returns tasks in needs-review status.
func (c *Client) FindTasksNeedingReview(ctx context.Context) ([]ShardSummary, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `
		SELECT t.id, t.title, t.type, t.status
		FROM shards t
		JOIN edges e ON e.from_id = t.id AND e.edge_type = 'child-of'
		JOIN shards d ON d.id = e.to_id AND d.type = 'design'
		WHERE t.project = $1
			AND t.type = 'task'
			AND t.status = 'needs-review'
			AND d.metadata IS NOT NULL
			AND d.metadata ? 'pipeline'
			AND d.metadata->'pipeline'->>'phase' = 'implement'
		ORDER BY t.updated_at ASC
	`, c.Config.Project)
	if err != nil {
		return nil, fmt.Errorf("FindTasksNeedingReview: %w", err)
	}
	defer rows.Close()

	var results []ShardSummary
	for rows.Next() {
		var s ShardSummary
		if err := rows.Scan(&s.ID, &s.Title, &s.Type, &s.Status); err != nil {
			return nil, err
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// FindSatisfiedWaits returns designs whose waiting_for list is all closed.
func (c *Client) FindSatisfiedWaits(ctx context.Context) ([]PipelineWait, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `
		SELECT id, title, metadata->'pipeline'->'waiting_for' AS waiting_for
		FROM shards
		WHERE project = $1
			AND type = 'design'
			AND status IN ('open', 'ready', 'in_progress', 'needs-review')
			AND metadata IS NOT NULL
			AND metadata ? 'pipeline'
			AND jsonb_array_length(COALESCE(metadata->'pipeline'->'waiting_for', '[]'::jsonb)) > 0
		ORDER BY updated_at ASC
	`, c.Config.Project)
	if err != nil {
		return nil, fmt.Errorf("FindSatisfiedWaits: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		ID         string
		Title      string
		WaitingFor []string
	}
	var candidates []candidate
	for rows.Next() {
		var id, title string
		var wfRaw json.RawMessage
		if err := rows.Scan(&id, &title, &wfRaw); err != nil {
			return nil, err
		}
		var wf []string
		if err := json.Unmarshal(wfRaw, &wf); err != nil {
			continue
		}
		if len(wf) == 0 {
			continue
		}
		candidates = append(candidates, candidate{ID: id, Title: title, WaitingFor: wf})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	conn2, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn2.Close(ctx)

	var results []PipelineWait
	for _, cand := range candidates {
		row := conn2.QueryRow(ctx, `
			SELECT COUNT(*) FROM shards WHERE id = ANY($1) AND status != 'closed'
		`, cand.WaitingFor)
		var notClosed int
		if err := row.Scan(&notClosed); err != nil {
			continue
		}
		if notClosed == 0 {
			results = append(results, PipelineWait{
				DesignID:   cand.ID,
				Title:      cand.Title,
				WaitingFor: cand.WaitingFor,
			})
		}
	}
	return results, nil
}

// FindInProgressTasks returns all task shards currently in_progress.
func (c *Client) FindInProgressTasks(ctx context.Context) ([]ShardSummary, error) {
	conn, err := c.Connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `
		SELECT id, title, type, status, updated_at, metadata
		FROM shards
		WHERE project = $1 AND type = 'task' AND status = 'in_progress'
		ORDER BY updated_at ASC
	`, c.Config.Project)
	if err != nil {
		return nil, fmt.Errorf("FindInProgressTasks: %w", err)
	}
	defer rows.Close()

	var results []ShardSummary
	for rows.Next() {
		var s ShardSummary
		if err := rows.Scan(&s.ID, &s.Title, &s.Type, &s.Status, &s.UpdatedAt, &s.Metadata); err != nil {
			return nil, err
		}
		results = append(results, s)
	}
	return results, rows.Err()
}
