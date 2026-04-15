// Package merge provides conflict analysis, supersession detection,
// and merge plan generation for CoBuild pipeline task branches.
package merge

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// BranchInfo describes a task branch and its changed files.
type BranchInfo struct {
	TaskID string   `json:"task_id"`
	Branch string   `json:"branch"`
	Wave   int      `json:"wave"`
	Files  []string `json:"files"`
	PR     string   `json:"pr,omitempty"`
}

// FileConflict identifies a file modified by multiple branches.
type FileConflict struct {
	File     string   `json:"file"`
	Branches []string `json:"branches"` // task IDs
	SameWave bool     `json:"same_wave"`
}

// ConflictMap is the result of analysing all branches for a design.
type ConflictMap struct {
	Branches  []BranchInfo   `json:"branches"`
	Conflicts []FileConflict `json:"conflicts"`
	Clean     bool           `json:"clean"` // true if no conflicts
}

// AnalyseBranches examines all task branches relative to main and identifies file overlaps.
func AnalyseBranches(ctx context.Context, repoRoot string, tasks []BranchInfo) (*ConflictMap, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("repoRoot is empty")
	}

	// Get files changed per branch
	for i := range tasks {
		if tasks[i].Branch == "" {
			tasks[i].Branch = tasks[i].TaskID
		}
		files, err := getChangedFiles(ctx, repoRoot, tasks[i].Branch)
		if err != nil {
			return nil, fmt.Errorf("get files for %s: %w", tasks[i].TaskID, err)
		}
		tasks[i].Files = files
	}

	// Build file → branches map
	fileMap := make(map[string][]string)
	for _, t := range tasks {
		for _, f := range t.Files {
			fileMap[f] = append(fileMap[f], t.TaskID)
		}
	}

	// Build wave lookup
	waveLookup := make(map[string]int)
	for _, t := range tasks {
		waveLookup[t.TaskID] = t.Wave
	}

	// Find conflicts (files in multiple branches)
	var conflicts []FileConflict
	for file, taskIDs := range fileMap {
		if len(taskIDs) < 2 {
			continue
		}
		// Check if all branches are in the same wave
		sameWave := true
		firstWave := waveLookup[taskIDs[0]]
		for _, tid := range taskIDs[1:] {
			if waveLookup[tid] != firstWave {
				sameWave = false
				break
			}
		}
		conflicts = append(conflicts, FileConflict{
			File:     file,
			Branches: taskIDs,
			SameWave: sameWave,
		})
	}

	// Sort conflicts by file name for stable output
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].File < conflicts[j].File
	})

	return &ConflictMap{
		Branches:  tasks,
		Conflicts: conflicts,
		Clean:     len(conflicts) == 0,
	}, nil
}

// CanMergeCleanly tests whether a branch can merge into main without conflicts.
func CanMergeCleanly(ctx context.Context, repoRoot, branch string) (bool, error) {
	// Try a no-commit merge
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "merge", "--no-commit", "--no-ff", branch)
	out, err := cmd.CombinedOutput()

	// Always abort the merge attempt
	exec.CommandContext(ctx, "git", "-C", repoRoot, "merge", "--abort").Run()

	if err != nil {
		if strings.Contains(string(out), "CONFLICT") {
			return false, nil
		}
		return false, fmt.Errorf("merge test failed: %w\n%s", err, string(out))
	}
	return true, nil
}

func getChangedFiles(ctx context.Context, repoRoot, branch string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "diff", "--name-only", "main.."+branch).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only main..%s: %w\n%s", branch, err, string(out))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, ".cobuild/") {
			files = append(files, l)
		}
	}
	return files, nil
}
