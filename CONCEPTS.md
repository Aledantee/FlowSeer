# Concepts

Shared domain vocabulary for this project — entities, named processes, and status concepts with project-specific meaning. Seeded with core domain vocabulary, then grows as the `compound` skill proposes terms from verified lessons; direct edits are fine. Glossary only, not a spec or catch-all.

## Inventory

### Relationships

A Binding joins one Device to one Integration and never changes either end. A Placement joins one Device to one Integration Scope for a dated span. An Attribute Assignment joins one owner entity to one Attribute Definition. Scopes belong to their Integration; Tags form their own tree independent of all of these.

### Device

The physical box FlowSeer manages, independent of every integration that reaches it. Its identity is a FlowSeer-assigned UUID; the vendor serial and chassis MAC are correlation data the service merges sightings on, never identity, because addresses and platform ids move between boxes.

Lifecycle and reachability are separate axes: reachability is per Binding and heals on its own, while a Device lifecycle change is an operator action or an explicit policy. A retired Device keeps its history and refs; a reappearing serial un-retires it instead of creating a duplicate.

### Integration

A configured adapter instance — the cloud tenant, controller, or edge agent through which FlowSeer reaches devices. Kinds are code, instances are data: each first-party kind carries its own typed configuration, while all third-party kinds share one descriptor-typed configuration told apart by the kind's announced name.

### Binding

One path to a Device through one Integration — "device X is reachable via integration Y at address Z". A Device can have many; routing reads only the Binding. Its status is machine-owned end to end: reachability flaps and heals with nobody acting, and no status value ever means the device is removed. A relationship change is expressed as retiring the old Binding and creating a new one, never by retargeting.

### Integration Scope

The platform's own hierarchy — a controller's network, zone, or site — mirrored generically inside FlowSeer. Scopes are synced from the platform and never operator-intended; they are keyed by the platform's own identifier under the owning Integration, because the platform can rename or drop them on any sync.

### Placement

The dated, append-only record that a Device belongs to an Integration Scope, with who asserted it (an operator, or derivation from the scope the device appeared under — operator wins). A move is a new Placement plus a close of the old one; a closed Placement never reopens, and Placements are never removed.

### Tag

An operator-curated hierarchical grouping label. Each Tag names at most one parent; the point of the hierarchy is rollup — anything scoped to an ancestor also matches everything tagged under its descendants. Sibling names are unique and the parent chain stays cycle-free.

### Attribute Definition

An operator-defined, typed attribute: a display name, one value type (free text, number, closed vocabulary, or entity reference), the entity kinds it may attach to, and bounds on how many values one assignment holds. Edits that would invalidate existing values are gated by an explicit operator decision to drop them or abandon the edit.

### Attribute Assignment

The values one entity carries for one Attribute Definition. At most one Assignment exists per owner and definition, attachment implies at least one value, and the Assignment shares its owner's lifecycle. An Assignment's owner and definition never change; only its values do.

### Capability

A device-functionality area a Binding advertises as reachable. Capability sets stay open to values a reader does not know: capabilities are device-reported, so an older core must tolerate kinds introduced by newer writers.

### Provenance

The origin of one live response or event — which Binding answered and when the answering integration observed the payload. Provenance rides response and event envelopes, never the entity messages themselves.

### Entity Reference

A reference to one entity whose kind is decided at runtime, as a kind plus an id. Used only where the target's kind is genuinely dynamic — a statically-known target keeps its typed ref pair. Admission of a kind to the dynamic-reference vocabulary is a contract: the entity must be UUID-identified, answer existence checks, and cascade attribute values that reference it when deleted.

## Runtime

### Service Module

A supervised runtime unit within one service. A leaf owns one setup function; a branch owns a supervisor containing one or more child modules. Its full path within the service is stable identity for gates, durable messaging, and telemetry. This is separate from a MIB Module, which is an SMI definition block.

### Local Bus

The private, file-backed message bus a service runs for its own Service Modules: listener-free, scoped to one process, and persisting messages so inter-module work resumes after a crash, reboot, or upgrade. Delivery is at-least-once, so a handler must tolerate seeing the same message twice. Durability is a service-wide runtime setting: the default survives a process kill but may lose the most recent writes on power loss, and a service that needs every acknowledged record to survive a power cut opts into flushing per message.

### Runtime Manifest

The persisted record of what a Local Bus store *is*: the service identity, its module paths, subscriptions, subject encoding, and broker provenance. Startup compares the stored manifest with the running binary and accepts only additive changes; anything else marks the store migration-required. Because the comparison covers the whole record, the manifest holds only what defines the store, such as identity, routing, and its capacity ceiling, and not operational settings an operator may change between starts.

### Settlement

The durable record of what a delivery decided about one message on the Local Bus: retry, acknowledge, or discard, with the count of committed retries. It is written before the broker is told, so an interrupted delivery resumes from its last committed decision instead of restarting the retry budget. A Settlement can be lost independently of its message on power loss, in which case the message is handled again.

### Signal Policy

Whether one Service Module emits each telemetry signal (logs, metrics, traces). Each is declared as inherit, enabled, or disabled; a child inherits its parent's effective value, an environment override beats the declaration, and enabling a signal that has no export backing is a startup error. The policy is snapshotted per module generation, so a running attempt never changes what it emits mid-flight.

Disabling traces suppresses span creation only. A trace-disabled module still carries inbound trace context and forwards it on anything it publishes, so modules downstream of it keep end-to-end continuity. Across a durable delivery the downstream span links to the publication rather than descending from it, because the delivery may happen later, more than once, or for many subscribers.

## Network model

### Facet

A bundle of per-layer attributes for one interface — switchport membership, IP enablement, Ethernet link facts — embedded by value in the interface message. A facet's presence is its own discriminator: a routed interface is one whose IP facet is set, with no boolean beside it to disagree.

### Table

Device-scoped state whose rows reference interfaces by name — the FDB, the neighbor cache, the VLAN database. Tables hang off the device, never under an interface, because every consumer queries them device-wide. The facet-versus-table distinction decides where a message embeds.

## Collection

### Declining

A decoder answering "this value is not mine to read" rather than failing. What a decline costs depends on where it happens: on the fused fast path it falls through to the general decoder and costs nothing, inside a generated table walk it ends the walk, and on the change-watch merge path it is dropped silently and leaves the field stale. A decoder that coerces a recoverable value is usually preferable to one that declines.

### Fatal walk

A collection walk whose failure voids the whole answer, as against one that degrades and returns what it gathered. Only the table a set of facts is keyed on is fatal; a walk that merely enriches those facts reports its failure alongside the rows already collected rather than in place of them. A caller therefore cannot read an error as "no data" — it must inspect the result too.
## MIB Parsing

### MIB Module

One named SMIv1 or SMIv2 definition block, and the unit everything else is attributed to. A module is not a file: several can share one file, one module can be shipped under many filenames across vendor trees, and a load follows a module's IMPORTS wherever the search paths find them. A resolved module records which dialect it was read as, because SMIv1 and SMIv2 disagree on access and status values that carry the same spelling.

### Declaration

One macro invocation or value assignment inside a module — an `OBJECT-TYPE`, a textual convention, a plain OID assignment. It is the blast radius of a parse error, which is the point: a source is cut into declarations before any grammar runs, so a vendor MIB that is malformed in one place costs that declaration rather than the file.

### Node

A declaration placed in the OID tree, with the clauses that survived resolution. A node that lost something a renderer needs — a required clause, a parent nothing defines, a contradictory syntax — stays in the tree marked unresolved rather than disappearing, so a reader can see what fell and its subtree stays placed. Unresolved nodes are never rendered as if they were whole.

### Diagnostic

One graded finding about a source, carrying a stable code, a position, and a severity. The parser grades and never decides: it has no abort threshold, so severity is a fact about the MIB rather than a policy about the build. What to do about a finding belongs to the consumer.

### Baseline

The committed record of the diagnostics a configured module is already known to raise. Generation fails on a diagnostic the baseline does not hold and never on one it does, which is what lets a build gate on vendor breakage nobody can fix. An entry is keyed on the code and the declaration it landed in, deliberately not on file or line, so an upstream MIB re-sync does not invalidate it.

### Corpus

The vendored MIB tree under `spec/mib/`, read as a leniency stress bar rather than an adoption target. Only a handful of its modules are configured for generation; the rest is there to exercise deviations no hand-written fixture would think of, and what it produces is committed per vendor so a leniency change surfaces as a reviewable diff.
