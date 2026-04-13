package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/connector"
)

var decomposeMergedTaskCollector = collectMergedTasks

type fileOverlapWarning struct {
	TaskID       string
	MergedTaskID string
	Paths        []string
}

type mergedFileOverlap struct {
	path   string
	taskID string
}

type mergedFileOverlapIndex struct {
	exact  map[string][]string
	sorted []mergedFileOverlap
}

func collectDecomposeFileOverlapWarnings(ctx context.Context, cn connector.Connector, designID, repoRoot string) ([]fileOverlapWarning, error) {
	if cn == nil {
		return nil, fmt.Errorf("no connector configured")
	}

	mergedTasks, err := decomposeMergedTaskCollector(ctx, cn, designID, repoRoot)
	if err != nil {
		return nil, err
	}
	if len(mergedTasks) == 0 {
		return nil, nil
	}

	childTasks, err := loadOpenChildTasks(ctx, cn, designID)
	if err != nil {
		return nil, err
	}
	if len(childTasks) == 0 {
		return nil, nil
	}

	index := newMergedFileOverlapIndex(mergedTasks)
	var warnings []fileOverlapWarning
	for _, task := range childTasks {
		candidates := taskFileOverlapCandidates(task)
		if len(candidates) == 0 {
			continue
		}
		warnings = append(warnings, index.findTaskOverlaps(task.ID, candidates)...)
	}

	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].TaskID == warnings[j].TaskID {
			return warnings[i].MergedTaskID < warnings[j].MergedTaskID
		}
		return warnings[i].TaskID < warnings[j].TaskID
	})
	return warnings, nil
}

func loadOpenChildTasks(ctx context.Context, cn connector.Connector, designID string) ([]*connector.WorkItem, error) {
	edges, err := cn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return nil, fmt.Errorf("list child tasks for %s: %w", designID, err)
	}

	tasks := make([]*connector.WorkItem, 0, len(edges))
	for _, edge := range edges {
		if edge.Type != "" && edge.Type != "task" {
			continue
		}
		task, err := cn.Get(ctx, edge.ItemID)
		if err != nil {
			return nil, fmt.Errorf("load child task %s: %w", edge.ItemID, err)
		}
		if task == nil || task.Type != "task" || task.Status == "closed" {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func taskFileOverlapCandidates(task *connector.WorkItem) []string {
	if task == nil || task.Metadata == nil {
		return nil
	}

	keys := []string{"files", "paths", "file", "path"}
	seen := map[string]struct{}{}
	var candidates []string
	for _, key := range keys {
		for _, rawPath := range metadataPaths(task.Metadata[key]) {
			path := normalizeOverlapPath(rawPath)
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			candidates = append(candidates, path)
		}
	}
	sort.Strings(candidates)
	return candidates
}

func metadataPaths(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return parseMetadataPathString(v)
	case []string:
		return append([]string(nil), v...)
	case []any:
		paths := make([]string, 0, len(v))
		for _, item := range v {
			paths = append(paths, fmt.Sprintf("%v", item))
		}
		return paths
	default:
		return []string{fmt.Sprintf("%v", v)}
	}
}

func parseMetadataPathString(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var paths []string
		if err := json.Unmarshal([]byte(trimmed), &paths); err == nil {
			return paths
		}
		var raw []any
		if err := json.Unmarshal([]byte(trimmed), &raw); err == nil {
			return metadataPaths(raw)
		}
	}
	if strings.Contains(trimmed, "\n") {
		lines := strings.Split(trimmed, "\n")
		paths := make([]string, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
			if line != "" {
				paths = append(paths, line)
			}
		}
		return paths
	}
	if strings.Contains(trimmed, ",") {
		parts := strings.Split(trimmed, ",")
		paths := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				paths = append(paths, part)
			}
		}
		return paths
	}
	return []string{trimmed}
}

func normalizeOverlapPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "-")
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "./")
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func newMergedFileOverlapIndex(mergedTasks []MergedTask) mergedFileOverlapIndex {
	exact := make(map[string][]string)
	seenPairs := make(map[string]struct{})
	sorted := make([]mergedFileOverlap, 0)

	for _, task := range mergedTasks {
		for _, rawPath := range task.FilesChanged {
			path := normalizeOverlapPath(rawPath)
			if path == "" {
				continue
			}
			key := task.TaskID + "\x00" + path
			if _, ok := seenPairs[key]; ok {
				continue
			}
			seenPairs[key] = struct{}{}
			exact[path] = append(exact[path], task.TaskID)
			sorted = append(sorted, mergedFileOverlap{path: path, taskID: task.TaskID})
		}
	}

	for path := range exact {
		sort.Strings(exact[path])
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].path == sorted[j].path {
			return sorted[i].taskID < sorted[j].taskID
		}
		return sorted[i].path < sorted[j].path
	})

	return mergedFileOverlapIndex{exact: exact, sorted: sorted}
}

func (idx mergedFileOverlapIndex) findTaskOverlaps(taskID string, candidates []string) []fileOverlapWarning {
	if len(candidates) == 0 {
		return nil
	}

	byMergedTask := make(map[string]map[string]struct{})
	for _, candidate := range candidates {
		path := normalizeOverlapPath(candidate)
		if path == "" {
			continue
		}

		for _, mergedTaskID := range idx.exact[path] {
			addTaskOverlap(byMergedTask, mergedTaskID, path)
		}

		prefix := path + "/"
		start := sort.Search(len(idx.sorted), func(i int) bool {
			return idx.sorted[i].path >= prefix
		})
		for i := start; i < len(idx.sorted); i++ {
			entry := idx.sorted[i]
			if !strings.HasPrefix(entry.path, prefix) {
				break
			}
			addTaskOverlap(byMergedTask, entry.taskID, entry.path)
		}
	}

	warnings := make([]fileOverlapWarning, 0, len(byMergedTask))
	for mergedTaskID, paths := range byMergedTask {
		overlapPaths := make([]string, 0, len(paths))
		for path := range paths {
			overlapPaths = append(overlapPaths, path)
		}
		sort.Strings(overlapPaths)
		warnings = append(warnings, fileOverlapWarning{
			TaskID:       taskID,
			MergedTaskID: mergedTaskID,
			Paths:        overlapPaths,
		})
	}
	sort.Slice(warnings, func(i, j int) bool {
		return warnings[i].MergedTaskID < warnings[j].MergedTaskID
	})
	return warnings
}

func addTaskOverlap(byMergedTask map[string]map[string]struct{}, mergedTaskID, path string) {
	if byMergedTask[mergedTaskID] == nil {
		byMergedTask[mergedTaskID] = make(map[string]struct{})
	}
	byMergedTask[mergedTaskID][path] = struct{}{}
}

func renderFileOverlapWarnings(warnings []fileOverlapWarning) string {
	if len(warnings) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("⚠️ file-overlap\n")
	for _, warning := range warnings {
		b.WriteString(fmt.Sprintf("  - task %s overlaps merged task %s: %s\n",
			warning.TaskID,
			warning.MergedTaskID,
			strings.Join(warning.Paths, ", "),
		))
	}
	return strings.TrimRight(b.String(), "\n")
}
