---
title: Remote Agent Harness, Phase 2, Orca Coordination, Approval, and Deployment - Plan
type: feat
date: 2026-10-15
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Remote Agent Harness, Phase 2, Orca Coordination, Approval, and Deployment - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`harness serve` runs on the provisioned Linux server, launches one
coordinator per task through Orca, relays its questions and progress to the
task's Telegram topic, and performs a commit or a local-main land only on a
matching, fresh approval. The means are an Orca adapter with recorded
operation intent, a supervisor loop over the phase 1 store and transport,
and a review module that binds approvals to repository fingerprints. Stop
condition: the target Orca cannot launch and recover agent terminals with
no desktop connected.

## Decisions

The parent's Decisions apply: `docs/plans/2026-10-15-1454-feat-remote-agent-harness-plan.md`.
These are carried from the first draft and are checked again when the
phase is re-planned.

- Deliver instructions as files with short terminal pointers, as
  `tools/scripts/skills/delegate/orca_worker.py` already does. Why: terminal
  input is size- and escape-sensitive, and the pattern is proven there.
- Send steering and recovery input with `orca terminal send --retry-request`
  after an ambiguous failure, and interrupt with `--interrupt`. Why: the
  local Orca 1.4.221 help states the retry ID "is bound to the prompt
  payload and exact terminal process incarnation", which Requirement 6
  needs. The target build may differ and is probed first.
- Record operation intent before every Orca or Git mutation and reconcile
  an ambiguous one from Orca and Git evidence, never by repeating it. Why:
  Requirement 6 forbids a blind relaunch.

## Requirements

Parent Requirements 3, 5, 6, and 8, and the end-to-end parts of 2 and 9.

## Units

### U1. Orca task coordination and recovery
Files: tools/scripts/harness/__init__.py, tools/scripts/harness/orca.py, tools/scripts/harness/supervisor.py, tools/scripts/harness/serve.py, tools/scripts/harness/doctor.py, tools/scripts/harness/coordinator.md, tools/scripts/tests/harness/test_orca.py, tools/scripts/tests/harness/test_supervisor.py
After: none
Change: Register `harness serve` and `harness doctor`. Resolve a configured Orca executable and check its installed help before launch. Start one coordinator in a worktree based on a recorded local commit, with the agent and model stored on the task. Retain returned worktree and terminal identities. Coordinators use the bridge for human questions and keep project skills for execution. Pause prevents another workflow stage, cancellation interrupts owned work and preserves changes, and restart reconciles existing identities. Completion requires an explicit result event and corroborating repository evidence.
Tests: Fake CLI fixtures for missing capabilities, failed launch, lost launch response, stale terminal identity, busy steering, pause acknowledgment, cancellation with dirty work, restart with a pending decision, no duplicate launch, and an idle terminal without a result. A live Linux start, question, reply, and retained-worktree check, following `.claude/skills/implement/references/launcher-check.md`.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/harness tools/scripts/tests/harness`

### U2. Review, authorization, and deployment
Files: tools/scripts/harness/review.py, tools/scripts/harness/doctor.py, tools/scripts/tests/harness/test_review.py, tools/scripts/tests/harness/test_acceptance.py, tools/scripts/harness/README.md, tools/scripts/harness/deploy/flowseer-harness.service, docs/README.md
After: U1
Change: Build approval summaries with scope, review verdict, verification evidence, and residual risks. Persist commit and land proposals with base, head, and diff-digest fingerprints, and hand the coordinator a one-use authorization only while they match. Require review, compound, and verifier checkpoints for land, recheck after main integration, and serialize main updates. Document BotFather private-topic enablement, secrets, backups, recovery, and first-run trust steps. `doctor` checks `getMe.has_topics_enabled` and the native message calls. Supply a service template without installing it.
Tests: Stale diff and main fingerprints, replayed approval, wrong-task callback, missing checkpoints, failed verification, and a commit or merge whose acknowledgment is lost. The live exercise in parent Requirement 8, with restart and transport-disconnect cases, logs with credentials removed, and the Orca and agent versions named.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/harness tools/scripts/tests/harness docs/README.md`

Waves: U1 | U2

## Open questions

- The Linux host, Orca build, agent installations, and bot identity have not
  been supplied.
- Upstream documents `orca agent launch`, and the local 1.4.221 help lists
  no such command. `orca worktree create --agent --prompt` exists locally.
  Choose the launch call from the target's installed help.
- Native workflow questions and initial CLI trust prompts are not proven to
  use the bridge. Unrecognized prompts block and notify instead of
  receiving a guessed terminal answer.
