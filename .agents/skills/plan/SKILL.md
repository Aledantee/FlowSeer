---
name: plan
description: Scopes and plans bounded FlowSeer work into a decision record under docs/plans/ before implementation. Use when asked to plan, brainstorm, scope, or break down a change, or when a request is too large or too open to implement directly. Not for diagnosing bugs (`diagnose`), and not for changes that need no design choice.
argument-hint: "[request or path of an existing plan]"
---

# Plan FlowSeer work

A plan is an implementation decision record: a later session implements from
it without re-deriving the decisions, and a reviewer checks the result
against it. Plans do not override `docs/architecture/` or a convention.

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
call of the question tool (`AGENTS.md`, Agent behavior), each with a
recommended answer. A question whose options depend on an answer still
open waits for a second call. A fact the tree, a vendored spec, or a
command can supply is looked up, never asked. When the user cannot answer, take the recommendation,
mark the decision "unconfirmed", and repeat it under Open questions. A
decision the user answered ends with `(decided by the user, <YYYY-MM-DD>)`,
which no worker may edit (`delegate`, Write the brief, item 6). Every
decision carries its reason.

Load `references/replan-phase.md` before re-planning a phase plan (one with
a `parent:` field): it holds the checks that the tree is fit to plan from.

When the plan being re-planned reads `status: implemented` (a review that
ended in `rework` sent it back), set `status: planned` and delete its
`review` and `compound` fields in the edit that makes it
`implementation-ready`. The re-planned units are open work, and a plan left
`implemented` reads as finished to `next`, `drive`, and `land`.

### Promote a decision to a direction record

A decision that outlives the task belongs in `docs/architecture/`, since a
plan goes stale once the work lands and `land` then deletes it. Promote a decision when reverting it
would touch more than one package or a wire contract, when it constrains
work outside this plan's units, when it changes an accepted record, or when
an earlier plan or record already decided the same question. Which library,
test layout, or field name stays a plan decision. When a decision passes
this test, load `references/direction-record.md` and write the record it
describes.

## 2. Gather evidence

Answer one-grep questions yourself. Delegate a question that needs many
files read as `delegate` describes, one bounded question per agent, in
parallel when independent.

- Read and cite the RFCs, vendor specs under `spec/`, and library source
  under `~/go/pkg/mod` the change depends on. Every Decision or Requirement
  about external behavior cites its source as `AGENTS.md`, Investigation
  discipline, defines one. A claim you cannot check says "unverified" and
  goes under Open questions.
- For a third-party library outside the module cache, use Context7
  (`mcp__context7__query-docs` when connected, else the `ctx7` CLI), one
  question per query; keep the code example it returns and record the
  library version the plan relied on.
- Fetch every URL before citing it, and cite only what the page says.
- A Decision that rests on a standard-library API's exact behavior reads
  `go doc <symbol>` first; a name and a reputation are not evidence for a
  security property.
- Decode or hex-dump a binary fixture (a pcap, a golden file, a captured
  payload) and check its bytes against the claim before the plan cites it.
  A wire format gets a known-bytes test from a second source (a dissector,
  a device capture, the vendored spec's example), since a fixture authored
  from the same document as the code cannot show that document misread.

## 3. Write the plan

Path: `docs/plans/<date>-<type>-<slug>-plan.md`, `<date>` from
`date +%Y-%m-%d-%H%M`, `<type>` the commit type the work will carry (`feat`,
`fix`, `refactor`, `perf`, `docs`, `chore`). Scripts in `drive`, `next`,
`implement`, and `verify-change` read the frontmatter fields, the `### U1.`
unit headings, and the `Files:`, `After:`, and `Landed:` lines, so copy
their shape exactly.

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
when they run (`review: accept`, `compound: no lesson`); `land` reads the
three together.

The body, in this order; leave out an empty section:

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
- `After:` names only the units whose landed code this unit imports, edits,
  or tests against; a preferred order, a shared convention, or "it reads
  better" is not an `After`, since `implement` runs every unit whose
  prerequisites have landed at once. Two units that touch the same file are
  never independent. After the Units, write the waves the graph yields, as
  `Waves: U1 U2 | U3 | U4 U5`, and re-cut units whose graph is a chain when
  the files allow a wider one.
- A Change or Tests line names no placeholder ("TBD", "add error handling",
  "implement later", "tests as appropriate"). Write the shape, or write that
  it is decided in a named later unit.
- Requirement and unit labels (R1, U2) stay in the plan and never enter
  code, comments, or commit messages.
- A requirement phrased as what a component knows, sees, or is told is a
  claim about a wire: name the message and field that carry it, or write
  that it does not exist yet and is part of the work.
- A change list naming struct members is a sketch of an interface; the
  contract is the behavior the unit's Tests prove. Check a change list
  naming dependents, or a deferral's trigger, against the plan's own
  Decisions before writing it; two parts of one plan disagreeing is the
  normal case.
- A unit that changes an output shape (emitted source, a golden file, a
  rendered message) lists in `Files:` every existing test and golden file
  holding the old shape; an unlisted one blocks the implement pass or
  collides with a parallel unit. Grep `*_test.go` and `testdata/` for the
  distinctive token of the old output (`with-hyphen`).
- A plan for a checker, linter, parser, or other tool that reads input
  says under Out of scope whose input it reads and whether that author is
  trusted. `review` briefs its reviewer with that sentence, so a plan that
  omits it invites findings about hostile input no one meant to handle.
- A unit adding or changing the exported `Config` of a module under
  `src/modules/` names a test in a package outside that module's directory.
- Over six units or 300 lines (an inventory of sites excluded), cut what the
  implementer can decide alone, then load `references/phases.md` and split
  along its dependency clusters into a parent plan and phase plans. A
  request that names a decided sequence of changes gets a parent plan by the
  same reference without waiting for the size check.

## 4. Review the plan

Three reads, in this order; fix the plan after each.

1. As the implementer: can each unit start without a question? Every claim
   about external behavior carries its source as step 2 requires, or reads
   "unverified" and appears under Open questions. For each Decision whose
   reason is where data already lives (a host that holds the address, a
   store that has the row), ask whether the Requirement needs that coupling
   or only the data. A coupling the Requirement does not need is removed or
   becomes an Open question.
2. Each unit's Tests line against the risks the plan itself names for that
   unit, in its Decisions, Open questions, and Change. A risk the plan calls
   unverifiable, or one a symmetric test cannot see, gets a test that pins
   it, a fixture with known bytes, or a sentence in the unit saying nothing
   in it covers that risk. Where the plan calls a wire layout unverifiable,
   a round-trip test cannot catch it, since it passes whatever octet an
   encoder chooses. A wire-format Tests line names a second-source fixture,
   as step 2 requires.
3. Each value a unit supplies to an existing matching primitive (a prompt
   regex, a header parser, a routing predicate): trace the primitive's
   matching rule (anchor scope, tie-break) against the value and against
   every sibling it must not also match.

For more than one unit or a schema change, then dispatch one
`independent-reviewer` as `delegate` describes, with the plan path and the
question "what would block or mislead an implementer, what does the plan
contradict in `docs/architecture/` or the conventions, and which cited
external source does not say what the plan claims?". Apply the findings
that hold.

## 5. Hand off

Run the verifier on the plan and any direction record it added. In Orca, set
the worktree comment to the plan path and its readiness. Report the path,
the readiness, any proposed direction record awaiting acceptance, the open
questions, the waves, and the decision you are least sure of.

End by asking the user what happens next (`AGENTS.md`, Agent behavior), with
the options that fit:

- A plan of one wave: implement it now in this session (recommended when no
  question is open); revise the plan; stop here.
- A plan of more than one wave: stop here and implement from the plan file
  in a fresh session (recommended, since after a compaction the implementer
  would work from a summary of this session's research); implement now
  anyway; revise the plan.
- A parent plan with phase plans: run `drive` on the parent (recommended,
  since it takes each phase through implement, review, and compound and
  lands it before the next); implement the ready phase plan, naming its
  path, in a fresh session; stop here. `implement` does not run a parent
  plan, whose units are plan files.
- `artifact_readiness: needs-decisions`: ask the unresolved design questions
  instead; do not offer the plan for implementation until they are
  answered. Open questions alone do not block the offer: name them in the
  option's reason, since `implement` rules on them or asks.
- A proposed direction record: accept it, amend it, or leave it proposed.

Implement only on that answer. Log a correction to this procedure with
`compound`, as Observe describes.
