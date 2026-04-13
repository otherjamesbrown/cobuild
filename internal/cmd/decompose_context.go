package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
)

// MergedTask describes a task from this design that has already landed on the
// target repo's main branch.
type MergedTask struct {
	TaskID       string
	CommitSHA    string
	MergedAt     time.Time
	FilesChanged []string
}

type gitHistoryCommit struct {
	SHA      string
	MergedAt time.Time
	Message  string
}

func collectMergedTasks(ctx context.Context, cn connector.Connector, designID, repoRoot string) ([]MergedTask, error) {
	if cn == nil {
		return nil, fmt.Errorf("no connector configured")
	}
	if strings.TrimSpace(designID) == "" {
		return nil, fmt.Errorf("missing design ID")
	}
	if strings.TrimSpace(repoRoot) == "" {
		return nil, fmt.Errorf("missing repo root")
	}

	taskIDs, err := designTaskIDSet(ctx, cn, designID)
	if err != nil {
		return nil, err
	}
	if len(taskIDs) == 0 {
		return nil, nil
	}

	mainRef, err := resolveMainRef(ctx, repoRoot)
	if err != nil {
		return nil, err
	}

	commits, err := gitMainHistory(ctx, repoRoot, mainRef)
	if err != nil {
		return nil, err
	}

	mergedByTask := make(map[string]MergedTask, len(taskIDs))
	for _, commit := range commits {
		for _, taskID := range matchedTaskIDsInCommit(commit.Message, taskIDs) {
			if _, seen := mergedByTask[taskID]; seen {
				continue
			}

			files, err := changedFilesForCommit(ctx, repoRoot, commit.SHA)
			if err != nil {
				return nil, fmt.Errorf("read changed files for %s: %w", commit.SHA, err)
			}

			mergedByTask[taskID] = MergedTask{
				TaskID:       taskID,
				CommitSHA:    commit.SHA,
				MergedAt:     commit.MergedAt,
				FilesChanged: files,
			}
		}
	}

	merged := make([]MergedTask, 0, len(mergedByTask))
	for _, task := range mergedByTask {
		merged = append(merged, task)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].MergedAt.Equal(merged[j].MergedAt) {
			return merged[i].TaskID < merged[j].TaskID
		}
		return merged[i].MergedAt.Before(merged[j].MergedAt)
	})
	return merged, nil
}

func designTaskIDSet(ctx context.Context, cn connector.Connector, designID string) (map[string]struct{}, error) {
	edges, err := cn.GetEdges(ctx, designID, "incoming", []string{"child-of"})
	if err != nil {
		return nil, fmt.Errorf("list child tasks for %s: %w", designID, err)
	}

	taskIDs := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		item, err := cn.Get(ctx, edge.ItemID)
		if err != nil || item == nil || item.Type != "task" {
			continue
		}
		taskIDs[item.ID] = struct{}{}
	}
	return taskIDs, nil
}

func matchedTaskIDsInCommit(message string, taskIDs map[string]struct{}) []string {
	if len(taskIDs) == 0 || message == "" {
		return nil
	}

	seen := map[string]struct{}{}
	var matches []string
	for _, token := range bracketedTokens(message) {
		if _, ok := taskIDs[token]; !ok {
			continue
		}
		if _, dup := seen[token]; dup {
			continue
		}
		seen[token] = struct{}{}
		matches = append(matches, token)
	}
	sort.Strings(matches)
	return matches
}

func bracketedTokens(message string) []string {
	var tokens []string
	for _, field := range strings.FieldsFunc(message, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		if len(field) < 3 || field[0] != '[' || field[len(field)-1] != ']' {
			continue
		}
		token := strings.TrimSpace(field[1 : len(field)-1])
		if token == "" || strings.ContainsAny(token, "[]") {
			continue
		}
		tokens = append(tokens, token)
	}
	return tokens
}

func resolveMainRef(ctx context.Context, repoRoot string) (string, error) {
	for _, ref := range []string{"origin/main", "main"} {
		cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if err := cmd.Run(); err == nil {
			return ref, nil
		}
	}
	return "", fmt.Errorf("could not resolve main branch in %s", repoRoot)
}

func gitMainHistory(ctx context.Context, repoRoot, ref string) ([]gitHistoryCommit, error) {
	out, err := exec.CommandContext(
		ctx,
		"git",
		"-C", repoRoot,
		"log",
		"--format=%H%x00%cI%x00%B%x1e",
		ref,
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w\n%s", ref, err, strings.TrimSpace(string(out)))
	}
	return parseGitHistory(out)
}

func parseGitHistory(raw []byte) ([]gitHistoryCommit, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}

	records := bytes.Split(raw, []byte{0x1e})
	commits := make([]gitHistoryCommit, 0, len(records))
	for _, record := range records {
		record = bytes.TrimSpace(record)
		if len(record) == 0 {
			continue
		}

		fields := bytes.SplitN(record, []byte{0x00}, 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("malformed git log record")
		}

		mergedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(string(fields[1])))
		if err != nil {
			return nil, fmt.Errorf("parse commit timestamp %q: %w", strings.TrimSpace(string(fields[1])), err)
		}

		commits = append(commits, gitHistoryCommit{
			SHA:      strings.TrimSpace(string(fields[0])),
			MergedAt: mergedAt,
			Message:  string(fields[2]),
		})
	}
	return commits, nil
}

func changedFilesForCommit(ctx context.Context, repoRoot, sha string) ([]string, error) {
	out, err := exec.CommandContext(
		ctx,
		"git",
		"-C", repoRoot,
		"show",
		"--pretty=format:",
		"--name-only",
		sha,
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git show %s: %w\n%s", sha, err, strings.TrimSpace(string(out)))
	}

	fileSet := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		path := strings.TrimSpace(line)
		if path == "" || path == ".cobuild" || strings.HasPrefix(path, ".cobuild/") {
			continue
		}
		fileSet[path] = struct{}{}
	}

	files := make([]string, 0, len(fileSet))
	for path := range fileSet {
		files = append(files, path)
	}
	sort.Strings(files)
	return files, nil
}
