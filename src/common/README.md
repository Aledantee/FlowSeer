# common

Cross-cutting foundations: packages every layer may depend on and that depend on
nothing of FlowSeer's own.

| Package                | What it does                                                        |
| ---------------------- | ------------------------------------------------------------------- |
| `errs`                 | error types, codes, and boundary filtering                           |
| `net/ethernet`         | Ethernet II frame codec, tag stack, and EtherType constants          |
| `net/ip`               | IPv4 and IPv6 header codec                                           |
| `net/netaddr`          | MAC and EUI-64 hardware address types and parsing                    |
| `net/vlan`             | 802.1Q tag, VLAN identifier, and priority code point value types     |
| `netsim`               | network simulation: traces, capability-built virtual switch          |
| `pump`                 | shared work-pump concurrency primitive                               |
| `secret`               | redacting carrier for credential material                            |
| `service`              | process-local module runtime, supervision, delivery, and telemetry   |
| `internal/netpenguard` | build guard limiting heavy dependencies to `src/edge/netpen`         |
| `internal/secretguard` | build guard keeping credential material out of raw string fields     |

## What belongs here

Two tests, both required. A package qualifies when it has no domain types
and when at least two unrelated trees import it. One consumer is not
cross-cutting; it belongs in that consumer's `internal/`.

The rule against domain types keeps common packages independent of generated
schemas. Three packages carry narrow exceptions that import from
`generated/go/proto`: `errs` for status envelopes, `service` for process
runtime contracts, and `netsim/vswitch/netmodel` to translate model records
into simulator configurations. Translating at this boundary lets both the
control plane and edge tools run the simulator without depending on a service
module.

This directory used to hold the protocol libraries too, which is how it grew to
450 files under a name that told a reader nothing. Those moved to
`src/protocol/`. Resist re-growing it: a package with a real subject gets a
directory named after that subject, and a package that maps onto the domain
model goes under `src/modules/` (the SNMP mappers did).
