---
title: A Panic-to-Error Conversion That Reaches an API the Unit Did Not Scope Is a Re-Plan, Not a Wider Diff
date: 2026-09-18
category: architecture-patterns
module: src/common/netsim/vswitch
problem_type: architecture_pattern
component: refactor
severity: medium
applies_when:
  - "Converting a sanctioned panic to an error return, or otherwise adding an error return to a function that had none, as a bounded refactor unit, and tracing where the new error must be handled."
  - "A local un-erroring change's propagation reaches a core or exported API the unit did not name — a switch's Forward/Peek, a jen closure that cannot return an error, a variadic forwarder over a public Raise."
  - "Choosing between threading a new error up through many callers and containing the failure where it arises."
  - "A refactor unit's diff keeps growing because each converted site forces its callers to change too."
related_components: [netsim, mibgen, panic-policy]
tags: [error-handling, refactor-scope, panic-policy, blast-radius, api-boundary]
---

The panic-policy refactor
([`docs/architecture/2026-09-15-supervised-goroutine-spawn-direction.md`](../../architecture/2026-09-15-supervised-goroutine-spawn-direction.md),
the rule in [`docs/code-style.md`](../../code-style.md) Panics) converts each
unsanctioned panic to something clause-compliant. The obvious conversion —
return an `error` instead of panicking — is the one that most often broke,
because a new `error` return is not a local change: it widens every caller up
to the first frame that can handle it. The plan names this exactly: "This phase
is wrong if a unit's propagation reaches an exported API the unit does not name.
That has happened four times" — `scheduleDequeue`, `decodeNatural`,
`mustSetOperStatus`, `newOIDCall`
([`docs/plans/2026-09-15-1020-refactor-panic-policy-phase1-plan.md:30-34`](../../plans/2026-09-15-1020-refactor-panic-policy-phase1-plan.md)),
and a fifth, `diag.Raise`, later withdrawn to phase 4.

## The rule

Before converting a panic (or adding any error return) inside a bounded unit,
trace the propagation to the **first exported or core API it reaches**, naming
the *enclosing function* of every call site, not just the line. If that frame
is one the unit did not scope — a switch's forwarding API that returns no
error, a code-generation closure that cannot, a public variadic forwarder —
the error return is out of scope. Stop and re-plan the unit around a shape that
contains the failure where it arises, rather than widening the diff to chase
the error upward.

## The containing shapes

Each shape below replaced an error-threading conversion that escaped its unit.

- **Sticky fault read through a separate accessor.** When the reaching frame is
  a no-error core API, record the first failure on the receiver and expose it
  through a new method instead of returning it. `setOperStatus` runs under
  `Switch.Forward`/`Peek` (no error return); it calls `recordOperFault`, and
  callers learn of the fault through `Switch.Err`
  (`src/common/netsim/vswitch/switch.go:3543-3556`). This keeps one fault shape
  for the type rather than threading an error through `applyLAGEffects →
  interceptLACP → forward → Forward`.

- **Pre-pass that validates before any un-erroring emitter runs.** When the
  reaching frame cannot return an error (here, `jen` closures), validate the
  whole input once, up front, so the later call is provably safe.
  `renderModule` runs `validateEmittedOID` over every node before any emitter,
  so `newOIDCall`'s `MustOID` cannot panic and its signature stays unchanged
  (`src/protocol/snmp/cmd/mibgen/emit.go:156-167`, `emit.go:316`). The proof is
  a test over the emitted artifact (`emit_oid_scan_test.go`), not the emitter.

- **Delete an unreachable branch instead of converting it.** A panic on a
  condition the caller's contract already forecloses is dead code; removing it
  is correct and adds no error path.
  [`docs/code-style.md:40`](../../code-style.md) bans handling errors that
  cannot occur.

- **Keep a proven panic.** A `Must`-prefixed panic whose invariant a `go test`
  scan establishes satisfies the rule already; do not degrade it to an error.
  See
  [A Static Scan Cannot Decide What a Function Does With Its Arguments](../conventions/prove-a-body-property-by-execution-not-by-ast-reading.md)
  for when the scan itself is the hard part.

## What this does not cover

It does not say error returns are wrong — most conversions thread cleanly to a
near handler. It applies only when the propagation escapes the unit's named
scope, and it does not choose among the four shapes: that is the re-plan's
work, driven by what the reaching frame can and cannot return.
