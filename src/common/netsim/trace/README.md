# trace

Package `trace` defines the shared semantic vocabulary for network simulation
execution steps, frame outcomes, and configuration diffs.

The package is an import leaf. It does not import simulator capabilities,
device models, or protocol codecs. Simulator layers emit semantic facts, and
analysis tools compare or render traces without coupling to specific protocol
internals.

## Working example

Producers construct steps using capability-owned facts that satisfy [Fact].
The trace package provides deterministic ordering, equality, and rendering:

```go
// Capabilities construct steps with typed facts and evidence references.
step := trace.Step{
	Layer:   "bridge",
	Op:      trace.OpLookup,
	RuleID:  "fdb-hit",
	Subject: trace.Subject{Kind: "port", Key: "1/1/2"},
	Inputs: []trace.Fact{
		macFact{addr: "00:11:22:33:44:55"},
		vlanFact{vid: 10},
	},
	Outputs: []trace.Fact{
		portFact{name: "1/1/2"},
	},
	Evidence: []trace.EvidenceRef{"obs:fdb:dynamic"},
}

// Compare semantic equality across executions without string matching.
if step.Equal(expectedStep) {
	// Both executions performed the same operation with the same facts.
}

// Render trace records for operator inspection.
fmt.Println(trace.RenderStep(step))
// [layer="bridge" op="lookup"] rule="fdb-hit" subject.kind="port" subject.key="1/1/2" in=[{type="mac" value="00:11:22:33:44:55"}, {type="vlan" value="10"}] out=[{type="port" value="1/1/2"}] evidence=["obs:fdb:dynamic"]
```

## Semantic facts

Earlier trace designs stored free-form prose strings in each step. Callers
had to parse strings to discover which rule fired or whether an egress port
matched expectations.

Semantic facts replace prose strings with capability-owned value types
implementing [Fact]:

```go
type Fact interface {
	TypeID() string
	Canonical() string
}
```

A fact exposes a stable type identifier and a deterministic canonical string.
Semantic comparison compares `TypeID()` then `Canonical()` without reflection,
`any`, or JSON serialization. A capability owns its fact types and validation;
`trace` only requires equality and canonical ordering keys. Capability APIs
return immutable fact snapshots so a retained step or change cannot be altered
by later mutation of the source configuration.

## Producer-owned rule identifiers

Rules are typed through [RuleID]. Capabilities define their own rule IDs
without central registration in the trace package. When a new capability or
proprietary protocol adds a rule, existing tooling accepts the rule identifier
directly.

## Opaque evidence references

Trace steps and configuration changes can associate with supporting evidence
through [EvidenceRef]. The reference is an opaque key into an evidence catalog
managed by higher-level analysis packages. The trace package stores, sorts,
and deduplicates references without inspecting their catalog entries.

`OpQueue` marks an egress queue observation. The first enqueue beyond an
unstated buffer's maximum-frame threshold carries a producer-owned
`traffic.queue.buffer-unstated` rule, a typed depth fact, and one reference to
runtime evidence. It records why readiness is Incomplete; the frame may still
be delivered.

## Canonical ordering and equality

Comparing traces from two runs must not depend on map iteration order or slice
insertion order.

- [Step.Canonical] returns a copy with sorted input facts, sorted output facts,
  and sorted, deduplicated evidence references.
- [Trace.Equal] and [Step.Equal] compare canonical forms.
- [SortChanges] sorts configuration changes by layer, subject, field, and fact
  values.

## Zero values

Zero values are safe across the package:

- A zero-value [Step] renders as `[unspecified]` and compares equal to any other
  zero-value step.
- A zero-value [Trace] renders as `Outcome: unspecified`.
- A zero-value [Change] renders as `[unspecified]`.
- Unset fields in [Change] carry a `nil` [Fact], distinguishing added, removed,
  and modified configuration values without sentinel values.
