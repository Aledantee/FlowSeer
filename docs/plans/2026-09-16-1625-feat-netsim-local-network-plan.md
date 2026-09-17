---
title: Local Network Analysis - Plan
type: feat
date: 2026-09-16
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Local Network Analysis - Plan

## Goal

`src/common/netsim` answers the questions a local network with one firewall
between its VLANs and one mDNS reflector raises: whether a client on one VLAN
may reach a service on another through the firewall's rules, whether a reply
comes back through a stateful rule, whether an mDNS query reaches the
reflector under multicast snooping, and which VLANs the reflected copy lands
on. The means: transport codecs under `src/common/net`, a routed
sub-interface on a trunk port, a `filter` capability on the virtual switch, a
reflector node in the fabric, and conformance cases that pin the multicast
flooding rules the reflector depends on. The plan is wrong if any of these
answers turns out to need a connection table, a routing protocol, or a DNS
parser; those are separate decisions.

The previous assessment (session of 2026-09-16, Batfish comparison) said
netsim lacks a control plane. For this network it lacks none: the firewall
routes between connected VLANs, which `routing` models with connected routes.
What it lacks is filtering, the reflector, and the trunk attachment a
firewall uses, and those are what this plan adds.

## Decisions

- Filtering is a capability package `src/common/netsim/vswitch/filter`.
  Why: the rules belong to one device, are keyed by its routed interfaces,
  and take part in its trace and diff like every other layer
  (`docs/architecture/2026-09-10-virtual-device-direction.md`, "one package
  per capability over the port table"). The direction record proposed with
  this plan, `docs/architecture/2026-09-16-local-network-analysis-direction.md`,
  holds the record-level form of the decisions below.
- Rules attach to a routed interface with a direction, first match wins, and
  a rule set carries an explicit default action. Why: this is the RFC 8519
  ACL shape ("Actions on the first matching ACE are applied with no
  processing of subsequent ACEs", section 3), it maps an interface-bound
  firewall (OPNsense, pfSense) directly and a zone-based one as one rule set
  per interface pair, and the user chose it over a product-specific shape.
- A rule set is stateful by flag, and statefulness is a reverse-match rule
  computed from the configuration: a packet a stateful set does not accept on
  its own rules is accepted when the reversed 5-tuple would be accepted by
  the stateful set bound in the same direction on the interface the packet
  crosses at the other side (its egress interface for an ingress binding,
  its ingress interface for an egress binding). Why: netsim traces one frame
  as a function of configuration and frame, with no state between frames;
  the reverse match is exactly the state a stateful firewall would hold for
  the forward packet, and the trace can name the forward rule it relied on.
  A connection table would make the answer depend on frames the caller
  never injected.
- `reject` is a drop whose reason says so; no ICMP error is generated. Why:
  emitting ICMP needs the switch to originate a packet to a possibly
  unresolved neighbor, and no golden workflow asks what the ICMP error does.
- The mDNS reflector is a fabric node kind, `fabric.Reflector`, with several
  attachments over one or more cabled ports. Why: a reflector is an
  application on an endpoint, and the fabric keeps endpoints out of the
  switch ladder for the reason `src/common/netsim/fabric/config.go:171`
  gives: a one-port switch would carry a forwarding database the endpoint
  must never use. A reflector on the firewall itself is modeled by cabling the
  reflector node to a spare port of the firewall switch; the device is
  sized by its caller.
- A reflected copy is a new journey whose `Parent` is the received frame,
  and loop detection keys on the root of the parent chain. Why: mirror
  copies already use `Parent` (`src/common/netsim/fabric/run.go:521-526`),
  and two reflectors on the same VLANs loop in reality (avahi-daemon.conf(5)
  warns against it); a run must report that as a loop rather than exhaust
  its budget silently.
- A routed sub-interface is a `routing.Interface` with both `Port` and
  `VLAN` set, classified on the routed-port path by the outer C-TAG, and its
  parent port stays outside the bridge. Why: this is how a firewall attaches
  to a trunk (`eth0.10`), the schema already expresses it
  (`spec/proto/flowseer/net/interface/v1/subinterface.proto`), and keeping
  the parent out of the bridge and spanning tree leaves the existing bans at
  `src/common/netsim/vswitch/config.go:316-335` in force.
- Transport codecs live in `src/common/net/udp`, `src/common/net/tcp`, and
  `src/common/net/icmp`, one package per protocol like `igmp` and `mld`.
  Why: the filter matches ports, flags, and ICMP types, and the reflector
  rewrites a UDP datagram's checksum; both trees may import
  `src/common/net`, and the codec lesson in
  `docs/solutions/conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md`
  applies to each.
- The multicast flooding rules change nothing and gain conformance cases.
  Why: `Switch.Resolve` already floods 224.0.0.0/24 as RFC 4541 section
  2.1.2 requires (`src/common/netsim/vswitch/switch.go:1110-1114`), and
  snoops IPv6 link-scope groups other than `ff02::1` as section 3 allows.
  The corpus has no case for either, and the reflector's answer depends on
  both.
- The filter rules get a schema package `flowseer.net.filter.v1` and a
  `netmodel` translation in the last phase; the collector that fills it is
  out of scope. Why: the user chose this, and the direction record calls a
  simulator nobody can load from the network model a test fixture.
- The work is four phases in dependency order. Why: the units cluster by
  package: codecs and corpus, the routing sub-interface, the fabric
  reflector, and the filter with its schema; `.claude/skills/plan/references/phases.md`.

## Requirements

1. A UDP datagram decodes to its ports, length, and checksum, and encodes
   with a checksum over the IPv4 or IPv6 pseudo-header. Example: phase 1's
   hand-computed IPv4 fixture decodes to source and destination port 5353,
   length 54, and checksum `0xaa94`, and re-encodes to the same bytes.
2. A TCP segment decodes to its ports, data offset, and flags; an ICMP
   message decodes to its type and code. Example: bytes `00 50 1f 90 ...`
   with offset 5 and flags `0x12` decode to source 80, destination 8080,
   SYN and ACK set.
3. Under snooping with unregistered flooding off, an IPv4 frame to
   224.0.0.251 floods the VLAN and an IPv6 frame to `ff02::fb` with no
   member reaches only router ports. Example: switch `sw1`, VLAN 10 with
   `FloodUnregistered=false`, router port `p9`: the IPv4 frame from `p1`
   floods to `p2..p9`; the IPv6 frame goes to `p9` alone.
4. A routed interface with `Port` and `VLAN` both set receives frames that
   arrive on that port with that outer C-TAG and transmits with that tag
   pushed. Example: firewall `fw` with `eth1.10` and `eth1.20` on trunk
   `eth1`; a frame tagged 10 to the router MAC routes and leaves `eth1`
   tagged 20.
5. `netmodel` loads a `Subinterface` whose parent is a physical or LAG
   interface and whose encapsulation is one C-TAG as such a routed
   interface, and reports any other encapsulation as unsupported. Example:
   `eth1.10` with `encapsulation.tags=[{vid:10}]` loads; a two-tag stack
   raises `netmodel.routing.unsupported_encapsulation`.
6. A reflector node that receives an mDNS datagram on one attachment
   originates one copy per other attachment of the same address family,
   with its own source MAC and address, hop limit 255, and source port
   5353, and each copy is a journey with `Parent` set. Example: query from
   host `h10` on VLAN 10 reaches reflector `r1` (attachments VLAN 10 and 20
   on trunk `p8`); one copy leaves `p8` tagged 20 and host `h20` accepts it.
7. Two reflectors sharing two VLANs produce a journey that records a loop
   and the run halts on its budget. Example: `r1` and `r2` both attached to
   VLANs 10 and 20; a reflected copy's journey gains an `EntryLoop` entry.
   The copy's, not the query's: re-entry is detected per frame with a copy
   seeded from its parent, so the injected query enters each reflector once
   and never re-enters. Phase 3 records why a root key does not work.
8. A filter set bound to an interface decides accept, drop, or reject on the
   first matching rule, else on the default action, and a drop is a Complete
   domain outcome. Example: set `lan-in` `[deny udp any->10.0.20.0/24:5353,
   accept any]` bound `vlan10 in`: a UDP packet to 10.0.20.5:5353 drops with
   reason `filter-drop`; TCP to the same host forwards.
9. A stateful set accepts a packet whose reversed 5-tuple its counterpart
   accepts. Example: `lan-in` stateful accepts `10.0.10.7:40000 ->
   10.0.20.5:443/tcp`; `srv-in` bound `vlan20 in` is `[deny any]`; the
   reply `10.0.20.5:443 -> 10.0.10.7:40000/tcp` arriving on `vlan20`
   forwards, and the trace names `lan-in`'s rule.
10. A filter change flips a planning comparison. Example: `Compare` of a
    switch with and without the deny rule in R8 reports the packet as a
    proven mismatch with the rule as the first divergence.
11. Filter rule sets and bindings load from `flowseer.net.filter.v1`
    messages through `netmodel`, with a binding to an interface that has no
    IP facet reported as an issue. Example: the R8 set as protobuf loads to
    the same `filter.Config`.

## Out of scope

- Dynamic routing (OSPF, BGP), a WAN interface, NAT, and any default route
  beyond what `routing.Route` already takes.
- A connection table, TCP state, or fragment reassembly.
- ICMP error generation for `reject`.
- DNS parsing: the reflector copies the datagram byte for byte; Avahi's
  `reflect-filters` and `reflect-ipv` have no counterpart.
- Unicast mDNS responses to the reflector, and the reflector as a host with
  its own services.
- A snooping schema in the network model; `netmodel` still rejects
  `LayerMcast` (`src/common/netsim/vswitch/netmodel/netmodel.go:352-364`).
  The trigger is a collector that reports snooping state.
- The collector that reads firewall rules into the schema.

## Units

### U1. Phase 1 - transport codecs and multicast conformance
Files: `docs/plans/2026-09-16-1625-feat-netsim-local-network-phase1-plan.md`
After: none
Change: `src/common/net/udp`, `src/common/net/tcp`, and `src/common/net/icmp`
decode and, for UDP, encode; the corpus pins R3.
Landed: `f638a3bd..7698b1a6`

### U2. Phase 2 - routed sub-interfaces
Files: `docs/plans/2026-09-16-1625-feat-netsim-local-network-phase2-plan.md`
After: none
Change: `routing.Interface` accepts `Port` with `VLAN`; the switch classifies
and tags on the routed-port path; `netmodel` loads `Subinterface` (R4, R5).
It also pins the two `udp.Verify` refusal guards phase 1 left unpinned, which
its own plan records as a decision.
Landed: `91af8469..edd97001`

### U3. Phase 3 - the mDNS reflector node
Files: `docs/plans/2026-09-16-1625-feat-netsim-local-network-phase3-plan.md`
After: U1
Change: `fabric.Reflector` exists, reflects, and loops detectably (R6, R7).
Landed:

### U4. Phase 4 - the filter capability and its schema
Files: `docs/plans/2026-09-16-1625-feat-netsim-local-network-phase4-plan.md`
After: U1, U2, U3
Change: `vswitch/filter`, its wiring, corpus cases, `flowseer.net.filter.v1`,
and the `netmodel` translation (R8 to R11). Its code depends on U1 and U2
only; it runs after U3 because both register cases in
`src/common/netsim/internal/netsimtest`, and two units that edit the same
files are never independent.
Landed:

Waves: U1 U2 | U3 | U4 (U3 and U4 are ordered by their shared corpus files)

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
buf lint && buf generate
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

Each phase plan names its focused checks. No lab device takes part, and no
manual step remains: the capture phase 1 asked for was not taken, and
`TestCapture` in `src/common/net/udp/udp_test.go` records its absence as a
skip.

## Definition of done

- [ ] Every phase's `Landed:` line carries a commit range and its plan
      reads `implemented`.
- [ ] R1 to R11 each hold on the landed tree, with the corpus cases the
      phases name admitted and listed in the corpus README.
- [ ] Verifier green for every changed path in every phase.
- [ ] `src/common/netsim/README.md`, the package READMEs the phases touch,
      and the "Remaining capability gaps" of the virtual-device direction
      record are updated in the change that invalidates them.
- [ ] The proposed direction record is accepted or revised by a person.
- [ ] This plan's `status` is set with an outcome note under its title; no
      plan labels appear in code.

## Open questions

- Whether the reflector should also join the mDNS groups on the switch by
  emitting IGMP or MLD reports itself. The plan says no (unconfirmed): the
  IPv4 group floods regardless, and an IPv6 reflector in a snooped VLAN is
  modeled by injecting the report as the tests do today. Phase 3 revisits
  this when it re-plans.
- Where the schema hangs the filter bindings: on the interface as a facet
  beside the IP facet, or as device-level rows like `InterfaceAddress`.
  Phase 4 decides; the parent only fixes the package name.
