# common

Cross-cutting foundations: packages every layer may depend on and that depend on
nothing of FlowSeer's own.

| Package                | What it does                                                        |
| ---------------------- | ------------------------------------------------------------------- |
| `errs`                 | error types, codes, and boundary filtering                           |
| `pump`                 | shared work-pump concurrency primitive                              |
| `service`              | process-local module runtime, supervision, delivery, and telemetry  |
| `snmpmap`              | temporary SNMP-to-domain mapping exception described below          |
| `internal/netpenguard` | build guard limiting heavy dependencies to `src/edge/netpen`        |

## What belongs here

Two tests, both required. A package qualifies when it has no FlowSeer domain
knowledge — nothing from `generated/go/proto` or `generated/go/mib` — and when
at least two unrelated trees would import it. One consumer is not cross-cutting;
it is that consumer's `internal/`.

This directory used to hold the protocol libraries too, which is how it grew to
450 files under a name that told a reader nothing. Those moved to
`src/protocol/`. Resist re-growing it: a package with a real subject gets a
directory named after that subject.

`snmpmap` is the known violation. It maps walked SNMP tables onto FlowSeer
protobuf messages, so it fails the domain-knowledge test outright. It stays here
only until `src/modules/` has an admission contract to move it under.
