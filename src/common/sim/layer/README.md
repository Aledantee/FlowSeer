# layer

Package `layer` defines shared types and contracts common to data-link and
network layer models in the switch pipeline.

| Type | What it represents |
| --- | --- |
| `Env` | Environment inputs for layer normalization, validation, construction, and retention |
| `Emission` | An Ethernet frame to transmit out a virtual switch port |
| `FlushTarget` | A port whose learned forwarding table entries must be flushed, and which FIDs on it are stale |
| `Effects` | Frames to emit, forwarding entries to flush, and LAGs whose enabled membership changed |

## Env

`Env` packages the device environment provided to layers: node identity, port
table, base MAC address, and resolved port speeds. Each field is optional.

## Emission

`Emission` pairs an egress port name, a VLAN ID, and an Ethernet frame.

A zero VID retains layer-specific egress semantics:
- Spanning tree protocol (`stp`) emissions with VID 0 leave untagged without a
  VLAN membership check.
- Loop-protection (`loopprotect`) emissions with VID 0 request transmission
  onto the port's native VLAN.

## Effects

Layer state changes and timer advances produce `Effects`. Virtual switch
integrations collect these effects to dispatch frame transmissions, invalidate
filtering database entries, or update port state.

## Layer architecture contract

Every package under `src/common/sim/layer/` implements a uniform contract.

### Universal members

Every layer package exports:
- `const LayerName trace.Layer`: the layer identifier for trace records, with `Rule*` (`trace.RuleID`, or untyped string prefixes) and `Reason*` (`trace.Reason`) constants declared by the package that produces them.
- `type Config`: the package configuration struct.
- `Config.Normalize(layer.Env) Config`: returns a deep copy with standard defaults and sorting applied.
- `Config.Validate(layer.Env) error`: checks configuration consistency and references against the environment.
- `Config.Clone() Config`: returns an independent deep copy of the configuration.
- `Diff(prev, next Config) []trace.Change`: computes configuration differences between two revisions.

### Stateful layer runtime shape

Packages maintaining runtime state (`bridge`, `filter`, `lag`, `loopprotect`, `mcast`, `routing`, `stp`, `traffic`) export:
- `New(cfg Config, env layer.Env) (*Layer, error)`: constructs an active layer instance.
- `(*Layer).Clone() *Layer`: deep copies runtime state for branching or non-mutating preview.
- `RetentionKey(cfg Config, env layer.Env) string`: produces an exact key determining when active state may be retained across switch reconfiguration.

Layers advance simulated time through `(*Layer).Advance(now time.Time) layer.Effects`. Layers that report an earliest pending deadline declare `(*Layer).NextWake() (time.Time, bool)` (`lag`, `loopprotect`, `routing`, `stp`). `bridge` and `mcast` declare `Advance` without `NextWake`. `routing` parks hold-queue exits during `Advance` and returns them through `(*Layer).DrainExits() []HeldFrame`. Layers without timers (`filter`, `traffic`) declare neither method. Method names `Wake` and `Age` belong to the host virtual switch and are forbidden on layer types.

### Isolation boundaries

Layers are strictly decoupled:
- No layer package imports a sibling layer package under `layer/`. Inter-layer coordination occurs through virtual switch orchestration and types in `layer`.
- No layer package imports `sim/device` or `sim/fabric`.
- Trace step facts remain unexported within their declaring layer package.

The conformance gate in `test/conformance/sim` validates these invariants across every layer package. Its table `layerGuards` in `layer_contract_test.go` is the list of what is enforced, and a guard arrives with its fixture: each row names a directory under `test/conformance/sim/testdata/`, and the fixture passes once that row is dropped. The gate reads declarations as text and compares them with each row's literal, so a member of the wrong shape reads as missing. An aliased import of `layer`, `trace`, or `time` is one such shape, and no layer aliases one.
