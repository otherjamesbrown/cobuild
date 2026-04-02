# Project Anatomy

Auto-generated file index. Use this to understand the codebase without reading every file.
Token estimates help you decide what's worth reading vs what you can skip.

## Root (~8.1K tokens)

- **.cobuild.yaml** (3 lines, ~9 tok) — .cobuild.yaml
- **CLAUDE.md** (203 lines, ~2.5K tok) — CoBuild
- **README.md** (364 lines, ~3.3K tok) — CoBuild
- **go.mod** (24 lines, ~182 tok) — go.mod
- **go.sum** (90 lines, ~2.1K tok) — go.sum

## .cobuild/retrospectives/ (~4.4K tokens)

- **cb-e9562d.md** (145 lines, ~1.9K tok) — Pipeline Retrospective: cb-e9562d
- **cobuild-build-session-2026-03-24-29.md** (183 lines, ~2.5K tok) — CoBuild Build Session Retrospective

## cmd/cobuild/ (~29 tokens)

- **main.go** (8 lines, ~29 tok) — Go package: main

## docs/ (~9.0K tokens)

- **BACKLOG.md** (50 lines, ~430 tok) — CoBuild Backlog
- **VISION.md** (178 lines, ~2.3K tok) — CoBuild Vision
- **bootstrap-template.md** (45 lines, ~278 tok) — CoBuild Local Bootstrap — Template
- **cobuild.md** (734 lines, ~6.0K tok) — CoBuild

## docs/guides/ (~16.1K tokens)

- **audit-trail.md** (220 lines, ~2.0K tok) — Audit Trail
- **config.md** (310 lines, ~2.2K tok) — Config-Driven Pipeline
- **context-layers.md** (440 lines, ~5.1K tok) — Context Layers
- **feedback-loop.md** (212 lines, ~1.8K tok) — Self-Improving Pipeline
- **models.md** (201 lines, ~1.5K tok) — Per-Phase Model Selection
- **multi-project.md** (254 lines, ~1.6K tok) — Multi-Project Support
- **skills.md** (214 lines, ~1.8K tok) — Skills as Markdown

## examples/ (~2.1K tokens)

- **cobuild.yaml** (9 lines, ~145 tok) — .cobuild.yaml — project identity file
- **pipeline-minimal.yaml** (31 lines, ~196 tok) — Minimal CoBuild pipeline config — just the essentials.
- **pipeline.yaml** (143 lines, ~1.8K tok) — Example CoBuild pipeline configuration

## hooks/ (~3.0K tokens)

- **cobuild-event.sh** (170 lines, ~2.3K tok) — cobuild-event.sh in hooks/
- **hooks.json** (101 lines, ~721 tok) — hooks.json in hooks/

## internal/client/ (~17.6K tokens)

- **client.go** (221 lines, ~1.5K tok) — Go package: client
- **format.go** (80 lines, ~442 tok) — Go package: client
- **insights.go** (328 lines, ~2.7K tok) — Go package: client
- **pipeline.go** (1172 lines, ~9.4K tok) — Go package: client
- **runs.go** (410 lines, ~3.5K tok) — Go package: client

## internal/cmd/ (~57.3K tokens)

- **admin.go** (16 lines, ~78 tok) — Go package: cmd
- **admin_cleanup.go** (219 lines, ~1.7K tok) — Go package: cmd
- **admin_health.go** (238 lines, ~1.8K tok) — Go package: cmd
- **admin_stats.go** (78 lines, ~502 tok) — Go package: cmd
- **admin_stuck.go** (123 lines, ~907 tok) — Go package: cmd
- **admin_tokens.go** (172 lines, ~1.6K tok) — Go package: cmd
- **admin_waste.go** (221 lines, ~1.8K tok) — Go package: cmd
- **complete.go** (242 lines, ~2.3K tok) — Go package: cmd
- **deploy.go** (392 lines, ~3.3K tok) — Go package: cmd
- **dispatch.go** (617 lines, ~6.6K tok) — Go package: cmd
- **explain.go** (272 lines, ~2.3K tok) — Go package: cmd
- **gatelogic.go** (149 lines, ~1.1K tok) — Go package: cmd
- **helpers.go** (93 lines, ~559 tok) — Go package: cmd
- **improve.go** (184 lines, ~1.5K tok) — Go package: cmd
- **init_skills.go** (227 lines, ~1.7K tok) — Go package: cmd
- **insights.go** (131 lines, ~1.0K tok) — Go package: cmd
- **merge.go** (169 lines, ~1.4K tok) — Go package: cmd
- **merge_design.go** (220 lines, ~1.7K tok) — Go package: cmd
- **nextstep.go** (108 lines, ~910 tok) — Go package: cmd
- **pipeline.go** (705 lines, ~5.8K tok) — Go package: cmd
- **poller.go** (336 lines, ~2.8K tok) — Go package: cmd
- **retro.go** (108 lines, ~872 tok) — Go package: cmd
- **root.go** (142 lines, ~1.3K tok) — Go package: cmd
- **run.go** (77 lines, ~659 tok) — Go package: cmd
- **scan.go** (333 lines, ~2.5K tok) — Go package: cmd
- **setup.go** (253 lines, ~2.0K tok) — Go package: cmd
- **setup_agents.go** (422 lines, ~4.9K tok) — Go package: cmd
- **status.go** (61 lines, ~372 tok) — Go package: cmd
- **wait.go** (138 lines, ~1.1K tok) — Go package: cmd
- **workitem.go** (323 lines, ~2.4K tok) — Go package: cmd

## internal/config/ (~7.8K tokens)

- **config.go** (637 lines, ~5.6K tok) — Go package: config
- **context.go** (267 lines, ~2.2K tok) — Go package: config

## internal/connector/ (~7.0K tokens)

- **beads.go** (395 lines, ~2.8K tok) — Go package: connector
- **connector.go** (119 lines, ~1.4K tok) — Package connector defines the interface for external work-item systems.
- **cp.go** (330 lines, ~2.5K tok) — Go package: connector
- **factory.go** (41 lines, ~280 tok) — Go package: connector

## internal/merge/ (~5.8K tokens)

- **analyse.go** (135 lines, ~1.1K tok) — Package merge provides conflict analysis, supersession detection,
- **execute.go** (196 lines, ~1.9K tok) — Go package: merge
- **plan.go** (175 lines, ~1.3K tok) — Go package: merge
- **supersede.go** (173 lines, ~1.5K tok) — Go package: merge

## internal/store/ (~7.9K tokens)

- **factory.go** (35 lines, ~254 tok) — Go package: store
- **postgres.go** (648 lines, ~5.6K tok) — Go package: store
- **store.go** (62 lines, ~777 tok) — Package store defines the interface for CoBuild's own data persistence.
- **types.go** (131 lines, ~1.3K tok) — Go package: store

## internal/worktree/ (~1.8K tokens)

- **worktree.go** (175 lines, ~1.8K tok) — Package worktree manages git worktrees for pipeline task dispatch.

## migrations/ (~842 tokens)

- **001_pipeline_gates.sql** (49 lines, ~586 tok) — Pipeline operational state tables
- **002_drop_design_id_fk.sql** (14 lines, ~256 tok) — +goose Up

## research/ (~24.2K tokens)

- **anthropic-skills-best-practices.md** (492 lines, ~5.5K tok) — Lessons from Building Claude Code: How We Use Skills
- **claude-code-best-practices.md** (371 lines, ~3.8K tok) — Best Practices for Claude Code
- **claude-patterns.md** (250 lines, ~2.5K tok) — Claude Code / CoWork Patterns for CoBuild
- **cobuild-ontology.md** (307 lines, ~2.9K tok) — CoBuild Design Ontology Spec (DOS)
- **competitive-landscape.md** (179 lines, ~2.7K tok) — Competitive Landscape: AI Agent Orchestration Tools
- **design-file-store.md** (200 lines, ~1.8K tok) — Design: File-Based Store (YAML + JSONL)
- **design-sqlite-store.md** (136 lines, ~1.1K tok) — Design: SQLite Store
- **the-ontology-layer-of-design.md** (206 lines, ~4.1K tok) — The Ontology Layer of Design

## skills/decompose/ (~2.0K tokens)

- **decompose-design.md** (192 lines, ~2.0K tok) — Break a design into implementable tasks with dependency ordering and wave assignment. Trigger after the readiness gate passes and the pipeline advances to the decompose phase.

## skills/design/ (~1.6K tokens)

- **gate-readiness-review.md** (104 lines, ~1.1K tok) — Evaluate whether a design is ready for decomposition. Trigger on design review, readiness gate, or when a design reaches the design phase.
- **implementability.md** (47 lines, ~520 tok) — Check whether a design is specific enough for an agent to implement without asking questions. Called as part of the readiness review gate.

## skills/done/ (~653 tokens)

- **gate-retrospective.md** (106 lines, ~653 tok) — Review a completed pipeline to extract lessons learned and suggest improvements. Trigger when a design reaches the done phase.

## skills/implement/ (~1.7K tokens)

- **dispatch-task.md** (83 lines, ~732 tok) — Dispatch tasks to implementing agents and monitor until complete. Trigger when tasks are ready for implementation.
- **stall-check.md** (137 lines, ~985 tok) — Diagnose a task that may be stalled, crashed, or rate-limited. Trigger on health check, stall detection, or agent crash.

## skills/investigate/ (~1.4K tokens)

- **bug-investigation.md** (152 lines, ~1.4K tok) — Investigate a bug to identify root cause, affected areas, and produce a fix spec. Trigger when a bug enters the pipeline or when investigation is needed before implementation.

## skills/review/ (~2.2K tokens)

- **gate-process-review.md** (146 lines, ~1.2K tok) — Process external review feedback (Gemini, CI) on a task PR and decide approve, request-changes, or escalate. Trigger when a PR has external review results.
- **gate-review-pr.md** (73 lines, ~551 tok) — Review a pull request against its task spec and parent design. Trigger when an agent-based review is needed for a task PR.
- **merge-and-verify.md** (78 lines, ~484 tok) — Merge an approved PR, run post-merge tests, and auto-revert on failure. Trigger after a task PR is approved.

## skills/shared/ (~13.5K tokens)

- **bootstrap-claude-md.md** (172 lines, ~1.6K tok) — Generate .cobuild/AGENTS.md with pipeline instructions and add a CoBuild pointer to CLAUDE.md. Final bootstrap step.
- **bootstrap-connector-beads.md** (102 lines, ~540 tok) — Configure and verify the Beads connector for CoBuild. Trigger when setting up a project that uses Beads for work items.
- **bootstrap-connector-cp.md** (106 lines, ~684 tok) — Configure and verify the Context Palace connector for CoBuild. Trigger when setting up a project that uses Context Palace for work items.
- **bootstrap-context-layers.md** (137 lines, ~1.1K tok) — Discover existing context files and configure phase-aware context layers in pipeline.yaml. Trigger during bootstrap or when updating context configuration.
- **bootstrap-skills.md** (109 lines, ~891 tok) — Copy and customize pipeline skills for a CoBuild project. Trigger during bootstrap or when refreshing skills.
- **bootstrap-storage-postgres.md** (129 lines, ~883 tok) — Configure and verify Postgres as CoBuild's pipeline data store. Trigger when setting up storage for a new project.
- **bootstrap.md** (300 lines, ~2.9K tok) — Set up CoBuild on a new project. Interactive walkthrough of connector, storage, context layers, skills, and agent instructions. Trigger on "set up cobuild", "bootstrap", "configure pipeline".
- **create-design.md** (151 lines, ~1.3K tok) — Create a well-formed design work item that will pass the readiness review gate. Trigger on "create design", "write a design", "new design".
- **design-review.md** (127 lines, ~1.3K tok) — Review a design for pipeline readiness. Pre-flight check before submitting to CoBuild. Trigger on "review design", "design review", "is this ready".
- **playbook.md** (279 lines, ~2.3K tok) — Pipeline orchestration decision tree. Trigger when a pipeline event occurs — phase transition, gate result, task completion, or health check.

---

104 files, ~196.2K tokens total
