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

For each piece of context, ask: **which phase needs this to do its job?**

| Context | Who needs it | When |
|---------|-------------|------|
| Architecture docs | Everyone — how the system is built | `always` |
| Architectural principles / constraints | Design reviewers and implementing agents | `always` |
| Domain knowledge (pipeline specs, API docs) | Design reviewers — need domain context to evaluate | `phase:design` |
| Testing standards | Implementing agents — need to know how to test | `phase:implement` |
| Playbook / orchestration instructions | The orchestrating agent (interactive sessions) | `interactive` |
| Task prompt | Dispatched agents only | `dispatch` |
| Parent design | Dispatched agents need the big picture | `dispatch` |
| Sub-agent context (dev-index, domain guides) | Only when that specific sub-agent is spawned | Not a layer — loaded by the agent itself |

### What NOT to put in context layers

- **Playbooks and interactive-only instructions.** Dispatched agents are NOT the orchestrating agent. Don't load your orchestrator's playbook into an implementing agent's context — it'll confuse it with orchestration instructions when it should be writing code.
- **All your knowledge shards.** Context window is finite. Loading 20 KB shards wastes tokens. Pick the 2-3 most relevant per phase.
- **Sub-agent context.** Your sub-agents (debugger, worker-dev, service-dev) have their own context loaded when they're spawned. CoBuild dispatch handles its own context injection — don't duplicate it.
- **Identity/personality.** "You are Mycroft" is for interactive sessions. Dispatched agents get their role from the task prompt.

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

## Troubleshooting

**Layer not appearing in generated CLAUDE.md:** Check the `when` field matches the session mode and phase. Dispatch mode only loads layers with `when: dispatch`, `when: always`, or matching `when: phase:<name>`. Look for HTML comments (`<!-- context: name -->`) in the generated file to see which layers were included.

**File layer returning empty:** The `source: file:<path>` is relative to the repo root, not the worktree. If the file does not exist, the layer is silently skipped (a comment is inserted). Check the path exists from the repo root.

**Work-item layer failing:** Ensure the work-item ID is valid and accessible via the configured connector. Failed fetches produce an HTML comment with the error message in the generated CLAUDE.md rather than failing the entire assembly.
