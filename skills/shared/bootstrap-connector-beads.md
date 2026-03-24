---
name: bootstrap-connector-beads
description: Configure and verify the Beads connector for CoBuild. Trigger when setting up a project that uses Beads for work items.
---

# Skill: Configure Beads Connector

Set up and verify the Beads connector for CoBuild. Called from the main bootstrap or run independently.

---

## Step 1: Verify bd CLI

```bash
which bd && bd --version
```

If `bd` is not found, check the bootstrap config for the CLI path. Beads may need to be installed: see https://github.com/steveyegge/beads

---

## Step 2: Check Beads Initialization

Beads stores data in `.beads/` in the repo. Check if it's already initialized:

```bash
ls .beads/ 2>/dev/null || echo "not initialized"
```

If not initialized:

> Should I initialize Beads in this repo? This creates a `.beads/` directory with a Dolt database.

```bash
bd init --prefix <project-prefix>
```

The prefix is used for issue IDs (e.g., `cb` produces `cb-a1b2c3`).

> What prefix should Beads use for issue IDs in this project?

---

## Step 3: Test Connectivity

```bash
bd list --limit 5 --json
```

**If this works:** Beads is configured. Note whether there are existing issues.

**If this fails with "no database":** Run `bd init` (Step 2).

**If this fails with "dolt not found":** Beads requires Dolt. Install it:
```bash
# macOS
brew install dolt
# Linux
curl -L https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash
```

---

## Step 4: Verify Ready Queue

Beads has a built-in readiness detector — it knows which tasks are unblocked:

```bash
bd ready --json
```

This is what CoBuild will use to find dispatchable work.

---

## Step 5: Write Connector Config

Add to `.cobuild/pipeline.yaml`:

```yaml
connectors:
    work_items:
        type: beads
        config:
            prefix: <project-prefix>
```

---

## Verification Checklist

- [ ] `bd --version` works
- [ ] `.beads/` directory exists in the repo
- [ ] `bd list --json` returns valid JSON
- [ ] `bd ready --json` works
- [ ] `connectors.work_items.type: beads` in pipeline.yaml
- [ ] Prefix configured correctly

## Gotchas

<!-- Add failure patterns here as they're discovered -->
