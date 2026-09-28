# Calibration: the fixed tasks and how they are graded

Load this only for `tune` step 4 or when comparing two routing setups.

- The task
- The review task
- Lanes and cost
- Run a lane
- Acceptance tests
- Record the result
- Cost figures

## The task

Ensure `src/common/pump` has `Merge`: a function that forwards the values
of several pumps into one, with stop, cancellation, and the first error
propagating in both directions and no goroutine left behind. The brief is
`calibration/brief.md`; it states the contract and nothing about the tests.
`Merge` landed in d4421211, so a lane branched from a later commit measures
verification, and one branched from d4421211's parent (`d4421211^`)
measures implementation. Name the base commit in the report; two
calibrations compare only on the same base. The task is small (one file,
well under 150 lines) and hard because every close path has to be ordered
against in-flight sends, and Go's `select` does not promise which ready
case runs.

## The review task

The review lanes grade `review-unit`, `review-seam`, and every
`judgment: true` role (`plan`, `research`, `judge`, `critique`), since
those roles have no task of their own and the review task is the one that
measures reasoning over code rather than typing it. Branch the lane from
30c8da74, a `Merge` that deadlocks, and give it `calibration/review-brief.md`.
The known bug: a forwarder never watches its source's `Stopped()`, so when
one source fails and a sibling's producer stops without closing its data
channel, that forwarder blocks on the channel read and `wg.Wait` never
returns. A lane finds it when a finding names that path with a call
sequence that reaches it. Count every other finding that holds up on a
read of the code as a valid extra.

## Lanes and cost

One worktree per lane, branched from the same commit:

```bash
git worktree add -b bench-<lane> ~/Projects/worktrees/FlowSeer/bench-<lane> <base>
```

Lanes on the `opencode` CLI each get their own SQLite database: `bench.sh`
sets `OPENCODE_DB` per lane, because instances sharing the default one
corrupt each other's sessions when lanes overlap.

## Run a lane

```bash
.claude/skills/tune/scripts/bench.sh --lane <name> --cli <claude|codex|agy|opencode> \
  --model <id> [--effort <level>] \
  --brief <brief file> --dir <worktree> --out <json>
```

A sweep runs one lane per level in the model's `effort` list. `agy` takes
the level in the model id (`gemini-3.8-flash-<effort>`), and an `opencode`
lane runs once with no level. `bench.sh` exits 2 on an `--effort` its CLI
branch cannot apply. It records wall time,
the CLI's reported usage, and the exit code. On `opencode` it also writes
`finish` and `tool_calls`: a lane that ends `finish: length` with
`tool_calls: 0` never touched the repository, which is a 0 and not a
harness failure, and it spends an ordinary-looking number of tokens doing
it.

## Acceptance tests

`calibration/merge_accept_test.go.txt` holds tests the candidate never
sees. To grade, copy it into the package and run each test on its own
under the race detector:

```bash
cp .claude/skills/tune/references/calibration/merge_accept_test.go.txt <worktree>/src/common/pump/merge_accept_test.go
(cd <worktree> && for t in $(sed -n 's/^func \(Test[A-Za-z0-9_]*\).*/\1/p' src/common/pump/merge_accept_test.go); do
  go test -race -count=3 -run "^$t\$" ./src/common/pump/ >"${TMPDIR:?}/$t.log" 2>&1 && echo "PASS $t" || { echo "FAIL $t"; tail -5 "$TMPDIR/$t.log"; }; done)
```

`testing/synctest` bubbles fail when a goroutine is still blocked at the
end, so a leaked forwarder shows up as a failure, not a hang. Run each test
alone, since a deadlock panics and ends the test binary, so one run of the
whole package never reaches the tests after the failing one. Then run the
verifier on the candidate's changed paths from the worktree root. A lane
passes when both are green; record partial credit as the count of `PASS`
lines over the total.

## Record the result

Write one result per level under the task's role, keyed by the level the
lane actually ran at (the figures here show the shape only):

```yaml
local: {execute: {medium: {runs: 1, base: bd9e0862, pass: 7/7, wall_s: 301, cost_usd: 0.61},
                  xhigh: {runs: 1, base: bd9e0862, pass: 6/7, wall_s: 552, cost_usd: 2.26}},
        review-unit: {high: {runs: 1, base: 30c8da74, found_known_bug: true, extra_valid: 2, wall_s: 240, cost_usd: 0.80}}}
```

- The key is the id suffix on `agy`, the literal `none` for a model whose
  `effort` list is empty, `default` for a lane that ran at the CLI's own
  default, and `unrecorded` for a result from before levels were recorded.
- `base`: the commit the lane branched from.
- When `runs` is above 1, `pass`, `wall_s`, and `cost_usd` are lists, one
  value per run.

The review result stands for the `judgment: true` roles too. Write it once
under `review-unit` and do not copy it into those roles.

## Cost figures

For a comparable estimate, `field.py` applies the same formula to each
transcript using the registry's prices per million tokens:

```text
cost_usd = ((input + 0.1 * cache_read + 1.25 * cache_write_5m
             + 2 * cache_write_1h) * price[0] + output * price[1]) / 1_000_000
```

`input` is uncached input. Codex reports cached input inside `input_tokens`,
so subtract `cached_input_tokens` before applying the formula. Its reasoning
tokens are already inside `output_tokens`; report them separately, but do not
charge them twice. Claude reports cache writes by duration: `ephemeral_5m`
uses the 1.25 multiplier and `ephemeral_1h` uses 2. For example, 1,000
uncached input, 2,000 cache reads, 3,000 five-minute writes, 4,000 one-hour
writes, and 500 output tokens at `[2, 10]` cost $0.0309.

Mark every computed figure `est`. opencode's recorded message cost takes
precedence and is not estimated; `bench.sh` sums it over every step of the
session and its child sessions, because the reply to the prompt carries
only the last message's, and writes `usage: null` when it could not read
the steps. A prepaid pool's marginal cost is zero below its cap, so also
report what share of the pool's window the lane consumed when the pool
exposes one. When the lane had its pool to itself, compare that share with
the reported cost before ranking lanes: a meter that moved well past what
the cost accounts for means usage the CLI did not report, and the report
gives both figures.

Remove the worktrees and branches when the comparison is recorded.
