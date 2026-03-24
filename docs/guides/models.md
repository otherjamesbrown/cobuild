# Per-Phase Model Selection

Different pipeline phases need different models. Readiness checks and code reviews are judgment tasks -- fast and cheap models work well. Code writing and decomposition need deeper reasoning. CoBuild lets you set the model at each level so you optimize for both quality and cost.

## Quick start

```yaml
# .cobuild/pipeline.yaml
dispatch:
    default_model: sonnet
phases:
    - name: design
      model: haiku
    - name: implement
      model: sonnet
```

## How it works

Models are resolved through a priority chain. The most specific setting wins:

```
gate model  >  phase model  >  review/monitoring model  >  dispatch default_model
```

In code, this is `cfg.ModelForPhase(phaseName, gateName)`. The resolved model is appended to `claude_flags` when spawning agent sessions.

### Resolution examples

Given this config:

```yaml
dispatch:
    default_model: sonnet

phases:
    - name: design
      model: haiku
      gates:
          - name: readiness-review
            model: haiku
    - name: implement
      model: sonnet

monitoring:
    model: haiku

review:
    model: haiku
```

| Phase | Gate | Resolved model | Why |
|-------|------|----------------|-----|
| design | readiness-review | haiku | Gate model (most specific) |
| design | (none) | haiku | Phase model |
| implement | (none) | sonnet | Phase model |
| review | (none) | haiku | Review section model |
| monitoring | stall-check | haiku | Monitoring section model |
| deploy | (none) | sonnet | Dispatch default (no phase model set) |

### Why different models matter

| Task type | Needs | Best model | Reasoning |
|-----------|-------|------------|-----------|
| Readiness review | Judgment, evaluation | haiku | Fast evaluation against criteria; does not generate code |
| Decomposition | Architecture understanding | sonnet | Needs to understand codebase structure for task breakdown |
| Implementation | Code writing | sonnet | Complex code generation, file creation, test writing |
| Code review | Judgment, pattern matching | haiku | Evaluate diffs against criteria; fast and cheap |
| Health checks | Diagnosis | haiku | Quick stall/crash assessment |
| Retrospective | Pattern analysis | haiku | Summarize execution data; judgment task |

### Cost implications

Haiku is significantly cheaper and faster than Sonnet. For a typical design with 5 tasks:

- **1 readiness review** (haiku) -- judgment call
- **1 decomposition** (sonnet) -- needs depth
- **5 task dispatches** (sonnet) -- code writing
- **5 PR reviews** (haiku) -- judgment calls
- **1 retrospective** (haiku) -- pattern summary

By using haiku for judgment tasks, you run 8 of 13 agent sessions on the cheaper model without sacrificing quality on the work that needs it.

## Configuration

### Phase-level model

```yaml
phases:
    - name: implement
      model: sonnet
```

Applies to all agent sessions in this phase unless a gate overrides it.

### Gate-level model

```yaml
phases:
    - name: design
      model: haiku
      gates:
          - name: readiness-review
            model: haiku         # overrides phase model for this gate
```

### Review and monitoring models

These are set in their own config sections:

```yaml
review:
    model: haiku                 # applies to review phase evaluations

monitoring:
    model: haiku                 # applies to health checks and stall diagnosis
```

### Dispatch default

```yaml
dispatch:
    default_model: sonnet        # fallback when nothing else is set
```

This is the bottom of the resolution chain. Every phase without an explicit model uses this.

## Examples

### Example 1: penfold model mapping

The penfold project maps models to task types with this reasoning:

```yaml
dispatch:
    default_model: sonnet              # code-writing fallback

phases:
    - name: design
      model: haiku                     # readiness checks are judgment
      gates:
          - name: readiness-review
            model: haiku
    - name: decompose
      model: sonnet                    # task breakdown needs architecture understanding
      gates:
          - name: decomposition-review
            model: haiku               # reviewing decomposition is judgment
    - name: implement
      model: sonnet                    # code writing
    - name: review
      model: haiku                     # code review is judgment
    - name: done
      gates:
          - name: retrospective
            model: haiku               # reviewing patterns is judgment

monitoring:
    model: haiku                       # health checks

review:
    model: haiku                       # PR review evaluation
```

The pattern: sonnet for creation (code, architecture), haiku for evaluation (reviews, checks, diagnosis).

### Example 2: All-sonnet for high-stakes project

If cost is not a concern and quality is paramount:

```yaml
dispatch:
    default_model: sonnet
# No per-phase models -- everything uses sonnet
```

### Example 3: Opus for critical gates

For a project where readiness review quality is critical:

```yaml
phases:
    - name: design
      model: haiku
      gates:
          - name: readiness-review
            model: opus            # expensive but thorough
    - name: implement
      model: sonnet
```

The gate model overrides the phase model, so readiness reviews use opus while other design-phase work uses haiku.

## Troubleshooting

**Wrong model being used:** Check the resolution chain. Run `cobuild show <id>` to see the effective phase and gate. The model resolves as: gate > phase > review/monitoring > dispatch default. If a gate has a model set, the phase model is ignored for that gate.

**Model not recognized:** CoBuild passes the model name directly to Claude's `--model` flag. Valid values include `haiku`, `sonnet`, `opus`, and their versioned variants. Check Claude CLI docs for the current model list.

**Cost higher than expected:** Check whether your judgment tasks (reviews, health checks) are defaulting to sonnet because no per-phase model is set. Adding `model: haiku` to review and monitoring phases is the fastest way to reduce costs.
