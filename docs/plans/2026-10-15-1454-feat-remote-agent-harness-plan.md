---
title: Remote Agent Harness - Plan
type: feat
date: 2026-10-15
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Remote agent harness

## Goal

Start and steer FlowSeer work from Telegram, answer design questions, and
authorize commits and local-main landing while agents run through Orca on
a dedicated Linux server. Telegram's native AI interface provides task
topics, rich decision cards, and streamed updates. A persistent host-side harness connects it
to task coordinators and the existing project workflow.

Stop if the target Linux Orca installation cannot launch and recover agent
terminals without a connected desktop. Verify that before integrating the bot.

## Decisions

- Telegram is the first control surface. It supports private chat, long
  polling, and callback buttons through the [Bot API](https://core.telegram.org/bots/api).
- Use Telegram's native AI agent features: private-chat topics, streaming
  with a Stop control, and rich messages with tables, expandable details,
  and action buttons. The [AI Agents guide](https://core.telegram.org/bots/features#ai-agents)
  documents this combination. It covers the requested interface without a
  Mini App or a hosted web API.
- Keep code diffs out of Telegram, including attachments and expandable
  sections. Approval uses outcome summaries, verification evidence, and
  risks. Internal fingerprints still bind it to the exact changes, and
  automated review continues to inspect code.
- Linux with Orca is the execution target. Orca's
  [CLI reference](https://www.onorca.dev/docs/cli/reference#runtime-commands)
  documents headless serving and Linux service management. The target
  installation still needs a live capability check.
- Follow the proposed direction in
  `docs/architecture/2026-10-15-remote-agent-harness-direction.md`. It needs
  acceptance before implementation because it constrains task coordination
  and the human decision boundary across workflows.
- Use a new `harness` Python command group and a SQLite database outside
  the checkout. `tools/scripts/run.py:collect_groups` discovers literal
  command mappings without an entry-point edit. No direct dependency or
  policy-surface change is planned.
- Give every task a coordinator with an isolated worktree. Existing
  delegated workers return blockers to it. The coordinator calls the
  structured decision bridge, rather than relying on terminal-text parsing
  to discover questions. Existing `drive` and `land` rules remain binding.
- Support the full workflow, including explicit commit and land decisions.
  Approval binds the operation, base and head commits, worktree diff digest,
  and task revision. A changed proposal needs another decision.
- Serialize landing on the server's local `main`. Work branches start from
  a recorded local commit. Preserve the repository's no-fetch, no-push rule.

## Requirements

1. Only the configured Telegram user and private chat can control tasks.
   An otherwise valid command from another account produces no task event
   and discloses no repository content.
   Reply, callback, topic, and draft identities must match the stored task
   and decision. A topic ID alone grants no authorization.
2. An overview rich message groups tasks by active, awaiting decision,
   blocked, and finished states. New Task requests a natural-language brief
   and creates a private-chat topic with a durable task ID. Messages in that
   topic address its task. Replaying the Telegram update after a crash
   returns the task instead of creating another task or topic.
3. Task topics contain a persistent status card, bounded activity updates,
   pending decisions, and Steer, Pause, Resume, and Cancel buttons. Steering
   during work is queued until the coordinator's next safe boundary.
   Pause and cancellation report requested until acknowledged. Cancellation
   preserves dirty worktrees and never masquerades as successful completion.
4. `harness bridge ask --task <id> --request <file>` validates a question
   with concrete options, persists it, and waits for its answer. Telegram
   replies carry the decision ID and revision. Answering task A cannot
   unblock task B. Repeated replies cannot apply the decision twice.
5. Rich approval cards show the outcome, change scope, verification results,
   review verdict, remaining risks, and proposed commit or land operation.
   No Telegram message, attachment, or expandable section includes a diff.
   Decision cards show a question, alternatives with tradeoffs, and a
   recommendation. Custom Answer requests a reply to the stored decision.
   Modifying a file after this preview invalidates its approval. A land
   operation also rechecks the current main and required workflow checkpoints.
6. Restarting the harness preserves tasks, pending questions, delivery
   records, and Orca identities. An interrupted launch is reconciled or
   marked blocked for investigation. It is never retried as a blind launch.
7. Notify on a question, completion, failure, or actionable blocker.
   Unchanged progress produces no repeated messages. A transport retry may
   duplicate a notification but never a commit, launch, or decision effect.
8. A Linux acceptance exercise starts a task, answers a question, steers it,
   reviews its summary, commits it, and lands it through Telegram's native
   interface without typing a slash command. Restart
   the harness with a question pending and repeat a callback. Retain the
   worktree until the maintainer removes it.
9. Stream bounded progress through `sendRichMessageDraft` and persist the
   completed response with `sendRichMessage`. Route a
   `stopped_message_generation` update by chat, topic, and draft ID to a
   cancellation request. Show requested until the owned worker acknowledges
   cancellation. A stale draft's Stop control cannot cancel another task.
   Telegram's [draft API](https://core.telegram.org/bots/api#sendrichmessagedraft)
   defines the temporary preview and final-message requirement.

## Out of scope

Diff displays, Mini Apps, browser dashboards, other chat services,
desktop-session migration, automatic
repository synchronization, provider remote products, and live-device
mutation are outside this change. Server purchasing and credential entry
are provisioning steps, not actions performed by a task worker.

Telegram updates are untrusted until the configured sender and private chat
are checked. Escape worker text before placing it in rich-message blocks.
Worker output is data, not formatting instructions or authorization.
Agents and processes under the configured server account are trusted. This
change handles malformed bridge data but does not contain a hostile agent
that already has the account's filesystem and process capabilities.

## Units

### U1. Durable task and decision contract
Files: tools/scripts/harness/__init__.py, tools/scripts/harness/state.py, tools/scripts/harness/bridge.py, tools/scripts/tests/harness/__init__.py, tools/scripts/tests/harness/test_state.py, tools/scripts/tests/harness/test_bridge.py
After: none
Change: Register `harness bridge`. Store tasks, topic mappings, draft identities, events, input updates, operations, decisions, answers, and an outbound notification queue outside Git. Define versioned JSON question, progress, result, and acknowledgment payloads. The bridge validates task capabilities, bounds file sizes, and waits with a cancellation path. Database transitions enforce one decision effect and one active mutation per task. A result event names artifact paths and evidence, without setting plan checkpoints itself.
Tests: Replay an update, answer twice, answer another task, reject an obsolete revision, reject malformed and oversized requests, cancel a waiting bridge call, and recover a transaction interrupted before commit. Include concurrent decision responses with only one accepted answer.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/harness tools/scripts/tests/harness`

### U2. Telegram transport
Files: tools/scripts/harness/telegram.py, tools/scripts/harness/render.py, tools/scripts/tests/harness/test_telegram.py, tools/scripts/tests/harness/test_render.py
After: none
Change: Implement standard-library HTTPS calls for polling, private topic creation and routing, rich-message sending and editing, streamed drafts, callback acknowledgment, and custom-answer reply prompts. Use typed rich-message blocks to render an overview table, task status, expandable activity summaries, decision alternatives, and action buttons. Drafts expose native Stop and finish as persistent messages. Normalize stopped_message_generation updates with chat, topic, and draft identities. Accept only the configured sender in the configured private chat. Use opaque callback IDs within Telegram's documented 64-byte limit. Honor API retry delays and redact token-bearing request URLs from errors. Reject an existing webhook configuration instead of silently changing it. Never render or attach repository diffs.
Tests: Exercise fixtures for authorized and unauthorized senders, group messages, topic routing, duplicate updates, forged callback IDs, custom-answer replies, native Stop identity, draft finalization, escaped worker text, bounded cards, webhook conflicts, network timeouts, API errors, and retry delays. Assert that approval and result renderers accept summary data rather than raw patches. Use the [Telegram API](https://core.telegram.org/bots/api) for topic, rich-message, draft, and callback fixtures. Live acceptance checks tables, expandable sections, actions, and Stop on the intended mobile client.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/harness/telegram.py tools/scripts/harness/render.py tools/scripts/tests/harness/test_telegram.py tools/scripts/tests/harness/test_render.py`

### U3. Orca task coordination and recovery
Files: tools/scripts/harness/__init__.py, tools/scripts/harness/orca.py, tools/scripts/harness/supervisor.py, tools/scripts/harness/serve.py, tools/scripts/harness/doctor.py, tools/scripts/harness/coordinator.md, tools/scripts/tests/harness/test_orca.py, tools/scripts/tests/harness/test_supervisor.py
After: U1 U2
Change: Register `harness serve` and `harness doctor`. Resolve a configured Orca executable and check installed CLI capabilities before launch. Start one coordinator in an explicitly selected local-base worktree with the chosen installed agent and model. Record operation intent before calling Orca and retain returned worktree and terminal identities. Reconcile ambiguous operations before further input. Deliver instructions as files with short terminal pointers, using `tools/scripts/skills/delegate/orca_worker.py` as the existing delivery pattern. Coordinators use the bridge for human questions and retain project skills for execution. Pause prevents another workflow stage, cancellation interrupts owned work and preserves changes, and restart reconciles existing identities. Completion requires an explicit result and corroborating repository evidence. Polling state advances only after durable input storage.
Tests: Fake CLI fixtures cover missing capabilities, failed launch, lost launch response, stale terminal identity, busy steering, pause acknowledgment, cancellation with dirty work, restart with a pending decision, no duplicate launch, and terminal idle without a result. Run a real Linux start, question, reply, and retained-worktree cleanup check before this unit is accepted. Do not remove the checkout during cleanup.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/harness tools/scripts/tests/harness`

### U4. Review, authorization, and deployment
Files: tools/scripts/harness/review.py, tools/scripts/tests/harness/test_review.py, tools/scripts/tests/harness/test_acceptance.py, tools/scripts/harness/README.md, tools/scripts/harness/deploy/flowseer-harness.service, docs/README.md
After: U3
Change: Produce outcome summaries, change scope, review verdicts, verification evidence, and residual risks for approval cards. Persist commit and land proposals with their exact operation and internal repository fingerprints. Deliver a one-use authorization to the coordinator only while fingerprints match. Require existing review, compound, and verifier checkpoints for land. Recheck after main integration and request a fresh decision if the resulting proposal changes. Serialize main updates and reconcile an interrupted commit or merge by recorded Git evidence instead of repeating it. Document outbound polling, BotFather private-topic enablement, local-main ownership, secret provisioning, backups, recovery, and manual first-run trust steps. Doctor checks getMe.has_topics_enabled and exercises the required native message APIs. Supply a service template without installing it or changing system policy during implementation.
Tests: Reject stale diff and main fingerprints, replayed approval, wrong-task callbacks, missing checkpoints, and failed verification. Recover after commit or merge succeeds but its acknowledgment is lost. Run the complete Linux Telegram acceptance exercise in Requirement 8, plus harness restart and transport-disconnect cases. Retain logs with credentials removed and name the installed Orca and agent versions.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/harness tools/scripts/tests/harness docs/README.md`

Waves: U1 U2 | U3 | U4

## Verification

Run focused unittest cases for each unit, then the diff-aware verifier over
all changed paths. Before accepting U3, execute the real Linux launcher
check. Inspect native topics, rich cards, streamed drafts, and Stop inside
Telegram on the intended mobile client.
Before completion, execute the Telegram exercise on the provisioned
server with the maintainer's bot. A mocked transport or a macOS terminal
does not satisfy either Linux acceptance check.

## Definition of done

- The direction is accepted and the implementation has passed independent review.
- Every changed path has a passing verifier receipt and docs describe recovery.
- The Linux Telegram exercise passes, including explicit commit and land decisions.
- Record the implementation with `plan record implemented` and its completed unit count and times.

## Open questions

- The Linux host, Orca build, agent installations, and bot identity have not
  been supplied. Provisioning must confirm them before live acceptance.
- Verify BotFather private-topic settings, rich-message support, streamed
  drafts, and generation-stop delivery on the intended bot and client. The
  [AI Agents guide](https://core.telegram.org/bots/features#ai-agents)
  documents these features but does not verify the provisioned environment.
- The installed local Orca is 1.4.206. Current upstream documentation is
  interface evidence, not a version guarantee. Probe the target's installed
  help and JSON replies before choosing its exact launch and interrupt flags.
- Native workflow questions and initial CLI trust prompts are not proven to
  use the bridge. The Linux launcher exercise must prove coordinator
  behavior. Unrecognized prompts block and notify instead of receiving a
  guessed terminal answer.
- Synchronizing the server and Mac repositories needs a separate policy
  decision. This plan operates only on the server's local repository.
