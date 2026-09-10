# Herdr as the worker runtime: trial of 2026-09-10

Why this trial: the Orca sessions of 2026-09-05 to 2026-09-10 spent an
estimated 15 to 30% of coordinator effort on orchestration mechanics. Read
back from the transcripts, the recurring failures were, most frequent first:

1. The Bash sandbox blocking Orca's Unix socket, so `worker_done`,
   heartbeats, and `check` reported "Could not connect to the running Orca
   app"; rediscovered by hand in seven sessions.
2. Parallel reviewer subagents stalling ("no progress for 600s"), twice for
   all four at once, with the review then done by hand.
3. Codex workers blocked at startup by the hooks-review dialog.
4. Dispatch state errors (`dispatch_inactive`, `dispatch_capability_invalid`
   after a context compaction, `worker_identity_changed`), one of which left
   a finished worker unable to report at all.
5. `ask` and `check --wait` timing out below real latency, with the wait
   re-armed on every heartbeat.
6. The coordinator reading a stale card status instead of the queue.
7. `worker-start --model` pinning only Claude, Codex, and Cursor, so a wave
   routed to Gemini and OpenCode pools ran six times on Sonnet.

Items 1 to 3 are the sandbox, the Claude subagent runtime, and Codex
itself; a different manager inherits them. Items 4, 5, and 7 are Orca's,
and item 6 is half Orca's. Herdr 0.9.0 was tried against those three, with
the sandbox and Codex behaviours observed on the way.

## Setup

- `herdr integration install claude|codex|opencode` edits the global hook
  files of each agent (`~/.claude/settings.json`, `~/.codex/hooks.json`,
  `~/.config/opencode/plugins/`). The `agy` kind has no integration and is
  detected from the screen.
- The server was started headless from inside the coordinating Claude Code
  session by accident (`herdr server` with no subcommand runs it). Panes
  inherit that environment: the Claude worker reported "Transcript saving
  is off — inherited CLAUDE_CODE_CHILD_SESSION marker". Start the server
  from a plain terminal with the Claude variables unset.
- `herdr worktree create` refuses to run from a linked worktree
  (`linked_worktree_source`); with `--cwd <main checkout> --base <sha>` it
  branches from any commit, so a child still starts from the coordinator's
  `HEAD`.

## The wave

Four lanes from the same commit, one per pool, on the `tune` calibration
brief (`Merge` in `src/common/pump`). The brief predates the commit that
landed `Merge` (d4421211), so every lane found the work already done; the
trial measures the runtime, not the models. Worker names and launch lines:

| Lane | CLI | Launch line | Start | First prompt |
| --- | --- | --- | --- | --- |
| hc | claude | `--model claude-sonnet-5 --dangerously-skip-permissions` | idle in 4 s | working |
| hx | codex | `-a never --sandbox danger-full-access -m gpt-5.6-sol -c model_reasoning_effort=xhigh` | idle in 3 s | swallowed by the hooks dialog, reported `done` |
| hg | agy | `--model gemini-3.8-flash-high --dangerously-skip-permissions` | idle in 6 s | `agent_prompt_stalled`, input empty; second prompt worked |
| ho | opencode | `--agent execute-open` (Kimi K3 on OpenCode Go) | idle in 4 s | `blocked` on a directory-permission dialog; `enter` cleared it |

All four were `working` 90 seconds after the first `agent start`. Under Orca
the `agy` and `opencode` lanes are not dispatchable at all.

## Results

One `agent wait --until done --until idle --until blocked` per lane, started
after the prompts, returned once each with the right state; no wait was
re-armed, no state was read from a card, and no worker had to be settled
by hand. The report of every lane was readable with `agent read` after it
ended.

| Lane | Settled | Tree at the end | Hidden acceptance tests |
| --- | --- | --- | --- |
| hc, Sonnet 5 | done, 1722 s | rewrote `merge.go` and its test, commit 34560b24, clean | fail: `TestMergeAcceptContextCancelPropagates` leaks a goroutine |
| hx, GPT-5.6 Sol | done, 1795 s | found the work landed, ran the full verifier (failed on the known host quirks), reported d4421211, clean | not applicable, unchanged |
| hg, Gemini 3.8 Flash | done, 1716 s | rewrote `merge.go` (147 added, 95 removed), commit 1aeb1560, clean | pass, 3 runs under `-race` |
| ho, Kimi K3 | done, 1074 s | found the work landed, reported d4421211, clean; $0.56 on the Go window | not applicable, unchanged |

The two lanes that rewrote a landed implementation did so because the
brief says "add"; that is a brief defect, not a runtime one, and it cost
one regression (Sonnet's rewrite fails an acceptance test the landed code
passes). Gemini reported 2.76M input tokens after a compaction; the plan
covers it, but the number says a lane on that pool should get a brief that
names the files to read and nothing wider.

What the wave cost the coordinator: four `agent start` calls, four
prompts, three dialog answers (Codex hooks, OpenCode directory access, one
agy re-prompt), one wait each, one read each. No retries against a socket,
no dispatch ids, no capability tokens.

## Second wave, through the wrapper

The same four pools, this time started by `herdr-worker.sh` with the brief
submitted as the prompt itself (one bracketed-paste submission, no pointer
file) and the brief corrected to "verify, change only what fails". Every
lane read the brief intact, did the verification, reported "no change"
with the commit that holds the implementation, and left a clean tree.

| Lane | Settled | Notes |
| --- | --- | --- |
| Sonnet 5 | done, 2 min | nine test cases named in the report |
| Gemini 3.8 Flash | done, 2 min | 191K input tokens, no compaction |
| Kimi K3 | done, 4 min | $0.24; no directory-permission dialog, since nothing was outside the worktree |
| GPT-5.6 Sol | done, 5 min | showed its own update offer at startup; Herdr reported `blocked` and `agent start` returned `agent_not_ready`; the wrapper now answers it |

Two wrapper defects surfaced and were fixed in the same pass: `herdr
status server` is not a reliable liveness probe right after another CLI
call (two of four starts died on it while the server was up; an API call
is the probe now), and a Codex startup dialog other than the hooks review
ended the start. `stop` removed each workspace and checkout, and the
branches deleted cleanly.

## What Herdr answered

- Item 7, model pinning: closed. Every pool takes its pin on the launch
  line and `agent start` reports the argv it ran.
- Item 4, dispatch tokens: closed by construction. A worker is a pane and a
  branch; `agent read` returns the report after any coordinator restart.
- Item 5, waits: `agent wait --timeout` is the caller's own number, and it
  returns the settled state, not a heartbeat. One process per worker
  blocked for the whole run with no re-arming.
- Item 6, stale status: the state comes from the agent's own hooks, and
  `done` versus `idle` is tracked per client. Blocked is a real state.
- The sandbox: `herdr` uses a Unix socket like `orca`, so every call still
  runs unsandboxed. Nothing changes here.
- Codex hooks dialog: Herdr does not recognise it as `blocked`; it reads
  as `idle`, and a prompt sent into it is lost. The wrapper answers it
  before prompting.

## What it does not give

- Quota. Nothing reports a pool's windows; `orca account list --json` stays
  the gate.
- Orca's cards, comments, and mailbox. The coordinating session still
  updates the worktree comment when it runs under Orca.
- Durability across a server restart: every pane process dies with it.
- One maintainer, pre-1.0, and the detection for agents without an
  integration is heuristic.

## Decision

Herdr becomes the `execute` lane in the `delegate` skill when a server
runs, on every pool, through `.claude/skills/delegate/scripts/herdr-worker.sh`;
`references/herdr.md` there carries the setup and the pitfalls above. Orca
stays the workspace, the quota surface, the card, and the full-handoff
lane, and the `execute` lane when no Herdr server runs. The three
failures that travel with any manager keep their own fixes: the sandbox
socket rule stays a per-machine setting, Codex's hooks dialog is answered
by the wrapper, and reviewer subagents stay native.

Open after both waves: whether Herdr's `blocked` fires for Claude's own
permission prompts (the lanes ran with permissions off), and how a lane
behaves across a coordinator compaction in a real plan.
