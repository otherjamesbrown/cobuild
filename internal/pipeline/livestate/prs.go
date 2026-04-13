package livestate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// PRInfo is a dashboard-oriented view of an open pull request.
type PRInfo struct {
	Repo       string `json:"repo"`        // e.g. "otherjamesbrown/cobuild"
	Number     int    `json:"number"`
	Title      string `json:"title"`
	Branch     string `json:"branch"`
	Mergeable  string `json:"mergeable"`   // MERGEABLE, CONFLICTING, UNKNOWN
	TaskID     string `json:"task_id,omitempty"` // parsed from branch name if it looks like a cobuild task
	URL        string `json:"url"`
}

// CollectPRs queries `gh pr list --json` for each repo, with an in-memory
// cache keyed by repo to avoid hammering the GitHub API on repeated dashboard
// invocations.
func CollectPRs(ctx context.Context, exec CommandRunner, repos []string, now time.Time) ([]PRInfo, error) {
	if len(repos) == 0 {
		return nil, nil
	}

	out := make([]PRInfo, 0)
	var firstErr error
	for _, repo := range repos {
		prs, err := prCache.fetch(ctx, exec, repo, now)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("repo %s: %w", repo, err)
			}
			continue
		}
		out = append(out, prs...)
	}
	return out, firstErr
}

// prCacheTTL is how long a per-repo PR list is reused before refetching.
// Short enough to feel live, long enough to avoid rate limits when an
// orchestrator runs the dashboard repeatedly during waits.
const prCacheTTL = 60 * time.Second

type prCacheEntry struct {
	fetchedAt time.Time
	prs       []PRInfo
}

type prCacheStore struct {
	mu      sync.Mutex
	entries map[string]prCacheEntry
}

var prCache = &prCacheStore{entries: map[string]prCacheEntry{}}

func (c *prCacheStore) fetch(ctx context.Context, exec CommandRunner, repo string, now time.Time) ([]PRInfo, error) {
	c.mu.Lock()
	if entry, ok := c.entries[repo]; ok && now.Sub(entry.fetchedAt) < prCacheTTL {
		defer c.mu.Unlock()
		return entry.prs, nil
	}
	c.mu.Unlock()

	args := []string{
		"pr", "list",
		"--repo", repo,
		"--state", "open",
		"--json", "number,title,headRefName,mergeable,url",
		"--limit", "100",
	}
	out, err := exec(ctx, "gh", args...)
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	type ghPR struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		HeadRefName string `json:"headRefName"`
		Mergeable   string `json:"mergeable"`
		URL         string `json:"url"`
	}
	var raw []ghPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse gh output: %w", err)
	}

	prs := make([]PRInfo, 0, len(raw))
	for _, p := range raw {
		prs = append(prs, PRInfo{
			Repo:      repo,
			Number:    p.Number,
			Title:     p.Title,
			Branch:    p.HeadRefName,
			Mergeable: p.Mergeable,
			TaskID:    parseTaskIDFromBranch(p.HeadRefName),
			URL:       p.URL,
		})
	}

	c.mu.Lock()
	c.entries[repo] = prCacheEntry{fetchedAt: now, prs: prs}
	c.mu.Unlock()

	return prs, nil
}

// parseTaskIDFromBranch extracts a cobuild task ID (e.g. "cb-abc123")
// from a branch name. CoBuild branches are named after the task ID, so
// the branch name IS the task ID in the simple case.
func parseTaskIDFromBranch(branch string) string {
	branch = strings.TrimSpace(branch)
	parts := strings.SplitN(branch, "-", 2)
	if len(parts) != 2 {
		return ""
	}
	prefix := parts[0]
	if len(prefix) < 1 || len(prefix) > 4 {
		return ""
	}
	for _, r := range parts[1] {
		if !isHexOrDash(r) && r != '-' {
			return ""
		}
	}
	return branch
}

func isHexOrDash(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || r == '-'
}

// resetPRCacheForTest clears the cache so tests get deterministic behaviour.
func resetPRCacheForTest() {
	prCache.mu.Lock()
	prCache.entries = map[string]prCacheEntry{}
	prCache.mu.Unlock()
}
