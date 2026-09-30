---
title: Harness Gates and Waiting - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
---

# Harness Gates and Waiting - Plan

## Goal

The agent harness lands sound code, but a few gaps let wrong work reach a
branch unnoticed, and coordinators pay for their own waiting. After this
plan, a Claude lane that ran on another model is caught before its branch
merges, every workflow merge is checked for work it reset, the hook test
suite fails when an assertion fails, a hook run by hand cannot block on its
input, `orca-worker.sh wait` returns once per settled stage, `drive` hands
off to a fresh session per phase, and the review fix loop turns to prior art
or `plan` after two rounds on one mechanism. Three phases carry it: skill
scripts for the gates, the hook changes, and the waiting and rework rules.

**Stop condition:** Claude Code stops recording a per-message `model` or
the `model_refusal_fallback` record in its session files, or Orca stops
reporting `childWorktreeIds`. The model check and the child-aware wait
would then each need another signal.

## Decisions

- One parent with three phases, not two separate plans. Why: the units
  share `orca-worker.sh`, `drive/SKILL.md`, `delegate/SKILL.md`, and
  `fix-loop.md`, the order between them is decided, and
  `next/scripts/plan-queue.py` reads `After` only between phases of a
  parent, so an order written as prose between two plans is invisible to
  `next` and `drive`.
- The merge check fails on a reset path, where a merge dropped every line
  one side added, and lists without failing the added lines a merge lacks
  (decided by the user, 2026-09-30). Why: a reset has no legitimate
  reading and is the `f5be45ac` drop in
  `docs/solutions/conventions/diffing-a-merge-against-its-first-parent-catches-silently-dropped-changes.md`.
  A partial drop such as the one `a81410c6` repaired also fires on a
  deliberate conflict resolution, so it goes into the merger's report.
- After each phase of a parent plan lands, `drive` starts a successor
  Claude session in its worktree and ends its own turn (decided by the
  user, 2026-09-30). Why: `drive` resumes from files, so a fresh session
  loses nothing, while one session driving every phase carries all
  earlier phases in its context for every later call.
- The hook changes form their own phase and run in the coordinating
  session. Why: `tools/hooks/` is a policy surface (`AGENTS.md`, Hard
  boundaries). `delegate` item 6 forbids a worker to change or run
  anything there, and `drive` step 4 parks a policy-surface change, so the
  phase stops at a staged diff for a person's review.

## Requirements

Each phase plan states its requirements with examples. The whole change
claims:

1. A Claude lane whose session files show another model or a fallback
   record fails `orca-worker.sh check` before any merge. Phase 1.
2. A merge that resets a path one side changed fails `merge-check.py` at
   every workflow merge site. Phase 1.
3. A fix worker that needs a file beyond the classes `fix-loop.md` step 1
   allows reports a blocker instead of a stand-in. Phase 1.
4. A false assertion in `tools/hooks/tests/run.sh` fails the suite. Phase 2.
5. A hook whose stdin stays open without data returns within 10 seconds
   with its current malformed-input result. Phase 2.
6. `wait` does not return `idle` while a child lane of the waited lane is
   working. Phase 3.
7. `drive` hands off after each landed phase. Phase 3.
8. The fix loop stops patching after two rounds on one mechanism and after
   three rounds in total. Phase 3.

## Out of scope

- The effort level the registry routes `execute` and `review-unit` at. It
  pins `execute` at `xhigh` because the integration tier was measured there
  (`~/.claude/models/registry.yaml`, the `roles.execute` comment). Whether
  `medium` holds on that tier is a `tune` calibration in its own session,
  with any registry change on the user's approval.
- Keeping the host awake during a drive, and retrying on `index.lock`.

## Units

### U1. Correctness gates
Files: `docs/plans/2026-09-30-2000-fix-harness-gates-and-waiting-phase1-plan.md`
After: none
Landed: `ea366104..69b9117c`

### U2. Hook assertions and input
Files: `docs/plans/2026-09-30-2000-fix-harness-gates-and-waiting-phase2-plan.md`
After: none
Landed:

### U3. Waiting and rework
Files: `docs/plans/2026-09-30-2000-fix-harness-gates-and-waiting-phase3-plan.md`
After: U1
Landed:

Waves: U1 U2 | U3

## Verification

Each phase's verifier run, then one drive of a two-phase parent plan in
which the coordinator wakes once per settled stage and a successor terminal
starts after the first phase.

## Definition of done

- [ ] Every phase reads `implemented` with its `Landed:` range filled.
- [ ] Verifier green on the union of changed paths.
- [ ] Phase 2's staged diff reviewed by a person before commit.
- [ ] This plan's `status` set with an outcome note under its title.
- [ ] No plan labels in code or commit messages.
