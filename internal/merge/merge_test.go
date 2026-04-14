package merge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newTestRepo creates a minimal git repo in a temp dir with one initial
// commit on main and returns the absolute path. All tests using this
// helper stay hermetic — no network, no postgres, no tmux (cb-383574).
func newTestRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "merge-test@example.com")
	runGit(t, dir, "config", "user.name", "merge-test")
	runGit(t, dir, "config", "commit.gpgsign", "false")

	writeFile(t, dir, "README.md", "base\n")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-q", "-m", "base")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// makeBranch creates a branch from main, writes the given files, and
// commits. Leaves the repo on main after returning.
func makeBranch(t *testing.T, dir, branch string, files map[string]string) {
	t.Helper()
	runGit(t, dir, "checkout", "-q", "-b", branch, "main")
	for p, c := range files {
		writeFile(t, dir, p, c)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", branch+" change")
	runGit(t, dir, "checkout", "-q", "main")
}

// -----------------------------------------------------------------------
// GeneratePlan — pure function, no git required
// -----------------------------------------------------------------------

func TestGeneratePlan_SingleWave_AllMerge(t *testing.T) {
	cm := &ConflictMap{
		Branches: []BranchInfo{
			{TaskID: "t1", Branch: "t1", Wave: 1, Files: []string{"a.go"}},
			{TaskID: "t2", Branch: "t2", Wave: 1, Files: []string{"b.go"}},
		},
		Clean: true,
	}
	sr := &SupersessionResult{PartiallySuperseded: map[string]SkipInfo{}}

	plan := GeneratePlan("design-1", cm, sr)

	if plan.Summary.Total != 2 || plan.Summary.Merge != 2 || plan.Summary.Skip != 0 {
		t.Fatalf("summary = %+v, want 2 merge/0 skip", plan.Summary)
	}
	if plan.Summary.Waves != 1 {
		t.Fatalf("waves = %d, want 1", plan.Summary.Waves)
	}
	for _, e := range plan.Entries {
		if e.Action != ActionMerge {
			t.Errorf("%s action = %s, want merge", e.TaskID, e.Action)
		}
	}
}

func TestGeneratePlan_MultiWave_OrderedByWave(t *testing.T) {
	cm := &ConflictMap{
		Branches: []BranchInfo{
			{TaskID: "late", Branch: "late", Wave: 3, Files: []string{"z.go"}},
			{TaskID: "early", Branch: "early", Wave: 1, Files: []string{"a.go"}},
			{TaskID: "mid", Branch: "mid", Wave: 2, Files: []string{"m.go"}},
		},
	}
	sr := &SupersessionResult{PartiallySuperseded: map[string]SkipInfo{}}

	plan := GeneratePlan("d", cm, sr)

	if len(plan.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(plan.Entries))
	}
	want := []string{"early", "mid", "late"}
	for i, e := range plan.Entries {
		if e.TaskID != want[i] {
			t.Errorf("entry[%d] = %s, want %s", i, e.TaskID, want[i])
		}
	}
	if plan.Summary.Waves != 3 {
		t.Errorf("waves = %d, want 3", plan.Summary.Waves)
	}
}

func TestGeneratePlan_FullySuperseded_Skips(t *testing.T) {
	cm := &ConflictMap{
		Branches: []BranchInfo{
			{TaskID: "small", Branch: "small", Wave: 1, Files: []string{"x.go"}},
			{TaskID: "big", Branch: "big", Wave: 1, Files: []string{"x.go"}},
		},
	}
	sr := &SupersessionResult{
		Supersessions: []Supersession{
			{SupersededTask: "small", SupersedingTask: "big", File: "x.go"},
		},
		FullySuperseded:     []string{"small"},
		PartiallySuperseded: map[string]SkipInfo{},
	}

	plan := GeneratePlan("d", cm, sr)

	var smallEntry *MergePlanEntry
	for i := range plan.Entries {
		if plan.Entries[i].TaskID == "small" {
			smallEntry = &plan.Entries[i]
		}
	}
	if smallEntry == nil {
		t.Fatalf("small not in plan")
	}
	if smallEntry.Action != ActionSkip {
		t.Errorf("action = %s, want skip", smallEntry.Action)
	}
	if !strings.Contains(smallEntry.Note, "big") {
		t.Errorf("note = %q, want mention of 'big'", smallEntry.Note)
	}
	if plan.Summary.Skip != 1 || plan.Summary.Merge != 1 {
		t.Errorf("summary = %+v, want 1 skip / 1 merge", plan.Summary)
	}
}

func TestGeneratePlan_PartiallySuperseded_PartialMerge(t *testing.T) {
	cm := &ConflictMap{
		Branches: []BranchInfo{
			{TaskID: "mix", Branch: "mix", Wave: 1, Files: []string{"a.go", "b.go"}},
			{TaskID: "big", Branch: "big", Wave: 1, Files: []string{"a.go"}},
		},
	}
	sr := &SupersessionResult{
		PartiallySuperseded: map[string]SkipInfo{
			"mix": {
				SkipFiles:    []string{"a.go"},
				IncludeFiles: []string{"b.go"},
				SupersedBy:   "big",
			},
		},
	}

	plan := GeneratePlan("d", cm, sr)

	var mix *MergePlanEntry
	for i := range plan.Entries {
		if plan.Entries[i].TaskID == "mix" {
			mix = &plan.Entries[i]
		}
	}
	if mix == nil || mix.Action != ActionPartialMerge {
		t.Fatalf("mix action = %v, want partial_merge", mix)
	}
	if len(mix.IncludeFiles) != 1 || mix.IncludeFiles[0] != "b.go" {
		t.Errorf("IncludeFiles = %v, want [b.go]", mix.IncludeFiles)
	}
	if len(mix.SkipFiles) != 1 || mix.SkipFiles[0] != "a.go" {
		t.Errorf("SkipFiles = %v, want [a.go]", mix.SkipFiles)
	}
	if plan.Summary.Partial != 1 {
		t.Errorf("summary.Partial = %d, want 1", plan.Summary.Partial)
	}
}

func TestGeneratePlan_WithinWave_OrdersByFewestConflicts(t *testing.T) {
	// two tasks in wave 1, heavy has 2 conflicts, light has 0 → light first.
	cm := &ConflictMap{
		Branches: []BranchInfo{
			{TaskID: "heavy", Branch: "heavy", Wave: 1, Files: []string{"a.go", "b.go"}},
			{TaskID: "light", Branch: "light", Wave: 1, Files: []string{"c.go"}},
			{TaskID: "other1", Branch: "other1", Wave: 1, Files: []string{"a.go"}},
			{TaskID: "other2", Branch: "other2", Wave: 1, Files: []string{"b.go"}},
		},
		Conflicts: []FileConflict{
			{File: "a.go", Branches: []string{"heavy", "other1"}, SameWave: true},
			{File: "b.go", Branches: []string{"heavy", "other2"}, SameWave: true},
		},
	}
	sr := &SupersessionResult{PartiallySuperseded: map[string]SkipInfo{}}

	plan := GeneratePlan("d", cm, sr)

	// light (0 conflicts) should come before heavy (2 conflicts).
	lightIdx, heavyIdx := -1, -1
	for i, e := range plan.Entries {
		if e.TaskID == "light" {
			lightIdx = i
		}
		if e.TaskID == "heavy" {
			heavyIdx = i
		}
	}
	if lightIdx == -1 || heavyIdx == -1 {
		t.Fatalf("missing entries: lightIdx=%d heavyIdx=%d", lightIdx, heavyIdx)
	}
	if lightIdx >= heavyIdx {
		t.Errorf("light at %d, heavy at %d — light should come first (fewer conflicts)", lightIdx, heavyIdx)
	}
}

func TestFormatPlan_IncludesActions(t *testing.T) {
	plan := &MergePlan{
		DesignID: "d-1",
		Entries: []MergePlanEntry{
			{TaskID: "t1", Branch: "t1", Wave: 1, Action: ActionMerge},
			{TaskID: "t2", Branch: "t2", Wave: 1, Action: ActionSkip, Note: "superseded"},
			{TaskID: "t3", Branch: "t3", Wave: 2, Action: ActionPartialMerge,
				IncludeFiles: []string{"keep.go"}, SkipFiles: []string{"drop.go"}},
		},
		Summary: PlanSummary{Total: 3, Merge: 1, Partial: 1, Skip: 1, Waves: 2},
	}

	out := FormatPlan(plan)
	for _, want := range []string{"d-1", "Wave 1", "Wave 2", "MERGE", "SKIP", "PARTIAL",
		"superseded", "keep.go", "drop.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// -----------------------------------------------------------------------
// AnalyseBranches — shells out to git; needs a real repo
// -----------------------------------------------------------------------

func TestAnalyseBranches_DetectsFileOverlap_SameWave(t *testing.T) {
	dir := newTestRepo(t)
	makeBranch(t, dir, "a", map[string]string{"shared.go": "from a\n"})
	makeBranch(t, dir, "b", map[string]string{"shared.go": "from b\n", "b-only.go": "b\n"})

	ctx := context.Background()
	cm, err := AnalyseBranches(ctx, dir, []BranchInfo{
		{TaskID: "a", Branch: "a", Wave: 1},
		{TaskID: "b", Branch: "b", Wave: 1},
	})
	if err != nil {
		t.Fatalf("AnalyseBranches: %v", err)
	}

	if cm.Clean {
		t.Errorf("Clean = true, want false (shared.go conflict)")
	}
	var sharedConflict *FileConflict
	for i := range cm.Conflicts {
		if cm.Conflicts[i].File == "shared.go" {
			sharedConflict = &cm.Conflicts[i]
		}
	}
	if sharedConflict == nil {
		t.Fatalf("shared.go not in conflicts: %+v", cm.Conflicts)
	}
	if !sharedConflict.SameWave {
		t.Errorf("SameWave = false, want true")
	}
	if len(sharedConflict.Branches) != 2 {
		t.Errorf("Branches = %v, want 2", sharedConflict.Branches)
	}
}

func TestAnalyseBranches_DetectsFileOverlap_DifferentWaves(t *testing.T) {
	dir := newTestRepo(t)
	makeBranch(t, dir, "a", map[string]string{"shared.go": "from a\n"})
	makeBranch(t, dir, "b", map[string]string{"shared.go": "from b\n"})

	ctx := context.Background()
	cm, err := AnalyseBranches(ctx, dir, []BranchInfo{
		{TaskID: "a", Branch: "a", Wave: 1},
		{TaskID: "b", Branch: "b", Wave: 2},
	})
	if err != nil {
		t.Fatalf("AnalyseBranches: %v", err)
	}
	if len(cm.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(cm.Conflicts))
	}
	if cm.Conflicts[0].SameWave {
		t.Errorf("SameWave = true, want false (different waves)")
	}
}

func TestAnalyseBranches_NoOverlap_CleanTrue(t *testing.T) {
	dir := newTestRepo(t)
	makeBranch(t, dir, "a", map[string]string{"a.go": "a\n"})
	makeBranch(t, dir, "b", map[string]string{"b.go": "b\n"})

	ctx := context.Background()
	cm, err := AnalyseBranches(ctx, dir, []BranchInfo{
		{TaskID: "a", Branch: "a", Wave: 1},
		{TaskID: "b", Branch: "b", Wave: 1},
	})
	if err != nil {
		t.Fatalf("AnalyseBranches: %v", err)
	}
	if !cm.Clean {
		t.Errorf("Clean = false, want true (no shared files)")
	}
	if len(cm.Conflicts) != 0 {
		t.Errorf("conflicts = %d, want 0", len(cm.Conflicts))
	}
}

func TestAnalyseBranches_IgnoresDotCobuildFiles(t *testing.T) {
	dir := newTestRepo(t)
	// Both branches modify .cobuild/session.log but only one real file each.
	makeBranch(t, dir, "a", map[string]string{
		".cobuild/session.log": "a log\n",
		"a-real.go":            "a\n",
	})
	makeBranch(t, dir, "b", map[string]string{
		".cobuild/session.log": "b log\n",
		"b-real.go":            "b\n",
	})

	ctx := context.Background()
	cm, err := AnalyseBranches(ctx, dir, []BranchInfo{
		{TaskID: "a", Branch: "a", Wave: 1},
		{TaskID: "b", Branch: "b", Wave: 1},
	})
	if err != nil {
		t.Fatalf("AnalyseBranches: %v", err)
	}
	// .cobuild/ files should be filtered; real files don't overlap.
	if !cm.Clean {
		t.Errorf("Clean = false; conflicts were %+v, expected .cobuild/ filtered", cm.Conflicts)
	}
	for _, b := range cm.Branches {
		for _, f := range b.Files {
			if strings.HasPrefix(f, ".cobuild/") {
				t.Errorf("branch %s kept .cobuild file %s", b.TaskID, f)
			}
		}
	}
}

func TestAnalyseBranches_EmptyRepoRoot_ReturnsError(t *testing.T) {
	_, err := AnalyseBranches(context.Background(), "", nil)
	if err == nil {
		t.Fatalf("want error for empty repoRoot, got nil")
	}
}

// -----------------------------------------------------------------------
// CanMergeCleanly — shells out to git
// -----------------------------------------------------------------------

func TestCanMergeCleanly_CleanBranch_ReturnsTrue(t *testing.T) {
	dir := newTestRepo(t)
	makeBranch(t, dir, "clean", map[string]string{"new.go": "new file\n"})

	ok, err := CanMergeCleanly(context.Background(), dir, "clean")
	if err != nil {
		t.Fatalf("CanMergeCleanly: %v", err)
	}
	if !ok {
		t.Errorf("got false, want true (branch adds a new file)")
	}
}

func TestCanMergeCleanly_ConflictingBranch_ReturnsFalse(t *testing.T) {
	dir := newTestRepo(t)
	// Modify README on main, then create a branch that also modifies it
	// from the original base.
	runGit(t, dir, "checkout", "-q", "-b", "conflict", "main")
	writeFile(t, dir, "README.md", "conflict side\n")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-q", "-m", "conflict change")
	runGit(t, dir, "checkout", "-q", "main")
	writeFile(t, dir, "README.md", "main side\n")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-q", "-m", "main change")

	ok, err := CanMergeCleanly(context.Background(), dir, "conflict")
	if err != nil {
		t.Fatalf("CanMergeCleanly: %v", err)
	}
	if ok {
		t.Errorf("got true, want false (README diverged on both sides)")
	}
}

// -----------------------------------------------------------------------
// DetectSupersessions — shells out to git via getDiffSize
// -----------------------------------------------------------------------

func TestDetectSupersessions_LargerDiff_Supersedes(t *testing.T) {
	dir := newTestRepo(t)
	// small: 1-line change; big: 10-line change on the same file.
	makeBranch(t, dir, "small", map[string]string{"x.go": "one\n"})
	makeBranch(t, dir, "big", map[string]string{
		"x.go": "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n",
	})

	ctx := context.Background()
	cm, err := AnalyseBranches(ctx, dir, []BranchInfo{
		{TaskID: "small", Branch: "small", Wave: 1},
		{TaskID: "big", Branch: "big", Wave: 1},
	})
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}

	sr, err := DetectSupersessions(ctx, dir, cm)
	if err != nil {
		t.Fatalf("DetectSupersessions: %v", err)
	}
	if len(sr.Supersessions) != 1 {
		t.Fatalf("supersessions = %d, want 1", len(sr.Supersessions))
	}
	s := sr.Supersessions[0]
	if s.SupersededTask != "small" || s.SupersedingTask != "big" {
		t.Errorf("got %s superseded by %s, want small superseded by big",
			s.SupersededTask, s.SupersedingTask)
	}
	// small has only x.go and x.go is superseded -> fully superseded.
	found := false
	for _, id := range sr.FullySuperseded {
		if id == "small" {
			found = true
		}
	}
	if !found {
		t.Errorf("FullySuperseded = %v, want to contain 'small'", sr.FullySuperseded)
	}
}

func TestDetectSupersessions_SimilarSize_NoSupersession(t *testing.T) {
	dir := newTestRepo(t)
	makeBranch(t, dir, "a", map[string]string{"x.go": "alpha\nbeta\n"})
	makeBranch(t, dir, "b", map[string]string{"x.go": "gamma\ndelta\n"})

	ctx := context.Background()
	cm, err := AnalyseBranches(ctx, dir, []BranchInfo{
		{TaskID: "a", Branch: "a", Wave: 1},
		{TaskID: "b", Branch: "b", Wave: 1},
	})
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	sr, err := DetectSupersessions(ctx, dir, cm)
	if err != nil {
		t.Fatalf("DetectSupersessions: %v", err)
	}
	if len(sr.Supersessions) != 0 {
		t.Errorf("supersessions = %d, want 0 (diffs are comparable size)", len(sr.Supersessions))
	}
}

func TestDetectSupersessions_NilConflictMap_SafeEmpty(t *testing.T) {
	sr, err := DetectSupersessions(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("DetectSupersessions(nil): %v", err)
	}
	if len(sr.Supersessions) != 0 || len(sr.FullySuperseded) != 0 {
		t.Errorf("non-empty result for nil cm: %+v", sr)
	}
}

// -----------------------------------------------------------------------
// cb-7dd0d4 scenario: dependent branches after a squash-merge rebase
// -----------------------------------------------------------------------

// When task B is built on top of task A, and A gets squash-merged to main,
// B's branch still points at the pre-squash A commits. Rebase is required
// for B to merge cleanly.
//
// This test exercises the invariant via local-git: create A, create B
// atop A, squash-merge A, then rebase B onto main and assert it replays
// without conflicts (the fix).
func TestCB7DD0D4_DependentBranch_RebasesCleanly(t *testing.T) {
	dir := newTestRepo(t)

	// Task A modifies README.
	makeBranch(t, dir, "a", map[string]string{"README.md": "base\nfrom A\n"})

	// Task B is built ON TOP OF A, not on main. B touches a different file
	// so A's changes remain but B adds its own.
	runGit(t, dir, "checkout", "-q", "-b", "b", "a")
	writeFile(t, dir, "b-only.go", "package b\n")
	runGit(t, dir, "add", "b-only.go")
	runGit(t, dir, "commit", "-q", "-m", "b change")

	// Squash-merge A onto main, rewriting its history into a single commit.
	runGit(t, dir, "checkout", "-q", "main")
	runGit(t, dir, "merge", "--squash", "a")
	runGit(t, dir, "commit", "-q", "-m", "squash merge a")

	// Now B still points at the old A commits. Merging B directly would
	// try to re-apply A's README change on top of the squashed main and
	// was the exact scenario that conflicted before cb-7dd0d4. Rebase
	// onto main should succeed cleanly — B's b-only.go change replays
	// on top of the new main.
	cmd := exec.Command("git", "-C", dir, "rebase", "main", "b")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Clean up any in-progress rebase state for follow-up tests.
		exec.Command("git", "-C", dir, "rebase", "--abort").Run()
		t.Fatalf("rebase b onto main failed (cb-7dd0d4 regression?):\n%s\n%v",
			string(out), err)
	}

	// After rebase, b-only.go should be present and README should match
	// main's squashed state (i.e. A's README change is not re-introduced).
	readmeOut, err := exec.Command("git", "-C", dir, "show", "b:README.md").CombinedOutput()
	if err != nil {
		t.Fatalf("show b:README.md: %v\n%s", err, string(readmeOut))
	}
	if !strings.Contains(string(readmeOut), "from A") {
		t.Errorf("README after rebase missing A's changes (rebase dropped them?):\n%s", string(readmeOut))
	}
	bOnly, err := exec.Command("git", "-C", dir, "show", "b:b-only.go").CombinedOutput()
	if err != nil || !strings.Contains(string(bOnly), "package b") {
		t.Errorf("b-only.go not present on rebased b: %v\n%s", err, string(bOnly))
	}
}
