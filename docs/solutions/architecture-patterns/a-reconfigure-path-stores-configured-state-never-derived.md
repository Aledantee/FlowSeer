---
title: A Reconfigure Path Stores Configured State, Never Derived Operational State
date: 2026-09-28
last_verified: 2026-10-09
category: architecture-patterns
module: src/common/sim/fabric
problem_type: bug
component: sim
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

A port configured `port.Down` can sit on a cable that derives `port.Up`.
The switch executes with the cable-derived state. The fabric keeps the declared
state so `assembly.Spec(f)` can reconstruct the input and `Metadata()` can
report the conflict.

`Fabric.Configure` once stored the cable-rebuilt port table as configuration.
That erased `IssueOperStatusConflict` and changed the specification after a
no-op update.
The configured table now belongs to `builtNode.ports` in
[`topology.go`](../../../src/common/sim/fabric/topology.go).

## What is true

[`configure.go`](../../../src/common/sim/assembly/configure.go) passes
`rebuiltPorts` to `vswitch.Derive`, then replaces the node with the configured
port table from `configuredTopology(normCfg).Nodes[node]`. The derived device
and its output travel beside that table.
[`topology.go`](../../../src/common/sim/fabric/topology.go) restores the old
node when its replacement's link reports fail. `TestLinkChangeErrorReachesNew`
checks the retained node through `Snapshot().Devices`.

[`assemble.go`](../../../src/common/sim/assembly/assemble.go) follows the same
rule during construction and derivation. `assembly.Spec(f)` reads each device's own
specification and substitutes the node's configured port table.
`assembly.Spec(f).Config()` comes from that specification. The fabric holds no
stored switch configuration.

[`Fabric.Metadata`](../../../src/common/sim/fabric/fabric.go) compares
`builtNode.ports` against `derivedEnd`. A conflict remains visible while the
switch forwards using the derived state.

## How to apply

Keep configured environmental inputs beside the runtime device. Build or derive
the device with resolved inputs, then replace the node using the configured
inputs. Reading a specification must recover those configured values.

```go
n := configuredTopology(normCfg).Nodes[node]
n.Device, n.Output = derived, out
return fabric.Replace(f, node, n)
```

## Evidence

- `TestConfigureNoOpKeepsConfiguredOperStatus` in
  [`configure_test.go`](../../../src/common/sim/assembly/configure_test.go)
  preserves the configured `Down` value and exactly one conflict after a no-op
  configuration update.
- `TestConfigureWithoutChangeKeepsSpec` in
  [`built_spec_test.go`](../../../src/common/sim/assembly/built_spec_test.go)
  checks the complete specification, including a seeded switch, tagged host,
  reflector, and unreferenced evidence. Substituting `derived.Ports()` for
  `n.Ports` fails with `no-change Configure changed Spec`.
- `TestSpecKeepsConfiguredState` in the same file checks configured `Down`
  beside runtime `Up`, the conflict, the start, the assumption, an uncabled
  entry, and evidence no cable cites. It also checks the present cable fault.
  Removing the configured-port substitution in `assembly.Spec(f)` fails with
  `configured port = Up, want Down`.

The rule concerns externally derived properties. Node-owned state such as
learned forwarding entries still follows the device's retention rules.
