# Orca workers and full handoffs

Load this when no Herdr server runs and editing work still has to leave
the coordinating session, or for a full handoff. `SKILL.md` names the
lane; this file is the procedure.

## Supervised worker

Load the version-matched guide first with `orca skills get orchestration`,
then:

```bash
orca orchestration run-create --objective "<plan title>" --json
orca orchestration task-create --spec "<brief>" --json
orca orchestration worker-start --task <task_id> --worktree new-child --base-branch "$(git branch --show-current)" --name <slug> --agent <claude|codex> --model <id> --effort <level> --setup skip --json
orca orchestration check --wait --types worker_done,escalation,question --timeout-ms 900000 --json
orca orchestration worker-release --dispatch <dispatch_id> --json
```

- `--worktree new-child` for workers that edit code, since `go test` build
  state collides in a shared checkout; `--worktree current` for workers
  that edit disjoint documentation files.
- `--base-branch` names the coordinator's own branch; without it the child
  starts from `master`, and merging it then also merges whatever landed on
  `master` since.
- `--setup skip`: this repository configures no Orca setup script, and an
  empty script is reported as a failed setup.
- `--model` pins Claude, Codex, and Cursor ids only; an `agy` or
  `opencode` lane cannot be dispatched here. Without a Herdr server the
  `google` and `go` pools have no supervised lane; the wave runs on
  `claude` and `codex` and the report says so.
- A codex worker stops at the hooks-review dialog for `.codex/hooks.json`
  and Orca reports "Agent startup blocked: codex-hooks-review-prompt"; use
  a Claude worker unless that dialog has been answered on this host.
- `check --wait` is re-armed by heartbeats and returns before a long
  worker finishes; reissue it. When it returns nothing, rule out causes in
  cost order: the provider's quota, the terminal tail (`orca terminal
  read`) for a prompt or a mangled instruction, then `git status` in the
  worker's worktree, where a written file with no commit means the worker
  is still testing. A clean tree, a final commit, and an agent that says
  Orca is not running means the worker sent its report from inside the
  sandbox: `orca-sandbox.md` says how to settle it.

Update the worktree comment at each checkpoint:

```bash
orca worktree set --worktree active --comment "unit 2 landed; verifying unit 3" --json
```

## Remove a finished child worktree

`worker-release` closes only the agent terminal; the worktree and its
branch stay, and a merged child left behind shows in Orca as live work.
After the merge, the green verifier run, and `worker-release`:

```bash
child=$(orca orchestration worker-show --dispatch <dispatch_id> --json | jq -r .result.worker.worktree_id)
git -C "${child#*::}" status --porcelain     # empty, or stop and report what is there
orca worktree rm --worktree "id:$child" --json
git branch -d <child branch>                 # refuses when the merge did not land here
```

A handed-off child has no dispatch: take its id from `orca worktree list
--json`, where `parentWorktreeId` names this worktree. Never pass
`--force`. The session's own worktree is a person's to remove: `orca
worktree rm` on it kills the terminal that issues it.

## Full handoff

A full handoff creates the worktree with the brief as its prompt and stops
supervising. The brief carries the whole ledger when one exists, since the
child worktree has its own git directory:

```bash
orca worktree create --name <slug> --parent-worktree active --agent <agent> --prompt "<brief>" --json
```

`--agent codex` accepts no flags, so a controlled codex handoff is a
worktree without `--agent` followed by `orca terminal create --command
"codex -a never --sandbox danger-full-access --model <id>"`.
