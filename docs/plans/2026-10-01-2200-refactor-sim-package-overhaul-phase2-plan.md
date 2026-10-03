---
title: Capability Contract and Surface Trim - Plan
type: refactor
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: rework
execution: code
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Capability Contract and Surface Trim - Plan

> Implemented. 10 units, 2026-10-02T17:40:30Z to 2026-10-03T14:34:24Z.

## Goal

Every package under `sim/layer/` has the one shape the
[package shape record](../architecture/2026-10-01-simulation-package-shape-direction.md)
states, the shared types live in `sim/layer`, and the exported surface holds
only what a caller outside the package uses. Stop condition: if folding
`Age` into `Advance` changes when an entry ages relative to an arrival at the
same instant, the two verbs stay and the record is amended.

## Decisions

- The parent's Decisions apply. Paths use the Inventory's `S`, `L`, and `V`.
- The contract is the record's "One capability contract" list. This phase
  changes shapes and moves code. It fixes no behaviour except where a shape
  change forces it, and each such case is named in the unit.
- Rule identifiers and layer names become constants in the package that
  produces them, named `LayerName` as `filter` already does. `port.Layer*`
  shrinks to `port.LayerName`. Why: the virtual-device record says rule
  identifiers are producer-owned, and `V/compare.go:217` matches the literal
  `"traffic.mirror_decision"`. `phy` and `bridge` write two layer names and
  also declare `LayerNamePoE` and `LayerNameVLAN`. Every string keeps its
  value.
- Fact wrapper types are unexported and tests assert on `TypeID` and
  `Canonical`. Why: `BoolFact` is declared in seven packages, `DurationFact` in
  five, `MACFact` in four, and none has a caller outside tests. A type another
  package constructs becomes a function of the same name returning
  `trace.Fact`. One type identifier names one canonical shape, declared by the
  layer that decides, since a test now selects a fact by `TypeID`.
- The BPDU and SSTP codecs move to `src/common/net/bpdu`. Why: the record
  places wire codecs there, and `lag` already uses `net/lacp`. What names a
  `trace.Reason` stays in `stp`, since `net` may not import `sim/trace`.
- `Difference` and the metadata clone, equal, and merge helpers move into
  `analysis`. Why: `V/compare.go` and `S/fabric/compare.go` declare the same
  struct, and both packages carry their own metadata helpers. Each helper keeps
  the comparison it makes today (Inventory).
- `Age` and `Wake` fold into `Advance(now) layer.Effects`, and the stop
  condition does not fire. Why: the fabric ages before it forwards, a wake
  sorts before a frame at one instant, and aging twice at one instant changes
  nothing (Inventory, Time verbs). A frame at instant T meets state aged to T
  before and after the fold. U3 names what the fold adds.
- `layer.Effects` holds `Emissions`, `Flush`, and `Changed`. A frame leaving a
  routing hold queue stays a `routing.HeldFrame` that `Advance` parks and
  `DrainExits` returns. Why: the record lists the four types `layer` holds, and
  a held frame needs its interface, cause, and priority
  (`L/routing/neighbor.go:99-106`).
- `layer.Env` is `NodeID`, `Ports`, `MAC`, and `Speeds` (bits per second by
  port name): every argument a layer takes beside its `Config` today. `Clone`
  takes nothing, as R1 lists it. `Diff` uses the zero `Env`.
- A layer package holds runtime state when it declares `New`. `phy` does not.
  `bridge.Bridge` is renamed `Layer`, and `traffic` gains a `Layer` owning the
  policer buckets the switch holds (`V/switch.go:223,413-423`).
  `bridge.RetentionKey`, `filter.RetentionKey`, and `filter.Layer.Clone` are
  new and have no production caller until parent U9.
- A subject key of one part is the identifier. A key of several parts quotes
  each with `strconv.Quote` and joins them with `/`, through one function in
  `trace`. Why: port names hold `/` (`S/trace/README.md:22`), so a raw join is
  ambiguous, and `go doc strconv.Quote` returns a Go string literal.
  `TestDiffMemberSubjectKeysAreInjectiveAcrossLAGs` pins the form.
- `lag` normalization keeps its positional default LACP key. Why: no shape
  change forces the rule, the README documents it, and adding a LAG rebuilds
  the layer whatever the keys are (Inventory, Default LACP key).
- The plan stays whole at ten units. Why: one cluster. Every unit before the
  trims edits `V/switch.go`, so the graph is a chain until U8.
- U10 is re-planned before more fixing, so the contract gate names one
  fixture per guard it enforces. The review ended `rework` after three fix
  rounds with sixteen guards no fixture refuses and a fork row that fails by
  panic. The re-plan starts from `parked/sim-p2-review`, which holds every
  fix commit, and U1 to U9 stay landed. (decided by the user, 2026-10-03)
- The gate holds its guards as a table, and each row names a fixture
  directory the gate refuses. The gate's own test drops each row in turn and
  requires that row's fixture to pass. Why: the merged checker's guards
  are operands of nested conditions over syntax nodes, its 66 fixture
  directories leave the 23 of Inventory, Gate guards, unheld, and a table
  turns a missing fixture into a failing test.
- The gate compares declarations as text, one line each. Why: a member then
  needs one comparison, where `returnsLayerAndError` holds six operands
  (`test/conformance/sim/layer_contract_test.go:914-929`).
  `go/types.ExprString` prints an identifier, a selector, a pointer, and a
  slice or array type as the source writes them, and shortens only function
  and composite literals (`go/types/exprstring.go` in Go 1.27.1, the cases
  for `*ast.Ident`, `*ast.SelectorExpr`, `*ast.StarExpr`, `*ast.ArrayType`,
  `*ast.FuncLit`, and `*ast.CompositeLit`).
- The gate calls a package stateful when it declares `Layer`, `New`, or
  `RetentionKey`, which is wider than the `New` rule above. Why: under `New`
  alone no package can be reported for lacking `New`, and the merged gate
  decides this way (`layer_contract_test.go:754`).
- The gate's fixtures are rebuilt, and the merged ones are deleted in the
  commit that adds the table. Why: 18 rows need 18 fixtures and two that
  pass, and a directory no row names is a fixture nothing accounts for.
  `03246c8e` rewrote five fixtures in place while it added 24, so U10 runs
  the new gate over the old directories before it deletes them.
- The review of the re-planned U10 gets a fourth fix round, past the fix
  loop's limit of three. It adds a stateless fixture for each of the five
  member rows, so the gate is shown to refuse a stateless package missing a
  member, and resumes from `parked/sim-p2-u10-review`. (decided by the user,
  2026-10-03)

## Requirements

R1. Each layer package has `const LayerName`, `Config.Normalize(layer.Env)`,
`Config.Validate(layer.Env)`, `Config.Clone`, `Diff`, and, when it holds
runtime state, `New(cfg, layer.Env)`, `(*Layer).Clone`, and
`RetentionKey(cfg, layer.Env)`. Example: `filter.New` no longer takes a port
table it ignores, and `lag.Diff` normalizes its inputs like every other
`Diff`.

R2. No package under `sim/layer/` imports a sibling. Example: `go list -f
'{{.Imports}}' ./src/common/sim/layer/traffic` holds no `layer/bridge`.

R3. A conformance test under `test/conformance/` enforces R1 and R2 and the
absence of exported `*Fact` types. Example: adding `type FooFact string` to
`layer/mcast` fails the gate.

R4. Every exported identifier listed under Inventory, Dead exports, is
deleted, unexported, or has a named caller outside its package. Example:
`analysis.InputValidity` does not exist.

R5. `mcast.Layer` holds no mutex. Example: its doc comment states the caller
serializes, as `stp`, `lag`, `routing`, and `bridge` do.

## Out of scope

- The switch's `Age`, `Wake`, `NextWake`, `Fork`, and `Derive` loops, its
  receive entry, and `LinkChange` signatures. Parent U9 owns them.
- `fabric`'s layer constant, rule literals, and `nestedSubjectKey`
  (`S/fabric/diff.go:859`). Parent U10 owns the package.
- What a retention key covers. `routing.RetentionKey` still omits the node
  identity, which phase 6 fixes with its failing test.
- Whether the BPDU layout matches IEEE 802.1Q. Phase 3 adds the known-bytes
  fixtures (parent R7).
- Hostile input to the gate. It reads this repository's Go source under
  `src/common/sim/layer/`, whose authors are trusted.
- A gate row for `NextWake`. R1 does not list it, and `bridge` and `mcast`
  advance without one (U3).

## Inventory

Paths are as of commit `64e1039f`. `S` is `src/common/sim`, `L` is
`S/layer`, `V` is `S/device/vswitch`. A bare file name is in the package its
row or bullet names.

### Contract divergences

| Package | Validate | Normalize | Constructor | Time | RetentionKey | Layer constant | Rule IDs |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `S/port` | `Table.Validate()` | `Table.Normalize`, `Port.Normalize` | `NewBuilder().Build()` | none | none | all 12 `Layer*` (`port.go:18-54`) | literals in `V/switch.go` |
| `phy` | `(ports)` | `()` | none: `Resolve`, `Allocate`, `Negotiate` | none | none | in `port`, two names | none |
| `bridge` | `(ports)` and `Bridge.Validate` | `()` and `NormalizeSeeds` | `New(cfg, ports)`, type `Bridge` | `Age` | none | in `port`, two names | 46 literal sites |
| `lag` | `(ports)` | `(ports, systemID)` and alias `Defaults` | `New(cfg, ports, systemID)` | `Wake`, `NextWake`, `LinkChange(now, member, up)` | `(cfg, ports, systemID)` | in `port` | literals in `V/switch.go` |
| `stp` | `(ports)`, `ValidateTimers`, `MST.Validate`, `PVST.Validate` | `()`, `MST.Normalize`, `PVST.Normalize(priority)` | `New(cfg, ports)` | `Wake`, `NextWake`, `LinkChange(now, port, up, p2p, speed)` | `(cfg, ports, speeds)` | in `port` | literals in `V/switch.go` |
| `loopprotect` | `(ports)` | `()` | `New(cfg, ports, mac)` | `Wake`, `NextWake`, `LinkChange` | `(cfg, ports, mac)` | own (`diff.go:14`) and `port` | literals in `V/switch.go` |
| `mcast` | `(ports)` | `()` | `New(cfg, ports)` | `Age` | `(cfg, ports)`, unused (`V/derive.go:165-173`) | in `port` | literal in `V/switch.go` |
| `routing` | `(ports)` | `()` | `New(cfg, ports, nodeID)` | `Wake`, `NextWake`, `Age` | `(cfg, ports)`, omits `nodeID` | in `port` | 14 literal sites, and `V/switch.go` |
| `traffic` | `(ports)` | `()` | none: `NewBucket`, `Copies(cfg, *bridge.VLAN, ...)` | none | `(cfg)`, in `policer.go` | own `Layer` (`config.go:18`) and `port` | five exported, plus literals |
| `filter` | `()` | `()` | `New(cfg, _ port.Table, nodeID)` | none | none, absent from `V/retention.go` | own (`filter.go:20`) and `port` | five exported |

`stp.Config.ValidateTimers` has a caller in `S/netmodel` and stays. `S/port`
is not a layer package: the contract leaves `port.Table` alone, and only the
constants and `Transmit` change.

### Declared more than once

- `FlushTarget`: `L/stp/layer.go:39`, `L/loopprotect/layer.go:43`,
  `L/bridge/bridge.go:217`. The `loopprotect` one never fills `FIDs`.
- `Emission`: `L/stp/layer.go:21`, `L/lag/layer.go:21`,
  `L/loopprotect/layer.go:21`. Struck: `V/switch.go:155`, the device's
  output, which carries the `PCP` and `Protocol` the fabric reads. A zero
  `VID` means two things: `stp` sends the frame as built
  (`V/switch.go:3020`), and `loopprotect` asks for the port's untagged VLAN
  (`:3057`). The shared type keeps both, each applied by its own function,
  which is the hazard of
  `docs/solutions/architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md`.
  A device that merges the two appliers has to tell the cases apart.
- `Effects`: `L/stp/layer.go:29`, `L/lag/layer.go:27`,
  `L/loopprotect/layer.go:30`, `L/routing/neighbor.go:300`.
- `Difference`: `V/compare.go:19-31` and `S/fabric/compare.go:21-33`.
- Metadata helpers: `V/metadata.go:17-73`, `S/fabric/journey.go:439-545`,
  and `S/fabric/compare.go:822`. The issue comparisons are three rules:
  `issueEqual` ignores `Message`, `sameIssue` compares canonical forms with
  it, and `sameIssues` compares code, status, and scope in order.
- Layer constants for `loopprotect`, `filter`, and `traffic` exist in the
  package and in `port`.
- `mcast.MembershipDecisionFact` (`L/mcast/fact.go:31`, no production
  caller) against `newMembershipFact` (`V/metadata.go:139`), which adds the
  source and carries `vswitch.mcast_membership`.
- Three loop-protection facts built in the switch share
  `vswitch.loopprotect_decision` (`V/switch.go:2564-2595`).
  `loopprotect.Layer.ForwardingFact` is in use through `bridge.Gate`
  (`L/bridge/bridge.go:1409`), so the earlier claim that it is unused is
  struck.
- `lag.LACPDecodeFact` and `lag.MemberTransitionFact` share the type
  identifier `lag.lacp_decision` (`L/lag/fact.go:21,112,120`).
- `routing.EgressFact` reuses `routing.lookup_decision`
  (`L/routing/fact.go:21,91`).
- Struck: `Origin` and `Lifetime` (`L/bridge/fdb.go:40-61`,
  `L/mcast/layer.go:51-72`). Neither package may import the other (R2), and
  the record's `layer` does not hold them.
- Struck: `bridge.Selection` against `lag.Selection`
  (`L/bridge/bridge.go:44`, `L/lag/layer.go:282`). `bridge` declares what
  its `Selector` returns, and the adapter at `V/switch.go:3765-3786` is the
  seam R2 requires.
- Struck: `bridge.Seed` against `bridge.Entry` (`L/bridge/fdb.go:63-80`).
  One is the input to `Learn` and the other the output of `Entries`, and
  merging them renames some 140 call sites for no caller's benefit.

### Import rule breaches

- `L/traffic/mirror.go:8` imports `bridge` for `*bridge.VLAN`,
  `bridge.Switchport`, and `bridge.Egress`.
- The codecs sit in `L/stp/bpdu.go` and `L/stp/sstp.go`. Every decode error
  carries `Attr("reason", ReasonUnsupportedBPDU)`, a `trace.Reason`, and
  the switch reads it back at `V/switch.go:2633-2636`.
- `mcast.Layer` holds `mu sync.RWMutex` (`L/mcast/layer.go:103-105`).
- `loopprotect.GroupAddress` (`probe.go:33`) and `stp.GroupAddressSSTP`
  (`sstp.go:16`) are exported mutable variables.
- Not a breach: test files of `mcast`, `lag`, `phy`, and `traffic` import
  `bridge` or `device/vswitch`. R2 reads `.Imports`, which leaves tests out.

### Time verbs

- The fabric calls `Wake` on a wake arrival (`S/fabric/run.go:410`) and
  `Age` before `Forward` on a frame arrival (`:561`), its only `Age` call.
- A wake sorts before a frame at one instant
  (`S/fabric/queue.go:15-18,41-46`, `TestQueueStepsWakeFrameDequeueInOrder`).
- Aging compares stored times with `now` and keeps no other state
  (`L/bridge/bridge.go:377-385`, `L/mcast/layer.go:245-262`,
  `L/routing/neighbor.go:285-294`).
- `Switch.Age` ages `bridge`, `mcast`, and `routing`
  (`V/switch.go:2220-2230`). `Switch.Wake` wakes `stp`, `lag`,
  `loopprotect`, and `routing` (`:3499-3516`).

### Default LACP key

- Assigned from the LAG's position among the table's LAG ports
  (`L/lag/config.go:188-189,216-217`) and documented at
  `L/lag/README.md:202`.
- `Diff` reports the key in force (`L/lag/diff.go:298-305`), and the
  retention key encodes every LAG (`L/lag/layer.go:844-852`).

### Subject keys and shapes that change

- Quoted composite keys: `L/lag/diff.go:383-385`,
  `L/routing/diff.go:529-535`.
- Raw composite keys: `L/filter/diff.go:181,205,271,339`,
  `L/traffic/diff.go:216`, `L/stp/diff.go:409,435,505,531`, and the queue
  subjects of steps at `S/fabric/fabric.go:1051,1054` and
  `S/fabric/run.go:986`.
- Tests and corpus files holding a key or fact identifier U7 changes:
  `V/switch_test.go`, `S/fabric/loopprotect_test.go`,
  `S/fabric/egress_buffer_test.go`,
  `S/fabric/queue_buffer_internal_test.go:348`, `L/traffic/config_test.go`,
  `L/stp/config_test.go`, `L/filter/diff_test.go:113-138` (a `switch` on
  the key, which a search for `Key:` literals misses), and `cases.go`,
  `filter_cases.go`, `mcast_cases.go`, `load_cases.go` under
  `S/internal/simtest`. The list is what a search found, and the suites
  are the check.

### Dead exports

No use outside the package directory, production or test, counted with
`go/packages` over `./src/common/sim/...` and `./src/edge/simload/...`. An
exported `*Fact` type is unexported whoever names it.

Each entry takes the first rule that fits. Nothing uses it: deleted. The
package uses it: unexported. Only the package's own tests use it: a test
helper when it composes exported API (`Bridge.Forward` is `Ingress` then
`Egress`), otherwise unexported with its tests in an internal test file. An
exported field of an unexported type goes with the type. An identifier
another package's test names is kept and struck here.

Earlier units settle some entries, and U8 and U9 leave those alone:
`analysis` (U2), `Table.Transmit` and `LayerLoopProtect` (U4),
`Config.Defaults` (U5), `mcast.RetentionKey` (U6, which keeps it exported
and gives it a caller in `V/derive.go`), and every fact type (U7).

- `trace`: `Render`, `CompareStep`, `EqualChange`, `CompareChange`,
  `SortChanges`, `CanonicalChanges` (`render.go:12`, `trace.go:185-375`).
- `analysis`: `InputValidity`, `InputValid`, `InputInvalid`
  (`status.go:6-13`).
- `port`: `IfIndexFact`, `MTUFact`, `LagParentFact` (`port.go:154-178`).
  `Table.Transmit` returns a member string that is always empty
  (`port.go:368-392`).
- `phy`: `SourceSetting`, `ClassPowerNanowatts`, `ReasonLimit`,
  `ReasonClassUnsupported`, five fact types. Struck: `Class`
  (`S/netmodel/netmodel.go`), `ReasonBudget` (`S/netmodel/netmodel_test.go`),
  `ReasonDuplexMismatch`, `ReasonSpeedMismatch`,
  `ReasonForcedAgainstAutoUnmodeled` (`S/fabric/link_test.go`,
  `S/fabric/topology_state_test.go`).
- `bridge`: `Bridge.Forward`, `Bridge.Peek`, `Bridge.Validate`,
  `DefaultServiceTPID`, five fact types. Struck: `GateCount`
  (`V/derive_internal_test.go`), `DefaultAgingTime`
  (`V/trace_fact_semantics_test.go`).
- `lag`: `Pending`, `PendingCause` and its constants, seven default and
  timing constants (`config.go:17-38`), nine fact types. `Config.Defaults` is an
  alias `S/netmodel` calls.
- `stp`: `Layer.InstancePortInfo`, `DefaultPathCost`, `MigrateTime`,
  `DefaultMaxHops`, eleven fact types (`diff.go:16-106`).
- `loopprotect`: `ReasonUnsupportedProbe`, `EtherType`, `LayerLoopProtect`,
  four fact types. Struck: `DefaultInterval` (`V/switch_test.go`).
- `mcast`: `RetentionKey` (kept, U6), `DefaultLastMemberQueryInterval`,
  `DefaultLastMemberQueryCount`, three fact types. Struck:
  `ReasonNoRouterPort` and `ReasonBadControl` (`V/switch.go`).
- `routing`: `Candidate`, `LocalAddressLookupScope`, `Layer.DiscardHeld`,
  eleven fact types. `AddrFact`, `PortFact`, and `VLANFact` are constructed
  at `S/fabric/accept.go:237,343` and `V/switch.go:1253`, and
  `bridge.PVIDFact` at `S/internal/simtest/cases.go:64-65`.
- `filter`: `Layer.HasBinding`, `BindingScope`, `Tuple`, `Config.Equal`,
  `Rule.Equal`, `Match.Equal`, five fact types.
- `traffic`: eight fact types, `Bucket.Tokens`, `Config.OutputPorts`.
  Struck: `PortsFact` and `VLANsFact` (`S/fact_contract_test.go`).
- `vswitch`: `CompareResults`, `DeviceMACFact`, `LayerFact`,
  `AllKeptRetention`, `Switch.Err`, `Switch.PeekMember`, `Switch.Speeds`,
  `Switch.Resolve` and `Switch.MembershipFact` (exported for
  `bridge.GroupResolver` only), `ReasonHeldInterfaceUnknown`,
  `ReasonHeldCauseUnknown`.
- `fabric`: seven fact types, `Fabric.Unlinked`, `JourneyOrigin.Validate`,
  `ReasonTruncatedRecord`, `Fabric.Mcheck`.
- `stream`: `SplitMix64` and `NewSplitMix64`, exported only because
  `Variation.Apply` takes the type (`variation.go:21`).
- `simtest`: 39 `Case*` constructors. Struck: `PermuteOrder`
  (`V/conformance_test.go`, `S/fabric/conformance_test.go`).

### Gate guards

`G` is `test/conformance/sim/layer_contract_test.go` as of `3247dcf7`, its
last change. A guard is unheld when every row of
`TestLayerContractReportsViolations` stays green with the guard removed. The
review's verdict counts sixteen and lists none (`1ad57013`). Counted by
operand of `G` there are 23:

| Line of `G` | Guard | Source no fixture declares |
| --- | --- | --- |
| 583 | a file that does not parse is reported | a syntax error |
| 592 | an import of `sim/device` itself | that import |
| 594 | an import below `sim/fabric/` | that import |
| 650 | `LayerName` is a constant | `var LayerName` |
| 654 | a constant is named `LayerName` | another constant and no `LayerName` |
| 664 | a type is named `Config` | another type and no `Config` |
| 674 | `Diff`, `New`, and `RetentionKey` are functions | a method of that name |
| 716 | the receiver of `Clone` is `Layer` | `Clone() *Layer` on another type |
| 789, 798, 807, 898 | the qualifier of `layer.Env`, `time.Time`, `layer.Effects`, `trace.Change` | the name from another package |
| 837 | an unnamed parameter counts | `Clone(int)` |
| 868, 877, 886, 903, 933 | one result for `Normalize` and `Config.Clone`, `Validate`, `Diff`, `(*Layer).Clone`, `RetentionKey` | `Normalize(layer.Env) (Config, error)` |
| 872 | the result of `Normalize` and `Config.Clone` is `Config` | another named type |
| 880, 936 | the result of `Validate` and of `RetentionKey` is an identifier, the `ok` of each assertion | a pointer or slice result |
| 911 | `(*Layer).Clone` returns `*Layer` | `Clone() *Config` |
| 916 | two results for `New` | a third result |

Five more conditions let honest source through, and no fixture declares that
source. `TestLayerContract` over the nine layer packages holds the first two.
Nothing holds the other three.

- An unexported type whose name ends in `Fact` (`G:609`).
- An `Advance` with the contract's signature (`G:723`). `valid` declares none.
- A `testdata` directory (`G:573`).
- An import of the layer's own package from a file below it, and an import of
  a package below the layer (`G:598`, two operands).

The fork gate formats a non-zero `resetOnFork` field with `vDst.Interface()`
(`V/fork_internal_test.go:116-119`). `vDst` is read from an unexported field,
and `Interface` "panics if the Value was obtained by accessing unexported
struct fields" (`go doc reflect.Value.Interface`). The test passes today,
since `Fork` resets all five such fields. A `Fork` that kept one would panic
the test where it should fail with a message.

The table U10 builds. A row's name is its fixture directory under
`test/conformance/sim/testdata/`. A fixture is one file holding what the
merged `valid/fixture.go` declares, a whole stateful layer, with the one
change named. Fifteen rows refuse, each with one finding that quotes its
literal. The three `stateful_` rows report nothing themselves. Each marks the
package stateful, their fixtures start from `valid_stateless`, and what the
gate reports there are the findings of `new`, `layer_clone`, and
`retention_key` for the members the fixture lacks. With its `stateful_` row
dropped the fixture is stateless and passes.

| Row and fixture | Rule | The fixture's change |
| --- | --- | --- |
| `layer_name` | requires `const LayerName` | declares it with `var` |
| `normalize` | requires `func (Config) Normalize(layer.Env) (Config)` | returns `(Config, error)` |
| `validate` | requires `func (Config) Validate(layer.Env) (error)` | takes no parameter |
| `config_clone` | requires `func (Config) Clone() (Config)` | has a pointer receiver |
| `diff` | requires `func Diff(Config, Config) ([]trace.Change)` | is a method of `Config` with the same parameters |
| `stateful_layer` | `type Layer` makes the package stateful | adds `type Layer struct{}` |
| `stateful_new` | `func New` makes the package stateful | adds `func New(cfg Config) Config` |
| `stateful_retention_key` | `func RetentionKey` makes the package stateful | adds the contract's `RetentionKey` |
| `new` | a stateful package has `func New(Config, layer.Env) (*Layer, error)` | returns `*Layer` alone |
| `layer_clone` | a stateful package has `func (*Layer) Clone() (*Layer)` | declares `Clone` on another type |
| `retention_key` | a stateful package has `func RetentionKey(Config, layer.Env) (string)` | takes `Config` alone |
| `advance` | a package with `func Layer.Advance` has `func (*Layer) Advance(time.Time) (layer.Effects)` | returns nothing |
| `wake` | refuses `func Layer.Wake` | adds it with a pointer receiver |
| `age` | refuses `func Layer.Age` | adds it with a value receiver |
| `exported_fact` | refuses an exported type whose name ends in `Fact` | adds `type FooFact string` |
| `import_sibling` | refuses an import below `sim/layer/` whose first element is not the directory's name | a file in `sub/` imports `sim/layer/bridge` |
| `import_device` | refuses an import of `sim/device` or below it | imports `sim/device/vswitch` |
| `import_fabric` | refuses an import of `sim/fabric` or below it | imports `sim/fabric` |

`valid` keeps those declarations, passes every row, and adds what the gate
lets through. A gate that read any of these differently would refuse `valid`:

- a `_test.go` file that imports `sim/layer/bridge`,
- a text file,
- a `testdata/` file declaring `type ForeignFact string`,
- an import of `sim/layer/valid/sub`,
- `type fixtureFact string`,
- a method `Age` with no result on a type other than `Layer`,
- a file in `sub/` declaring its own `Layer` with a `Wake` method,
- an `Advance` with the contract's signature,
- an unnamed parameter, and two parameter names sharing one type.

`valid_stateless` declares the five members every layer has and nothing
else, as `phy` does.

## Units

### U1. Move the BPDU and SSTP codecs to `net/bpdu`
Files: src/common/net/bpdu/, src/common/sim/layer/stp/, src/common/sim/device/vswitch/, src/common/sim/netmodel/, src/common/sim/internal/simtest/, src/common/sim/fabric/, src/common/sim/README.md, src/common/README.md
After: none
Change: `net/bpdu` holds what is on the wire: `BPDU`, `BPDUType`, `MSTIRecord`,
`BridgeID`, `Role`, the four codec functions, and the SSTP group address as a
function, with a README. A decode error wraps `bpdu.ErrUnsupported`, as
`net/lacp/lacp.go:65` does, and the switch names `stp.ReasonUnsupportedBPDU`
itself. No encoded octet changes.
Tests: the codec cases of `L/stp/bpdu_test.go` move and pass with only the
qualifier changed. A new case asserts `errors.Is(err, bpdu.ErrUnsupported)` for
a truncated payload. Nothing here checks the layout against the standard, since
the cases are round trips
(`docs/solutions/conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md`).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/bpdu src/common/sim`

### U2. Move comparison and metadata helpers into `analysis`
Files: src/common/sim/analysis/, src/common/sim/device/vswitch/, src/common/sim/fabric/, src/common/sim/search/result.go
After: U1
Change: `analysis.Difference` replaces both structs. `analysis.Metadata` has
`Clone`, `Equal`, and `Merge`, each with the comparison its helper makes today
and a doc comment stating it. `sameIssues` moves beside them, and
`InputValidity` with its two constants is deleted.
Tests: `analysis` cases for a clone sharing no slice with its source, for
`Equal` on values differing only in an issue's `Message` (equal today), and for
`Merge` adding an absent issue with its evidence and dropping a duplicate.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U3. Shared effect types and one time verb
Files: src/common/sim/layer/layer.go, src/common/sim/layer/README.md, src/common/sim/layer/stp/, src/common/sim/layer/lag/, src/common/sim/layer/loopprotect/, src/common/sim/layer/routing/, src/common/sim/layer/bridge/, src/common/sim/layer/mcast/, src/common/sim/device/vswitch/, src/common/sim/fabric/, src/common/sim/README.md
After: U2
Change: package `layer` declares `Effects`, `Emission` (`Port`, `VID`,
`Frame`), and `FlushTarget`. `stp`, `lag`, and `loopprotect` drop their own,
and `bridge.Flush` takes `layer.FlushTarget`. A zero `Emission.VID` keeps each
layer's meaning (Inventory), and the type's doc comment says so. `Wake` and
`Age` are named `Advance` on all six layers, and `bridge` and `mcast` get no
`NextWake`. `routing.Advance` ages, then settles. Exits wait in the layer in
today's order, `DrainExits` returns and clears them, `Clone` copies them, and
`FailHeld` returns `[]HeldFrame` with any waiting exit first. `loopprotect`
emits the encoded probe, and the switch sets the VLAN it resolves through a
`loopprotect` function that patches the payload without decoding. The probe
group address becomes a function. `Switch.Age`, `Switch.Wake`, and
`observeAndRelease` (`V/switch.go:1529-1541`, after `Observe`) advance the
layers they age and wake today and apply routing's exits. Forced change:
`routing` ages and settles on all three paths, so an evicted frame or an
expired resolution leaves at the next call on any of them.
Tests: `routing.Advance` past a Reachable expiry and an Incomplete deadline
leaves Stale and Failed, `DrainExits` returns the timed-out frame once, and a
clone taken before the drain returns it too. A frame evicted from a full hold
queue is in `DrainNeighborFailures` after the next `Age` with no `Wake`
between. For the stop condition, with a Reachable neighbor expiring at T,
`Wake(T)`, `Age(T)`, `Forward(T)` gives the result and neighbor state of
`Age(T)`, `Forward(T)`. A probe on a port with no configured VLAN carries the
port's PVID.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U4. Layer names and rule identifiers owned by their producers
Files: src/common/sim/port/, src/common/sim/layer/, src/common/sim/device/vswitch/, src/common/sim/netmodel/, src/common/sim/fabric/, src/common/sim/internal/simtest/, src/common/sim/layer_names_test.go
After: U3
Change: each layer package declares `LayerName`, and `port` loses the other
eleven constants and the `Layer` alias. Every rule literal in `L`, `S/port`,
and `V` is a `Rule*` constant in the package whose layer name the step carries.
A rule built from a prefix and a reason keeps the prefix as the constant.
`bridge.SetGroupResolver` takes the layer name and rule of its replication step
(`L/bridge/bridge.go:993`). `V/compare.go:217` compares against a constant
`traffic` exports. `Table.Transmit` returns the reason alone.
Tests: `TestLayerNames` in `S/layer_names_test.go` asserts the twelve strings
and replaces `TestLayerConstants`. The corpus and every test holding a rule
literal pass unchanged, which shows no identifier moved.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U5. One environment for configuration and construction
Files: src/common/sim/layer/, src/common/sim/device/vswitch/, src/common/sim/fabric/, src/common/sim/netmodel/, src/common/sim/internal/simtest/, src/common/sim/search/, src/edge/simload/
After: U4
Change: `layer.Env` exists. `Config.Normalize`, `Config.Validate`, `New`, and
`RetentionKey` take it wherever they exist, and `bridge.NormalizeSeeds` takes
it in place of the port table. `lag.Diff` normalizes, and `Config.Defaults` is
deleted. The switch builds its `Env` from its normalized configuration, since
`docs/architecture/2026-09-10-virtual-device-direction.md` computes both
retention keys from constructed switches. No key's content changes.
`L/lag/README.md` says a default key follows the LAG's position and shifts when
one is added.
Tests: `lag.Diff` reports nothing between `Mode: ""` and `Mode: ActiveBackup`,
which it reports today. `stp.RetentionKey` differs when `Env.Speeds` differs
for one port, and `lag.RetentionKey` when `Env.MAC` does. The diff-coverage
call sites normalize with the zero `Env`. The derive and retention suites pass
unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim src/edge/simload`

### U6. Runtime shape for every stateful layer
Files: src/common/sim/layer/bridge/, src/common/sim/layer/filter/, src/common/sim/layer/traffic/, src/common/sim/layer/mcast/, src/common/sim/device/vswitch/, src/common/sim/fabric/, src/common/sim/netmodel/, src/common/sim/internal/simtest/
After: U5
Change: `bridge.Bridge` is `bridge.Layer`, and `bridge.RetentionKey` encodes
the normalized configuration and every port's admin and oper state in the
sections `V/retention.go` parses. `filter.Layer` has `Clone`, and
`filter.RetentionKey` covers the configuration and the node identity.
`traffic.New(cfg, env)` builds a `Layer` owning one bucket per policer, with
`Admit`, `Clone`, `Copies`, and the bucket carry of `V/derive.go:52-59`.
`Copies` takes values the switch builds in place of `bridge` types, so
`traffic` imports no sibling. `mcast.Layer` loses its mutex and says the caller
serializes. `Derive` names the mcast difference through `diffDependency` over
`mcast.RetentionKey` of both sides, as for the four keyed layers. `Kept` keeps
the rule at `V/derive.go:165-173`, the name stays `config` when both keys are
empty, and no report changes until parent U9 decides `Kept` from the key.
Tests: `AssertRetentionKeyCoversConfig`
(`S/internal/simtest/diffcoverage.go:586`, unused today) runs for the two new
keys. A cloned `traffic.Layer` admits independently of its source, and a cloned
`filter.Layer` decides a frame as its source does. The mirror cases pass
through the new parameters, and the derive suite passes unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U7. Unexported fact types, one identifier per shape, one key form
Files: src/common/sim/trace/, src/common/sim/port/, src/common/sim/layer/, src/common/sim/device/vswitch/, src/common/sim/fabric/, src/common/sim/netmodel/, src/common/sim/internal/simtest/, src/common/sim/fact_contract_test.go
After: U6
Change: every `*Fact` type under `S` is unexported, and the four the Inventory
names as constructed elsewhere become functions. `lag.MemberTransitionFact`
carries `lag.member_transition` and `routing.EgressFact` carries
`routing.egress_decision`. The membership fact is `mcast`'s, takes the source,
and carries `mcast.membership_decision`. The three loop-protection facts move
to `loopprotect`, each with its own identifier. `trace` gains the composite-key
function, `S/trace/README.md` states the rule, and every site the Inventory
lists builds its key through it. The listed tests and corpus files take the new
shapes.
Tests: failing first, `filter.Diff` gives set `a/b` with rule `c` and set `a`
with rule `b/c` different subjects. The `trace` function is injective over the
pairs of `L/lag/diff_injectivity_test.go:17-20`. Each renamed fact has a case
asserting its `TypeID` differs from the one it shared.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim`

### U8. Trim the layer packages and `port`
Files: src/common/sim/layer/phy/, src/common/sim/layer/bridge/, src/common/sim/layer/lag/, src/common/sim/layer/stp/, src/common/sim/layer/loopprotect/, src/common/sim/layer/mcast/, src/common/sim/layer/routing/, src/common/sim/layer/filter/, src/common/sim/layer/traffic/, src/common/sim/port/
After: U7
Change: every unstruck Dead exports entry of these packages that no earlier
unit settled takes its rule. No file outside these directories names them.
Tests: no new test. Each suite passes, and a test that named a removed constant
asserts its literal.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/layer src/common/sim/port`

### U9. Trim the device, the fabric, and the leaves
Files: src/common/sim/device/vswitch/, src/common/sim/fabric/, src/common/sim/trace/, src/common/sim/stream/, src/common/sim/internal/simtest/
After: U7
Change: every unstruck Dead exports entry of these packages that no earlier
unit settled takes its rule. The switch satisfies `bridge.GroupResolver`
through an unexported adapter, as `lagSelector` does for `bridge.Selector`.
`simtest_test` reads a case from `DefaultRegistry().Get`.
Tests: no new test. Each suite passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim/device src/common/sim/fabric src/common/sim/trace src/common/sim/stream src/common/sim/internal`

### U10. Contract gate and fork gate reset rows
Files: test/conformance/sim/, src/common/sim/device/vswitch/fork_internal_test.go, src/common/sim/layer/README.md, src/common/sim/README.md
After: U8, U9
Change: `test/conformance/sim` reads each directory under
`src/common/sim/layer/` by path, as `test/conformance/panic` does, and writes
the declarations of the non-test files in the directory itself as lines of
text. Each declaration gets a name line (`const LayerName`, `type Layer`,
`func New`, `func Layer.Wake`, a pointer receiver's star dropped). Each
function also gets a signature line the gate builds itself: the receiver
type, one `ExprString` per parameter, and the results in parentheses whatever
their count (`func (*Layer) Clone() (*Layer)`, `func (*Layer) Wake() ()`).
One form for every count leaves no branch for a fixture to hold. Parameter
names do not appear. The checker takes a list of guards and applies the 18
rows of Inventory, Gate guards. A row tests one literal against those lines,
or against the imports and type names of every non-test file under the
directory, subdirectories other than `testdata` included, and a finding
quotes the literal. Three things change for a layer.
`Advance` needs its pointer receiver. An aliased import of `layer`, `trace`,
or `time` reads as a missing member, and no layer aliases one. The `Config`
type and an empty directory lose their own findings, since the five member
rows report both. A file that does not parse stops the gate with an error.
The 20 directories the Inventory names replace the 66 merged ones, once the
new gate has refused the 65 of those that are not `valid`. The fork gate
formats a reset field through `exportValue`, as
`assertPointerOrValueIdentical` does. The READMEs name the table as the list
of what is enforced and say a guard arrives with its fixture.
Tests: `TestLayerContract` passes on the tree and fails when it scanned no
package. `TestLayerContractGuardsRefuseTheirFixtures` runs the whole list
over each row's fixture and requires exactly the findings its table lists
for that row, written as literals. It then runs the list without that row
and requires none. It fails for a directory under `testdata/` that no row
names, other than `valid` and `valid_stateless`.
`TestLayerContractPassesValidFixtures` requires no finding for those two.
`valid` holds every item of the Inventory's list, and for each item the
commit body quotes the `--- FAIL` line that removing its exemption produces.
`TestLayerContractStopsOnUnparsableFile` writes a broken file into
`t.TempDir()`. It cannot sit under `testdata/`, since the verifier runs
`gofumpt` over every changed Go file
(`.claude/skills/verify-change/scripts/verify-change.sh:620`) and `gofumpt`
v0.11.0 exits 2 on a file that does not parse. The commit body records that
the new gate refused all 65 old directories. With `Fork` changed to copy
`filter`
(`V/switch.go:495-505`), `TestSwitchFieldsAreClassifiedAndChecked` fails on
its `resetOnFork` line with the field's name and without a panic, and the
commit body quotes that line. No test shows the table covers R1 to R3. A
reader checks its 18 rows against them.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- test/conformance/sim src/common/sim`

Waves: U1 | U2 | U3 | U4 | U5 | U6 | U7 | U8 U9 | U10

## Verification

Per unit, the verifier on the unit's paths, never `--full`. For the phase:

```bash
go test -race ./src/common/sim/... ./src/common/net/bpdu/... ./src/edge/simload/... ./test/conformance/sim/...
go list -f '{{.ImportPath}}{{range .Imports}} {{.}}{{end}}' ./src/common/sim/layer/... | grep -E ' [^ ]*/sim/(layer/[a-z]+|device|fabric)' ; test $? -eq 1
```

## Definition of done

- [ ] Verifier green for every changed path of every unit.
- [ ] READMEs under `src/common/sim` and `src/common/net/bpdu` match the code.
- [ ] Every Inventory entry is fixed with its test or struck with its reason.
- [ ] This plan's `status` is set with an outcome note under its title.
- [ ] No plan label appears in code, comments, or commit messages.

## Open questions

- Whether a default LACP key must stay fixed when a LAG is added is a question
  about IEEE 802.1AX, unverified here and not vendored under `spec/`. Phase 4
  decides it. U5 states the current rule in `L/lag/README.md`.
- Parked by drive: the fourth fix round closed the stateless gap
  (`1b6ff63b`), and its re-review ended `rework` (`2db760b7` on
  `parked/sim-p2-round4`, which holds every fix commit since the re-plan).
  The gate refuses every violation its table names. What stays open is that
  some rules of the gate can be changed with every test green: the
  `RetentionKey` row's literal, the `import_device` root, and
  `exported_fact` narrowed to stateful packages. Each round since the
  re-plan has found another such rule. Options: re-plan U10 so the property
  is executable, for example fixtures derived from each row's kind and a
  test that every row refuses in both package kinds (the reviewer's route
  and `fix-loop.md`'s, at a plan stage and another implement and review) |
  accept after fixes, with the gate's mutation coverage carried as an open
  question for phase 12's close-out (unblocks phases 3 to 8 now, the gate
  stays correct but not every rule of it is pinned). Recommended: accept
  after fixes, because no finding is a layer that breaks the contract, and
  the open work is hardening the gate's own tests.
