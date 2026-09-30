---
title: YANG Augment Namespaces, Phase 2 - Generator Recovery and Group Emission - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-30-1129-fix-yang-augment-namespaces-plan.md
---

# YANG Augment Namespaces, Phase 2 - Generator Recovery and Group Emission - Plan

## Goal

`yanggen` recovers each child goyang dropped from the augmenting module's
own augment entry and emits the nodes each module augments into a parent
as that module's group, typed in its package, using phase 1's group field.
The runtime decodes a bare JSON member into a group when no other field of
the parent has that name and fails on an ambiguous one, so gNMI rows keep
decoding augmented nodes and a collision never drops a value silently. The
bindings are regenerated, and the doc calling the duplicate-augment diff
noise is removed. Stop condition: a dropped child whose augment entry does
not hold it, which leaves no source to recover it from.

## Decisions

- The parent plan's Decisions govern, and the direction record gains the
  JSON rule and descriptor placement below. Bare `.go` names sit under
  `src/protocol/yang/cmd/yanggen`, except goyang's `entry.go` and
  `modules.go` (`github.com/openconfig/goyang@v1.6.3/pkg/yang`).
- One plan, no sub-phases. Why: U5's gNMI test needs U1's runtime change
  and U4's bindings, so the units form one cluster.
- Recovery reads `goyang.ToEntry` of every `*goyang.Augment` in each module
  and standalone submodule after `Process` returns no error. Why: goyang
  caches that entry with its full `Dir` (`entry.go:553-566`, `:739-743`),
  and `merge` copies a child before the duplicate test, so the dropped
  original stays there (`entry.go:1496-1522`). `target.Augmented` holds
  shallow copies (`entry.go:1121`, `:1446-1458`). A `Process` error fails
  as today (`load.go:251-258`). After a clean one, "Duplicate node" errors
  sit unreported, since goyang re-collects only for unapplied augments
  (`modules.go:383-388`).
- A child is merged when the target's entry of that name, unwrapped from an
  implied case (`entry.go:1263-1286`), has the same `Node` and the same
  instantiating module. Why the module test: `tailf-confd-monitoring2`
  augments `/tfcm:confd-state` with a grouping `tailf-confd-monitoring`
  also uses, so both copies share one `Node`
  (`spec/yang/cisco/iosxe/2611/tailf-confd-monitoring2.yang:29-30`).
- A target with no entry of that name holds a drop only when it carries a
  "Duplicate node" error for it. Why: `cisco-xe-openconfig-spanning-tree-ext`
  deviates `oc-stp`'s `enabled-protocol` away and augments its own
  (`spec/yang/cisco/iosxe/2611/cisco-xe-openconfig-spanning-tree-ext.yang:34-56`).
- Recovery covers the data tree only. Why: the emitter skips RPC, action,
  and notification nodes (`emit_module.go:628-636`, `:649`).
- Cross-check: at each data-tree entry, the "Duplicate node" errors naming
  a child (format at `entry.go:1507`) equal the recovered children of that
  name. Why: a goyang change that loses children another way then fails.
- Emission never reads `Entry.Path()`. It keys nodes by a module-qualified
  data path in RFC 7951 member form, the module named where it changes:
  `/fixture-main:servers/server/fixture-aug:owner`. This covers comments,
  sort keys, tie-breaks, and discriminators (`emit_module.go:247-260`,
  `:350`, `:535`, `naming.go:278`, `:431`, `:471-533`). Why: a recovered
  child's `Path()` embeds the augment argument (`entry.go:1386-1391`), and
  where no contender belongs to the parent's module, goyang's map order
  picks the survivor (`modules.go:350-364`).
- Children per node: the parent-module children by name, then one group
  per other module by module name. A node's types live in its module's
  package, a group's in the group module's package. Why: it matches the
  runtime iterator (`src/protocol/yang/schema.go:130-162`), and no key
  depends on which contender goyang kept.
- Group field: Go name `camel(module)` (`CiscoIOSXESwitch`), literal
  `{Child: <pkg>.<Name>AugmentSchema, GoName: ..., Group: true}`
  (decided by the user, 2026-09-30). Why: the direction record's example uses it, and module
  names are unique in a vendor. A clash falls to `nameScope`'s suffix.
- Group struct: the shortest unique suffix of the target's path plus
  `Augment` (`ciscoiosxeswitch.GigabitEthernetAugment`), its shape key
  hashing the target's name, the module, and the children (decided by the user, 2026-09-30).
  Why: name candidates come from a suffix every instance shares
  (`naming.go:428-440`), which a shared target name guarantees.
- Group schemas are exported, with no runtime root guard, and documented
  as no codec root (decided by the user, 2026-09-30). Why: the target package names them.
- Key, FlatRow, and Descriptor for a list inside a group stay in the
  package of the tree's top-level module. Why: a descriptor names every
  schema on its path, and the augmenting package cannot import the target
  package that imports it.
- Every Go import then points at a package whose module augments a node on
  the path, and an augmenting module imports every module its path names.
  A Go cycle thus needs the circular imports RFC 7950 §5.1 forbids
  (https://www.rfc-editor.org/rfc/rfc7950#section-5.1). The check stays,
  since goyang loads modules that import each other (U3's test).
- A chunk imports the binding packages its fragments reference, at the
  output module path plus vendor and package. Goldens use
  `go.aledante.io/FlowSeer/src/protocol/yang/cmd/yanggen/testdata/golden/fixture`.
  Why: the `yang.` substring test (`emit_module.go:123`) cannot see them.
- `generatorVersion` becomes `yanggen-6` (`lockfile.go:17`). The closure
  hash is unchanged. Why: an augmenting module imports what its bindings
  reference, and reverse edges (`load.go:437-446`) reach every augmenter.
- JSON decode also matches a grouped member by its bare name when no plain
  field and no other group of the parent has that name. A bare name that
  two or more groups hold, with no plain field of that name, fails decoding
  with an error naming the candidates (decided by the user, 2026-09-30). This
  reverses phase 1's qualified-only rule. Why: gNMI path elements carry no
  module (`src/protocol/gnmi/session.go:463-466`), the row store builds its
  JSON from them (`src/protocol/gnmi/rows.go:12-20`), and
  `src/protocol/gnmi/watch_test.go:362` would read a nil group. The decoder
  took bare augmented members before groups
  (`src/protocol/yang/structcodec_test.go:207-216`).
- Recovery runs in `LoadVendor` after `parseModules` and sorts each
  target's recovered children by module, then name (decided by the user, 2026-09-30). Why:
  `-verify` and `-check` then run the same checks as `-update`.

## Requirements

1. Every "Duplicate node" error goyang records during `Modules.Process`
   corresponds to one emitted node. A dropped child that cannot be matched
   to an augment entry fails generation with a `LoadError` naming the
   target path and both source positions. Example: `yanggen -verify`
   prints `113 recovered` for `cisco-iosxe`, `1 recovered` for
   `ruckus-icx`, and `0 recovered` for `aruba-cx`.
2. An augment or deviation whose target path passes through a recovered
   node fails generation, since goyang resolved it against the other
   sibling. A path segment at a collided position must name the parent's
   own module, and goyang must have kept that node. Example: `t` defines
   `/t:c/k`, module `b` adds its own `k` to `/t:c`, and module `d`
   augments `/t:c/b:k`, so generation fails naming `d`'s path.
   `Cisco-IOS-XE-dhcp`'s `/ios:native/ios:interface/ios:ATM/ios:ip/ios:dhcp`
   passes, since `ios:ip` is native's own `ip`.
3. A cycle in the binding-package import graph fails generation, naming
   the packages on the cycle.
4. A `testdata/modules` fixture with two modules augmenting one node with a
   same-named child, plus one augmenting a node the target defines,
   generates both group fields in golden output under
   `src/protocol/yang/cmd/yanggen/testdata/golden/fixture`. Example:
   `fixturemain.ServersServer` has `FixtureAug *fixtureaug.ServerAugment`
   and `FixtureAug2 *fixtureaug2.ServerAugment`, and each group struct has
   its own `Owner`.
5. Two consecutive `yanggen -update` runs leave `git status` clean.
6. `go build` of a package importing `ciscoiosxenative` is timed before and
   after, and the result is recorded in the parent plan's outcome note.
7. A bare JSON member decodes into a group when its name is unique among
   the parent's fields. Example: with the phase 1 fixture
   (`src/protocol/yang/schema_test.go:30-50`), `{"y":"v"}` sets `B.Y`,
   `{"x":0}` sets `X` and leaves `B` and `C` nil. With groups `B` and `C`
   both holding `z` and no plain `z`, `{"z":1}` fails with an error naming
   both modules.
8. A relative leafref that climbs out of a contender from a module other
   than the parent's fails generation, since `Entry.Find` would resolve it
   against the augment entry and fall back to string (`emit_type.go:59-62`).

## Out of scope

- A runtime error for a group schema passed as a codec root.
- gNMI path elements carrying a `module:` prefix.
- RPC, action, and notification nodes, which `yanggen` does not emit.
- `yanggen` reads trusted vendored trees under `spec/yang/`. The runtime
  decoder reads untrusted device payloads, and U1 changes only which field
  a bare name selects, with no new allocation or recursion.

## Units

### U1. Bare grouped JSON members
Files: src/protocol/yang/structjson.go, src/protocol/yang/nested.go, src/protocol/yang/structcodec_test.go, src/protocol/yang/nested_test.go, docs/solutions/conventions/nested-json-row-lookup-must-match-struct-decoding.md
After: none
Change: `lookupJSONField` (`src/protocol/yang/structjson.go:209-222`)
receives the parent schema and applies requirement 7. `decodeJSONObject`
and `findJSONDescendant` (`src/protocol/yang/nested.go:301-340`) keep
sharing it. The solution doc states the new rule and quotes the new code.
Tests: `structcodec_test.go` covers requirement 7 with the shared
fixture, plus groups `B` and `C` both holding `z` with no plain `z`:
`{"z":1}` returns the ambiguity error. `nested_test.go` renames
`TestDecodeJSONNestedDoesNotMatchBareGroupedContainer` to assert that the
bare `c` now yields the row and allocates `B`. It adds an ambiguous case
returning the ambiguity error. `TestDecodeJSONNestedRejectsCrossModuleBareRows` stays
green unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/structjson.go src/protocol/yang/nested.go src/protocol/yang/structcodec_test.go src/protocol/yang/nested_test.go docs/solutions/conventions/nested-json-row-lookup-must-match-struct-decoding.md`

### U2. Recovery and its checks
Files: src/protocol/yang/cmd/yanggen/recover.go, src/protocol/yang/cmd/yanggen/recover_test.go, src/protocol/yang/cmd/yanggen/load.go, src/protocol/yang/cmd/yanggen/main.go, src/protocol/yang/cmd/yanggen/main_test.go
After: none
Change: `LoadVendor` recovers dropped children as Decisions describe and
stores them on `VendorSet`, keyed by target entry. It applies requirements
1, 2, and 8. A recovered child in the survivor's module fails as a
same-namespace duplicate. Each failure is a `LoadError` naming the target
path and the `goyang.Source` positions. `-verify` appends `, N recovered`
to each vendor line.
Tests: `recover_test.go` writes modules to `t.TempDir()`, as
`load_test.go:123-144` does. Two modules each adding leaf `x` to one
container: across 20 loads, survivor plus recovered always cover both.
An augment adding `name` beside the target's own `name`: one recovered
child. A deviated-away survivor (the `enabled-protocol` shape): one
recovered child. A shared grouping (the `tailf` shape): all grouping
children recovered. Requirement 2's example, a same-module duplicate, a
`../../name` leafref in a dropped container, and an entry given a
synthetic "Duplicate node" error with no dropped child each yield a
`LoadError` naming the path. A leaf augmented into an RPC input is ignored.
`main_test.go`: `TestRunVerifyRealTrees` asserts requirement 1's counts,
and `TestRunVerifyFixture` asserts `0 recovered`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/cmd/yanggen/recover.go src/protocol/yang/cmd/yanggen/recover_test.go src/protocol/yang/cmd/yanggen/load.go src/protocol/yang/cmd/yanggen/main.go src/protocol/yang/cmd/yanggen/main_test.go`

### U3. Vendor data view
Files: src/protocol/yang/cmd/yanggen/view.go, src/protocol/yang/cmd/yanggen/view_test.go
After: none
Change: a builder takes a vendor's modules and a recovered-children map
(`map[*goyang.Entry][]*goyang.Entry`) and returns each data node's
module, qualified data path, and children per the Children rule.
Choice and case layers flatten as `dataChildren` does
(`emit_module.go:640-658`), and recovered children of a flattened choice
or case join the enclosing node. A node's path is computed from its view
position, never from its `Entry` parents. The builder fails on a cycle
among (parent package, group package) edges, naming the packages in
order.
Tests: `view_test.go` on the fixture tree. `servers/server` has
parent-module children and one `fixture-aug` group holding `owner`, at
`/fixture-main:servers/server/fixture-aug:owner`, and `by-owner-b` has a
`fixture-aug` group holding `flag`. Passing `fixture-aug`'s top-level
`sub-typed` leaf as a recovered child of `server` puts it in that group at
`/fixture-main:servers/server/fixture-aug:sub-typed`. Temp modules `cyc-a`
and `cyc-b` import each other and augment each other's container, and the
builder fails naming `cyca` and `cycb`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/cmd/yanggen/view.go src/protocol/yang/cmd/yanggen/view_test.go`

### U4. Group emission
Files: src/protocol/yang/cmd/yanggen/naming.go, src/protocol/yang/cmd/yanggen/emit_module.go, src/protocol/yang/cmd/yanggen/emit.go, src/protocol/yang/cmd/yanggen/lockfile.go, src/protocol/yang/cmd/yanggen/doc.go, src/protocol/yang/cmd/yanggen/emit_test.go, src/protocol/yang/cmd/yanggen/golden_roundtrip_test.go, src/protocol/yang/cmd/yanggen/load_test.go, src/protocol/yang/cmd/yanggen/main_test.go, src/protocol/yang/cmd/yanggen/testdata/modules/fixture-aug2.yang, src/protocol/yang/cmd/yanggen/testdata/golden/fixture, docs/solutions/conventions/yanggen-output-depends-on-goyang-augment-order.md, docs/solutions/README.md
After: U2, U3
Change: `Emit` builds the U3 view once per vendor from `VendorSet`'s
recovered children. It resolves names for every package, then writes each
package. Shape keys add a
`group:<module>:<GoName>:<group shape key>` signature per group. Struct,
schema, and group placement, naming, descriptors, imports, and the version
follow Decisions. Plain fields no longer carry `Module:`. The golden seam
takes the vendor set and an import base. `fixture-aug2` augments
`/fm:servers/fm:server` with leaf `owner` (`uint8`) and leaf `port`
(`string`), and `/fm:servers` with list `mirror` keyed by `id`. `doc.go`
describes recovery, groups, and descriptor placement, and drops the
non-determinism paragraph (`doc.go:47-49`). The solution doc and its
`docs/solutions/README.md` row are deleted.
Tests: goldens regenerated with `-update-golden` and audited for
requirement 4, the server's own `Port` beside `fixtureaug2`'s, and
`MirrorDescriptor` in `fixturemain` returning `fixtureaug2.Mirror` rows.
`TestGoldenPackagesBuild` compiles the cross-package imports.
`golden_roundtrip_test.go` round-trips a server whose two groups each set
`owner`, in XML and in JSON (`"fixture-aug:owner"`, `"fixture-aug2:owner"`).
`emit_test.go`: `TestEmitAugmentModule` asserts the group field in place
of `Module: moduleFixtureAug`. A new test loads the fixture 20 times and
gets byte-identical output, with the `owner` survivor random per load.
`load_test.go` and `main_test.go` expect six modules, and
`TestRunVerifyFixture` expects `2 recovered`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/yang/cmd/yanggen docs/solutions/conventions/yanggen-output-depends-on-goyang-augment-order.md docs/solutions/README.md`

### U5. Regenerate the bindings
Files: generated/go/yang, src/protocol/gnmi/watch_test.go, docs/plans/2026-09-30-1129-fix-yang-augment-namespaces-plan.md
After: U1, U4
Change: first, with the committed bindings, time a cold build:
`cd generated/go/yang && GOCACHE=$(mktemp -d) /usr/bin/time -l go build ./cisco-iosxe/ciscoiosxenative`.
Then regenerate with `go run ./src/protocol/yang/cmd/yanggen -update`
(never by hand), run it again, and repeat the timing. The parent's outcome
note gets both wall times and peak RSS. `watch_test.go` reads
`ev.Row.OpenconfigIfAggregate.Aggregation.State.Member`.
Tests: `go test -race ./src/protocol/gnmi/` passes, which proves
requirement 7 through the row store. `go vet -tags yang_integration_t4
./src/protocol/netconf/test/integration/ ./src/protocol/restconf/test/integration/`
compiles the lab tests. They read only own-module fields (`Native.Hostname`,
`Native.Version`, `System.State.Hostname`), so they need no edit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- generated/go/yang src/protocol/gnmi/watch_test.go docs/plans/2026-09-30-1129-fix-yang-augment-namespaces-plan.md`
(the verifier builds the bindings module and lints its sample packages).

Waves: U1 U2 U3 | U4 | U5

## Verification

```bash
go test -race ./src/protocol/yang/...
go test -race ./src/protocol/gnmi/
go run ./src/protocol/yang/cmd/yanggen -update
go run ./src/protocol/yang/cmd/yanggen -update
git status --porcelain generated/go/yang   # empty
go run ./src/protocol/yang/cmd/yanggen -check
```

Parent requirement 3, by hand: the native `GigabitEthernet` schema names
group schemas in `ciscoiosxeswitch` (`macsec` typed `yang.TEmpty`) and
`ciscoiosxeethernet` (`yang.TBool`). Never run `verify-change.sh --full`.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `yanggen -check` passes, and two `-update` runs leave no diff.
- [ ] Timings recorded in the parent plan's outcome note.
- [ ] This plan's `status` set with an outcome note, and the parent's
      `Landed:` line for U2 filled.
- [ ] No plan labels in code.

## Open questions

- Requirement 1 is read as the data tree. `Process` also drops children
  augmented into RPC input and output (`ietf-ipv4-unicast-routing` and
  `ietf-ipv6-unicast-routing` under `ietf-routing` `fib-route`), which
  `yanggen` never emits.
