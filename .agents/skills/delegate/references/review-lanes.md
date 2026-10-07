# Resolving review lanes

Load this when resolving a `review-unit` or `review-seam` lane (step 2 of
`SKILL.md`), and when a review role has no survivor because the executors
cover its fit set, as after a fix round on several models.

## Find the executor

The run log names the model that executed each unit, with `$plan` the
plan's path:

```bash
uv run --quiet tools/scripts/run.py delegate runlog executors --plan "$plan"
```

- The last `grade` event for a run wins. A rejected or blocked executor did
  not author work that reached the review set, so its model stays eligible.
- A unit with no line of its own ran in a lane that ran several (printed
  `-`, or a `drive` stage name such as `implement`). With no such lane, the
  coordinator executed it, on its own vendor.
- A `google` id carries the effort suffix (`gemini-3.8-flash-high` is
  `gemini-3.8-flash`). An omp id is the `pool_id` of its registry model,
  pinned with `--model`.
- A reviewer a session spawns as its own subagent runs on that session's
  vendor, so step 2 applies to it like any other lane.

## Split by writer

A review role left with no survivor because the executors cover its fit
set splits by writer. The writers are the runs step 2 counts that also have
an `end` event. A commit belongs to the run with the fewest commits in its
`start` `base` to `end` `head` range that still holds it, the later-started
on a tie, since a `drive` stage's range holds the commits of the lanes it
merged. A commit in no range belongs to the coordinator's model.

This prints each commit of the review range with its writer, `$coordinator`
being the coordinator's registry model id, and exits nonzero on a revision
git cannot resolve:

```bash
uv run --quiet tools/scripts/run.py delegate runlog writers --plan "$plan" --range "$base..$head" --coordinator "$coordinator"
```

Split by the added lines that survive at head, not by commit, since a fix
commit often rewrites lines an earlier writer added. Each surviving added
line goes to the writer of the commit `git blame <base>..<head> -- <file>`
names for it. Context lines and boundary lines (blamed to a commit before
`<base>`, marked `^`) belong to no writer.

- Group the lines by writer (by vendor for `review-unit`) and resolve one
  lane per group, with step 2 dropping only that group's writer.
- A hunk whose lines have several writers is in each of their groups; each
  lane reviews its own lines.
- A hunk that only deletes has no surviving line to blame; the coordinator
  reviews it with the seams.
- Each lane gets the whole `<base>..<head>` diff for context and reviews its
  group's lines report-only.

The coordinator reads the seams between the groups itself, as `review` does
for units, and records the one verdict; under `drive`, that is the
review-stage worker. The report names the split and each lane's lines.
