package cmd

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
)

func TestMatchedTaskIDsInCommitHandlesShortIDsAndPrefixCollisions(t *testing.T) {
	taskIDs := map[string]struct{}{
		"cb-1":      {},
		"cb-10":     {},
		"cb-1dbec5": {},
	}

	tests := []struct {
		name    string
		message string
		want    []string
	}{
		{
			name:    "short ID matches exactly",
			message: "[cb-1] add initial support",
			want:    []string{"cb-1"},
		},
		{
			name:    "longer prefix sibling does not match short ID",
			message: "[cb-10] refine the implementation",
			want:    []string{"cb-10"},
		},
		{
			name:    "prefix collision does not partial match",
			message: "[cb-1dbec5-extra] unrelated follow-up",
			want:    nil,
		},
		{
			name:    "duplicate tags collapse to one exact match",
			message: "[cb-1dbec5] fix\n\nFollow-up for [cb-1dbec5]",
			want:    []string{"cb-1dbec5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchedTaskIDsInCommit(tt.message, taskIDs); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("matchedTaskIDsInCommit(%q) = %v, want %v", tt.message, got, tt.want)
			}
		})
	}
}

func TestParseGitHistory(t *testing.T) {
	raw := []byte(
		"abc123\x002026-04-13T10:00:00Z\x00[cb-1] first commit\nbody line\x1e" +
			"def456\x002026-04-13T11:00:00Z\x00[cb-2] second commit\x1e",
	)

	commits, err := parseGitHistory(raw)
	if err != nil {
		t.Fatalf("parseGitHistory() error = %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("parseGitHistory() returned %d commits, want 2", len(commits))
	}
	if commits[0].SHA != "abc123" {
		t.Fatalf("first SHA = %q, want abc123", commits[0].SHA)
	}
	if commits[0].MergedAt.Format(time.RFC3339) != "2026-04-13T10:00:00Z" {
		t.Fatalf("first merged time = %s", commits[0].MergedAt.Format(time.RFC3339))
	}
	if commits[1].Message != "[cb-2] second commit" {
		t.Fatalf("second message = %q", commits[1].Message)
	}
}

func TestCollectMergedTasksFiltersToDesignAndCapturesFiles(t *testing.T) {
	ctx := context.Background()
	repoRoot := newMergedTaskTestRepo(t)

	runGit(t, repoRoot, "config", "user.email", "test@example.com")
	runGit(t, repoRoot, "config", "user.name", "Test User")

	commitOneSHA := commitMergedTask(t, repoRoot, "[cb-1] add parser", map[string]string{
		"internal/cmd/parser.go": "package cmd\n",
	})
	commitTwoSHA := commitMergedTask(t, repoRoot, "[cb-10] add repo-backed scan", map[string]string{
		"internal/cmd/scan.go":  "package cmd\n",
		"internal/cmd/extra.go": "package cmd\n",
	})
	_ = commitMergedTask(t, repoRoot, "[cb-999] unrelated design work", map[string]string{
		"docs/other.md": "other\n",
	})
	_ = commitMergedTask(t, repoRoot, "[cb-100] prefix collision but wrong design", map[string]string{
		"docs/prefix.md": "prefix\n",
	})

	testConn := newFakeConnector()
	testConn.addItem(&connector.WorkItem{ID: "cb-design", Type: "design", Status: "open"})
	testConn.addItem(&connector.WorkItem{ID: "cb-1", Type: "task", Status: "closed"})
	testConn.addItem(&connector.WorkItem{ID: "cb-10", Type: "task", Status: "closed"})
	testConn.addItem(&connector.WorkItem{ID: "cb-100", Type: "task", Status: "closed"})
	testConn.setChildTasks("cb-design", "cb-1", "cb-10")

	merged, err := collectMergedTasks(ctx, testConn, "cb-design", repoRoot)
	if err != nil {
		t.Fatalf("collectMergedTasks() error = %v", err)
	}

	want := []MergedTask{
		{
			TaskID:       "cb-1",
			CommitSHA:    commitOneSHA,
			FilesChanged: []string{"internal/cmd/parser.go"},
		},
		{
			TaskID:       "cb-10",
			CommitSHA:    commitTwoSHA,
			FilesChanged: []string{"internal/cmd/extra.go", "internal/cmd/scan.go"},
		},
	}

	if len(merged) != len(want) {
		t.Fatalf("collectMergedTasks() returned %d tasks, want %d", len(merged), len(want))
	}
	for i := range want {
		if merged[i].TaskID != want[i].TaskID {
			t.Fatalf("merged[%d].TaskID = %q, want %q", i, merged[i].TaskID, want[i].TaskID)
		}
		if merged[i].CommitSHA != want[i].CommitSHA {
			t.Fatalf("merged[%d].CommitSHA = %q, want %q", i, merged[i].CommitSHA, want[i].CommitSHA)
		}
		if merged[i].MergedAt.IsZero() {
			t.Fatalf("merged[%d].MergedAt is zero", i)
		}
		if !reflect.DeepEqual(merged[i].FilesChanged, want[i].FilesChanged) {
			t.Fatalf("merged[%d].FilesChanged = %v, want %v", i, merged[i].FilesChanged, want[i].FilesChanged)
		}
	}
}

func newMergedTaskTestRepo(t *testing.T) string {
	t.Helper()

	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init", "-b", "main")
	writeFile(t, filepath.Join(repoRoot, "README.md"), "initial\n")
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "initial")

	return repoRoot
}

func commitMergedTask(t *testing.T, repoRoot, message string, files map[string]string) string {
	t.Helper()

	for path, body := range files {
		fullPath := filepath.Join(repoRoot, path)
		completionWriteFile(t, fullPath, body)
		runGit(t, repoRoot, "add", path)
	}
	runGit(t, repoRoot, "commit", "-m", message)
	return runGit(t, repoRoot, "rev-parse", "HEAD")
}
