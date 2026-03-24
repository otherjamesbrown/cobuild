# CoBuild Design Ontology Spec (DOS)

```yaml
dos_version: 0.1
product: CoBuild — config-driven pipeline that turns designs into deployed code
```

## 1. Core Objects (Nouns)

Seven primitives. Everything in CoBuild is one of these or composed from them.

```yaml
objects:

  WorkItem:
    description: A unit of work that exists in an external system
    examples: design, bug, task, review, outcome
    properties:
      id: string (system-prefixed, e.g. cb-a3bf71, bd-1f2e3d)
      title: string
      content: string
      type: enum (design, bug, task, review, outcome)
      status: string
      labels: string[]
      metadata: map
    lives_in: Connector (not CoBuild's database)
    note: >
      CoBuild does not own work items. It reads and writes them through
      a Connector. The same work item might be a CP shard, a Beads issue,
      or a Jira ticket. CoBuild doesn't care.

  Pipeline:
    description: The orchestration of a single work item through phases
    properties:
      id: string
      work_item_id: string (foreign key to a WorkItem)
      current_phase: Phase
      status: enum (active, completed, failed, paused)
      created_at: timestamp
      updated_at: timestamp
    lives_in: CoBuild's database
    note: >
      One Pipeline per WorkItem. The Pipeline is CoBuild's record of
      "where is this work item in the process?" It does NOT store work
      item content — only orchestration state.

  Phase:
    description: A named stage in a pipeline's lifecycle
    examples: design, decompose, implement, review, deploy, done
    properties:
      name: string
      model: string (haiku, sonnet, opus)
      gates: Gate[]
    lives_in: Config (pipeline.yaml)
    note: >
      Phases are defined in config, not code. The sequence of phases
      depends on the WorkItem type (design gets all phases, bug skips
      design+decompose). This mapping is called a Workflow.

  Gate:
    description: A quality check that must pass before a phase transition
    properties:
      name: string
      skill: Skill
      verdict: enum (pass, fail)
      round: int
      body: string (findings)
    lives_in: Config (definition) + CoBuild's database (records)
    note: >
      Gates enforce quality. Every gate evaluation is recorded as an
      audit entry — who ran it, what they found, pass or fail, which
      round. Gates are the audit trail.

  Skill:
    description: A markdown file with instructions that tells an agent what to do
    properties:
      name: string (kebab-case)
      description: string
      model: string
      allowed_tools: string[]
      content: markdown
    lives_in: Filesystem (skills/ directory)
    format: YAML frontmatter + markdown body (same as Claude Code skills)
    note: >
      Skills are the intelligence of the pipeline. They are NOT code.
      A non-technical person should be able to read a skill file and
      understand what the agent will do.

  Agent:
    description: An AI worker that executes skills in isolated environments
    properties:
      name: string (e.g. agent-steve, agent-mycroft)
      domains: string[] (what they're good at)
      model: string
    lives_in: Config (pipeline.yaml agents section)
    note: >
      Agents are ephemeral. They spawn, do one thing, and exit.
      They don't hold state — the Pipeline and WorkItem hold state.
      An agent that crashes can be replaced by another.

  Connector:
    description: A bridge between CoBuild and an external work-item system
    examples: context-palace, beads, jira, linear
    properties:
      type: string
      config: map (system-specific connection details)
    lives_in: Config (pipeline.yaml connectors section)
    note: >
      The Connector is how CoBuild talks to the outside world.
      It reads work items, updates statuses, creates relationships.
      CoBuild never touches the external system's database directly —
      everything goes through the Connector interface.
```

## 2. Relationships (Verbs)

```yaml
relationships:
  # Pipeline ↔ WorkItem
  - Pipeline ORCHESTRATES WorkItem        # one pipeline per work item
  - Pipeline READS WorkItem               # via Connector
  - Pipeline WRITES_STATUS WorkItem       # via Connector

  # Phase ↔ Pipeline
  - Pipeline PROGRESSES_THROUGH Phase     # sequential, one at a time
  - Phase CONTAINS Gate                   # zero or more gates per phase

  # Gate ↔ Pipeline
  - Gate EVALUATES Pipeline               # produces a verdict
  - Gate RECORDS_TO Pipeline              # audit trail entry
  - Gate ADVANCES Pipeline                # on pass, moves to next phase

  # Skill ↔ Gate/Agent
  - Gate USES Skill                       # skill defines evaluation criteria
  - Agent EXECUTES Skill                  # agent follows skill instructions

  # Agent ↔ Pipeline
  - Pipeline DISPATCHES Agent             # into a worktree, via tmux
  - Agent COMPLETES WorkItem              # pushes code, creates PR, marks done

  # WorkItem ↔ WorkItem (via Connector)
  - WorkItem BLOCKS WorkItem              # dependency
  - WorkItem CHILD_OF WorkItem            # hierarchy (task child-of design)
  - WorkItem RELATES_TO WorkItem          # informational link

  # Connector ↔ everything external
  - Connector PROVIDES WorkItem           # CoBuild reads through it
  - Connector RECEIVES updates            # CoBuild writes through it
```

## 3. Rules (Constraints)

```yaml
rules:
  # Pipeline
  - A WorkItem has at most one active Pipeline
  - A Pipeline must have a Connector to read/write its WorkItem
  - A Pipeline can only be in one Phase at a time
  - Phase transitions require all Gates in the current Phase to pass

  # Gates
  - Every Gate evaluation must be recorded (no silent passes or failures)
  - A Gate can be evaluated multiple rounds (fail → iterate → retry)
  - Gate logic lives in Skills, not in code

  # Agents
  - An Agent works in an isolated worktree (never on main)
  - An Agent is ephemeral — crash it anytime, another can continue
  - An Agent's output is committed code, not internal state

  # Connectors
  - CoBuild never bypasses the Connector to access work items directly
  - Pipeline orchestration state lives in CoBuild, not in the Connector
  - A Connector must normalize external data into WorkItem shape

  # Config
  - Phase names, gate logic, and workflows are defined in config, never hardcoded
  - Config follows scope precedence: global < repo < local
  - Adding a phase or gate should require only a YAML change
```

## 4. Composition (How things combine)

```yaml
composition:

  Workflow:
    description: A sequence of Phases for a specific WorkItem type
    structure: WorkItem.type → Phase[]
    examples:
      design: [design, decompose, implement, review, done]
      bug: [implement, review, done]
      task: [implement, review, done]
    note: Workflows are the "shape" of a Pipeline for a given work type.

  Wave:
    description: A group of tasks dispatched together within a Phase
    derived_from:
      - Tasks created during decomposition
      - Dependency ordering (wave 1 has no blockers, wave 2 blocked by wave 1)
    structure: Phase(implement) contains Wave[] contains WorkItem(task)[]
    note: >
      Waves are an implementation detail of the implement phase.
      Serial wave strategy: merge wave N before dispatching wave N+1.
      Parallel wave strategy: dispatch all, rebase as needed.

  AuditTrail:
    description: Complete history of all gate evaluations for a Pipeline
    derived_from:
      - Gate records (verdict, round, reviewer, findings)
      - Phase transitions (timestamp, from, to)
      - Dispatch records (agent, worktree, timestamp)
    note: The audit trail is CoBuild's memory. It answers "what happened and why?"

  Context:
    description: The assembled information an Agent sees during a session
    derived_from:
      - Skill content (what to do)
      - WorkItem content (the work itself)
      - Parent WorkItem content (design context for a task)
      - Config layers (architecture docs, identity, etc.)
    note: >
      Context is assembled at dispatch time from context layers defined
      in config. Different modes (dispatch, interactive, gate) see
      different layers.
```

## 5. Provenance Maps

How the core objects change over time.

```yaml
provenance:

  WorkItem:
    - "created in → Connector (external system)"
    - "discovered by → Poller (trigger: new-design)"
    - "orchestrated by → Pipeline (init)"
    - "evaluated by → Gate (readiness-review)"
    - "decomposed into → child WorkItems (tasks)"
    - "dispatched to → Agent (in worktree)"
    - "completed by → Agent (PR created)"
    - "reviewed by → Gate (review phase)"
    - "merged to → main branch"
    - "closed in → Connector"

  Pipeline:
    - "created from → WorkItem (on init)"
    - "advanced by → Gate (on pass)"
    - "blocked by → Gate (on fail) or WorkItem dependency"
    - "locked by → Agent session (5-min TTL)"
    - "completed when → final Phase reached"

  Gate:
    - "triggered by → Phase transition or Poller"
    - "evaluated by → Agent using Skill"
    - "recorded to → Pipeline audit trail"
    - "if pass → Pipeline advances"
    - "if fail → Pipeline stays, round increments"
```

## 6. Agent Guidelines

```yaml
agent_guidelines:
  - Do not introduce new core objects without updating this DOS
  - Use Connector terminology (not "adapter", "backend", "driver")
  - Use Claude Code terminology where concepts align (skill, hook, scope, connector)
  - Every action that changes state must go through the defined interfaces
  - Pipeline state belongs in CoBuild's tables, work-item state belongs in the Connector
  - Prefer extending existing objects over creating new ones
  - Skills are the right place for intelligence, not Go code
```

## 7. Anti-Patterns

```yaml
anti_patterns:
  - Creating "Shard", "Issue", "Ticket", and "Task" as separate objects
    → Use "WorkItem" as the universal noun, let type distinguish them
  - Storing pipeline orchestration state in work-item metadata
    → Pipeline state lives in CoBuild's own tables
  - Shelling out to external CLIs for core operations
    → Use the Connector interface
  - Hardcoding phase names or gate logic in Go code
    → Everything comes from config
  - Creating a "Design" object separate from WorkItem
    → A Design IS a WorkItem with type=design
  - Having agents hold state between sessions
    → Agents are ephemeral; state lives in Pipeline and WorkItem
  - Mixing CoBuild's vocabulary with the Connector's vocabulary
    → CoBuild speaks WorkItem; the Connector translates to/from shards/issues/tickets
```

## 8. Open Questions

```yaml
open_questions:
  - Should "Wave" be a first-class object with its own database record, or remain implicit?
  - Should "Context" (the assembled agent prompt) be recorded in the audit trail?
  - How does a Connector handle work-item types that don't map to CoBuild's vocabulary?
  - Should CoBuild support custom WorkItem types beyond design/bug/task?
  - Should Hooks be typed events (like Claude Code) or config-driven triggers (current poller)?
  - How do we handle cross-project dependencies when different projects use different Connectors?
  - Should the Connector interface support streaming/watching for changes, or is polling sufficient?
```
