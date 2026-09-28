# Refresh: audit the existing solutions

Load this only for `compound refresh` or a request to audit stale solutions.

Audit each solution under `docs/solutions/`. Dispatch one worker per
solution for role `execute`, as many at once as `delegate`'s Wave size
allows; when `delegate` sends the work to this session, audit the solutions
one at a time here. Edit `docs/solutions/README.md` from the coordinating
session only.

1. Open every cited path and confirm the quoted lines and symbols exist.
   Check that `module` exists, `applies_when` still describes the situation,
   and the README row still matches.
2. Re-run the test it relies on. Skip a gate that needs hardware, Docker, or
   more than a few minutes, and say so. A dated measurement stays as history.
3. When a later convention, `docs/architecture/` record, style rule, or
   sibling solution states the same thing, replace the restatement with a
   link.

Outcomes: kept, fixed (citations only), rewritten (a claim changed), removed
(the tree no longer behaves as described; delete the file and its row). Set
`last_verified` on every solution checked. Run the verifier on the changed
files and report the outcomes table with a reason per row.
