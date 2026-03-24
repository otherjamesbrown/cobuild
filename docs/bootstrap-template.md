# CoBuild Local Bootstrap — Template

Copy this file to `~/.cobuild/bootstrap.md` and fill in your details. This is read by agents during `cobuild setup` to configure projects correctly for your environment.

## Machine

- Hostname: <your hostname>
- User: <your username>
- Projects: <path to your repos>
- Worktrees: <path for git worktrees>

## Database

- Host: <database host>
- Database: <database name>
- SSL: <sslmode>
- DSN: `host=<host> dbname=<db> user=<user> sslmode=<mode>`

## Connector

- Type: <context-palace or beads>
- CLI path: <path to cxp or bd binary>

## Agents

| Agent | Domains | Notes |
|-------|---------|-------|
| <agent-name> | <domain1, domain2> | <notes> |

## Defaults

- Default model: sonnet
- Review strategy: <external or agent>
- CI mode: <pr-only, all-pass, or ignore>
- Max concurrent agents: 3
- Stall timeout: 30m

## Tmux

CoBuild auto-creates tmux sessions named `cobuild-<project>` by default. Set `dispatch.tmux_session` in `.cobuild/pipeline.yaml` only if you need a different name.

## Project Setup Conventions

<Any conventions specific to your environment>
