---
title: A script move must audit inline imports
date: 2026-10-07
last_verified: 2026-10-07
category: conventions
module: tools/scripts/skills/delegate
problem_type: bug
component: workflow-skills
severity: medium
applies_when:
  - "Moving Python script modules and updating callers in shell scripts or Markdown command examples."
related_components: [drive, delegate]
tags: [skills, migration, python, shell]
symptoms:
  - "A documented inline Python command fails with ModuleNotFoundError after its imported module moves."
root_cause: "The caller search required a .py suffix, while inline programs imported the module by name."
resolution_type: code_fix
---

Search for the module name and its old directory when moving a Python script.
A filename search misses inline programs that import the module without its
`.py` suffix. Execute the replacement through the entry point its caller uses.

## Evidence

The uv scripts Phase 2 plan's U6 check (2026-10-05, retired with its
land; its text at `7d2d5e46`) required a filename suffix:

```text
(model_check|runlog|catalogue|field|merge-check|plan-state|plan-queue|plan-deviations|plan_record|check-prose|check-plan-status|check-test-integrity|ledger|test_[a-z_]+)\.py
```

The old caller in `.agents/skills/drive/SKILL.md`, removed by `cfce3311`,
had neither `runlog.py` nor another filename that pattern matched:

```bash
base=$(python3 -B -c 'import sys; sys.path.insert(0, ".claude/skills/delegate/scripts"); import runlog; print(next(e["base"] for e in runlog.read() if e.get("event") == "start" and e["run"] == sys.argv[1]))' "$run")
```

On 2026-10-07 on darwin/arm64, its import prefix fails in the current tree:

```text
$ python3 -B -c 'import sys; sys.path.insert(0, ".claude/skills/delegate/scripts"); import runlog'
ModuleNotFoundError: No module named 'runlog'
```

The replacement at `.agents/skills/drive/SKILL.md:100` dispatches a registered
query:

```bash
base=$(uv run --quiet tools/scripts/run.py delegate runlog start-base --run "$run")
```

`tools/scripts/tests/skills/delegate/test_runlog.py:161` executes that query
against a fixture log and pins the first matching start's base:

```python
result = self.command("start-base", "--run", "r1")
self.assertEqual(result.returncode, 0, result.stderr)
self.assertEqual(result.stdout, "abc123\n")
```

The same fix replaced inline imports in
`.agents/skills/delegate/references/review-lanes.md` with `executors` and
`writers` queries (`cfce3311`). Their behavioral tests are at
`tools/scripts/tests/skills/delegate/test_runlog.py:198` and `:221`.

## Apply it

For a move of `runlog.py`, inspect both module references and path injection:

```bash
rg -n 'runlog|sys\.path|python3.*-c|delegate/scripts' .agents docs tools
uv run --quiet tools/scripts/run.py test tools/scripts/tests/skills/delegate
```

This covers callers embedded in shell and documentation. It does not establish
that every registered command's body runs, or that a compile-only check proves
behavior.
