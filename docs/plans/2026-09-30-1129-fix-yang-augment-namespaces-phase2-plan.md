---
title: YANG Augment Namespaces, Phase 2 - Generator Recovery and Group Emission - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-30-1129-fix-yang-augment-namespaces-plan.md
---

# YANG Augment Namespaces, Phase 2 - Generator Recovery and Group Emission - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`yanggen` emits every node the vendored trees define. After
`Modules.Process` it recovers each child goyang dropped, from the
augmenting module's own augment entry. It emits the nodes each module
augments into a parent as that module's group, typed in that module's
package, using the runtime group field kind from phase 1. The bindings
under `generated/go/yang` are regenerated, the hand code that reads
augmented nodes follows the new paths, and the documentation that calls
the duplicate-augment diff noise is removed.

## Decisions

- The parent plan's Decisions govern.
- Recovery reads `yang.ToEntry` of each `*yang.Augment` in every module.
  goyang caches that entry with its full `Dir`, and `Entry.merge`
  duplicates children before attaching them, so the dropped subtree
  survives there (`github.com/openconfig/goyang@v1.6.3/pkg/yang/entry.go:1095-1122`,
  `github.com/openconfig/goyang@v1.6.3/pkg/yang/entry.go:1496-1522`).
  A probe on 2026-09-30 found a non-empty `Dir` on 2077 `cisco-iosxe`,
  47 `ruckus-icx`, and 17 `aruba-cx` augment entries.
- Emission walks a merged child list per node, sorted by module name and
  then local name. The name registry and list ordering key a node by its
  module-qualified data-tree path, not `Entry.Path()`. Why: a recovered
  child's parent is the augment entry, so its `Entry.Path()` embeds the
  augment argument (`github.com/openconfig/goyang@v1.6.3/pkg/yang/entry.go:1386-1391`)
  and is no data-tree path, while `src/protocol/yang/cmd/yanggen/emit_module.go:260`,
  `:350`, `:393`, and `:408` key on `Entry.Path()`.
- Recovery reads `ToEntry(*Augment).Dir`, not `target.Augmented`. Why:
  `shallowDup` drops grandchildren from `Augmented`
  (`github.com/openconfig/goyang@v1.6.3/pkg/yang/entry.go:1121`,
  `:1446-1458`).

## Requirements

1. Every "Duplicate node" error goyang records during `Modules.Process`
   corresponds to one emitted node. A dropped child that cannot be
   matched to an augment entry fails generation with a `LoadError`
   naming the target path and both source positions.
2. An augment or deviation whose target path passes through a recovered
   node fails generation, since goyang resolved it against the other
   sibling. None exists on 2026-09-30.
3. A cycle in the binding-package import graph fails generation, naming
   the packages on the cycle.
4. A `testdata/modules` fixture with two modules augmenting one node with
   a same-named child, plus one augmenting a node the target defines,
   generates both group fields in golden output under
   `src/protocol/yang/cmd/yanggen/testdata/golden/fixture`.
5. Two consecutive `yanggen -update` runs leave `git status` clean.
6. `go build` of a package importing `ciscoiosxenative` is timed before
   and after, and the result is recorded in the parent plan's outcome
   note.

## Open questions

- Whether group schemas are exported package variables like every other
  shape (`src/protocol/yang/cmd/yanggen/emit_module.go:341-344`). An
  exported one passed as a root to `MarshalXMLStruct` or
  `SubtreeDescriptor` has an empty `Name` and matches no element.
- A target module with an unresolvable augment stays in goyang's pending
  list, and its "Duplicate node" errors then fail `parseModules`
  (`src/protocol/yang/cmd/yanggen/load.go:251-258`). Recovery must run
  on the errors that path does not already surface.
- The group field's Go name: the camel-cased module name
  (`CiscoIOSXESwitch`) or the module's `Package` from `LoadedModule`.
- Which hand-written callers change. On 2026-09-30 the importers of
  `generated/go/yang` outside `generated/` were
  `src/protocol/gnmi/watch_test.go` and the `t4` lab tests under
  `src/protocol/gnmi`, `src/protocol/netconf`, and
  `src/protocol/restconf`.
- Removing `docs/solutions/conventions/yanggen-output-depends-on-goyang-augment-order.md`,
  its row in `docs/solutions/README.md`, and the non-determinism paragraph
  in `src/protocol/yang/cmd/yanggen/doc.go` belongs in the unit that makes
  output deterministic.
