---
name: compound
description: Captures a verified FlowSeer lesson as a solution under docs/solutions/ with applies_when frontmatter and quoted evidence, refreshes the existing solutions against the current tree, or logs an observation about a skill or agent that did not fit the work. Use after a fix or decision taught something the code does not make obvious, when asked to compound, capture a learning, or audit stale solutions, or when a workflow skill ends with a process correction.
argument-hint: "[refresh | observe | the lesson in one sentence]"
---

# Capture or refresh a FlowSeer solution

A solution preserves a lesson the happy path would miss, indexed by the
conditions in which it applies. Vocabulary it introduces goes to
`CONCEPTS.md`. Neither replaces a convention doc or a direction record. An
observation records a gap in a skill, agent, or hook for a person to act on.

## Gate

Write a solution only when all three hold:

- The lesson is verified by a test, a measurement, or observed behavior, and
  you can quote the source lines that make it true.
- It is not stated in, or derivable within minutes from, the code,
  `AGENTS.md`, a convention doc, or a direction record. When a record states
  the rule, link to it.
- It will apply again. A one-off fix goes in the commit message.

Otherwise say why no solution is warranted and stop.

A solution never carries a decision. When the tree disagrees with an
accepted `docs/architecture/` record, or a decision made during the work
should bind future work, say so and stop: the record is amended or proposed
through `plan`. A solution with `problem_type: architecture_pattern`
explains how to apply a decision and links to the record that made it.

Whichever way the gate goes, record the outcome where `land` reads it
(`land`, step 1): `compound: <solution path>`, `compound: no lesson`, or
`compound: observation logged`.

- With a plan: a field in the plan's frontmatter beside `status`, committed
  together with the solution, then the verifier on the changed paths so the
  receipt post-dates the commit.
- Without a plan: a line appended to
  `$(git rev-parse --git-dir)/flowseer-checkpoints` with
  `.claude/skills/verify-change/scripts/ledger.py checkpoint compound "<outcome>"`.
- In Orca, also append the entry to the card:

```bash
orca worktree set --worktree active --comment "<existing>; compound: no lesson" --json
```

## Capture

### 1. Find the lesson

Name the mistake or discovery in one sentence, then the rule that prevents or
reuses it. If `docs/solutions/README.md` lists a solution that covers it,
extend that one. When the change already amended a solution, check that
solution's citations and index row and say so.

### 2. Ground it

Cite evidence as repository-relative paths with line numbers
(`src/common/service/bus.go:132`): the code that embodies the rule, the test
that proves it, the measurement if any. Every non-obvious claim cites one.
Label a measurement with its date and platform. Reuse existing `component`,
`category`, and tag values.

### 3. Write it

Path: `docs/solutions/<category>/<slug>.md`, `category` an existing
directory or a new one if none fits.

```yaml
---
title: <Statement of the rule, as a sentence>
date: <YYYY-MM-DD>
category: <directory name>
module: <primary path, e.g. src/common/service>
problem_type: architecture_pattern | convention | bug
component: <area, reuse corpus values>
severity: critical | high | medium | low
applies_when:
  - "<concrete situation in which an agent should read this>"
related_components: [<other areas>]
tags: [<lowercase-hyphenated>]
---
```

For `problem_type: bug` add `symptoms`, `root_cause`, and `resolution_type`.

Body: the situation, what is true and why, how to apply it with a short
working example, the evidence as quoted lines with paths, and what it does not
cover. Follow `docs/doc-style.md`. Under about 120 lines.

### 4. Index and vocabulary

Add a row to `docs/solutions/README.md` whose "Read when" sentence matches
`applies_when`; extend the row when you extended `applies_when`. Set
`last_verified` to today on any solution whose citations you re-checked.

Ask the user before adding a new `CONCEPTS.md` term, with the term and its
one-line definition as the option to accept. Never edit `AGENTS.md` or
another policy surface: say where the rule belongs and ask whether to log it
as an observation for `steer`.

### 5. Verify

Run the verifier on the changed files and report the path and the one
sentence a reader should remember.

When the work's `implement` and `review` checkpoints are in place, end by
asking the user (`AGENTS.md`, Agent behavior) whether to run `land` now or
stop here. The same question ends a run the gate stopped with
`compound: no lesson` or `compound: observation logged`.

## Refresh

Load `references/refresh.md` when the argument is `refresh` or the user asks
to audit the existing solutions; it holds the audit steps and outcomes.

## Observe

Log an entry under "Entries" in `docs/agent-observations.md` when:

- The user corrected how a skill, agent, or hook worked rather than what
  the code does.
- A step did not fit the task, or a step was followed and still produced
  the wrong result. Say which case; the second wants enforcement, not
  louder prose.
- The same manual sequence recurred across sessions and no skill covers it.
  Target it as `new skill candidate: <working name>`.
- A skill step never fired across several uses. Suggest removing it.

Any skill or agent is a valid target, `compound`, `delegate`, and `steer`
included.

```markdown
## 2026-09-05 review: reviewer brief carried the author's claim of safety
Skill or agent: `.claude/skills/review/SKILL.md`, step 3.
What happened: the brief said the change was "tested and safe"; the
reviewer accepted an untested error path on that basis. The step was
followed as written.
Suggested change: state intended behavior as a specification and drop
such claims from the brief.
```

Do not log a one-off correction that would not recur in another task, a
preference a skill already states, a tool failure unrelated to the
procedure, or a workaround forced by this task's circumstances. The test:
would the entry still name a missing or wrong rule for another task using
the same skill? If not, it is task context.

An entry logged from another skill's step (`implement`, `review`, and the
rest end that way) records no `compound:` field or checkpoint, since that
field is the compound stage's outcome and `land` reads it as one. A
`compound` run writes `compound: observation logged` only when it ends with
an observation and no solution.

Log it and stop. Do not edit the skill, agent, hook, or any policy surface
from this mode; `steer` works the queue when a person asks and deletes each
entry it applies or rejects. A lesson about the code is a solution, not an
observation.
