---
title: A Shell-to-Python Port Changes How Empty Variables and Carriage Returns Behave
date: 2026-10-06
category: conventions
module: src/protocol/smi/bench
problem_type: convention
component: repository-scripts
severity: medium
applies_when:
  - "Porting a shell script that reads `${NAME:-default}` to Python, where a caller may export the variable empty."
  - "Porting a shell pipeline that reads a child process's output to Python's `subprocess` in text mode and splits it into lines."
  - "Reviewing a ported script whose tests only cover the unset variable and the plain line feed."
related_components: [uv-scripts, bench-gate, lab-scripts]
tags: [python, shell-port, environment, subprocess, text-mode]
---

Two defaults of Python differ from the shell file a port replaces, and a test
that sets a variable or feeds output the obvious way passes on both.

## Empty is not unset

`${NAME:-default}` takes the default when `NAME` is unset or empty.
`os.environ.get("NAME", "default")` takes it only when `NAME` is unset:

```text
$ sh -c 'X=; echo "[${X:-d}]"'
[d]
$ X= python3 -c 'import os; print(repr(os.environ.get("X", "d")))'
''
```

A caller that writes `MIN_DELTA= make bench-gate` got the default from the
shell file and an exit 2 from the port. Read the variable with `or`:

```python
min_delta_text = os.environ.get("MIN_DELTA") or "1"
```

The same read is wrong where the shell file used `${NAME-default}`, so open
each original line and check which form it used.

## Text mode rewrites `\r`

`subprocess.run(..., text=True)` reads with universal newlines, so a lone
`\r` and a `\r\n` both become `\n` before the script sees them. `grep` and
`awk` in the shell file split on `\n` only. Measured 2026-10-06 on
darwin/arm64:

```text
$ python3 -c 'import subprocess; r = subprocess.run(["printf", "a\\rb\\r\\nc"], stdout=subprocess.PIPE, text=True); print(r.stdout.split("\n"))'
['a', 'b', 'c']
```

The shell file would have seen two lines, `a\rb\r` and `c`. A benchmark row
that follows a `\r` on its line was therefore compared by the port and
dropped by `grep`. Read bytes, decode them, and split on `"\n"` yourself:

```python
raw = run.stdout.decode("utf-8")
kept = [line for line in raw.split("\n") if KEPT_LINE.match(line)]
```

`str.splitlines()` is the same defect, since it also breaks on `\r`, `\x1c`,
and others.

## Evidence

- `src/protocol/smi/bench/bench-gate.py:125-130`: every defaulted variable
  is read with `or`. `RAW_IN` keeps `get(..., "")` because its default is
  empty.
- `src/protocol/smi/bench/bench-gate.py:142-155`: output is read as bytes
  and split on `"\n"`.
- `src/protocol/smi/bench/gate_test.go:296`
  (`TestGateTakesTheDefaultForAnEmptyVariable`): sets `MIN_DELTA=` and
  `BASELINE=` and expects exit 0.
- `src/protocol/smi/bench/gate_test.go:356`
  (`TestGateReadsLinesByLineFeedOnly`): hides a regression behind
  `junk\r` and expects the gate not to see it.
- `deploy/lab/write-openfga-store.py:77-78` and
  `src/services/device/test/integration/lab_scripts_test.go:584`
  (`TestTheLabStoreScriptDefaultsAnEmptyHTTPEndpoint`): the same rule in a
  second script, found after the first fix.
- Commits `448eee4f` and `02cbe848` carry the failing output of each test
  against the unfixed read.

## How to apply

When a port lands, give every defaulted variable a test that exports it
empty, and give every line-oriented reader a test with `\r` inside a line.
Probe the original tool on the host where the shell file and the port
disagree (`448eee4f` did so for awk) instead of reasoning about it.

## What this does not cover

A required argument that is empty is a different case: `02cbe848` rejects
it through argparse. The bytes rule does not apply where the shell file
itself read the stream in a mode that translates line ends.
