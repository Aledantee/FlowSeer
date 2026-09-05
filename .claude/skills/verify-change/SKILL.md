---
name: verify-change
description: Run FlowSeer's diff-aware format, lint, build, race-test, protobuf, hook, and configuration gates. Use after changing Go, protobuf, Claude hooks or settings, and before reporting implementation complete or preparing a commit.
argument-hint: "[--full | --base REF | -- paths]"
---

# Verify FlowSeer Change

Run the verifier from the worktree root. It selects checks from the changed
paths and, within a Go module, vets, race-tests, and lints only the packages
that can observe the change: those holding a changed file and every package
that imports one of them. The whole module still compiles.

```bash
.claude/skills/verify-change/scripts/verify-change.sh
```

Run it with the Bash sandbox disabled: the Go gates bind loopback listeners
and the telemetry tier starts Docker, and the sandbox denies both.

| Scope | Command |
| --- | --- |
| every change on the branch, committed included | `verify-change.sh --base master` |
| named paths only, ignoring other worktree changes | `verify-change.sh -- <paths>` |
| all Go modules, protobuf sources, and Claude configuration | `verify-change.sh --full` |

Finish a cross-module or schema change with `--full`; a targeted run is
enough for documentation, hook, or single-module work. Pass explicit paths
when the worktree contains changes outside the current task.

Full runs and telemetry-sensitive paths (service Go sources, its Collector
integration sources and fixture, the wrapper, root `go.mod` or `go.sum`) run
the Docker-backed OpenTelemetry tier through
`tools/test/service-otel-integration.sh`. An unavailable daemon is a failed
gate. `--print-selection -- <paths>` reports whether that tier would run
without running any gate.

A missing required tool is a failed gate. Do not replace a failed race test
with a non-race test or skip lint; fix the failure or report the exact
blocked command and reason.

A successful run records its scope in the worktree's git metadata and clears
matching dirty markers; `close` reads that receipt.
