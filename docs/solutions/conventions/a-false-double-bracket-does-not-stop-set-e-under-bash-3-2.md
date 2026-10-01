---
title: A False [[ ]] Does Not Stop a set -e Script Under macOS Bash 3.2; End Each Assertion in || fail
date: 2026-10-01
category: conventions
module: tools/hooks/tests
problem_type: convention
component: hook-tests
severity: high
applies_when:
  - "Writing or reviewing a shell test, hook, or script under tools/ that relies on `set -e` to stop at a false `[[ … ]]`."
  - "A shell test passes and you are deciding whether its assertions can fail at all."
  - "A shell script kills a background job and its stderr is compared against an expected message."
  - "Running a shell test that kills child processes with `pkill` or lists them with `ps` inside the Claude Code sandbox."
related_components: [conformance-gates, verify-change]
tags: [bash, shell, set-e, hooks, silent-failure, sandbox]
---

`#!/usr/bin/env bash` resolves to `/bin/bash` on the macOS hosts this
repository runs on, and that is GNU bash 3.2.57. There a `[[ … ]]` that
evaluates false does not trigger `set -e`, at top level or in a loop body.
A false `test` does. Measured 2026-10-01 on arm64-apple-darwin25:

```text
$ /bin/bash -c 'set -e; [[ a == b ]]; echo reached'; echo rc=$?
reached
rc=0
$ /bin/bash -c 'set -e; test a = b; echo reached'; echo rc=$?
rc=1
```

A script whose assertions are bare `[[ … ]]` lines therefore passes
whether they hold or not. In `tools/hooks/tests/run.sh` over a hundred of them did.
Once they could fail, one had been false all along: the OpenTelemetry
wrapper's timeout case expected one line of stderr and got two (below).

## How to apply

End every assertion in an explicit failure, and make the script refuse a
bare one so a later edit cannot reintroduce it:

```bash
fail() {
  echo "assertion failed at line $1" >&2
  exit 1
}
[[ $rc -eq 1 ]] || fail "$LINENO"
```

`tools/hooks/tests/run.sh:46` defines `fail`. Lines 51 to 64 grep the
script's own file for a bare `[[ … ]]` statement and fail on a match, and
fail as well when grep cannot read the file (exit 2, as under
`bash < run.sh`), so the check never skips silently. Line 66 probes
`fail` itself before any fixture work. `[[` is a keyword, so a helper that takes the test as
arguments cannot evaluate it. `test` lacks the `==` pattern match the
suite uses.

## Bash prints a killed job's status on its own stderr

A script that kills a background job prints a line like
`script.sh: line 40:  97731 Terminated: 15  docker info > /dev/null 2>&1`
when bash reaps the job. The line goes to the shell's stderr at reap
time, so `kill … 2>/dev/null` does not stop it. Take the job out of the
job table before killing it, as `tools/test/service-otel-integration.sh:31`
does, or discard stderr around the whole kill-and-reap block:

```bash
disown "$docker_probe" 2>/dev/null || true
kill "$docker_probe" 2>/dev/null || true
```

Observed 2026-10-01 under bash 3.2.57 with a stub `docker` that sleeps:
without either, the wrapper printed the notice ahead of its message, and
the case "service OpenTelemetry wrapper gives up on a Docker daemon that
does not answer" in `tools/hooks/tests/run.sh` failed once assertions
could fail. Wrapping the block in `{ … } 2>/dev/null` printed only the
message in five of five runs. Keep `|| true` on the first `kill` as well:
the probe can exit between the liveness check and the kill.

## In the Claude Code sandbox, ps and pkill see nothing

Inside the sandbox, `ps` fails with `operation not permitted`, and
`pkill -P <pid>` matches no process. `idle_run` in
`tools/hooks/tests/run.sh:756` kills a timed-out hook's children with
`pkill -9 -P` before the hook, because a `$(cat)` child holds the FIFO
that `exec 3<>` (line 740) opened for reading and writing, and so never
sees end of input. A sandboxed run that hits that timeout leaves the `cat`
orphaned under pid 1. The verifier runs unsandboxed and leaves none. Run
a mutation that is meant to hit the timeout unsandboxed, or look for and
kill the orphan afterwards.

## What this does not cover

- How newer bash releases treat a false `[[` under `set -e` is unverified
  here. A script that also runs on Linux still needs `|| fail` for macOS.
- The self-check matches a line that is a `[[ … ]]` statement, alone or
  followed by `&&` or a comment. `[[ … ]]; ok` and an assertion continued
  across lines pass it.
- `if [[ … ]]; then exit 1; fi` and `jq -e` assertions already fail and
  need no change.
