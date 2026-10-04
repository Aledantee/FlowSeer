---
title: Pool Account Plan and Capacity Slots - Plan
type: feat
date: 2026-10-04
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: mixed
---

# Pool Account Plan and Capacity Slots - Plan

> Implemented. 3 units, 2026-10-04T09:14Z to 2026-10-04T09:19Z.

## Goal

A pool row states which account plan the pool is signed in to, which models
that account serves where the CLI lists them, and how many lanes the pool
can hold. `delegate` reads the slot count from the row and stops computing
it from the used percent, so a small plan at 0% no longer reads as the same
headroom as a large plan at 50%. The means: `pool-usage.sh` reads the plan
from each pool's own source, looks its capacity up in a registry table that
`tune` maintains, and prints `slots` per window.

Stop condition: a pool source stops reporting the plan field named under
Decisions. The plan table then has no key to look up.

## Decisions

- Each pool row gains `plan`, read from the source that owns it. Why: the
  2026-10-02 `delegate` entry in `docs/agent-observations.md` records a wave
  that gave a signed-in `codex` pool two slots at 0% used and lost four
  lanes to `400 The 'gpt-6.1-sol' model is not supported when using Codex
  with a ChatGPT account`. The fields, probed on 2026-10-04:

  | Pool | Source and field | CLI version probed |
  | --- | --- | --- |
  | `claude` | `claude auth status`, `subscriptionType` | Claude Code 2.1.289 |
  | `codex` | `codex app-server`, `account/read`, `account.planType` | codex-cli 0.160.0 |
  | `zai` | `omp usage --provider zai --json`, `reports[].metadata.planType` | omp 18.4.3 |
  | `google` | none in `agy -p /quota --output-format json` | agy 1.2.16 |
  | `synthetic` | none in `GET https://api.synthetic.new/v2/quotas` | not applicable |

  `google` and `synthetic` rows carry `plan: null`.
- The plan is read on every run, also when Orca supplies the windows. Why:
  `orca account list --json` (Orca 1.4.216) carries `rateLimits` with
  `usedPercent` per window and no plan field for either pool.
- The multiplier lives in the registry as
  `pools.<pool>.plans: {<plan>: {capacity: <value>, excludes: [<model id>]}}`,
  written by `tune` with a source per number in `evidence.md`. Why: no
  source reports a multiplier, and `.claude/skills/tune/SKILL.md` already
  requires a source and a date for every registry number. Capacity is
  relative to that pool's standard paid plan, which is 1. It does not
  compare one pool with another. (decided by the user, 2026-10-04)
- `capacity` is a number that covers every window of the pool, or a map
  from window name to number in which an unnamed window is 1. Why:
  Anthropic's Max plan page says "Max 20x includes 20 times the Pro plan's
  per-session usage allowance" and does not say the weekly limit scales
  (<https://support.claude.com/en/articles/11049741-what-is-the-max-plan>,
  fetched 2026-10-04). One number would apply 20 to a weekly window no
  source supports.
- The plan table starts with these values:

  | Pool | Plan | `capacity` | Source |
  | --- | --- | --- | --- |
  | `claude` | `max` | `{session: 20}` | The account is Max 20x, stated by the user, 2026-10-04. The ratio and its session scope are from the Max plan page above. |
  | `codex` | `prolite` | `1` | Unverified. <https://learn.chatgpt.com/docs/pricing> (fetched 2026-10-04) names Pro at $100, $200, and $500, no "Pro Lite", and no ratio to Plus. |
  | `zai` | `lite` | `1` | <https://docs.z.ai/devpack/overview> (fetched 2026-10-04) lists Lite at 2,000 five-hour and 10,000 weekly credits, the lowest tier. `omp usage` reports the same two limits. |

  (decided by the user, 2026-10-04)
- A plan name the registry table does not list counts as capacity 1, and
  the row carries `plan_unlisted: true`. A row whose `plan` is null counts
  as capacity 1 and carries no `plan_unlisted` key. Why: a new or renamed
  plan must not stop routing, and the report has to name it so `tune` adds
  it. A pool with no plan field has nothing to add.
- `pool-usage.sh` computes and prints `slots` per window. Why: a prose rule
  is recomputed by hand before every wave, and the script has tests in
  `.claude/skills/delegate/scripts/test_pool_usage.py`. (decided by the
  user, 2026-10-04)
- The slot rule, per window, with `limit` the pool's `usable_below` (85
  when unset), `used` the window's percent, and `capacity` the value for
  that window:

  ```text
  used >= limit            -> 0
  otherwise                -> min(6, max(1, ceil(capacity * (100 - used) / 50)))
  ```

  Why: at capacity 1 it returns the current rule exactly (2 under 50%, 1
  from 50% to the limit, 0 at or over it, `.claude/skills/delegate/SKILL.md`
  Wave size). The limit stays a guard on the percent whatever the plan,
  since its purpose is to keep running lanes from pushing a window over. 6
  is the wave cap, so no pool reports more than a wave can use.
- The script reads the effective registry with the overlay
  `.claude/skills/drive/scripts/successor.sh` uses (lines 124 to 128):
  `~/.claude/models/registry.yaml`, then `.claude/models/registry.yaml`
  under the working directory, a project pool entry replacing the
  machine-wide one of the same name whole. The reader is a Python function
  inside the script's heredoc that resolves both paths itself, parses with
  the standard library, and returns nested flow maps as maps and numbers as
  numbers. Why: no skill script imports PyYAML, and a quota read must not
  fail on a missing package. `successor.sh`'s parser is the precedent for
  the overlay only: it returns a nested map as a raw string, returns every
  value as a string, and exits 1 without a registry. `test_pool_usage.py`
  loads the heredoc and strips top-level calls, so a reader that takes its
  paths from the shell cannot be tested. With no registry readable every
  named plan is unlisted.
- The `codex` row gains `models`, read on the same app-server connection.
  The request is `{"id": N, "method": "model/list", "params": {}}`. The
  reply's `result` holds `data`, a list of objects whose `id` is the model
  id, and `nextCursor`. On 2026-10-04 (codex-cli 0.160.0, plan `prolite`)
  `data` held `gpt-6.1-sol`, `gpt-6-astra`, `gpt-6-sol`, `gpt-6-luna`,
  `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, and `gpt-5.5`, with
  `nextCursor: null`. A request without `params` answers
  `-32600 Invalid request: missing field params`. The reader follows a
  non-null `nextCursor` by sending it back as `params.cursor`, which is
  unverified since the probe saw one page. A failed request, a reply of any
  other shape, or a reply with no ids leaves `models` out of the row, so an
  unreadable list never drops every model. (decided by the user,
  2026-10-04)
- `delegate` step 1 drops a model that a row carrying `models` does not
  list, or that the registry's entry for the row's plan `excludes`. A CLI
  error that names a model as unsupported marks that model out on that pool
  for the session, and the report says to add it to `excludes` through
  `tune`. Why: the live list is unverified for the plan that failed (Open
  questions), so the registry list and the reactive rule cover what it may
  miss. (decided by the user, 2026-10-04)
- `tune` writes a pool's `plans` into every registry file that defines that
  pool's entry. Why: a project entry replaces the machine-wide one whole,
  and this repository's project registry defines all five pools, so a plan
  added to the machine-wide file alone would stay unlisted here.
- `discover-host.sh` does not change. Why: it prints the rows
  `pool-usage.sh` emits, so `host.yaml` gains the fields with no edit.

## Requirements

1. Every row carries `plan`. Example: `claude auth status` printing
   `{"loggedIn": true, "authMethod": "claude.ai", "subscriptionType": "max"}`
   with Orca windows present yields a `claude` row with `"plan": "max"` and
   `"source": "orca"`.
2. A source that omits the field yields `plan: null` and changes nothing
   else in the row. Example: a Codex `account/read` reply of
   `{"account": {"type": "chatgpt"}}` yields `"signed_in": true, "plan": null`.
3. The `codex` row carries `models` when `model/list` returns ids and omits
   the key otherwise. Examples: a `result` of
   `{"data": [{"id": "gpt-6-sol"}, {"id": "gpt-6-luna"}], "nextCursor": null}`
   yields `"models": ["gpt-6-sol", "gpt-6-luna"]`. A `result` of
   `{"data": [], "nextCursor": null}`, a `result` of `{"account": {}}`, and
   a request that times out each yield a row with its windows and no
   `models` key.
4. Every row carries `capacity` from the effective registry. Examples:
   `codex: {cli: codex, plans: {prolite: {capacity: 5}}}` and plan
   `prolite` yield `"capacity": 5`. Plan `plus` against the same entry
   yields `"capacity": 1, "plan_unlisted": true`. `plan: null` yields
   `"capacity": 1` and no `plan_unlisted` key.
5. Every row with windows carries `slots` per window by the rule under
   Decisions, and a row with `windows: null` carries `slots: null`.
   Examples at limit 85:

   | capacity | used | slots |
   | --- | --- | --- |
   | 1 | 0 | 2 |
   | 1 | 49 | 2 |
   | 1 | 50 | 1 |
   | 1 | 84 | 1 |
   | 1 | 85 | 0 |
   | 5 | 80 | 2 |
   | 20 | 50 | 6 |
   | 0.2 | 0 | 1 |

   At `usable_below: 95`, capacity 1 and used 90 yield 1.
6. A capacity map applies per window. Example: `capacity: {session: 20}`
   with windows `{"session": 60, "weekly": 60, "fableWeekly": 5}` at limit
   95 yields `"slots": {"session": 6, "weekly": 1, "fableWeekly": 2}`.
7. A project registry pool entry replaces the machine-wide one whole.
   Example: machine-wide
   `claude: {usable_below: 95, plans: {max: {capacity: 5}}}` and project
   `claude: {cli: claude}` yield, for plan `max`,
   `"capacity": 1, "plan_unlisted": true` and limit 85.
8. `delegate` takes a lane's slots as the lowest `slots` value among the
   windows that apply to the lane's model, and 1 for a signed-in row with
   `slots: null`. Example: a `claude` row with
   `"slots": {"session": 6, "weekly": 2, "fableWeekly": 6}` holds 2 lanes
   of `claude-opus-5-5`.
9. `delegate` step 1 drops a model absent from a row that carries `models`,
   or named in the plan's `excludes`. Example: a `codex` row with
   `"models": ["gpt-6-luna"]` drops `gpt-6.1-sol` before any lane starts. A
   `google` row, which has no `models` key, drops nothing by this rule.
10. `.claude/models/registry.yaml` holds the plan table under Decisions,
    and `.claude/models/evidence.md` holds each row's source and date, or
    the word "unverified". Example: the live run under Verification prints
    `"plan": "max", "capacity": {"session": 20}` on `claude` and no
    `plan_unlisted` on `claude`, `codex`, or `zai`.

## Out of scope

- Comparing capacity across pools, or reordering a role's `fit` list by
  capacity. `fit` order still decides which model a lane takes.
- Absolute limits. `zai` and `synthetic` report credits and requests, and
  the rows keep reporting percent.
- Raising the wave cap of six.
- `~/.claude/models/registry.yaml`. The project registry defines every
  pool, so it is the effective one here, and the next `tune` run writes the
  machine-wide file.
- `.claude/skills/drive/SKILL.md` saying "four rows" where `pool-usage.sh`
  prints five. It was stale before this change.
- Input trust: `pool-usage.sh` reads the replies of CLIs the user installed
  and signed in to, and two registry files the user or `tune` wrote. Both
  authors are trusted. A malformed reply or registry line degrades to an
  `error` or an unlisted plan and is not treated as hostile.

## Units

### U1. Pool rows carry plan, models, capacity, and slots

Files: `.claude/skills/delegate/scripts/pool-usage.sh`,
`.claude/skills/delegate/scripts/test_pool_usage.py`
After: none
Change: `emit` takes the plan and the model list as keyword parameters
after `error`, so its positional callers keep working, and adds `plan`,
`capacity`, `plan_unlisted` when true, `models` when known, and `slots` to
the row. A registry reader function returns each pool's `usable_below` and
`plans` from the effective registry. `orca_pools` reads the Claude plan
from `claude auth status` and the Codex plan and model list from the
app-server whether or not Orca supplied the windows, through readers
separate from `claude_pool` and `codex_pool`, and still takes the windows
from Orca when it has them. `zai_pool` reads `metadata.planType`.
`google_pool` and `synthetic_pool` pass no plan. The app-server request
deadline, 30 seconds today, becomes a parameter so a test can time out
without waiting for it. The header comment names the new fields and the
registry files read. The existing `worst` field is unchanged.
Tests: in `test_pool_usage.py`, one case per Requirement 1 to 7 with the
inputs given there. The slot table of Requirement 5 is a table-driven case
that includes the `usable_below: 95` row. The `model/list` fixture is the
reply shape under Decisions, and the fake app-server answers an unknown
method with an error and no longer repeats the previous result, since that
repeat would hand `{"account": ...}` to the model reader in the existing
Codex cases. Further cases: no readable registry yields capacity 1 with
`plan_unlisted: true` for a named plan, and a registry pool entry with a
trailing comment and a nested `plans` map parses to numbers. The existing
cases keep passing with the added fields. Nothing in this unit shows that
a real free-plan account reports a plan name or a shorter model list,
since no such account is available. The cursor request is covered by a
two-page fixture written from the assumed shape, which cannot show that
shape is right.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate/scripts/pool-usage.sh .claude/skills/delegate/scripts/test_pool_usage.py`

### U2. Registry plan table and the tune step that fills it

Files: `.claude/models/registry.yaml`, `.claude/models/evidence.md`,
`.claude/skills/tune/SKILL.md`
After: none
Change: the project registry gains the `plans` table under Decisions on
`claude`, `codex`, and `zai`, each on the pool's existing one-line entry,
and its header comment describes the `plans` key and the two forms of
`capacity`. `evidence.md` gains the three sources with their dates, the
`prolite` row marked "unverified". `as_of` is not changed, since no
catalogue was refreshed. In `tune`, step 1 says that after discovery it
compares each row's `plan` with the pool's `plans` table and, for a row
with `plan_unlisted`, fetches the vendor's plan page in that run, adds the
plan with its `capacity`, and records the URL and date in `evidence.md`.
For a plan name that covers more than one tier, it asks the user which
tier the account holds and records the answer as stated by the user with
the date. `excludes` is filled only from a quoted CLI error or a vendor
page. The step writes `plans` into every registry file that defines the
pool's entry, and the overlay paragraph says so beside its rule that a
project entry replaces the machine-wide one. The opening rule that a
number without a source does not go in gains its one exception: a plan
whose page names no ratio is listed at capacity 1 with "unverified" in
`evidence.md`, which routes as an unlisted plan does and stops the row
reporting it.
Tests: none are executable for a registry edit. The check is the live run
under Verification against Requirement 10.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/models/registry.yaml .claude/models/evidence.md .claude/skills/tune/SKILL.md`

### U3. delegate reads slots and drops unserved models

Files: `.claude/skills/delegate/SKILL.md`,
`.claude/skills/delegate/references/pool-rows.md`,
`docs/agent-steering.md`, `docs/agent-observations.md`
After: U1, U2
Change: step 1 of "Pick the role, then resolve the lane" also drops a model
that a row carrying `models` does not list or that the plan's `excludes`
names. Wave size states that a pool's slots are the lowest `slots` value
among the windows that apply to the lane, 1 for a signed-in row with
`slots: null`, and 0 for a signed-out one. It keeps the sentence defining
a pool's limit as its registry `usable_below`, 85 when unset, which step 1,
Dispatch by quota, and `pool-rows.md` refer to, and drops the percent
thresholds. Dispatch by quota says a CLI error naming a model as
unsupported marks that model out on that pool for the session, and that
the report names it and says to add it to `excludes` through `tune`. Its
load trigger for `references/pool-rows.md` gains "a row with
`plan_unlisted`". `pool-rows.md` documents `plan`, `capacity`,
`plan_unlisted`, `models`, and `slots`, holds the slot rule and the
plan-source table, and says the plan is read natively even on an Orca row.
`docs/agent-steering.md` states the two-lane rule in two passages of
"`delegate` and `tune`": the one that sizes a wave from measured headroom
("a pool holds two lanes under 50% used") and the one that explains the
85% threshold by "the two lanes a pool may hold under 50%". Both are
rewritten to the capacity rule and its reason, since a pool can now hold
six lanes below its limit. The 2026-10-02 `delegate` entry is deleted from
`docs/agent-observations.md`. The slot rule lives in the reference. The
`SKILL.md` body grew by two lines, since the model drop and the
unsupported-model rule are new text and only the percent thresholds left.
Tests: run every command the edited text embeds once, verbatim, from a
fresh shell, and run `check-prose.py` through the verifier.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/delegate/SKILL.md .claude/skills/delegate/references/pool-rows.md docs/agent-steering.md docs/agent-observations.md`

Waves: U1 U2 | U3

## Verification

```bash
python3 -m unittest discover -s .claude/skills/delegate/scripts -p 'test_*.py'
.claude/skills/delegate/scripts/pool-usage.sh    # unsandboxed
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

The live run prints five rows. `claude`, `codex`, and `zai` carry a plan
name, `codex` carries `models`, and every row with windows carries `slots`.
Edits under `.claude/skills/` need the sandbox disabled, since it denies
writes under `.agents/skills/`.

## Definition of done

- [x] The verifier is green for every changed path.
- [x] `pool-rows.md`, the `tune` skill, and `docs/agent-steering.md` describe
      the new row fields in the same change.
- [x] The observation entry is deleted.
- [x] This plan's `status` is set, with an outcome note under its title.
- [x] No plan labels in code.

## Open questions

- Unverified: whether a free ChatGPT account's `model/list` omits the
  models Codex then rejects, and what `planType` it reports. The account
  that failed is on `prolite` now. The `excludes` list and the reactive
  rule cover the gap.
- Unverified: which Pro price `prolite` is and its ratio to Plus. It
  routes at capacity 1 until a page or a measured window says.
- Unverified: whether Claude's weekly limit scales with the Max tier. The
  weekly and Fable windows stay at capacity 1.
- Unverified: whether any Claude Code source separates the two Max tiers
  without reading the credential store, which the reader does not touch.
  `tune` asks the user until one is found.
- Unverified: that an Orca row's windows and the CLI's own sign-in name the
  same account. Orca's `usageMetadata` reports `authProvenance: system` for
  `claude`, which suggests the system sign-in, and nothing in the tree
  confirms it for `codex`. If they differ, capacity multiplies another
  account's window.
