---
name: bootstrap-connector-cp
description: Configure and verify the Context Palace connector for CoBuild. Trigger when setting up a project that uses Context Palace for work items.
---

# Skill: Configure Context Palace Connector

Set up and verify the Context Palace connector for CoBuild. Called from the main bootstrap or run independently.

---

## Step 1: Verify cxp CLI

```bash
which cxp && cxp version
```

If `cxp` is not found, check the bootstrap config (`~/.cobuild/bootstrap.md`) for the CLI path. It may need to be added to PATH or symlinked.

---

## Step 2: Check Connection Config

The `cxp` CLI reads connection settings from (in order):
1. Environment variables: `CXP_HOST`, `CXP_DATABASE`, `CXP_USER`
2. Project config: `.cxp.yaml` or `.cobuild.yaml` in the repo
3. Global config: `~/.cxp/config.yaml` or `~/.cp/config.yaml`

Check which config is being used:

```bash
cxp status
```

This should show the database host, project, and agent. If it fails, the connection config is missing.

---

## Step 3: Test Connectivity

> What is the Context Palace project name for this repo?

Test that the project exists and has accessible shards:

```bash
cxp shard list --project <project-name> --limit 5 -o json
```

**If this works:** The connector is configured. Note the project name for the pipeline config.

**If this fails with "connection refused" or "no pg_hba.conf entry":**
- Check the database host and credentials in the bootstrap config
- Verify SSL settings (`sslmode=verify-full` requires valid certs)
- Ask the developer if the DB user exists for this project

**If this works but returns empty results:**
- The project may not have any shards yet — that's OK for a new project
- Verify the project name is correct: `cxp shard list -o json` (lists all projects)

---

## Step 4: Ensure Project Config

The repo needs a project identity file so `cxp` and `cobuild` know which project to query. Create `.cobuild.yaml` in the repo root:

```yaml
project: <project-name>
agent: <agent-identity>
```

Verify it works:

```bash
cxp status
```

Should now show the correct project and agent.

---

## Step 5: Write Connector Config

Add to `.cobuild/pipeline.yaml`:

```yaml
connectors:
    work_items:
        type: context-palace
```

No additional config needed — the `cxp` CLI handles connection details via its own config chain.

---

## Verification Checklist

- [ ] `cxp version` works
- [ ] `cxp status` shows correct host, project, and agent
- [ ] `cxp shard list --project <name> -o json` returns valid JSON
- [ ] `.cobuild.yaml` exists with project and agent
- [ ] `connectors.work_items.type: context-palace` in pipeline.yaml

## Gotchas

<!-- Add failure patterns here as they're discovered -->

## Final step

When the connector is configured and the verification checklist is complete, stop here. Do not run `cobuild complete` from this bootstrap skill. Exit the session with `/exit`.
