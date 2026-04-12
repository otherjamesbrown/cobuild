package orchestrator

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrDeployApprovalRequired indicates the pipeline reached deploy and needs a human.
	ErrDeployApprovalRequired = errors.New("deploy approval required")
	// ErrInterrupted indicates the run was cancelled by an external signal.
	ErrInterrupted = errors.New("orchestrator interrupted")
	// ErrBlocked indicates the run stopped because progress is blocked.
	ErrBlocked = errors.New("orchestrator blocked")
)

// StopReason classifies why the foreground loop stopped.
type StopReason string

const (
	StopReasonCompleted      StopReason = "completed"
	StopReasonDeployApproval StopReason = "deploy-approval"
	StopReasonInterrupted    StopReason = "interrupted"
	StopReasonTimeout        StopReason = "timeout"
	StopReasonStalledAgent   StopReason = "stalled-agent"
	StopReasonBlockedReview  StopReason = "blocked-review"
	StopReasonMergeConflict  StopReason = "merge-conflict"
	StopReasonCriticalReview StopReason = "critical-review"
	StopReasonUnknownFailure StopReason = "error"
)

// StopReport is the machine-readable summary of an orchestration stop.
type StopReport struct {
	Status          string     `json:"status"`
	ShardID         string     `json:"shard_id,omitempty"`
	Phase           string     `json:"phase,omitempty"`
	Reason          StopReason `json:"reason"`
	Message         string     `json:"message"`
	BlockingTaskIDs []string   `json:"blocking_task_ids,omitempty"`
	Recoverable     bool       `json:"recoverable"`
	ExitCode        int        `json:"exit_code"`
}

// Reporter exposes a structured stop report.
type Reporter interface {
	Report() StopReport
}

// DeployRequiredError reports a pipeline paused at deploy.
type DeployRequiredError struct {
	ShardID string
	Phase   string
}

func (e *DeployRequiredError) Error() string {
	return fmt.Sprintf("shard %s is blocked at %s: %v", e.ShardID, e.Phase, ErrDeployApprovalRequired)
}

func (e *DeployRequiredError) Unwrap() error {
	return ErrDeployApprovalRequired
}

func (e *DeployRequiredError) Report() StopReport {
	return StopReport{
		Status:      "stopped",
		ShardID:     e.ShardID,
		Phase:       e.Phase,
		Reason:      StopReasonDeployApproval,
		Message:     e.Error(),
		Recoverable: true,
		ExitCode:    2,
	}
}

// TimeoutError reports a phase that failed to advance before the deadline.
type TimeoutError struct {
	ShardID         string
	Phase           string
	Timeout         time.Duration
	BlockingTaskIDs []string
}

func (e *TimeoutError) Error() string {
	if len(e.BlockingTaskIDs) == 0 {
		return fmt.Sprintf("timed out waiting for shard %s to advance from %s after %s", e.ShardID, e.Phase, e.Timeout)
	}
	return fmt.Sprintf(
		"timed out waiting for shard %s to advance from %s after %s (blocking tasks: %v)",
		e.ShardID, e.Phase, e.Timeout, e.BlockingTaskIDs,
	)
}

func (e *TimeoutError) Report() StopReport {
	return StopReport{
		Status:          "failed",
		ShardID:         e.ShardID,
		Phase:           e.Phase,
		Reason:          StopReasonTimeout,
		Message:         e.Error(),
		BlockingTaskIDs: append([]string(nil), e.BlockingTaskIDs...),
		Recoverable:     true,
		ExitCode:        1,
	}
}

// InterruptedError reports a clean SIGINT stop.
type InterruptedError struct {
	ShardID string
	Phase   string
}

func (e *InterruptedError) Error() string {
	return fmt.Sprintf("orchestrator interrupted while waiting on shard %s in %s", e.ShardID, e.Phase)
}

func (e *InterruptedError) Unwrap() error {
	return ErrInterrupted
}

func (e *InterruptedError) Report() StopReport {
	return StopReport{
		Status:      "stopped",
		ShardID:     e.ShardID,
		Phase:       e.Phase,
		Reason:      StopReasonInterrupted,
		Message:     e.Error(),
		Recoverable: true,
		ExitCode:    1,
	}
}

// BlockedError reports a recoverable blocker that needs operator action.
type BlockedError struct {
	ShardID         string
	Phase           string
	Reason          StopReason
	Message         string
	BlockingTaskIDs []string
	Recoverable     bool
}

func (e *BlockedError) Error() string {
	return e.Message
}

func (e *BlockedError) Unwrap() error {
	return ErrBlocked
}

func (e *BlockedError) Report() StopReport {
	return StopReport{
		Status:          "failed",
		ShardID:         e.ShardID,
		Phase:           e.Phase,
		Reason:          e.Reason,
		Message:         e.Message,
		BlockingTaskIDs: append([]string(nil), e.BlockingTaskIDs...),
		Recoverable:     e.Recoverable,
		ExitCode:        1,
	}
}

// UnknownPhaseError reports a phase the runner does not know how to handle.
type UnknownPhaseError struct {
	ShardID string
	Phase   string
}

func (e *UnknownPhaseError) Error() string {
	return fmt.Sprintf("unknown phase %q for shard %s", e.Phase, e.ShardID)
}

// Report returns the structured stop report for an orchestrator outcome.
func Report(err error) StopReport {
	if err == nil {
		return StopReport{
			Status:      "completed",
			Reason:      StopReasonCompleted,
			Message:     "pipeline complete",
			Recoverable: false,
			ExitCode:    0,
		}
	}

	var reporter Reporter
	if errors.As(err, &reporter) {
		return reporter.Report()
	}

	return StopReport{
		Status:      "failed",
		Reason:      StopReasonUnknownFailure,
		Message:     err.Error(),
		Recoverable: false,
		ExitCode:    1,
	}
}
