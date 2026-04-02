# Context Layers

Context layers control exactly what information each agent sees per session type. They solve the problem of needing different `CLAUDE.md` content for interactive sessions (human typing) vs dispatched agents (pipeline tasks) vs gate evaluations.

## Quick start

```yaml
# .cobuild/pipeline.yaml
context:
    layers:
        - name: architecture
          source: file:ARCHITECTURE.md
          when: always
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
```

## How it works

When `cobuild dispatch` spawns an agent, it generates a `CLAUDE.md` in the worktree by assembling context layers. Each layer has three fields:

- **`name`** -- identifier for the layer (used in HTML comments for debugging)
- **`source`** -- where the content comes from
- **`when`** -- which session mode activates the layer

Layers are assembled in order. Active layers are joined with `---` separators. Each layer is wrapped in an HTML comment (`<!-- context: name -->`) so you can trace which layer produced which content.

### The problem context layers solve

Without context layers, you face a dilemma:

- Your repo `CLAUDE.md` has identity info, playbooks, and interactive instructions that confuse dispatched agents
- Dispatched agents need the task spec and design context, which interactive sessions do not
- Gate evaluations need specific review criteria that neither interactive nor dispatch sessions need
- Design-phase agents need domain docs (architecture, pipeline specs) that implement-phase agents don't

Context layers let you compose the right context for each situation from reusable pieces.

### The `when` field

| Value | Active when |
|-------|-------------|
| `always` | Every session type (interactive, dispatch, gate) |
| `interactive` | Human is typing in an interactive Claude session |
| `dispatch` | Pipeline dispatched the agent via `cobuild dispatch` |
| `phase:<name>` | Specific pipeline phase (e.g., `phase:design`, `phase:implement`) |
| `gate:<name>` | Only during a specific gate evaluation (e.g. `gate:security-review`) |

An empty `when` field is treated as `always`.

### Source types

| Source | Resolves to | Notes |
|--------|-------------|-------|
| `file:<path>` | Read file from repo | Path relative to repo root |
| `work-item:<id>` | Fetch content via connector | Works with any connector (CP, Beads, etc.) |
| `skills:<name>` | Resolve skill file | Follows skill resolution chain (repo then global) |
| `skills-dir` | Load all `.md` files from skills directory | Optional `filter` list to select specific files |
| `claude-md` | Read the repo's `CLAUDE.md` | Useful when you want it as one layer among many |
| `dispatch-prompt` | Injected task prompt | Only meaningful in dispatch mode |
| `parent-design` | Parent design content | Only meaningful in dispatch mode |
| `hook:<name>` | Deferred to Claude Code hooks | For integration with external hook systems |

### Default behavior (no layers configured)

If `context.layers` is empty or missing:

- **Dispatch mode:** injects the task prompt and parent design content
- **Interactive mode:** loads the repo's `CLAUDE.md`

This means context layers are opt-in. An unconfigured project works the same as before.

## Directory Convention (zero config)

The simplest way to manage context: put markdown files in phase-named directories under `.cobuild/context/`. CoBuild auto-discovers them — no YAML configuration needed.

```
.cobuild/context/
    always/                        # every agent sees these
        anatomy.md                 # auto-generated file index (cobuild scan)
        architecture.md            # system structure, patterns
        principles.md              # hard constraints, rules
        naming-conventions.md      # terminology, style
    design/                        # design phase agents
        domain-ingest.md           # how the ingest pipeline works
        domain-entities.md         # entity model reference
    implement/                     # implementing agents
        sub-agents.md              # available sub-agents and when to use them
        testing-standards.md       # how to write tests for this project
        coding-patterns.md         # patterns to follow, anti-patterns to avoid
    investigate/                   # bug investigation agents
        sub-agents.md              # debugger agent available
        infrastructure.md          # deploy topology, logs, monitoring
        known-fragile-areas.md     # areas that break often and why
    review/                        # review agents
        review-checklist.md        # project-specific review criteria
```

Each `.md` file in a phase directory is loaded as a context layer for that phase. No YAML needed — just create the file and it's included next time an agent is dispatched.

**Combined with YAML layers:** Auto-discovered files load first, then YAML-configured layers. Use YAML for work-item references (`source: work-item:<id>`) since those can't be files on disk. Use directories for static documents.

## Configuration (YAML)

### Layer definition

```yaml
context:
    layers:
        - name: <identifier>
          source: <source-type>
          when: <mode>
          filter: [file1.md, file2.md]    # only for skills-dir source
```

### Source: file

```yaml
- name: architecture
  source: file:ARCHITECTURE.md
  when: always
```

Paths are relative to the repo root. Absolute paths also work.

### Source: work-item

```yaml
- name: principles
  source: work-item:pf-eeb256
  when: always
```

Fetches the work item via the connector (Context Palace, Beads, etc.). The result includes the title as a heading followed by the content. This is connector-agnostic — the same syntax works regardless of which work-item system backs the project.

### Source: skills

```yaml
- name: review-procedure
  source: skills:review/gate-review-pr
  when: gate:review
```

Resolves the skill file using the standard resolution chain (repo `skills/` then `~/.cobuild/skills/`).

### Source: skills-dir

```yaml
- name: all-skills
  source: skills-dir
  when: interactive
  filter: [shared/playbook.md, shared/create-design.md]
```

Loads all `.md` files from the skills directory. The optional `filter` list restricts which files are included.

## Context Strategy

The mechanics are simple — the hard part is deciding what goes where. This section explains how to think about context for each pipeline phase, especially for projects with rich knowledge systems (playbooks, KB shards, sub-agent context docs, architectural principles).

### The key question

For each piece of context, ask: **does this agent need this to do its specific job?**

If the answer is "maybe" — leave it out. You can always add it later when a retrospective shows an agent was missing context. You can't un-waste the tokens from loading context the agent didn't need.

### What to consider for each phase

Use this as a checklist when setting up context. Not everything will apply — pick what's relevant for your project.

**Always (every dispatched agent):**
| Context type | Example | Why |
|-------------|---------|-----|
| Architecture overview | `ARCHITECTURE.md` | Every agent needs to know how the system is structured |
| Hard constraints | Architectural principles doc | Rules that must never be broken (e.g., "all config in DB") |
| Naming conventions | Terminology / etymology guide | Consistent naming across all agents |
| Coding style | Style guide, linting rules | Agents should produce code that fits the codebase |

**Design phase (evaluating designs):**
| Context type | Example | Why |
|-------------|---------|-----|
| Domain knowledge | Pipeline specs, API docs, entity model | Reviewer needs domain expertise to judge completeness |
| Prior designs | Related completed designs | Avoid duplicating solved problems |
| Known limitations | Tech debt, planned deprecations | Don't design against something being removed |

**Decompose phase (breaking designs into tasks):**
| Context type | Example | Why |
|-------------|---------|-----|
| Migration numbering | Last migration number, collision rules | Prevent parallel tasks from picking the same number |
| Repo boundaries | Which code lives in which repo | Multi-repo projects need task-to-repo tagging |
| Test infrastructure | What test tiers exist, how to run them | Tasks need correct acceptance criteria |

**Investigate phase (bug analysis):**
| Context type | Example | Why |
|-------------|---------|-----|
| Known fragile areas | Areas that break often and why | Speeds up root cause identification |
| Infrastructure topology | Deploy topology, log locations, monitoring | Investigator needs to check running systems |
| Available sub-agents | Debugger, domain-specific agents | Investigator may want to delegate |

**Implement phase (writing code):**
| Context type | Example | Why |
|-------------|---------|-----|
| Testing standards | Test tiers, patterns, anti-patterns | Agent needs to write correct tests |
| Available sub-agents | Worker-dev, service-dev, data-dev | Agent can delegate specialised work |
| Build/deploy notes | "Worker uses launchd, not systemd" | Platform-specific gotchas |
| DB patterns | "Use pipeline_operational_config for config" | Project-specific data access patterns |

**Review phase (PR review):**
| Context type | Example | Why |
|-------------|---------|-----|
| Review checklist | Project-specific review criteria | What to check beyond "does it compile" |
| Security policy | Auth patterns, data handling rules | Security-sensitive review criteria |
| Architectural principles | Constraints the implementation must honour | Catch violations at review time, not production |

### What NOT to put in context layers

- **Playbooks and orchestration instructions.** Dispatched agents are NOT the orchestrating agent. Loading your orchestrator's playbook into an implementing agent confuses it with management instructions when it should be writing code.

- **Everything "just in case".** Context window is finite. Every document you load reduces the tokens available for the agent's actual work (reading code, planning, editing). 3 focused documents is better than 15 broad ones.

- **Duplicate information.** If your architecture doc already describes your test infrastructure, don't also load a separate test infrastructure doc. Check for overlap. The same information loaded twice costs double the tokens and can cause contradictory instructions if they drift.

- **Full sub-agent definitions.** Your sub-agents (debugger, worker-dev, etc.) have their own context loaded when Claude Code spawns them from `.claude/agents/`. Don't duplicate their full instructions as context layers. Instead, provide an **inventory** — a short doc listing which sub-agents exist and when to use them, so the dispatched agent knows they're available.

- **Identity or personality.** "You are Mycroft, a senior backend engineer" is for interactive sessions. Dispatched agents get their role from the task prompt and the phase-specific instructions.

- **Large reference documents.** A 50-page API reference wastes context. Instead, give the agent a pointer ("API docs are at docs/api.md — refer to the section on auth endpoints when implementing") and let it read what it needs.

### Building context for a complex project

If your project has a knowledge graph (e.g., Context Palace shards with triggers and relationships), here's how to map it to CoBuild phases:

**Step 1: Identify your always-on context**

What does every agent need regardless of phase? Usually:
- Architecture overview (file structure, patterns, conventions)
- Hard constraints (architectural principles, "all config in DB", etc.)

```yaml
- name: architecture
  source: file:ARCHITECTURE.md
  when: always
- name: principles
  source: work-item:pf-eeb256
  when: always
```

**Step 2: Map domain knowledge to design phase**

Design reviewers need to understand the domain to evaluate whether a design is complete. Which knowledge shards describe the subsystem being changed?

```yaml
- name: ingest-pipeline
  source: work-item:pf-c66536
  when: phase:design
- name: entity-resolution
  source: work-item:pf-637703
  when: phase:design
```

You don't need to load ALL domain shards — just the top-level ones. The design reviewer can follow links from there if needed.

**Step 3: Map implementation context**

Implementing agents need: testing standards, coding patterns, and whatever context helps them write correct code for this specific codebase.

```yaml
- name: testing-standards
  source: work-item:pf-129647
  when: phase:implement
- name: dev-index
  source: work-item:pf-6eac47
  when: phase:implement
```

**Step 4: Investigation context for bugs**

Bug investigators need infrastructure and operational knowledge — how things are deployed, what observability exists, where logs are.

```yaml
- name: infrastructure
  source: work-item:pf-30eb23
  when: phase:investigate
```

**Step 5: Don't configure what CoBuild already provides**

CoBuild automatically injects these — you don't need layers for them:
- `dispatch-prompt` — the task spec (injected at dispatch)
- `parent-design` — the parent design content (injected at dispatch)
- CLAUDE.md — the repo's instructions (loaded by Claude Code itself)

### Interactive vs dispatched: the boundary

Your orchestrating agent (the one YOU talk to) uses CLAUDE.md, hooks, playbooks, and interactive context. That's outside CoBuild's layers.

CoBuild's context layers control what **dispatched agents** see — the ones spawned by `cobuild dispatch` into worktrees. They don't see your CLAUDE.md, your hooks, or your playbook. They see:
1. Context layers with `when: always` or `when: dispatch`
2. Phase-specific layers (`when: phase:implement`)
3. The task prompt and parent design

This is intentional. Dispatched agents should be focused on their specific task, not loaded with orchestration context.

### Growing context over time

Start minimal:
```yaml
context:
    layers:
        - name: architecture
          source: file:ARCHITECTURE.md
          when: always
```

After your first pipeline run, the retrospective will tell you what agents were missing:
- "Agent didn't follow testing conventions" → add testing standards to `phase:implement`
- "Design reviewer missed an architectural constraint" → add principles to `always`
- "Investigator didn't know about the deploy topology" → add infra to `phase:investigate`

Each retrospective finding becomes a context layer. Don't try to get it right upfront — iterate.

## Examples

### Example 1: Basic project (files only)

For a simple project that just needs CLAUDE.md and an architecture doc:

```yaml
context:
    layers:
        - name: architecture
          source: file:ARCHITECTURE.md
          when: always
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
        - name: design-context
          source: parent-design
          when: dispatch
```

### Example 2: Phase-aware context (work items from connector)

For a complex project where different phases need different domain context:

```yaml
context:
    layers:
        # Always — every agent sees these
        - name: architecture
          source: file:ARCHITECTURE.md
          when: always
        - name: principles
          source: work-item:pf-eeb256
          when: always

        # Design phase — domain knowledge for evaluating designs
        - name: ingest-pipeline
          source: work-item:pf-c66536
          when: phase:design
        - name: infra
          source: work-item:pf-30eb23
          when: phase:design

        # Implement phase — coding standards and testing
        - name: testing
          source: work-item:pf-129647
          when: phase:implement

        # Dispatch — task prompt and parent design
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
        - name: design-context
          source: parent-design
          when: dispatch
```

What this achieves:

- **Architecture and principles** are always visible -- every agent knows the codebase structure and hard constraints
- **Design-phase agents** get domain knowledge docs (ingest pipeline, infrastructure) to evaluate designs in context
- **Implement-phase agents** get testing standards so they write proper tests
- **Dispatched agents** get their task spec and parent design for focused implementation

### Example 3: Gate-specific context

Load security policies only during the security review gate:

```yaml
context:
    layers:
        - name: architecture
          source: file:ARCHITECTURE.md
          when: always
        - name: security-policy
          source: file:SECURITY.md
          when: gate:security-review
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
```

## Gotchas

**Context volume directly impacts agent performance.** Every token of context reduces the tokens available for the agent's work. A dispatched agent with 50K tokens of context has less room for code reads, planning, and edits than one with 10K. Measure: if agents are compacting frequently or producing shallow work, you may be loading too much context.

**Duplicate context causes contradictions.** If your architecture doc says "use PostgreSQL" and a domain knowledge shard says "we're migrating to SQLite", the agent gets contradictory instructions. Before adding a new layer, grep for overlap with existing layers. One authoritative source per topic is better than three partial ones.

**The `always` bucket grows silently.** Every document you add to `always` loads for every dispatched agent — design reviewers, implementers, investigators, everyone. After a few months you may have 10 documents in `always` that were each "just one more small doc." Audit `always` periodically — if something is only needed for one or two phases, move it.

**Work-item content changes but your context doesn't.** If you reference `work-item:pf-eeb256` as architectural principles, and someone updates that shard, agents automatically get the new version. This is a feature — but it means a badly edited shard can silently degrade agent performance. Pin to specific versions if your work-item system supports it, or use `file:` sources for stable documents.

**Directory-discovered files have no ordering.** Files in `.cobuild/context/always/` load in filesystem order (alphabetical). If ordering matters (e.g., architecture before principles), prefix with numbers: `01-architecture.md`, `02-principles.md`.

**The prompt IS context too.** The task prompt (from decomposition) and the parent design are already loaded via `dispatch-prompt` and `parent-design`. Don't also load the design as a `work-item:` layer — you'll have it twice. CoBuild handles the task-specific context automatically.

**Sub-agent inventory, not instructions.** For projects with sub-agents (`.claude/agents/`), create a short inventory doc listing what's available, not a copy of each agent's full instructions. The inventory goes in `.cobuild/context/implement/sub-agents.md`:

```markdown
# Available Sub-Agents

| Agent | Expertise | Use when |
|-------|-----------|----------|
| debugger | Root cause analysis | Investigating failures, read-only |
| worker-dev | Temporal workflows | Worker service modifications |
| service-dev | gRPC, protobuf | Gateway/service layer changes |
| data-dev | Migrations, schema | Database schema work |
```

The implementing agent reads this, decides whether to spawn a sub-agent, and Claude Code loads the sub-agent's full instructions from `.claude/agents/` at spawn time.

## Troubleshooting

**Layer not appearing in generated CLAUDE.md:** Check the `when` field matches the session mode and phase. Dispatch mode only loads layers with `when: dispatch`, `when: always`, or matching `when: phase:<name>`. Look for HTML comments (`<!-- context: name -->`) in the generated file to see which layers were included.

**File layer returning empty:** The `source: file:<path>` is relative to the repo root, not the worktree. If the file does not exist, the layer is silently skipped (a comment is inserted). Check the path exists from the repo root.

**Work-item layer failing:** Ensure the work-item ID is valid and accessible via the configured connector. Failed fetches produce an HTML comment with the error message in the generated CLAUDE.md rather than failing the entire assembly.
