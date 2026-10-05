---
title: Python Scripts Under uv Phase 3, Hooks - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: code
---

# Python Scripts Under uv Phase 3, Hooks - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The ten hook scripts and two helpers under `tools/hooks/` are modules under
`tools/scripts/hooks/`, their suite is unittest, both runtime configurations
start them through `uv`, and `tools/hooks/` is gone.

## Decisions

- The parent plan's Decisions and the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  apply.
- The suite is ported before any hook. `tools/hooks/tests/run.sh` (1,325
  lines) becomes unittest cases that take the hook's command line as a
  parameter. The cases run against the shell hooks first. A case that cannot
  be expressed against both is listed in the plan with the reason.
- `common.sh` becomes `lib/hookio.py`: read the event, resolve paths
  relative to the repository, and emit `deny`, `ask`, or added context.
  `tree-state.sh` becomes `lib/treestate.py`. `json` replaces `jq`.
- Claude Code registers each hook in exec form:
  `"command": "uv", "args": ["run",
  "${CLAUDE_PROJECT_DIR}/tools/scripts/run.py", "hook", "<name>"]`. The
  hooks reference says a hook with `args` is "spawned directly with no shell
  involved" on every platform, with path placeholders substituted as plain
  strings (https://code.claude.com/docs/en/hooks). The re-plan confirms the
  placeholder is expanded inside `args` with a hook that prints its
  arguments.
- The Codex registration is decided in the re-plan from the Codex hooks
  documentation or source. Today `.codex/hooks.json` holds POSIX shell
  strings, and how Codex starts them on Windows is unverified.
- `create-worktree.sh` and `worktree-guard.sh` are ported last, since a
  fault in either blocks every edit in a session.
- The Stop gate is timed again on a cold and a warm Go build cache against
  the 30-second timeout, as `docs/agent-steering.md` records for the shell
  version, with the first `uv run` of a session included.
- The shell verifier outlives this phase, so its references move here:
  `verify-change.sh:297` and `:336` (path classes), `:929` to `:933`
  (`shellcheck` globs and the hook suite), and `:1043` to `:1050`, which
  skips a missing `tree-state.sh` silently. The listing must still be
  rewritten after a pass, through `lib/treestate.py`, or the dirty marker
  re-marks verified files. `.agents/skills/verify-change/SKILL.md:90` names
  `tree-state.sh` as well.
- `AGENTS.md`, `pre-tool-policy`, `docs/agent-steering.md`, and the `steer`
  skill name the new paths in the same change. Every edit here is to a
  policy surface and prompts the person.

## Requirements

1. Every ported case passes against the shell hook and the Python hook.
   Example: the case "an edit under `generated/` is denied" passes with the
   command set to `tools/hooks/pre-tool-policy.sh` and to `uv run
   tools/scripts/run.py hook pre-tool-policy`.
2. A hook that cannot start fails closed. Example: with `uv` absent from
   `PATH`, a `PreToolUse` hook yields a blocking error, not a silent allow.
   How each runtime treats a command that cannot be spawned is read from
   its documentation in the re-plan.
3. No hook calls `jq`. Example: `git grep -w jq -- tools/scripts` prints
   nothing.
4. The Stop gate finishes inside its timeout on an empty Go build cache.
   Example: the measured time is recorded in `docs/agent-steering.md`
   beside the command that reproduces it.
