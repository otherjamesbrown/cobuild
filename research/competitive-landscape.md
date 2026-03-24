# Competitive Landscape: AI Agent Orchestration Tools

> Date: 2026-03-24
> Purpose: Understand where CoBuild sits relative to existing tools and validate our design philosophy.

---

## The Complexity Spectrum

There's a clear spectrum in AI agent orchestration tools:

```
Simple                                                              Complex
  |                                                                    |
  Ralph          CoBuild                    gstack              Gastown
  (bash loop)    (pipeline + gates)         (28 skills)         (multi-agent platform)
```

Our thesis: most developers look at gstack and Gastown and think "too much." They want something they can start using in minutes but grow into over time. Ralph proves the demand for simplicity. CoBuild should occupy the middle ground — simple to start, configurable to grow.

---

## Ralph (snarktank/ralph)

**Source:** https://github.com/snarktank/ralph

### What it is
A ~100-line bash script that loops an AI coding tool (Claude Code or Amp) over a list of user stories in `prd.json`. Each iteration gets a fresh context window, picks the next incomplete story, implements it, commits, and marks it done.

### Architecture
- `ralph.sh` — the entire runtime
- `prd.json` — task list with pass/fail per story
- `progress.txt` — append-only memory between iterations
- Prompt templates (`CLAUDE.md` / `prompt.md`) — the actual intelligence
- Git history — the only durable state

### Why developers like it
- **Zero infrastructure.** No database, no server, no Docker. Files in a repo.
- **Understandable in 5 minutes.** Read the bash script, get the whole system.
- **Works today.** Copy files, write stories, run `./ralph.sh --tool claude 10`.
- **The pattern is the product.** The value is in the workflow discipline (small stories, dependency ordering, verifiable criteria), not the code.

### What it lacks
- No parallelism — stories execute strictly sequentially
- No quality gates or review steps between iterations
- No structured phase transitions
- Memory between iterations is informal text, not structured data
- No error recovery beyond "try again next iteration"
- Fully autonomous with no human checkpoint (`--dangerously-skip-permissions` only mode)
- No way to dispatch to multiple agents

### Relevance to CoBuild
Ralph validates that **simple orchestration patterns with markdown-driven intelligence work.** It proves developers want:
1. File-based state over databases
2. Prompt engineering over compiled code
3. Minimal config over comprehensive config
4. A workflow they can read and modify

CoBuild's "basic mode" should feel this easy. A developer should be able to go from zero to dispatching their first design in minutes, not hours.

---

## gstack (garrytan/gstack)

**Source:** https://github.com/garrytan/gstack
**Stars:** ~44,500 (massive traction, created 2026-03-11)

### What it is
28 opinionated slash-command skills for Claude Code that map to roles in a software development sprint. Think → Plan → Build → Review → Test → Ship → Reflect. Plus a persistent headless browser daemon for QA and design review.

### Architecture
- **Skills layer:** 28 directories in `~/.claude/skills/gstack/`, each with a `SKILL.md` template
- **Browse daemon:** Compiled Bun binary (~58MB) running a long-lived Chromium via Playwright with sub-second latency
- **No pipeline state.** Skills are standalone — there's no concept of a "run" or "phase transition." Each skill is invoked manually.

### Why it's popular
- **Low barrier to entry.** Clone, run `./setup`, done. No config files to write.
- **Opinionated workflow.** The 28 skills encode the judgment of a staff engineer, QA lead, security officer, etc.
- **The browser is genuinely useful.** Persistent headless Chromium with cookie import, accessibility-tree refs, and ring buffers for logs.
- **Garry Tan's platform.** YC president, daily user, vocal advocate.

### Why developers bounce off it
- **28 skills is a lot to learn.** Which one do you use when? The README table helps, but it's still a wall of commands.
- **No structured state.** There's no pipeline tracking what's been done. You run `/review`, then `/qa`, then `/ship` — but nothing enforces the order or records that review passed.
- **No audit trail.** If a review found issues, that's in your conversation history, not in a queryable record.
- **Prompt-only customization.** To change behavior, you edit SKILL.md templates. Powerful but opaque — no config knobs, just Markdown.
- **Bun dependency.** Not standard in most Go/Python/Rust shops.

### Relevance to CoBuild
gstack validates that **skills-as-markdown is the right pattern.** CoBuild's skills are structurally similar. But gstack lacks what CoBuild provides:
- **Pipeline state** — gstack has no concept of a design flowing through phases
- **Quality gates** — gstack's skills don't block progression or record verdicts
- **Audit trail** — no gate history, no dispatch records
- **Work-item tracking** — no connector to external systems
- **Multi-agent dispatch** — gstack is one human, one Claude

CoBuild should learn from gstack's onboarding simplicity (clone + setup) but provide the structure that gstack deliberately omits.

---

## Gastown (steveyegge/gastown)

**Source:** https://github.com/steveyegge/gastown

### What it is
A full multi-agent workspace manager coordinating 20-30 concurrent AI coding agents. It handles work decomposition, agent communication, merge queuing, health monitoring, and cross-machine federation.

### Architecture
- **Monolithic Go CLI** (`gt`) with ~70 internal packages, 1,483 files
- **Spatial hierarchy:** Town → Rig → Hook (workspace → project → worktree)
- **Agent hierarchy:** Mayor → Deacon → Witness → Polecats (coordinator → daemon → monitor → workers)
- **Data layer:** Beads (git-backed issue tracker via Dolt database) + Convoys (grouped work orders) + Molecules (durable workflows)
- **Communication:** Nudge (real-time), Mail (persistent), Seance (history query), Handoff (session transfer)
- **Federation:** "Wasteland" — Gastown instances link via DoltHub for distributed coordination

### Why developers bounce off it
- **25+ novel domain terms** to learn: Town, Rig, Hook, Polecat, Crew, Mayor, Deacon, Witness, Dogs, Boot, Refinery, Bead, Convoy, Formula, Protomolecule, Molecule, Wisp, GUPP, MEOW, NDI, Nudge, Mail, Seance, Sling, Wasteland, Overseer
- **Heavy dependencies:** Dolt (versioned SQL database), Beads (`bd` CLI — another Steveyegge project), tmux, Go 1.25+
- **Massive surface area.** 70+ Go packages, three binaries, a web dashboard, a TUI, launchd/systemd templates
- **Designed for 20-30 agents.** If you have one or two, this is extreme overkill.
- **The naming is clever but alienating.** "Polecats," "Molecules," "Wasteland" — fun for the creator, intimidating for newcomers.

### What it does well
- **Merge queue (Refinery)** — real solution to the multi-agent merge problem CoBuild also faces
- **Agent health monitoring** — three-tier watchdog with automatic recovery
- **Persistent agent identity** — agents survive crashes, sessions transfer state
- **Federation** — cross-machine coordination (future-looking but interesting)

### Relevance to CoBuild
Gastown is what happens when you solve every problem at once. It validates that **the problems CoBuild is solving are real** (work decomposition, agent dispatch, merge conflicts, health monitoring). But it also validates our approach of solving them incrementally:

| Problem | Gastown | CoBuild |
|---------|---------|---------|
| Work tracking | Own system (Beads + Dolt) | Connector to existing systems (CP, Beads) |
| Agent dispatch | Mayor → Polecat hierarchy | Simple dispatch to worktree |
| Merge handling | Built-in Refinery | Serial wave merging (planned) |
| Monitoring | 3-tier watchdog | Stall check skill |
| Communication | Nudge, Mail, Seance, Handoff | Context layers |
| Config | Town/Rig/Hook hierarchy | Global → repo YAML |
| Learning curve | Weeks | Hours |

CoBuild should never become Gastown. If we need a feature Gastown has, we should find the simplest way to provide it.

---

## Where CoBuild Fits

### Our design principles (validated by this analysis)

1. **Start simple, grow into complexity.** Ralph proves developers want simplicity. Gastown proves you can go too far. CoBuild should be closer to Ralph on day one and grow toward (but never reach) Gastown.

2. **Connector over own system.** Gastown built its own work-item tracker (Beads + Dolt). We connect to whatever the developer already uses. Less to learn, less to maintain.

3. **Skills as markdown.** All three projects confirm this pattern. gstack's 28 skills, Ralph's prompt templates, and CoBuild's phase-organized skills are all markdown-driven intelligence.

4. **Config over code.** Adding a phase or gate should be a YAML change. gstack has no config (too rigid). Gastown has too much (too complex). We're in the middle.

5. **Audit everything.** gstack has no audit trail. Ralph has `progress.txt`. Gastown has Dolt-backed history. CoBuild has structured gate records in Postgres — queryable, not just appendable.

6. **Don't invent terminology.** Gastown's 25+ novel terms are a barrier. We use Claude ecosystem terms (connector, skill, hook, scope) plus standard engineering terms (pipeline, phase, gate).

### What we should learn from each

| From | Lesson |
|------|--------|
| **Ralph** | Zero-config should be possible. File-based state is valid for simple cases. The pattern matters more than the code. |
| **gstack** | Great onboarding (clone + setup). Skills should be discoverable and well-documented. A persistent browser/tool daemon is genuinely useful. |
| **Gastown** | The merge queue problem is real and needs solving. Agent health monitoring is essential at scale. Federation is interesting but premature for us. |

### What makes CoBuild different

None of these tools have **structured phase transitions with quality gates and an audit trail**. That's our core value:

- Ralph loops until stories pass — no quality gates, no review phase
- gstack has review skills but no enforcement — you can skip `/review` and go straight to `/ship`
- Gastown has convoys and molecules but they're workflow orchestration, not quality gates

CoBuild's pipeline is: design → decompose → implement → review → done, with gates at each transition that must pass before work moves forward. Every gate verdict is recorded. Every dispatch is tracked. That's the thing that doesn't exist elsewhere.
