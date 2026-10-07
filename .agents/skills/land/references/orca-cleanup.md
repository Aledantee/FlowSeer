# Settling workers, child worktrees, and lanes

Load this in step 2 whenever `orca status --json` (unsandboxed) reports the
runtime reachable.

Orca workers started for this task must be settled and released, so that
removing the worktree later kills nothing:

```bash
orca orchestration worker-list --json
orca terminal list --worktree active --json   # this terminal and earlier coordinators
```

Release a settled worker with `worker-release`.

A lane's terminal runs in its child worktree, so this worktree's list holds
coordinators and terminals the person opened, not lanes. After a `drive`
hand-off the predecessor coordinators stay open in this worktree:
each successor terminal is titled `drive`, the title `successor.sh` passes
to `orca terminal create`. A terminal with that title other than this one
is an earlier coordinator and is expected. Leave it open and name it in
the report. The terminal the drive began in carries whatever title the
person gave it. Name any other terminal in the report too, and leave it
alone. Do not read a terminal's state as idle or running from this list,
since how Orca reports an idle CLI terminal there is unverified.

Remove a child worktree of this one whose branch has landed here, as
`delegate/references/orca.md` describes under Orchestration runs. Name one
whose branch did not land, or that holds uncommitted files while its
terminal is live, in the report and leave it alone. One whose terminal
exited with uncommitted files is parked and stopped as
`delegate/references/orca.md`, When a step fails, describes. A `git worktree remove` or `git branch -d` the harness refuses
from this session goes into the report as a command for the person, with the
child's path and branch:

```bash
orca worktree list --json   # entries whose parentWorktreeId is this worktree
```

`.claude/skills/delegate/scripts/orca-worker.sh status` (unsandboxed) must
also list no lane of this task; grade a merged lane still listed (`delegate`,
the outcome table), then stop it with `stop <slug>`, which removes its
checkout and branch. The script keeps lane state in the git common
directory, so `status` lists the lanes of every worktree of the repository
and does not record which coordinator started one. A lane of this task is
one whose path is a child worktree of this one (`orca worktree list` above).
That includes the lanes a predecessor `drive` coordinator started, since a
successor runs in the same worktree and `start` makes each lane a child of
the active worktree. A lane under another worktree belongs to other work:
leave it alone.
