# Reading pool rows

Load this when reading a `google`, `synthetic`, or Fable row from
`scripts/pool-usage.sh`, a window at 0%, a row whose source failed, or when
the `claude` pool is past 85%.

`pool-usage.sh` reads each pool from the source that owns its numbers and
prints one row per pool with `signed_in`, the used percent of every window,
and the `worst` one with its reset time:

| Pool | Source | Windows |
| --- | --- | --- |
| `claude`, `codex` | `orca account list --json`, `rateLimits` | `session`, `weekly`, `fableWeekly` |
| `google` | `agy -p /quota --output-format json`, answered without a model turn | `gemini-5h`, `gemini-weekly`, `3p-5h`, `3p-weekly` |
| `synthetic` | `GET https://api.synthetic.new/v2/quotas` with the key opencode holds; the call is not counted | `5h`, `week` |

- `orca account list` also carries an `antigravity` row with
  `status: unavailable`. It means Orca cannot read that usage (no Gemini CLI
  sign-in), nothing about the pool. Never drop `google` on it: a pool is out
  only when its own `pool-usage.sh` row shows `signed_in: false` or a window
  over the threshold.
- A row with `windows: null` and an `error` means the source failed: say so
  in the report and treat the pool as signed in with unknown headroom.
- On `google`, the `gemini-*` windows meter Gemini models and the `3p-*`
  windows meter Claude and GPT models run through `agy`; only the group of
  the lane's model counts.
- `synthetic` meters a rolling five-hour request limit and a weekly credit
  limit, counted by Synthetic, so usage from another host is included. Both
  refill in ticks instead of resetting, so its row carries no `resets`; the
  tick interval is unmeasured.
- The coordinator and every native subagent draw on the `claude` pool, a
  Fable session also on `fableWeekly`. Past 85% there, keep native
  delegation to `judge`; review lanes follow "Orca or native" in `SKILL.md`,
  and the rest goes to the other prepaid pools.
- A window at 0% may have just rolled over; `resets` in the same row says
  whether it did.
