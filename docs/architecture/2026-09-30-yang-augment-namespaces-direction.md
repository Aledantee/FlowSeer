---
title: YANG Augment Namespaces - Direction
type: direction
date: 2026-09-30
topic: yang-augment-namespaces
status: accepted-direction
---

# YANG Augment Namespaces - Direction

A generated YANG binding keeps every node the vendored modules define,
including nodes that another module augments into a tree. The nodes one
module augments into a parent sit behind one field on the parent's Go
struct, named after that module, and the field's type is declared in that
module's own binding package. Two modules can then add a child of the same
name to one node without either replacing the other.

## Context

YANG qualifies every data node by the namespace of the module that defines
it. Two modules may each augment one target with a child of the same local
name, and both children exist on the device: NETCONF tells them apart by
XML namespace and JSON by the `module:name` member form of
[RFC 7951 §4](https://www.rfc-editor.org/rfc/rfc7951#section-4). Which of
them a device carries depends on the modules and features it advertises.

`yanggen` resolves the trees with `openconfig/goyang`, whose `Entry.Dir`
is a map keyed by local name. When an augment brings a child whose name
the target already holds, `Entry.merge` records a "Duplicate node" error
and drops the newcomer
(`github.com/openconfig/goyang@v1.6.3/pkg/yang/entry.go:1496-1522`). The
error lands on the target entry after `Modules.Process` has collected that
module's errors (`github.com/openconfig/goyang@v1.6.3/pkg/yang/modules.go:335-343`).
`Process` collects errors again only for modules with an augment it could
not apply (`modules.go:356-372`, `:383-388`), so the loss is silent.

On 2026-09-30 a probe over the three vendored trees counted 114 dropped
nodes: 113 in `cisco-iosxe`, 1 in `ruckus-icx`, none in `aruba-cx`. They
fall into two kinds:

| Kind | Examples | Outcome today |
| --- | --- | --- |
| Augment against a node the target module defines | `Cisco-IOS-XE-switch` and `Cisco-IOS-XE-ethernet` `macsec-option` against `Cisco-IOS-XE-native`, `cisco-xe-openconfig-spanning-tree-ext` `enabled-protocol` against `openconfig-spanning-tree` | The augment is always dropped |
| Augment against another augment | `Cisco-IOS-XE-ethernet` against `Cisco-IOS-XE-switch` `macsec`, `Cisco-IOS-XE-ospfv3` against `Cisco-IOS-XE-ospf` `ospf` under `ipv6`, `icx-openconfig-if-poe-aug` against `openconfig-if-poe` `poe` | goyang's map order picks the survivor, so each run can differ |

The dropped definitions are not copies. The `Cisco-IOS-XE-ethernet`
`macsec` leaf is a boolean and the `Cisco-IOS-XE-switch` one is
`type empty`, their `macsec-option/macsec` containers hold different
children, and they depend on different features
(`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-ethernet.yang:1368-1420`,
`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-switch.yang:3439-3474`). The ICX
`poe` container adds `presence "Enables POE support"`, so its existence
turns PoE on (`spec/yang/ruckus/icx/9.0.00/icx-openconfig-if-poe-aug.yang:35-69`).
The accepted
[YANG Protocol Libraries](2026-09-28-yang-protocol-libraries-direction.md)
record requires the full vendored surface, which dropped nodes break.

## Decision

- `yanggen` recovers every child goyang dropped, from the augment's own
  entry, and emits it.
- A node another module augments into a parent is reached through one
  field per augmenting module on the parent struct:
  `gi.CiscoIOSXESwitch.Macsec` and `gi.CiscoIOSXEEthernet.Macsec` are two
  fields of two types. A node from the parent's own module, or from one of
  its submodules, stays a plain field.
- The group field's type is declared in the augmenting module's binding
  package, so the target package imports the augmenting package. A cycle
  in that import graph fails generation. None exists on 2026-09-30: the
  cross-module augment graph has 257 edges and no cycle.
- The runtime schema gains a group field kind with no wire element of its
  own. The XML, JSON, and path codecs encode a group's children at the
  parent's level, qualified by the group's module.
- JSON decoding also accepts a group's child under its bare name when no
  other field of the parent has that name, and fails with an error when
  two groups hold the name and the parent does not. gNMI path elements carry no
  module (`src/protocol/gnmi/session.go:463-466`), and the gNMI row store
  builds its row JSON from them (`src/protocol/gnmi/rows.go:12-20`).

## Alternatives

- **A module suffix on colliding names only** (`MacsecCiscoIOSXESwitch`).
  Smallest change, but it needs a rule for which side keeps the plain
  name, and a new vendored module can rename an existing field by adding
  a collision.
- **Group fields with the types kept in the target package.** Same access
  paths without the cross-package import, but the augmenting package no
  longer owns the types it defines.
- **Patching or forking goyang** to key `Entry.Dir` by qualified name.
  The map is used across goyang's resolver, so a fork would carry a wide
  patch against every upstream release.

## Consequences

- Every cross-module augmented node moves one level down in its binding,
  and every binding under `generated/go/yang` changes shape.
- Importing a target package compiles every package that augments it.
  147 modules under `spec/yang/cisco/iosxe/2611` augment `/ios:native`,
  so importing `ciscoiosxenative` compiles at least that many packages
  where it compiles one today.
- A generated-name collision can no longer come from two modules, because
  each module's nodes sit behind its own field.
- A list inside a group keeps its descriptor in the package of its tree's
  top-level module, since the descriptor names a schema from every package
  on its path.
- `yanggen` output no longer depends on goyang's map order at these nodes,
  so a regeneration without a source change produces no diff there.
