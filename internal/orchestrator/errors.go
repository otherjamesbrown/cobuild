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
)

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

// TimeoutError reports a phase that failed to advance before the deadline.
type TimeoutError struct {
	ShardID string
	Phase   string
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("timed out waiting for shard %s to advance from %s after %s", e.ShardID, e.Phase, e.Timeout)
}

// UnknownPhaseError reports a phase the runner does not know how to handle.
type UnknownPhaseError struct {
	ShardID string
	Phase   string
}

func (e *UnknownPhaseError) Error() string {
	return fmt.Sprintf("unknown phase %q for shard %s", e.Phase, e.ShardID)
}
