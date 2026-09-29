---
title: A Reconfigure Path Stores Configured State, Never Derived Operational State
date: 2026-09-28
last_verified: 2026-09-28
category: architecture-patterns
module: src/common/netsim/fabric
problem_type: bug
component: netsim
severity: high
symptoms:
  - "A no-op in-run reconfiguration mutates the fabric Spec"
  - "Oper-status conflict analysis issues disappear after reconfiguring a switch"
  - "A switch port configured Down becomes Up in stored configuration when attached to an active cable"
root_cause: "Fabric.Configure wrote the cable-rebuilt port table into the stored fabric configuration instead of keeping the normalized configured ports, replacing configured operational states with cable-derived runtime states."
resolution_type: code_fix
applies_when:
  - "Reconfiguring a child node inside a container that tracks both declared configuration and derived runtime state"
  - "A no-op reconfiguration mutates the container's Spec or Config"
  - "Discrepancy or conflict reporting between configured intent and derived topology disappears after a configuration update"
related_components: [vswitch, fabric, analysis]
tags: [reconfigure, netsim, fabric, oper-status, configured-vs-derived, spec-stability]
---

# A reconfigure path stores configured state, never derived operational state

`Fabric.Configure` updates a switch mid-simulation while preserving in-flight
frames, queues, and other switches. Cables derive the operational status of
physical ports (`rebuildSwitchPorts`), and the simulated switch runs with those
derived states. During initial implementation, `Configure` assigned the
cable-rebuilt ports directly into `f.cfg.Switches[node].Ports`.

Because `f.cfg` stores the declared fabric specification, this write
overwrote the caller's configured port states with runtime cable observations.
A port configured `port.Down` over a healthy cable became `port.Up` in `f.cfg`.
Two defects followed:

1. `fab.Metadata().Issues()` compares configured port operational state against
   cable-derived state to report `IssueOperStatusConflict`. Writing derived
   states into `f.cfg` erased the conflict.
2. `fab.Spec()` builds its switch specs from `f.cfg`. A no-op `Configure`
   produced a `Spec()` that differed from the original.

## What is true

- A container hosting simulated nodes maintains two views of state: declared
  configuration (`f.cfg`, returned by `Config()` and `Spec()`) and active
  runtime state (`f.switches`, `f.links`).
- Active switch execution requires cable-derived operational states.
  `vswitch.Derive` takes `targetSpec` with `rebuiltPorts` so protocol timers,
  spanning tree states, and forwarding decisions observe physical link state
  (`src/common/netsim/fabric/configure.go:72-78`).
- Stored configuration requires the caller's declared ports.
  `f.cfg.Switches[node]` must store `normSpec.Switches[node].Config.Ports`
  (`src/common/netsim/fabric/configure.go:88-91`), mirroring how `fabric.Derive`
  restores `next.cfg.Switches[name].Ports`
  (`src/common/netsim/fabric/derive.go:40-42`).
- Analysis and lint passes judge the gap between intent and reality.
  `Metadata()` detects misconfigurations by comparing `p.OperStatus` from
  `f.cfg` against `derived.Oper` from `f.derivedEnd(ep)`
  (`src/common/netsim/fabric/fabric.go:989-997`). If `f.cfg` absorbs derived
  states, this detection fails silently.
- Reconfiguration must be idempotent. Calling `Configure(node, fab.Config().Switches[node])`
  must leave `fab.Spec()` identical to its initial value
  (`src/common/netsim/fabric/fabric.go:282-286`).

## How to apply

When updating a component that derives runtime properties from its environment:

1. Pass the derived properties to the runtime constructor or mutation method
   (`vswitch.Derive`), so execution reflects environment constraints.
2. Store the normalized declared configuration in the container's configuration
   struct (`f.cfg`), discarding runtime-derived overrides before assignment.
3. Verify idempotence by testing that a no-op update with
   `fab.Config().Switches[node]` preserves both `fab.Spec()` and all
   pre-existing analysis issues.

```go
// derived is the active switch instance constructed with rebuiltPorts.
f.switches[node] = derived
derivedCfg := derived.Config()
// Keep normalized declared ports in f.cfg, not rebuiltPorts.
derivedCfg.Ports = normSpec.Switches[node].Config.Ports
f.cfg.Switches[node] = derivedCfg
```

## Evidence

- `src/common/netsim/fabric/configure.go:88-91`: `Configure` stores
  `normSpec.Switches[node].Config.Ports` into `f.cfg.Switches[node]`.
- `src/common/netsim/fabric/derive.go:40-42`: `fabric.Derive` preserves
  `next.cfg.Switches[name].Ports` across switch derivation.
- `src/common/netsim/fabric/fabric.go:989-997`: `Metadata()` flags
  `IssueOperStatusConflict` when configured `p.OperStatus` differs from
  `derived.Oper`.
- `src/common/netsim/fabric/configure_test.go:430-465`:
  `TestConfigureNoOpKeepsConfiguredOperStatus` configures a port `Down` over a
  healthy cable, runs a no-op `Configure`, and asserts that the
  `IssueOperStatusConflict` remains present and `Spec()` is equal.
- Mutation test: assigning `derivedCfg.Ports = rebuiltPorts` in `configure.go:90`
  fails `TestConfigureNoOpKeepsConfiguredOperStatus` with:
  `oper-status conflicts after no-op Configure = 0, want 1` and
  `Spec changed after no-op Configure`.

## What it does not cover

This rule governs state retention inside the orchestrating container, not
node-local state transitions. A node that owns internal state transitions (such
as dynamic MAC learning or STP port roles) still updates its own internal
tables during `vswitch.Derive`. The rule applies specifically to fields where
the container derives external facts (such as physical cable continuity) that
differ from the operator's configured specification.
