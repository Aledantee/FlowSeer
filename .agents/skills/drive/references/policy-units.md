# Review policy units before committing

Load before dispatching a unit whose Files line names a policy surface in
`AGENTS.md`, Hard boundaries. This procedure applies to every worker runtime.
Do not rely on a launcher's permission prompts for guardrail review.

The brief authorizes the named policy edits and focused checks, and requires
the worker to stop with that unit uncommitted. Earlier approved units may
remain committed. The report file carries the full diff, changed paths,
check results, and a Git blob hash for each changed file. For a deletion,
record the deleted path and its prior blob. Include new files in the diff.

The driving conversation shows the diff and hashes to the person and asks
for explicit guardrail review of that unit. A delegated stage forwards the
unit's report file to its parent and stops, keeping the unit's lane and its
own lane in place. It asks no question itself.

Keep the lanes and worktrees while waiting. A refusal leaves the unit
uncommitted and stops this plan with the reason. Do not run parking cleanup
on a dirty checkout or commit rejected edits to make cleanup pass. Report
the retained lanes and paths. The driving coordinator records a
`Parked by drive:` question in its own plan copy, including those lane
handles, and verifies that path. Keep the recorded work branch unchanged.
A resumed drive asks that question before dispatch and reuses the retained
lanes when approval arrives. Independent phases may continue, but `land`
waits until the person decides how to handle the retained work.

After approval, compare the index and working tree with the approved blobs
and deletions before committing the unit. Any changed content needs a new
review. Record the approved paths, blobs, and deletions in the driving
plan's Decisions and remove its hold question, then verify that path.
Resume the stage in the same checkout, carrying the approval and
hashes in its brief. The stage passes them to the waiting unit worker.
The coordinator verifies the committed blobs before
accepting the unit. Approval covers only that unit, not later policy edits.
