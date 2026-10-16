---
title: A Metric Callback Registered in Leaf Setup Leaks When the Supervisor Ends the Attempt Before Its Runner Starts
date: 2026-10-16
category: architecture-patterns
module: src/common/service
problem_type: bug
component: service_layer
severity: medium
symptoms:
  - "After a restart or a cancel during startup, one metrics collection reads a log's stats more than once"
  - "An observable-instrument callback keeps running for a log whose attempt never ran"
root_cause: "Leaf.Setup runs before the attempt is admitted. When the coordinator stops accepting work between Setup and the runner, the runner never starts, so a deferred Unregister placed in the runner never runs for a callback registered in Setup."
resolution_type: code_fix
applies_when:
  - "Registering an OpenTelemetry observable-instrument callback (meter.RegisterCallback) for a supervised leaf"
  - "Pairing any acquire in Leaf.Setup with a release that lives in the Runner"
  - "Writing a test of instrument registration for a module that gets its meter from service.Meter(ctx)"
related_components: [edge-agent, observability]
tags: [opentelemetry, observable-instruments, leaf-setup, attempt-lifecycle, supervision]
---

# A Metric Callback Registered in Leaf Setup Leaks When the Attempt Ends Before Its Runner

## Situation

`deliveryAssembly.setup` registered the edge buffer callback and deferred
`Unregister` inside the returned runner. When the supervisor ended an attempt
after `Setup` and before the runner, the callback stayed registered and the
next attempt added a second one.

## What is true

- After `Setup` returns, the runner starts only if the coordinator is still
  accepting and its context is live. Otherwise the attempt returns without
  calling the runner (`src/common/service/supervisor.go:706-715`).
- A release deferred in the runner is therefore skipped on that path.
- Outside a running service, `service.Meter` returns a no-op meter
  (`src/common/service/context.go:46-48`, `:133-139`). A test that calls
  `setup` directly registers nothing and cannot show the leak.

## How to apply

Register at the start of the runner, next to the deferred release, so the pair
cannot be separated (`src/edge/agent/host/delivery.go:76-82`). Take the meter
in `Setup`, where the context carries it.

Prove both halves under `service.Run`:

- `TestDeliveryCanceledBeforeRunnerRegistersNoBufferCallback`
  (`src/edge/agent/host/delivery_test.go:237`) cancels inside `Setup` and
  expects zero stats reads on collection. With registration still in `Setup`
  it reported `stats reads after skipped runner = 1, want 0`.
- `TestDeliveryRestartKeepsOneBufferCallback` (`delivery_test.go:185`)
  forces restarts, then expects one stats read per collection. Removing the
  deferred `Unregister` made it report `8, want 1`.

## Does not cover

Whether creating an instrument in `Setup` more than once on one meter is safe. That is unverified here; `registerBufferInstruments` creates them in the runner too.
