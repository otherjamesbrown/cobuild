---
name: bootstrap-context-layers
description: Discover existing context files and configure phase-aware context layers in pipeline.yaml. Trigger during bootstrap or when updating context configuration.
---

# Skill: Configure Context Layers

Set up context layers for a CoBuild project. Called from the main bootstrap or run independently.

Context layers control what information agents see in each session type (interactive, dispatch, gate evaluation) and pipeline phase (design, implement, review). CoBuild assembles the right context for each situation from reusable pieces.

---

## Step 1: Discover Existing Context

Look for existing context files, architecture docs, and knowledge references:

```bash
# Existing context directories
ls .cobuild/context/ 2>/dev/null
ls .cxp/context/ 2>/dev/null

# Architecture docs
ls ARCHITECTURE.md architecture.md docs/architecture.md 2>/dev/null

# CLAUDE.md (may already have context pointers)
head -50 CLAUDE.md 2>/dev/null

# README
head -30 README.md 2>/dev/null
```

**Do not create new architecture or context docs if they already exist.** Use what's there. CoBuild should point to existing files, not duplicate them.

If `.cxp/context/` exists (old format), those files can be referenced directly — no need to copy them.

---

## Step 2: Ask About Context Sources

> **What context should agents have access to?**
>
> CoBuild assembles a CLAUDE.md for each agent session from context layers. Layers can pull from local files or from your work-item system (Context Palace shards, Beads, etc.).
>
> I found these existing docs:
> - (list discovered files)
>
> **For all agents (always):** Which of these should every agent see? Architecture docs, coding standards, key principles?
>
> **For design phase:** Are there domain knowledge docs (e.g., pipeline specs, API docs) that design-phase agents should have? These can be work-item IDs if they're in your work-item system.
>
> **For implement phase:** Testing standards? Coding conventions? Deploy procedures?
>
> **For review phase:** Any specific review criteria beyond what's in the review skill?

---

## Step 3: Configure Layers in Pipeline YAML

Build the context layers section based on what was discovered and what the developer requested.

### Sources

| Source | Example | What it does |
|--------|---------|-------------|
| `file:<path>` | `file:ARCHITECTURE.md` | Read a local file (relative to repo root) |
| `work-item:<id>` | `work-item:pf-eeb256` | Fetch content via the connector |
| `dispatch-prompt` | — | Task prompt (injected at dispatch) |
| `parent-design` | — | Parent design content (injected at dispatch) |

### When filters

| When | Active for |
|------|-----------|
| `always` | Every session |
| `dispatch` | All dispatched tasks |
| `phase:design` | Design phase only |
| `phase:implement` | Implement phase only |
| `phase:review` | Review phase only |
| `gate:<name>` | Specific gate evaluation |

### Minimal config (files only)

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

### Phase-aware config (with work items from connector)

```yaml
context:
    layers:
        - name: architecture
          source: file:ARCHITECTURE.md
          when: always
        - name: principles
          source: work-item:pf-eeb256
          when: always
        - name: ingest-pipeline
          source: work-item:pf-c66536
          when: phase:design
        - name: testing
          source: work-item:pf-129647
          when: phase:implement
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
        - name: design-context
          source: parent-design
          when: dispatch
```

---

## Verification Checklist

- [ ] Existing context files discovered (not duplicated)
- [ ] Context layers added to pipeline.yaml
- [ ] `file:` sources point to files that exist
- [ ] `work-item:` sources use valid IDs from the project's work-item system
- [ ] Task-prompt and design-context dispatch layers configured
- [ ] Phase-specific layers configured if the developer identified phase-specific context

## Gotchas

<!-- Add failure patterns here as they're discovered -->
