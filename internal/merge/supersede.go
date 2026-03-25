package merge

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Supersession describes one task's changes being replaced by another.
type Supersession struct {
	SupersededTask  string `json:"superseded_task"`
	SupersedingTask string `json:"superseding_task"`
	File            string `json:"file"`
	Reason          string `json:"reason"`
}

// SupersessionResult is the analysis of which tasks are superseded.
type SupersessionResult struct {
	Supersessions       []Supersession    `json:"supersessions"`
	FullySuperseded     []string          `json:"fully_superseded"`      // task IDs to skip entirely
	PartiallySuperseded map[string]SkipInfo `json:"partially_superseded"` // task ID → which files to skip
}

// SkipInfo describes which files to skip for a partially superseded task.
type SkipInfo struct {
	SkipFiles    []string `json:"skip_files"`
	IncludeFiles []string `json:"include_files"`
	SupersedBy   string   `json:"superseded_by"`
}

// DetectSupersessions analyses file overlaps between branches to find
// tasks whose changes are fully or partially replaced by later/better implementations.
func DetectSupersessions(ctx context.Context, repoRoot string, cm *ConflictMap) (*SupersessionResult, error) {
	if cm == nil || len(cm.Conflicts) == 0 {
		return &SupersessionResult{
			PartiallySuperseded: make(map[string]SkipInfo),
		}, nil
	}

	// For each conflicting file, compare the diff sizes between branches
	var supersessions []Supersession
	fileSupersessions := make(map[string]map[string]string) // file → superseded_task → superseding_task

	for _, conflict := range cm.Conflicts {
		if len(conflict.Branches) != 2 {
			continue // skip 3+ way conflicts for now
		}

		taskA := conflict.Branches[0]
		taskB := conflict.Branches[1]

		sizeA, err := getDiffSize(ctx, repoRoot, branchForTask(cm, taskA), conflict.File)
		if err != nil {
			continue
		}
		sizeB, err := getDiffSize(ctx, repoRoot, branchForTask(cm, taskB), conflict.File)
		if err != nil {
			continue
		}

		// The branch with strictly more changes supersedes the other
		// Heuristic: if one diff is 50%+ larger, it's the better version
		var superseded, superseding string
		var reason string

		if sizeB > sizeA && float64(sizeB) > float64(sizeA)*1.5 {
			superseded = taskA
			superseding = taskB
			reason = fmt.Sprintf("%s has %d lines vs %s has %d lines on %s", taskB, sizeB, taskA, sizeA, conflict.File)
		} else if sizeA > sizeB && float64(sizeA) > float64(sizeB)*1.5 {
			superseded = taskB
			superseding = taskA
			reason = fmt.Sprintf("%s has %d lines vs %s has %d lines on %s", taskA, sizeA, taskB, sizeB, conflict.File)
		} else {
			// Similar size — not a clear supersession, this is a real conflict
			continue
		}

		supersessions = append(supersessions, Supersession{
			SupersededTask:  superseded,
			SupersedingTask: superseding,
			File:            conflict.File,
			Reason:          reason,
		})

		if fileSupersessions[conflict.File] == nil {
			fileSupersessions[conflict.File] = make(map[string]string)
		}
		fileSupersessions[conflict.File][superseded] = superseding
	}

	// Determine fully vs partially superseded tasks
	branchFiles := make(map[string][]string) // task → all files
	for _, b := range cm.Branches {
		branchFiles[b.TaskID] = b.Files
	}

	supersededFiles := make(map[string]map[string]bool) // task → set of superseded files
	supersedingBy := make(map[string]string)             // task → who supersedes it
	for _, s := range supersessions {
		if supersededFiles[s.SupersededTask] == nil {
			supersededFiles[s.SupersededTask] = make(map[string]bool)
		}
		supersededFiles[s.SupersededTask][s.File] = true
		supersedingBy[s.SupersededTask] = s.SupersedingTask
	}

	var fullySuperseded []string
	partiallySuperseded := make(map[string]SkipInfo)

	for taskID, sFiles := range supersededFiles {
		allFiles := branchFiles[taskID]
		if len(allFiles) == 0 {
			continue
		}

		// Filter out .cobuild files
		var realFiles []string
		for _, f := range allFiles {
			if !strings.HasPrefix(f, ".cobuild/") {
				realFiles = append(realFiles, f)
			}
		}

		allSuperseded := true
		var skipFiles, includeFiles []string
		for _, f := range realFiles {
			if sFiles[f] {
				skipFiles = append(skipFiles, f)
			} else {
				includeFiles = append(includeFiles, f)
				allSuperseded = false
			}
		}

		if allSuperseded {
			fullySuperseded = append(fullySuperseded, taskID)
		} else if len(skipFiles) > 0 {
			partiallySuperseded[taskID] = SkipInfo{
				SkipFiles:    skipFiles,
				IncludeFiles: includeFiles,
				SupersedBy:   supersedingBy[taskID],
			}
		}
	}

	return &SupersessionResult{
		Supersessions:       supersessions,
		FullySuperseded:     fullySuperseded,
		PartiallySuperseded: partiallySuperseded,
	}, nil
}

func branchForTask(cm *ConflictMap, taskID string) string {
	for _, b := range cm.Branches {
		if b.TaskID == taskID {
			return b.Branch
		}
	}
	return taskID
}

func getDiffSize(ctx context.Context, repoRoot, branch, file string) (int, error) {
	// Count lines in the diff (rough proxy for change size)
	diffOut, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "diff", "main.."+branch, "--", file).CombinedOutput()
	if err != nil {
		return 0, err
	}
	lines := strings.Count(string(diffOut), "\n")
	return lines, nil
}
