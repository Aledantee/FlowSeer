# Measure a skill edit

Load this before an edit that adds lines to a skill with an `evals/`
directory. A skill that has none is edited as before, and the report names
it unmeasured.

## Run the comparison

Apply the edit and leave it uncommitted. The `current` variant copies the
skill directory from the runner's own checkout, uncommitted edits included,
and `ref:HEAD` is the skill as it was before the edit. Run with the sandbox
disabled, from any directory, with `checkout` set to the absolute path of
the worktree that holds the edit:

```bash
checkout=<absolute path of the worktree>
uv run "$checkout/tools/scripts/run.py" steer skill-eval --skill <skill> --variants current,ref:HEAD \
  --out "$TMPDIR/<skill>-eval.jsonl"
```

Each lane is a worktree under `~/Projects/worktrees/FlowSeer`, removed with
its branch once graded. Lanes run one at a time on `claude-opus-5-5`, one run
per variant. A lane stops at the scenario's `budget_usd` (3 by default), and
no lane starts once the invocation's reported cost reaches `--max-total-usd`
(30 by default). The table prints one row per scenario and variant: `pass`
as passed over total checks, `invoked` (whether the skill fired),
`cost_usd`, `wall_s`, and `status`.

## Decide

Read `status` first. Only an `ok` row is a measurement:

| Status | Meaning | Next |
| --- | --- | --- |
| `ok` | graded | compare |
| `stale` | `setup.py` failed or left the lane dirty | fix the scenario, rerun |
| `invalid` | a `none` lane could still load the skill | report it, the row measures nothing |
| `refused`, `downgraded` | the model declined or another model answered | rerun once, then report |
| `skipped` | the cost cap was reached | raise `--max-total-usd` or narrow with `--scenario` |

Apply the edit when `current` loses no check that `ref:HEAD` passed. When it
loses one, rerun both variants with `--runs 3` before rejecting, since a
single run varies, and reject only when `current` passes that check in fewer
of the three runs than `ref:HEAD`. Quote the table in the edit's commit body.
Results are never committed as files.

On a reject, restore the edited skill files from `HEAD`, keep the
observation entry in the queue with the table appended, and report the edit
as rejected by measurement. Step 6 of `steer` deletes every applied and
rejected entry and then asks to commit, so a rejected edit left in the tree
would be committed with the others.

Run `--variants current,none` as well when the skill's value is in doubt.
When `none` matches or beats `current` on every scenario, log an observation
through `compound` (Observe) that proposes cuts to the skill, since a skill
that the model does without adds listing tokens on every turn.

## Write a scenario

A scenario is `.agents/skills/<skill>/evals/<scenario>/` with three files:

- `scenario.json`: `skill`, `query`, `budget_usd`, and `expected_behavior`,
  one sentence per check. The query never names the skill, is the same for
  every variant, and ends with "Do not ask questions. Where you would ask,
  state the question and its options, then stop." The runner replaces
  `{EVAL_PARENT}` with the fixture commit's parent. A query that uses
  `{EVAL_PARENT}` needs a `setup.py`. Without one, a `current` lane that adds
  skill files sits on the runner's lane-only commit, so the parent differs
  across variants.
- `setup.py` (optional): a script with inline metadata that the runner
  starts through `uv run --script`. It runs in the lane and commits the fixture, leaving
  `git status --porcelain` empty. A failing setup makes the row `stale`.
- `check.py <lane> <result-text>`: runs in the lane with `EVAL_BASE` set to
  the fixture commit and prints one `PASS <name>` or `FAIL <name>` per
  check.

A check reads the lane mechanically, never through a judge model: files on
disk, a state file, `git diff --name-only "$EVAL_BASE"` plus untracked
files (a lane may leave its work uncommitted), the checkpoint file, a
`run.py` command's output, or a grep of the result text for a path and a
symbol. A seeded defect is one no hook or conformance gate reports, since
lanes keep the hooks and the Stop hook hands a gate finding to the model.

Write each check so it fails on an untouched lane with an empty result
text. A check that passes on nothing measures nothing. Run the lint, which
asserts that and the scenario's shape, before committing a scenario:

```bash
uv run "$checkout/tools/scripts/run.py" steer skill-eval --lint --skill <skill> --worktree-root "$TMPDIR/lanes"
```

It prints how many scenarios it checked. Then hand-make a passing state in a
lane kept with `--keep` and confirm the `PASS` lines, so the check is
reachable as well. Lanes are named `eval-<skill>-<scenario>-<variant>-<n>`,
and lint lanes use variant `lint` and run 1. Grade a kept lane with:

```bash
EVAL_BASE=$(git -C <lane> rev-parse HEAD) uv run --script <scenario dir>/check.py <lane> "<text>"
```

A kept lane is never removed, and the next lint stops on its existing
`skill-eval/<name>` branch. Remove it when done:

```bash
git -C "$checkout" worktree remove --force <lane>
git -C "$checkout" branch -D skill-eval/<name>
```
