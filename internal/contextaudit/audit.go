// Package contextaudit inspects the .cobuild/context/ tree and reports on
// per-layer size, bloat signals, and the assembled total an agent would see.
// Used by `cobuild context audit` and the pre-dispatch warning.
package contextaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Thresholds (bytes). Kept here so the dispatch warning and audit CLI agree.
const (
	LayerWarnBytes     = 15 * 1024  // single file ≥15KB is worth a second look
	LayerHighBytes     = 30 * 1024  // ≥30KB is almost always trimmable
	AssembledWarnBytes = 30 * 1024  // assembled total crossing this slows agents
	AssembledHighBytes = 100 * 1024 // assembled total above this degrades output
)

// LayerEntry is one file under .cobuild/context/.
type LayerEntry struct {
	// RelPath is relative to repoRoot (e.g. ".cobuild/context/always/anatomy.md").
	RelPath string `json:"rel_path"`
	// Bucket is the subdir: "always", "dispatch", "design", etc.
	Bucket string `json:"bucket"`
	Bytes  int    `json:"bytes"`
	// Flags are human-readable tags: "oversize", "very-large", "cache-pollution".
	Flags []string `json:"flags,omitempty"`
}

// Report is the result of auditing a repo's context layers.
type Report struct {
	RepoRoot   string       `json:"repo_root"`
	Entries    []LayerEntry `json:"entries"`
	TotalBytes int          `json:"total_bytes"`
	// FlaggedCount is the number of entries with at least one flag.
	FlaggedCount int `json:"flagged_count"`
}

// Inspect walks .cobuild/context/ and returns a Report. Missing directory
// is not an error — the report will simply be empty.
func Inspect(repoRoot string) (*Report, error) {
	base := filepath.Join(repoRoot, ".cobuild", "context")
	r := &Report{RepoRoot: repoRoot}

	buckets, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("read context dir: %w", err)
	}

	for _, b := range buckets {
		if !b.IsDir() {
			continue
		}
		bucketDir := filepath.Join(base, b.Name())
		files, err := os.ReadDir(bucketDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			full := filepath.Join(bucketDir, f.Name())
			info, err := os.Stat(full)
			if err != nil {
				continue
			}
			rel, _ := filepath.Rel(repoRoot, full)
			entry := LayerEntry{
				RelPath: rel,
				Bucket:  b.Name(),
				Bytes:   int(info.Size()),
			}
			entry.Flags = flagsFor(entry, full)
			r.Entries = append(r.Entries, entry)
			r.TotalBytes += entry.Bytes
			if len(entry.Flags) > 0 {
				r.FlaggedCount++
			}
		}
	}

	sort.Slice(r.Entries, func(i, j int) bool {
		return r.Entries[i].Bytes > r.Entries[j].Bytes
	})
	return r, nil
}

// flagsFor inspects a context file and returns applicable flag tags.
// Cheap heuristics only — no parsing, no LLM calls.
func flagsFor(e LayerEntry, fullPath string) []string {
	var flags []string
	switch {
	case e.Bytes >= LayerHighBytes:
		flags = append(flags, "very-large")
	case e.Bytes >= LayerWarnBytes:
		flags = append(flags, "oversize")
	}

	// anatomy.md pollution: cache dirs and binary data dirs that cobuild scan
	// should have skipped but sometimes doesn't. If any are present, point the
	// user at scan configuration.
	if filepath.Base(e.RelPath) == "anatomy.md" && e.Bytes >= LayerWarnBytes {
		data, err := os.ReadFile(fullPath)
		if err == nil {
			lower := strings.ToLower(string(data))
			for _, needle := range []string{".pytest_cache", "__pycache__", ".ruff_cache", "node_modules/", "/data/raw/", "/venv/"} {
				if strings.Contains(lower, needle) {
					flags = append(flags, "cache-pollution")
					break
				}
			}
		}
	}
	return flags
}

// FormatKB renders a byte count as "N KB" (integer KB, always non-negative).
func FormatKB(bytes int) string {
	if bytes < 0 {
		bytes = 0
	}
	return fmt.Sprintf("%d KB", bytes/1024)
}

// Recommendation returns a short human-readable action for an entry, or "" if none.
func Recommendation(e LayerEntry) string {
	has := func(tag string) bool {
		for _, f := range e.Flags {
			if f == tag {
				return true
			}
		}
		return false
	}
	switch {
	case has("cache-pollution"):
		return "contains cache/build-dir references — re-run `cobuild scan` and add these paths to its skip list"
	case has("very-large"):
		return "≥30 KB — split by phase, move phase-specific content out of always/, or prune verbose sections"
	case has("oversize"):
		return "≥15 KB — review for redundancy with project CLAUDE.md or other layers"
	}
	return ""
}
