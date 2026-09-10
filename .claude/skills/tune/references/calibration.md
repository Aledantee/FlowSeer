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
sees. To grade, copy it into the package and run under the race detector:

```bash
cp .claude/skills/tune/references/calibration/merge_accept_test.go.txt <worktree>/src/common/pump/merge_accept_test.go
(cd <worktree> && go test -race -count=3 ./src/common/pump/ 2>&1 | tail -20)
```

`testing/synctest` bubbles fail when a goroutine is still blocked at the
end, so a leaked forwarder shows up as a failure, not a hang. Then run the
verifier on the candidate's changed paths from the worktree root. A lane
passes when both are green; record partial credit as the count of passing
acceptance tests over the total.

## Lanes and cost

One worktree per lane, branched from the same commit:

```bash
git worktree add -b bench-<lane> /Users/aledante/Projects/worktrees/FlowSeer/bench-<lane> HEAD
```

`bench.sh` writes `wall_s` and the CLI's reported usage. Where the CLI
reports no dollar figure, estimate from the registry price:
`input * price[0] + output * price[1]` per million, with cache reads at a
tenth of the input price. Report cost per lane as that estimate and mark it
estimated; a prepaid pool's marginal cost is zero below its cap, so also
report what share of the pool's window the lane consumed when the pool
exposes one.

Remove the worktrees and branches when the comparison is recorded.
