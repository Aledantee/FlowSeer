# Calibration: the fixed task and how it is graded

Load this only for `tune` step 4 or when comparing two routing setups.

## The task

Ensure `src/common/pump` has `Merge`: a function that forwards the values
of several pumps into one, with stop, cancellation, and the first error
propagating in both directions and no goroutine left behind. The brief is
`calibration/brief.md`; it states the contract and nothing about the tests.
`Merge` landed in d4421211 on 2026-09-09, so a lane branched from a later
commit measures verification, and one branched from d4421211's parent
(`d4421211^`) measures implementation. A calibration names its base
commit in the report; two calibrations compare only on the same base.
It is small (one file, well under 150 lines) and hard for the reason the
package doc gives: every close path has to be ordered against in-flight
sends, and Go's `select` does not promise which ready case runs.

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
end, so a leaked forwarder shows up as a failure, not a hang. A deadlock
panics and ends the test binary, so one run of the whole package never
reaches the tests after the failing one and undercounts; that is why each
test runs alone. Then run the verifier on the candidate's changed paths
from the worktree root. A lane passes when both are green; record partial
credit as the count of `PASS` lines over the total.

## Lanes and cost

One worktree per lane, branched from the same commit:

```bash
git worktree add -b bench-<lane> /Users/aledante/Projects/worktrees/FlowSeer/bench-<lane> HEAD
```

`bench.sh` writes `wall_s` and the CLI's reported usage. For a comparable
estimate, `field.py` applies the same formula to each transcript using the
registry's prices per million tokens:

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
the steps. A prepaid pool's marginal cost is zero below
its cap, so also report what share of the pool's window the lane consumed
when the pool exposes one. When the lane had its pool to itself, compare
that share with the reported cost before ranking lanes: a meter that moved
well past what the cost accounts for means usage the CLI did not report,
and the report gives both figures.

Remove the worktrees and branches when the comparison is recorded.
