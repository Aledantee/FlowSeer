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
no catalogue request or calibration lane. `all` includes the field step
before calibration; `discover` and `catalogue` keep their named scope.

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

For each new or changed model, one web pass for: the vendor's launch note,
Terminal-Bench 2.1 and SWE-bench Verified with the harness named, and refusal
reports for security tooling. Append to `evidence.md` as `model — claim —
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
cannot receive a field block. Aggregate `runs` by model and role across
sources; do not average the per-source `groups` rates. Write `field.<role>`
only for a registry role with at least five graded runs, or at least ten
findings for a review role. Each block records `as_of`, `since`, `runs`, the
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
- A model seen in a role without `local.<role>` becomes a calibration
  candidate.

Field evidence can order a fit set or support a removal proposal; entry
requires a calibration result. Step 5 asks before applying any proposed
fit-set change.

## 4. Calibrate on the repository (optional, costs money)

Public numbers do not show how a model does on this Go tree with race tests
and the verifier. State the lanes and the expected spend per lane from the
registry prices, and ask the user which lanes to run; a prepaid pool still
consumes its window. Calibrate models flagged by field runs as missing
`local.<role>` first. Load `references/calibration.md` before running a
lane: it holds the fixed task, the `bench.sh` command, grading, and the
`local.<role>` record to write.

Entry to a role's fit set requires a calibration result; a public benchmark
cannot change a fit set.

## 5. Write and report

Update the machine-wide registry: `as_of`, changed fields, fit sets. Keep
each role's `fit` list best-first by calibrated speed, then pool usage,
among models that passed; `delegate` takes the first fitting model in that
order after filtering hot and busy pools. Keep the registry's role-order
header accurate: fit sets are ordered by field success when every member
has enough evidence, with median active time breaking close results, and
keep calibration order otherwise. When the project file overrides a field
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
