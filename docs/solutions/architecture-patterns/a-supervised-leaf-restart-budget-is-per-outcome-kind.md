---
title: A Supervised Leaf's Restart Budget Is Set Per Outcome Kind, So Raising Only the Error Budget Leaves Panics Able to End the Service
date: 2026-10-16
category: architecture-patterns
module: src/common/service
problem_type: bug
component: service_layer
severity: high
symptoms:
  - "A leaf that must restart forever survives repeated returned errors, but service.Run returns once the same leaf has panicked a few times within a minute"
  - "The returned error reads `outcome restart budget exhausted: ... runner panic` for a leaf whose Policy sets a large Error budget"
root_cause: "Policy keeps one RestartBudget per outcome (Normal, Error, Panic). Setting Policy.Error.Budget leaves Policy.Panic at the default of 3 restarts per minute, and an exhausted leaf budget is returned up the tree as the branch's own failure."
resolution_type: code_fix
applies_when:
  - "Giving a supervised leaf an unbounded or very large restart budget so that backoff, not a counter, limits restarts"
  - "Writing a test that a leaf keeps restarting under a persistent failure and the rest of the service keeps running"
  - "Deciding which outcome kinds (error, panic) a restart policy must cover"
related_components: [edge-agent]
tags: [supervision, restart-budget, panic-policy, service-run, outcome-policy]
---

# A Supervised Leaf's Restart Budget Is Set Per Outcome Kind

## Situation

The edge agent runs one delivery drain per log. A drain must restart for as
long as the log's storage or the send path keeps failing, and a failure in one
log must not stop the other. `deliveryAssembly.module` first set only
`Policy.Error` to a machine-sized budget. A send that panicked then ended
`service.Run` after three restarts.

## What is true

- `Policy` holds three independent `OutcomePolicy` values, each with its own
  budget and backoff state (`src/common/service/policy.go:74-90`).
- An outcome left at its zero value takes the defaults, 3 restarts per
  minute (`policy.go:10-11`, `policy.go:134`). Only the Normal, Error, and
  Panic values you write are replaced.
- `decide` consumes the budget of the result's own outcome and returns
  `outcome restart budget exhausted` when it is spent
  (`src/common/service/supervisor.go:235-237`). That error is the leaf's
  failure, so the owning branch fails with it even when the branch's own
  `Intensity` is generous.

## How to apply

Give every outcome the leaf must survive the same budget. Reuse one value so
the two cannot drift:

```go
budget := service.RestartBudget{Max: math.MaxInt, Window: time.Minute}
Policy: service.Policy{
	Error: service.OutcomePolicy{Budget: budget},
	Panic: service.OutcomePolicy{Budget: budget},
}
```

`src/edge/agent/host/delivery.go:64-67` is the working example.

Test the claim past the default window. `TestDeliveryPanicStaysInItsBranch`
(`src/edge/agent/host/delivery_test.go:70`) panics on every send, advances a
synctest clock by four minutes, and asserts that `service.Run` has not ended
and that at least eight attempts ran. Before the `Panic` budget was set it
failed with `service.Run ended during persistent drain panic: ... outcome
restart budget exhausted` (the red run of this test on 2026-10-16, macOS).

## Does not cover

Whether an unbounded panic budget is the right policy for a leaf. That is a
design decision for the owning plan, not a property of the runtime.
