package stub

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/otherjamesbrown/cobuild/internal/runtime"
)

func TestRegistered(t *testing.T) {
	rt, err := runtime.Get(Name)
	if err != nil {
		t.Fatalf("runtime.Get(%s): %v", Name, err)
	}
	if rt.Name() != Name {
		t.Fatalf("registered runtime name = %q, want %q", rt.Name(), Name)
	}
}

func TestLoadFixture_FromTestdata(t *testing.T) {
	fixturesDir := filepath.Join("..", "..", "e2e", "testdata", "runtime", "stub")
	loaded, err := LoadFixture(fixturesDir, "design", "cb-design-pass")
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if loaded.Fixture.GateVerdict == nil {
		t.Fatal("expected gate fixture")
	}
	if got := loaded.Fixture.GateVerdict.Gate; got != "readiness-review" {
		t.Fatalf("gate = %q, want readiness-review", got)
	}
}

func TestLoadFixture_MissingFixture(t *testing.T) {
	_, err := LoadFixture(t.TempDir(), "implement", "cb-missing")
	if err == nil {
		t.Fatal("expected missing fixture error")
	}
	if !strings.Contains(err.Error(), `phase "implement" task "cb-missing"`) {
		t.Fatalf("error = %v, want phase/task context", err)
	}
}

func TestLoadFixture_MalformedFixture(t *testing.T) {
	fixturesDir := t.TempDir()
	path := filepath.Join(fixturesDir, "implement", "cb-bad.json")
	mkdirWrite(t, path, `{"phase":"implement","task_id":"cb-bad","implement":{"commit_message":42}}`)

	_, err := LoadFixture(fixturesDir, "implement", "cb-bad")
	if err == nil {
		t.Fatal("expected malformed fixture error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error = %v, want parse context", err)
	}
}

func TestLoadFixture_RejectsPhaseMismatch(t *testing.T) {
	fixturesDir := t.TempDir()
	path := filepath.Join(fixturesDir, "design", "cb-phase.json")
	mkdirWrite(t, path, `{"phase":"implement","task_id":"cb-phase","implement":{"patch":"diff --git a/a.txt b/a.txt\n","commit_message":"x"}}`)

	_, err := LoadFixture(fixturesDir, "design", "cb-phase")
	if err == nil {
		t.Fatal("expected phase mismatch error")
	}
	if !strings.Contains(err.Error(), "fixture phase mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteGateFixtureWritesVerdict(t *testing.T) {
	worktree := t.TempDir()
	fixturesDir := filepath.Join("..", "..", "e2e", "testdata", "runtime", "stub")

	res, err := Execute(context.Background(), ExecInput{
		FixturesDir:  fixturesDir,
		WorktreePath: worktree,
		Phase:        "design",
		TaskID:       "cb-design-pass",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Fixture.Phase; got != "design" {
		t.Fatalf("phase = %q, want design", got)
	}
	data, err := os.ReadFile(filepath.Join(worktree, ".cobuild", "gate-verdict.json"))
	if err != nil {
		t.Fatalf("read gate verdict: %v", err)
	}
	if !strings.Contains(string(data), `"gate": "readiness-review"`) {
		t.Fatalf("gate verdict = %s", data)
	}
}

func TestBuildRunnerScript_Shape(t *testing.T) {
	script, err := New().BuildRunnerScript(runtime.RunnerInput{
		WorktreePath: "/tmp/wt",
		RepoRoot:     "/tmp/repo",
		TaskID:       "cb-stub",
		PromptFile:   "/tmp/prompt.md",
		SessionID:    "sess-1",
		HooksDir:     "/tmp/repo/hooks",
		Phase:        "implement",
	})
	if err != nil {
		t.Fatalf("BuildRunnerScript: %v", err)
	}
	for _, want := range []string{
		"cobuild stub-runtime exec",
		"--task-id \"$COBUILD_TASK_ID\"",
		"--fixtures-dir \"$FIXTURES_DIR\"",
		".cobuild/session.log",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q\n%s", want, script)
		}
	}
}

func TestExecuteImplementFixtureAppliesPatchAndCommits(t *testing.T) {
	repo := initGitRepo(t)
	fixturesDir := filepath.Join("..", "..", "e2e", "testdata", "runtime", "stub")

	_, err := Execute(context.Background(), ExecInput{
		FixturesDir:  fixturesDir,
		WorktreePath: repo,
		Phase:        "implement",
		TaskID:       "cb-implement-add-file",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repo, "runtime.txt"))
	if err != nil {
		t.Fatalf("read runtime.txt: %v", err)
	}
	if got := string(data); got != "stub runtime says hello\n" {
		t.Fatalf("runtime.txt = %q", got)
	}

	log := gitOutput(t, repo, "log", "--oneline", "-1")
	if !strings.Contains(log, "[cb-implement-add-file] apply stub patch") {
		t.Fatalf("last commit = %q", log)
	}
}

func TestParseSessionStats(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.log")
	mkdirWrite(t, logPath, strings.Join([]string{
		`{"type":"thread.started","thread_id":"stub-implement-cb-1"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"loaded"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":3,"cached_input_tokens":1,"output_tokens":2}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`,
		"",
	}, "\n"))

	stats, err := New().ParseSessionStats(logPath)
	if err != nil {
		t.Fatalf("ParseSessionStats: %v", err)
	}
	if stats.SessionUUID != "stub-implement-cb-1" {
		t.Fatalf("SessionUUID = %q", stats.SessionUUID)
	}
	if stats.TurnCount != 1 || stats.InputTokens != 3 || stats.CachedInputTokens != 1 || stats.OutputTokens != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if stats.LastMessage != "done" {
		t.Fatalf("LastMessage = %q, want done", stats.LastMessage)
	}
}

func mkdirWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitTest(t, repo, "init")
	runGitTest(t, repo, "config", "user.email", "test@example.com")
	runGitTest(t, repo, "config", "user.name", "Test User")
	mkdirWrite(t, filepath.Join(repo, "README.md"), "base\n")
	runGitTest(t, repo, "add", "README.md")
	runGitTest(t, repo, "commit", "-m", "initial")
	return repo
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := runGit(context.Background(), dir, args...); err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}
