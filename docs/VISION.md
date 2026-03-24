# CoBuild Vision

## The Problem

Building software with AI agents is powerful but chaotic. You can dispatch an agent to write code, but:
- Who reviews it?
- How do you know the design was good enough to implement?
- What happens when the agent stalls or crashes?
- How do you coordinate 5 agents working on related tasks?
- How do you learn from what went wrong?

Every team solves this ad-hoc: custom scripts, manual dispatch, hope-based monitoring.

## The Solution

CoBuild is a **portable, config-driven pipeline** that takes any piece of work from design to deployed code. Drop `.cobuild/` and `skills/` into a repo, and the pipeline works.

```
Design → Review → Decompose → Implement → Review → Deploy → Retrospective
```

Every transition is a **gate** — a configurable quality check that creates an audit trail. The pipeline is defined in YAML. The intelligence lives in markdown skill files. Adding a security review phase is one line of config + one skill file.

## Core Principles

1. **Config over code** — phases, gates, models, context, deploy rules — all YAML
2. **Skills as markdown** — the pipeline's intelligence is in `.md` files, not compiled code
3. **Portable** — drop into any repo, any language, any team
4. **Self-improving** — learns from execution patterns, suggests skill and config updates
5. **Audit everything** — every decision recorded, queryable, traceable
6. **Fail visible** — no silent failures, ever

## What Makes CoBuild Different

### vs. GitHub Actions / CI pipelines
CI runs tests. CoBuild orchestrates the entire design-to-delivery process including the humans and AI agents involved. Gates are quality judgments, not just pass/fail test suites.

### vs. Agent frameworks (CrewAI, AutoGen, etc.)
Agent frameworks help you build agents. CoBuild assumes you already have agents (Claude Code, Cursor, etc.) and provides the pipeline around them — what work to do, in what order, with what context, reviewed by whom.

### vs. Project management tools (Jira, Linear)
PM tools track issues. CoBuild executes them — dispatching agents, reviewing their output, merging their code, deploying it, and learning from the results.

## Architecture

```
                    .cobuild/pipeline.yaml
                           │
                    ┌──────┴──────┐
                    │   CoBuild   │
                    │   Poller    │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
         ┌────┴────┐ ┌────┴────┐ ┌────┴────┐
         │ Design  │ │  Bug    │ │  Task   │
         │ Review  │ │ Fix     │ │ Direct  │
         └────┬────┘ └────┬────┘ └────┬────┘
              │            │            │
              ▼            ▼            ▼
         ┌─────────────────────────────────┐
         │        Stage Gates              │
         │  (audit trail on every pass)    │
         └────────────┬────────────────────┘
                      │
              ┌───────┼───────┐
              │       │       │
         ┌────┴──┐ ┌──┴──┐ ┌─┴────┐
         │Agent 1│ │Agent│ │Agent │
         │(tmux) │ │  2  │ │  3   │
         └───────┘ └─────┘ └──────┘
              │       │       │
              ▼       ▼       ▼
         ┌─────────────────────────────────┐
         │     Review → Merge → Deploy     │
         └─────────────────────────────────┘
              │
              ▼
         ┌─────────────────────────────────┐
         │   Retrospective + Insights      │
         │   (feedback loop)               │
         └─────────────────────────────────┘
```

## Target Users

### Now (v0.1)
- Solo developers using Claude Code with multiple repos
- Small teams where one person defines designs and agents implement

### Next (v0.5)
- Teams of 2-5 developers with shared pipeline config
- Multiple AI providers (Claude, Gemini, Codex)

### Future (v1.0)
- Any team using AI coding assistants
- Plugin marketplace for skills and integrations
- SaaS version with hosted poller and dashboard
