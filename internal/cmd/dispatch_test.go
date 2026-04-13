package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
)

func TestWritePhasePromptDecomposeMentionsCompletionModeDirect(t *testing.T) {
	var b strings.Builder
	writePhasePrompt(&b, "decompose", "cb-parent", "cb-parent", nil)
	got := b.String()

	for _, want := range []string{
		"set `completion_mode: direct` only for non-code tasks",
		"leave it unset and let `cobuild complete` auto-detect",
		"`cxp shard metadata set <task-id> completion_mode direct`",
		"tasks tagged `completion_mode: direct`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("decompose prompt missing %q\nprompt:\n%s", want, got)
		}
	}
}

func TestWritePhasePromptDecomposeMentionsMergedWorkDedup(t *testing.T) {
	var b strings.Builder
	writePhasePrompt(&b, "decompose", "cb-parent", "cb-parent", nil)
	got := b.String()

	for _, want := range []string{
		"Do NOT re-create tasks listed in the `Work already merged` section below.",
		"add a `blocked-by` edge to the merged task instead",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("decompose prompt missing %q\nprompt:\n%s", want, got)
		}
	}
}

func TestRenderMergedWorkSectionEmpty(t *testing.T) {
	got := renderMergedWorkSection(nil)

	for _, want := range []string{
		"## Work already merged",
		"None.",
		"Do NOT re-create these tasks.",
		"`cobuild wi links add <new-task> <merged-task> blocked-by`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("merged-work section missing %q\nsection:\n%s", want, got)
		}
	}
}

func TestRenderMergedWorkSectionPopulated(t *testing.T) {
	got := renderMergedWorkSection([]MergedTask{
		{
			TaskID:       "cb-1dbec5",
			CommitSHA:    "abc123def456",
			MergedAt:     time.Date(2026, time.April, 13, 10, 0, 0, 0, time.UTC),
			FilesChanged: []string{"internal/cmd/decompose_context.go", "internal/cmd/decompose_context_test.go"},
		},
	})

	for _, want := range []string{
		"## Work already merged",
		"`cb-1dbec5`",
		"`abc123def456`",
		"internal/cmd/decompose_context.go, internal/cmd/decompose_context_test.go",
		"Do NOT re-create these tasks.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("merged-work section missing %q\nsection:\n%s", want, got)
		}
	}
}

func TestWritePhasePromptImplementAndFixRequireExplicitExit(t *testing.T) {
	tests := []struct {
		phase string
		want  []string
	}{
		{
			phase: "implement",
			want: []string{
				"Implement this task following the acceptance criteria above.",
				"Do this as your LAST action.",
				"After `cobuild complete` finishes, immediately exit the session",
			},
		},
		{
			phase: "fix",
			want: []string{
				"Fix this bug.",
				"Commit — the Stop hook will run `cobuild complete`",
				"IMPORTANT: After the Stop hook completes, immediately exit the session",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.phase, func(t *testing.T) {
			var b strings.Builder
			writePhasePrompt(&b, tt.phase, "cb-task", "cb-task", nil)
			got := b.String()

			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("%s prompt missing %q\nprompt:\n%s", tt.phase, want, got)
				}
			}
		})
	}
}

func TestWritePhasePromptDecomposeEnforcesSingleRepoTasks(t *testing.T) {
	var b strings.Builder
	writePhasePrompt(&b, "decompose", "cb-parent", "cb-parent", nil)
	got := b.String()

	for _, want := range []string{
		"One task = one repo. Never create a task that requires edits in multiple repos.",
		"that is NOT one task",
		"one penfold task with `repo=penfold`",
		"one context-palace task with `repo=context-palace`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("decompose prompt missing %q\nprompt:\n%s", want, got)
		}
	}
}

func TestResolveDispatchTargetRepoRejectsMissingRepoForMultiRepoParentDesign(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(filepath.Join(homeDir, ".cobuild"), 0o755); err != nil {
		t.Fatalf("mkdir home config: %v", err)
	}

	writeTestRepoConfig(t, filepath.Join(tempDir, "cobuild"), "cobuild")
	writeTestRepoConfig(t, filepath.Join(tempDir, "penfold"), "penfold")
	writeTestRepoConfig(t, filepath.Join(tempDir, "context-palace"), "context-palace")

	registry := "repos:\n" +
		"  cobuild:\n" +
		"    path: " + filepath.Join(tempDir, "cobuild") + "\n" +
		"  penfold:\n" +
		"    path: " + filepath.Join(tempDir, "penfold") + "\n" +
		"  context-palace:\n" +
		"    path: " + filepath.Join(tempDir, "context-palace") + "\n"
	if err := os.WriteFile(filepath.Join(homeDir, ".cobuild", "repos.yaml"), []byte(registry), 0o644); err != nil {
		t.Fatalf("write repo registry: %v", err)
	}

	prevHome := os.Getenv("HOME")
	prevConn := conn
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	conn = newDispatchWaveTestConnector(
		dispatchTestItem("task-1", "task", "open", "Dispatch task", "", nil),
		dispatchTestItem("design-1", "design", "open", "Multi-repo design", "Edit penfold/internal/session_hook.go and context-palace/CLAUDE.md in the same rollout.", nil),
	)
	t.Cleanup(func() {
		_ = os.Setenv("HOME", prevHome)
		conn = prevConn
	})

	testConn := conn.(*dispatchWaveTestConnector)
	testConn.edgesByItem["task-1"] = []connector.Edge{
		{Direction: "outgoing", EdgeType: "child-of", ItemID: "design-1", Status: "open"},
	}

	_, err := resolveDispatchTargetRepo(context.Background(), testConn.items["task-1"], "task-1", "cobuild", io.Discard)
	if err == nil {
		t.Fatal("expected missing repo metadata error, got nil")
	}
	for _, want := range []string{
		"missing `repo` metadata",
		"parent design design-1 references multiple repos",
		"context-palace, penfold",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
}

func TestResolveDispatchTargetRepoRejectsUnknownRepoMetadata(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(filepath.Join(homeDir, ".cobuild"), 0o755); err != nil {
		t.Fatalf("mkdir home config: %v", err)
	}

	writeTestRepoConfig(t, filepath.Join(tempDir, "cobuild"), "cobuild")
	registry := "repos:\n" +
		"  cobuild:\n" +
		"    path: " + filepath.Join(tempDir, "cobuild") + "\n"
	if err := os.WriteFile(filepath.Join(homeDir, ".cobuild", "repos.yaml"), []byte(registry), 0o644); err != nil {
		t.Fatalf("write repo registry: %v", err)
	}

	prevHome := os.Getenv("HOME")
	prevConn := conn
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	conn = newDispatchWaveTestConnector(
		dispatchTestItem("task-1", "task", "open", "Dispatch task", "", map[string]any{"repo": "missing-repo"}),
	)
	t.Cleanup(func() {
		_ = os.Setenv("HOME", prevHome)
		conn = prevConn
	})

	_, err := resolveDispatchTargetRepo(context.Background(), conn.(*dispatchWaveTestConnector).items["task-1"], "task-1", "cobuild", io.Discard)
	if err == nil {
		t.Fatal("expected invalid repo error, got nil")
	}
	if !strings.Contains(err.Error(), `task specifies repo "missing-repo" but it's not in the registry`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHasInvestigationContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "empty body",
			content: "",
			want:    false,
		},
		{
			name:    "plain bug report, no investigation",
			content: "## Description\n\nServer crashes on startup.\n\n## Steps to Reproduce\n\n1. Run server\n2. Observe crash",
			want:    false,
		},
		{
			name:    "has investigation report heading",
			content: "## Description\n\nServer crashes.\n\n## Investigation Report\n\nFound null pointer in auth middleware.",
			want:    true,
		},
		{
			name:    "has root cause heading",
			content: "## Description\n\nServer crashes.\n\n## Root Cause\n\nMissing nil check in auth.go:42.",
			want:    true,
		},
		{
			name:    "has fix applied heading",
			content: "## Description\n\nServer crashes.\n\n## Fix Applied\n\nAdded nil check.",
			want:    true,
		},
		{
			name:    "has fix heading",
			content: "## Description\n\nServer crashes.\n\n## Fix\n\n- [ ] Add nil check in auth.go",
			want:    true,
		},
		{
			name:    "case insensitive - uppercase",
			content: "## INVESTIGATION REPORT\n\nFound the issue.",
			want:    true,
		},
		{
			name:    "case insensitive - mixed",
			content: "## Root Cause\n\nThe bug is here.",
			want:    true,
		},
		{
			name:    "heading in prose, not heading level",
			content: "The investigation report showed nothing useful here.",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasInvestigationContent(tt.content)
			if got != tt.want {
				t.Errorf("hasInvestigationContent(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestInvestigationContentDowngrade(t *testing.T) {
	// These test the 4 combinations of label × investigation content.
	// The dispatch logic is: if label=needs-investigation → investigate, else → fix.
	// Then: if phase=investigate AND hasInvestigationContent → downgrade to fix.

	type input struct {
		hasNeedsInvestigationLabel bool
		hasInvestigationBody       bool
	}
	type want struct {
		phase string
	}

	tests := []struct {
		name  string
		input input
		want  want
	}{
		{
			name:  "label=false, investigation=false → fix (normal bug)",
			input: input{false, false},
			want:  want{"fix"},
		},
		{
			name:  "label=true, investigation=false → investigate (escalation path)",
			input: input{true, false},
			want:  want{"investigate"},
		},
		{
			name:  "label=false, investigation=true → fix (already investigated, default path)",
			input: input{false, true},
			want:  want{"fix"},
		},
		{
			name:  "label=true, investigation=true → fix (downgrade: body overrides label)",
			input: input{true, true},
			want:  want{"fix"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the dispatch.go phase inference + downgrade logic
			labels := []string{}
			if tt.input.hasNeedsInvestigationLabel {
				labels = []string{"needs-investigation"}
			}

			content := "## Description\n\nSome bug."
			if tt.input.hasInvestigationBody {
				content += "\n\n## Investigation Report\n\nFound the root cause."
			}

			// Phase inference (mirrors dispatch.go fallback logic)
			phase := "fix"
			if hasLabel(labels, "needs-investigation") {
				phase = "investigate"
			}

			// Downgrade (mirrors dispatch.go post-inference check)
			if phase == "investigate" && hasInvestigationContent(content) {
				phase = "fix"
			}

			if phase != tt.want.phase {
				t.Errorf("phase = %q, want %q", phase, tt.want.phase)
			}
		})
	}
}

func TestDispatchDryRunUsesTaskRepoMetadataForWorktreeTargeting(t *testing.T) {
	cpRepo, pfRepo := setupDispatchRepoRegistry(t)

	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-task-pf"] = &store.PipelineRun{
		ID:           "run-1",
		DesignID:     "cb-task-pf",
		CurrentPhase: "implement",
		Status:       "active",
	}

	fc.addItem(&connector.WorkItem{
		ID:      "cb-design",
		Title:   "Cross-repo design",
		Type:    "design",
		Status:  "open",
		Content: "Edit context-palace dispatch code and penfold worker code in separate tasks.",
	})
	fc.addItem(&connector.WorkItem{
		ID:       "cb-task-pf",
		Title:    "Penfold-only task",
		Type:     "task",
		Status:   "open",
		Content:  "Change only penfold files.",
		Metadata: map[string]any{"repo": "penfold"},
	})
	fc.parent["cb-task-pf"] = "cb-design"

	restore := installTestGlobals(t, fc, fs, "context-palace")
	defer restore()

	_ = dispatchCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		_ = dispatchCmd.Flags().Set("dry-run", "false")
	})

	out := captureStdout(t, func() {
		if err := dispatchCmd.RunE(dispatchCmd, []string{"cb-task-pf"}); err != nil {
			t.Fatalf("dispatch returned error: %v", err)
		}
	})

	assertContains(t, out, "Target repo: "+pfRepo+" (from task metadata: repo=penfold)")
	assertContains(t, out, "[dry-run] Would create worktree for cb-task-pf in "+pfRepo)
	assertNotContains(t, out, cpRepo+" (from project: context-palace)")
}

func TestDispatchDryRunReviewUsesReviewSkillAndWindowPrefix(t *testing.T) {
	repoRoot := t.TempDir()
	homeDir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(homeDir, ".cobuild"), 0o755); err != nil {
		t.Fatalf("mkdir home config: %v", err)
	}
	writeTestRepoConfig(t, repoRoot, "cobuild")
	if err := os.WriteFile(filepath.Join(repoRoot, "CLAUDE.md"), []byte("# Test\n"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, ".cobuild"), 0o755); err != nil {
		t.Fatalf("mkdir repo .cobuild: %v", err)
	}
	pipelineYAML := "" +
		"skills_dir: skills\n" +
		"phases:\n" +
		"  review:\n" +
		"    gate: review\n" +
		"    skill: review/dispatch-review.md\n"
	if err := os.WriteFile(filepath.Join(repoRoot, ".cobuild", "pipeline.yaml"), []byte(pipelineYAML), 0o644); err != nil {
		t.Fatalf("write pipeline config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "skills", "review"), 0o755); err != nil {
		t.Fatalf("mkdir review skills dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "skills", "review", "dispatch-review.md"), []byte("# review skill\n"), 0o644); err != nil {
		t.Fatalf("write review skill: %v", err)
	}
	prevHome := os.Getenv("HOME")
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := config.SaveRepoRegistry(&config.RepoRegistry{
		Repos: map[string]config.RepoEntry{
			"cobuild": {Path: repoRoot},
		},
	}); err != nil {
		t.Fatalf("save repo registry: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", prevHome)
		_ = os.Chdir(prevWD)
	})

	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-task-review"] = &store.PipelineRun{
		ID:           "run-review",
		DesignID:     "cb-task-review",
		CurrentPhase: "review",
		Status:       "active",
	}
	fc.addItem(&connector.WorkItem{
		ID:      "cb-design",
		Title:   "Review parent design",
		Type:    "design",
		Status:  "open",
		Content: "Parent design context.",
	})
	fc.addItem(&connector.WorkItem{
		ID:      "cb-task-review",
		Title:   "Review shard",
		Type:    "task",
		Status:  "open",
		Content: "Review this implementation PR.",
	})
	fc.parent["cb-task-review"] = "cb-design"

	restore := installTestGlobals(t, fc, fs, "cobuild")
	defer restore()

	_ = dispatchCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		_ = dispatchCmd.Flags().Set("dry-run", "false")
	})

	out := captureStdout(t, func() {
		if err := dispatchCmd.RunE(dispatchCmd, []string{"cb-task-review"}); err != nil {
			t.Fatalf("dispatch returned error: %v", err)
		}
	})

	assertContains(t, out, "Follow the configured review skill `review/dispatch-review.md`:")
	assertContains(t, out, "tmux new-window -n review-cb-task-review")
}

func TestDispatchRefusesMissingRepoMetadataWhenParentDesignReferencesMultipleRepos(t *testing.T) {
	_, _ = setupDispatchRepoRegistry(t)

	fc := newFakeConnector()
	fs := newFakeStore()
	fs.runs["cb-task-missing"] = &store.PipelineRun{
		ID:           "run-2",
		DesignID:     "cb-task-missing",
		CurrentPhase: "implement",
		Status:       "active",
	}

	fc.addItem(&connector.WorkItem{
		ID:      "cb-design",
		Title:   "Cross-repo design",
		Type:    "design",
		Status:  "open",
		Content: "This work spans context-palace and penfold. Split it into one task per repo.",
	})
	fc.addItem(&connector.WorkItem{
		ID:      "cb-task-missing",
		Title:   "Unsafe task",
		Type:    "task",
		Status:  "open",
		Content: "Do not let this default to the wrong checkout.",
	})
	fc.parent["cb-task-missing"] = "cb-design"

	restore := installTestGlobals(t, fc, fs, "context-palace")
	defer restore()

	_ = dispatchCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		_ = dispatchCmd.Flags().Set("dry-run", "false")
	})

	err := dispatchCmd.RunE(dispatchCmd, []string{"cb-task-missing"})
	if err == nil {
		t.Fatal("dispatch returned nil error, want unsafe targeting refusal")
	}

	msg := err.Error()
	for _, want := range []string{
		"cb-task-missing is missing `repo` metadata",
		"parent design cb-design references multiple repos",
		"context-palace, penfold",
		"set `repo` metadata before dispatching",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error missing %q:\n%s", want, msg)
		}
	}
}

func TestDispatchWaveSerialOnlyDispatchesLowestEligibleWave(t *testing.T) {
	testDir := setupDispatchWaveTestRepo(t, "dispatch:\n  wave_strategy: serial\n  max_concurrent: 3\n")

	prevConn := conn
	prevClient := cbClient
	prevProject := projectName
	conn = newDispatchWaveTestConnector(
		dispatchWaveTestItem("design-1", "design", "open", nil),
		dispatchWaveTestItem("task-1", "task", "open", map[string]any{"wave": 1}),
		dispatchWaveTestItem("task-2", "task", "open", map[string]any{"wave": 1}),
		dispatchWaveTestItem("task-3", "task", "open", map[string]any{"wave": 2}),
	)
	cbClient = &client.Client{}
	projectName = "cb-test"
	t.Cleanup(func() {
		conn = prevConn
		cbClient = prevClient
		projectName = prevProject
	})

	testConn := conn.(*dispatchWaveTestConnector)
	testConn.edgesByItem["design-1"] = []connector.Edge{
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-1", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-2", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-3", Status: "open"},
	}

	output := captureStdout(t, func() {
		if err := dispatchWaveCmd.RunE(dispatchWaveCmd, []string{"design-1"}); err != nil {
			t.Fatalf("dispatch-wave failed: %v", err)
		}
	})

	assertContains(t, output, "[dry-run] task-1")
	assertContains(t, output, "[dry-run] task-2")
	assertNotContains(t, output, "[dry-run] task-3")
	_ = testDir
}

func setupDispatchRepoRegistry(t *testing.T) (string, string) {
	t.Helper()

	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	cpRepo := filepath.Join(tempDir, "context-palace")
	pfRepo := filepath.Join(tempDir, "penfold")

	for _, dir := range []string{homeDir, cpRepo, pfRepo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(cpRepo, ".cobuild.yaml"), []byte("project: context-palace\nprefix: cp-\n"), 0o644); err != nil {
		t.Fatalf("write context-palace config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pfRepo, ".cobuild.yaml"), []byte("project: penfold\nprefix: pf-\n"), 0o644); err != nil {
		t.Fatalf("write penfold config: %v", err)
	}

	prevHome := os.Getenv("HOME")
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if err := os.Chdir(cpRepo); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", prevHome)
		_ = os.Chdir(prevWD)
	})

	if err := config.SaveRepoRegistry(&config.RepoRegistry{
		Repos: map[string]config.RepoEntry{
			"context-palace": {Path: cpRepo},
			"penfold":        {Path: pfRepo},
		},
	}); err != nil {
		t.Fatalf("save repo registry: %v", err)
	}

	return cpRepo, pfRepo
}

func TestDispatchWaveParallelKeepsMultiWaveDispatch(t *testing.T) {
	setupDispatchWaveTestRepo(t, "dispatch:\n  wave_strategy: parallel\n  max_concurrent: 3\n")

	prevConn := conn
	prevClient := cbClient
	prevProject := projectName
	conn = newDispatchWaveTestConnector(
		dispatchWaveTestItem("design-1", "design", "open", nil),
		dispatchWaveTestItem("task-1", "task", "open", map[string]any{"wave": 1}),
		dispatchWaveTestItem("task-2", "task", "open", map[string]any{"wave": 2}),
		dispatchWaveTestItem("task-3", "task", "open", map[string]any{"wave": 3}),
	)
	cbClient = &client.Client{}
	projectName = "cb-test"
	t.Cleanup(func() {
		conn = prevConn
		cbClient = prevClient
		projectName = prevProject
	})

	testConn := conn.(*dispatchWaveTestConnector)
	testConn.edgesByItem["design-1"] = []connector.Edge{
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-1", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-2", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-3", Status: "open"},
	}

	output := captureStdout(t, func() {
		if err := dispatchWaveCmd.RunE(dispatchWaveCmd, []string{"design-1"}); err != nil {
			t.Fatalf("dispatch-wave failed: %v", err)
		}
	})

	assertContains(t, output, "[dry-run] task-1")
	assertContains(t, output, "[dry-run] task-2")
	assertContains(t, output, "[dry-run] task-3")
}

func TestDispatchWaveAppliesConcurrencyAfterWaveSelection(t *testing.T) {
	setupDispatchWaveTestRepo(t, "dispatch:\n  wave_strategy: serial\n  max_concurrent: 1\n")

	prevConn := conn
	prevClient := cbClient
	prevProject := projectName
	conn = newDispatchWaveTestConnector(
		dispatchWaveTestItem("design-1", "design", "open", nil),
		dispatchWaveTestItem("task-1", "task", "open", map[string]any{"wave": 1}),
		dispatchWaveTestItem("task-2", "task", "open", map[string]any{"wave": 1}),
		dispatchWaveTestItem("task-3", "task", "open", map[string]any{"wave": 2}),
	)
	cbClient = &client.Client{}
	projectName = "cb-test"
	t.Cleanup(func() {
		conn = prevConn
		cbClient = prevClient
		projectName = prevProject
	})

	testConn := conn.(*dispatchWaveTestConnector)
	testConn.edgesByItem["design-1"] = []connector.Edge{
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-1", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-2", Status: "open"},
		{Direction: "incoming", EdgeType: "child-of", ItemID: "task-3", Status: "open"},
	}

	output := captureStdout(t, func() {
		if err := dispatchWaveCmd.RunE(dispatchWaveCmd, []string{"design-1"}); err != nil {
			t.Fatalf("dispatch-wave failed: %v", err)
		}
	})

	assertContains(t, output, "Dispatching 1 tasks")
	assertContains(t, output, "[dry-run] task-1")
	assertNotContains(t, output, "[dry-run] task-2")
	assertNotContains(t, output, "[dry-run] task-3")
}

type dispatchWaveTestConnector struct {
	items       map[string]*connector.WorkItem
	edgesByItem map[string][]connector.Edge
}

func newDispatchWaveTestConnector(items ...*connector.WorkItem) *dispatchWaveTestConnector {
	index := make(map[string]*connector.WorkItem, len(items))
	for _, item := range items {
		index[item.ID] = item
	}
	return &dispatchWaveTestConnector{
		items:       index,
		edgesByItem: make(map[string][]connector.Edge),
	}
}

func (c *dispatchWaveTestConnector) Name() string { return "test" }

func (c *dispatchWaveTestConnector) Get(ctx context.Context, id string) (*connector.WorkItem, error) {
	item, ok := c.items[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return item, nil
}

func (c *dispatchWaveTestConnector) List(ctx context.Context, filters connector.ListFilters) (*connector.ListResult, error) {
	return nil, nil
}

func (c *dispatchWaveTestConnector) GetEdges(ctx context.Context, id string, direction string, types []string) ([]connector.Edge, error) {
	edges := c.edgesByItem[id]
	if len(edges) == 0 {
		return nil, nil
	}
	filtered := make([]connector.Edge, 0, len(edges))
	for _, edge := range edges {
		if direction != "" && edge.Direction != direction {
			continue
		}
		if len(types) > 0 && !containsString(types, edge.EdgeType) {
			continue
		}
		filtered = append(filtered, edge)
	}
	return filtered, nil
}

func (c *dispatchWaveTestConnector) GetMetadata(ctx context.Context, id string, key string) (string, error) {
	item, ok := c.items[id]
	if !ok {
		return "", os.ErrNotExist
	}
	return metadataString(item.Metadata, key), nil
}

func (c *dispatchWaveTestConnector) Create(ctx context.Context, req connector.CreateRequest) (string, error) {
	return "", nil
}

func (c *dispatchWaveTestConnector) UpdateStatus(ctx context.Context, id string, status string) error {
	return nil
}

func (c *dispatchWaveTestConnector) AppendContent(ctx context.Context, id string, content string) error {
	return nil
}

func (c *dispatchWaveTestConnector) SetMetadata(ctx context.Context, id string, key string, value any) error {
	return nil
}

func (c *dispatchWaveTestConnector) UpdateMetadataMap(ctx context.Context, id string, patch map[string]any) error {
	return nil
}

func (c *dispatchWaveTestConnector) AddLabel(ctx context.Context, id string, label string) error {
	return nil
}

func (c *dispatchWaveTestConnector) CreateEdge(ctx context.Context, fromID string, toID string, edgeType string) error {
	return nil
}

func dispatchWaveTestItem(id, itemType, status string, metadata map[string]any) *connector.WorkItem {
	return &connector.WorkItem{
		ID:       id,
		Type:     itemType,
		Status:   status,
		Metadata: metadata,
	}
}

func dispatchTestItem(id, itemType, status, title, content string, metadata map[string]any) *connector.WorkItem {
	return &connector.WorkItem{
		ID:       id,
		Type:     itemType,
		Status:   status,
		Title:    title,
		Content:  content,
		Metadata: metadata,
	}
}

func writeTestRepoConfig(t *testing.T, repoRoot, project string) {
	t.Helper()
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	body := "project: " + project + "\nprefix: " + project + "-\n"
	if err := os.WriteFile(filepath.Join(repoRoot, ".cobuild.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
}

func setupDispatchWaveTestRepo(t *testing.T, pipelineConfig string) string {
	t.Helper()

	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(filepath.Join(homeDir, ".cobuild"), 0o755); err != nil {
		t.Fatalf("mkdir home config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tempDir, ".cobuild"), 0o755); err != nil {
		t.Fatalf("mkdir repo config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, ".cobuild.yaml"), []byte("project: cb-test\nprefix: cb-\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, ".cobuild", "pipeline.yaml"), []byte(pipelineConfig), 0o644); err != nil {
		t.Fatalf("write pipeline config: %v", err)
	}
	if err := exec.Command("git", "init", "-q", tempDir).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	prevHome := os.Getenv("HOME")
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.Setenv("HOME", homeDir); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	dispatchWaveCmd.Flags().Set("dry-run", "true")
	t.Cleanup(func() {
		_ = dispatchWaveCmd.Flags().Set("dry-run", "false")
		_ = os.Chdir(prevWD)
		_ = os.Setenv("HOME", prevHome)
	})

	return tempDir
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()
	os.Stdout = origStdout
	return <-done
}

func assertContains(t *testing.T, output, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Fatalf("expected output to contain %q, got:\n%s", want, output)
	}
}

func assertNotContains(t *testing.T, output, want string) {
	t.Helper()
	if strings.Contains(output, want) {
		t.Fatalf("expected output not to contain %q, got:\n%s", want, output)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
