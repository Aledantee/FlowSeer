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

A test that passes with the condition removed that its title, comment, or
commit body states is a false test. A mutation that survives in a branch or
boundary no test's title, comment, or commit body states is a gap. A
surviving mutation that can be read either way is a false test
(`fix-loop.md` decides the boundary).
