---
name: bootstrap-storage-postgres
description: Configure and verify Postgres as CoBuild's pipeline data store. Trigger when setting up storage for a new project.
---

# Skill: Configure Postgres Storage

Set up and verify Postgres as CoBuild's data store. Called from the main bootstrap or run independently.

CoBuild stores its own orchestration data (pipeline runs, gate records, task tracking) in Postgres. This is separate from the work-item connector — even if work items are in Beads or Jira, CoBuild's own tables live here.

---

## Step 1: Connection Details

Read the bootstrap config (`~/.cobuild/bootstrap.md`) for database details:
- Host
- Database name
- SSL mode
- DSN template

> What database user should CoBuild use for this project?
>
> Convention: the user often matches the project name. Check with the developer if unsure.

Build the DSN:
```
host=<host> dbname=<database> user=<user> sslmode=<mode>
```

---

## Step 2: Test Connectivity

Test the connection using `psql` or `cxp`:

```bash
psql "host=<host> dbname=<database> user=<user> sslmode=<mode>" -c "SELECT 1" 2>&1
```

**If this works:** Postgres is reachable.

**If "connection refused":**
- Is the database server running?
- Is the host correct? (check bootstrap config)
- Is there a firewall blocking the connection?

**If "no pg_hba.conf entry":**
- The database user may not have access from this machine
- Ask the developer to add an entry or create the user

**If "FATAL: role does not exist":**
- The database user needs to be created
- Ask the developer: `CREATE ROLE <user> LOGIN;`

**If "FATAL: database does not exist":**
- The database needs to be created
- Ask the developer: `CREATE DATABASE <database>;`

---

## Step 3: Check CoBuild Tables

Test if CoBuild's tables already exist:

```bash
psql "<dsn>" -c "SELECT COUNT(*) FROM pipeline_runs" 2>&1
```

**If the table exists:** CoBuild has been used with this database before. Check if there's existing pipeline data for this project.

**If "relation does not exist":** The tables need to be created. CoBuild can auto-migrate:

```bash
cobuild migrate
```

Or run the migration SQL manually from `migrations/001_pipeline_gates.sql` in the CoBuild repo.

Note: the existing migration references `shards(id)` as a foreign key. If this database doesn't have a `shards` table (e.g., you're using Beads for work items), the migration DDL needs the version without foreign key constraints. CoBuild's Store `Migrate()` method uses this version.

---

## Step 4: Write Storage Config

Add to `.cobuild/pipeline.yaml`:

```yaml
storage:
    backend: postgres
```

If the DSN is different from what the `cxp`/`cobuild` connection config provides, set it explicitly:

```yaml
storage:
    backend: postgres
    dsn: "host=<host> dbname=<database> user=<user> sslmode=<mode>"
```

If the DSN matches the existing connection config (same host, same database), you can omit the `dsn` field — CoBuild inherits it from the connection settings.

---

## Step 5: Verify End-to-End

```bash
# Check cobuild can use the store
cobuild version

# If there's an existing design shard, test pipeline init:
# cobuild init <design-id>
# cobuild show <design-id>
```

---

## Verification Checklist

- [ ] Database host is reachable
- [ ] Database user exists and can connect
- [ ] CoBuild tables exist (or migration ran successfully)
- [ ] `storage.backend: postgres` in pipeline.yaml
- [ ] DSN is correct (explicit or inherited)

## Gotchas

<!-- Add failure patterns here as they're discovered -->
