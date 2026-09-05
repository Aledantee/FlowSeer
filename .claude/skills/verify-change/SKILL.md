---
name: verify-change
description: Run FlowSeer's diff-aware format, lint, build, race-test, protobuf, hook, and configuration gates. Use after changing Go, protobuf, Claude hooks or settings, and before reporting implementation complete or preparing a commit.
---

# Verify FlowSeer Change

Run the repository verifier from the worktree root. It selects checks from the
changed paths and maps Go files to their nearest module.

```bash
.claude/skills/verify-change/scripts/verify-change.sh
```

Use a base ref when verifying every change on the current branch, including
committed work:

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base master
```

Use explicit paths after `--` to verify a narrow scope without including other
worktree changes:

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- tools/hooks/protect-generated-bash.sh
```

Use `--full` for all Go modules, protobuf sources, and Claude configuration. This
is intentionally slower. Finish substantive cross-module or schema changes with
this gate; a targeted receipt is sufficient for narrow documentation, hook, or
single-module work.

```bash
.claude/skills/verify-change/scripts/verify-change.sh --full
```

Full verification and telemetry-sensitive paths run the Docker-backed service
OpenTelemetry tier through `tools/test/service-otel-integration.sh`. The selected
paths are service Go sources, its Collector integration sources and fixture,
the wrapper, and the root `go.mod` or `go.sum`. Docker is a required tool for
these scopes; an unavailable daemon is a failed gate.

Policy fixtures can inspect that decision without running any gate:

```bash
.claude/skills/verify-change/scripts/verify-change.sh --print-selection -- \
  src/common/service/telemetry_config.go
```

`--print-selection` emits `service_otel_integration=true` or `false` and exits
before creating temporary build state, checking tools, starting Docker, running
hook tests, or updating verification markers and receipts. An explicit empty
path set after `--` is valid in this mode and reports `false`; it remains an
error during normal verification.

Treat a missing required tool as a failed gate. Do not silently replace a failed
race test with a non-race test or skip lint. Fix the failure or report the exact
blocked command and reason.

A successful run records its scope in the current worktree's git metadata and
clears matching dirty markers. No completion hook enforces the receipt; running
the verifier before handoff is part of the work sequence in `AGENTS.md`.

Do not run this verifier against unrelated dirty files. Pass explicit paths when
the worktree contains user-owned changes outside the current task.
