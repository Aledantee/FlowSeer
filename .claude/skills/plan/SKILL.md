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

### Promote a decision to a direction record

A plan goes stale once the work lands. A decision that outlives the task
belongs in `docs/architecture/`, this repository's architecture decision
record. Promote a decision when reverting it would touch more than one package
or a wire contract, when it constrains work outside this plan's units, when it
changes an accepted record, or when an earlier plan or record already decided
the same question. Which library, test layout, or field name stays a plan
decision.

Write `docs/architecture/<date>-<slug>-direction.md` with the frontmatter of
the existing records and `status: proposed-direction`, add a "Proposed
direction" row to `docs/architecture/README.md`, and cite the record from the
plan's Decisions. When it amends an accepted record, edit that record in the
unit that changes the code and name it in `amends`. The body gives the
context, the decision, the alternatives and why they lost, and the
consequences. Write only what the evidence supports; nobody edits a record
after acceptance. Only a person sets `accepted-direction`: ask for it in the
handoff and treat a proposed record as non-binding until then.

## 2. Gather evidence

Answer one-grep questions yourself. Delegate a question that needs many files
read as `delegate` describes, one bounded question per agent, in parallel
when independent.

Read the RFCs, vendor specs under `spec/`, and library source under
`~/go/pkg/mod` the change depends on, and cite them. For a third-party library
outside the module cache, use Context7 (`mcp__context7__query-docs` when
connected, else the `ctx7` CLI), one question per query; keep the code example
it returns and record the library version the plan relied on. Fetch every URL
before citing it and cite only what the page says.

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
---
```

`status` is `planned` until the work lands, then `implemented`,
`partially-implemented`, `superseded`, or `abandoned`. `artifact_readiness`
describes the plan's completeness and does not change with progress.

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
- `After: none` means the unit can land with every other unit absent, so
  `implement` may run it in parallel. Two units that touch the same file are
  never both `none`.
- Requirement and unit labels (R1, U2) stay in the plan and never enter code,
  comments, or commit messages.
- Over 300 lines, split the plan or cut what the implementer can decide alone.

## 4. Review the plan

Read the plan as the implementer: can each unit start without a question? Fix
the plan where not. For more than three units or a schema change, dispatch one
`independent-reviewer` as `delegate` describes, with the plan path and the
question "what would block or mislead an implementer, and what does the plan
contradict in `docs/architecture/` or the conventions?". Apply the findings
that hold.

## 5. Hand off

Run the verifier on the plan and any direction record it added. In Orca, set
the worktree comment to the plan path and its readiness. Report the path, the
readiness, any proposed direction record awaiting acceptance, the open
questions, and the decision you are least sure of. Do not start implementing
unless asked. A correction to this procedure is logged as `compound`, Observe
describes.
