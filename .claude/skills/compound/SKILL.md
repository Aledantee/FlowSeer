---
name: compound
description: Capture a verified FlowSeer lesson as a solution under docs/solutions/ with applies_when frontmatter and quoted evidence, refresh the existing solutions against the current tree, or log an observation about a skill or agent that did not fit the work. Use after a fix or decision taught something the code does not make obvious, when asked to compound, capture a learning, or audit stale solutions, or when a workflow skill ends with a process correction.
argument-hint: "[refresh | observe | the lesson in one sentence]"
---

# Capture or refresh a FlowSeer solution

A solution preserves a lesson that reading the happy path would miss, indexed
by the conditions in which it applies. Vocabulary it introduces goes to
`CONCEPTS.md`. Neither replaces a convention doc or a direction record. An
observation records a gap in a skill, agent, or hook for a person to act on.

## Gate

Write a solution only when all three hold:

- The lesson is verified: a test, a measurement, or observed behavior backs
  it, and you can quote the source lines that make it true.
- It is not already stated in, or derivable within minutes from, the code,
  `AGENTS.md`, a convention doc, or a direction record. When a record states
  the rule, link to it.
- It will apply again. A one-off fix goes in the commit message.

Otherwise say why no solution is warranted and stop.

A solution never carries a decision. When the lesson is that the tree
disagrees with an accepted `docs/architecture/` record, or that a decision
made during the work should bind future work, say so and stop: the record is
amended or proposed through `plan`. A solution with
`problem_type: architecture_pattern` explains how to apply a decision and
links to the record that made it.

Whichever way the gate goes, leave the outcome where `close` reads it
(`close`, step 1): `compound: <solution path>`, `compound: no lesson`, or
`compound: observation logged`, as a field in the plan's frontmatter beside
`status`, committed together with the solution and followed by the verifier
on the changed paths so the receipt post-dates the commit, or as a line
appended to `$(git rev-parse --git-dir)/flowseer-checkpoints` when the work
has no plan, with
`.claude/skills/verify-change/scripts/ledger.py checkpoint compound "<outcome>"`.
In Orca, append the same entry to the card:

```bash
orca worktree set --worktree active --comment "<existing>; compound: no lesson" --json
```

## Capture

### 1. Find the lesson

Name the mistake or discovery in one sentence, then the rule that prevents or
reuses it. Check `docs/solutions/README.md` for a solution that already covers
it and extend that one. When the change already amended a solution, check
that solution's citations and index row and say so.

### 2. Ground it

Collect evidence as repository-relative paths with line numbers
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
one-line definition as the option to accept. This skill never edits
`AGENTS.md` or another policy surface: say where the rule belongs and ask
whether to log it as an observation for `steer`.

### 5. Verify

Run the verifier on the changed files and report the path and the one
sentence a reader should remember.

When the work's `implement` and `review` checkpoints are in place, end by
asking the user (`AGENTS.md`, Agent behavior) whether to run `close` now
or stop here. The same question ends a run the gate stopped with
`compound: no lesson` or `compound: observation logged`.

## Refresh

Audit each solution under `docs/solutions/`. Dispatch one worker per
solution, up to three at once, as `delegate` describes (an Orca
worker, else a `general-purpose` subagent on `sonnet`). Edit
`docs/solutions/README.md` from the coordinating session only.

1. Open every cited path and confirm the quoted lines and symbols exist.
   Check that `module` exists, `applies_when` still describes the situation,
   and the README row still matches.
2. Re-run the test it relies on. Skip a gate that needs hardware, Docker, or
   more than a few minutes, and say so. A dated measurement stays as history.
3. When a later convention, `docs/architecture/` record, style rule, or
   sibling solution states the same thing, replace the restatement with a
   link.

Outcomes: kept, fixed (citations only), rewritten (a claim changed), removed
(the tree no longer behaves as described; delete the file and its row). Set
`last_verified` on every solution checked. Run the verifier on the changed
files and report the outcomes table with a reason per row.

## Observe

Log an entry under "Entries" in `docs/agent-observations.md` when one of
these happened:

- The user corrected how a skill, agent, or hook worked rather than what
  the code does.
- A step did not fit the task, or a step was followed and still produced
  the wrong result. The second case is the stronger signal: a rule that was
  read and violated wants enforcement, not louder prose. Say which case.
- The same manual sequence recurred across sessions and no skill covers it.
  Target it as `new skill candidate: <working name>`.
- A skill step never fired across several uses. Suggest removing it.

Any skill or agent is a valid target, including `compound`, `delegate`, and
`steer`.

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

Log it and stop. Do not edit the skill, agent, hook, or any policy surface
from this mode; `steer` works the queue when a person asks for it and
deletes each entry it applies or rejects. A lesson about the code is a
solution, not an observation.
