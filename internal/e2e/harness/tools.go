package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func (h *Harness) prepareTooling() error {
	if err := os.MkdirAll(h.ToolBinDir, 0o755); err != nil {
		return fmt.Errorf("mkdir tool bin dir: %w", err)
	}
	if err := os.MkdirAll(h.GitHubStateDir, 0o755); err != nil {
		return fmt.Errorf("mkdir fake gh dir: %w", err)
	}

	sourceRoot, err := harnessSourceRoot()
	if err != nil {
		return err
	}

	h.BinaryPath = filepath.Join(h.ToolBinDir, "cobuild")
	build := exec.Command("go", "build", "-tags=e2e", "-o", h.BinaryPath, "./cmd/cobuild")
	build.Dir = sourceRoot
	build.Env = os.Environ()
	out, err := build.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build cobuild e2e binary: %w\n%s", err, strings.TrimSpace(string(out)))
	}

	ghPath := filepath.Join(h.ToolBinDir, "gh")
	if err := os.WriteFile(ghPath, []byte(fakeGHScript), 0o755); err != nil {
		return fmt.Errorf("write gh shim: %w", err)
	}
	return nil
}

func harnessSourceRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("resolve harness source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..")), nil
}

func (h *Harness) RunCobuild(ctx context.Context, args ...string) (string, error) {
	cmd := h.CommandContext(ctx, h.BinaryPath, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (h *Harness) syncTmuxEnvironment(ctx context.Context) error {
	if h.Tmux == nil {
		return nil
	}
	for _, entry := range h.Env() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch key {
		case "HOME", "PATH", "COBUILD_STUB_FIXTURES_DIR", "COBUILD_TEST_POSTGRES_DSN", "COBUILD_FAKE_GH_DIR":
			if err := h.Tmux.Run(ctx, "set-environment", "-g", key, value); err != nil {
				return fmt.Errorf("set tmux env %s: %w", key, err)
			}
		}
	}
	return nil
}

const fakeGHScript = `#!/bin/sh
set -eu

state_dir="${COBUILD_FAKE_GH_DIR:?COBUILD_FAKE_GH_DIR is required}"
mkdir -p "$state_dir"

next_pr() {
  next_file="$state_dir/next_pr"
  if [ ! -f "$next_file" ]; then
    echo 1 > "$next_file"
  fi
  n=$(cat "$next_file")
  echo $((n + 1)) > "$next_file"
  echo "$n"
}

parse_pr_num() {
  case "$1" in
    */pull/*) echo "$1" | sed 's#.*/pull/##' ;;
    *) echo "$1" ;;
  esac
}

pr_dir() {
  echo "$state_dir/pr-$(parse_pr_num "$1")"
}

if [ "$1" = "pr" ] && [ "$2" = "create" ]; then
  shift 2
  repo=""
  head=""
  base="main"
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="$2"; shift 2 ;;
      --head) head="$2"; shift 2 ;;
      --base) base="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  pr=$(next_pr)
  dir="$state_dir/pr-$pr"
  mkdir -p "$dir"
  printf '%s' "$repo" > "$dir/repo"
  printf '%s' "$head" > "$dir/head"
  printf '%s' "$base" > "$dir/base"
  printf '%s' "${COBUILD_REPO_ROOT:-$(git rev-parse --show-toplevel)}" > "$dir/repo_root"
  printf '%s' "OPEN" > "$dir/state"
  date -u +"%Y-%m-%dT%H:%M:%SZ" > "$dir/created_at"
  echo "https://github.com/$repo/pull/$pr"
  exit 0
fi

if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  target="$3"
  dir="$(pr_dir "$target")"
  if [ "$4" = "--json" ] && [ "$5" = "state" ] && [ "$6" = "--jq" ] && [ "$7" = ".state" ]; then
    cat "$dir/state"
    exit 0
  fi
  if [ "$4" = "--json" ] && [ "$5" = "createdAt" ] && [ "$6" = "--jq" ] && [ "$7" = ".createdAt" ]; then
    cat "$dir/created_at"
    exit 0
  fi
  if [ "$4" = "--repo" ] && [ "$6" = "--json" ] && [ "$7" = "headRefOid" ] && [ "$8" = "--jq" ] && [ "$9" = ".headRefOid" ]; then
    dir="$state_dir/pr-$target"
    repo_root=$(cat "$dir/repo_root")
    branch=$(cat "$dir/head")
    git -C "$repo_root" rev-parse "$branch"
    exit 0
  fi
fi

if [ "$1" = "pr" ] && [ "$2" = "merge" ]; then
  target="$3"
  dir="$(pr_dir "$target")"
  repo_root=$(cat "$dir/repo_root")
  head=$(cat "$dir/head")
  base=$(cat "$dir/base")
  git -C "$repo_root" fetch origin "$base" >/dev/null 2>&1 || true
  git -C "$repo_root" fetch origin "$head" >/dev/null 2>&1 || true
  branch_ref="$head"
  if ! git -C "$repo_root" show-ref --verify --quiet "refs/heads/$head"; then
    branch_ref="origin/$head"
  fi
  git -C "$repo_root" checkout "$base" >/dev/null 2>&1
  git -C "$repo_root" merge --squash "$branch_ref" >/dev/null
  msg=$(git -C "$repo_root" log -1 --format=%s "$branch_ref")
  git -C "$repo_root" commit -m "$msg" >/dev/null
  git -C "$repo_root" push origin "$base" >/dev/null
  git -C "$repo_root" branch -D "$head" >/dev/null 2>&1 || true
  git -C "$repo_root" push origin --delete "$head" >/dev/null 2>&1 || true
  printf '%s' "MERGED" > "$dir/state"
  echo "merged"
  exit 0
fi

if [ "$1" = "api" ]; then
  case "$2" in
    repos/*/pulls/*/reviews)
      echo "[]"
      exit 0
      ;;
    repos/*/commits/*/check-runs)
      echo "[]"
      exit 0
      ;;
    repos/*/actions/runs*)
      echo ""
      exit 0
      ;;
  esac
fi

printf 'unsupported gh invocation: %s\n' "$*" >&2
exit 1
`
