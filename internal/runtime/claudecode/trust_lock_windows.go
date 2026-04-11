//go:build windows

package claudecode

// flockExclusive is a best-effort no-op on Windows. CoBuild's dispatch path
// is inherently Unix-centric (tmux, bash worktree setup) so Windows support
// is secondary; the read-modify-write of ~/.claude.json is unguarded on
// Windows but compilation is preserved.
func flockExclusive(fd uintptr) error { return nil }

// flockUnlock is the Windows counterpart no-op.
func flockUnlock(fd uintptr) error { return nil }
