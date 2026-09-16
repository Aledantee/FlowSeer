---
title: Local Network Analysis - Direction
type: direction
date: 2026-09-16
topic: local-network-analysis
status: proposed-direction
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Local Network Analysis - Direction

## Context

The networks FlowSeer models first are local: access switches, one firewall
that routes between VLANs, and one mDNS reflector so that printers and
media devices on one VLAN are found from another. The virtual device
direction record models the switches well and lists "dynamic IP routing
(BGP and OSPF)" under its remaining gaps, but a firewall between connected
VLANs needs no routing protocol. What it needs is missing: a packet filter
and a trunk attachment with tagged sub-interfaces on the firewall, and an
endpoint that re-originates mDNS across VLANs. This record decides how
those enter `src/common/netsim` without breaking the rules the virtual
device record set: one package per capability, hosts without behavior, a
trace that is a value, and no state between frames.

## Decision

- **Filtering is a capability of the virtual switch.** Package
  `src/common/netsim/vswitch/filter` holds rule sets and bindings; a
  binding attaches a set to a routed interface in one direction. First
  match wins and a set carries an explicit default action, the RFC 8519
  shape. A drop by a filter is a Complete domain outcome, as a port-down
  drop is.
- **Statefulness is a reverse match over the configuration.** A stateful
  set that does not accept a packet on its own rules accepts it when the reversed
  5-tuple would be accepted by the stateful set bound in the same direction
  on the interface at the packet's other side: its egress interface for an
  ingress binding, its ingress interface for an egress binding. The trace
  names the forward rule. This is the state a stateful firewall would hold
  for the forward packet, computed from the configuration instead of
  remembered, so the answer stays a function of configuration and frame.
- **`reject` drops and generates nothing.** The reason distinguishes it
  from `drop`; no ICMP error leaves the switch.
- **A routed sub-interface is a routing interface with a port and a VLAN.**
  It is classified on the routed-port path by the outer C-TAG and tagged on
  egress; its parent port stays outside the bridge and the spanning tree,
  as a routed port does today. The schema's `Subinterface` kind with one
  C-TAG loads as one.
- **The mDNS reflector is a fabric node kind.** `fabric.Reflector` has
  attachments over cabled ports; on an mDNS datagram it originates one copy
  per other attachment with its own source, hop limit 255, and source port
  5353. Hosts stay passive; the reflector is the one endpoint kind that
  reacts, and it reacts only to mDNS.
- **A reflected copy is a journey with a parent, and loops are detected on
  the root.** The copy carries `Parent`; re-entry detection keys on the
  injected root frame, so two reflectors sharing two VLANs produce a
  recorded loop and a budget halt rather than a silent exhaustion.
- **Transport codecs are common packages.** `src/common/net/udp`, `tcp`,
  and `icmp` sit beside `igmp` and `mld`; the filter matches on them and
  the reflector recomputes the UDP checksum with them.
- **The multicast flooding rules stand.** The resolver floods 224.0.0.0/24
  regardless of membership (RFC 4541 section 2.1.2) and snoops IPv6
  link-scope groups other than `ff02::1` (section 3). The corpus pins both
  for the mDNS groups, because the reflector's answer depends on them.

## Alternatives

- **Filter as a fabric-level policy between nodes.** Rejected: the rules
  belong to one device and take part in its trace, diff, and comparison;
  a fabric policy would have no place in a single-switch planning question.
- **A connection table for stateful sets.** Rejected: it would make the
  answer depend on frames the caller never injected and break the rule that
  a run is a function of configuration, frame, and time. The reverse match
  gives the same answer for every flow whose forward packet the rules
  accept, and the trace shows why.
- **The reflector as a switch capability.** Rejected: it would sit on the
  firewall's virtual switch only, and a dedicated reflector host could not
  be modeled; a node kind covers both, with the firewall-hosted case cabled
  to a spare port.
- **The reflector as a host with a behavior hook.** Rejected: the virtual
  device record keeps hosts as endpoints that accept or reject; a general
  behavior hook would invite protocol engines into hosts one at a time.
  A named node kind with one reaction is the smaller commitment.
- **Sub-interfaces classified through the bridge.** Rejected: a firewall's
  trunk is not a bridge member; the VLAN interface already covers a router
  on a stick over the bridge, and the routed-port path keeps the parent
  port's existing bans in force.
- **Emitting ICMP on `reject`.** Deferred: it needs the switch to
  originate to a neighbor that may be unresolved and no workflow asks for
  it.

## Consequences

- The virtual device record's "Remaining capability gaps" keeps dynamic IP
  routing, which this record does not touch, and gains the items this
  record leaves out: NAT, connection tracking, ICMP error generation,
  DNS-aware reflection, and a snooping schema in the network model.
- `netmodel` gains a translation for `flowseer.net.filter.v1` and for the
  `Subinterface` kind; it still rejects the multicast capability until a
  collector reports snooping state.
- The fabric has two endpoint kinds. Adding a third that reacts is a new
  decision against this record, not an extension of the reflector.
- The plan that implements this record is
  `docs/plans/2026-09-16-1625-feat-netsim-local-network-plan.md`.
