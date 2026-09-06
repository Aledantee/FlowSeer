# modules

Reusable modules: units of behavior a host assembles rather than writes. A
device poller, an inventory reconciler, a syslog ingest pipeline, an auth
boundary, each configured, started, stopped, and observed the same way, so
building a host becomes a matter of choosing modules and wiring their
configuration.

A host is either a control-plane service (`src/services/`) or an edge
application (`src/edge/`), and a module may be assembled into both. That is the
point of the tier: the same syslog pipeline can run centrally in a service and
inside an agent at the edge, written once here.

| Module     | What it is                                                                 |
| ---------- | -------------------------------------------------------------------------- |
| `localnet` | SNMP side of the local-network integration kind: collector and mappers     |
| `edgebus`  | NATS carrier between edge and central: hub, leaf node, OTLP receiver and forwarder |

## Admission

The contract a module is held to is the service runtime in
[`src/common/service`](../common/service/README.md): a `service.Module` is
constructed from a typed config value with no ambient globals, declares what it
needs from its host through its leaf's subscriptions, starts and stops under a
context with defined behavior on a failed start, and surfaces health and
metrics through the host rather than registering them globally.

A package is admitted here when it is the thing a host would wire as a module
under that contract and at least two hosts will assemble it. A package with one
host stays in that host's `internal/`, where it costs nothing to move later.

The wiring itself (the `Leaf` or `Branch` declaration, its config message, its
gates) is written together with the first host, because its shape follows the
host's supervision tree. A module directory may therefore hold the library a
future leaf wraps before the leaf exists; what it may not hold is a package
that no host will ever wire.

## Why `localnet` is first

The SNMP mappers already sat at the seam between the SNMP library and the
protobuf domain model, which made them the one package in the repository that
was reusable behavior rather than a protocol library or a foundation. Both
hosts the direction record names will assemble them: the edge agent that polls
devices inside a site's network, and the central process that reaches devices
directly. Two hosts and a seam is the admission rule met, so the mappers moved
here from `src/common/`, where they had been the documented exception to that
directory's no-domain-knowledge rule.

## Keep it from becoming the new `src/common/`

`src/common/` had no admission rule, so everything shared drifted into it until
it was mostly protocol libraries under a name that said nothing. A directory
named "modules" attracts the same drift harder, because almost any package can
be described as a module. The rule above is the guard: a package that is not
something a host wires under the service runtime does not belong here, no
matter how many packages import it. Cross-cutting foundations go to
`src/common/`, protocol libraries to `src/protocol/`.
