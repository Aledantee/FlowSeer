---
title: Remote Agent Harness, Phase 1, State, Bridge, and Telegram Transport - Plan
type: feat
date: 2026-10-15
artifact_contract: flowseer-plan/v2
execution: code
---

# Remote Agent Harness, Phase 1, State, Bridge, and Telegram Transport - Plan

## Goal

A `harness` command group holds tasks and decisions in a durable store,
answers `harness bridge ask` with exactly one stored answer per decision,
and speaks Telegram's rich-message, topic, draft, and callback API to the
one configured user. Everything is proven against fixtures and needs no
server, Orca runtime, or live bot. The means are a SQLite store with
transition rules, a file-based bridge command, and a standard-library Bot
API client with renderers that accept summary data only. Stop condition: if
`sqlite3` in the `run.py` interpreter cannot give one accepted answer under
concurrent writers (`BEGIN IMMEDIATE` with a busy timeout), the store
design is wrong.

## Decisions

The parent's Decisions apply: `docs/plans/2026-10-15-1454-feat-remote-agent-harness-plan.md`.
These are local to this phase.

- Configuration is a TOML file outside the checkout, read with `tomllib`,
  at the path in `FLOWSEER_HARNESS_CONFIG` or `--config`. It names the
  state directory, the bot token file, the allowed numeric user ID and
  private chat ID, the default coordinator agent and model, and the
  override alternatives. Why: the standard library reads it, and secrets
  stay in a separate file the configuration only points to.
- The loader refuses a token file readable by group or others. Why: the
  token grants full control of the bot, and the direction keeps
  credentials on the host.
- Every write runs in a `BEGIN IMMEDIATE` transaction, and uniqueness
  constraints enforce one inbound row per `update_id`, one answer per
  decision, and one open operation per task. An operation is a row in
  `operations` whose state is `intended` or `running`. Phase 2 records
  launches, commits, and lands there. Why: the database, not process
  ordering, makes replays and double answers harmless across restarts.
- The transport normalizes every update and authorizes none. The store
  writes each one as an inbound row, accepted or rejected, and the
  `getUpdates` offset advances only after that commit. The Bot API confirms
  an update "as soon as getUpdates is called with an offset higher than its
  update_id", so a crash before the commit leaves it to be fetched again.
- A message or callback is authorized by `from.id` and `chat.id`. A stop
  update is authorized by `chat.id` and a stored draft mapping, since
  `MessageGenerationStopped` carries `chat`, `message_thread_id`, and
  `draft_id` and no sender.
- Callback data is an opaque random ID of at most 32 bytes, issued and
  looked up in the store. Why: the field holds 1 to 64 bytes, and an
  opaque ID carries no task or decision identity a forger could edit.
- Renderers take dataclasses of summary fields. None of them has a field
  for patch or diff text. Why: parent Requirement 5 then holds by
  construction, and a test can assert the field set.
- The transport reads `getWebhookInfo` and refuses to poll when a webhook
  URL is set, instead of deleting it. The Bot API says getUpdates "will not
  work if an outgoing webhook is set up", and removing someone's webhook is
  an outward-facing change.
- Bot API request URLs contain the token (`https://api.telegram.org/bot<token>/<method>`).
  The client therefore never puts a URL or an `HTTPError` into a message,
  log line, or re-raised exception, and reports the method name instead.

## Requirements

Parent Requirements 1, 4, 7, and 9 at the level of the store, the bridge,
and the transport, and the store side of Requirement 2. Calling
`createForumTopic` for a new task, and its crash recovery, are phase 2.

1. An update from a sender other than the configured user, or from any chat
   other than the configured private chat, is stored as rejected and yields
   no task event. Example: a `message` update with `from.id` 42 when the
   configured ID is 7 produces a rejected inbound row, no row in `events`,
   and no outbound message.
2. Storing the same `update_id` twice returns the first result. Example: a
   New Task update replayed after a simulated crash returns the existing
   task ID and stored topic ID, and the store holds one task.
3. `harness bridge ask --task T --request q.json` waits for the answer and
   then prints one JSON object
   `{"decision": ..., "revision": ..., "option": ..., "text": ...}` and
   exits 0. `option` is null for a Custom Answer and `text` is null for an
   option press. It exits 3 when the decision is cancelled and 4 when the
   optional `--timeout` passes. It exits 2 on a malformed or oversized
   request and stores nothing. Example: a request with no options, or over
   64 KiB, exits 2.
4. Answering a decision a second time, answering it from another task's
   topic, answering an obsolete revision, or pressing a callback ID the
   store never issued changes nothing and reports which rule refused it.
   Example: two threads answer one decision at once, and exactly one answer
   row exists afterwards.
5. A `stopped_message_generation` update whose chat, `message_thread_id`,
   and `draft_id` match a stored draft records a cancellation request for
   that draft's task. Example: a stop for draft 9 in topic 3 when draft 9
   belongs to topic 4 records nothing.
6. The New Task card names the configured default agent and model and one
   button per configured alternative. Creating a task from a pressed option
   stores the pair. Example: a press on the alternative `codex/gpt-5.5`
   stores agent `codex` and model `gpt-5.5`.
7. The outbound queue accepts a progress notification only when its content
   digest differs from the last one queued for that task. Example: two
   identical progress payloads for task T yield one queued notification.

## Out of scope

Launching agents, `harness serve`, creating topics for new tasks, approval
of commits and landing, and the service template are phase 2. The bridge
reads its request file from a trusted coordinator on the same host. It
rejects malformed data and does not defend against a hostile process under
the server account. Telegram updates are untrusted until the store checks
the sender and chat.

## Units

### U1. Configuration, durable store, and decision bridge
Files: tools/scripts/harness/__init__.py, tools/scripts/harness/README.md, tools/scripts/harness/config.py, tools/scripts/harness/state.py, tools/scripts/harness/bridge.py, tools/scripts/tests/harness/__init__.py, tools/scripts/tests/harness/test_config.py, tools/scripts/tests/harness/test_state.py, tools/scripts/tests/harness/test_bridge.py
After: none
Change: `COMMANDS = {"bridge": "bridge"}` registers `harness bridge`, whose argparse subcommands are `ask`, `progress`, `result`, and `ack`. `ask` polls the store once a second. `progress`, `result`, and `ack` validate their payload, store an event, and return at once. `config.py` loads and validates the TOML file. `state.py` creates a versioned schema with tables for tasks (with agent and model), topics, drafts, messages (chat, thread, and message ID to task and decision), callbacks (opaque ID to task, decision, revision, and option), inbound updates, events, operations, decisions, answers, and an outbound notification queue keyed by an idempotency key and a content digest. Its functions are the only writers and enforce the transition rules. A result event names artifact paths and evidence and sets no plan checkpoint. `README.md` documents the configuration, the state directory, the bridge exit codes, and the payload versions.
Tests: `test_config.py`: a missing field, a token file with mode 0644, and a valid file. `test_state.py`: a rejected sender, a replayed update returning the stored task and topic, a double answer, a cross-task answer, an obsolete revision, an unissued callback ID, two threads answering at once with one accepted, a second open operation for one task refused, a transaction killed before commit leaving no partial row, a duplicate notification key, a repeated progress digest, a matching and a mismatched stop, and a task created from a stored agent and model option. `test_bridge.py`: no options, an oversized file, an unknown payload version, an option answer and a Custom Answer on standard output, cancellation exiting 3, and timeout exiting 4.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/harness tools/scripts/tests/harness`

### U2. Telegram transport and rich renderers
Files: tools/scripts/harness/telegram.py, tools/scripts/harness/render.py, tools/scripts/tests/harness/test_telegram.py, tools/scripts/tests/harness/test_render.py, tools/scripts/tests/harness/testdata/telegram/
After: U1
Change: `telegram.py` calls the Bot API over `urllib.request` with an injectable opener: `getMe`, `getWebhookInfo`, `getUpdates`, `createForumTopic`, `sendRichMessage`, `editMessageText` with `rich_message`, `sendRichMessageDraft` with `can_stop`, and `answerCallbackQuery`. It normalizes every update into a typed record (message, callback, stop, other) carrying chat, topic, draft, and sender where present, honors `retry_after`, and reports failures by method name. `render.py` builds `InputRichMessage` JSON for the overview table, the task status card, expandable activity, the New Task card with agent and model choices, and decision cards with alternatives and a Custom Answer button, using callback IDs issued by the store. Worker text is escaped into plain `RichText`.
Tests: `test_telegram.py` replays JSON fixtures copied from the Bot API object definitions: a private message, a group message, a topic message, a callback, a custom-answer reply, a stop update with no sender, a set webhook, a timeout, an API error, and `retry_after`. It asserts that no exception message, `repr`, or log record contains the token. `test_render.py` asserts escaped worker text, card size bounds, callback IDs of at most 32 bytes and callback data of at most 64, and that no renderer input dataclass has a diff or patch field.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/harness/telegram.py tools/scripts/harness/render.py tools/scripts/tests/harness/test_telegram.py tools/scripts/tests/harness/test_render.py tools/scripts/tests/harness/testdata`

Waves: U1 | U2

## Verification

```bash
uv run tools/scripts/run.py test
uv run tools/scripts/run.py verify change -- tools/scripts/harness tools/scripts/tests/harness
```

The fixtures are authored from the Bot API page, the same document the
transport reads, so they cannot show a misread of it. Phase 2's live
acceptance on the intended client is the second source.

## Definition of done

- The verifier is green for every changed path.
- `tools/scripts/harness/README.md` describes configuration, state, and the bridge.
- This plan's outcome is recorded with `uv run tools/scripts/run.py plan record implemented <plan> --units 2 --from <t> --to <t>`.
- No plan labels appear in code.

## Open questions

- Whether `sendRichMessageDraft` and private-chat topics behave as
  documented on the maintainer's client is unverified until phase 2's live
  check.

## Review gaps

Follow-ups left open by the review. None holds the verdict.

- `tools/scripts/harness/render.py:81`: `_plain` truncates by code points, and `json.dumps` writes each C0 control character as 6 bytes; fails: eight activity entries of 512 escape characters make `_card` raise instead of truncating; class: hardening
- `tools/scripts/harness/telegram.py:30`: `normalize` indexes nested fields without type checks and runs for the whole batch at line 113; fails: one update with `"from": null`, or a `result` that is not a list, raises before any ingest and the offset never advances; class: hardening
- `tools/scripts/harness/telegram.py:87`: `error.close()` runs inside the `HTTPError` handler; fails: a raising `close()` chains the `HTTPError`, whose URL holds the token, as `__context__`; class: hardening
- `tools/scripts/harness/state.py:19`: `mkdir(mode=0o700, exist_ok=True)` leaves an existing state directory's mode alone, and line 28 chmods the database only after the first transaction; fails: a state directory created 0755 stays 0755; class: hardening
- `tools/scripts/harness/state.py:253`: inbound rows are keyed by `update_id`, and the Bot API says "If there are no new updates for at least a week, then identifier of the next update will be chosen randomly instead of sequentially"; fails: a random ID equal to a stored one returns the old result, and one below the in-memory `Poller.offset` may be confirmed unseen; needs validation of where the random ID can fall, at phase 2's live check; class: hardening
- `tools/scripts/harness/bridge.py:73`: `json.loads` on a nested request under 64 KiB raises `RecursionError`, which the bridge does not catch; fails: exit 1 with a traceback instead of exit 2; class: hardening
- `tools/scripts/harness/state.py:27`: the callbacks and inbound table shapes changed while `user_version` stays 1; fails: a database created before the change opens and then raises on `card_id`; class: convention
- `tools/scripts/harness/state.py:249`: every `IntegrityError` in `_apply` becomes a stored `state_conflict` with no log line or counter; fails: a store defect consumes the owner's answer silently; class: hardening
- `tools/scripts/harness/state.py:158`: `issue_agent_options(brief: str)` accepts `None` and issues a card with no brief; fails: every button of that card returns `missing_brief`; class: convention
- `tools/scripts/harness/state.py:253`: `encode(update)` raises on a NaN or a lone surrogate that `json.load` accepted from the Bot API; fails: `ingest` raises and the poller refetches the update; class: hardening
- `tools/scripts/harness/state.py:279`: the `new_task` kind has no producer, since `normalize` emits only message, callback, stop, and other, and `Poller.poll` at `telegram.py:147` returns store results without `callback_query_id` or message text; fails: a caller cannot answer a callback query or read a brief to issue New Task buttons; class: convention
