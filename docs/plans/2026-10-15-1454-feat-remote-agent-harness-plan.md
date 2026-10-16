---
title: Remote Agent Harness - Plan
type: feat
date: 2026-10-15
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Remote Agent Harness - Plan

## Goal

Start and steer FlowSeer work from Telegram, answer design questions, and
authorize commits and local-main landing while agents run through Orca on
a dedicated Linux server. Telegram's native AI interface provides task
topics, rich decision cards, and streamed updates. A persistent host-side
harness connects it to task coordinators and the existing project workflow.

Stop if the target Linux Orca installation cannot launch and recover agent
terminals without a connected desktop. Phase 2 verifies that before it
integrates the bot with Orca.

## Decisions

- Follow `docs/architecture/2026-10-15-remote-agent-harness-direction.md`,
  now accepted (decided by the user, 2026-10-15). It constrains task
  coordination and the human decision boundary across workflows.
- Split the work into two phases (decided by the user, 2026-10-15). Phase 1
  builds the durable state, the decision bridge, and the Telegram transport
  against fakes, and needs no server. Phase 2 adds Orca coordination,
  approval, and deployment, and needs the provisioned Linux host and bot.
  Why: phase 1 can land now instead of sitting partially implemented until
  the host exists.
- A server configuration names the default coordinator agent CLI and model.
  The New Task card offers a per-task override chosen from configured
  alternatives, and the task record stores the pair it ran with (decided by
  the user, 2026-10-15). Why: predictable defaults, with a choice when a
  task needs a different model, and no dependency on the `tune` registry
  being present on the server.
- Telegram is the first control surface. It supports private chat, long
  polling through `getUpdates`, and callback buttons. Bot API 10.3
  (2026-08-24) is the version read for this plan, from the
  [Bot API](https://core.telegram.org/bots/api).
- Use Telegram's native AI agent features: private-chat topics, streamed
  drafts with a Stop control, and rich messages with tables, expandable
  details, and buttons. The [AI Agents guide](https://core.telegram.org/bots/features#ai-agents)
  documents the combination, and the Bot API defines the calls:
  - `createForumTopic` creates a topic in "a private chat with a user", and
    `getMe` returns `has_topics_enabled`, which BotFather sets.
  - `sendRichMessage` sends a rich message, and `editMessageText` edits
    "text, rich and game messages" through its `rich_message` parameter.
    The persistent status card is edited this way.
  - `sendRichMessageDraft` streams a draft that is "a temporary 30-second
    preview". `can_stop` shows the Stop button, and pressing it delivers a
    `stopped_message_generation` update whose `MessageGenerationStopped`
    carries `chat`, `message_thread_id`, and `draft_id`.
  - `RichMessageButton.callback_data` and `InlineKeyboardButton.callback_data`
    hold 1 to 64 bytes.
- Keep code diffs out of Telegram, including attachments and expandable
  sections. Approval uses outcome summaries, verification evidence, and
  risks. Internal fingerprints still bind it to the exact changes, and
  automated review continues to inspect code.
- Linux with Orca is the execution target. Orca's
  [CLI reference](https://www.onorca.dev/docs/cli/reference#runtime-commands)
  documents `orca serve` and `orca serve service install`, which writes a
  systemd user unit on Linux. The local Orca 1.4.221 help shows
  `orca terminal send --interrupt` and `--retry-request <id>`, an idempotent
  retry "bound to the prompt payload and exact terminal process
  incarnation". The target installation still needs a live check.
- Use a new `harness` command group under `tools/scripts/harness/` and a
  SQLite database (`sqlite3`, standard library) outside the checkout.
  `tools/scripts/run.py:collect_groups` discovers the group from the literal
  `COMMANDS` mapping in its `__init__.py`. No direct dependency or
  policy-surface change is planned.
- Give every task a coordinator with an isolated worktree. Delegated
  workers return blockers to it. The coordinator calls the structured
  decision bridge, rather than relying on terminal-text parsing to discover
  questions. Existing `drive` and `land` rules remain binding.
- Support the full workflow, including explicit commit and land decisions.
  Approval binds the operation, base and head commits, worktree diff digest,
  and task revision. A changed proposal needs another decision.
- Serialize landing on the server's local `main`. Work branches start from
  a recorded local commit. Preserve the repository's no-fetch, no-push rule.

## Requirements

1. Only the configured Telegram user and private chat can control tasks.
   An otherwise valid command from another account produces no task event
   and discloses no repository content. Reply, callback, topic, and draft
   identities must match the stored task and decision. A topic ID alone
   grants no authorization.
2. An overview rich message groups tasks by active, awaiting decision,
   blocked, and finished states. New Task requests a natural-language brief,
   offers the configured agent and model with override buttons, and creates
   a private-chat topic with a durable task ID. Messages in that topic
   address its task. Replaying the Telegram update after a crash returns the
   task instead of creating another task or topic.
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
   interface without typing a slash command. Restart the harness with a
   question pending and repeat a callback. Retain the worktree until the
   maintainer removes it.
9. Stream bounded progress through `sendRichMessageDraft` with `can_stop`
   and persist the completed response with `sendRichMessage`. Route a
   `stopped_message_generation` update by chat, topic, and draft ID to a
   cancellation request. Show requested until the owned worker acknowledges
   cancellation. A stale draft's Stop control cannot cancel another task.

## Out of scope

Diff displays, Mini Apps, browser dashboards, other chat services,
desktop-session migration, automatic repository synchronization, provider
remote products, and live-device mutation are outside this change. Server
purchasing and credential entry are provisioning steps, not actions
performed by a task worker.

Telegram updates are untrusted until the configured sender and private chat
are checked. Escape worker text before placing it in rich-message blocks.
Worker output is data, not formatting instructions or authorization.
Agents and processes under the configured server account are trusted. This
change handles malformed bridge data but does not contain a hostile agent
that already has the account's filesystem and process capabilities.

## Units

### U1. Phase 1, state, bridge, and Telegram transport
Files: docs/plans/2026-10-15-1454-feat-remote-agent-harness-phase1-plan.md
After: none
Change: The durable task and decision store, `harness bridge`, and the Telegram transport and renderers, tested against fixtures. Requirements 1, 4, 7, and 9 at the transport and store level, and the store side of 2.
Tests: Named in the phase plan.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/harness tools/scripts/tests/harness`

### U2. Phase 2, Orca coordination, approval, and deployment
Files: docs/plans/2026-10-15-1454-feat-remote-agent-harness-phase2-plan.md
After: U1
Change: `harness serve` and `harness doctor`, coordinator launch and recovery through Orca, commit and land authorization, and the service template. Requirements 3, 5, 6, and 8, topic creation for 2, and the end-to-end parts of 9.
Tests: Named in the phase plan, including the live Linux exercise.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/harness tools/scripts/tests/harness docs/README.md`

Waves: U1 | U2

## Definition of done

- Both phases are implemented, reviewed, compounded, and landed.
- The Linux Telegram exercise in Requirement 8 passes, including explicit
  commit and land decisions.

## Open questions

- The Linux host, Orca build, agent installations, and bot identity have not
  been supplied. Phase 2 cannot be re-planned to ready until they are.
- Synchronizing the server and Mac repositories needs a separate policy
  decision. This plan operates only on the server's local repository.
