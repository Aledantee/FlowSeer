---
name: tune
description: Refresh the model registry that `delegate` routes on. Discovers which agent CLIs and prepaid pools this host can reach, pulls live model catalogues and prices, records external evidence, optionally calibrates candidate models on a fixed repository task, and writes the machine-wide registry `~/.claude/models/registry.yaml` plus the host pool file. Use when asked to tune, when a new model or CLI appears, when `delegate` warns the registry is stale, or before a large plan is dispatched. Not for choosing a model for one task; `delegate` does that from the registry.
argument-hint: "[discover | catalogue | field | calibrate <lane>... | all]"
---

# Tune the model registry

The registry is the routing table: pools, models with price, context,
effort levels and refusal posture, and the fit set per role. `delegate`
never names a model; it names a role and resolves it here. This skill keeps
the registry true. Every number it writes carries a source and a date in
`~/.claude/models/evidence.md`; a number without one is an opinion and does
not go in.

The registry is machine-wide, because pools, prices, and calibrations belong
to the machine and its accounts, not to one checkout:

| File | Holds | Written by |
| --- | --- | --- |
| `~/.claude/models/registry.yaml` | The registry every project on this machine reads | this skill |
| `~/.claude/models/evidence.md` | Source and date per registry number | this skill |
| `~/.claude/models/host.yaml` | CLIs, Orca reachability, pool sign-in and windows | step 1, `pool-usage.sh` refreshes |
| `.claude/models/registry.yaml` in a project | Overrides for that project, committed | a person, or this skill on request |

The effective registry is the machine-wide file with the project file laid
over it. Under `pools`, `models`, `roles`, and `opencode_agents` a project
entry replaces the machine-wide entry of the same name and adds the ones it
lacks; any other top-level key in the project file (`sensitive_paths`,
`as_of`) replaces the machine-wide key whole. Either file may be absent;
with neither, there is no registry and this skill writes the machine-wide
one. Write a result to the project file only when it holds for that project
alone, such as its `sensitive_paths` or a fit set the project narrows.

Inputs: the effective registry, the network, the installed CLIs. Completion:
the machine-wide `as_of` is today, `host.yaml` is regenerated, and the report
names every field that changed with its evidence and the file it changed in.
Failure: a step that cannot reach its source says so and leaves the previous
value with its old date; it never guesses.

`field` runs steps 1, 3a, and 5 from local run and transcript stores. It does
not need a catalogue request or a calibration lane. `all` includes the field
step before calibration; `discover` and `catalogue` keep their named scope.

## 1. Discover the host

```bash
mkdir -p ~/.claude/models
.claude/skills/tune/scripts/discover-host.sh > ~/.claude/models/host.yaml
```

Writes which CLIs exist, which pools are signed in, what Orca can pin with
`--model`, the `opencode` model ids split by `synthetic/` (prepaid) and
`opencode/` (per-token), and the Claude rate-limit windows. Run this step on
every invocation.

opencode's `synthetic/` list is its own catalogue and keeps ids Synthetic
has stopped serving: on 2026-09-19 four of ten answered 404, and by
2026-09-23 the same four answered a probe without error, so a request no
longer tells a served id from a retired one. Read the served ids and their
context from `GET https://api.synthetic.new/openai/v1/models` with the
`synthetic` key from opencode's `auth.json`, and compare the registry's
`agent` names with the `agent` block in `~/.config/opencode/opencode.json`.

## 2. Pull live catalogues

```bash
python3 .claude/skills/tune/scripts/catalogue.py ~/.claude/models/registry.yaml .claude/models/registry.yaml
```

Reads models.dev and OpenRouter, prints per registry model the vendor price
and context beside the registry's, and lists ids on either feed that the
registry lacks. Vendor price wins over broker price; where they differ by
more than 20% both go in the report, the vendor's in the registry. A missing
id on the vendor feed means the model is gone: mark it `retired: <date>`, do
not delete it, since a plan ledger may name it.

## 3. Record external evidence

For each new or changed model, one web pass for: the vendor's launch note,
Terminal-Bench 2.1 and SWE-bench Verified with the harness named, and refusal
reports for security tooling. Append to `evidence.md` as `model — claim —
source URL — date`. Conflicting numbers are recorded as conflicting.
Benchmarks run on different harnesses are not compared in the registry;
`terminal_bench` is filled only from a run whose harness is named.

## 3a. Read field results

Run the scorer with `--since` set to the latest `field.<role>.as_of` in the
effective registry, or 90 days before today when no field block exists. Pass
both registry files in precedence order:

```bash
python3 .claude/skills/tune/scripts/field.py --since YYYY-MM-DD \
  --registry ~/.claude/models/registry.yaml .claude/models/registry.yaml
```

`field.py` prints JSON. Read `unmatched` before using any rate: a missing
transcript removes speed and cost evidence, and a role outside the registry
cannot receive a field block. Aggregate `runs` by model and role across
sources; do not average the per-source `groups` rates. Write `field.<role>`
only for a registry role with at least five graded runs, or at least ten
findings for a review role. Each block records `as_of`, `since`, `runs`, the
accepted, amended, rejected, and blocked counts, `verify_pass`, the reviewer
`held` and `findings` when applicable, and medians of `active_s`, total
tokens, and `cost_usd`. Append one dated `evidence.md` line naming the run
log and transcript sources for each block changed.

These thresholds are a starting point. Propose fit-set order by accepted
over graded runs, leaving blocked out of the denominator. Within ten
percentage points, put the lower median `active_s` first. Keep calibration
order unless every model in that role's fit set has enough graded runs for
the comparison. Propose removal when amended plus rejected reaches 40% of
at least five graded runs, or a reviewer's held share is below 50% of at
least ten findings. A model seen in a role without `local.<role>` becomes a
calibration candidate. Field evidence can order a fit set or support a
removal proposal; entry still requires a calibration result. Step 5 asks
before applying any proposed fit-set change.

## 4. Calibrate on the repository (optional, costs money)

The public numbers do not say how a model does on this Go tree with race
tests and the verifier. `references/calibration.md` describes the fixed
task, the acceptance tests, and the lanes. Before running, state the lanes
and the expected spend per lane from the registry prices, and ask the user
which lanes to run; a prepaid pool still consumes its window. Then, per lane:

```bash
.claude/skills/tune/scripts/bench.sh --lane <name> --cli <claude|codex|agy|opencode> \
  --model <id> [--effort <level>] [--agent <opencode agent>] \
  --brief <brief file> --dir <worktree> --out <json>
```

Run each lane at the `effort` of the role it is graded for; `bench.sh`
exits 2 on an `--effort` its CLI branch cannot apply. The script records
wall time, the CLI's reported usage, and the exit code. Grade each lane
with the acceptance tests and the verifier, and write
`local.<role>: {effort, runs, base, pass, wall_s, cost_usd}` on the model,
with `base` the commit the lane branched from and `effort` the level the
lane actually ran at: the id suffix on `agy`, and the literal `none` for a
model whose `effort` list is empty, since no level reaches it. When `runs`
is above 1, `pass`, `wall_s`, and `cost_usd` are lists, one value per run.
A result without `effort`, `runs`, and `base` cannot show whether it was
measured at another level or on another base than the role routes at. Entry
to a role's fit set requires a calibration result. Field results can change
its order or support a removal proposal under step 3a; a benchmark cannot.
Calibrate models flagged by field runs as missing `local.<role>` first.

## 5. Write and report

Update the machine-wide registry: `as_of`, changed fields, fit sets. Keep each
role's `fit` list best-first by calibrated speed, then pool usage, among models
that passed; `delegate` uses this order after filtering hot and busy pools.
When the project file overrides a field this run changed, name the override in
the report: the project keeps routing on its own value. Keep the opencode
agent block in `~/.config/opencode/opencode.json` in step with
`opencode_agents`. Keep the registry's role-order header accurate: fit sets
are ordered by field success when every member has enough evidence, with
median active time breaking close results; otherwise they keep calibration
order. `delegate` takes the first fitting model in that order.

A change to a role's `fit` membership or order is never applied silently.
Report field-driven proposals separately from calibration-driven ones,
along with the commands run, every changed field and its evidence, and
anything a source refused to answer, then ask the user (`AGENTS.md`, Agent
behavior) per proposed membership or order change whether to apply it, so
a person sees which default moved and why.
