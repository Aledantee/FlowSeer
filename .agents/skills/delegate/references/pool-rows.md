# Reading pool rows

Load this when reading a `google`, `synthetic`, `zai`, or Fable row from
`scripts/pool-usage.sh`, a window at 0%, a row whose source failed, or when
the `claude` pool is past its limit (`usable_below`, `SKILL.md` Wave size).

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
- A row with `windows: null` and an `error` means usage is unknown. Report
  the error. Only `signed_in: true` permits dispatch with unknown headroom.
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
