---
title: YANG Protocol Libraries - Direction
type: direction
date: 2026-09-28
topic: yang-protocol-libraries
status: accepted-direction
---

# YANG Protocol Libraries - Direction

FlowSeer reads model-driven devices through three protocols that share one
schema language. The vendored YANG trees under `spec/yang/` are compiled once
by `yanggen` (`src/protocol/yang/cmd/yanggen`) into typed Go bindings under
`generated/go/yang`, and three sibling libraries put those bindings on the
wire: NETCONF, RESTCONF, and gNMI.

This record fixes the shape of that layer because it crosses four packages and
a generated tree, and because a later consumer has to know which protocol
library to reach for. The package contract lives in
[`src/protocol/yang/doc.go`](../../src/protocol/yang/doc.go); this record states
the decisions the code does not.

## One runtime, three protocols, no facade

`src/protocol/yang` is the meeting point of three parties that never import
each other. Generated binding packages implement its codec surface and emit
descriptors. The protocol libraries accept its paths, values, and descriptors
and own their wire form: NETCONF keeps the candidate/commit model, RESTCONF
keeps immediate edits and ETags, gNMI keeps Get/Set/Subscribe. Callers connect
the two by handing a generated descriptor to a library constructor.

The alternative, a protocol-transparent `Get(path)`/`Set(path)` session over
all three, was rejected because the protocols diverge exactly where
transactions matter. A common session either lies about candidate/commit,
immediate edit, and Set semantics or re-exposes each protocol through escape
hatches; the device service reaches devices through integrations for the same
reason ([device service and inventory](2026-08-20-device-service-and-inventory-direction.md)).

A consequence worth stating: generated code imports only the public API of
`src/protocol/yang`, and no protocol library imports a generated package.
That keeps the 1,051-package binding tree out of every consumer's import graph.
The bindings are their own Go module,
`go.aledante.io/FlowSeer/generated/go/yang`, so the root module's
`go build ./...` and `go vet ./...` no longer compile the whole tree; the
module path keeps the root prefix, so import paths are unchanged
(`src/protocol/README.md`).

## The code generator is in-house

`yanggen` resolves the vendored trees with `openconfig/goyang` and emits
package-level declarations and static schema data through `jennifer`, in the
same shape as `mibgen`. Generated behavior that differs only in type parameters
lives in the runtime as a generic helper, not in repeated emitted code — for
example `StructRowCodec` and `NestedRowCodec` in
`src/protocol/yang/structops.go`. Generated names follow Go naming: the package
is part of the identity, so a type takes the shortest suffix unique in its
package, and one type is emitted per distinct subtree shape. Both rules are in
[`docs/code-style.md`](../../docs/code-style.md) under Project layout.

Adopting `ygot` lost because it emits no NETCONF XML and fails outright on the
full IOS-XE native tree (`openconfig/ygot` issue 888), and because its output
style conflicts with the generated-code conventions. A typed-core-plus-dynamic
hybrid and a module allowlist lost because the full vendored surface is the
requirement, which is why every vendored module is generated.

Drift between `spec/yang` and `generated/` is tracked by a lockfile of
per-module closure hashes plus one generator version
(`src/protocol/yang/cmd/yanggen/lockfile.go`), not by regenerating and
byte-diffing the whole tree. A closure hash covers the module's source and
every module that can change its output, including modules that augment or
deviate it.

## State reads extend the collection primitives

Standing reads on the three libraries surface through the same lifecycle the
SNMP library defines ([`src/protocol/snmp/doc.go`](../../src/protocol/snmp/doc.go)):
a bounded Walker over one subtree and a Watcher that diffs rows between ticks
for NETCONF and RESTCONF, or wraps a gNMI Subscribe stream. The unit of
currency between generated code and a primitive is a `ListDescriptor` pairing a
list's path with its row codec, so a library decodes rows without importing
generated packages. One-shot read helpers were rejected in favor of the same
lifecycle every other collection primitive has.

## Lab hardware is the validation authority

Conformance capture runs against real devices, the way the SNMP quirk corpus
does. NETCONF and RESTCONF validate against IOS-XE and ICX; gNMI validates
against an Arista vEOS-lab node, because AOS-CX gNMI is telemetry-oriented and
no lab Aruba serves the protocol. The Aruba-specific write criterion is a
recorded accepted-risk gap in the gNMI conformance corpus, and Set is proven on
Arista instead. Container-tier reference servers (netopeer2, clixon, a gNMI
reference target) gate the libraries before lab work; live-device suites are
env-var gated and skip when unset.

## Landed

Landed 2026-08-21: the RESTCONF library validated against a Ruckus ICX7150 in
`src/protocol/restconf`. Landed 2026-09-18: NETCONF and gNMI validated against
a Cisco CSR1000v and an Arista vEOS-lab node in `src/protocol/{netconf,gnmi}`;
the gNMI validation authority and the Aruba write gap recorded. Landed
2026-09-26: the generated-binding size change gave one type per subtree shape
and package-qualified names across `generated/go/yang`. Landed 2026-09-28:
`generated/go/yang` became a nested Go module.
