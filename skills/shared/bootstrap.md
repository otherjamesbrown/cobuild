# Skill: Bootstrap CoBuild on a Project

Set up CoBuild pipeline automation on a repository. This is an interactive process — read the local infrastructure config, auto-detect what you can, and ask the developer for decisions you can't infer.

---

## Before You Start

1. Read `~/.cobuild/bootstrap.md` for local infrastructure details (database, agents, defaults)
2. Confirm you're in the target repo root: `pwd` and `git remote -v`
3. Verify `cobuild version` works

If `~/.cobuild/bootstrap.md` doesn't exist, ask the developer to create it from the template at `docs/bootstrap-template.md` in the CoBuild repo.

---

## Phase 1: Gather Information

### Auto-detect (don't ask, just do)

- **Language and build system**: look for `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`
- **GitHub remote**: `git remote get-url origin`
- **Default branch**: `git symbolic-ref refs/remotes/origin/HEAD`
- **Existing config**: check for `.cobuild/`, `.cxp/`, `.cobuild.yaml`, `.cxp.yaml`
- **Existing skills**: check for `skills/` directory
- **Existing CLAUDE.md**: read it for project context

### Ask the developer

Present what you auto-detected, then ask these questions:

**1. Project identity**
> What is the project name for this repo? This should match the name in your work-item system (Context Palace project, Beads prefix, etc.).
>
> Detected: `<dirname or existing config>`

**2. Multi-repo designs**
> Do designs for this project ever span multiple repos? If yes, which repos?
>
> This affects how tasks are decomposed — tasks need to be tagged with their target repo so CoBuild dispatches into the right worktree.

**3. Agent identity**
> Which agent identity should this project use? Available agents from your bootstrap config:
>
> | Agent | Domains |
> |-------|---------|
> (list from bootstrap.md)
>
> Default: `<first agent from bootstrap>`

**4. Build/test commands**
> I detected these build and test commands. Are they correct?
>
> Build: `<detected>`
> Test: `<detected>`

**5. Deploy**
> Does this project have services to deploy after PR merge? If yes, list the service names, path prefixes, and deploy commands.

**6. Review strategy**
> How should PRs be reviewed?
> - **external**: An external reviewer (e.g., Gemini) reviews, then CoBuild processes the result
> - **agent**: A CoBuild agent reviews the PR directly
>
> Default from bootstrap: `<default>`

**7. Tmux session**
> What tmux session should dispatched agents run in?
>
> Convention from bootstrap: `<convention>`
> Detected sessions: `tmux list-sessions`

---

## Phase 2: Create Config

Based on the answers, create these files:

### `.cobuild.yaml` (project identity — repo root)

```yaml
project: <project-name>
agent: <agent-identity>
```

### `.cobuild/pipeline.yaml` (pipeline config)

Build this from the answers + bootstrap defaults. Include all sections:

```yaml
build:
    - <detected or provided build commands>
test:
    - <detected or provided test commands>

agents:
    <agent-name>:
        domains: [<domains>]

dispatch:
    max_concurrent: <from bootstrap>
    tmux_session: <from answer>
    claude_flags: "--dangerously-skip-permissions"
    default_model: <from bootstrap>

connectors:
    work_items:
        type: <from bootstrap>

phases:
    - name: design
      model: haiku
      gates:
          - name: readiness-review
            skill: design/gate-readiness-review
            model: haiku
            fields:
                readiness: {type: int, min: 1, max: 5, required: true}
    - name: decompose
      model: sonnet
      gates:
          - name: decomposition-review
            requires_label: integration-test
    - name: implement
      model: sonnet
    - name: review
      model: haiku
    - name: done
      gates:
          - name: retrospective
            skill: done/gate-retrospective
            model: haiku

review:
    strategy: <from answer>
    external_reviewers: [gemini]        # if external
    process_skill: review/gate-process-review
    ci:
        mode: pr-only
        wait: true
    model: haiku

monitoring:
    stall_timeout: 30m
    crash_check: true
    max_retries: 3
    cooldown: 5m
    model: haiku
    actions:
        on_stall: skill:implement/stall-check
        on_crash: redispatch
        on_max_retries: escalate

# If deploy was configured:
deploy:
    enabled: <true if services listed>
    services:
        - name: <service>
          paths: [<paths>]
          command: <deploy command>

github:
    owner_repo: <detected from git remote>

skills_dir: skills

workflows:
    design:
        phases: [design, decompose, implement, review, done]
    bug:
        phases: [implement, review, done]
    task:
        phases: [implement, review, done]
```

### If multi-repo: document the relationship

If the developer said designs span multiple repos, add a note to the pipeline config:

```yaml
# Multi-repo: designs may span these repos
# Tasks are tagged with target repo during decomposition
# Related repos: <list>
```

---

## Phase 3: Copy Skills

```bash
cobuild init-skills
```

This creates the `skills/` directory with phase subfolders. After copying, tell the developer:

> Default skills have been installed. You should customize these for your project:
>
> - `skills/shared/create-design.md` — add project-specific design requirements
> - `skills/design/gate-readiness-review.md` — adjust readiness criteria for this codebase
> - `skills/shared/playbook.md` — review agent routing if your agents have specialized domains

---

## Phase 4: Create Context Layers

### Architecture doc

Read the existing CLAUDE.md (if any) and the codebase structure. Create `.cobuild/context/architecture.md` describing:

- Directory structure and what each major directory contains
- Build system and how modules/packages relate
- Key patterns and conventions
- External dependencies and integrations
- How to run the project locally

If there's an existing `.cxp/context/` directory with context files, migrate them to `.cobuild/context/`.

### Configure layers in pipeline.yaml

Add to `.cobuild/pipeline.yaml`:

```yaml
context:
    layers:
        - name: architecture
          source: file:.cobuild/context/architecture.md
          when: always
        - name: task-prompt
          source: dispatch-prompt
          when: dispatch
        - name: design-context
          source: parent-design
          when: dispatch
```

If there were existing context files migrated from `.cxp/context/`, add layers for those too.

---

## Phase 5: Register and Verify

```bash
# Register the repo (updates ~/.cobuild/repos.yaml)
cobuild setup

# Verify connector
cxp shard list --project <project-name> --limit 1 -o json

# Verify build
<run build commands>

# Verify test
<run test commands>

# Verify skills are in place
ls skills/design/ skills/implement/ skills/review/ skills/done/ skills/shared/

# Verify context
cat .cobuild/context/architecture.md
```

---

## Phase 6: Summary

Print a summary of everything that was configured:

```
CoBuild setup complete for <project-name>
================================================

Project:     <name>
Repo:        <github url>
Agent:       <agent>
Connector:   <type>
Review:      <strategy>
Build:       <commands>
Test:        <commands>
Deploy:      <yes/no + services>
Multi-repo:  <yes/no + related repos>

Config:      .cobuild/pipeline.yaml
Skills:      skills/ (10 files across 6 directories)
Context:     .cobuild/context/architecture.md

Next steps:
  1. Review and customize skills for this project
  2. Create a design shard: cobuild shard create --type design --title "..."
  3. Initialize a pipeline: cobuild init <design-id>
  4. Run the poller: cobuild poller --once --dry-run
```
