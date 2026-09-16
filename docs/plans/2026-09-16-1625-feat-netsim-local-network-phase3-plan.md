---
title: Local Network Analysis Phase 3 - The mDNS Reflector Node - Plan
type: feat
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-16-1625-feat-netsim-local-network-plan.md
---

# Local Network Analysis Phase 3 - The mDNS Reflector Node - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A fabric can hold an mDNS reflector, and a journey shows an mDNS query
crossing from one VLAN to the others through it. The means: a
`fabric.Reflector` node kind with attachments over one or more cabled
ports, an arrival path that reflects, journeys linked by `Parent`, and loop
detection over the parent chain. The plan is wrong if the reflector's
copies cannot be transmitted through `enqueueEgress` from a non-switch
endpoint, which would mean the egress accounting keyed by `Endpoint`
(`src/common/netsim/fabric/run.go:608-625`) has a switch assumption the
research did not see.

## Decisions

The parent's decisions on the reflector, on `Parent`, and on loop detection
apply. In addition:

- Shape: `Reflector{Address netaddr.MAC, Ports map[string]phy.Ethernet,
  Interfaces map[string]ReflectorInterface{Port string, VLAN *vlan.ID,
  Addresses []netip.Prefix}}`. Every port is cabled exactly once; every
  interface names a port; (port, VLAN) pairs are unique; each interface
  needs at least one address. Why: Avahi binds one socket per interface and
  sends from that interface's address; an attachment without an address of
  the datagram's family gets no copy, and the journey records that as a
  drop with reason `reflector-no-address`.
- Acceptance: a frame is reflected when its tag form matches an attachment
  on the arrival port, its destination MAC is `01:00:5e:00:00:fb` or
  `33:33:00:00:00:fb`, the IP destination is 224.0.0.251 or `ff02::fb`,
  the protocol is UDP, and the UDP destination port is 5353. Anything else
  is a rejection on `ReflectorLayer` with a reason naming the failed
  clause, in the shape of `Host.macRule` and `Host.ipRule`
  (`src/common/netsim/fabric/accept.go:178-235`). Why: the reflector has no
  other role; a reply addressed to it by unicast is out of scope.
- The copy: same payload, IP source the attachment's address of the same
  family, hop limit 255, UDP source port 5353, UDP checksum recomputed
  with `udp.Encode`, Ethernet source the reflector's MAC, tag form the
  attachment's. Why: RFC 6762 section 11 requires hop limit 255 and
  section 5.2 requires source port 5353 for a compliant querier; Avahi
  sends the reflected packet from its own socket.
- The reflector never reflects a frame whose Ethernet source is its own
  MAC. Why: a copy the reflector sent on VLAN 20 floods back to it on
  VLAN 20; that is its own transmission, not a query to reflect, and
  suppressing it is what Avahi does with its own packets. Two different
  reflectors still loop, which parent R7 requires to be visible.
- Loop detection keys on the root frame: a reflected journey carries
  `Root FrameID` (the injected frame) and `f.entered` is keyed by root,
  so the second arrival of any copy of one query at one endpoint records
  `EntryLoop`. Mirror copies keep their own `FrameID` key. Why: the
  direction record says a loop is a queue that does not drain and the run
  marks the re-entry; a mirror copy re-entering a switch is legitimate.
- The corpus gains `troubleshooting/mdns-reflected-across-vlans` (a fabric
  case: host on VLAN 10, snooping with unregistered flooding off,
  reflector on a trunk with attachments 10 and 20, host on VLAN 20 with
  the group MAC in `HostAccept.Multicast`; expected two journeys, the
  copy's `Parent` set, the VLAN 20 host's delivery) and
  `troubleshooting/mdns-two-reflectors-loop`. Why: parent R6 and R7 are the
  behaviors the phase adds.

## Requirements

Parent R6 and R7, plus:

1. `Config.Validate` rejects a reflector with an uncabled port, an interface
   naming an unknown port, duplicate (port, VLAN), or an interface without
   an address; `Diff` reports reflector changes per interface field;
   `Fabric.Spec` and `ConstructionSpec` carry reflectors.
2. A reflected copy's journey `Metadata` folds in the reflector's own
   dependencies (its port's link state and the cable) as a host delivery
   does (`src/common/netsim/fabric/journey.go:166`).
3. A frame the reflector rejects is a terminal `EntryRejection` on the
   parent journey with the clause's reason, and produces no copy.

## Out of scope

- IPv4 to IPv6 reflection, service-name filters, DNS parsing.
- The reflector joining groups by emitting IGMP or MLD reports (parent's
  first open question).
- A reflector that is also a host with addresses reachable by unicast.

## Open questions

- Whether `EntryLoop` on the root journey is enough, or the copy's journey
  should also record it. The plan prefers both, with the root as the
  authority a `Compare` reads.
- Whether the copy's `FrameID` order needs a tie-break beyond `Seq` when
  several attachments emit at once. The existing `compareArrival`
  (`src/common/netsim/fabric/queue.go:35-50`) orders by `Seq` then device
  and port, which should suffice.
