# Reading pool rows

Load this when reading a `google`, `synthetic`, `zai`, or Fable row from
`scripts/pool-usage.sh`, a window at 0%, a row whose source failed, or when
the `claude` pool is past its limit (`usable_below`, `SKILL.md` Wave size).

`pool-usage.sh` reads each pool from the source that owns its numbers and
prints one row per pool with `signed_in`, the used percent of every window,
and the `worst` one with its reset time:

| Pool | Source | Windows |
| --- | --- | --- |
| `claude`, `codex` | `orca account list --json`, `rateLimits` | `session`, `weekly`, `fableWeekly` |
| `google` | `agy -p /quota --output-format json`, answered without a model turn | `gemini-5h`, `gemini-weekly`, `3p-5h`, `3p-weekly` |
| `synthetic` | `GET https://api.synthetic.new/v2/quotas` with the key from `~/.local/share/opencode/auth.json`; the call is not counted | `5h`, `week` |
| `zai` | `omp usage --provider zai --json`, which reads omp's own Z.ai credential | `5h`, `week` |

- `orca account list` also carries an `antigravity` row with
  `status: unavailable`. It means Orca cannot read that usage (no Gemini CLI
  sign-in), nothing about the pool. Never drop `google` on it: a pool is out
  only when its own `pool-usage.sh` row shows `signed_in: false` or a window
  over the threshold.
- A row with `signed_in: null`, `windows: null`, and an `error` means the
  source failed: say so in the report and treat the pool as signed in with
  unknown headroom. When Orca is missing or unreadable, the `claude` row
  reads `signed_in: true` if the CLI's own token file holds an unexpired
  token, and null otherwise.
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
