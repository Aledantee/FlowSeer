---
title: Panic Policy Phase 4 - The Goroutine Boundary - Plan
type: refactor
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-15-1020-refactor-panic-policy-plan.md
---

# Panic Policy Phase 4 - The Goroutine Boundary - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A panic inside a spawned goroutine stops that unit of work and is reported,
instead of taking the process down. The means: one supervised-spawn helper
under `src/common/`, and every `go` statement in non-test `src/` routed through
it.

This phase is wrong if the helper cannot carry what the call sites need. The 64
spawn sites do different things — some run for the process lifetime, some per
connection, some per work item — and a helper that fits only the simplest of
them would be adopted at the easy sites and worked around at the rest, which is
worse than none.

## Decisions

The parent's Decisions govern. What is decided here:

- The helper lives under `src/common/`, alongside `errs`, `pump`, `secret` and
  `service`. Why: it is a cross-cutting foundation with no domain types, which
  is what `AGENTS.md` says that directory holds. It is not part of
  `src/common/service`, because `src/protocol/` packages spawn goroutines
  without running under a service host — `src/protocol/snmp/watcher.go:632` and
  `src/protocol/smi/load.go:305` are both outside it.
- The helper recovers and reports; it does not restart. Why: a restart policy
  belongs to the supervisor that owns the work, and a helper that retries would
  turn a crash into a loop at 64 sites with no owner deciding it.
- The two sites that already do this by hand are the specification, not
  candidates for conversion first. `src/protocol/snmp/watcher.go:632` latches
  the panic as a terminal error for the watch; `src/protocol/smi/load.go:305`
  attributes it to the file being read and returns it as a read failure. Both
  turn a process-wide crash into a failure of one unit of work, and the helper
  has to express both shapes before anything is migrated onto it.

What is not decided, and is this phase's real work: the helper's signature.
The candidates differ in what the goroutine returns and who reads it, and that
is a question about the 64 call sites rather than about the helper.

## Requirements

Parent requirement 3 applies. Restated:

1. Every goroutine running first-party work in non-test `src/` is launched
   through the helper. Acceptance: a bare `go func() { … }()` in non-test `src/`
   is a violation; the count of `go` statements outside the helper reaches zero.
2. A panic inside a spawned goroutine is reported as a structured error
   naming the unit of work, and the process survives. Acceptance: a test that
   panics inside a helper-launched goroutine observes the error and a running
   process.
3. The helper expresses both existing shapes. Acceptance:
   `src/protocol/snmp/watcher.go:632` and `src/protocol/smi/load.go:305` are
   rewritten onto it without losing the attribution each does today.

## Out of scope

- `_test.go` goroutines.
- The recover sites that contain foreign code:
  `src/common/service/{delivery,supervisor,gate}.go`. They are clause 2's
  foreign-code answer and are not goroutine boundaries.
- Restart and backoff policy.

## Units

To be written when this phase is re-planned. The shape is one unit for the
helper and its tests, then one unit per package cluster for the migration —
`src/modules/capture` (4 sites), `src/modules/localnet` (6),
`src/protocol/snmp` (8), `src/services/device` (3), and the rest. The
migration units are independent of each other and all depend on the helper.

## Verification

```bash
go test -race ./...
cd src/edge/netpen && go test -race ./...
```

The count that closes the phase, which must reach zero outside the helper:

```bash
grep -rn --include='*.go' -E '^\s*go (func|[a-zA-Z])' src/ | grep -v '_test.go'
```

`-race` matters more here than elsewhere: routing a goroutine through a helper
changes when its closure captures, and a migration that introduces a data race
is the likeliest way this phase does harm.

## Definition of done

- [ ] The `go`-statement count outside the helper is zero in non-test `src/`.
- [ ] A panic in a helper-launched goroutine is reported and the process
      survives, proved by a test rather than asserted.
- [ ] `watcher.go` and `load.go` are on the helper with their attribution
      intact.
- [ ] Verifier green for every changed path, per module.
- [ ] This plan's `status` set, and `U2. Landed:` filled in the parent.

## Open questions

- The helper's signature. Does the goroutine return an `error` the helper
  reports, or does the helper hand it a callback? Do long-lived spawns and
  per-item spawns share one entry point? This is the phase's first unit and
  the reason it is `needs-decisions`.
- Whether the 64 sites are really one population. If a third of them turn out
  to be fire-and-forget with no reader for the error, the helper needs a second
  shape and the migration units get larger.
- Whether `src/common/pump` already owns part of this. It has one `go`
  statement and is the repository's existing concurrency primitive; the helper
  may belong beside it or inside it.
