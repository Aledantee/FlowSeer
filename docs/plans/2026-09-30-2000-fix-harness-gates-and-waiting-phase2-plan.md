---
title: Harness Gates and Waiting Phase 2, Hook Assertions and Input - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-30-2000-fix-harness-gates-and-waiting-plan.md
---

# Harness Gates and Waiting Phase 2, Hook Assertions and Input - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`tools/hooks/tests/run.sh` fails when an assertion fails, and a hook run
with a stdin that stays open returns within seconds with the result it
gives malformed input today. The phase runs in the coordinating session
and stops at a staged diff for a person's review, since `tools/hooks/` is
a policy surface (parent Decisions).

**Stop condition:** the host's `bash` becomes 4 or newer for every
runtime, which makes the assertion change unnecessary but harmless.

## Decisions

The parent's Decisions apply. Local to this phase:

- Every assertion becomes `[[ … ]] || fail "<what failed>"`, with `fail`
  printing its argument and exiting 1. Why: `/usr/bin/env bash` here is
  GNU bash 3.2.57, where a failing top-level `[[ ]]` does not trigger
  `set -e`. `/bin/bash -c 'set -e; [[ a == b ]]; echo reached'` prints
  `reached` and exits 0, while the same test as the last line of a called
  function exits 1. `[[` is a shell keyword, so a helper that takes the
  test as arguments cannot run it, and `test` lacks the `==` pattern
  match `run.sh` uses.
- The conversion covers indented assertions in loop bodies as well as
  top-level ones, and skips the `[[` inside the heredoc that writes the
  stub `go`. Helper functions whose last line is a `[[ ]]` keep it.
- Hooks read input through one `hook_read_input` in `common.sh`: wait up
  to 5 seconds for the first character with `IFS= read -r -n 1 -t 5`,
  then take the rest with `cat`, and keep the status so `set -e` hooks do
  not abort on it. Why: `hook_init` and six hooks run `$(cat)`, which
  blocks until stdin closes. The harness writes the payload at once, so
  the timeout only fires on a hand run, and `cat` keeps large payloads
  fast. Under `/bin/bash` 3.2.57, a `read -t 3` on a FIFO held open with
  no data returned after 3 seconds with nothing read.
- Empty input keeps each hook's current malformed-input result. The
  PreToolUse guards (`pre-tool-policy.sh`, `protect-generated-bash.sh`)
  deny and `create-worktree.sh` exits 1, as they do today. The reporting
  hooks (Stop, PostToolUse) exit 0. Why: a guard that allowed on empty
  input would fail open on a policy surface. `stop-check.sh` today runs
  its gates on empty input in the current directory, which a hand run
  should not trigger.

## Requirements

1. A false assertion fails the suite. Example: a scratch copy of `run.sh`
   with one expected value flipped exits non-zero and prints the `fail`
   text.
2. A guard hook fed from a FIFO held open with no data denies within 10
   seconds, timed on the hook process.
3. `create-worktree.sh` on the same input exits 1 within 10 seconds.
4. `stop-check.sh` on the same input exits 0 within 10 seconds without
   running its gates.
5. Every existing case in `run.sh` passes unchanged in meaning.

## Open questions

- Whether any other script under `tools/` or `.claude/skills/*/scripts/`
  relies on a top-level `[[ ]]` under `set -e`. A grep found only a
  function-final one in `create-worktree.sh`, which already works.
