---
title: A Shell-to-Python Port Changes How Empty Variables, Carriage Returns, and Word Splitting Behave
date: 2026-10-06
last_verified: 2026-10-09
category: conventions
module: src/protocol/smi/bench
problem_type: convention
component: repository-scripts
severity: medium
applies_when:
  - "Porting a shell script that reads `${NAME:-default}` to Python, where a caller may export the variable empty."
  - "Porting a shell pipeline that reads a child process's output to Python's `subprocess` in text mode and splits it into lines."
  - "Reviewing a ported script whose tests only cover the unset variable and the plain line feed."
  - "Porting an unquoted `for tok in $cmd` or other shell word expansion (globs, brackets, names that hold a line feed) to Python."
  - "Writing a matrix test whose expected side calls the function under test or the code it shares."
related_components: [uv-scripts, bench-gate, lab-scripts, hooks]
tags: [python, shell-port, environment, subprocess, text-mode, word-splitting, glob]
---

Three defaults of Python differ from the shell file a port replaces, and a test
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

## An unquoted expansion is bash's, so ask bash

`worktree-guard.sh` iterated `for tok in $cmd`, which splits on blanks and
line feeds and replaces each pattern word with the names it matches. Two
ports rebuilt that in Python and both diverged. `glob.glob` reads `[^a]` as
the set `^` and `a`, bash reads it as "not `a`". Splitting the output of
`printf "%s\n" $1` on line feeds cuts a tracked `a\nb.tmp` into the words
`a` and `b.tmp`, which `git ls-files` does not list, so `rm *.tmp` was
allowed. Measured 2026-10-09 on darwin/arm64 in a directory holding `a.tmp`
and `b.tmp`:

```text
$ /bin/bash -c 'for t in $1; do printf "[%s]" "$t"; done' _ '[^a].tmp'
[b.tmp]
$ python3 -c "import glob; print(glob.glob('[^a].tmp'))"
['a.tmp']
```

Run the loop itself, end each word with a NUL, and split the output on NUL.
No file name holds a NUL, and the command travels as a positional argument,
so nothing in it is evaluated. A command that holds a NUL cannot be an
argument, so `subprocess.run` raises `ValueError`, which the caller must
handle.

When the guard cannot judge an operand (`bash` missing or a non-zero exit),
the function returns `None` and both callers keep the denial. An empty word
list would read as "nothing to check" and allow the command.

## A matrix needs literal rows

A test that compares `words(p, d)` with a helper running the same loop
cannot fail on what the two share. Keep the matrix, since it covers the
patterns, and add a table of literal expected lists beside it.

## Evidence

- `tools/scripts/hooks/worktree_guard.py:214-238`: `words`, the loop, the
  NUL split, and the `None` returns. `:260` and `:283`: both callers.
- `tools/scripts/tests/hooks/test_worktree.py:723`: the matrix against
  `loop_words` (`:714`). `:733`: the literal rows for `a\nb.tmp`,
  `c\rd.tmp`, a pattern with no match, and an empty command. `:753`: `None`
  for a missing `bash` and for one that exits 9.
- Commits `d4c9891f` and `3cdc9f28` carry the two replaced forms, `d6ea0d66`
  the loop. The plan's Decisions (phase 3) hold the probe table.

### Empty variables and carriage returns

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
it through argparse. A shell file that quotes its expansion has no word
splitting to port. The bytes rule does not apply where the shell file
itself read the stream in a mode that translates line ends.
