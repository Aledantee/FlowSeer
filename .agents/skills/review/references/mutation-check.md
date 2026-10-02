# Running a reviewer's mutation

For each behavior change, run one mutation yourself, never through a
sub-worker. Choose a fault the commit body's mutation does not already
cover: the check moved to the wrong place, a boundary off by one, a branch
inverted. A new test whose commit body carries no mutation and quoted
`--- FAIL` line gets its missing mutation the same way.

1. Copy the source file first: `cp <path> "$TMPDIR/<name>.orig"`.
2. Mutate it in place.
3. Run the focused test and quote the result.
4. Restore from the copy, never with `git checkout` or `git restore`.

A test that passes against the defect it names is a correctness finding. A
mutation that survives in behaviour no test names is a gap (`fix-loop.md`).
