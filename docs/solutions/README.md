# Captured solutions

Solutions preserve verified lessons that are easy to miss by reading the happy
path. Use each document's `applies_when` frontmatter to decide whether to read
it. Category and severity help with search and review priority; they do not make
a solution override an accepted architecture record or binding convention.

| Solution | Read when |
| --- | --- |
| [SNMP Collection Library: Architecture and Fast-Path Conventions](architecture-patterns/snmp-collection-library-architecture-and-fast-path-conventions.md) | Changing `src/protocol/snmp`, generated MIB bindings, the SNMP hot path, or its conformance and performance gates. |
| [A Decoder's Decline Costs the Whole Table, Not the Field](architecture-patterns/decode-failure-blast-radius-in-generated-walks.md) | Tightening a generated-column decoder, choosing error versus coercion for malformed agent data, or investigating a table that unexpectedly returns no rows. |
| [Key Resolution Degrades and Reports, It Never Fails a Row or a Run](architecture-patterns/key-resolution-degrades-and-reports-never-fails.md) | Changing INDEX/AUGMENTS resolution, mibgen key-type emission, `DecodeIndex`/`DecodeIndexInto`, or a mapper's use of `KeyValid`. |
| [The errs Package: FlowSeer's Owned Error Type and Its Conventions](architecture-patterns/errs-package-architecture-and-error-conventions.md) | Creating or exposing Go errors, adding an `errs.Code`, or deciding whether diagnostic data belongs in an error, attribute, or log. |
| [Declaration-Level SMI Recovery Preserves Declared Semantics](architecture-patterns/gosmi-drops-bits-member-numbers.md) | Changing SMI parser recovery, BITS member handling, MIB generation, or use of gosmi as a reference. |
| [Local Bus Durability Is a Runtime Setting, Not Storage Identity](architecture-patterns/local-bus-durability-is-a-runtime-setting-not-storage-identity.md) | Changing `BusConfig` or the embedded JetStream server options, adding a field to the runtime manifest, choosing a durability policy for a service, a service that refuses to start with `service/bus-config` because no fsync policy is declared, or investigating a handler that ran twice after a power cut. |
| [Trace Context Relays Through Trace-Disabled Modules](architecture-patterns/trace-context-relays-through-trace-disabled-modules.md) | Adding a publish or delivery path or a telemetry helper in `src/common/service`, choosing parent versus link at a durable boundary, or debugging a trace that breaks where one module has traces disabled. |
| [Documenting Intentional Protobuf Validation Deviations](conventions/document-intentional-schema-deviations-with-comment-and-test.md) | Deliberately departing from a package-wide validation pattern or preserving forward compatibility for enum and repeated-field validation. |

Add a solution only after the behavior and lesson have been verified. Keep its
frontmatter specific enough that an agent can reject unrelated documents
without reading their full bodies.
