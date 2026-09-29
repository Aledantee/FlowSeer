---
name: tune
description: Refreshes the model registry that `delegate` routes on. Discovers reachable agent CLIs and prepaid pools, pulls live catalogues and prices, records evidence, optionally calibrates models on a fixed repository task, and writes `~/.claude/models/registry.yaml` and the host pool file. Use when asked to tune, when a new model or CLI appears, when `delegate` warns the registry is stale, or before a large plan is dispatched. Not for choosing a model for one task; `delegate` does that from the registry.
argument-hint: "[discover | catalogue | field | calibrate <lane>... | all]"
---

# Tune the model registry

The registry holds pools, models with price, context, effort levels and
refusal posture, and the fit set per role; `delegate` names a role and
resolves it here. Every number written carries a source and a date in
`~/.claude/models/evidence.md`; a number without one does not go in.

| File | Holds | Written by |
| --- | --- | --- |
| `~/.claude/models/registry.yaml` | The registry every project on this machine reads | this skill |
| `~/.claude/models/evidence.md` | Source and date per registry number | this skill |
| `~/.claude/models/host.yaml` | CLIs, Orca reachability, pool sign-in and windows | step 1, `pool-usage.sh` refreshes |
| `.claude/models/registry.yaml` in a project | Overrides for that project, committed | a person, or this skill on request |

The effective registry is the machine-wide file with the project file laid
over it. Under `pools`, `models`, and `roles` a project entry replaces the
machine-wide entry of the same name and adds the ones it lacks; any other
top-level key in the project file (`sensitive_paths`, `as_of`) replaces the
machine-wide key whole. Either file may be absent; with neither, write the
machine-wide one. Write a result to the project file only when it holds for
that project alone, such as its `sensitive_paths` or a fit set the project
narrows.

Inputs: the effective registry, the network, the installed CLIs.
Completion: the machine-wide `as_of` is today, `host.yaml` is regenerated,
and the report names every changed field with its evidence and the file it
changed in. Failure: a step that cannot reach its source says so and leaves
the previous value with its old date; never guess.

`field` runs steps 1, 3a, and 5 from local run and transcript stores, with
no catalogue request or calibration lane. `all` is a full run: every step,
the field step before calibration, and an effort sweep (step 4) over every
model a signed-in pool serves. `discover` and `catalogue` keep their named
scope.

The goal of every run is one routing point per role: the model and effort
level that give the best result for the least spend on that role's work.
Effort is part of that point. A model that passes at `medium` should not
route at `xhigh`, and one that fails at `high` may pass at `max`.

Work is not one thing, so the run measures a model against five execute
tasks of rising difficulty (simple, medium, complex, a cross-component
integration one whose trap is a subtle flow bug, and a security-sensitive
one) and two review tasks (a single unit and a component seam), so a model
cheap enough for simple edits but wrong on a subtle cross-system flow, or
one that a safety classifier refuses or silently downgrades on sensitive
paths, is caught before it routes there. Two costs decide a routing point: the model's spend and its
runtime, both read per lane. A refusal or a downgrade is a third: on the
sensitive task the run records whether the CLI declined the work or served
a different model than the one asked for, and a model that does either is
not the sensitive routing point whatever its price.

## 1. Discover the host

Run on every invocation:

```bash
mkdir -p ~/.claude/models
.claude/skills/tune/scripts/discover-host.sh > ~/.claude/models/host.yaml
```

It writes which CLIs exist, which pools are signed in, what Orca can pin
with `--model`, the `opencode` model ids split by `synthetic/` (prepaid) and
`opencode/` (per-token), and the Claude rate-limit windows.

opencode's `synthetic/` list keeps ids Synthetic no longer serves, and a
request to a retired id may answer without error. Read the served ids and
their context from `GET https://api.synthetic.new/openai/v1/models` with the
`synthetic` key from opencode's `auth.json`, and compare each registry
`pool_id` on the `synthetic` pool with that list.

## 2. Pull live catalogues

```bash
python3 .claude/skills/tune/scripts/catalogue.py ~/.claude/models/registry.yaml .claude/models/registry.yaml
```

It reads models.dev and OpenRouter, prints per registry model the vendor
price and context beside the registry's, and lists ids on either feed that
the registry lacks. Vendor price wins over broker price; where they differ
by more than 20%, report both and write the vendor's. An id missing from the
vendor feed means the model is gone: mark it `retired: <date>` and do not
delete it, since a plan ledger may name it.

## 3. Record external evidence

For each new or changed model, and for every model on a full run, one web
pass for: the vendor's launch note, the effort levels the vendor documents
and what each changes (thinking budget, default level, levels a CLI does
not expose), Terminal-Bench 2.1 and SWE-bench Verified with the harness and
effort level named, and refusal reports for security tooling. The model's
`effort` list holds the levels its CLI accepts, checked against the vendor. Append to `evidence.md` as `model — claim —
source URL — date`. Record conflicting numbers as conflicting. Do not
compare benchmarks run on different harnesses in the registry; fill `terminal_bench` only
from a run whose harness is named.

## 3a. Read field results

Set `--since` to the latest `field.<role>.as_of` in the effective registry,
or 90 days before today when no field block exists. Pass both registry
files in precedence order:

```bash
python3 .claude/skills/tune/scripts/field.py --since YYYY-MM-DD \
  --registry ~/.claude/models/registry.yaml .claude/models/registry.yaml
```

`field.py` prints JSON. Read `unmatched` before using any rate: a missing
transcript removes speed and cost evidence, and a role outside the registry
cannot receive a field block. Aggregate `runs` by model, effort level, and
role across sources (a run carries the `effort` it launched at). Do not
average the per-source `groups` rates. Write `field.<role>.<level>` only
for a registry role with at least five graded runs at that level, or at
least ten findings for a review role. Each block records `as_of`, `since`, `runs`, the
accepted, amended, rejected, and blocked counts, `verify_pass`, the reviewer
`held` and `findings` when applicable, and medians of `active_s`, total
tokens, and `cost_usd`. Append one dated `evidence.md` line naming the run
log and transcript sources for each block changed.

Proposals from field results (thresholds are a starting point):

- Order: by accepted over graded runs, blocked left out of the denominator;
  within ten percentage points, the lower median `active_s` first. Keep
  calibration order unless every model in the role's fit set has enough
  graded runs for the comparison.
- Removal: amended plus rejected reaches 40% of at least five graded runs,
  or a reviewer's held share is below 50% of at least ten findings.
- A model seen in a role without a calibration result for it becomes a
  calibration candidate. A `judgment: true` role's result is
  `local.review-unit` (`review-seam`'s is `local.review-seam`),
  `execute-sensitive`'s is `local.sensitive`, and every other role's is
  `local.<role>`.

Field evidence can order a fit set or support a removal proposal; entry
requires a calibration result. Step 5 asks before applying any proposed
fit-set change.

## 4. Calibrate on the repository (optional, costs money)

Public numbers do not show how a model does on this Go tree with race tests
and the linter. State the lanes and the expected spend per lane from the
registry prices, and ask the user which lanes to run. A prepaid pool still
consumes its window. Calibrate models flagged by field runs as missing a
result first. Load `references/calibration.md` before running a lane: it
holds the fixed tasks, the `bench.sh` command, grading, and the `local`
record to write.

A full run sweeps effort. Every model a signed-in pool serves runs the
calibration ladder (the simple, medium, complex, integration, and sensitive
execute tasks and the unit and seam review tasks) once per level in its
`effort` list, and once with no level when the list is empty. A single-model check sweeps that
model the same way. Lanes on one pool may overlap. Take cost from each
lane's own CLI figure then, since the pool meter cannot be split.

`bench.sh` records `served_model`, `downgraded`, and `refused` on every
lane. Read them before grading a sensitive lane: a `refused` lane or one
whose `served_model` is not the model asked for is not a pass, whatever its
exit code, and its record carries the refusal or the model that answered so
the report can name it. A downgrade or refusal on the sensitive task is the
measured basis for the model's `refusal_cyber`; keep the web-reported value
only until a lane replaces it.

Entry to a role's fit set requires a calibration result; a public benchmark
cannot change a fit set. A model may pass the simple task and fail the
complex one; record every tier and let the report show the spread rather
than collapsing it to one verdict.

## 5. Write and report

Update the machine-wide registry: `as_of`, changed fields, fit sets.
`delegate` takes the first fitting entry of a role's `fit` list after
filtering hot and busy pools, so order decides routing. Build each list in
two passes.

1. Pick each model's level. Consider only levels at or above the role's
   `min_effort`. Among those at which the model passed the role's task
   (every acceptance test and the package check for `execute`, the known bug
   found for a review), take the cheapest. Take a costlier level only when
   it passes more runs or, on a review task, finds more valid extras. Each
   role reads the task that measures it: `execute` from `local.execute` (the
   medium task), `execute-sensitive` from `local.sensitive`, `review-seam`
   from `local.review-seam`, and every other `judgment: true` role from
   `local.review-unit`. `local.simple`, `local.complex`, and
   `local.integration` are the difficulty spread; they do not by themselves
   place a model, but a model that fails `local.complex` or
   `local.integration` does not lead an `execute` fit set. Write the entry as `<model>@<level>`, or as the bare
   model id when its `effort` list is empty (its result sits under `none`).
   A bare id of a model with levels routes at the role's `effort`, which is
   the level for a model not yet swept.
2. Order the entries. A `judgment: true` role (planning, research,
   verdicts, adversarial reads) orders by result first: the review task's
   known bug found, then valid extras, then cost. `execute-sensitive` drops
   any model whose sensitive lane `refused` or `downgraded`, then orders the
   rest by cost. Every other role orders by cost, with median wall time
   deciding costs within 25% of each other.

Fit sets are ordered by field success when every member has enough
evidence, with median active time breaking close results. Keep calibration
order otherwise, and keep the registry's role-order header accurate. When the project file overrides a field
this run changed, name the override in the report: the project keeps
routing on its own value.

An opencode model is pinned by its `pool_id` on the launch line and needs
nothing in `~/.config/opencode/opencode.json`; do not add agent profiles
there, since a profile that pins a model gets no system prompt.

Never apply a change to a role's `fit` membership or order silently. Report
field-driven proposals separately from calibration-driven ones, with the
commands run, every changed field and its evidence, and anything a source
refused to answer. Then ask the user (`AGENTS.md`, Agent behavior) per
proposed membership or order change whether to apply it.
