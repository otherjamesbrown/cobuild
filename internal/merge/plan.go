package merge

import (
	"fmt"
	"sort"
	"strings"
)

// MergeAction is what to do with a task branch.
type MergeAction string

const (
	ActionMerge        MergeAction = "merge"
	ActionPartialMerge MergeAction = "partial_merge"
	ActionSkip         MergeAction = "skip"
)

// MergePlanEntry is one step in the merge plan.
type MergePlanEntry struct {
	TaskID       string      `json:"task_id"`
	Branch       string      `json:"branch"`
	Wave         int         `json:"wave"`
	Action       MergeAction `json:"action"`
	IncludeFiles []string    `json:"include_files,omitempty"`
	SkipFiles    []string    `json:"skip_files,omitempty"`
	Note         string      `json:"note,omitempty"`
	PR           string      `json:"pr,omitempty"`
}

// MergePlan is the ordered list of merge actions for a design.
type MergePlan struct {
	DesignID string           `json:"design_id"`
	Entries  []MergePlanEntry `json:"entries"`
	Summary  PlanSummary      `json:"summary"`
}

// PlanSummary counts actions in the plan.
type PlanSummary struct {
	Total    int `json:"total"`
	Merge    int `json:"merge"`
	Partial  int `json:"partial"`
	Skip     int `json:"skip"`
	Waves    int `json:"waves"`
}

// GeneratePlan creates an ordered merge plan from conflict analysis and supersession data.
func GeneratePlan(designID string, cm *ConflictMap, sr *SupersessionResult) *MergePlan {
	// Build lookup sets
	fullySkipped := make(map[string]bool)
	for _, id := range sr.FullySuperseded {
		fullySkipped[id] = true
	}

	// Group branches by wave
	waveGroups := make(map[int][]BranchInfo)
	for _, b := range cm.Branches {
		waveGroups[b.Wave] = append(waveGroups[b.Wave], b)
	}

	// Sort waves
	var waves []int
	for w := range waveGroups {
		waves = append(waves, w)
	}
	sort.Ints(waves)

	var entries []MergePlanEntry

	for _, wave := range waves {
		branches := waveGroups[wave]

		// Sort branches within wave: fewest conflicts first (cleanest merges first)
		conflictCount := make(map[string]int)
		for _, c := range cm.Conflicts {
			if c.SameWave {
				for _, tid := range c.Branches {
					conflictCount[tid]++
				}
			}
		}
		sort.Slice(branches, func(i, j int) bool {
			return conflictCount[branches[i].TaskID] < conflictCount[branches[j].TaskID]
		})

		for _, b := range branches {
			entry := MergePlanEntry{
				TaskID: b.TaskID,
				Branch: b.Branch,
				Wave:   wave,
				PR:     b.PR,
			}

			if fullySkipped[b.TaskID] {
				entry.Action = ActionSkip
				// Find who supersedes
				for _, s := range sr.Supersessions {
					if s.SupersededTask == b.TaskID {
						entry.Note = fmt.Sprintf("fully superseded by %s", s.SupersedingTask)
						break
					}
				}
			} else if info, ok := sr.PartiallySuperseded[b.TaskID]; ok {
				entry.Action = ActionPartialMerge
				entry.IncludeFiles = info.IncludeFiles
				entry.SkipFiles = info.SkipFiles
				entry.Note = fmt.Sprintf("partial — skip files superseded by %s", info.SupersedBy)
			} else {
				entry.Action = ActionMerge
			}

			entries = append(entries, entry)
		}
	}

	// Build summary
	summary := PlanSummary{Total: len(entries), Waves: len(waves)}
	for _, e := range entries {
		switch e.Action {
		case ActionMerge:
			summary.Merge++
		case ActionPartialMerge:
			summary.Partial++
		case ActionSkip:
			summary.Skip++
		}
	}

	return &MergePlan{
		DesignID: designID,
		Entries:  entries,
		Summary:  summary,
	}
}

// FormatPlan returns a human-readable representation of the merge plan.
func FormatPlan(plan *MergePlan) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Merge plan for %s (%d tasks, %d waves)\n",
		plan.DesignID, plan.Summary.Total, plan.Summary.Waves))
	sb.WriteString(fmt.Sprintf("  Merge: %d | Partial: %d | Skip: %d\n\n",
		plan.Summary.Merge, plan.Summary.Partial, plan.Summary.Skip))

	currentWave := -1
	for _, e := range plan.Entries {
		if e.Wave != currentWave {
			currentWave = e.Wave
			sb.WriteString(fmt.Sprintf("Wave %d:\n", currentWave))
		}

		action := string(e.Action)
		switch e.Action {
		case ActionMerge:
			action = "MERGE"
		case ActionPartialMerge:
			action = "PARTIAL"
		case ActionSkip:
			action = "SKIP"
		}

		sb.WriteString(fmt.Sprintf("  %-8s %s", action, e.TaskID))
		if e.Note != "" {
			sb.WriteString(fmt.Sprintf("  — %s", e.Note))
		}
		sb.WriteString("\n")

		if e.Action == ActionPartialMerge {
			sb.WriteString(fmt.Sprintf("           include: %s\n", strings.Join(e.IncludeFiles, ", ")))
			sb.WriteString(fmt.Sprintf("           skip:    %s\n", strings.Join(e.SkipFiles, ", ")))
		}
	}

	return sb.String()
}
