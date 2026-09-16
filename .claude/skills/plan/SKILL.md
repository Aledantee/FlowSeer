---
name: plan
description: Scope and plan bounded FlowSeer work into a decision record under docs/plans/ before implementation. Use when asked to plan, brainstorm, scope, or break down a change, or when a request is too large or too open to implement directly. Not for diagnosing bugs, and not for changes that need no design choice.
argument-hint: "[request or path of an existing plan]"
---

# Plan FlowSeer work

A plan is an implementation decision record: a later session implements from
it without re-deriving the decisions, and a reviewer checks the result against
it. Plans do not override `docs/architecture/` or a convention.

## Skip the plan when

The request fits in one sitting, touches one package (its README and docs
count as the package), and involves no design choice. Implement it directly
with the work sequence in `AGENTS.md` and say that you skipped the plan.

## 1. Orient and frame

Read the source the request touches, the `CONCEPTS.md` entries for its
entities, the `docs/architecture/` records for the area, and the "Read when"
column of `docs/solutions/README.md`. When the request conflicts with an
accepted record, say so first and make amending the record a unit of the
plan, so code and record change together.

Ask only questions whose answer changes the design, at most three, in one
message with a recommended answer each. When the user cannot answer, take the
recommendation, mark the decision "unconfirmed", and repeat it under Open
questions. Every decision carries its reason.

Re-planning a phase starts from a tree that holds the phases before it:
for each phase the parent's `After:` names, the last commit of its
`Landed:` line passes `git merge-base --is-ancestor <sha> HEAD`, and the
parent on `main` shows this phase's own `Landed:` still empty. A worktree
forked before the previous phase merged fails the first test, and a
re-plan from it re-derives that phase as new units; a phase already
landed on `main` fails the second, and a re-plan from it lands the phase
twice. Either way, stop and say which, rather than plan.

### Promote a decision to a direction record

A plan goes stale once the work lands. A decision that outlives the task
belongs in `docs/architecture/`, this repository's architecture decision
record. Promote a decision when reverting it would touch more than one package
or a wire contract, when it constrains work outside this plan's units, when it
changes an accepted record, or when an earlier plan or record already decided
the same question. Which library, test layout, or field name stays a plan
decision. When a decision passes this test, load
`references/direction-record.md` and write the record it describes.

## 2. Gather evidence

Answer one-grep questions yourself. Delegate a question that needs many files
read as `delegate` describes, one bounded question per agent, in parallel
when independent.

Read the RFCs, vendor specs under `spec/`, and library source under
`~/go/pkg/mod` the change depends on, and cite them. For a third-party library
outside the module cache, use Context7 (`mcp__context7__query-docs` when
connected, else the `ctx7` CLI), one question per query; keep the code example
it returns and record the library version the plan relied on. Fetch every URL
before citing it and cite only what the page says. A Decision that rests on a
standard-library API's exact behavior reads `go doc <symbol>` first; a name
and a reputation are not evidence for a security property. A binary fixture
(a pcap, a golden file, a captured payload) is decoded or hex-dumped and its
bytes checked against the claim before the plan cites it, as a page is read
before it is quoted.

## 3. Write the plan

Path: `docs/plans/<date>-<type>-<slug>-plan.md`, `<date>` from
`date +%Y-%m-%d-%H%M`, `<type>` the commit type the work will carry (`feat`,
`fix`, `refactor`, `perf`, `docs`, `chore`).

```yaml
---
title: <Title> - Plan
type: <type>
date: <YYYY-MM-DD>
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready | needs-decisions
status: planned
execution: code | docs | mixed
amends: <path of the plan or direction record this one changes, if any>
superseded_by: <path of the replacing plan; only with status superseded>
parent: <path of the parent plan; only in a phase plan>
---
```

`status` is `planned` until the work lands, then `implemented`,
`partially-implemented`, `superseded`, or `abandoned`. `artifact_readiness`
describes the plan's completeness and does not change with progress.
`review` and `compound` each add a field of their own name beside `status`
when they run (`review: accept`, `compound: no lesson`); `close` reads the
three together, so a plan carries its own checkpoints into the history.

Body, in this order; leave out an empty section.

```markdown
# <Title> - Plan

## Goal
One paragraph: the observable outcome, and the means in one sentence. Then a
one-sentence stop condition: the discovery that would make this plan wrong.

## Decisions
- <Decision>. Why: <reason grounded in code, convention, or evidence>.

## Requirements
Numbered, testable statements, each with one acceptance example: a concrete
input and the expected result, close enough to become a test.

## Out of scope
What a reader might expect and will not find here.

## Units
### U1. <Unit name>
Files: <paths>
After: <units that must land first, or none>
Change: what the code does after the unit, in present tense.
Tests: the test files and cases that prove it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- <paths>`

## Verification
The commands that prove the whole change, and any manual or lab check.

## Definition of done
Checklist: verifier green for every changed path, package README and
convention docs updated in the same change, this plan's `status` set with an
outcome note under its title, no plan labels in code.

## Open questions
What the implementer must decide or ask. Empty is a valid answer.
```

Rules:

- Follow `docs/doc-style.md`. Cite files as repository-relative paths and
  sources with a URL or document section.
- `After:` names only the units whose landed code this unit imports,
  edits, or tests against; a preferred order, a shared convention, or
  "it reads better" is not an `After`. `implement` runs every unit whose
  prerequisites have landed at once, up to three, so each `After` edge
  that is not a real dependency serializes work that could run in
  parallel. Two units that touch the same file are never independent.
  After the Units, write the waves the graph yields, as
  `Waves: U1 U2 | U3 | U4 U5`, and re-cut units whose graph is a chain
  when the files allow a wider one.
- A Change or Tests line names no placeholder: "TBD", "add error
  handling", "implement later", "tests as appropriate" are decisions the
  implementer will make in the plan's name. Write the shape, or write
  that it is decided in a named later unit.
- Requirement and unit labels (R1, U2) stay in the plan and never enter code,
  comments, or commit messages.
- A requirement phrased as what a component knows, sees, or is told is a
  claim about a wire: name the message and field that carry it, or write
  that it does not exist yet and is part of the work. Absent data fails no
  gate until something has to use it.
- A change list naming struct members is a sketch of an interface; the
  contract is the behavior the unit's Tests prove. A change list naming
  dependents, or a deferral's trigger, is a claim checkable against the
  plan's own Decisions before it is written; two parts of one plan
  disagreeing is the normal case, not a surprise.
- A unit adding or changing the exported `Config` of a module under
  `src/modules/` names a test in a package outside that module's directory.
- Over six units or 300 lines, cut what the implementer can decide alone,
  then load `references/phases.md` and split along its dependency
  clusters into a parent plan and phase plans. A request that names a
  decided sequence of changes gets a parent plan by the same reference
  without waiting for the size check.

## 4. Review the plan

Read the plan as the implementer: can each unit start without a question? Fix
the plan where not. Then read each unit's Tests line against the risks the
plan itself names for that unit, in its Decisions, Open questions, and
Change: a risk the plan calls unverifiable, or one a symmetric test cannot
see, gets a test that pins it, a fixture with known bytes, or a sentence in
the unit saying that nothing in it covers that risk. A round trip passes
whatever octet an encoder chooses, so a Tests line asking for one where the
plan calls the wire layout unverifiable prescribes the one test that cannot
catch the risk it names. Where a unit supplies a value to an existing matching
primitive (a prompt regex, a header parser, a routing predicate), trace that
primitive's matching rule (anchor scope, tie-break) against the value and
against every sibling it must not also match; the value's apparent intent is
not what the primitive sees. For more than three units or a schema change,
dispatch one `independent-reviewer` as `delegate` describes, with the plan
path and the question "what would block or mislead an implementer, and what
does the plan contradict in `docs/architecture/` or the conventions?". Apply
the findings that hold.

## 5. Hand off

Run the verifier on the plan and any direction record it added. In Orca, set
the worktree comment to the plan path and its readiness. Report the path, the
readiness, any proposed direction record awaiting acceptance, the open
questions, the waves, and the decision you are least sure of. Do not start
implementing unless asked, and say that a plan of more than one wave is
implemented in a fresh session from the plan file: this session's context
holds the research and the rejected alternatives, and after a compaction
the summary of that is what the implementer would work from. A
correction to this procedure is logged as `compound`, Observe describes.
