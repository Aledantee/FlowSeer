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
preserves the unaffected configuration and records scoped findings in `res.Report`
and `res.Metadata`. A malformed address or prefix omits its row rather than making
the whole switch unconstructible.

An FDB row becomes a seed only when it reports `ACTIVE` status and either
`STATIC` or `DYNAMIC` kind. Missing, unspecified, unsupported, and unrecognized
values leave no executable seed and make readiness non-Complete. The row must
also name a logical switchport that admits its VLAN in the completed bridge
configuration. A row that has no relay, no VLAN-aware relay, no matching VLAN,
or no admitting switchport is omitted with a port-scoped issue.
Conflicting executable rows are scoped to their exact FID and MAC lookup key, so
they affect forwarding only when that unicast destination is consulted.

An explicit capability request is also input to the trust decision. The loader
deduplicates supported layers and reports duplicate requests as Incomplete.
Unknown layers and layers that the loader cannot construct are omitted and
reported as Unsupported. If every requested layer is rejected, the loader does
not fall back to capability inference.

The spanning tree layer constructs only from a bridge state that explicitly
reports RSTP. A missing or `UNSPECIFIED` protocol version is Incomplete; STP and
unknown versions are Unsupported. In each case the STP layer is omitted. An
absent bridge or port priority uses the IEEE default, while an explicitly
reported priority of zero remains zero through switch construction.

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
  optional fields are absent, including STP bridge and port priorities, hello
  time, max age, forward delay, transmit hold count, and the executable
  zero/unlimited MTU fallback.
  These are recorded both in the report and as executable assumptions in metadata.
  A missing MTU also creates an evidenced Incomplete port issue. An explicitly
  reported MTU of zero is observed data and does not create a default or issue.
- **Conflicts**: Distinct values for the same source key, such as one MAC and VLAN
  reported on two ports. The conflicted fact is omitted, while identical repeated
  rows collapse to one fact. Conflicts lower readiness to [analysis.Unstable] and
  do not depend on input order.
- **Deterministic ordering**: All report collections (capabilities, skips, defaults,
  conflicts) are sorted deterministically, ensuring reproducible diffs and tests.

## Construction trust boundary

The construction specification contains normalized configuration, executable
seeds, the source device identity, and an immutable copy of the loading metadata.
Building a switch with `vswitch.NewWithSpec(res.Spec)` therefore carries scoped
loading conflicts, skips, assumptions, and their evidence into later `Forward`
results. The detailed loading report remains on `Result` because it describes the
translation rather than runtime forwarding.

A forwarding result selects metadata against the dependencies it actually
consulted. Port state uses port scopes. Routing uses exact port, VLAN, ownership,
route, local-address, and neighbor lookup scopes; neighbor scopes include the VRF,
interface, and address, including for VLAN interfaces. Bridge forwarding uses the
exact FID and destination MAC scope for an FDB lookup. A sibling dependency is
excluded, while a node-scoped conflict affects every query on that node. The
result's scope retains `SourceContext.DeviceID`, which keeps similarly named ports
on different devices disjoint.
