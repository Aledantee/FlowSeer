---
title: Diffing a Merge Against Its First Parent Catches Silently Dropped Changes
date: 2026-09-26
last_verified: 2026-09-26
category: conventions
module: .claude/skills
problem_type: convention
component: conformance-gates
severity: high
applies_when:
  - "Resolving conflicts in a multi-file merge or rebasing a branch that touched shared skills or configuration"
  - "Reviewing a merge commit to verify conflict resolution did not discard non-conflicting changes from either parent"
  - "Investigating why a command, flag, or feature present on a branch vanished after a merge"
related_components: [workflow, verify-change]
tags: [merge, git, conflict-resolution, skills, silent-failure]
---

# Diffing a merge against its first parent catches silently dropped changes

## The situation

When a merge produces conflicts across several files, resolving conflicts in one directory can inadvertently take the incoming branch wholesale for others. Git records the conflicted paths in the default commit message, but it does not check whether the resolved tree preserved the first parent's non-conflicted additions.

Merge `f5be45ac` ("Merge remote-tracking branch 'origin/main' into Aledantee/next-work-triage", 2026-09-24) merged first parent `d6b6b574` with incoming `origin/main` (`670ef8cd`). Conflicts were resolved in `.claude/models/*`, but the resolution in `.claude/skills/` took `670ef8cd` verbatim across seven skill files. This silently dropped implementations that `d6b6b574` had introduced: `runlog.py` integration, `orca-worker.sh grade`, Wave size configuration, machine-wide registry overlays, and bench step-finish accounting. Downstream callers (`drive`, `review`, `implement`, `compound`) and `test_orca_worker.py` still referenced those dropped flags and commands, while the underlying implementations had vanished. Commit `4b3155ec` had to re-merge the parents and restore the dropped changes.

## Why it bites

Standard automated test suites do not flag missing commands in untyped shell scripts or markdown workflows until an agent or runner invokes them. If tests for the dropped features exist on the incoming branch but the merge took the incoming branch's older implementation without those features, the suite passes against the older contract.

Textual conflict resolution cannot detect that a file was resolved by discarding one side completely. Git considers any file whose index is updated as resolved.

## How to apply

After resolving merge conflicts, inspect the resolved diff against the first parent before committing:

```bash
# 1. Check which files differed from the first parent
git diff --stat <first-parent> HEAD -- <path>

# 2. Confirm that files without conflicts show only the incoming parent's additions
git diff <first-parent> HEAD -- <unconflicted-path>

# 3. For conflicted files, ensure both parents' contributions are synthesized
git diff <second-parent> HEAD -- <conflicted-path>
```

When resolving a merge:
1. Never resolve a conflicted directory by checking out a parent wholesale unless that parent completely supersedes the other by design.
2. If flags or subcommands are referenced by sibling skills or test files, verify their implementations exist in the resolved scripts.
3. Compare the diff against the incoming branch (`git diff <second-parent> HEAD`) to verify your branch's additions survived into the merge result.

## Evidence

- `git diff --stat d6b6b574 f5be45ac -- .claude/skills` shows 7 files changed, 231 insertions(+), 511 deletions(-), whereas `git diff --stat 670ef8cd f5be45ac -- .claude/skills` shows 0 diffs for those 7 files.
- `git show f5be45ac:.claude/skills/delegate/scripts/orca-worker.sh | grep -c runlog` returns 0, while `git show d6b6b574:.claude/skills/delegate/scripts/orca-worker.sh | grep -c runlog` returns 6.
- Commit `4b3155ec` restored the dropped implementations across `.claude/skills/delegate/`, `.claude/skills/tune/`, and `.claude/models/registry.yaml`.

## What this does not cover

This rule does not dictate semantic reconciliation when two parents make incompatible changes to the same function. It addresses the silent loss of independent features when conflict resolution resets files to a stale parent.
