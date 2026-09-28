# Resolving review lanes

Load this when resolving a `review-unit` or `review-seam` lane (step 3 of
`SKILL.md`), and when a review role has no survivor because the executors
cover its fit set, as after a fix round on several models.

## Find the executor

The run log names the model that executed each unit, with `$plan` the
plan's path:

```bash
python3 -B -c 'import sys; sys.path.insert(0, ".claude/skills/delegate/scripts"); import runlog; events=list(runlog.read()); grades={}; [grades.__setitem__(e.get("run"), e.get("outcome")) for e in events if e.get("event") == "grade"]; [print(e.get("unit") or "-", e.get("model") or e.get("agent")) for e in events if e.get("event") == "start" and e["role"].startswith("execute") and e.get("plan") == sys.argv[1] and grades.get(e.get("run")) in {"accepted", "amended"}]' "$plan"
```

- The last `grade` event for a run wins. A rejected or blocked executor did
  not author work that reached the review set, so its model stays eligible.
- A unit with no line of its own ran in a lane that ran several (printed
  `-`, or a `drive` stage name such as `implement`). With no such lane, the
  coordinator executed it, on its own vendor.
- A `google` id carries the effort suffix (`gemini-3.8-flash-high` is
  `gemini-3.8-flash`). An opencode id is the `pool_id` of its registry
  model; a lane logged before opencode took `--model` names the agent.
- A reviewer a session spawns as its own subagent runs on that session's
  vendor, so step 3 applies to it like any other lane.

## Split by writer

A review role left with no survivor because the executors cover its fit
set splits by writer. The writers are the runs step 3 counts that also have
an `end` event. A commit belongs to the run with the fewest commits in its
`start` `base` to `end` `head` range that still holds it, the later-started
on a tie, since a `drive` stage's range holds the commits of the lanes it
merged. A commit in no range belongs to the coordinator's model.

This prints each commit of the review range with its writer, `$coordinator`
being the coordinator's registry model id, and exits nonzero on a revision
git cannot resolve:

```bash
python3 -B -c 'import subprocess, sys; sys.path.insert(0, ".claude/skills/delegate/scripts"); import runlog; git = lambda *a: subprocess.run(["git", *a], capture_output=True, text=True, check=True).stdout.split(); events = list(runlog.read()); grade = {e["run"]: e.get("outcome") for e in events if e.get("event") == "grade"}; head = {e["run"]: e["head"] for e in events if e.get("event") == "end"}; runs = [(set(git("rev-list", e["base"] + ".." + head[e["run"]])), e.get("model") or e.get("agent"), e["at"]) for e in events if e.get("event") == "start" and e["role"].startswith("execute") and e.get("plan") == sys.argv[1] and grade.get(e["run"]) in {"accepted", "amended"} and e["run"] in head]; [print(c[:12], max((r for r in runs if c in r[0]), key=lambda r: (-len(r[0]), r[2]), default=(None, sys.argv[3]))[1]) for c in git("rev-list", "--no-merges", sys.argv[2])]' "$plan" "$base..$head" "$coordinator"
```

Split by the added lines that survive at head, not by commit, since a fix
commit often rewrites lines an earlier writer added. Each surviving added
line goes to the writer of the commit `git blame <base>..<head> -- <file>`
names for it. Context lines and boundary lines (blamed to a commit before
`<base>`, marked `^`) belong to no writer.

- Group the lines by writer (by vendor for `review-unit`) and resolve one
  lane per group, with step 3 dropping only that group's writer.
- A hunk whose lines have several writers is in each of their groups; each
  lane reviews its own lines.
- A hunk that only deletes has no surviving line to blame; the coordinator
  reviews it with the seams.
- Each lane gets the whole `<base>..<head>` diff for context and reviews its
  group's lines report-only.

The coordinator reads the seams between the groups itself, as `review` does
for units, and records the one verdict; under `drive`, that is the
review-stage worker. The report names the split and each lane's lines.
