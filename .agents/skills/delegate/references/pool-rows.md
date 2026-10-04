# Reading pool rows

Load this when reading a `google`, `synthetic`, `zai`, or Fable row from
`scripts/pool-usage.sh`, a window at 0%, a row whose source failed, a row
with `plan_unlisted`, or when the `claude` pool is past its limit
(`usable_below`, `SKILL.md` Wave size).

`pool-usage.sh` reads each pool from the source that owns its numbers and
prints one row per pool with `signed_in`, the used percent of every window,
and the `worst` one with its reset time:

| Pool | Source | Windows |
| --- | --- | --- |
| `claude`, `codex` | Orca `rateLimits`, with native fallback below | `session`, `weekly`, model scopes |
| `google` | `agy -p /quota --output-format json`, answered without a model turn | `gemini-5h`, `gemini-weekly`, `3p-5h`, `3p-weekly` |
| `synthetic` | `GET https://api.synthetic.new/v2/quotas` with the key from `~/.local/share/opencode/auth.json`; the call is not counted | `5h`, `week` |
| `zai` | `omp usage --provider zai --json`, which reads omp's own Z.ai credential | `5h`, `week` |

- `orca account list` also carries an `antigravity` row with
  `status: unavailable`. It means Orca cannot read that usage (no Gemini CLI
  sign-in), nothing about the pool. Never drop `google` on it: a pool is out
  only when its own `pool-usage.sh` row shows `signed_in: false` or a window
  over the threshold.
- A failed read leaves an `error`. Report it. Windows that were read stay
  in the row. `windows: null` means none were read. A row with `plan: null`
  beside an `error` has an unknown plan and counts capacity 1.
  Only `signed_in: true` permits dispatch with unknown headroom.
  A false or null sign-in state excludes the pool, as `SKILL.md` specifies.
- On `google`, the `gemini-*` windows meter Gemini models and the `3p-*`
  windows meter Claude and GPT models run through `agy`; only the group of
  the lane's model counts.
- `synthetic` meters a rolling five-hour request limit and a weekly credit
  limit, counted by Synthetic, so usage from another host is included. Both
  refill in ticks instead of resetting, so its row carries no `resets`; the
  tick interval is unmeasured.
- `zai` meters Z.ai plan credits over a five-hour and a weekly window, read
  through omp, which holds the credential. It serves `execute-sensitive`
  models through their `zai_pool_id` and `glm-5.3` through its `pool_id`.
- The coordinator and every native subagent draw on the `claude` pool, a
  Fable session also on `fableWeekly`. Past that pool's limit, step 1 drops
  the Claude models, so every lane resolves to a model on another prepaid
  pool and runs on that pool's CLI, as "Orca or native" in `SKILL.md` says.
- A window at 0% may have just rolled over; `resets` in the same row says
  whether it did.

## Plan, capacity, and slots

Every row also carries the account plan and the lanes it leaves room for:

| Field | Meaning |
| --- | --- |
| `plan` | The plan name the pool's own source reports, `null` where it reports none. |
| `capacity` | The plan's entry in the effective registry, `pools.<pool>.plans.<plan>.capacity`. A number covers every window. A map names windows, and an unnamed window is 1. It is relative to that pool's standard paid plan and does not compare pools. |
| `plan_unlisted` | `true` when the registry does not list the plan or lists it without a usable `capacity`. The row then counts capacity 1. Name the plan in the report so `tune` adds it. A `null` plan never sets it. |
| `models` | On `codex`, the model ids the account serves. The key is absent when the list could not be read, so an unreadable list drops no model. |
| `slots` | Lanes per window, `null` when `windows` is `null`. |

The slot rule, per window, with `limit` the pool's `usable_below` (85 when
unset), `used` the window's percent, and `capacity` the value for that
window:

```text
used >= limit            -> 0
otherwise                -> min(6, max(1, ceil(capacity * (100 - used) / 50)))
```

At capacity 1 that is 2 lanes under 50%, 1 from 50% to the limit, and 0 at
or over it. `capacity: {session: 20}` with `session` and `weekly` both at
60% and a limit of 95 gives 6 and 1, and a lane takes the lower one. The
limit guards the percent whatever the plan, so running lanes do not push a
window over. 6 is the wave cap.

The plan is read from the pool's own source on every run, also when Orca
supplied the windows, since `orca account list --json` carries no plan
field:

| Pool | Source and field | CLI version probed |
| --- | --- | --- |
| `claude` | `claude auth status`, `subscriptionType` | Claude Code 2.1.289 |
| `codex` | `codex app-server`, `account/read`, `account.planType` | codex-cli 0.160.0 |
| `zai` | `omp usage --provider zai --json`, `reports[].metadata.planType` | omp 18.4.3 |
| `google` | none in `agy -p /quota --output-format json` | agy 1.2.16 |
| `synthetic` | none in `GET https://api.synthetic.new/v2/quotas` | not applicable |

`codex` reads `models` on the same app-server connection with
`model/list`, following `nextCursor` as `params.cursor`. The cursor request
is unverified, since codex-cli 0.160.0 returned one page. Whether a free ChatGPT
account's list omits the models Codex then rejects is unverified too, so
the registry's `excludes` and the unsupported-model rule in `SKILL.md`,
Dispatch by quota, cover what the list may miss.

The registry is `~/.claude/models/registry.yaml` with
`.claude/models/registry.yaml` under the working directory laid over it. A
project pool entry replaces the machine-wide one whole. With neither
readable, every named plan is unlisted.

## Native fallback for Claude and Codex

Missing Orca (`orca not installed`), an unreachable runtime, an unreadable
account list, or a pool without usable Orca windows selects that pool's native
reader. Usable Orca windows remain authoritative. The `source` field names the
reader that answered, so a missing Orca installation does not itself leave an
error on a successful native row.

Claude sign-in comes from `claude auth status`. Subscription usage comes from
this local command, checked with Claude Code 2.1.287:

```bash
claude -p /usage --output-format stream-json --verbose --no-session-persistence --tools ''
```

The stream's `usage_report.rate_limits.limits` rows carry `kind`, `percent`,
and `resets_at`. The reader maps session and all-model weekly limits to
`session` and `weekly`, and keeps model scopes separate. It requires a local
`usage` result with zero model turns. The command runs in a temporary directory
so repository hooks do not run a conformance suite for a quota read. The CLI
handles authentication and refresh. The reader never extracts its token.

Codex uses the [app-server account API](https://learn.chatgpt.com/docs/app-server#6-rate-limits-chatgpt),
checked with Codex CLI 0.142.3. After initialization, it reads the account and
quota without starting a thread or model turn. It prefers the `codex` bucket in
`rateLimitsByLimitId` and falls back to `rateLimits` when that bucket is absent.
Window duration names the quota: 300 minutes is `session`, 10080 is `weekly`.
A primary window can be weekly when the account has no session window. Reset
timestamps are seconds in this API, milliseconds in Orca's rows.

A native quota read that fails after sign-in succeeds leaves `signed_in: true`
and `windows: null` with an error. A failed sign-in query leaves sign-in unknown.
An API-key account is not a prepaid subscription pool. Parsing and fallback
regressions are checked in
[`test_pool_usage.py`](../scripts/test_pool_usage.py).
