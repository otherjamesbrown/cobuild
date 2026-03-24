# Context Layers

Context layers control exactly what information each agent sees per session type. They solve the problem of needing different `CLAUDE.md` content for interactive sessions (human typing) vs dispatched agents (pipeline tasks) vs gate evaluations.

## Quick start

```yaml
# .cobuild/pipeline.yaml
context:
    layers:
        - name: architecture
          source: file:.cobuild/context/architecture.md
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

Context layers let you compose the right context for each situation from reusable pieces.

### The `when` field

| Value | Active when |
|-------|-------------|
| `always` | Every session type (interactive, dispatch, gate) |
| `interactive` | Human is typing in an interactive Claude session |
| `dispatch` | Pipeline dispatched the agent via `cobuild dispatch` |
| `gate:<name>` | Only during a specific gate evaluation (e.g. `gate:security-review`) |

An empty `when` field is treated as `always`.

### Source types

| Source | Resolves to | Notes |
|--------|-------------|-------|
| `file:<path>` | Read file from repo | Path relative to repo root |
| `shard:<id>` | Fetch work item via connector | Returns title + content |
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

## Configuration

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
  source: file:.cobuild/context/architecture.md
  when: always
```

Paths are relative to the repo root. Absolute paths also work.

### Source: shard

```yaml
- name: playbook
  source: shard:pf-2b76b4
  when: interactive
```

Fetches the work item via the connector. The result includes the title as a heading followed by the content.

### Source: skills

```yaml
- name: review-procedure
  source: skills:m-review-pr
  when: gate:review
```

Resolves the skill file using the standard resolution chain (repo `skills/` then `~/.cobuild/skills/`).

### Source: skills-dir

```yaml
- name: all-skills
  source: skills-dir
  when: interactive
  filter: [m-playbook.md, create-design.md]
```

Loads all `.md` files from the skills directory. The optional `filter` list restricts which files are included.

## Examples

### Example 1: penfold context layers (production config)

This is the real context configuration from the penfold project:

```yaml
context:
    layers:
        # === Always loaded (both interactive and dispatch) ===
        - name: architecture
          source: file:.cobuild/context/architecture.md
          when: always
        - name: deploy
          source: file:.cobuild/context/deploy.md
          when: always

        # === Interactive sessions (human typing) ===
        - name: agent-identity
          source: file:.cobuild/context/agent-identity.md
          when: interactive
        - name: completion-protocol
          source: file:.cobuild/context/completion-protocol.md
          when: interactive
        - name: playbook
          source: shard:pf-2b76b4
          when: interactive
        - name: menu-instructions
          source: file:.cobuild/context/interactive-menu.md
          when: interactive

        # === Pipeline-dispatched agents ===
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
        - name: design-context
          source: parent-design
          when: dispatch
        - name: dispatch-completion
          source: file:.cobuild/context/dispatch-completion.md
          when: dispatch
```

What this achieves:

- **Architecture and deploy docs** are always visible -- every session knows the codebase structure
- **Interactive sessions** get identity, playbook, and menu instructions -- the agent knows who it is and what commands are available
- **Dispatched agents** get their task spec, the parent design for context, and completion instructions -- focused on the work, no identity clutter

### Example 2: Minimal dispatch-only config

For a project that only uses dispatched agents (no interactive sessions):

```yaml
context:
    layers:
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
        - name: design-context
          source: parent-design
          when: dispatch
        - name: coding-standards
          source: file:.cobuild/context/standards.md
          when: always
```

### Example 3: Gate-specific context

Load security policies only during the security review gate:

```yaml
context:
    layers:
        - name: architecture
          source: file:.cobuild/context/architecture.md
          when: always
        - name: security-policy
          source: file:SECURITY.md
          when: gate:security-review
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
```

## Troubleshooting

**Layer not appearing in generated CLAUDE.md:** Check the `when` field matches the session mode. Dispatch mode only loads layers with `when: dispatch` or `when: always`. Look for HTML comments (`<!-- context: name -->`) in the generated file to see which layers were included.

**File layer returning empty:** The `source: file:<path>` is relative to the repo root, not the worktree. If the file does not exist, the layer is silently skipped (a comment is inserted). Check the path exists from the repo root.

**Shard layer failing:** Ensure the shard ID is valid and accessible. Failed shard fetches produce an HTML comment with the error message in the generated CLAUDE.md rather than failing the entire assembly.
