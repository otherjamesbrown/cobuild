# Agentic Engineering Tools — Research and Gotchas (2026-04-14)

Tracked by cb-bb86bf. Input for the CLI-bootstrap refactor (cb-88707a), the audit-trail design (cb-663873), and ongoing cb-0e0482 / cb-e619cb investigations.

Scope of this doc:
1. Gotchas running Claude Code and Codex CLIs as dispatched agents (what we've hit; what others have hit).
2. How other agentic engineering tools structure dispatch, coordination, and review.
3. Concrete patterns CoBuild should adopt, avoid, or file as designs.

Source list at the bottom.

---

## 1. Executive summary

CoBuild's architecture (fresh agent per phase, tmux + git-worktree isolation, config-driven workflows, cross-model review, gate-based quality control) is firmly in the mainstream of what the best tools in this space are converging on. The specific projects that most resemble us architecturally:

- **metaswarm** (Dave Sifry) — essentially a fully-built version of where CoBuild is heading. 18 agents, 9-phase workflow, cross-model review, BEADS-backed knowledge base, recursive orchestration. Worth studying line-by-line for pattern ideas.
- **Gastown** (Steve Yegge) — larger-scoped sibling: adds a merge queue (Refinery), a capacity governor (Scheduler), a watchdog tier (Witness/Deacon/Dogs), and federated coordination (Wasteland). Three-tier monitoring is novel.
- **Symphony** (OpenAI) — explicit spec, Linear-driven, lightweight. The SPEC.md alone is worth reading as a reference for how to write up what a minimal agent-dispatch service is.
- **Ralph** (Ryan Carson / Geoffrey Huntley pattern) — opposite extreme: `while :; do cat prompt.md | claude-code; done`. One-context-at-a-time loops. No orchestration. Useful floor.

On the gotcha side, the bugs we've hit are not unique — the ecosystem is fighting the same fires, and the known-open Anthropic and OpenAI issues line up closely with our own shards.

---

## 2. Claude Code gotchas

### 2.1 `--print`/`-p` headless mode hangs on deferred tools (cb-0e0482 repro)

Direct match for cb-0e0482. anthropics/claude-code#35262 (OPEN as of 2026-04) reports that `claude -p --dangerously-skip-permissions` **hangs intermittently** when the model attempts to use **Agent, Skill, WebSearch, WebFetch, or ToolSearch**. Core tools (Bash, Read, Write, Edit, Grep, Glob) work reliably.

This is a regression from v2.1.74. Known to be open. The `cb-f3f33f` worktree we lost showed the last action was an `Agent` tool call before the session froze.

**Implication for CoBuild:**
- Silent deaths in implement phase are plausibly this bug when the agent reaches for a Skill or Agent tool.
- Our observability (`dispatch-error.log` via the 60s liveness probe) catches deaths within the first minute; this bug kills *mid-work* (1.5–3 min). The probe window is too short.
- Workaround options: (a) extend the liveness probe window and poll continuously, not just at 60s; (b) detect "no stream events for N seconds" as a death signal; (c) disable Agent/Skill tools in the dispatched agent's settings until Anthropic fixes this.

### 2.2 tmux agent-team pane variants (related to cb-e619cb)

- **#24108 (CLOSED)** — "Agent teams: teammates stuck at idle prompt in tmux split-pane mode (mailbox never polled)." Spawned teammates start cleanly but never poll their initial message.
- **#34614 (CLOSED)** — "TeamCreate spawns teammates that silently exit due to incorrect command generation." Root cause: wrong PATH / node binary resolution when spawning.
- **#23615** — "Agent teams should spawn in new tmux window, not split current pane." CoBuild already does the right thing here (new window, not split).

None of these is exactly our cb-e619cb (agent types `/exit`, process stays alive). But the cluster tells us **tmux × Claude Code has a long tail of lifecycle bugs**. Closest named comment from Eric Buess: newer Claude Code *does* clean up teammate panes when spawned from tmux-in-iTerm — suggesting a terminal-detection code path whose presence or absence determines whether `/exit` or Stop behaves as expected.

**Implication:** don't rely on the agent cleanly exiting itself. Make the runner script responsible for `tmux kill-window` after the expected work is done. That's what cb-699bf2 already did for the happy path; cb-e619cb needs the same belt-and-braces for the `/exit`-typed-but-not-honoured path.

### 2.3 `--dangerously-skip-permissions` is not quite unconditional

- **#45290** — starting a session with `--dangerously-skip-permissions` can have the permission mode **reset mid-session**. Retoggling via Shift+Tab reverts immediately. Not stable.
- **#36168** — bypass flag reported broken in all versions newer than v2.1.77.
- **#12261** — bypass flag reported not actually bypassing in some edge cases.
- **#12507** — Claude Code exits immediately on HPC interactive sessions; "stdin consumed by shell detection subprocesses."

Anthropic has now launched a safer "auto mode" as the official replacement ([auto-mode announcement](https://www.anthropic.com/engineering/claude-code-auto-mode)). Worth tracking as a migration target.

**Implication:** CoBuild pins a specific Claude Code version in the dispatch runner. When we upgrade, run a single-task dogfood first to catch silent regressions in the bypass / Stop-hook surface.

### 2.4 Stop-hook semantics

Claude Code's Stop hook runs when the agent stops. Two traps:
- **Slow shutdown:** teammates finish their current tool call before shutting down; the Stop hook can take many seconds after the logical "done" moment.
- **Non-exit:** the process may stay alive at the REPL prompt after a logical shutdown signal (`/exit`, etc.), as we hit in cb-e619cb. Stop hook never fires in that case.

**Implication:** trust the hook for the happy path, but always have an external watchdog that can kill the process if the hook doesn't fire within N seconds of expected completion.

---

## 3. Codex gotchas

### 3.1 ChatGPT-account 5-hour rolling limits

- **openai/codex#1985, #11508, #15281, #16920** all describe users hitting 5-hour rolling quotas plus weekly limits. Error messaging is poor — users report "limit reached in 1 hour, try again in 11 days" style messages.
- Discussion #2251 is the canonical limits page.
- OpenAI help: ["Using Codex with your ChatGPT plan"](https://help.openai.com/en/articles/11369540-using-codex-with-your-chatgpt-plan).

**Implication:** cb-bf9271 (pinning codex at concurrency=2) was a response to the same pressure. If we start running larger pipelines we will hit both the rate limit and the 5-hour window, and the errors we see may not clearly distinguish "concurrent dispatch" from "weekly quota" from "5-hour window." Expect to log raw error bodies so we can triage.

### 3.2 Model compatibility (already fixed by cb-b3356d)

Codex with a ChatGPT account doesn't accept Anthropic model names. We fixed this in `modelFitsRuntime` today. Not widely documented publicly — the pattern "different runtime, different model family" is one CoBuild should keep visible.

### 3.3 Limit-reached behaviour

Codex cleanly returns an error when limits hit — it doesn't hang. Which means **codex deaths in the 1.5–3 min range (cb-0e0482) are not rate-limit deaths** — they're something else (app-server state, local crash, upstream timeout). The hypothesis in the cb-0e0482 shard stands.

---

## 4. How other tools dispatch agents

### 4.1 metaswarm — the closest sibling

Repo: [dsifry/metaswarm](https://github.com/dsifry/metaswarm).

Architecture (quoted from README, heavily condensed):

```
prompt → Swarm Coordinator → Issue Orchestrator →
  Research → Plan → Design Review Gate (5 parallel reviewers) →
  Decomposition → Execution Loop (IMPLEMENT → VALIDATE → ADVERSARIAL REVIEW → COMMIT) →
  Final Review → PR → PR Shepherd → Closure + Knowledge Extraction
```

Relevant design choices:

| Choice | What they do | CoBuild parallel |
|---|---|---|
| Task tracking | BEADS (`bd` CLI) | We have a BEADS connector (`internal/connector/beads.go`). Same upstream system. |
| Orchestrator posture | "Trust nothing, verify everything" — orchestrator runs tests itself, never trusts subagent self-reports | CoBuild's gate system is the same idea, but we mostly trust subagent output from `cobuild complete` |
| Cross-model review | Writer ≠ reviewer. Codex writes → Claude reviews, Claude writes → Codex/Gemini reviews | We have this via `review.cross_model` but it's routed via `review.ResolveReviewer` — still half-wired (cb-efe119 today) |
| Knowledge base | Git-versioned JSONL facts, **primed selectively** by affected files / keywords / work type | We have nothing like this. Knowledge today is in commit messages and shard bodies only |
| Reflection / self-improvement | `/self-reflect` after merge — scans review comments, build failures, user corrections, writes new knowledge entries + flags skill candidates | `cobuild insights` + `cobuild improve` are stubbed for this but haven't been built out |
| Parallel gate | 5 specialist agents (PM, Architect, Designer, Security, CTO) run **concurrently** | Our gates are serial today |
| Escalation cap | 3 iterations → human escalation | cb-13744c gate-retry has a similar cap (3), also matches |
| Plugin install | Claude Code plugin marketplace (`claude plugin install metaswarm`) | Worth considering — CoBuild could ship as a plugin |

Three specific things worth lifting:

1. **Knowledge base with selective priming.** Solves the "you told me this yesterday" problem. A JSONL file of {pattern, gotcha, decision, anti-pattern} entries, filtered per-dispatch by file globs + keywords + work type. Scales to thousands of entries without eating the context window.
2. **Orchestrator self-verification.** Don't trust "tests pass" from the agent — the orchestrator runs the tests independently in the worktree after the Stop hook fires and before advancing phase. We partly do this via CI-gated review but not locally.
3. **Parallel review gate.** Five reviewers, each with a narrow rubric, running concurrently against the same PR. Ours runs one.

### 4.2 Gastown — everything-and-the-kitchen-sink

Repo: [steveyegge/gastown](https://github.com/steveyegge/gastown).

Vocabulary: Mayor (coordinator Claude Code), Town (workspace), Rigs (project containers), Polecats (worker agents), Hooks (git-worktree persistent storage), Convoys (bundled work), Molecules (workflow templates), Refinery (merge queue), Scheduler (capacity governor), Witness/Deacon/Dogs (three-tier watchdog), Seance (session discovery), Wasteland (federated network).

Novel patterns worth noting:

1. **Refinery as a Bors-style merge queue.** Polecats complete via `gt done` → batched into the Refinery → verification gates → merge to main. Failed MRs isolated and either fixed inline or re-dispatched. This is the problem cb-7dd0d4 was about (squash merge + dependent branches), solved by a queue rather than a rebase-before-merge.
2. **Scheduler — config-driven capacity governor.** `scheduler.max_polecats` caps concurrent dispatches to prevent API rate-limit exhaustion. CoBuild has per-runtime caps already (cb-0a0762, cb-bf9271); Gastown adds deferred dispatch with a daemon when the cap is hit.
3. **Three-tier monitoring.** Witness (per-rig lifecycle) → Deacon (cross-rig supervisor) → Dogs (ad-hoc infrastructure workers). Separates "is this one agent healthy" from "is the whole town healthy" from "someone needs to clean up stale state." Our `cobuild doctor` + `cobuild recover` + orchestrate's dead-agent recovery are roughly Witness + Dogs; we have no Deacon.
4. **Seance.** Agents can query previous agent sessions' `.events.jsonl` logs for context and decisions. Replaces institutional memory with queryable session history.
5. **Wasteland.** Federated coordination across multiple "Gas Towns" via DoltHub. Not relevant to CoBuild today, but an interesting multi-team future.

### 4.3 Symphony — a language-agnostic SPEC

Repo: [openai/symphony](https://github.com/openai/symphony).

Most useful thing here is **the SPEC itself** — a carefully written, deliberately language-agnostic description of what an agent-dispatch service does. Key boundaries they draw:

- Symphony is **a scheduler/runner + tracker reader**. Nothing more.
- **Ticket writes** (state transitions, comments, PR links) are done by the coding agent, not by Symphony.
- A successful run ends at a **workflow-defined handoff state** (e.g. "Human Review"), not necessarily "Done".
- `WORKFLOW.md` lives in the repo — workflow policy is versioned with the code.

Layer naming they use:
1. Policy Layer (repo's `WORKFLOW.md`)
2. Configuration Layer (typed getters)
3. Coordination Layer (orchestrator, retries, reconciliation)
4. Execution Layer (workspace + agent subprocess)
5. Integration Layer (tracker adapter — Linear)
6. Observability Layer (logs + optional status surface)

CoBuild has each of these but they're not cleanly separated — most live in `internal/cmd`. cb-88707a's refactor could use these layer names directly as the target structure.

Worth reading in full: [SPEC.md](https://github.com/openai/symphony/blob/main/SPEC.md).

### 4.4 Ralph — the simplest possible loop

Repo: [snarktank/ralph](https://github.com/snarktank/ralph). Based on Geoffrey Huntley's original pattern.

Core idea, unchanged:

```bash
while :; do cat PROMPT.md | claude-code; done
```

Each iteration is a fresh context. Memory persists only in git history + `progress.txt` + `prd.json`. Skill files + marketplace plugin wrap it, but the loop is the whole product.

**Why it's in this doc:** it's the floor. Anything CoBuild does that doesn't beat "run the same prompt in a loop until `prd.json` says done" has to justify the complexity. For a single developer with a scoped feature, Ralph may literally be enough.

Lessons:
- **Every iteration is a fresh context.** We already do this — fresh agent per phase. Key invariant: the agent reconstructs state from the repo, not from memory.
- **`prd.json` as durable state.** Mirrors our `pipeline_runs` table. Git-native would be simpler (no DB), but doesn't scale to concurrent pipelines on the same repo.
- **Amp auto-handoff** (`amp.experimental.autoHandoff: { context: 90 }`) hands off at 90% context automatically. Claude Code should have something similar — PreCompact hook is the hook point.

### 4.5 AgentHub (karpathy), Agent Orchestrator (ComposioHQ), Smol Developer, Pythagora, Genie

Less detail here — these are either commercial (Genie, Pythagora), archived (Smol Developer, GPT Engineer), or early/lightweight (AgentHub, Agent Orchestrator). The pattern most worth noting from AgentHub is **branchless coordination**: git DAG of commits + message board instead of PRs + branches. Interesting future direction but not a near-term fit.

---

## 5. Pattern catalogue

Patterns that the field (and our own experience) have converged on:

| Pattern | Who does it | Why it works | CoBuild status |
|---|---|---|---|
| **Fresh agent per phase** (no long-lived session) | metaswarm, Ralph, Gastown, CoBuild | Context is cheap; agent state is a liability | Shipped |
| **Git worktree isolation per task** | metaswarm, Gastown, CoBuild | Parallel work without branch collisions; easy cleanup | Shipped |
| **Policy in repo (`WORKFLOW.md`)** | Symphony, metaswarm, CoBuild | Team-versioned with the code; no global state drift | Shipped as `.cobuild/pipeline.yaml` |
| **Cross-model review** (writer ≠ reviewer) | metaswarm, CoBuild (config exists) | Catches model-specific blind spots | Partial — cb-efe119 exposed the gemini→claude leak today |
| **Blocking quality gates with retry caps** | metaswarm (3 iter), CoBuild (cb-13744c, also 3) | Prevents infinite-loop escalation | Shipped |
| **Knowledge base with selective priming** | metaswarm | Scales institutional memory without eating context | **Not built** — design opportunity |
| **Parallel review gate** (N concurrent specialists) | metaswarm | Faster reviews; specialists don't block each other | **Not built** — we're serial |
| **Merge queue** (Bors-style) | Gastown | Solves dependent-branch conflicts structurally | **Not built** — cb-7dd0d4 solved via rebase, but queue would be more general |
| **Three-tier monitoring** (per-task / cross-project / maintenance) | Gastown | Separates concerns of liveness / health / cleanup | Partial — we have doctor + recover but not the cross-project tier |
| **Orchestrator self-verification** (run tests, don't trust agent) | metaswarm | Agent self-reports are unreliable | Partial — we delegate to CI |
| **External kill after grace period** | Gastown (Witness), our cb-699bf2 | Agent processes sometimes don't exit themselves | Partial — cb-e619cb is the gap |
| **Extended liveness probe** (not just 60s) | — | Catches mid-work deaths like cb-0e0482 | **Not built** — cb-1d8abc probe is 60s only |
| **Plugin distribution** (marketplace install) | metaswarm, Ralph | Frictionless install + versioned updates | **Not built** — could be a one-off |

Anti-patterns consistently flagged:

| Anti-pattern | Source | Why it fails |
|---|---|---|
| Trust agent self-reports for test pass / review verdict | metaswarm principle #2 | Hallucination + sycophancy |
| Silent error suppression in the orchestrator | CoBuild review (cb-663873) + Symphony spec emphasis on observability | Failures become invisible, debugging becomes archaeology |
| Interactive REPL commands in automation | cb-e619cb (`/exit` doesn't exit), anthropics/claude-code#24108 | Slash commands are terminal-aware; don't behave the same in automation |
| One big `RunE` for the dispatch command | CoBuild review (cb-88707a) | Not testable; logic drift |
| Hardcoded fallbacks that bypass config | CoBuild review (cb-9a336c) | Defeats "config over code" |
| Single-reviewer gate | Implicit in metaswarm's 5-reviewer pattern | Single point of judgment failure |
| Agent workflow that assumes a specific terminal | #24108, #12507 | tmux / iTerm / HPC / CI all behave differently |

---

## 6. Concrete recommendations for CoBuild

Highest leverage, ordered by unlock:

1. **Extend the liveness probe** (cb-0e0482 angle). Don't just check at 60s — stream `.cobuild/session.log` continuously and flag "no stream events for 90s" as a probable mid-work death. Catches the Agent/Skill/WebSearch hang that claude-code#35262 describes.

2. **External kill after grace period** (cb-e619cb angle). After `cobuild complete` records success for a task, if the tmux window is still alive N seconds later, kill it. Stop relying on the agent exiting itself.

3. **Pin a known-good Claude Code version in the dispatch runner.** Claude Code has active regressions in Stop hook / bypass flag / Agent tools. Pin the version, run a single-task dogfood on any version bump, treat "works on the pinned version" as the contract.

4. **Adopt metaswarm's knowledge base pattern.** File-filtered JSONL entries primed per-dispatch. Would solve the "why did you make the same mistake again?" class of problem that we've hit manually (the 2026-04-13 50-file working-tree dump is a data point — we forgot to commit per shard across a dozen fixes).

5. **Adopt Symphony's layer names for the cb-88707a refactor.** Policy / Configuration / Coordination / Execution / Integration / Observability. Use them directly as package boundaries.

6. **Cross-model review validation.** cb-efe119 today fixed the gemini→claude silent demote. Extend with a startup check: if review config resolves to an unusable combo on this machine (no API key for claude, codex in 5-hour lockout, etc.), refuse to start the pipeline rather than degrading to ci-fallback.

7. **Parallel review gate** (longer-term). Spin up N reviewers concurrently against the same PR. Each has a narrow rubric (security / correctness / style / fit-to-spec / UX). The orchestrator aggregates verdicts.

8. **Merge queue as an alternative to auto-rebase** (longer-term). cb-7dd0d4 is closed via rebase; Gastown's queue is more general when you have 5+ branches in flight. File a design if we start seeing queue pressure.

9. **Plugin distribution** (optional). metaswarm ships via `claude plugin install metaswarm`. Much lower-friction than our current "git clone + go build" path. Would need a bootstrap that reads a repo's `.cobuild/` and configures the plugin accordingly.

### Shards to file from this research

| Proposed shard | Why | Priority |
|---|---|---|
| Extended liveness probe (stream-event based, not just 60s) | cb-0e0482 concrete fix direction | P1 |
| External kill after grace period on implement completion | cb-e619cb concrete fix direction | P1 |
| Claude Code version pinning in runtime | Avoid silent regressions | P2 |
| Knowledge base design (JSONL + selective priming) | Institutional memory | P2 |
| Cross-model review startup validation | Fail-loud on unusable review config | P2 |
| Parallel review gate | Faster reviews, specialist rubrics | P3 |
| Symphony-style package layer rename (informs cb-88707a) | Cleaner boundaries | Inform cb-88707a, not a new shard |

---

## Sources

### Claude Code issues (GitHub)
- [#35262 — --print mode hangs on deferred tools (Agent, Skill, WebSearch)](https://github.com/anthropics/claude-code/issues/35262) **(OPEN)**
- [#24108 — Agent teams stuck at idle prompt in tmux](https://github.com/anthropics/claude-code/issues/24108) (closed)
- [#34614 — TeamCreate silent exit](https://github.com/anthropics/claude-code/issues/34614) (closed)
- [#23615 — Agent teams should spawn in new tmux window](https://github.com/anthropics/claude-code/issues/23615)
- [#45290 — `--dangerously-skip-permissions` resets mid-session](https://github.com/anthropics/claude-code/issues/45290)
- [#36168 — Bypass flag broken after v2.1.77](https://github.com/anthropics/claude-code/issues/36168)
- [#12261 — Bypass flag not bypassing](https://github.com/anthropics/claude-code/issues/12261)
- [#12507 — Exits immediately on HPC interactive sessions](https://github.com/anthropics/claude-code/issues/12507)

### Claude Code docs
- [Agent teams docs](https://code.claude.com/docs/en/agent-teams)
- [Permission modes](https://code.claude.com/docs/en/permission-modes)
- [Claude Code auto mode (safer bypass replacement)](https://www.anthropic.com/engineering/claude-code-auto-mode)

### Codex issues / docs
- [openai/codex#1985 — ChatGPT Pro concurrency / limits](https://github.com/openai/codex/issues/1985)
- [openai/codex#11508 — 5-hour + weekly limits burned](https://github.com/openai/codex/issues/11508)
- [openai/codex#15281 — Expose usage/limits in `/status`](https://github.com/openai/codex/issues/15281)
- [openai/codex#16920 — Limit-reached error message clarity](https://github.com/openai/codex/issues/16920)
- [openai/codex Discussion #2251 — Usage Limits](https://github.com/openai/codex/discussions/2251)
- [OpenAI help — Using Codex with your ChatGPT plan](https://help.openai.com/en/articles/11369540-using-codex-with-your-chatgpt-plan)
- [community.openai.com — new Codex limit system (April 9 update)](https://community.openai.com/t/understanding-the-new-codex-limit-system-after-the-april-9-update/1378768)

### Agentic tools
- [Ryan Walker — Autonomous Agentic Engineering Tools (source survey)](https://rywalker.com/research/autonomous-agentic-engineering-tools)
- [dsifry/metaswarm](https://github.com/dsifry/metaswarm)
- [steveyegge/gastown](https://github.com/steveyegge/gastown)
- [openai/symphony](https://github.com/openai/symphony) and its [SPEC.md](https://github.com/openai/symphony/blob/main/SPEC.md)
- [snarktank/ralph](https://github.com/snarktank/ralph)
- [karpathy/agenthub](https://github.com/karpathy/agenthub)
- [ComposioHQ/agent-orchestrator](https://github.com/ComposioHQ/agent-orchestrator)
- [AntonOsika/gpt-engineer](https://github.com/AntonOsika/gpt-engineer) (archived)
- [smol-ai/developer](https://github.com/smol-ai/developer) (archived)
- [Geoffrey Huntley — the Ralph pattern](https://ghuntley.com/ralph/)
- [BEADS (Steve Yegge)](https://github.com/steveyegge/beads) — task-tracking backbone for both metaswarm and Gastown
- [Superpowers (Jesse Vincent)](https://github.com/obra/superpowers) — agentic skills framework acknowledged by metaswarm
