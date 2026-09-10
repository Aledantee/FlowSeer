---
name: tune
description: Refresh the model registry that `delegate` routes on. Discovers which agent CLIs and prepaid pools this host can reach, pulls live model catalogues and prices, records external evidence, optionally calibrates candidate models on a fixed repository task, and writes `.claude/models/registry.yaml` plus the host-local pool file. Use when asked to tune, when a new model or CLI appears, when `delegate` warns the registry is stale, or before a large plan is dispatched. Not for choosing a model for one task; `delegate` does that from the registry.
argument-hint: "[discover | catalogue | calibrate <lane>... | all]"
---

# Tune the model registry

The registry (`.claude/models/registry.yaml`) is the routing table: pools,
models with price, context, effort levels and refusal posture, and the fit
set per role. `delegate` never names a model; it names a role and resolves it
here. This skill keeps the registry true. Every number it writes carries a
source and a date in `.claude/models/evidence.md`; a number without one is
an opinion and does not go in.

Inputs: the current registry, the network, the installed CLIs. Completion: the
registry's `as_of` is today, `host.local.yaml` is regenerated, and the report
names every field that changed with its evidence. Failure: a step that cannot
reach its source says so and leaves the previous value with its old date; it
never guesses.

## 1. Discover the host

```bash
.claude/skills/tune/scripts/discover-host.sh > .claude/models/host.local.yaml
```

Writes which CLIs exist, which pools are signed in, what Orca can pin with
`--model`, the `opencode` model ids split by `opencode-go/` (prepaid) and
`opencode/` (per-token), and the Claude rate-limit windows. The file is
gitignored: it describes this machine. Run this step on every invocation.

## 2. Pull live catalogues

```bash
python3 .claude/skills/tune/scripts/catalogue.py .claude/models/registry.yaml
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

## 4. Calibrate on the repository (optional, costs money)

The public numbers do not say how a model does on this Go tree with race
tests and the verifier. `references/calibration.md` describes the fixed
task, the acceptance tests, and the lanes. Before running, state the lanes,
the expected spend per lane from the registry prices, and wait for
approval; a prepaid pool still consumes its window. Then, per lane:

```bash
.claude/skills/tune/scripts/bench.sh --lane <name> --cli <claude|codex|agy|opencode> \
  --model <id> [--effort <level>] [--agent <opencode agent>] \
  --brief <brief file> --dir <worktree> --out <json>
```

The script records wall time, the CLI's reported usage, and the exit code.
Grade each lane with the acceptance tests and the verifier, and write
`local.<role>: {pass, wall_s, cost_usd}` on the model. A model enters or
leaves a role's fit set only on a calibration result, never on a benchmark.

## 5. Write and report

Update the registry: `as_of`, changed fields, fit sets. Keep the opencode
agent block in `~/.config/opencode/opencode.json` in step with
`opencode_agents`. A change to a role's fit set is proposed in the report,
not applied silently, so a person sees which default moved and why. Report
the commands run, each changed field with its evidence, and anything a
source refused to answer.
