# Design: File-Based Store (YAML + JSONL)

## Summary

A `FileStore` implementation of the `Store` interface that persists CoBuild's orchestration data as plain files — YAML for mutable state, JSONL for append-only records. Zero dependencies, git-trackable, human-readable.

## Motivation

For the simplest possible CoBuild setup: no Postgres, no SQLite, just files on disk. This is useful for:
- Quick experiments ("let me try CoBuild on this repo")
- CI/CD environments where installing a database isn't an option
- Git-trackable pipeline state (commit `.cobuild/data/` and see pipeline history in git log)
- Debugging (open the files in any editor)

## Config

```yaml
storage:
    backend: file
    path: .cobuild/data/          # relative to repo root, or absolute
```

Default path when `path` is empty: `.cobuild/data/`

## Directory Layout

```
.cobuild/data/
    runs/
        cb-a3bf71.yaml            # pipeline run state (rewritten on phase change)
        cb-939118.yaml
    gates/
        cb-a3bf71.jsonl           # gate audit trail (append-only)
        cb-939118.jsonl
    tasks/
        cb-a3bf71.jsonl           # task records (append-only)
        cb-939118.jsonl
```

One file per pipeline run (keyed by design_id). Gates and tasks are append-only JSONL.

## File Formats

### runs/{design_id}.yaml

```yaml
id: pr-1711234567890-a1b2c3d4e5f6
design_id: cb-a3bf71
project: cobuild
current_phase: implement
status: active
created_at: "2026-03-24T09:00:00Z"
updated_at: "2026-03-24T10:30:00Z"
```

Rewritten atomically on every phase transition (write to temp file, rename).

### gates/{design_id}.jsonl

```jsonl
{"id":"pg-001","pipeline_id":"pr-001","design_id":"cb-a3bf71","gate_name":"readiness-review","phase":"design","round":1,"verdict":"fail","reviewer":"agent-m","body":"Missing success criteria","created_at":"2026-03-24T09:15:00Z"}
{"id":"pg-002","pipeline_id":"pr-001","design_id":"cb-a3bf71","gate_name":"readiness-review","phase":"design","round":2,"verdict":"pass","reviewer":"agent-m","readiness_score":4,"body":"All criteria met","created_at":"2026-03-24T10:00:00Z"}
```

Append-only. Each line is a self-contained JSON object. `git diff` shows exactly what was added.

### tasks/{design_id}.jsonl

```jsonl
{"id":"pt-001","pipeline_id":"pr-001","task_shard_id":"cb-task1","design_id":"cb-a3bf71","wave":1,"status":"pending","created_at":"2026-03-24T10:30:00Z","updated_at":"2026-03-24T10:30:00Z"}
{"id":"pt-002","pipeline_id":"pr-001","task_shard_id":"cb-task2","design_id":"cb-a3bf71","wave":1,"status":"pending","created_at":"2026-03-24T10:30:00Z","updated_at":"2026-03-24T10:30:00Z"}
```

## Implementation

```go
type FileStore struct {
    root string // base directory (.cobuild/data/)
}

func NewFileStore(root string) (*FileStore, error) {
    for _, dir := range []string{"runs", "gates", "tasks"} {
        if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
            return nil, err
        }
    }
    return &FileStore{root: root}, nil
}
```

### Method Mapping

| Store Method | File Operation |
|---|---|
| `CreateRun` | Write `runs/{designID}.yaml` |
| `GetRun` | Read `runs/{designID}.yaml`, parse YAML |
| `UpdateRunPhase` | Read-modify-write `runs/{designID}.yaml` |
| `UpdateRunStatus` | Read-modify-write `runs/{designID}.yaml` |
| `RecordGate` | Append line to `gates/{designID}.jsonl` |
| `GetGateHistory` | Read all lines from `gates/{designID}.jsonl` |
| `GetLatestGateRound` | Scan `gates/{designID}.jsonl` for max round |
| `AddTask` | Append line to `tasks/{designID}.jsonl` |
| `ListTasks` | Read all lines from `tasks/{designID}.jsonl` |
| `GetRunStatusCounts` | Scan all `runs/*.yaml` files |
| `GetTaskStatusCounts` | Scan all `tasks/*.jsonl` files |
| `GetGatePassRates` | Scan all `gates/*.jsonl` files |
| `GetGateFailures` | Scan all `gates/*.jsonl` files, filter verdict != pass |
| `GetAvgTaskDuration` | Scan all `tasks/*.jsonl`, compute duration |
| `Migrate` | Create directories (no schema needed) |
| `Close` | No-op |

### Atomic Writes (YAML files)

YAML files are mutable — they get rewritten on phase transitions. Use atomic write:

```go
func atomicWriteYAML(path string, data any) error {
    tmp := path + ".tmp"
    f, err := os.Create(tmp)
    if err != nil { return err }
    defer f.Close()
    if err := yaml.NewEncoder(f).Encode(data); err != nil {
        os.Remove(tmp)
        return err
    }
    f.Close()
    return os.Rename(tmp, path)
}
```

### JSONL Append

Append-only operations are straightforward:

```go
func appendJSONL(path string, record any) error {
    f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil { return err }
    defer f.Close()
    return json.NewEncoder(f).Encode(record)
}
```

`json.NewEncoder.Encode` appends a newline automatically, producing valid JSONL.

### Insights Queries (Full Scan)

The insights methods require scanning all files. For a typical project (5-20 pipelines, 50-100 gate records), this is fast enough. For very large projects, the file store may become slow — that's when you upgrade to SQLite or Postgres.

```go
func (s *FileStore) GetRunStatusCounts(ctx context.Context, project string) (map[string]int, error) {
    counts := make(map[string]int)
    files, _ := filepath.Glob(filepath.Join(s.root, "runs", "*.yaml"))
    for _, f := range files {
        var run PipelineRun
        // parse YAML...
        if run.Project == project {
            counts[run.Status]++
        }
    }
    return counts, nil
}
```

## Concurrency

File-based storage has limited concurrency support:
- JSONL appends are safe for single-line writes (OS guarantees atomicity for small writes)
- YAML rewrites use atomic rename (safe for single writer)
- Multiple concurrent writers to the same YAML file could conflict — CoBuild's pipeline lock prevents this in practice

For multi-agent scenarios where the poller and agents write concurrently, SQLite or Postgres is recommended.

## Git Integration

The `.cobuild/data/` directory can be committed to git:

```gitignore
# .gitignore — optionally exclude pipeline data
# .cobuild/data/
```

If committed, `git log --oneline -- .cobuild/data/` shows pipeline activity over time. JSONL diffs are clean — each new gate record shows as one added line.

## Limitations

- No concurrent write safety beyond OS-level atomic operations
- Insights queries require full file scans (O(files * records))
- No referential integrity (a gate record can reference a non-existent pipeline)
- No transactions (a crash mid-update could leave inconsistent state)

These are acceptable for the target use case: single-user experimentation and simple CI pipelines.

## Open Questions

- Should the file store support an `index.json` for faster lookups?
- Should task status updates rewrite the JSONL line (breaking append-only) or append a new record with updated status?
- Should `.cobuild/data/` be in `.gitignore` by default, or committed?
- Is YAML the right format for run state, or should it be JSON for consistency with JSONL?
