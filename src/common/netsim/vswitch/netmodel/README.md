# netmodel

Package `netmodel` translates device model records into executable virtual switch
construction specifications with explicit trust and readiness metadata.

The package accepts structured interface, VLAN, forwarding database, spanning tree,
LACP, and IP configuration inputs. It produces a reproducible [vswitch.ConstructionSpec]
alongside a [Report] and [analysis.Metadata] detailing readiness, assumptions,
defaults, skipped facets, and data conflicts.

## Working example

The loader accepts caller source context and device model slices, returning a
constructible result envelope:

```go
src := netmodel.SourceContext{
    DeviceID: "sw1",
    Origin:   "telemetry-snapshot",
    Context:  "collector-run-42",
}

res, err := netmodel.Load(
    now,
    src,
    ifaces,
    vlans,
    fdb,
    budgets,
    bridgeState,
    stpPorts,
    lacpAggregators,
    lacpPorts,
    addrs,
    neighbors,
    nil,
)
if err != nil {
    // Unconstructible input: empty interfaces, duplicate port names, or invalid LAG parents.
    return err
}

// Check readiness across the loaded model.
switch res.Readiness() {
case analysis.Complete:
    // Full model fidelity without missing operational state or conflicts.
case analysis.Incomplete:
    // Model contains missing operational state or skipped facets; inspect res.Metadata.Issues().
case analysis.Unstable:
    // Model contains contradictory or conflicting state rows.
}

// Build the switch directly from the construction specification.
sw, err := vswitch.NewWithSpec(res.Spec)
if err != nil {
    return err
}
```

## Error versus result separation

Errors returned by [Load] are strictly reserved for impossible construction inputs:

- Empty interface slices.
- Duplicate interface names.
- LAG parent references to non-existent interfaces.
- Configuration invariants that violate switch validation.

Partial, uncertain, or conflicting inputs do not return an error. Instead, [Load]
preserves all constructible configuration and records scoped findings in
`res.Report` and `res.Metadata`.

## Operational uncertainty and localized scoping

Interfaces with unspecified or unobserved operational status are assigned
[port.Unknown]. The loader records an `analysis.Incomplete` issue scoped directly to
the affected port:

- A port with unknown operational status cannot forward frames (`port.Forwards() == false`).
- A known-down port remains a definite domain drop with `analysis.Complete` status.
- Sibling uncertainty is localized. An unknown port on a switch does not taint
  independent known-up ports on the same switch.

## Report collections and metadata

The returned [Report] and [analysis.Metadata] expose fine-grained tracking for
synthesis decisions:

- **Skipped facets**: Features or records deliberately omitted during translation,
  such as bridge switchport configuration on routed interfaces, or IP addresses on
  interfaces without an IP facet. Each record carries the exact scope, explanation,
  and source evidence references.
- **Defaults and assumptions**: Standards-based fallback values applied when
  optional fields are absent, such as default STP timers or bridge MAC assignment.
  These are recorded both in the report and as executable assumptions in metadata.
- **Conflicts**: Duplicate or conflicting rows, such as duplicate FDB records for
  the same MAC and VLAN across different ports. Conflicts lower the overall model
  readiness to [analysis.Unstable] while preserving usable switch structure.
- **Deterministic ordering**: All report collections (capabilities, skips, defaults,
  conflicts) are sorted deterministically, ensuring reproducible diffs and tests.
