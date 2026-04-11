package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ensureClaudeTrust pre-accepts Claude Code's workspace trust dialog for a
// directory by editing ~/.claude.json. This avoids dispatched agents blocking
// on "Is this a project you created or one you trust?" in fresh worktrees.
//
// Concurrency: read-modify-write is guarded by an flock on a sibling lock
// file so concurrent `cobuild dispatch` processes (e.g. from dispatch-wave)
// cannot clobber each other's updates. The lock is released automatically
// when the function returns (file close).
//
// The file is read, the specific project entry is added/updated, and the
// whole file is written back atomically (temp file + rename). If the file
// doesn't exist or can't be parsed, we return an error rather than creating
// one from scratch — that's Claude Code's job.
func ensureClaudeTrust(worktreePath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	configPath := filepath.Join(home, ".claude.json")
	lockPath := configPath + ".cobuild-lock"

	// Acquire exclusive advisory lock for the whole read-modify-write cycle.
	// Using a sibling .cobuild-lock file (not the config itself) avoids any
	// interaction with Claude Code's own file handles on .claude.json.
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	defer lockFile.Close()
	if err := flockExclusive(lockFile.Fd()); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	defer flockUnlock(lockFile.Fd())

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}

	// Decode into a generic map so we preserve unknown fields on write-back.
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	// Guard against the file containing literal "null" or being empty-object,
	// either of which would leave cfg nil and panic on the map assignment below.
	if cfg == nil {
		cfg = make(map[string]any)
	}

	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		projects = make(map[string]any)
		cfg["projects"] = projects
	}

	entry, _ := projects[worktreePath].(map[string]any)
	if entry == nil {
		entry = make(map[string]any)
		projects[worktreePath] = entry
	}

	// Only write if ALL required flags are already set — avoids gratuitous file
	// churn but ensures a partially-populated entry (e.g. trust set but onboarding
	// still pending) gets fully repaired before the agent sees it.
	trusted, _ := entry["hasTrustDialogAccepted"].(bool)
	onboarded, _ := entry["hasCompletedProjectOnboarding"].(bool)
	if trusted && onboarded {
		return nil
	}

	entry["hasTrustDialogAccepted"] = true
	entry["hasCompletedProjectOnboarding"] = true
	if _, ok := entry["allowedTools"]; !ok {
		entry["allowedTools"] = []any{}
	}
	if _, ok := entry["projectOnboardingSeenCount"]; !ok {
		entry["projectOnboardingSeenCount"] = 1
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	// Atomic write: temp file in the same directory, then rename
	tmp, err := os.CreateTemp(filepath.Dir(configPath), ".claude.json.tmp.*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp: %w", err)
	}
	// fsync before rename so a crash between the rename and OS flush cannot
	// leave an empty or truncated ~/.claude.json.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, configPath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
