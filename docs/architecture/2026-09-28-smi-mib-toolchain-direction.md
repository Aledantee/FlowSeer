---
title: SMI Parser and MIB Bindings - Direction
type: direction
date: 2026-09-28
topic: smi-mib-toolchain
status: accepted-direction
---

# SMI Parser and MIB Bindings - Direction

FlowSeer reads SNMP devices through generated MIB bindings. The bindings come
from `mibgen` (`src/protocol/snmp/cmd/mibgen`), which renders a resolved model
produced by an SMIv1/SMIv2 parser FlowSeer owns
([`src/protocol/smi`](../../src/protocol/smi/doc.go)). This record fixes why
the parser is in-house, what the generated bindings carry, and what the parser
deliberately does not do.

## The parser is ours, and leniency is the product

`src/protocol/smi` is a hand-written two-tier parser: a lexer and framer cut
each source into declarations, and a per-declaration recursive-descent parser
recovers at the frame boundary. A malformed declaration costs that declaration
and nothing else; only an unterminated string or comment, a missing module
header, or a resource limit costs a file. `mibgen` renders from the resolved
model, and the parser imports nothing from `src/protocol/snmp` so a later
runtime loader can depend on the parser without closing a cycle.

The alternative was to keep `gosmi` and patch around it. That was rejected
because the costs are structural: `gosmi` parses a `BITS` member's declared
number and discards it, panics on `DEFVAL { { } }` — a construct RFC 2578 §7.9
spells out and 29 vendored files carry — and aborts a file on its first syntax
error, since it is a participle grammar with no recovery. Two vendored IEEE
MIBs carried local patches to dodge that panic. A fork would buy the `BITS`
number and the panic fix but not declaration-level recovery, a graded
diagnostic catalogue, or a loader free of package-level state. The corpus of
about 1,700 vendored MIBs across twelve vendor and standards-body
directories is the leniency bar, and it is not an
adoption target: a vendor MIB is never edited to make the toolchain accept it.

The `gosmi` dependency survives only as a comparand in an isolated differential
module, so it cannot re-enter the main graph
([`docs/solutions/architecture-patterns/gosmi-drops-bits-member-numbers.md`](../solutions/architecture-patterns/gosmi-drops-bits-member-numbers.md)).

## Diagnostics grade, the caller sets policy

Every diagnostic carries a file, position, a stable `smi/...` code, and a
severity. The parser assigns severity and stops early only at a resource
limit (source size, declaration count and size, nesting depth, enumeration
members, diagnostic count), never because of a severity; it has no
configurable abort threshold. The catalogue
is a generated table, so a code's description and its coverage fixture are
mechanically checkable. `mibgen` fails generation on a diagnostic absent from a
committed per-module baseline and never on a recorded one, keyed on the code
and declaration name so an upstream MIB re-sync does not invalidate the
baseline (`src/protocol/snmp/cmd/mibgen/baseline.go`).

## Bindings carry what the MIBs declare

The resolved model resolves `INDEX` and `AUGMENTS` on tables: each index part
carries the column it names and that column's type, and an augmenting table
points at the table it augments (`src/protocol/smi/model.go`). `mibgen` renders
that as one typed field per index part in place of a raw OID suffix, a named Go
key type per keyed textual convention in its declaring module, and a per-table
descriptor holding the root OID, change indicator, and key type. A malformed
index suffix leaves the typed key zero, reports `KeyValid()` false, and still
delivers the row, because a decode error inside a generated walk stops the
whole table
([`docs/solutions/architecture-patterns/key-resolution-degrades-and-reports-never-fails.md`](../solutions/architecture-patterns/key-resolution-degrades-and-reports-never-fails.md)).

Every naming node under the enterprises subtree of a configured module is
aggregated into one generated identity package,
`generated/go/mib/sysobjectid`, which resolves a `sysObjectID` to the deepest
known node. The alternatives lost on the same point: a hand-curated
family-to-mapper table, hand-written index decoding in mappers, and runtime
profiles all restate facts the MIBs already carry and drift from them.
Which mappers apply to a device is decided by per-mapper detection, not by a
generated table.

Generated table walks retrieve only selected columns through the runtime
`WalkColumns` helper, and bulk walks decode a varbind only when it is yielded;
the watcher keeps its own full-walk snapshot semantics.

## Landed

Landed 2026-09-01: the hand-written parser replaced `gosmi` in
`src/protocol/smi`, `mibgen` rendered from the resolved model, and the two
patched IEEE MIBs were restored. Landed 2026-09-03: generated table walks
stream selected columns instead of buffering the table root. Landed
2026-09-05: `INDEX` and `AUGMENTS` resolution in `src/protocol/smi/model.go`,
typed row keys and per-table descriptors in `src/protocol/snmp`, and the
`generated/go/mib/sysobjectid` identity package. Landed 2026-09-27: bulk walks
decode varbinds lazily in `src/protocol/snmp/session_engine.go`.
