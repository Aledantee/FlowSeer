# Goals

What FlowSeer is being built to do, one line per goal, each with the record
that states it. The `next` skill reads this file when no plan is open and
looks for goals with nothing behind them, so a goal belongs here only when a
person has decided it. This file records no status: whether a goal has
landed is read from the tree and from `docs/plans/`, and a status column here
would be wrong within a week.

Add a goal by naming the record that decides it, or by writing the decision
here first and the record after. Remove a goal when it is dropped, with the
reason in the commit message.

## Control plane

- A central device service abstracts devices and their capabilities through
  FlowSeer-owned typed protobuf, not through protocols.
  [`device-service-and-inventory-direction`](docs/architecture/2026-08-20-device-service-and-inventory-direction.md)
- A central inventory holds Device, Integration, Binding, and Placement,
  one lifecycle each. Same record.
- Discovery and asynchronous ingestion (traps, webhooks, pollers) are planes
  of their own that feed the inventory. Same record;
  [`src/services/README.md`](src/services/README.md) names where they land.
- NATS is the integration fabric between edge and central, on the hardened
  profile from the first deployment. Same record.
- A typed change reaches a device through the local-network lane, and an
  observation verifies that it took effect.
  [`verified-device-access-direction`](docs/architecture/2026-09-05-verified-device-access-direction.md)
- The operator and admin API surfaces are authorized through OpenFGA.
  [`src/services/device/README.md`](src/services/device/README.md) names it
  as a follow-up; no record decides its shape yet.
- A mutation is gated before apply by projecting it onto a full-network
  view. [`mutation-shadow-projection-direction`](docs/architecture/2026-09-09-mutation-shadow-projection-direction.md),
  deferred; the virtual-device record leaves open who computes the
  projection.

## Edge

- An edge agent enrolls with central, holds its dispatch stream, and drives
  the local-network access lane. [`src/edge/README.md`](src/edge/README.md)
- `netpen` audits and attacks L2 and L3 on an operator's command. Same file.
- An operator names a packet capture, an edge runs it, and the packets come
  back. [`remote-packet-capture-direction`](docs/architecture/2026-09-09-remote-packet-capture-direction.md)

## Protocol libraries

- Clients for SNMP, NETCONF, RESTCONF, gNMI, SSH, and syslog, with SMI and
  YANG compilers, carry no domain types.
  [`src/protocol/README.md`](src/protocol/README.md)

## Network model and schema

- The schema has two trees, `net/` primitives and `api/` entities, under one
  import rule.
  [`network-model-structure-direction`](docs/architecture/2026-08-20-network-model-structure-direction.md)
- Wireless (`net/wlan`: Radio, Bss, WirelessClient) is a peer of switching.
  Same record; the location is reserved.
- Routing (`net/routing`: RIB, network instance, VRF) lands when a real RIB
  capability is ready. Same record; the location is reserved.
- End hosts get a Host/Client entity family distinct from Device and
  Interface. Same record; reserved, not in v1.
- The hardware component tree maps from ENTITY-MIB and links to the
  interface model. Same record.
- Errors cross the wire as `errs` codes mapped to Connect codes.
  [`error-wire-design-direction`](docs/architecture/2026-09-04-error-wire-design-direction.md)
- Secret material travels in a redacting carrier.
  [`secret-material-carrying-direction`](docs/architecture/2026-09-06-secret-material-carrying-direction.md),
  proposed, not accepted.

## Simulation and analysis

- `src/common/netsim` simulates a network from virtual devices built on a
  port table.
  [`virtual-device-direction`](docs/architecture/2026-09-10-virtual-device-direction.md),
  proposed.
- An analysis states what it can be trusted for: readiness, scoped issues,
  evidence catalogs, and assumptions. Same record.
- A simulated local network includes a stateful firewall with a packet
  filter and tagged sub-interfaces, and an mDNS reflector across VLANs.
  [`local-network-analysis-direction`](docs/architecture/2026-09-16-local-network-analysis-direction.md),
  proposed.

## Web frontend

- An operations console runs on local fixtures with no backend.
  [`frontend/web/README.md`](frontend/web/README.md)

## Engineering

- A goroutine starts through `src/common/spawn`, which recovers, reports,
  and attributes a panic.
  [`supervised-goroutine-spawn-direction`](docs/architecture/2026-09-15-supervised-goroutine-spawn-direction.md)
- Every change passes the diff-aware verifier, and the hooks enforce what
  can be checked mechanically. [`AGENTS.md`](AGENTS.md), Enforced rules.

## Not decided

The repository states no goal for these, so `next` proposes nothing from
them until one is written above:

- Connecting the web console to the control plane, and how its users sign
  in and are authorized.
- What the vendored MIB and YANG corpora and the research under
  `docs/research/` are meant to turn into. Both say they are input to
  decisions, not decisions.
- Packaging, deployment, and a first release.
