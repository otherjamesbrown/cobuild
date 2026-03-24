# Skill: Configure Context Layers

Set up context layers for a CoBuild project. Called from the main bootstrap or run independently.

Context layers control what information agents see in each session type (interactive, dispatch, gate evaluation). The most important layer is the architecture doc.

---

## Step 1: Check for Existing Context

Look for existing context files that may have been created manually or by a previous setup:

```bash
ls .cobuild/context/ 2>/dev/null
ls .cxp/context/ 2>/dev/null
```

If `.cxp/context/` exists (old format), migrate files to `.cobuild/context/`:

```bash
mkdir -p .cobuild/context
cp .cxp/context/*.md .cobuild/context/
```

Review each migrated file and ask the developer if they're still current.

---

## Step 2: Create Architecture Doc

This is the most important context file. Every agent session loads it.

Read the codebase to understand the structure. Check these sources:
- `CLAUDE.md` (if it exists — often has architecture info)
- `README.md`
- Directory structure (`ls` the top level and key directories)
- `go.mod`, `package.json`, `Cargo.toml` for dependencies
- Build scripts, Makefile, CI config

Create `.cobuild/context/architecture.md` covering:

```markdown
# Architecture

## Structure
<directory tree with descriptions of what each major directory contains>

## Build & Test
<how to build, test, and run the project>

## Key Patterns
<conventions, naming, error handling, logging patterns>

## External Dependencies
<databases, APIs, services this project talks to>

## Deployment
<how the project is deployed, what environments exist>
```

> I've drafted an architecture doc based on what I can see in the codebase.
> Please review it — is anything wrong or missing?

---

## Step 3: Identify Additional Context Files

Ask the developer:

> Are there specific docs or context that agents should always have access to?
>
> For example:
> - API documentation
> - Security policies
> - Coding standards
> - Deploy procedures
>
> These can be added as context layers that load in specific modes (always, dispatch only, gate-specific).

For each additional context file, create it in `.cobuild/context/` and add a layer.

---

## Step 4: Configure Layers in Pipeline YAML

Build the context layers section. At minimum:

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

If there are additional context files:

```yaml
        # Examples of additional layers:
        - name: deploy
          source: file:.cobuild/context/deploy.md
          when: always
        - name: coding-standards
          source: file:.cobuild/context/standards.md
          when: dispatch
        - name: security-policy
          source: file:SECURITY.md
          when: gate:security-review
```

If migrating from `.cxp/context/`, map each existing file to a layer with the appropriate `when` mode.

---

## Verification Checklist

- [ ] `.cobuild/context/` directory exists
- [ ] `architecture.md` created and reviewed by developer
- [ ] Context layers added to pipeline.yaml
- [ ] Task-prompt and design-context dispatch layers configured
- [ ] Any migrated `.cxp/context/` files reviewed and updated
