---
title: A Go fixture is a .go.txt file or an exact-match edit, never a committed patch file
date: 2026-10-12
last_verified: 2026-10-12
category: conventions
module: .agents/skills/review/evals
problem_type: bug
component: workflow-skills
severity: medium
applies_when:
  - "Adding a skill evaluation scenario, a calibration task, or any other committed fixture that seeds a change into Go source."
  - "The verifier fails in its git diff gate with \"space before tab in indent\" on a .patch or .diff file."
related_components: [steer, tune, verify-change]
tags: [skills, evaluation, fixtures, whitespace, verifier]
symptoms:
  - "verify-change.sh --base main fails in gate: git diff, naming a fixture.patch line with \"space before tab in indent\"."
root_cause: "A unified diff prefixes every context line with a space, so a tab-indented Go context line starts with a space before a tab, which git diff --check reports."
resolution_type: replace-fixture-format
---

# A Go fixture is a .go.txt file or an exact-match edit, never a committed patch file

## Situation

A scenario or calibration task seeds a defect or a new file into Go source
inside a throwaway worktree. Storing that change as a `git diff` output
file and applying it with `git apply` looks like the smallest fixture.

## What is true

A unified diff writes one leading character per line: `+`, `-`, or a space
for context. Go is indented with tabs, so every context line inside a
function begins with a space followed by a tab. `git diff --check` reports
that as "space before tab in indent", and the verifier runs it on every
changed path:

```bash
# .agents/skills/verify-change/scripts/verify-change.sh:989-993
if [[ $full == true ]]; then
  run git diff --check
else
  run git diff --check "$base" -- "${paths[@]}"
fi
```

Exempting the patches with a whitespace attribute would be a suppression
for the change's own files, which `AGENTS.md` (Hard boundaries) rules out.

The default base is `HEAD` (`verify-change.sh:44`), so a path-scoped run
after the fixture is committed checks an empty diff and passes. The failure
shows only at the Finish run with `--base main`.

## How to apply

Use one of two forms:

- A new Go file: commit it beside the scenario as `<name>.go.txt` and copy
  it in from `setup.sh`. Calibration keeps its accept files this way
  (`.agents/skills/tune/references/calibration.md:27-29`), and the `.txt`
  suffix also keeps Go tools from building it in place.
- A change to an existing file: remove or replace exact text in `setup.sh`,
  writing tabs as `\t` escapes so the script has none, and fail when a
  target matches other than once. A drifted source then fails setup, which
  the runner reports as `stale`, as a patch that no longer applies would.

```bash
# .agents/skills/review/evals/merge-deadlock/setup.sh (excerpt)
removals = [
    "\tstopForwarders := make(chan struct{})\n",
    ...
]
for removal in removals:
    if text.count(removal) != 1:
        sys.exit(f"{path}: expected one match for {removal!r}")
    text = text.replace(removal, "")
```

`.agents/skills/review/evals/branch-verdict/setup.sh` shows the copy form.

## Evidence

Commit `59d31183` replaced three `fixture.patch` files that the Finish run
rejected. A scratch worktree compared `git write-tree` after each old patch
with the tree of the new setup commit, and all three matched, so the fixtures
seed the same code.

## What it does not cover

A patch of a file with no leading tabs (Markdown, YAML) passes the check.
The rule is about committed fixtures, not patches piped through a pipeline
and never committed.
