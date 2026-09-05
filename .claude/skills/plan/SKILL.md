---
name: plan
description: Scope and plan bounded FlowSeer work into a decision record under docs/plans/ before implementation. Use when asked to plan, brainstorm, scope, or break down a change, or when a request is too large or too open to implement directly. Not for diagnosing bugs, and not for changes that need no design choice.
---

# Plan FlowSeer work

A plan is an implementation decision record. It exists so that a later session
can implement the change without re-deriving the decisions, and so that a
reviewer can check the result against something written down. Plans do not
override accepted direction in `docs/architecture/` or a binding convention.

## Skip the plan when

The request fits in one sitting, touches one package (its README and the
docs that describe it count as the package), and involves no design choice.
A grep of the touched area is enough to decide; do not read further first.
Implement it directly with the work sequence in `AGENTS.md`, keep the same
wording at every site that states the same fact, and say that you skipped the
plan and why.

## Inputs

- The request, in the user's words.
- Any existing plan, direction record, or solution the user points at.

## 1. Orient and frame

Before asking anything, read the source the request touches, the
`CONCEPTS.md` entries for the entities involved, the `docs/architecture/`
records that name the same area, and the "Read when" column of
`docs/solutions/README.md` (the same conditions sit in each solution's
`applies_when` frontmatter). The plan must fit the accepted direction. When
the request conflicts with it, say so first and make the amendment of the
direction record a unit of this plan, so code and record change together.

Ask the user only questions whose answer changes the design. Three is the
usual limit; batch them in one message with your recommended answer for each.
Do not ask about things the code or the conventions already decide. When the
request is clear, ask nothing and move on. When the user cannot answer, take
your recommendation, record it in Decisions marked "unconfirmed", and list it
again under Open questions.

Record each answer and each decision you made yourself in the plan's
Decisions section, with the reason. A decision without a reason is a guess.

## 2. Gather evidence

Most questions are one grep or one bounded read; answer those yourself.
Delegate to `repo-researcher` only when a question needs many files read
(which callers depend on Y across the tree, what a large fixture covers), one
bounded question per agent, in parallel when they are independent.

Read external references (RFCs, vendor specs under `spec/`, library source
under `~/go/pkg/mod`) when the change depends on them, and cite them in the
plan.

## 3. Write the plan

Path: `docs/plans/<date>-<type>-<slug>-plan.md`, where `<date>` is
`date +%Y-%m-%d-%H%M`. `<type>` follows the commit type the work will carry:
`feat` adds behavior, `fix` closes a defect or a gap in a landed contract,
`refactor` keeps behavior, `perf`, `docs`, `chore`.

Frontmatter:

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

`status` records the outcome so a reader can filter plans without opening
them: `planned` until the work lands, then `implemented`,
`partially-implemented`, `superseded`, or `abandoned`. A new plan is always
`planned`. `artifact_readiness` stays as written because it describes the
plan's completeness, not its progress.

Body, in this order. Leave out a section that has nothing to say rather than
filling it.

```markdown
# <Title> - Plan

## Goal
One paragraph: the observable outcome, and the means in one sentence.
Then a one-sentence stop condition: the discovery that would make this plan
wrong ("stop if a non-test caller already depends on the zero value").

## Decisions
- <Decision>. Why: <reason grounded in code, convention, or evidence>.

## Requirements
Numbered, testable statements. Each has one acceptance example: a concrete
input and the expected result, close enough to become a test.

## Out of scope
What a reader might expect and will not find here.

## Units
### U1. <Unit name>
Files: <paths>
Change: what the code does after the unit, in present tense.
Tests: the test files and cases that prove it.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- <paths>`

## Verification
The commands that prove the whole change, and any manual or lab check.

## Definition of done
Checklist. Includes: verifier green for every changed path, package README
and convention docs updated in the same change, and this plan's `status`
set with an outcome note under its title (see `docs/README.md`).

## Open questions
Things the implementer must decide or ask. Empty is a valid answer.
```

Rules for the plan text:

- Follow `docs/doc-style.md`. Plain sentences, one idea each. No em-dash
  chains, no rule-of-three lists, no bold lead-in bullets.
- Cite files as repository-relative paths. Cite sources with a URL or the
  section of the document.
- Requirement and unit labels (R1, U2) are for the plan only. The
  implementer must not copy them into code, comments, or commit messages.
  Say that in the plan's Definition of done.
- A plan under 60 lines is fine for small work. Over 300 lines, split the
  work into two plans or cut what the implementer can decide alone.

## 4. Review the plan

Read the plan once as the implementer: can each unit be started without
asking a question? Fix the plan where the answer is no.

For plans with more than three units or a schema change, dispatch one
`independent-reviewer` with the plan path and the question "what would block
or mislead an implementer, and what does the plan contradict in
`docs/architecture/` or the conventions?". Apply the findings that hold.
Do not dispatch a panel; one careful pass is enough.

## 5. Hand off

Run the verifier on the plan file. Tell the user the path, the readiness, the
open questions, and the one decision you are least sure of. Do not start
implementing unless asked.
