---
title: An Untagged Frame's VLAN Is Local to Each End, So Two Ends' VLAN Ids Never Compare
date: 2026-09-16
last_verified: 2026-09-16
category: architecture-patterns
module: src/common/netsim/vswitch
problem_type: bug
component: netsim
severity: medium
symptoms:
  - "A check that compares the VLAN a frame was sent on against the VLAN it was received on fires on a configuration where nothing joined the two VLANs"
  - "A per-port VLAN finding is right on access ports and wrong on a port that is an untagged member of more than one VLAN"
root_cause: "Loop protection recorded an inter-VLAN loop whenever a returning probe's classified VLAN differed from the VLAN named in its payload. An untagged frame carries no VLAN on the wire, so both numbers are local classification choices; on an asymmetric-VLAN port, which untags for several VLANs while classifying ingress by its single PVID, they differ by design."
resolution_type: code_fix
applies_when:
  - "Comparing a VLAN id observed at one point in a topology against one observed at another, including across a simulated cable"
  - "Deriving a finding from ingress classification on a port whose Untagged set may hold more than one VLAN"
  - "Modelling 802.1Q egress membership or PVID behavior, or validating a switchport configuration against what egress actually admits"
related_components: [loopprotect, bridge, stp]
tags: [vlan, 802.1q, wire-semantics, asymmetric-vlan, false-positive, netsim]
---

# An untagged frame's VLAN is local to each end

## The situation

Loop protection detects a forwarding loop by sending a probe and recognizing
its own probe coming back. The probe's payload names the VLAN it was sent on,
and the switch compares that against the VLAN the returning frame classified
into. A difference was recorded as an inter-VLAN loop: evidence that something
in the topology had joined two VLANs.

The comparison is sound on access ports and wrong on a port that is an
untagged member of several VLANs, because an untagged frame carries no VLAN
tag at all. The id on each end is that end's own classification, not a
property of the frame.

## What makes the configuration legal

IEEE 802.1Q's object model is asymmetric, and the MIBs in `spec/mib/ietf/`
show it directly. Untagged egress is defined per VLAN as a set of ports:

> `dot1qVlanStaticUntaggedPorts` — "The set of ports that should transmit
> egress packets for this VLAN as untagged." (`Q-BRIDGE-MIB:1277`)

Ingress classification is defined per port as a scalar:

> `dot1qPvid` — "The PVID, the VLAN-ID assigned to untagged frames or
> Priority-Tagged frames received on this port." (`Q-BRIDGE-MIB:1371`)

So "this port untags for VLANs 10 and 20" is expressible and "this port has
two PVIDs" is not. Vendors ship the gap as a named feature, asymmetric VLAN:
tenant ports stay isolated behind their own PVIDs while one uplink port is an
untagged member of every tenant VLAN, so every tenant reaches one router that
never sees a tag. `bridge.Switchport.Untagged` is a plain `[]vlan.ID` with no
cardinality limit (`src/common/netsim/vswitch/bridge/config.go:100`), so
netsim admits it.

On such a port a probe leaves untagged on VLAN 20 and returns classified into
the port's PVID of 10. Nothing joined the two VLANs.

## What to do instead

Compare VLAN ids across a wire only where the wire carried one, or where you
have established that both ends classify the same way. Where the comparison is
still worth making, name the configuration that explains a difference and
exclude it:

> The VLAN difference is not evidence of an inter-VLAN loop when the port the
> probe was sent from is itself an untagged member of both the sent VLAN and
> the classified VLAN.

Put that judgment in the component that holds the VLAN configuration. The
capability layer that owns the finding owns no switchports — sibling layers do
not import one another — so the switch decides and the layer records what it
is told.

## Why the usual safety nets miss it

- **Every fixture used access ports.** One untagged VLAN per port makes the
  sent and classified ids agree whenever no VLAN was crossed, so the naive
  comparison is correct on every test anyone writes first.
- **The genuine positive and the false one are the same shape.** A probe sent
  on 10 returning classified into 20 is a real inter-VLAN loop on access ports
  and an artifact on an asymmetric-VLAN port. Nothing in the two ids
  distinguishes them; only the sending port's membership does.
- **Validation admitted what egress refused.** A related defect in the same
  area had configuration validation and egress applying different rules for
  "does this port carry this VLAN", so probes vanished silently. The fix was
  one exported predicate, `bridge.Switchport.CarriesVID`, used by both. A
  VLAN question asked in two places is worth one implementation.

## Evidence

- `dot1qVlanStaticUntaggedPorts` (`spec/mib/ietf/Q-BRIDGE-MIB:1277`) against
  `dot1qPvid` (`:1371`), read on 2026-09-16.
- Vendors documenting the feature and its shared-uplink use case:
  [TP-Link](https://community.tp-link.com/en/business/forum/topic/198458),
  [D-Link DGS-3100](https://www.manualslib.com/manual/419247/D-Link-Dgs-3100-48.html?page=88),
  [Zyxel](https://community.zyxel.com/en/discussion/28147/gs1200-8-802-1q-vlan-configuration-multiple-untagged-vlans-on-port).
  Their common warning is that MAC learning suffers: the shared router's
  address is learned in every tenant VLAN at once.

## What this does not cover

It says nothing about whether the finding is worth making at all on a tagged
wire, where the comparison is sound. It also leaves asymmetric VLAN's other
consequence unmodelled: a forwarding database that learns one address in
several VLANs, which is what the vendors' own guidance warns about.
