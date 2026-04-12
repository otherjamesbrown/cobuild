package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// GateFieldConfig describes a single field within a gate.
type GateFieldConfig struct {
	Type     string `yaml:"type"`
	Min      *int   `yaml:"min,omitempty"`
	Max      *int   `yaml:"max,omitempty"`
	Required bool   `yaml:"required,omitempty"`
}

// GateConfig describes a quality gate within a pipeline phase.
type GateConfig struct {
	Name          string                     `yaml:"name"`
	Skill         string                     `yaml:"skill,omitempty"`
	Model         string                     `yaml:"model,omitempty"`
	Fields        map[string]GateFieldConfig `yaml:"fields,omitempty"`
	RequiresLabel string                     `yaml:"requires_label,omitempty"`
}

// PhaseConfig describes a pipeline phase and its gates.
type PhaseConfig struct {
	Name       string       `yaml:"name,omitempty"`
	Gate       string       `yaml:"gate,omitempty"`
	Skill      string       `yaml:"skill,omitempty"`
	StallCheck string       `yaml:"stall_check,omitempty"`
	Model      string       `yaml:"model,omitempty"`
	Gates      []GateConfig `yaml:"gates,omitempty"`
}

// WorkflowConfig defines a pipeline path for a specific shard type.
type WorkflowConfig struct {
	Phases        []string       `yaml:"phases"`
	ContextLayers []ContextLayer `yaml:"context_layers,omitempty"`
}

// ContextLayer defines a piece of context that can be injected into agent sessions.
type ContextLayer struct {
	Name   string   `yaml:"name"`
	Source string   `yaml:"source"` // "file:<path>", "work-item:<id>", "skills:<name>", "dispatch-prompt", "parent-design"
	When   string   `yaml:"when"`   // "always", "interactive", "dispatch", "phase:<name>", "gate:<name>"
	Filter []string `yaml:"filter,omitempty"`
}

// ContextConfig controls what context is injected into agent sessions.
type ContextConfig struct {
	Layers []ContextLayer `yaml:"layers,omitempty"`
}

// StoreCfg configures where CoBuild stores its own orchestration data.
type StoreCfg struct {
	Backend string `yaml:"backend,omitempty"` // "postgres", "sqlite" (future), "file" (future)
	DSN     string `yaml:"dsn,omitempty"`     // connection string for postgres/sqlite
	Path    string `yaml:"path,omitempty"`    // directory for file-based store
}

// ConnectorsCfg holds connector configuration for external systems.
type ConnectorsCfg struct {
	WorkItems WorkItemsConnectorCfg `yaml:"work_items,omitempty"`
}

// WorkItemsConnectorCfg configures the work-item connector.
type WorkItemsConnectorCfg struct {
	Type   string            `yaml:"type,omitempty"`   // "context-palace", "beads"
	Config map[string]string `yaml:"config,omitempty"` // connector-specific settings
}

// Config holds pipeline configuration loaded from YAML files.
type Config struct {
	Build           []string                  `yaml:"build,omitempty"`
	Test            []string                  `yaml:"test,omitempty"`
	CompletionSteps []string                  `yaml:"completion_steps,omitempty"`
	Agents          map[string]AgentCfg       `yaml:"agents,omitempty"`
	Dispatch        DispatchCfg               `yaml:"dispatch,omitempty"`
	Monitoring      MonitoringCfg             `yaml:"monitoring,omitempty"`
	Review          ReviewCfg                 `yaml:"review,omitempty"`
	Context         ContextConfig             `yaml:"context,omitempty"`
	Workflows       map[string]WorkflowConfig `yaml:"workflows,omitempty"`
	Deploy          DeployCfg                 `yaml:"deploy,omitempty"`
	GitHub          GitHubCfg                 `yaml:"github,omitempty"`
	Storage         StoreCfg                  `yaml:"storage,omitempty"`
	Connectors      ConnectorsCfg             `yaml:"connectors,omitempty"`
	Poller          PollerCfg                 `yaml:"poller,omitempty"`
	KBSync          KBSyncCfg                 `yaml:"kb_sync,omitempty"`
	SkillsDir       string                    `yaml:"skills_dir,omitempty"`
	Phases          map[string]PhaseConfig    `yaml:"phases,omitempty"`
}

// KBSyncCfg controls automatic KB synchronisation after PR merges.
// Projects opt in by setting enabled: true and optionally specifying a
// root KB article. If root_article is empty, kb-sync searches all KB
// articles in the project.
type KBSyncCfg struct {
	Enabled     bool   `yaml:"enabled,omitempty"`
	RootArticle string `yaml:"root_article,omitempty"` // shard ID of the KB root (optional)
}

// PollerCfg controls the autonomous pipeline poller.
type PollerCfg struct {
	AutoLabel     string `yaml:"auto_label,omitempty"`     // label that triggers auto-processing (default: "cobuild")
	Interval      int    `yaml:"interval,omitempty"`       // poll interval in seconds (default: 30)
	MaxConcurrent int    `yaml:"max_concurrent,omitempty"` // max simultaneous dispatches (default: 3)
}

// AgentCfg defines an agent's domain capabilities.
type AgentCfg struct {
	Domains []string `yaml:"domains"`
}

// DispatchCfg controls how work is dispatched to agents.
//
// DefaultRuntime and Runtimes were added alongside pluggable agent runtimes
// (claude-code vs codex). ClaudeFlags and DefaultModel are legacy fields
// retained for back-compat with older configs — DefaultModel is still used
// as a final fallback by ModelForPhase[Runtime] when no runtime-specific
// model is configured.
type DispatchCfg struct {
	MaxConcurrent  int    `yaml:"max_concurrent,omitempty"`
	TmuxSession    string `yaml:"tmux_session,omitempty"`
	DefaultRuntime string `yaml:"default_runtime,omitempty"`
	// WaveStrategy controls whether dispatch proceeds one dependency wave at a time
	// ("serial") or dispatches all currently-eligible work at once ("parallel").
	WaveStrategy string                `yaml:"wave_strategy,omitempty"`
	Runtimes     map[string]RuntimeCfg `yaml:"runtimes,omitempty"`

	// Legacy / back-compat fields ----------------------------------------
	ClaudeFlags  string `yaml:"claude_flags,omitempty"`
	DefaultModel string `yaml:"default_model,omitempty"`
}

// RuntimeCfg holds per-runtime dispatch settings. Keyed by runtime name
// ("claude-code", "codex") inside DispatchCfg.Runtimes.
type RuntimeCfg struct {
	// Model is the default model identifier for this runtime (e.g. "sonnet"
	// for claude-code, "gpt-5.4" for codex). Overrides DispatchCfg.DefaultModel
	// when set; overridden by phase.model / gate.model from the workflow.
	Model string `yaml:"model,omitempty"`
	// Flags is additional CLI flags passed to the runtime binary. When set
	// this REPLACES the runtime's built-in default flags (e.g. for claude-code
	// this replaces "--dangerously-skip-permissions"; for codex it replaces
	// "--json --full-auto").
	Flags string `yaml:"flags,omitempty"`
}

const (
	WaveStrategySerial   = "serial"
	WaveStrategyParallel = "parallel"
)

// ModelForPhase resolves the model to use for a given phase, falling back
// through the legacy (runtime-unaware) chain. Kept for call sites that
// have not yet been migrated to ModelForPhaseRuntime.
// Priority: gate model -> phase model -> dispatch.default_model -> ""
func (c *Config) ModelForPhase(phaseName, gateName string) string {
	return c.ModelForPhaseRuntime(phaseName, gateName, "")
}

// ModelForPhaseRuntime resolves the model to use for a given phase,
// taking the active runtime into account so runtime-specific defaults
// (e.g. "sonnet" for claude-code, "gpt-5.4" for codex) are picked up
// when no phase/gate override is set.
//
// Priority (first non-empty wins):
//  1. gate-specific model (phase.gates[gateName].model)
//  2. phase-specific model (phase.model)
//  3. review.model when phaseName == "review"
//  4. monitoring.model when phaseName == "monitoring" or gate == "stall-check"
//  5. dispatch.runtimes[runtime].model
//  6. dispatch.default_model (legacy, runtime-unaware)
//  7. ""
func (c *Config) ModelForPhaseRuntime(phaseName, gateName, runtime string) string {
	if c == nil {
		return ""
	}
	if gateName != "" {
		if gate := c.FindGate(phaseName, gateName); gate != nil && gate.Model != "" {
			return gate.Model
		}
	}
	if phase := c.FindPhase(phaseName); phase != nil && phase.Model != "" {
		return phase.Model
	}
	if c.Review.Model != "" && phaseName == "review" {
		return c.Review.Model
	}
	if c.Monitoring.Model != "" && (phaseName == "monitoring" || gateName == "stall-check") {
		return c.Monitoring.Model
	}
	if runtime != "" {
		if rc, ok := c.Dispatch.Runtimes[runtime]; ok && rc.Model != "" {
			return rc.Model
		}
	}
	return c.Dispatch.DefaultModel
}

// FlagsForRuntime returns runtime-specific extra flags from config, falling
// back to the legacy ClaudeFlags field when runtime is "claude-code" and no
// runtime-specific flags are set. Empty return means "use the runtime's own
// built-in default flags".
func (c *Config) FlagsForRuntime(runtime string) string {
	if c == nil {
		return ""
	}
	if runtime != "" {
		if rc, ok := c.Dispatch.Runtimes[runtime]; ok && rc.Flags != "" {
			return rc.Flags
		}
	}
	// Back-compat: honour the legacy claude_flags field for the claude-code runtime.
	if runtime == "claude-code" && c.Dispatch.ClaudeFlags != "" {
		return c.Dispatch.ClaudeFlags
	}
	return ""
}

// ResolveRuntime picks the dispatch runtime for a task, applying the
// priority chain (caller-supplied override > task metadata > config default
// > hardcoded "claude-code"). The caller passes the flag override and
// task-metadata value — this function doesn't touch connectors itself.
func (c *Config) ResolveRuntime(flagOverride, metadataValue string) string {
	if flagOverride != "" {
		return flagOverride
	}
	if metadataValue != "" {
		return metadataValue
	}
	if c != nil && c.Dispatch.DefaultRuntime != "" {
		return c.Dispatch.DefaultRuntime
	}
	return "claude-code"
}

// ResolveWaveStrategy returns the normalized dispatch wave strategy.
// Supported values are "serial" and "parallel"; anything else falls back to "serial".
func (c *Config) ResolveWaveStrategy() string {
	if c == nil {
		return WaveStrategySerial
	}
	return normalizeWaveStrategy(c.Dispatch.WaveStrategy)
}

// GitHubCfg holds GitHub repository information.
type GitHubCfg struct {
	OwnerRepo string `yaml:"owner_repo"`
}

// DeployServiceCfg maps file paths to deploy/test/rollback commands.
type DeployServiceCfg struct {
	Name         string   `yaml:"name"`
	TriggerPaths []string `yaml:"trigger_paths"`        // file globs that trigger this deploy
	Command      string   `yaml:"command"`              // deploy command (e.g., "penf deploy gateway")
	SmokeTest    string   `yaml:"smoke_test,omitempty"` // verify deploy succeeded (e.g., "curl -s .../health")
	Rollback     string   `yaml:"rollback,omitempty"`   // revert on smoke test failure
	Timeout      string   `yaml:"timeout,omitempty"`    // max time for deploy + smoke test (default: 5m)
}

// DeployCfg controls auto-deploy after PR merge.
type DeployCfg struct {
	Enabled   bool               `yaml:"enabled,omitempty"`
	PreDeploy string             `yaml:"pre_deploy,omitempty"` // command to run before any deploys (e.g., migrations)
	Services  []DeployServiceCfg `yaml:"services,omitempty"`
}

// CICfg controls how CI results are evaluated.
type CICfg struct {
	Mode string `yaml:"mode,omitempty"`
	Wait bool   `yaml:"wait,omitempty"`
}

// ReviewCfg controls how PRs are reviewed.
type ReviewCfg struct {
	CI                CICfg    `yaml:"ci,omitempty"`
	Provider          string   `yaml:"provider,omitempty"`
	Strategy          string   `yaml:"strategy,omitempty"`
	ExternalReviewers []string `yaml:"external_reviewers,omitempty"`
	ProcessSkill      string   `yaml:"process_skill,omitempty"`
	ReviewSkill       string   `yaml:"review_skill,omitempty"`
	ReviewAgent       string   `yaml:"review_agent,omitempty"`
	Model             string   `yaml:"model,omitempty"`
	CrossModel        *bool    `yaml:"cross_model,omitempty"`
	PostComments      *bool    `yaml:"post_comments,omitempty"`
	CIMode            string   `yaml:"ci_mode,omitempty"`
	WaitForCI         *bool    `yaml:"wait_for_ci,omitempty"`
	Timeout           string   `yaml:"timeout,omitempty"`
}

// MonitoringCfg controls health monitoring for dispatched agents.
type MonitoringCfg struct {
	StallTimeout string            `yaml:"stall_timeout,omitempty"`
	CrashCheck   bool              `yaml:"crash_check,omitempty"`
	MaxRetries   int               `yaml:"max_retries,omitempty"`
	Cooldown     string            `yaml:"cooldown,omitempty"`
	Model        string            `yaml:"model,omitempty"`
	Actions      MonitoringActions `yaml:"actions,omitempty"`
}

// MonitoringActions defines what to do for each health event.
type MonitoringActions struct {
	OnStall      string `yaml:"on_stall,omitempty"`
	OnCrash      string `yaml:"on_crash,omitempty"`
	OnMaxRetries string `yaml:"on_max_retries,omitempty"`
}

// RepoRegistry maps project names to local repository paths.
type RepoRegistry struct {
	Repos map[string]RepoEntry `yaml:"repos"`
}

// RepoEntry describes a registered repository.
type RepoEntry struct {
	Path          string `yaml:"path"`
	DefaultBranch string `yaml:"default_branch,omitempty"`
}

// ValidPipelinePhases is the list of allowed pipeline phase values.
var ValidPipelinePhases = []string{
	"design", "decompose", "investigate", "fix", "implement", "review", "deploy", "kb-sync", "done",
}

// ValidatePipelinePhase checks if a phase string is valid.
func ValidatePipelinePhase(phase string) error {
	for _, valid := range ValidPipelinePhases {
		if phase == valid {
			return nil
		}
	}
	return fmt.Errorf("invalid pipeline phase %q; valid phases: %v", phase, ValidPipelinePhases)
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	defaultTrue := true
	return &Config{
		Dispatch: DispatchCfg{
			MaxConcurrent:  3,
			TmuxSession:    "", // empty = auto: cobuild-<project>
			WaveStrategy:   WaveStrategySerial,
			DefaultRuntime: "claude-code",
			Runtimes: map[string]RuntimeCfg{
				"claude-code": {Model: "sonnet"},
				"codex":       {Model: "gpt-5.4"},
			},
			// Legacy fields retained for back-compat
			ClaudeFlags:  "", // empty = use runtime's built-in default
			DefaultModel: "sonnet",
		},
		Monitoring: MonitoringCfg{
			StallTimeout: "30m",
			CrashCheck:   true,
			MaxRetries:   3,
			Cooldown:     "5m",
			Model:        "haiku",
			Actions: MonitoringActions{
				OnStall:      "skill:m-stall-check",
				OnCrash:      "redispatch",
				OnMaxRetries: "escalate",
			},
		},
		Review: ReviewCfg{
			Strategy:     "external",
			Provider:     "external",
			Model:        "haiku",
			CrossModel:   &defaultTrue,
			PostComments: &defaultTrue,
			WaitForCI:    &defaultTrue,
			Timeout:      "120s",
		},
		SkillsDir: "skills",
		Phases: map[string]PhaseConfig{
			"fix": {
				Skill: "fix/fix-bug.md",
			},
			"investigate": {
				Skill: "investigate/bug-investigation.md",
			},
			"kb-sync": {
				Skill: "kb-sync/kb-sync-phase.md",
			},
		},
		Workflows: map[string]WorkflowConfig{
			"design": {
				Phases: []string{"design", "decompose", "implement", "review", "kb-sync", "done"},
			},
			"bug": {
				Phases: []string{"fix", "review", "kb-sync", "done"},
			},
			"bug-complex": {
				Phases: []string{"investigate", "implement", "review", "kb-sync", "done"},
			},
			"task": {
				Phases: []string{"implement", "review", "kb-sync", "done"},
			},
		},
	}
}

// LoadConfig loads pipeline configuration by merging the global config
// (~/.cobuild/pipeline.yaml) with a repo-level override (<repoRoot>/.cobuild/pipeline.yaml).
func LoadConfig(repoRoot string) (*Config, error) {
	base := DefaultConfig()

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	globalPath := filepath.Join(home, ".cobuild", "pipeline.yaml")
	globalCfg, err := loadConfigFile(globalPath)
	if err != nil {
		return nil, fmt.Errorf("loading global pipeline config: %w", err)
	}
	if globalCfg != nil {
		base = MergeConfig(base, globalCfg)
	}

	if repoRoot != "" {
		repoPath := filepath.Join(repoRoot, ".cobuild", "pipeline.yaml")
		repoCfg, err := loadConfigFile(repoPath)
		if err != nil {
			return nil, fmt.Errorf("loading repo pipeline config: %w", err)
		}
		if repoCfg != nil {
			base = MergeConfig(base, repoCfg)
		}
	}

	return base, nil
}

// loadConfigFile reads and parses a single pipeline YAML file.
func loadConfigFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

// MergeConfig merges an override config into a base config.
func MergeConfig(base, override *Config) *Config {
	out := &Config{}

	out.Build = copyStrings(base.Build)
	out.Test = copyStrings(base.Test)
	out.CompletionSteps = copyStrings(base.CompletionSteps)
	out.Agents = copyAgents(base.Agents)
	out.Dispatch = base.Dispatch
	out.Monitoring = base.Monitoring
	out.Review = base.Review
	out.GitHub = base.GitHub
	out.SkillsDir = base.SkillsDir
	out.Workflows = copyWorkflows(base.Workflows)
	out.Phases = copyPhases(base.Phases)

	if override.Build != nil {
		out.Build = copyStrings(override.Build)
	}
	if override.Test != nil {
		out.Test = copyStrings(override.Test)
	}
	if override.CompletionSteps != nil {
		out.CompletionSteps = copyStrings(override.CompletionSteps)
	}
	if override.Phases != nil {
		out.Phases = copyPhases(override.Phases)
	}
	// Merge workflows per-name so an override that only names one workflow
	// doesn't wipe out the others from the base config (cb-11a464).
	// Historical behavior was wholesale replace, which combined with a stale
	// global ~/.cobuild/pipeline.yaml to produce the wrong bug workflow in
	// generated AGENTS.md files.
	if len(override.Workflows) > 0 {
		if out.Workflows == nil {
			out.Workflows = make(map[string]WorkflowConfig, len(override.Workflows))
		}
		for name, ow := range override.Workflows {
			out.Workflows[name] = WorkflowConfig{
				Phases:        copyStrings(ow.Phases),
				ContextLayers: ow.ContextLayers,
			}
		}
	}
	if override.Agents != nil {
		if out.Agents == nil {
			out.Agents = make(map[string]AgentCfg)
		}
		for k, v := range override.Agents {
			out.Agents[k] = v
		}
	}

	if override.Dispatch.MaxConcurrent != 0 {
		out.Dispatch.MaxConcurrent = override.Dispatch.MaxConcurrent
	}
	if override.Dispatch.WaveStrategy != "" {
		out.Dispatch.WaveStrategy = normalizeWaveStrategy(override.Dispatch.WaveStrategy)
	}
	if override.Dispatch.TmuxSession != "" {
		out.Dispatch.TmuxSession = override.Dispatch.TmuxSession
	}
	if override.Dispatch.DefaultRuntime != "" {
		out.Dispatch.DefaultRuntime = override.Dispatch.DefaultRuntime
	}
	if override.Dispatch.ClaudeFlags != "" {
		out.Dispatch.ClaudeFlags = override.Dispatch.ClaudeFlags
	}
	if override.Dispatch.DefaultModel != "" {
		out.Dispatch.DefaultModel = override.Dispatch.DefaultModel
	}
	// Merge per-runtime configs field-by-field so a repo config can override
	// just the model or just the flags for a single runtime without wiping
	// out the rest of the base runtimes map.
	if len(override.Dispatch.Runtimes) > 0 {
		if out.Dispatch.Runtimes == nil {
			out.Dispatch.Runtimes = make(map[string]RuntimeCfg, len(override.Dispatch.Runtimes))
		}
		for name, oc := range override.Dispatch.Runtimes {
			base := out.Dispatch.Runtimes[name]
			if oc.Model != "" {
				base.Model = oc.Model
			}
			if oc.Flags != "" {
				base.Flags = oc.Flags
			}
			out.Dispatch.Runtimes[name] = base
		}
	}
	if override.Review.Model != "" {
		out.Review.Model = override.Review.Model
	}
	if override.Review.Provider != "" {
		out.Review.Provider = override.Review.Provider
	}
	if override.Review.Strategy != "" {
		out.Review.Strategy = override.Review.Strategy
	}
	if override.Review.ReviewSkill != "" {
		out.Review.ReviewSkill = override.Review.ReviewSkill
	}
	if override.Review.ReviewAgent != "" {
		out.Review.ReviewAgent = override.Review.ReviewAgent
	}
	if override.Review.ProcessSkill != "" {
		out.Review.ProcessSkill = override.Review.ProcessSkill
	}
	if override.Review.ExternalReviewers != nil {
		out.Review.ExternalReviewers = copyStrings(override.Review.ExternalReviewers)
	}
	if override.Review.CrossModel != nil {
		out.Review.CrossModel = boolPtr(*override.Review.CrossModel)
	}
	if override.Review.PostComments != nil {
		out.Review.PostComments = boolPtr(*override.Review.PostComments)
	}
	if override.Review.CIMode != "" {
		out.Review.CIMode = override.Review.CIMode
	}
	if override.Review.WaitForCI != nil {
		out.Review.WaitForCI = boolPtr(*override.Review.WaitForCI)
	}
	if override.Review.Timeout != "" {
		out.Review.Timeout = override.Review.Timeout
	}
	if override.GitHub.OwnerRepo != "" {
		out.GitHub.OwnerRepo = override.GitHub.OwnerRepo
	}
	if override.SkillsDir != "" {
		out.SkillsDir = override.SkillsDir
	}

	if override.Monitoring.StallTimeout != "" {
		out.Monitoring.StallTimeout = override.Monitoring.StallTimeout
	}
	if override.Monitoring.CrashCheck {
		out.Monitoring.CrashCheck = true
	}
	if override.Monitoring.MaxRetries != 0 {
		out.Monitoring.MaxRetries = override.Monitoring.MaxRetries
	}
	if override.Monitoring.Cooldown != "" {
		out.Monitoring.Cooldown = override.Monitoring.Cooldown
	}
	if override.Monitoring.Actions.OnStall != "" {
		out.Monitoring.Actions.OnStall = override.Monitoring.Actions.OnStall
	}
	if override.Monitoring.Actions.OnCrash != "" {
		out.Monitoring.Actions.OnCrash = override.Monitoring.Actions.OnCrash
	}
	if override.Monitoring.Actions.OnMaxRetries != "" {
		out.Monitoring.Actions.OnMaxRetries = override.Monitoring.Actions.OnMaxRetries
	}
	if override.Monitoring.Model != "" {
		out.Monitoring.Model = override.Monitoring.Model
	}

	// KB Sync — override wins wholesale if enabled is set
	if override.KBSync.Enabled {
		out.KBSync = override.KBSync
	} else if base.KBSync.Enabled {
		out.KBSync = base.KBSync
	}

	// Deploy
	if len(override.Deploy.Services) > 0 {
		out.Deploy = override.Deploy
	} else if len(base.Deploy.Services) > 0 {
		out.Deploy = base.Deploy
	}

	// Storage
	if override.Storage.Backend != "" {
		out.Storage = override.Storage
	} else {
		out.Storage = base.Storage
	}

	// Connectors
	if override.Connectors.WorkItems.Type != "" {
		out.Connectors = override.Connectors
	} else {
		out.Connectors = base.Connectors
	}

	// Context
	if len(override.Context.Layers) > 0 {
		out.Context = override.Context
	} else {
		out.Context = base.Context
	}

	return out
}

func normalizeWaveStrategy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", WaveStrategySerial:
		return WaveStrategySerial
	case WaveStrategyParallel:
		return WaveStrategyParallel
	default:
		return WaveStrategySerial
	}
}

// EffectiveProvider returns the configured review provider with legacy
// strategy-based fallback preserved for older repos.
func (r ReviewCfg) EffectiveProvider() string {
	if provider := strings.ToLower(strings.TrimSpace(r.Provider)); provider != "" {
		return provider
	}
	if strategy := strings.ToLower(strings.TrimSpace(r.Strategy)); strategy == "external" {
		return "external"
	}
	return "auto"
}

// CrossModelEnabled defaults to true when the field is unset.
func (r ReviewCfg) CrossModelEnabled() bool {
	if r.CrossModel == nil {
		return true
	}
	return *r.CrossModel
}

// PostCommentsEnabled defaults to true when the field is unset.
func (r ReviewCfg) PostCommentsEnabled() bool {
	if r.PostComments == nil {
		return true
	}
	return *r.PostComments
}

// ReviewTimeout resolves the configured review timeout, defaulting to 120s.
func (r ReviewCfg) ReviewTimeout() time.Duration {
	timeout := strings.TrimSpace(r.Timeout)
	if timeout == "" {
		timeout = "120s"
	}
	d, err := time.ParseDuration(timeout)
	if err != nil {
		return 120 * time.Second
	}
	return d
}

// LoadRepoRegistry loads the repo registry from ~/.cobuild/repos.yaml.
func LoadRepoRegistry() (*RepoRegistry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home directory: %w", err)
	}

	path := filepath.Join(home, ".cobuild", "repos.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RepoRegistry{Repos: make(map[string]RepoEntry)}, nil
		}
		return nil, fmt.Errorf("reading repo registry: %w", err)
	}

	var reg RepoRegistry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parsing repo registry: %w", err)
	}
	if reg.Repos == nil {
		reg.Repos = make(map[string]RepoEntry)
	}
	return &reg, nil
}

// SaveRepoRegistry writes the repo registry to ~/.cobuild/repos.yaml.
func SaveRepoRegistry(reg *RepoRegistry) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	dir := filepath.Join(home, ".cobuild")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(reg)
	if err != nil {
		return fmt.Errorf("marshaling repo registry: %w", err)
	}

	path := filepath.Join(dir, "repos.yaml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing repo registry: %w", err)
	}
	return nil
}

// RepoForProject looks up a project in the repo registry and returns its path.
func RepoForProject(project string) (string, error) {
	reg, err := LoadRepoRegistry()
	if err != nil {
		return "", err
	}

	entry, ok := reg.Repos[project]
	if !ok {
		return "", fmt.Errorf("project %q not found in repo registry", project)
	}
	return entry.Path, nil
}

// ResolveSkill finds a skill by name, checking repo then global.
func ResolveSkill(repoRoot, skillName string) (string, error) {
	cfg, err := LoadConfig(repoRoot)
	if err != nil {
		return "", fmt.Errorf("loading pipeline config for skill resolution: %w", err)
	}

	skillsDir := cfg.SkillsDir
	if skillsDir == "" {
		skillsDir = "skills"
	}

	if repoRoot != "" {
		repoSkill := filepath.Join(repoRoot, skillsDir, skillName)
		if _, err := os.Stat(repoSkill); err == nil {
			return repoSkill, nil
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	globalSkill := filepath.Join(home, ".cobuild", "skills", skillName)
	if _, err := os.Stat(globalSkill); err == nil {
		return globalSkill, nil
	}

	return "", fmt.Errorf("skill %q not found in repo (%s) or global (~/.cobuild/skills/)", skillName, filepath.Join(repoRoot, skillsDir))
}

// PhaseNames returns the phase names from config, falling back to ValidPipelinePhases.
func (cfg *Config) PhaseNames() []string {
	if len(cfg.Phases) == 0 {
		return ValidPipelinePhases
	}
	names := make([]string, 0, len(cfg.Phases))
	for name := range cfg.Phases {
		names = append(names, name)
	}
	return names
}

// FindPhase finds a phase by name.
func (cfg *Config) FindPhase(name string) *PhaseConfig {
	if cfg.Phases == nil {
		return nil
	}
	if p, ok := cfg.Phases[name]; ok {
		return &p
	}
	return nil
}

// FindGate finds a gate within a phase.
func (cfg *Config) FindGate(phaseName, gateName string) *GateConfig {
	phase := cfg.FindPhase(phaseName)
	if phase == nil {
		return nil
	}
	for i := range phase.Gates {
		if phase.Gates[i].Name == gateName {
			return &phase.Gates[i]
		}
	}
	return nil
}

// NextPhase returns the next phase name after current, or "" if last.
func (cfg *Config) NextPhase(current string) string {
	names := cfg.PhaseNames()
	for i, name := range names {
		if name == current && i+1 < len(names) {
			return names[i+1]
		}
	}
	return ""
}

// WorkflowForType returns the workflow config for a shard type.
func (cfg *Config) WorkflowForType(shardType string) *WorkflowConfig {
	if cfg.Workflows != nil {
		if wf, ok := cfg.Workflows[shardType]; ok {
			return &wf
		}
		if wf, ok := cfg.Workflows["design"]; ok {
			return &wf
		}
	}
	names := cfg.PhaseNames()
	return &WorkflowConfig{Phases: names}
}

// StartPhaseForType returns the first phase for a shard type's workflow.
func (cfg *Config) StartPhaseForType(shardType string) string {
	wf := cfg.WorkflowForType(shardType)
	if len(wf.Phases) > 0 {
		return wf.Phases[0]
	}
	return "design"
}

// NextPhaseInWorkflow returns the next phase for a specific workflow.
func (cfg *Config) NextPhaseInWorkflow(shardType, current string) string {
	wf := cfg.WorkflowForType(shardType)
	for i, name := range wf.Phases {
		if name == current && i+1 < len(wf.Phases) {
			return wf.Phases[i+1]
		}
	}
	return ""
}

func copyStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func boolPtr(v bool) *bool {
	return &v
}

func copyAgents(m map[string]AgentCfg) map[string]AgentCfg {
	if m == nil {
		return nil
	}
	out := make(map[string]AgentCfg, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyPhases(p map[string]PhaseConfig) map[string]PhaseConfig {
	if p == nil {
		return nil
	}
	out := make(map[string]PhaseConfig, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// copyWorkflows returns a deep copy of a workflows map so the caller can
// mutate it (or MergeConfig can rewrite individual entries) without
// affecting the source — in particular, DefaultConfig()'s hardcoded
// workflows must not leak edits back into the package-level default.
func copyWorkflows(w map[string]WorkflowConfig) map[string]WorkflowConfig {
	if w == nil {
		return nil
	}
	out := make(map[string]WorkflowConfig, len(w))
	for k, v := range w {
		out[k] = WorkflowConfig{
			Phases:        copyStrings(v.Phases),
			ContextLayers: v.ContextLayers,
		}
	}
	return out
}
