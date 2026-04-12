// Package runtime defines the pluggable agent-runtime abstraction for
// cobuild dispatch. Each runtime implementation wraps a specific agent CLI
// (Claude Code, OpenAI Codex, etc.) and owns the CLI-specific bits of a
// dispatch: trust/auth pre-flight, agent settings files, the bash runner
// script spawned inside a tmux window, and post-hoc parsing of the session
// log for token/usage analytics.
//
// Adding a new runtime: implement the Runtime interface and register it in
// the package-level registry in init(). Dispatch selects a runtime by name
// via Get().
package runtime

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// RunnerInput is everything a runtime needs to build its tmux runner script.
// Values are already resolved by dispatch.go — the runtime just plugs them
// into its CLI template.
type RunnerInput struct {
	// WorktreePath is the absolute path to the git worktree the agent runs in.
	WorktreePath string
	// RepoRoot is the absolute path of the main repository (not the worktree).
	// Used by cobuild complete to find the connector config.
	RepoRoot string
	// TaskID is the work-item ID (e.g. "cb-abc123"); also used as the tmux window name.
	TaskID string
	// PromptFile is an absolute path to a temp file containing the rendered prompt.
	// The runner script is responsible for reading and removing it.
	PromptFile string
	// Model is the pre-resolved model identifier for this dispatch (e.g. "sonnet",
	// "gpt-5.4"). Empty means "use the runtime's own default".
	Model string
	// ExtraFlags is any runtime-specific CLI flags from config (e.g. ClaudeFlags
	// or a codex-specific equivalent).
	ExtraFlags string
	// SessionID is the pipeline_sessions row ID created by dispatch, exported
	// into the child env as COBUILD_SESSION_ID so hooks/events can link back.
	SessionID string
	// HooksDir is the absolute path to the hooks/ directory within the cobuild
	// repo; agents that support hooks (Claude) read their hook scripts from here.
	HooksDir string
	// Phase is the pipeline phase this dispatch is for (e.g. "design",
	// "implement", "fix"). Gate phases (design, decompose, review, done,
	// investigate) should NOT run cobuild complete — the agent records the
	// gate verdict directly. Only implementation phases (implement, fix)
	// produce code that needs the commit→PR→needs-review flow.
	Phase string
}

// IsGatePhase returns true if the phase is a gate/evaluation phase where
// the agent records a verdict rather than producing code changes.
func IsGatePhase(phase string) bool {
	switch phase {
	case "design", "decompose", "review", "done", "investigate":
		return true
	default:
		return false
	}
}

// SessionStats is the post-hoc aggregate a runtime extracts from its session
// log. Returned by ParseSessionStats; used by dispatch.go / complete.go to
// record analytics into pipeline_sessions.
type SessionStats struct {
	// InputTokens is the sum of non-cached input tokens across all turns.
	InputTokens int
	// CachedInputTokens is the sum of cached input tokens across all turns
	// (OpenAI prompt-caching credits, or Claude's cache_read_input_tokens).
	CachedInputTokens int
	// OutputTokens is the sum of output tokens across all turns.
	OutputTokens int
	// TurnCount is the number of turns observed in the log.
	TurnCount int
	// SessionUUID is the runtime-assigned session identifier (Claude Code
	// session UUID, Codex thread_id). Used for resume semantics.
	SessionUUID string
	// LastMessage is the agent's final message (the "answer") if the runtime
	// captured one via its equivalent of --output-last-message.
	LastMessage string
}

// Runtime is the abstraction over an agent CLI (Claude Code, Codex, ...).
// Each method corresponds to one step dispatch.go performs; implementations
// handle only the CLI-specific parts.
type Runtime interface {
	// Name returns the canonical runtime identifier ("claude-code", "codex").
	// Used by config lookup, task metadata, store records, and CLI flags.
	Name() string

	// ContextFile returns the filename inside a worktree that the runtime
	// reads for project instructions. Claude Code uses "CLAUDE.md", Codex
	// uses "AGENTS.md".
	ContextFile() string

	// PreDispatch runs before the agent is spawned. Use this to pre-accept
	// trust dialogs, pre-register worktree paths, or otherwise massage the
	// user's home-directory state so the agent starts clean. It should be
	// idempotent — redispatch must not break anything. A returned error is
	// treated as a warning by dispatch, not a hard failure.
	PreDispatch(ctx context.Context, worktreePath string) error

	// WriteSettings writes any agent-settings files into the worktree (e.g.
	// .claude/settings.local.json with the Stop hook and deny-list). Runtimes
	// that don't need this can return nil.
	WriteSettings(worktreePath string) error

	// BuildRunnerScript returns the full bash script body that will be written
	// to disk and executed inside a tmux window. The script is responsible for
	// running the agent CLI, capturing logs, and finally invoking
	// `cobuild complete <task-id>` from $COBUILD_REPO_ROOT.
	BuildRunnerScript(in RunnerInput) (string, error)

	// ParseSessionStats reads a captured session log (the agent's JSONL event
	// stream from the tmux run) and extracts aggregate usage data. Called by
	// complete / analytics paths after the agent has exited.
	ParseSessionStats(sessionLogPath string) (SessionStats, error)
}

// --- Registry ---

var (
	registryMu sync.RWMutex
	registry   = map[string]Runtime{}
)

// Register adds a Runtime to the global registry. Intended to be called
// from init() in each runtime-implementation package.
func Register(rt Runtime) {
	if rt == nil {
		panic("runtime: Register called with nil Runtime")
	}
	name := rt.Name()
	if name == "" {
		panic("runtime: Register called with empty Name")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("runtime: duplicate Register for %q", name))
	}
	registry[name] = rt
}

// Get looks up a Runtime by name. Returns an error if no runtime with that
// name has been registered, listing the available names to aid debugging.
func Get(name string) (Runtime, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	if rt, ok := registry[name]; ok {
		return rt, nil
	}
	return nil, fmt.Errorf("runtime %q not registered (available: %v)", name, listLocked())
}

// List returns all registered runtime names in sorted order.
func List() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return listLocked()
}

func listLocked() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
