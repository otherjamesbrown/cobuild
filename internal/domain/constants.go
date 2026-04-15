package domain

// Phase names used in pipeline_runs.current_phase.
const (
	// PhaseDesign is the design readiness-review phase.
	PhaseDesign = "design"
	// PhaseDecompose is the task decomposition phase.
	PhaseDecompose = "decompose"
	// PhaseFix is the single-session bug-fix phase.
	PhaseFix = "fix"
	// PhaseInvestigate is the bug investigation phase.
	PhaseInvestigate = "investigate"
	// PhaseImplement is the task implementation phase.
	PhaseImplement = "implement"
	// PhaseReview is the review and merge phase.
	PhaseReview = "review"
	// PhaseDeploy is the deployment approval or execution phase.
	PhaseDeploy = "deploy"
	// PhaseRetrospective is the retrospective step exposed by next-step flows.
	PhaseRetrospective = "retrospective"
	// PhaseDone is the terminal pipeline phase before completion.
	PhaseDone = "done"
)

// Gate names recorded in pipeline_gates.
const (
	// GateReadinessReview is the design readiness gate.
	GateReadinessReview = "readiness-review"
	// GateDecompositionReview is the task decomposition gate.
	GateDecompositionReview = "decomposition-review"
	// GateReview is the implementation review gate.
	GateReview = "review"
	// GateKBSync is the knowledge-base synchronization gate.
	GateKBSync = "kb-sync"
	// GateRetrospective is the final retrospective gate.
	GateRetrospective = "retrospective"
)

// Work-item metadata keys written by CoBuild.
const (
	// MetaAgent stores the dispatched agent label.
	MetaAgent = "agent"
	// MetaCompletionMode stores direct-vs-code completion mode.
	MetaCompletionMode = "completion_mode"
	// MetaCumulativeTokens stores cumulative token usage.
	MetaCumulativeTokens = "cumulative_tokens"
	// MetaDispatchRuntime stores the selected dispatch runtime.
	MetaDispatchRuntime = "dispatch_runtime"
	// MetaDispatchedAt stores the dispatch timestamp.
	MetaDispatchedAt = "dispatched_at"
	// MetaEarlyDeath stores whether a session died early.
	MetaEarlyDeath = "early_death"
	// MetaLogFile stores the dispatch log path.
	MetaLogFile = "log_file"
	// MetaMergeRetryCount stores review merge retry attempts.
	MetaMergeRetryCount = "merge_retry_count"
	// MetaPRURL stores the task or design pull request URL.
	MetaPRURL = "pr_url"
	// MetaRepo stores the target repository name.
	MetaRepo = "repo"
	// MetaSessionID stores the dispatch session identifier.
	MetaSessionID = "session_id"
	// MetaTmuxWindow stores the tmux window name.
	MetaTmuxWindow = "tmux_window"
	// MetaWave stores the task dependency wave number.
	MetaWave = "wave"
	// MetaWorktreePath stores the task worktree path.
	MetaWorktreePath = "worktree_path"
)

// Pipeline task rebase statuses stored in pipeline_tasks.rebase_status.
const (
	// RebaseStatusNone means no sibling rebase outcome has been recorded.
	RebaseStatusNone = "none"
	// RebaseStatusRebased means the sibling branch rebased and pushed cleanly.
	RebaseStatusRebased = "rebased"
	// RebaseStatusConflict means the sibling branch hit a rebase conflict.
	RebaseStatusConflict = "conflict"
)

// Work-item types used by the connector layer.
const (
	// WorkItemTypeBug is the bug shard type.
	WorkItemTypeBug = "bug"
	// WorkItemTypeDesign is the design shard type.
	WorkItemTypeDesign = "design"
	// WorkItemTypeReview is the review shard type.
	WorkItemTypeReview = "review"
	// WorkItemTypeTask is the task shard type.
	WorkItemTypeTask = "task"
)

// Dispatch runtimes selectable by CoBuild.
const (
	// RuntimeClaudeCode is the Claude Code dispatch runtime.
	RuntimeClaudeCode = "claude-code"
	// RuntimeCodex is the Codex dispatch runtime.
	RuntimeCodex = "codex"
)

// CoBuild-owned task, run, and session statuses.
const (
	// StatusCancelled marks a cancelled CoBuild session.
	StatusCancelled = "cancelled"
	// StatusCompleted marks completed CoBuild work.
	StatusCompleted = "completed"
	// StatusFailed marks failed CoBuild work.
	StatusFailed = "failed"
	// StatusInProgress marks active CoBuild work.
	StatusInProgress = "in_progress"
	// StatusNeedsReview marks work waiting for review processing.
	StatusNeedsReview = "needs-review"
	// StatusPending marks queued CoBuild work.
	StatusPending = "pending"
)

// Next-step actions printed in command guidance.
const (
	// ActionComplete is the completion guidance action.
	ActionComplete = "complete"
	// ActionDeploy is the deploy guidance action.
	ActionDeploy = "deploy"
	// ActionDispatch is the dispatch guidance action.
	ActionDispatch = "dispatch"
	// ActionDispatchWave is the wave-dispatch guidance action.
	ActionDispatchWave = "dispatch-wave"
	// ActionGateFail is the gate failure guidance action.
	ActionGateFail = "gate-fail"
	// ActionGatePass is the gate pass guidance action.
	ActionGatePass = "gate-pass"
	// ActionInit is the init guidance action.
	ActionInit = "init"
	// ActionMerge is the merge guidance action.
	ActionMerge = "merge"
	// ActionMergeDesign is the merge-design guidance action.
	ActionMergeDesign = "merge-design"
	// ActionProcessReview is the review-processing guidance action.
	ActionProcessReview = "process-review"
	// ActionRetro is the retrospective guidance action.
	ActionRetro = "retro"
	// ActionRun is the poller handoff guidance action.
	ActionRun = "run"
	// ActionWaitComplete is the post-wait guidance action.
	ActionWaitComplete = "wait-complete"
)

// Review-processing outcomes used in next-step guidance.
const (
	// OutcomeMerged indicates review merged the PR.
	OutcomeMerged = "merged"
	// OutcomeRedispatched indicates review requested a redispatch.
	OutcomeRedispatched = "redispatched"
	// OutcomeWaiting indicates review data is not ready yet.
	OutcomeWaiting = "waiting"
)
