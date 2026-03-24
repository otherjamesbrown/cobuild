# CoBuild Vision

## The Problem

AI coding tools are powerful but chaotic. You can point Claude Code at a task and get working code back. But beyond single-shot fixes, things fall apart:

- A design goes straight to implementation without anyone checking if it's complete enough to build
- Three agents work on related tasks and create merge conflicts on every PR
- An agent stalls at 2am and nobody notices until morning
- The same architectural mistake keeps happening because agents don't learn from previous runs
- There's no record of what was reviewed, what passed, or why something was approved

Tools exist at both extremes. Ralph (a 100-line bash loop) proves developers want simplicity — but it has no quality gates, no review, no parallelism. Gastown (70 Go packages, 25+ novel terms, Dolt database) solves every coordination problem — but most developers never get past the README. gstack provides 28 well-crafted skills — but no pipeline state, no enforcement, no audit trail.

The gap: **something you can start using in minutes that actually enforces quality as work scales.**

## The Solution

CoBuild is a **config-driven pipeline framework** that takes work from design to deployed code through structured phases with quality gates.

```
Design  →  Decompose  →  Implement  →  Review  →  Done
  ↑ gate      ↑ gate                     ↑ gate    ↑ gate
```

Every phase transition is a **gate** — a quality check that must pass before work moves forward. Every gate verdict is recorded. Every dispatch is tracked. The pipeline is defined in YAML. The intelligence lives in markdown skill files.

### Simple to start

A basic project needs three things:
1. `.cobuild.yaml` — project name and prefix
2. `.cobuild/pipeline.yaml` — build commands and context layers
3. A CLAUDE.md pointer to `.cobuild/AGENTS.md`

No database required for basic use. No novel terminology. No infrastructure beyond the `cobuild` binary.

### Configurable to grow

As the project gets more complex:
- Add **phase-aware context layers** that give design agents domain knowledge and implement agents testing standards
- Pull context from your **work-item system** (Context Palace, Beads, or future Jira/Linear) via connectors
- Configure **multi-repo designs** where tasks are tagged with their target repo during decomposition
- Add **deploy services** with trigger paths so only affected services redeploy
- Use **gate skills** to define project-specific quality criteria
- Store pipeline state in **Postgres** for audit trails and insights

## Core Principles

1. **Config over code** — adding a phase, gate, or reviewer is a YAML change, not a code change
2. **Skills as markdown** — the pipeline's intelligence lives in `.md` files, not compiled code
3. **Connector, not owner** — CoBuild connects to your existing work-item system. It never owns work items.
4. **Audit everything** — every gate verdict, every dispatch, every completion recorded and queryable
5. **Fail visible** — no silent failures. If something goes wrong, it's in the audit trail.
6. **Self-improving** — `cobuild insights` detects patterns, `cobuild improve` suggests fixes
7. **Claude-native patterns** — uses Claude Code terminology (connector, skill, hook, scope), not novel jargon

## Architecture

### Three layers

```
┌─────────────────────────────────────────┐
│              Skills (markdown)           │  What agents do
│  design/ implement/ review/ done/       │  Intelligence lives here
└─────────────────────┬───────────────────┘
                      │
┌─────────────────────┴───────────────────┐
│           Pipeline (cobuild CLI)         │  Orchestration
│  phases, gates, dispatch, audit          │  State lives here
└──────┬──────────────────────┬───────────┘
       │                      │
┌──────┴──────┐       ┌──────┴──────┐
│  Connector  │       │    Store    │      External systems
│  (CP/Beads) │       │  (Postgres) │      Data lives here
└─────────────┘       └─────────────┘
```

**Connector** — bridges to an external work-item system where designs, bugs, and tasks live. CoBuild reads and writes through the connector. Implementations: Context Palace (`cxp` CLI), Beads (`bd` CLI), future Jira/Linear.

**Store** — where CoBuild keeps its own orchestration data: pipeline runs, gate audit records, task tracking. Currently Postgres, with SQLite and file-based stores designed.

**Skills** — markdown files organized by phase that tell agents what to do. Gate skills define evaluation criteria. Phase skills define procedures.

### Seven core objects

| Object | What it is | Where it lives |
|--------|-----------|---------------|
| **WorkItem** | A unit of work (design, bug, task) | Connector (external) |
| **Pipeline** | Orchestration of a WorkItem through phases | CoBuild's database |
| **Phase** | A named stage (design, decompose, implement, review, done) | Config |
| **Gate** | Quality check at phase boundaries | Config + CoBuild's database |
| **Skill** | Markdown instructions for an agent | Filesystem |
| **Agent** | Ephemeral AI worker in a worktree | Runtime |
| **Connector** | Bridge to external work-item system | Config |

The critical boundary: **WorkItem** lives in the Connector. **Pipeline** lives in CoBuild. Don't mix them.

### Context layers

Agents get different context depending on what they're doing:

```yaml
context:
    layers:
        - name: architecture
          source: file:ARCHITECTURE.md
          when: always                    # every agent sees this
        - name: domain-knowledge
          source: work-item:pf-c66536
          when: phase:design              # design agents get domain docs
        - name: testing-standards
          source: work-item:pf-129647
          when: phase:implement           # implement agents get test guidance
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch                  # dispatched agents get their task
```

Sources are connector-agnostic (`work-item:<id>` works with any backend). Filters are phase-aware. Basic projects use `file:` sources only. Complex projects pull from the work-item system.

### Work-item CLI

One command for all connectors:

```bash
cobuild wi show <id>              # works with CP, Beads, future Jira
cobuild wi list --type design
cobuild wi links <id>
cobuild wi status <id> closed
cobuild wi create --type task --title "..."
```

Skills use `cobuild wi` commands — they work regardless of which work-item system backs the project.

## What Makes CoBuild Different

### vs. Ralph / simple loops
Ralph proves the pattern works. CoBuild adds what Ralph lacks: quality gates between phases, parallel dispatch, structured review, and an audit trail. But CoBuild's basic mode should feel almost as simple.

### vs. gstack / skill collections
gstack has great skills but no pipeline state. You can skip review and go straight to ship. CoBuild enforces phase transitions — a gate must pass before work moves forward.

### vs. Gastown / multi-agent platforms
Gastown solves every coordination problem at once. CoBuild solves them incrementally. You don't need to learn 25 terms or install Dolt to get started. Grow into complexity as your project demands it.

### vs. CI/CD pipelines
CI runs tests. CoBuild orchestrates the entire design-to-delivery process including the AI agents involved. Gates are quality judgments, not just pass/fail test suites.

### vs. Project management tools (Jira, Linear)
PM tools track issues. CoBuild executes them — dispatching agents, reviewing their output, merging their code, and learning from the results.

### vs. Agent frameworks (CrewAI, AutoGen)
Agent frameworks help you build agents. CoBuild assumes you already have agents (Claude Code, Cursor, etc.) and provides the pipeline around them.

## Target Users

### Now (v0.1)
- Solo developers using Claude Code with one or two repos
- Anyone who wants structured quality gates around AI-generated code
- Teams where one person defines designs and agents implement

### Next (v0.5)
- Teams of 2-5 developers with shared pipeline config
- Multiple AI providers (Claude, Gemini, Codex)
- SQLite store for zero-infrastructure setup

### Future (v1.0)
- Any team using AI coding assistants
- Plugin marketplace for skills and connectors
- File-based store for maximum portability
- SaaS version with hosted poller and dashboard

## Design Philosophy

**The right amount of complexity is the minimum needed for the current task.**

A developer bootstrapping CoBuild on a personal project should be done in minutes. A team running 10 concurrent agents across 3 repos should be able to configure that without switching tools. The same framework serves both — the difference is config, not code.
