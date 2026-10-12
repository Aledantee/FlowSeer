---
title: netpen Check Model Phase 2, Observables - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: code
---

# netpen Check Model Phase 2, Observables - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Every behavior under `src/edge/netpen/attacks/` reads the wire for the
effect its probe should have and returns `confirmed` or `resisted` from
what it saw, or `pending` with the exact reason the leg cannot carry the
observable. The teardown steps do what their names say. The means: one unit
per attack family, each adding the observe step, its predicate, and a
fixture of the response frames from a second source.

**Stop condition:** a family's observable needs the host's own IP stack
(a TCP session, a DHCP client) rather than raw frames. The parent record's
"every behavior is a check" then needs an exception for that family.

## Decisions

The parent plan's Decisions apply. Local to this phase:

- Each observable is one predicate over frames received within
  `Deps.Observe`, stated in the behavior's doc comment and pinned by a
  response fixture. Why: a predicate is what the test can mutate and what
  the matrix's `Observable` column can quote.
- Response fixtures come from a second source: a device capture under
  `docs/research/`, a Wireshark dissector sample, or the RFC's own example
  bytes. Why: `docs/solutions/conventions/a-permissive-synthetic-tier-passes-attacks-a-real-vendor-rejects.md`
  and `AGENTS.md`, Investigation discipline. A fixture authored from the
  same generator as the probe proves nothing about the response.
- Teardown steps that touch host state do so through a seam
  (`Deps.Sysctl` for `net.ipv4.ip_forward`, `Deps.Netlink` for the 802.1Q
  interface) that the test replaces. Why: `attacks/l2/portsteal.go:43` and
  `attacks/l2/vlanhop.go:48` return nil today and the catalog claims a
  restore.
- The VRRP advertisement carries a computed checksum. Why: RFC 3768
  section 7.1 requires receivers to verify it (unverified until the RFC is
  fetched and cited in the unit).
- Behaviors that need sustained traffic to hold their effect (HSRP, GLBP,
  VRRP hellos, OSPF hellos) send it for the observation window and stop.
  Why: a single coup followed by silence is restored by the real router's
  hold timer, so the restore the catalog claims is a side effect of the
  probe being too short.

## Requirements

Parent requirement 16 is claimed here. The re-plan writes one requirement
per family with the predicate and the fixture named, in this shape:

1. First hop (`attacks/fh`): `arpspoof` observes the victim's and gateway's
   next ARP requests or replies and returns `confirmed` when either carries
   the attacker's MAC for the other's IP. `hsrp` and `glbp` observe group
   hellos. `vrrp` observes advertisements. `icmpredirect` is `pending`
   unless the redirected destination's traffic reaches the attack leg.
   `llmnr` observes queries and answers only those. `lldpspoof` and
   `gratarp` observe nothing and return `pending` with the reason.
2. Layer 2 (`attacks/l2`): `stproot` observes BPDUs for a root change.
   `dtp` observes the DTP reply's trunk status. `vtp` observes the next
   summary's revision. `camflood` observes a unicast frame for a MAC the
   leg does not own on the watch leg. `vlanhop` and `doubletag` observe the
   watch leg for the inner-tagged frame. `etherchannel` observes the LACP
   partner state. `mvrp` observes the registrar's response. `portsteal`
   observes traffic for the stolen address arriving at the attack leg.
   `voicevlan` observes CDP or LLDP-MED for the voice VLAN and traffic on
   it.
3. IPv6 (`attacks/ip6`): `roguera` and `raflood` observe neighbor
   solicitations for the advertised prefix. `ndpspoof` observes the
   victim's neighbor advertisements. `daddos` observes whether the target
   abandons the address. `dhcpstarve` and `roguedhcp` observe offers and
   requests. `roguedhcp6` observes solicits and requests. `mld` observes
   the querier's response. `raguard` keeps its watch-leg predicate.
4. Routing (`attacks/routing`): `ospf` reads the target's hello, lists it as
   a neighbor, observes the adjacency state through the target's hello
   neighbor list and returns `confirmed` when the attacker's router ID
   appears. `eigrp` observes the hello and update exchange. `wpad` observes
   a PAC fetch on its listener.

## Out of scope

- Any new behavior. Phase 4 adds checks.
- Recon filling the target. Phase 3.

## Units

Written at re-plan time, one per family, with `Files:` naming the package
and its fixtures, and `Tests:` naming the predicate mutation per behavior.

## Verification

```bash
cd src/edge/netpen && go vet ./... && CGO_ENABLED=0 go test -race -count=1 ./attacks/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] The matrix's `Observable` column quotes each predicate.
- [ ] `uv run tools/scripts/run.py plan record implemented <phase> --units <n> --from <t> --to <t>`.

## Open questions

- Which response fixtures can be captured from the T1 FRR segment and
  which need a vendor capture. The re-plan lists them per behavior.
- Whether the sustained-hello behaviors need a per-check observation
  default longer than the run option.
