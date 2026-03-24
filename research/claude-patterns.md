# Claude Code / CoWork Patterns for CoBuild

Research into how Claude Code and Claude CoWork structure their extension systems, so CoBuild can reuse familiar patterns and terminology.

## Key Terminology Mapping

Claude has settled on specific terms. CoBuild should adopt these where the concept maps:

| Claude Term | Claude Definition | CoBuild Equivalent | Notes |
|-------------|------------------|-------------------|-------|
| **Connector** | Bridges Claude to external systems (Jira, Linear, Slack) | Work-item backend (CP, Beads, Jira) | User-facing term for external integrations |
| **MCP Server** | Backend implementation exposing tools via MCP protocol | Connector implementation | Technical term for the adapter |
| **Tool** | Individual function Claude can call (`create_issue`, `list_events`) | Operation on a connector (`create_work_item`, `update_status`) | Snake_case naming convention |
| **Skill** | Markdown file with YAML frontmatter + instructions | Skill (already aligned) | Our skills are identical in format |
| **Plugin** | Self-contained directory of components (skills + hooks + MCP + agents) | Pipeline template / plugin | Distributable config bundle |
| **Hook** | Event handler firing at lifecycle points | Gate handler / phase hook | `PrePhase`, `PostPhase`, `PreDispatch` etc. |
| **Agent** | Specialized AI worker with own model, tools, permissions | Phase worker agent | Already aligned |
| **Scope** | Config hierarchy: user > project > local > managed | Config hierarchy: global > repo > local | Already aligned (`~/.cobuild/` > `.cobuild/`) |

## Architecture Patterns to Adopt

### 1. Connector Pattern (for Work-Item Systems)

Claude's connector model is a two-layer system:

**Layer 1: Server definition** — where it is, how to auth
**Layer 2: Toolset config** — which operations are enabled

CoBuild should follow this exactly:

```yaml
# .cobuild/pipeline.yaml
connectors:
    work_items:
        type: context-palace          # or "beads", "jira", "linear"
        config:
            host: dev02.brown.chat
            database: contextpalace
            user: agent-m
        # Beads example:
        # type: beads
        # config:
        #     prefix: cb
        #     repo: .
        # Jira example:
        # type: jira
        # config:
        #     url: https://myorg.atlassian.net
        #     token: ${JIRA_TOKEN}
```

The word "connector" is what Claude uses publicly. Not "adapter", not "backend", not "driver". **Connector**.

### 2. Skill Format (already aligned)

Claude Code skills are markdown files with YAML frontmatter:

```yaml
---
name: m-readiness-check
description: "Evaluates design readiness for decomposition"
model: haiku
allowed-tools: Read, Grep, Glob
---

Instructions here...
```

CoBuild's skills already follow this pattern. Key additions from Claude Code we could adopt:

- **`allowed-tools`** — restrict which tools an agent can use during a skill
- **`context: fork`** — run in a forked subagent (maps to our dispatch model)
- **`effort: medium`** — control reasoning depth (maps to our model selection)
- **Dynamic context injection** — `` !`command` `` syntax to inject live data into skills before Claude sees them

### 3. Hook Lifecycle Events

Claude Code defines hooks that fire at lifecycle points. CoBuild should align gate/phase transitions to this model:

| Claude Code Event | CoBuild Equivalent |
|---|---|
| `PreToolUse` | `PreGate` — before a gate evaluates |
| `PostToolUse` | `PostGate` — after a gate passes/fails |
| `SubagentStart` | `PreDispatch` — before dispatching an agent |
| `SubagentStop` | `PostDispatch` / `OnComplete` — agent finished |
| `WorktreeCreate` | Already have this in dispatch |
| `WorktreeRemove` | Already have this in cleanup |
| `SessionStart` | `PipelineStart` |
| `SessionEnd` | `PipelineDone` |
| `TaskCompleted` | `TaskCompleted` (direct match) |

Hook handlers should support Claude Code's four types:
- **command** — run a shell script (exit 0 = pass, exit 2 = block)
- **http** — call an external service
- **prompt** — LLM evaluation
- **agent** — spawn a subagent to verify

### 4. Config Hierarchy (already aligned)

Claude Code uses a 4-scope system. CoBuild already has 3 of these:

| Claude Code Scope | CoBuild Equivalent | Location |
|---|---|---|
| `user` (personal, all projects) | Global | `~/.cobuild/pipeline.yaml` |
| `project` (team, version controlled) | Repo | `<repo>/.cobuild/pipeline.yaml` |
| `local` (project-specific, gitignored) | Local (new) | `<repo>/.cobuild/pipeline.local.yaml` |
| `managed` (enterprise, read-only) | — (future) | — |

**Action:** Add `.cobuild/pipeline.local.yaml` support for developer-specific overrides (gitignored).

### 5. Plugin/Template Distribution

Claude Code distributes plugins via marketplaces (JSON manifests). CoBuild could distribute pipeline templates the same way:

```
my-cobuild-plugin/
    .cobuild-plugin/plugin.json    # metadata
    skills/                         # skill files
    hooks/hooks.json               # hook config
    .cobuild/pipeline.yaml         # default pipeline config
    connectors/                    # connector implementations
```

This is a future concern but worth designing for now.

### 6. Environment Variable Substitution

Claude Code provides `${CLAUDE_PLUGIN_ROOT}` and `${CLAUDE_PLUGIN_DATA}`. CoBuild should provide:

| Variable | Resolves To |
|---|---|
| `${COBUILD_PROJECT_DIR}` | Repo root |
| `${COBUILD_DATA_DIR}` | `~/.cobuild/data/<project>/` |
| `${COBUILD_SKILLS_DIR}` | Resolved skills directory |
| `${COBUILD_DISPATCH}` | `true` when running in dispatch mode |

## Connector Interface Design

Based on Claude's MCP tool pattern, each connector exposes **tools** with:
- **Name** (snake_case): `create_work_item`, `update_status`, `list_work_items`
- **Input schema**: JSON Schema for parameters
- **Annotations**: `readOnlyHint`, `destructiveHint`, `idempotentHint`
- **Handler**: Returns content or error

The Go interface for CoBuild connectors:

```go
// Connector abstracts an external work-item system.
// Implementations: ContextPalaceConnector, BeadsConnector, JiraConnector
type Connector interface {
    // Identity
    Name() string                      // "context-palace", "beads", "jira"

    // CRUD
    GetWorkItem(ctx context.Context, id string) (*WorkItem, error)
    CreateWorkItem(ctx context.Context, req CreateRequest) (string, error)

    // Status + content
    UpdateStatus(ctx context.Context, id, status string) error
    AppendContent(ctx context.Context, id, content string) error

    // Metadata
    SetMetadata(ctx context.Context, id, key string, value any) error
    GetMetadata(ctx context.Context, id, key string) (string, error)
    UpdateMetadata(ctx context.Context, id string, patch map[string]any) error

    // Labels
    AddLabel(ctx context.Context, id, label string) error

    // Relationships
    GetEdges(ctx context.Context, id, direction string, types []string) ([]Edge, error)
    CreateEdge(ctx context.Context, fromID, toID, edgeType string) error

    // Queries
    List(ctx context.Context, filters ListFilters) ([]WorkItem, error)
}
```

### WorkItem struct (normalized across connectors)

```go
type WorkItem struct {
    ID        string
    Title     string
    Content   string
    Type      string            // design, bug, task, review
    Status    string
    Project   string
    Labels    []string
    Metadata  map[string]any
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

### Edge struct

```go
type Edge struct {
    ShardID string              // the related work item
    Title   string
    Type    string              // design, bug, task
    Status  string
}
```

## Beads vs Context Palace: Key Mapping Issues

| Concern | Context Palace | Beads | Resolution |
|---|---|---|---|
| ID format | `cb-a3bf71` (project prefix) | `bd-a1b2c3d4` (hash) | Connector normalizes IDs |
| Content | Single `content` field | `description` + `notes` + `design` + `acceptance` | Map to/from `content` |
| Append | `content = content \|\| $2` | `--append-notes` only | Use notes for append, description for replace |
| Access | Direct SQL (Go) | CLI only (`bd --json`) | Beads connector shells out, parses JSON |
| Pipeline metadata | Stored in shard `metadata` JSONB | Could use Beads `metadata` | **Move to CoBuild's own tables (Option B)** |
| Complex queries | SQL joins | CLI filters + post-processing | Connector handles query translation |
| Edge types | Generic `edge_type` string | Typed: `depends-on`, `blocks`, `relates-to` | Map between systems |

## What Needs to Change in CoBuild

### Phase 1: Extract Connector interface
1. Define `Connector` interface in `internal/connector/connector.go`
2. Create `WorkItem`, `Edge`, `CreateRequest`, `ListFilters` types
3. Implement `CPConnector` wrapping current SQL methods from `client/pipeline.go`

### Phase 2: Move pipeline state to CoBuild tables
1. Move phase, lock, review history from shard `metadata` to `pipeline_runs`
2. Stop using `SetMetadataPath` for pipeline state
3. Poller queries use `pipeline_runs` + connector instead of complex shard metadata SQL

### Phase 3: Implement BeadsConnector
1. Shell-out to `bd` CLI with `--json` flag
2. Parse JSON responses into `WorkItem` structs
3. Map Beads types/statuses to CoBuild's vocabulary

### Phase 4: Config-driven connector selection
1. Add `connectors.work_items` section to pipeline.yaml
2. Factory function creates the right connector from config
3. All commands use connector interface, not direct SQL

## Sources

- Claude Code Plugins Reference: https://code.claude.com/docs/en/plugins-reference
- Claude Code Skills: https://code.claude.com/docs/en/plugins-reference#skills
- Claude Code Hooks: https://code.claude.com/docs/en/hooks
- Claude Code MCP: https://code.claude.com/docs/en/mcp
- Claude Connectors: https://support.claude.com/en/articles/11176164
- MCP Specification: https://modelcontextprotocol.io/
- Beads: https://github.com/steveyegge/beads
