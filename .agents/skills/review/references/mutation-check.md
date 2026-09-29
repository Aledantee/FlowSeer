# Running a missing mutation

For a new test whose commit body carries no mutation and quoted `--- FAIL`
line, run the mutation yourself, never through a sub-worker:

1. Copy the source file first: `cp <path> "$TMPDIR/<name>.orig"`.
2. Mutate it in place.
3. Run the focused test and quote the result.
4. Restore from the copy, never with `git checkout` or `git restore`.

A test that passes against the defect is a correctness finding.
