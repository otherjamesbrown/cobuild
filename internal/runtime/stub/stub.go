package stub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/runtime"
)

const (
	Name               = "stub"
	DefaultFixturesDir = "internal/e2e/testdata/runtime/stub"
)

type Runtime struct{}

func New() *Runtime { return &Runtime{} }

func init() {
	runtime.Register(New())
}

func (r *Runtime) Name() string { return Name }

func (r *Runtime) ContextFile() string { return "AGENTS.md" }

func (r *Runtime) PreDispatch(_ context.Context, _ string) error { return nil }

func (r *Runtime) WriteSettings(_ string) error { return nil }

func (r *Runtime) BuildRunnerScript(in runtime.RunnerInput) (string, error) {
	if in.WorktreePath == "" {
		return "", fmt.Errorf("BuildRunnerScript: WorktreePath required")
	}
	if in.TaskID == "" {
		return "", fmt.Errorf("BuildRunnerScript: TaskID required")
	}
	if in.RepoRoot == "" {
		return "", fmt.Errorf("BuildRunnerScript: RepoRoot required")
	}
	if in.Phase == "" {
		return "", fmt.Errorf("BuildRunnerScript: Phase required")
	}
	if in.PromptFile == "" {
		return "", fmt.Errorf("BuildRunnerScript: PromptFile required")
	}

	script := fmt.Sprintf(`#!/bin/bash
set -euo pipefail
cd '%s'
export COBUILD_DISPATCH=true
export COBUILD_SESSION_ID='%s'
export COBUILD_HOOKS_DIR='%s'
export COBUILD_TASK_ID='%s'
export COBUILD_REPO_ROOT='%s'
export COBUILD_PHASE='%s'
LOGFILE=".cobuild/dispatch.log"
mkdir -p .cobuild
echo "$COBUILD_PHASE" > .cobuild/phase
echo "$COBUILD_SESSION_ID" > .cobuild/session_id
echo "[$(date)] Dispatch starting (runtime: stub, session: $COBUILD_SESSION_ID, phase: $COBUILD_PHASE)" >> "$LOGFILE"

PROMPT_FILE='%s'
if [ ! -f "$PROMPT_FILE" ]; then
    echo "[$(date)] ERROR: Prompt file not found: $PROMPT_FILE" >> "$LOGFILE"
    exit 1
fi
cp "$PROMPT_FILE" .cobuild/last-prompt.md
rm -f "$PROMPT_FILE"

FIXTURES_DIR="${COBUILD_STUB_FIXTURES_DIR:-$COBUILD_REPO_ROOT/%s}"
echo "[$(date)] Using stub fixtures from $FIXTURES_DIR" >> "$LOGFILE"

cobuild stub-runtime exec \
  --phase "$COBUILD_PHASE" \
  --task-id "$COBUILD_TASK_ID" \
  --worktree "$PWD" \
  --repo-root "$COBUILD_REPO_ROOT" \
  --fixtures-dir "$FIXTURES_DIR" \
  > .cobuild/session.log 2> .cobuild/session.err

echo "[$(date)] Stub runtime finished" >> "$LOGFILE"
rm -f "$0"
`,
		shellQuote(in.WorktreePath),
		in.SessionID,
		shellQuote(in.HooksDir),
		shellQuote(in.TaskID),
		shellQuote(in.RepoRoot),
		shellQuote(in.Phase),
		shellQuote(in.PromptFile),
		DefaultFixturesDir,
	)
	return script, nil
}

func (r *Runtime) ParseSessionStats(sessionLogPath string) (runtime.SessionStats, error) {
	f, err := os.Open(sessionLogPath)
	if err != nil {
		return runtime.SessionStats{}, fmt.Errorf("open %s: %w", sessionLogPath, err)
	}
	defer f.Close()

	stats := runtime.SessionStats{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id,omitempty"`
			Usage    struct {
				InputTokens       int `json:"input_tokens"`
				CachedInputTokens int `json:"cached_input_tokens"`
				OutputTokens      int `json:"output_tokens"`
			} `json:"usage,omitempty"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item,omitempty"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "thread.started":
			if stats.SessionUUID == "" {
				stats.SessionUUID = ev.ThreadID
			}
		case "turn.completed":
			stats.TurnCount++
			stats.InputTokens += ev.Usage.InputTokens
			stats.CachedInputTokens += ev.Usage.CachedInputTokens
			stats.OutputTokens += ev.Usage.OutputTokens
		case "item.completed":
			if ev.Item.Type == "agent_message" && ev.Item.Text != "" {
				stats.LastMessage = ev.Item.Text
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return stats, fmt.Errorf("scan %s: %w", sessionLogPath, err)
	}
	return stats, nil
}

type Fixture struct {
	Phase       string              `json:"phase"`
	TaskID      string              `json:"task_id"`
	GateVerdict *GateVerdictFixture `json:"gate_verdict,omitempty"`
	Implement   *ImplementFixture   `json:"implement,omitempty"`
	Session     SessionFixture      `json:"session,omitempty"`
}

type GateVerdictFixture struct {
	Gate      string `json:"gate"`
	ShardID   string `json:"shard_id"`
	Verdict   string `json:"verdict"`
	Readiness int    `json:"readiness,omitempty"`
	Body      string `json:"body"`
}

type ImplementFixture struct {
	Patch         string `json:"patch,omitempty"`
	PatchFile     string `json:"patch_file,omitempty"`
	CommitMessage string `json:"commit_message"`
}

type SessionFixture struct {
	InputTokens       int    `json:"input_tokens,omitempty"`
	CachedInputTokens int    `json:"cached_input_tokens,omitempty"`
	OutputTokens      int    `json:"output_tokens,omitempty"`
	Turns             int    `json:"turns,omitempty"`
	LastMessage       string `json:"last_message,omitempty"`
}

type LoadedFixture struct {
	Path    string
	Fixture Fixture
}

type ExecInput struct {
	FixturesDir  string
	WorktreePath string
	Phase        string
	TaskID       string
	Stdout       io.Writer
}

type ExecResult struct {
	Fixture     Fixture
	FixturePath string
}

func LoadFixture(fixturesDir, phase, taskID string) (*LoadedFixture, error) {
	if strings.TrimSpace(fixturesDir) == "" {
		return nil, fmt.Errorf("load stub fixture for phase %q task %q: fixtures dir required", phase, taskID)
	}
	path := fixturePath(fixturesDir, phase, taskID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load stub fixture for phase %q task %q: fixture not found at %s", phase, taskID, path)
		}
		return nil, fmt.Errorf("load stub fixture for phase %q task %q: read %s: %w", phase, taskID, path, err)
	}

	var fixture Fixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return nil, fmt.Errorf("load stub fixture for phase %q task %q: parse %s: %w", phase, taskID, path, err)
	}
	if err := validateFixture(fixture, phase, taskID); err != nil {
		return nil, fmt.Errorf("load stub fixture for phase %q task %q: %w", phase, taskID, err)
	}
	if f := fixture.Implement; f != nil && strings.TrimSpace(f.PatchFile) != "" {
		patchPath := f.PatchFile
		if !filepath.IsAbs(patchPath) {
			patchPath = filepath.Join(filepath.Dir(path), patchPath)
		}
		if _, err := os.Stat(patchPath); err != nil {
			return nil, fmt.Errorf("load stub fixture for phase %q task %q: patch file %s: %w", phase, taskID, patchPath, err)
		}
	}
	return &LoadedFixture{Path: path, Fixture: fixture}, nil
}

func Execute(ctx context.Context, in ExecInput) (*ExecResult, error) {
	if in.WorktreePath == "" {
		return nil, fmt.Errorf("execute stub fixture: worktree path required")
	}
	loaded, err := LoadFixture(in.FixturesDir, in.Phase, in.TaskID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(in.WorktreePath, ".cobuild"), 0o755); err != nil {
		return nil, fmt.Errorf("execute stub fixture for phase %q task %q: create .cobuild dir: %w", in.Phase, in.TaskID, err)
	}

	writeSessionEvent(in.Stdout, map[string]any{
		"type":      "thread.started",
		"thread_id": stubThreadID(in.Phase, in.TaskID),
	})
	writeSessionEvent(in.Stdout, map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "agent_message",
			"text": fmt.Sprintf("Loaded stub fixture %s", loaded.Path),
		},
	})

	if runtime.IsGatePhase(in.Phase) {
		if err := writeGateVerdict(in.WorktreePath, *loaded.Fixture.GateVerdict); err != nil {
			return nil, fmt.Errorf("execute stub fixture for phase %q task %q: %w", in.Phase, in.TaskID, err)
		}
	} else {
		if err := applyImplementFixture(ctx, in.WorktreePath, filepath.Dir(loaded.Path), *loaded.Fixture.Implement); err != nil {
			return nil, fmt.Errorf("execute stub fixture for phase %q task %q: %w", in.Phase, in.TaskID, err)
		}
	}

	session := normalizeSession(loaded.Fixture.Session)
	writeSessionEvent(in.Stdout, map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "agent_message",
			"text": session.LastMessage,
		},
	})
	for i := 0; i < session.Turns; i++ {
		writeSessionEvent(in.Stdout, map[string]any{
			"type": "turn.completed",
			"usage": map[string]any{
				"input_tokens":        session.InputTokens,
				"cached_input_tokens": session.CachedInputTokens,
				"output_tokens":       session.OutputTokens,
			},
		})
	}

	return &ExecResult{Fixture: loaded.Fixture, FixturePath: loaded.Path}, nil
}

func writeGateVerdict(worktreePath string, verdict GateVerdictFixture) error {
	path := filepath.Join(worktreePath, ".cobuild", "gate-verdict.json")
	data, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal gate verdict: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func applyImplementFixture(ctx context.Context, worktreePath, fixtureDir string, impl ImplementFixture) error {
	patch, err := resolvePatch(impl, fixtureDir)
	if err != nil {
		return err
	}
	patchFile := filepath.Join(worktreePath, ".cobuild", "stub.patch")
	if err := os.WriteFile(patchFile, []byte(patch), 0o644); err != nil {
		return fmt.Errorf("write patch file: %w", err)
	}
	if err := runGit(ctx, worktreePath, "apply", "--index", "--whitespace=nowarn", patchFile); err != nil {
		return fmt.Errorf("git apply: %w", err)
	}
	if err := runGit(ctx, worktreePath, "commit", "-m", impl.CommitMessage); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

func resolvePatch(impl ImplementFixture, fixtureDir string) (string, error) {
	hasInline := strings.TrimSpace(impl.Patch) != ""
	hasFile := strings.TrimSpace(impl.PatchFile) != ""
	switch {
	case hasInline && hasFile:
		return "", fmt.Errorf("implement fixture must set exactly one of patch or patch_file")
	case !hasInline && !hasFile:
		return "", fmt.Errorf("implement fixture must set patch or patch_file")
	case hasInline:
		return impl.Patch, nil
	default:
		path := impl.PatchFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(fixtureDir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read patch file %s: %w", path, err)
		}
		return string(data), nil
	}
}

func validateFixture(f Fixture, wantPhase, wantTaskID string) error {
	if f.Phase == "" {
		return fmt.Errorf("missing required field %q", "phase")
	}
	if f.TaskID == "" {
		return fmt.Errorf("missing required field %q", "task_id")
	}
	if f.Phase != wantPhase {
		return fmt.Errorf("fixture phase mismatch: got %q", f.Phase)
	}
	if f.TaskID != wantTaskID {
		return fmt.Errorf("fixture task_id mismatch: got %q", f.TaskID)
	}
	if runtime.IsGatePhase(wantPhase) {
		if f.GateVerdict == nil {
			return fmt.Errorf("gate fixture missing gate_verdict")
		}
		if f.Implement != nil {
			return fmt.Errorf("gate fixture must not include implement")
		}
		return validateGateVerdict(*f.GateVerdict)
	}
	if f.Implement == nil {
		return fmt.Errorf("implement fixture missing implement")
	}
	if f.GateVerdict != nil {
		return fmt.Errorf("implement fixture must not include gate_verdict")
	}
	return validateImplementFixture(*f.Implement)
}

func validateGateVerdict(v GateVerdictFixture) error {
	if v.Gate == "" {
		return fmt.Errorf("gate_verdict.gate is required")
	}
	if v.ShardID == "" {
		return fmt.Errorf("gate_verdict.shard_id is required")
	}
	if v.Verdict != "pass" && v.Verdict != "fail" {
		return fmt.Errorf("gate_verdict.verdict must be \"pass\" or \"fail\"")
	}
	if v.Body == "" {
		return fmt.Errorf("gate_verdict.body is required")
	}
	if v.Gate == "readiness-review" && (v.Readiness < 1 || v.Readiness > 5) {
		return fmt.Errorf("gate_verdict.readiness must be 1-5 for readiness-review")
	}
	return nil
}

func validateImplementFixture(v ImplementFixture) error {
	if strings.TrimSpace(v.CommitMessage) == "" {
		return fmt.Errorf("implement.commit_message is required")
	}
	hasInline := strings.TrimSpace(v.Patch) != ""
	hasFile := strings.TrimSpace(v.PatchFile) != ""
	switch {
	case hasInline && hasFile:
		return fmt.Errorf("implement fixture must set exactly one of patch or patch_file")
	case !hasInline && !hasFile:
		return fmt.Errorf("implement fixture must set patch or patch_file")
	}
	return nil
}

func normalizeSession(s SessionFixture) SessionFixture {
	if s.Turns <= 0 {
		s.Turns = 1
	}
	if s.LastMessage == "" {
		s.LastMessage = "stub runtime completed"
	}
	return s
}

func fixturePath(fixturesDir, phase, taskID string) string {
	return filepath.Join(fixturesDir, phase, taskID+".json")
}

func stubThreadID(phase, taskID string) string {
	replacer := strings.NewReplacer("/", "-", " ", "-", "\t", "-")
	return "stub-" + replacer.Replace(phase) + "-" + replacer.Replace(taskID)
}

func shellQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s\n%s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

func writeSessionEvent(w io.Writer, event map[string]any) {
	if w == nil {
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(w, string(data))
}
