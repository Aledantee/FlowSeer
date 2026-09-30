---
title: YANG Augment Namespaces - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
---

# YANG Augment Namespaces - Plan

## Goal

Every node the vendored YANG trees define appears in `generated/go/yang`,
including the 114 nodes goyang drops today when two modules add a child of
the same name to one node. The nodes a module augments into a parent sit
behind one group field named after that module, typed in that module's
package, and the runtime codecs put a group's children on the wire at the
parent's level. Stop condition: a vendored tree whose cross-module augment
graph has a cycle, which this layout cannot import.

## Decisions

- Recover, place, and name augmented nodes as
  `docs/architecture/2026-09-30-yang-augment-namespaces-direction.md`
  proposes. Why: it records the evidence and the rejected alternatives,
  and the choice outlives this plan.
- Every cross-module augmented node goes behind its module's group field,
  not only the 114 colliders (decided by the user, 2026-09-30). Why: the
  access path then never depends on whether a collision exists, so adding
  a vendored module cannot rename an existing field.
- Group types are declared in the augmenting module's package (decided by
  the user, 2026-09-30). Why: the package that binds a module owns every
  type that module defines.
- Two phases: the runtime group field kind, then the generator and the
  regenerated bindings. Why: they are separate packages
  (`src/protocol/yang` and `src/protocol/yang/cmd/yanggen`), and the
  emitted code needs the runtime kind to exist.
- An import cycle among binding packages fails generation, with no
  fallback layout. Why: the cross-module augment graph of all three
  vendors has 257 edges and no cycle (probe over `LoadVendors` output,
  2026-09-30), so a fallback would be dead code.

## Requirements

1. A runtime schema with a group field encodes the group's children as
   members of the parent. Example: a parent in module `a` with group
   `B` holding leaf `x` in module `b` marshals to `{"b:x":1}` with
   `MarshalJSON7951Struct` and to `<x xmlns="urn:b">1</x>` inside
   `<parent xmlns="urn:a">` with `MarshalXMLStruct`, and decodes back to
   the same struct.
2. A group may hold a child whose local name the parent or another group
   also uses. Example: parent leaf `x` and groups `B` and `C` each holding
   leaf `x` decode `x` into `X`, `b:x` into `B.X`, and `c:x` into `C.X`,
   in XML and in JSON.
3. `yanggen` emits every node goyang dropped. Example: the
   `GigabitEthernet` struct in `ciscoiosxenative` carries group fields
   for both `Cisco-IOS-XE-ethernet` and `Cisco-IOS-XE-switch`, each with
   its own `Macsec` field, typed `yang.TBool` and `yang.TEmpty`
   respectively in the emitted schema.
4. Two `yanggen -update` runs on an unchanged tree produce identical
   output.

## Out of scope

- Vendor semantics of which variant a device implements. Callers choose
  the group that matches the module the device advertises.
- Deviations. None of the vendored deviations targets a node inside a
  dropped subtree (grep of `spec/yang/cisco/iosxe/2611` and
  `spec/yang/ruckus/icx/9.0.00`, 2026-09-30).
- `yanggen` reads the vendored trees under `spec/yang/`, which the
  repository's own authors commit, so it does not defend against hostile
  input.

## Units

### U1. Runtime group field kind
Files: docs/plans/2026-09-30-1129-fix-yang-augment-namespaces-phase1-plan.md
After: none
Landed: `d8cb4540..cca18ef1`

### U2. Generator recovery and group emission
Files: docs/plans/2026-09-30-1129-fix-yang-augment-namespaces-phase2-plan.md
After: U1
Landed:

Waves: U1 | U2

## Verification

Each phase runs the verifier on its changed paths. After U2,
`go run ./src/protocol/yang/cmd/yanggen -update` twice in a row leaves no
diff, and `go run ./src/protocol/yang/cmd/yanggen -check` passes.

## Definition of done

- [ ] Both phases read `implemented` with their `Landed:` ranges filled.
- [ ] The direction record is accepted, or amended to what landed.
- [ ] `docs/solutions/conventions/yanggen-output-depends-on-goyang-augment-order.md`
      and its row in `docs/solutions/README.md` are removed.
- [ ] No plan labels in code.

## Open questions

- Whether the compile cost of importing `ciscoiosxenative` (147
  augmenting packages) is acceptable. The cold build took 51.32 s wall time
  and 11,819,909,120 bytes peak RSS before regeneration, then 34.53 s wall
  time and 10,748,739,584 bytes peak RSS after regeneration.
