---
title: netpen Check Model Phase 2, Observables - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: code
amends: docs/plans/2026-10-12-1259-feat-netpen-check-model-plan.md
---

# netpen Check Model Phase 2, Observables - Plan

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

**Re-plan of 2026-10-15.** Phase 1 landed the contract, the target form
rules, and the two observation helpers (`src/edge/netpen/check/observe.go`),
with observers in `ghost`, `raguard`, `vlanenum`, `daddos`, `roguedhcp6`,
`ospf`, and `eigrp`. The stop condition was met by one family member:
the baseline's `vlanhop` and `voicevlan` prove their hop with a DHCP lease
on a host subinterface (`src/edge/netpen/tools/l2l3-audit/l2l3-audit:904-926`
and `:1367`), which the Go port never creates. This plan rates the two
entries for the frames the port sends, which is `doubletag`'s hop and a
phone impersonation with a raw-frame observable, instead of asking the
record for an exception. Everything else stays inside raw frames,
with the WPAD listener the one host-stack seam, which `Deps.Listen`
already carries.

## Decisions

The parent plan's Decisions apply. Local to this phase:

- Each observable is one predicate over frames received within
  `Deps.Observe`, stated in the behavior's doc comment and pinned by a
  response fixture. Why: a predicate is what the test can mutate and what
  the matrix's `Observable` column can quote.
- A predicate attributes a response to its sender: the Ethernet source is
  not the attack leg's MAC, and where the protocol scopes a conversation
  (an HSRP group, a VRID, an EIGRP autonomous system, a DHCP transaction
  id, an LLMNR query id) the response carries the probe's value. Where
  the protocol emits the frame the predicate reads without any probe (an
  HSRP or GLBP hello, a VRRP advertisement, a VTP summary, a DTP frame, a
  BPDU, an LLDP-MED or CDP advertisement), the check first reads the leg
  for one protocol interval, bounded by the window, and confirms only on
  a change from what that read showed: a state the pre-read did not
  carry, a revision above the one it carried, a sender that falls silent.
  A pre-read that shows nothing of the protocol ends in `pending` with
  the reason, since there is nothing to change. Why: the phase 1 review
  found `eigrp.go:148` reading any protocol 88 packet with the flag as
  the goodbye, `full/gates.go:122-128` arms `voicevlan` because recon
  already saw the switch's own voice VLAN advertisement, and a predicate
  without a baseline turns what the segment was already saying into a
  verdict.
- A check whose probe needs sustaining (a hello, an advertisement, a BPDU)
  alternates one send with one bounded read of the protocol's hello
  interval until the window is spent or the predicate matches, through
  `check.Sustain` (U1), whose `match` closure may keep the last state it
  saw and whose return at the window's end hands that state back to the
  caller. Why: the runner's paced leg (phase 1) spaces sends but cannot
  repeat them, a coup followed by silence is restored by the real
  router's hold timer (RFC 2281 section 5.1 Holdtime, RFC 3768 section 6.1
  Master_Down_Interval), and `check.Emitter` supports one goroutine, the
  behavior's own (`src/edge/netpen/check/check.go:161-163`), so a sender
  started through `spawn.Go` would need its own serialization. One
  goroutine that alternates is simpler.
- A behavior whose confirmation needs a protocol timer longer than the
  window returns `pending` with a detail naming the window to pass (for
  OSPF `pass --observe 20s, the hello interval is 10s`). The run option
  stays the only window and no check carries its own default. Why: the
  parent's open question asked whether a per-check default is needed, and
  a detail that names the window gives the operator the same outcome with
  one knob and no hidden timer.
- Response fixtures come from a second source: each response frame is
  authored from the RFC's field layout (or the owned layer's documented
  layout where no RFC exists) and decoded by the gopacket or owned-layer
  decoder as the second reader, with the harvest header saying so. The
  Wireshark sample capture page (https://wiki.wireshark.org/SampleCaptures,
  fetched 2026-10-15) carries no licence statement for its capture files
  (its one licence note covers the ST2110-40 samples alone), so no frame
  is copied from a capture, which is the outcome the Open question on the
  licence named. Why:
  `docs/solutions/conventions/a-permissive-synthetic-tier-passes-attacks-a-real-vendor-rejects.md`,
  and `AGENTS.md`, Investigation discipline. A fixture authored from the
  same generator as the probe proves nothing about the response; a
  decoder that reads it back is the second reader.
- The first-hop HSRP predicate is `isHSRPFor`, which reads any opcode for
  the group from another sender, since a Resign is the evidence a coup
  wants and a hello-only predicate would miss it. The caller reads the
  opcode and state. Why: the unit text named `isHSRPHelloFor`, and the
  name follows what the function reads.
- An actively restored behavior arms its step before its pre-read, so a
  pre-read that ends in `pending` with nothing sent still leaves a step
  for the runner, whose teardown refuses an active entry that armed none
  (`runner/runner.go`, `armed zero teardown steps`). The step then sends a
  frame the protocol ignores: an HSRP Resign from a router that was never
  active (RFC 2281 section 5.7 acts on a Resign from the active router), a
  GLBP listen-state hello at priority 100, a DTP frame naming the access
  state the port already has. Why: the runner rule is phase 1's and holds;
  a step that does what its name says is what requirement 31 asks.
- `check.Deps` gains `Sysctl`, an interface with `Get(key string) (string,
  error)` and `Set(key, value string) error`, that the runner implements
  over `/proc/sys/<key with dots as slashes>` and a test replaces with a
  map. Only `portsteal --relay` uses it, to set `net.ipv4.ip_forward` to
  `1` before its first frame and restore the value it read in its armed
  step. Why: the parent's Out of scope says `portsteal --relay` "enables
  and restores host forwarding and nothing more", the baseline does it
  through that file (`tools/l2l3-audit/l2l3-audit:242-249`), and a seam the
  test replaces keeps the behavior tests off the host. No netlink seam is
  added: the only step that would use one (`vlan-interface-restore`)
  restores an interface the port never creates, and
  `docs/conventions/dependencies.md` makes a new direct dependency a
  separate, waited change.
- `vlanhop` keeps its name and takes `doubletag`'s costs (none, none,
  none, `WatchOptional`) and `doubletag`'s observable, and its `persist`
  mode goes. Why: `attacks/l2/vlanhop.go:37` sends `craftDoubleTagFrame`,
  the frame `doubletag` sends, its armed step returns nil (`:44-49`), and
  the `persist` mode is rated permanent for a host subinterface the port
  never creates. The hop the baseline performs (that subinterface with a
  DHCP lease) is this phase's stop condition. The name stays because
  `full/gates.go:116` arms it from recon for the first three VLAN ids,
  which phase 3 reads. Deleting the entry instead is the alternative
  under Open questions. Unconfirmed.
- `voicevlan` keeps both modes with honest costs: the base entry is
  temporary, active, clients, and its armed step `lldp-shutdown` sends an
  LLDPDU with TTL 0, the shutdown form that tells the receiver to discard
  the neighbor entry (IEEE 802.1AB, unverified: the standard is not
  vendored, and gopacket's `lldp.go` carries no shutdown note). The
  `persist` mode is temporary, decay with the 120 s bound the frame's TTL
  announces, clients, with nothing armed. Why: the phase 1 review found
  the active step a no-op and the permanent rating rests on the
  baseline's host interface. The mode keeps its flag and its meaning (no
  restore) with the decay the frame itself states.
- The LLDP-MED network policy the `voicevlan` probe carries is rebuilt as
  an organizationally specific TLV (type 127, OUI `00:12:bb`, subtype 2)
  whose four info octets are the application type, then the U, T, and X
  flags over a 12-bit VLAN id, then a 3-bit L2 priority and a 6-bit DSCP,
  the layout gopacket's decoder reads at
  `~/go/pkg/mod/github.com/gopacket/gopacket@v1.7.3/layers/lldp.go:1052-1063`
  (`VLANId = (b & 0x1ffe) >> 1`, `L2Priority = (b & 0x01c0) >> 6`,
  `DSCPValue = o.Info[3] & 0x3f`), preceded by the MED Capabilities TLV
  (subtype 1, class 3) the baseline sends (`tools/l2l3-audit/l2l3-audit:1361`).
  The 127 bound from phase 1 goes, since the id has twelve bits. Why:
  `attacks/l2/voicevlan.go:98-104` emits a plain TLV of type 8 with the id
  in seven bits, which no MED receiver parses as a network policy. The
  pinned bytes change. This amends the parent's byte-pin Decision for the
  fixtures this plan names (`vrrp`, `voicevlan`, `ndpspoof`, `stproot`,
  the EIGRP set, `roguedhcp`), each of which pinned a frame its receiver
  would not read as meant, as the OSPF checksum fix set the precedent.
- `portsteal` is rated temporary, decay, clients on the base entry and
  temporary, active, clients on `relay`. Why: its frames are gratuitous
  ARP replies that enter the hosts' caches (`attacks/l2/portsteal.go:79-96`),
  and the switch learns nothing from them that the CAM would not relearn
  on the victim's next frame.
- `arpspoof` drops the `ip-forward-restore` step. Why: it returns nil
  (`attacks/fh/arpspoof.go:43-45`), the behavior changes no forwarding, and
  the parent's requirement 8 example arms two repair frames only.
- `etherchannel`'s `port-channel-release` step sends an LACPDU whose actor
  state carries no Synchronization, Collecting, or Distributing bit, the
  departure its own comment describes (`attacks/l2/etherchannel.go:38-39`).
  What the partner does with it is unverified against IEEE 802.1AX, which
  is not vendored. Why: a step that sends the frame the comment names does
  what its name says, and the lab is where the partner's response is
  recorded.
- The VRRP advertisement carries the checksum RFC 3768 section 5.3.8
  defines, "the 16-bit one's complement of the one's complement sum of
  the entire VRRP message starting with the version field", with the
  checksum field zero while summing, and the probe fails the receiver's
  check otherwise: section 7.1 has the receiver "MUST verify the VRRP
  checksum" and discard on failure. The body also carries the eight
  Authentication Data octets, zero, that close the VRRPv2 layout
  (`~/go/pkg/mod/github.com/gopacket/gopacket@v1.7.3/layers/vrrp.go:20-38`
  draws them after the addresses). Why: `attacks/fh/vrrp.go:80-87`
  builds `8+4*len(vips)` bytes with the checksum zero, and the fixture
  pins both. The crafter and the generator each compute the checksum,
  and a residue test over the emitted bytes proves it,
  per `docs/solutions/architecture-patterns/a-packet-fixture-generator-must-compute-the-checksums-gopacket-leaves-on-a-raw-payload.md`.
- The EIGRP hello carries the Parameters TLV (type `0x0001`, length 12:
  K1 to K6 then a 2-octet hold time, RFC 7868 section 6.7.1, defaults K1
  1, K2 0, K3 1, K4 0, K5 0, K6 0) and the goodbye is a hello carrying the
  Peer Termination TLV (type `0x0007`, section 6.7.7, "used anytime a
  router needs to reset an adjacency, or signal an adjacency it is going
  down"), which the predicate also accepts in its older form, a
  Parameters TLV with K1 to K5 all 255. Why: section 5.3.2 has "Two
  routers become neighbors only if the K-values are the same", so a hello
  without the TLV forms nothing, and `attacks/routing/eigrp.go:40` sends
  and `:157` reads header flag `0x02`, which section 6.5 defines as the
  CR-Flag, not a goodbye. The pinned bytes change with the correction.
- `roguedhcp`, `roguedhcp6`, `llmnr`, and `wpad` answer the query they
  observe within the window instead of sending an unsolicited answer with
  a fixed transaction id. Why: a client matches an OFFER to its DISCOVER
  by `xid` (RFC 2131 section 4.4.1 selects among offers to its own
  request), a DHCPv6 client to its SOLICIT by transaction id, and an LLMNR
  sender by the id "copied from the query to the response" (RFC 4795
  section 2.1.1), so the fixed ids at `attacks/ip6/helpers.go:32`,
  `attacks/fh/llmnr.go:100`, and `attacks/routing/wpad.go:154,205` reach
  no client. `roguedhcp6` already waits for the SOLICIT.
- The WPAD listener binds the attack leg's IPv4 on port 80 through
  `Deps.Listen`, and its handler records each request path. Why:
  `attacks/routing/wpad.go:114` binds `127.0.0.1:0`, which no client the
  NBNS or LLMNR answer points at can reach, so the PAC was never
  fetchable. The port-collision code stays for a bind failure.
- Four cache-poisoning checks probe their own effect with an ICMP echo
  request whose addresses the poison decides, so the reply's Ethernet
  destination is the verdict: `arpspoof` sends the request to the victim
  from the gateway's IPv4, `gratarp` to the gateway from the virtual IP,
  `portsteal` to the gateway from the victim's IPv4, and `ndpspoof` to the
  victim from `Server6`. A reply addressed to the attack leg's MAC
  confirms, a reply or an ARP or neighbor solicitation for the spoofed
  address seen with another destination resists, and silence is
  `pending` with the reason. Why: poisoned caches show only in traffic,
  and a request the attacker sends is traffic the victim must answer, so
  the window no longer depends on the victim talking. `ndpspoof`'s
  advertisement changes target: it tells the victim that `Server6`'s
  address is at the attack leg's MAC (today it names the victim's own
  address, `attacks/ip6/ndpspoof.go:84`), with the Override flag set and
  the Solicited flag clear, which RFC 4861 section 7.2.5 has update an
  existing entry's link-layer address and section 7.2.6 has sent
  unsolicited. A victim with no entry for `Server6` discards it (section
  7.2.5, "SHOULD be silently discarded"), which the detail names.
- `camflood` sends 10,000 stamped frames, then a unicast probe to the
  victim's MAC with a sentinel payload, and reads the watch leg. Why: five
  frames (`attacks/l2/camflood.go:50`) fill no CAM table, and a flooded
  switch shows its state only by forwarding a unicast frame to a port that
  does not own the address. The count is unconfirmed, since no vendored
  source states a CAM size, and is repeated under Open questions.
- `stproot`'s BPDU carries the baseline's timers: message age 0, max age
  6, hello time 1, forward delay 4 (`tools/l2l3-audit/l2l3-audit:1049-1052`),
  and the check sends one BPDU per hello time. Why: `attacks/l2/stproot.go:39-43`
  carries message age 15, max age 20, hello time 6, forward delay 2,
  which the port drifted to, and the catalog's 6 s bound
  (`attacks/l2/register.go:37`) is the baseline's max age. The timer unit
  stays unverified (IEEE 802.1D is not vendored), which is the open
  question phase 1 left, and the pinned bytes change with the baseline
  parity.
- `doubletag`, `vlanhop`, and `camflood` become `WatchOptional` and
  return `pending` without a watch leg, as `raguard` does. Why: their
  observable lives on the other side of the switch. `voicevlan` stays
  `AttackOnly`: the LLDP-MED TLV requirement 17 reads is the switch's
  LLDPDU to the port the phone is on, which arrives on the attack leg, so a
  watch leg adds nothing to it.
- The `mvrp` predicate reads a JoinIn event alone, since the owned decoder
  (`layers/mvrp.go`) defines no plain In event; the matrix row says the
  registrar semantics wait for the lab. The `dtp` and `vtp` pre-reads are
  bounded by half the window, since no vendored source states either
  protocol's interval and the probe keeps the other half.
- `check.observe` treats a receive channel that closes under a live
  context as an error (`leg closed before the window`), which the runner
  turns into a `pending` verdict. Why: the phase 1 review found a watch
  leg that closes early reads as `resisted` (`ghost.go:104`,
  `raguard.go:89`, `daddos.go:64`), and the Linux leg drops a terminal
  error when its one-slot channel is full (`link/link_linux.go:174`).
- `check` gains `Scan(ctx, leg, window, each func([]byte))`, which hands
  each frame to `each` and keeps none, for observers that only count or
  collect a field. `vlanenum` moves to it. Why: the phase 1 review found
  `Collect` keeps every tagged frame until the window ends.

## Requirements

Parent requirements 8 and 16 are claimed here. Each row names the
predicate and the fixture that pins it. "From another sender" means an
Ethernet source other than the attack leg's MAC.

1. `arpspoof` sends its two poison replies, then an ICMP echo request to
   the victim's MAC from the gateway's IPv4, and observes the attack leg.
   Example: an echo reply from the victim's MAC whose Ethernet destination
   is the attack leg's MAC and whose IPv4 destination is the gateway's
   address, within the window, is `confirmed` with detail `the victim
   sent its gateway traffic to the attack leg`. An echo reply or an ARP
   request for the gateway's address seen with another Ethernet
   destination is `resisted`. Silence is `pending` with `no reply from the
   victim in <window>`. The two repair frames remain the only armed steps.
   Fixture: `fh/arpspoof_response.pcap`, spec-authored (an ARP request
   and an ICMP echo reply that gopacket decodes).
2. `gratarp` reads `Gateway` as well as `VirtualIP`, sends its three
   announcements, then an echo request to the gateway's MAC from the
   virtual IP. Example: the gateway's reply to the attack leg's MAC is
   `confirmed`, an ARP request from the gateway for the virtual IP is
   `resisted` with `the gateway asked who-has <vip>`, silence is `pending`.
   Fixture: `fh/gratarp_response.pcap`, spec-authored.
3. `hsrp` reads the leg for one Hellotime (3 s, RFC 2281 section 5.1)
   for a hello from another sender for the target group in state Active,
   then sends the Coup and a hello every Hellotime until the window ends
   or the predicate matches, with the Resign armed. Example: after the
   Coup, a Resign from that sender, a hello from it in state Speak,
   Standby, or Listen, or its silence for one Holdtime (10 s) is
   `confirmed` (section 5.7, event j in Active: send Resign, move to
   Speak). A hello from it still in state Active at the window's end is
   `resisted`. No Active hello for the group in the pre-read is `pending`
   with `no active router for group <n> in <window>`, and a window under
   6 s is `pending` naming `--observe 15s`. The last hello seen decides.
   The group byte and virtual IP of every sent frame are read back by the
   test for group 0. Fixture: `fh/hsrp_response.pcap`, frames from
   Wireshark `hsrp.pcap`.
4. `glbp` follows requirement 3's shape with its hello TLV state and a
   3 s interval: an Active hello in the pre-read, then the active hello
   sustained, and `confirmed` when that sender's state leaves Active or
   it falls silent for 10 s, `resisted` when it stays Active, `pending`
   with the reason otherwise. The state transitions are unverified (no
   RFC, no vendored source), and the matrix row says so. Fixture:
   `fh/glbp_response.pcap`, spec-authored from `packet-glbp.c`.
5. `vrrp` reads the leg for one Advertisement_Interval (1 s, RFC 3768
   section 6.1) for an advertisement for the VRID from another sender
   with priority below 120, then sends its advertisement with the
   computed checksum every second until the window ends. Example: that
   sender silent for `3 * 1 s + 1 s` after the first probe is `confirmed`
   (section 6.4.3: a Master receiving a higher priority transitions to
   Backup), one that keeps advertising is `resisted`, no advertisement
   for the VRID in the pre-read is `pending` with `no master for VRID <n>
   in <window>`, and a window under 5 s is `pending` naming
   `--observe 6s`. A residue test over the sent body gives `0xffff` and
   the body ends in eight zero octets. Fixture: `fh/vrrp.pcap`
   regenerated and `fh/vrrp_response.pcap`, spec-authored.
6. `icmpredirect` observes the attack leg for a frame from the victim's
   MAC to the attack leg's MAC whose IPv4 destination is the redirected
   address. Example: such a frame is `confirmed`, silence is `pending`
   with `the victim sends to <dst> on its own schedule`. No fixture of a
   response is needed since the frame is any IPv4 datagram, and the test
   pushes one.
7. `llmnr` waits for an LLMNR query (UDP 5355 to 224.0.0.252, QR clear)
   from the victim's MAC within the window, answers it unicast to the
   query's source MAC, IPv4, and UDP port (RFC 4795 section 2.3, "MUST be
   sent to the sender via unicast") with the query's id and name and the
   attack leg's IPv4, and is `confirmed` with `answered <name> for
   <victim>`. Example: no query in the window is
   `pending` with `no LLMNR query from the victim in <window>`. The detail
   says acceptance of the answer is not observed, since the follow-up
   connection is the victim's stack. Fixture: `fh/llmnr_query.pcap`,
   spec-authored (RFC 4795 section 2.1.1 header).
8. `lldpspoof` and `arpsweep` keep their verdicts. `lldpspoof` reads no
   frame and returns `pending` with `the neighbor table is readable only
   over management`.
9. `stproot` reads the leg for one hello time for a BPDU from another
   sender and records its root identifier, then sends its BPDU every
   hello time for the window. Example: a BPDU from another sender whose
   root identifier is priority 0 and the attack leg's MAC is `confirmed`,
   one still naming the pre-read's root at the window's end is `resisted`
   with `the neighbor keeps root <id>`, no BPDU before or after is
   `resisted` with `no BPDU in <window>`. Fixture: `l2/stproot.pcap`
   regenerated, and `l2/stproot_response.pcap`, a BPDU from Wireshark
   `stp.pcap` with its root identifier rewritten to the fixture bridge
   id, the rewrite noted in the harvest header.
10. `dtp` reads the leg for one DTP frame from another sender before
    sending, then observes the next one after. Example: a trunk status
    byte with bit `0x80` set after the probe, when the pre-read's byte had
    it clear or showed no DTP, is `confirmed`, any other byte after the
    probe (`0x02`, `0x03`, `0x04`, or an unknown value, each named in the
    detail) is `resisted`, no DTP frame after the probe is `resisted` with
    `no DTP reply in <window>`, and a pre-read already trunking is
    `pending` with `the port was trunking before the probe`. The bit
    reading rests on the owned layer's `0x81` (`src/edge/netpen/layers/dtp.go:38`)
    and is otherwise unverified. Fixture: `l2/dtp_response.pcap`, a frame
    from Wireshark `DTP.pcapng`.
11. `vtp` reads the leg for one summary advertisement from another sender
    in the target domain and sends its advertisement with that revision
    plus one (the `set` mode) or zero (the `wipe` mode), then observes the
    next summary. Example: on the base and `set` modes a revision at or
    above the sent one is `confirmed`, the pre-read's revision again is
    `resisted`; on `wipe` a revision of 0 is `confirmed` and the pre-read's
    revision `resisted`; no summary in the pre-read is `pending` with `no
    VTP summary for domain <d> in <window>` and nothing is sent; none
    after the probe is `pending` with `no summary in <window>`. The test
    feeds a pre-read summary with revision 41 so `l2/vtp.pcap` holds.
    Fixture: `l2/vtp_response.pcap`, spec-authored through the owned
    decoder.
12. `camflood` sends 10,000 frames, then one unicast frame to the victim's
    MAC with payload `netpen-camflood`, and reads the watch leg. Example:
    that frame on the watch leg is `confirmed`, its absence `resisted`, no
    watch leg `pending`. The test pins the first five frames' bytes and
    the stamping rule, and counts sends.
13. `doubletag` and `vlanhop` read the watch leg for their echo request.
    Example: an IPv4 ICMP echo request from the attack leg's MAC with
    source `10.0.0.1` and destination `10.0.0.2`, tagged with the inner
    VLAN or untagged, is `confirmed`, its absence `resisted`, no watch leg
    `pending`. `vlanhop` arms nothing and has no mode.
14. `etherchannel` observes an LACPDU from another sender whose partner
    system is the attack leg's MAC with the Synchronization bit set, or a
    PAgP frame whose partner device id is the attack leg's MAC. Example:
    either is `confirmed`, an LACPDU naming another partner at the
    window's end is `resisted`, silence is `resisted` with `no LACP or
    PAgP from the switch in <window>`. The armed step sends the departure
    LACPDU. Fixture: `l2/etherchannel_response.pcap`, a frame from
    Wireshark `lacp1.pcap.gz` with the partner fields rewritten to the
    fixture MAC.
15. `mvrp` observes an MVRP frame from another sender carrying one of its
    VIDs with a JoinIn or In event. Example: such a frame is `confirmed`,
    none in the window `resisted`. The registrar semantics are unverified
    (IEEE 802.1Q clause 10 is not vendored). Fixture:
    `l2/mvrp_response.pcap`, spec-authored through the owned decoder.
16. `portsteal` reads `Gateway` as well as `Victim`, sends its three
    announcements, then an echo request to the gateway's MAC from the
    victim's IPv4, and reads the reply as requirement 2 does. `relay`
    sets `net.ipv4.ip_forward` to `1` through `Deps.Sysctl` before the
    first frame and its armed step writes back the value read. Example:
    with a map sysctl holding `0`, the run leaves `1` during the behavior
    and `0` after the teardown, and the step name is `ip-forward-restore`.
17. `voicevlan` reads the leg for 30 s or the window, whichever is
    shorter, noting whether an LLDP-MED network policy TLV from another
    sender already names the target voice VLAN, then sends the rebuilt
    LLDP-MED frame and observes. Example: such a TLV after the probe, when
    the pre-read carried none, is `confirmed`, the switch's own CDP VoIP
    VLAN Reply and tagged traffic count for nothing since both arrive
    without a probe (`full/gates.go:122-128` arms this check from exactly
    that CDP frame), none after the probe is `resisted` with `no MED
    policy for VLAN <n> in <window>`, and a pre-read that already carried
    it is `pending` with `the switch advertised the policy before the
    probe`. Whether a switch sends LLDP-MED TLVs only after hearing a MED
    endpoint is unverified (ANSI/TIA-1057 is not vendored) and the matrix
    row says so. The TTL-0 shutdown is the armed step on the base entry. Fixture: `l2/voicevlan.pcap` regenerated, and
    `l2/voicevlan_response.pcap` from Wireshark `lldpmed_civicloc.pcap`
    and `cdp.pcap`.
18. `vlanenum` counts VIDs 1 to 4094 only and reads through `check.Scan`,
    which returns no frames. Example: a priority-tagged frame (VID 0) is
    skipped. The memory bound holds by construction, since `Scan` keeps no
    frame, and no test in U3 covers it.
19. `roguera` observes a neighbor solicitation from another sender whose
    source is unspecified and whose target lies within `Prefix6`, the DAD
    a host runs on an address it forms from the prefix (RFC 4862 section
    5.5.3 item (d), section 5.4). Example: such a solicitation is
    `confirmed`, none is `resisted` with `no host formed an address in
    <prefix> within <window>`. Fixture: `ip6/roguera_response.pcap`,
    spec-authored.
20. `raflood` observes a neighbor solicitation whose target is one of its
    stamped link-local sources. Example: one is `confirmed`, none is
    `pending` with `no host probed a flood router in <window>: a host
    probes a router only when it sends traffic` (RFC 4861 section 7.3.3,
    "If no traffic is sent to a neighbor, no probes are sent").
21. `ndpspoof` reads `Victim6`, `Victim6MAC`, and `Server6`, sends the
    unsolicited advertisement for `Server6`'s address, then an ICMPv6
    echo request to the victim from `Server6`'s address, and reads the
    reply as requirement 1 does. Example: a reply to the attack leg's MAC
    is `confirmed`, a neighbor solicitation from the victim for `Server6`
    or a reply to another MAC is `resisted`, silence is `pending`. The
    detail of `resisted` names the discard rule for a missing entry.
    Fixture: `ip6/ndpspoof.pcap` regenerated, `ip6/ndpspoof_response.pcap`
    spec-authored.
22. `daddos` returns `resisted` when a neighbor advertisement for the
    target arrives within the window (a host holds the address) and
    `pending` with `no defense in <window>: abandonment leaves no frame`
    otherwise. The finding keeps its fields.
23. `dhcpstarve` observes OFFERs (RFC 2131 section 3.1 step 2) to its
    chaddr values. Example: offers to all five is `confirmed` with `the
    server leased to 5 of 5 spoofed clients`, fewer is `confirmed` with
    the count, none is `resisted` with `no offer in <window>`. Fixture:
    `ip6/dhcpstarve_response.pcap`, an OFFER from Wireshark `dhcp.pcap`
    with chaddr rewritten.
24. `roguedhcp` waits for a DISCOVER from the victim's MAC, answers with
    an OFFER carrying its `xid`, waits for a REQUEST whose server
    identifier option is the attack leg's IPv4 (section 4.4.1), and
    answers with the ACK. Example: the REQUEST is `confirmed`, a REQUEST
    naming another server is `resisted`, no DISCOVER is `pending` with
    `no DISCOVER from the victim in <window>`, an OFFER sent and no
    REQUEST is `resisted` with `the victim did not request <ip>`. The
    test feeds a DISCOVER with xid `0x12345678` so the offer and ack
    fixtures hold. Fixture: `ip6/roguedhcp_query.pcap` from Wireshark
    `dhcp.pcap` with chaddr and xid rewritten.
25. `roguedhcp6` after its ADVERTISE waits for a REQUEST from the victim
    whose Server Identifier option is its DUID (RFC 8415 section 16.4,
    18.2.2). Example: that REQUEST is `confirmed`, one naming another DUID
    is `resisted`, none is `resisted` with `the client did not request
    the lease`, and the detail for a quiet leg reads the time listened,
    not the window. Fixture: `ip6/roguedhcp6_request.pcap`, a REQUEST
    from Wireshark `DHCPv6.pcap` with the identifiers rewritten.
26. `mld` observes traffic to its group from another sender. Example: an
    IPv6 packet to `ff0e::1234` is `confirmed`, none is `pending` with
    `no group traffic in <window>: a Report draws no reply (RFC 2710
    section 4)`.
27. `raguard` keeps its predicate. The `(c)` label on its matrix row
    becomes `(a)`, spec-authored, as its harvest header says.
28. `ospf` sends its hello, reads the target's hello, sends a second hello
    listing the target's router ID, and then reads for a hello from the
    target listing the attack leg's router ID (RFC 2328 section 10.5,
    2-WayReceived), sending a hello every 10 s meanwhile. Example: that
    hello is `confirmed`, a hello from the target without it after the
    window has passed one hello interval is `resisted`, no hello from the
    target is `resisted` with `no OSPF hello in <window>`, and a window
    under 10 s is `pending` naming `--observe 20s`. The DBD and LSA update
    follow the 2-Way hello as today. Fixture: `routing/ospf_target.pcap`
    extended with a hello listing router ID `10.0.0.99` from Wireshark
    `ospf.cap` with the neighbor rewritten.
29. `eigrp` sends the hello with the Parameters TLV, reads a hello from
    another sender in its AS, sends the null UPDATE with the INIT flag
    (RFC 7868 section 5.3.3), and reads for the target's acknowledgment,
    a unicast hello to the attack leg's IPv4 with a non-zero Ack number
    (section 5.2). Example: the acknowledgment is `confirmed`, a hello
    carrying the Peer Termination TLV or K-values of 255 from a sender in
    the AS is `resisted` with `adjacency refused`, no hello in the AS is
    `resisted` with `no EIGRP hello for AS <n> in <window>`. The route
    UPDATE follows the acknowledgment as today. Fixture: `routing/eigrp*.pcap`
    regenerated, `routing/eigrp_target.pcap` from Wireshark
    `EIGRP_Neighbors.cap` with the AS rewritten.
30. `wpad` opens the listener on the attack leg's IPv4 port 80, waits for
    an NBNS or LLMNR query for `wpad` within the window, answers it
    unicast to the query's source MAC, IPv4, and UDP port with the query's
    id, and reads the listener's request log for `/wpad.dat`.
    Example: a request is `confirmed` with `a client fetched the PAC`, a
    query answered and no request is `resisted` with `no PAC fetch in
    <window>`, no query is `pending` with `no wpad query in <window>`.
    The test's in-memory listener delivers a GET.
31. Every armed step performs work (parent requirement 8): the steps are
    `neighbor-unicast-repair-victim` and `-gateway` (arpspoof),
    `hsrp-resign`, `glbp-resign`, `access-port-restore` (dtp),
    `ip-forward-restore` (portsteal relay), `lldp-shutdown` (voicevlan),
    `port-channel-release` (etherchannel), `ospf-lsa-flush`,
    `ospf-goodbye`, and `eigrp-goodbye`. Example: `TestTeardownStepCounts`
    in `routing_test.go:803` gains a sibling in `fh_test.go` and
    `l2_test.go`, each listing its family's steps by name and asserting
    the step's recorded send or sysctl write.
32. The matrix's `Observable` column quotes each predicate and the
    detail strings above, the first five columns match the catalog, and
    `TestValidationMatrixCompleteness` passes.

## Out of scope

- Any new behavior. Phase 4 adds checks.
- Recon filling the target and the zero-source sweep request. Phase 3.
- Relaying victim traffic. `portsteal --relay` sets and restores
  forwarding and nothing more.
- The host-subinterface hop of the baseline's `vlanhop` and `voicevlan`.
  It needs a DHCP client, the stop condition.
- Vendor truth. A `confirmed` verdict is netpen's own observation and the
  T2 cells stay the lab's. The T1 FRR segment runs `ospfd` only
  (`src/edge/netpen/test/integration/t1/testdata/frr/r1/daemons:6`), so no
  response fixture is captured from it.
- Input authors: the frames the predicates read come from the segment,
  which is untrusted, and every predicate decodes with gopacket or an
  owned decoder and returns false for what it cannot read. The response
  fixtures are written by contributors.

## Units

### U1. Observation helpers and the sysctl seam

Files: src/edge/netpen/check/check.go, src/edge/netpen/check/observe.go,
src/edge/netpen/check/observe_test.go, src/edge/netpen/runner/runner.go,
src/edge/netpen/runner/options.go, src/edge/netpen/runner/sysctl.go,
src/edge/netpen/runner/sysctl_test.go, src/edge/netpen/runner/runner_test.go,
src/edge/netpen/attacks/fh/ghost_test.go, src/edge/netpen/attacks/routing/receive_test.go
After: none
Change: `check.observe` returns an error `leg closed before the window`
when the frame channel closes while `ctx` is live. `TestGhostTruncatedFrames`
(`attacks/fh/ghost_test.go:149-179`) and the "closed receiver proceeds"
rows of `attacks/routing/receive_test.go:82-95` move to the error, since
they pin the old rule. `check.Scan` hands each frame to a callback and
keeps none, returning the `Observation`. `check.Sustain(ctx, leg, window,
interval, send func(context.Context) error, match func([]byte) bool)`
sends, observes with `First` for `interval` (or what is left of the
window), and repeats until a match or the window's end, returning the
matched frame (nil at the window's end) and an `Observation` whose
`Elapsed` spans the whole call. `match` may record what it saw in the
caller's variables, which is how a check reads the last state at the
window's end. `check.Deps` gains `Sysctl Sysctl`, an
interface with `Get` and `Set` over a dotted key. The runner's
`procSysctl` maps `net.ipv4.ip_forward` to `/proc/sys/net/ipv4/ip_forward`
and reads and writes the file, and `Options.Sysctl` lets a test pass a map
implementation (`runner.MapSysctl`).
Tests: `check/observe_test.go`: `TestObserveClosedLegIsAnError`,
`TestScanKeepsNoFrame`, `TestSustainSendsOncePerInterval` (synctest, three
sends in a 9 s window at 3 s), `TestSustainStopsOnMatch`,
`TestSustainReturnsAtTheWindow`. `runner/sysctl_test.go`: `procSysctl`
against a temp directory root, `MapSysctl` round trip. `runner_test.go`: a
behavior that reads `deps.Sysctl` sees the map.
Verify: `uv run tools/scripts/run.py verify change -- src/edge/netpen/check src/edge/netpen/runner src/edge/netpen/attacks`

### U2. First-hop observables

Files: src/edge/netpen/attacks/fh/arpspoof.go, src/edge/netpen/attacks/fh/gratarp.go,
src/edge/netpen/attacks/fh/hsrp.go, src/edge/netpen/attacks/fh/glbp.go,
src/edge/netpen/attacks/fh/vrrp.go, src/edge/netpen/attacks/fh/icmpredirect.go,
src/edge/netpen/attacks/fh/llmnr.go, src/edge/netpen/attacks/fh/lldpspoof.go,
src/edge/netpen/attacks/fh/helpers.go, src/edge/netpen/attacks/fh/observe.go,
src/edge/netpen/attacks/fh/register.go, src/edge/netpen/attacks/fh/harvest_fh.go,
src/edge/netpen/attacks/fh/fh_test.go, src/edge/netpen/attacks/fh/observe_test.go,
src/edge/netpen/attacks/testdata/fh/vrrp.pcap,
src/edge/netpen/attacks/testdata/fh/arpspoof_response.pcap,
src/edge/netpen/attacks/testdata/fh/gratarp_response.pcap,
src/edge/netpen/attacks/testdata/fh/hsrp_response.pcap,
src/edge/netpen/attacks/testdata/fh/glbp_response.pcap,
src/edge/netpen/attacks/testdata/fh/vrrp_response.pcap,
src/edge/netpen/attacks/testdata/fh/llmnr_query.pcap
After: U1
Change: requirements 1 to 8. `observe.go` holds the family's predicates
(`isEchoReplyTo`, `isHSRPHelloFor`, `isGLBPHelloFor`, `isVRRPAdvertFor`,
`isLLMNRQueryFrom`, `isARPRequestFor`), each decoding with gopacket or the
owned layer and false on what it cannot read. `craftVRRP` computes the
checksum and `harvest_fh.go` computes it independently. `gratarp` reads
`Gateway`. `hsrp` and `glbp` and `vrrp` use `check.Sustain`. `arpspoof`
arms two steps. The file headers of `arpspoof.go` and `llmnr.go` carry
the cost fields and `harvest_fh.go:624` loses its em dash.
Tests: `fh_test.go` keeps the pins, with `vrrp.pcap` regenerated and
`TestVRRP_ChecksumResidue` summing the sent body to `0xffff`. `observe_test.go`
per behavior: the response fixture confirms, the mutated fixture (other
source MAC, other group, other VRID, other query id, reply to another
MAC) does not, the quiet leg gives the stated verdict, a window under 4 s
for `vrrp` is pending with the named flag, `TestHSRP_GroupZero` reads the
group byte of each sent frame, and `TestTeardownStepCounts` lists the
armed steps.
Verify: `uv run tools/scripts/run.py verify change -- src/edge/netpen/attacks/fh src/edge/netpen/attacks/testdata/fh`

### U3. Layer 2 observables and catalog costs

Files: src/edge/netpen/attacks/l2/stproot.go, src/edge/netpen/attacks/l2/dtp.go,
src/edge/netpen/attacks/l2/vtp.go, src/edge/netpen/attacks/l2/camflood.go,
src/edge/netpen/attacks/l2/doubletag.go, src/edge/netpen/attacks/l2/vlanhop.go,
src/edge/netpen/attacks/l2/etherchannel.go, src/edge/netpen/attacks/l2/mvrp.go,
src/edge/netpen/attacks/l2/portsteal.go, src/edge/netpen/attacks/l2/voicevlan.go,
src/edge/netpen/attacks/l2/vlanenum.go, src/edge/netpen/attacks/l2/craft.go,
src/edge/netpen/attacks/l2/helpers.go,
src/edge/netpen/attacks/l2/observe.go, src/edge/netpen/attacks/l2/register.go,
src/edge/netpen/attacks/l2/harvest_l2.go, src/edge/netpen/attacks/l2/l2_test.go,
src/edge/netpen/attacks/l2/vlanenum_test.go, src/edge/netpen/attacks/l2/observe_test.go,
src/edge/netpen/attacks/testdata/l2/voicevlan.pcap,
src/edge/netpen/attacks/testdata/l2/stproot.pcap,
src/edge/netpen/attacks/attacks_test.go, src/edge/netpen/runner/durability_test.go,
src/edge/netpen/catalog/zz_generated_catalog.go,
src/edge/netpen/attacks/testdata/l2/stproot_response.pcap,
src/edge/netpen/attacks/testdata/l2/dtp_response.pcap,
src/edge/netpen/attacks/testdata/l2/vtp_response.pcap,
src/edge/netpen/attacks/testdata/l2/etherchannel_response.pcap,
src/edge/netpen/attacks/testdata/l2/mvrp_response.pcap,
src/edge/netpen/attacks/testdata/l2/voicevlan_response.pcap,
src/edge/netpen/cmd/netpen/main.go, src/edge/netpen/cmd/netpen/main_test.go,
src/edge/netpen/test/integration/VALIDATION_MATRIX.md
After: U1
Change: requirements 9 to 18. `register.go` drops `vlanhop`'s mode and
arming, re-rates `vlanhop`, `portsteal`, and `voicevlan`, and sets
`WatchOptional` on `doubletag`, `vlanhop`, `camflood`, and `voicevlan`.
`go generate ./catalog/...` rewrites `zz_generated_catalog.go`, the cost
table in `attacks_test.go` follows, and `durability_test.go:227` takes
`vtp wipe` as its permanent stub. The regeneration is safe in this wave
because U2, U4, and U5 change no catalog field. The command drops
`vlanhop`'s `--persist` flag (`cmd/netpen/main.go:326`) and the
`voicevlan --persist` help text (`:329`) reads "leave the neighbor entry
to expire" without the permanent clause. `stproot.go` takes the
baseline's timers. The em dashes in `harvest_l2.go` go. `craft.go` loses the pooled buffers with no caller
once `camflood` serializes its template through `craft.Default` and
stamps copies. `vlanenum` uses `check.Scan` and skips VID 0 and 4095. The
matrix rows for the changed cost fields change in this unit, and only
those, so `TestValidationMatrixCompleteness` stays green between U3 and
U6. `stproot.go`'s header reads the hello time as the hello time.
`helpers.go` gains the LLDP destination MAC and the TTL constant the
voicevlan frames and the persist mode's decay bound share.
Tests: `l2_test.go` keeps the pins, with `voicevlan.pcap` regenerated and
decoded by gopacket's `LinkLayerDiscoveryInfo` to the target VLAN,
priority 5, DSCP 46. `TestVoiceVLAN_FullRange` sends VLAN 4094.
`observe_test.go` per behavior: confirm, mutate (other root id, status
`0x02`, lower revision, other partner system, other VID, other voice
VLAN), quiet leg, no watch leg. `TestPortSteal_RelaySetsAndRestoresForwarding`
with `runner.MapSysctl`. `TestCAMFlood_SendsTheProbeLast` counts 10,001
sends and pins the first five. `stproot.pcap` is regenerated and the
pre-read root is fed from the response fixture. `TestVTP_RevisionFollowsThePreRead`
feeds revision 41 and reads 42, 0, and 42 across the modes.
`TestTeardownStepCounts` lists `access-port-restore`, `ip-forward-restore`,
`lldp-shutdown`, and `port-channel-release`. `vlanenum_test.go`: VID 0
and 4095 skipped.
Verify: `uv run tools/scripts/run.py verify change -- src/edge/netpen/attacks/l2 src/edge/netpen/attacks/testdata/l2 src/edge/netpen/cmd/netpen src/edge/netpen/test/integration/VALIDATION_MATRIX.md`

### U4. IPv6 and DHCP observables

Files: src/edge/netpen/attacks/ip6/roguera.go, src/edge/netpen/attacks/ip6/raflood.go,
src/edge/netpen/attacks/ip6/ndpspoof.go, src/edge/netpen/attacks/ip6/daddos.go,
src/edge/netpen/attacks/ip6/dhcpstarve.go, src/edge/netpen/attacks/ip6/roguedhcp.go,
src/edge/netpen/attacks/ip6/roguedhcp6.go, src/edge/netpen/attacks/ip6/mld.go,
src/edge/netpen/attacks/ip6/raguard.go,
src/edge/netpen/attacks/ip6/helpers.go, src/edge/netpen/attacks/ip6/craft.go,
src/edge/netpen/attacks/ip6/observe.go, src/edge/netpen/attacks/ip6/harvest_ip6.go,
src/edge/netpen/attacks/ip6/ip6_test.go, src/edge/netpen/attacks/ip6/observe_test.go,
src/edge/netpen/attacks/ip6/receive_test.go,
src/edge/netpen/attacks/testdata/ip6/ndpspoof.pcap,
src/edge/netpen/attacks/testdata/ip6/roguedhcp.pcap,
src/edge/netpen/attacks/testdata/ip6/roguera_response.pcap,
src/edge/netpen/attacks/testdata/ip6/ndpspoof_response.pcap,
src/edge/netpen/attacks/testdata/ip6/dhcpstarve_response.pcap,
src/edge/netpen/attacks/testdata/ip6/roguedhcp_query.pcap,
src/edge/netpen/attacks/testdata/ip6/roguedhcp6_request.pcap
After: U1
Change: requirements 19 to 27. `observe.go` holds `isDADSolicitIn`,
`isSolicitForAny`, `isEchoReplyTo`, `isOfferFor`, `isDiscoverFrom`,
`isRequestNaming`, `isRequest6Naming`, `isGroupTraffic`. `linkLocalFromMAC`
returns an error for a MAC that is not six octets, and its callers,
`roguera.go:65` and `raguard.go:106`, return it coded
`check.ErrCodeTargetInvalid` with a detail naming the interface.
`craft.go`'s pooled path stays as it is for the flood crafts. The file
header of `roguedhcp.go` carries the cost fields and `craft.go:17` loses
its em dash. `roguedhcp6`'s quiet detail reads `obs.Elapsed`.
Tests: `ip6_test.go` keeps the pins, with `ndpspoof.pcap` regenerated and
`roguedhcp.pcap` pinned against a run fed the DISCOVER fixture.
`observe_test.go` per behavior: confirm, mutate (target outside the
prefix, other chaddr, other server identifier, other DUID, other group),
quiet leg, `TestDADDOS_LateDefenseIsRead` (`ip6_test.go:480`) reading
`resisted` where it read `pending`, and `TestDADDOS_NoDefense_AttackProceeds`
(`:448`) reading the new pending detail.
`receive_test.go` adds `roguedhcp` and `roguera` to the failure table.
Verify: `uv run tools/scripts/run.py verify change -- src/edge/netpen/attacks/ip6 src/edge/netpen/attacks/testdata/ip6`

### U5. Routing observables

Files: src/edge/netpen/attacks/routing/ospf.go, src/edge/netpen/attacks/routing/eigrp.go,
src/edge/netpen/attacks/routing/wpad.go, src/edge/netpen/attacks/routing/observe.go,
src/edge/netpen/attacks/routing/helpers.go,
src/edge/netpen/attacks/routing/harvest_routing.go,
src/edge/netpen/attacks/testdata/routing/ospf.pcap,
src/edge/netpen/attacks/routing/routing_test.go,
src/edge/netpen/attacks/routing/observe_test.go,
src/edge/netpen/attacks/routing/receive_test.go,
src/edge/netpen/attacks/routing/README.md,
src/edge/netpen/attacks/testdata/routing/eigrp.pcap,
src/edge/netpen/attacks/testdata/routing/eigrp_reject.pcap,
src/edge/netpen/attacks/testdata/routing/eigrp_restore.pcap,
src/edge/netpen/attacks/testdata/routing/eigrp_target.pcap,
src/edge/netpen/attacks/testdata/routing/ospf_target.pcap,
src/edge/netpen/attacks/testdata/routing/wpad.pcap,
src/edge/netpen/attacks/testdata/routing/wpad_query.pcap
After: U1
Change: requirements 28 to 30. `observe.go` holds `isOSPFHelloFrom`,
`isOSPFHelloListing`, `isEIGRPHelloInAS`, `isEIGRPGoodbyeInAS`,
`isEIGRPAckTo`, `isWPADQuery`. `craftOSPFHello` lists the target's router
ID on the second hello. `craftEIGRPHello` carries the Parameters TLV and
the goodbye carries the Peer Termination TLV. The WPAD listener binds
`<leg IPv4>:80`, its handler appends each request path to a slice under a
mutex, and the answers copy the query's id. `README.md` drops the
"tolerate a missing response after 100 ms" sentence and says what the
verdicts read. The em dashes in `harvest_routing.go` go. The route
UPDATE's TLV layout (`eigrp.go:274-290`) stays as it is, under the
parent's encoder audit exclusion. `helpers.go` loses the broadcast MAC
once the WPAD answer is unicast, and `ospf.pcap` is regenerated with the
hello listing the target as its second frame.
Tests: `routing_test.go` keeps the pins, with the EIGRP fixtures
regenerated and `TestEIGRP_HelloCarriesParameters` decoding the TLV with
the owned layer. `observe_test.go`: the 2-Way hello confirms, a hello
without the router ID after one interval resists, a short window is
pending naming the flag, the Peer Termination TLV and the K-255 form both
resist, an ack from another AS does not confirm, the in-memory listener's
GET confirms `wpad`, a query from the fixture is answered with its id.
`TestEIGRP_AuthMismatchRefusal` and `TestEIGRP_GoodbyeBehindAnotherFrameIsRead`
move to the TLV form.
Verify: `uv run tools/scripts/run.py verify change -- src/edge/netpen/attacks/routing src/edge/netpen/attacks/testdata/routing`

### U6. Matrix, README, and record text

Files: src/edge/netpen/test/integration/VALIDATION_MATRIX.md,
src/edge/netpen/README.md, src/edge/netpen/test/integration/matrix_test.go
After: U2, U3, U4, U5
Change: every `Observable` cell quotes its predicate and detail strings,
the `raguard` provenance reads `(a)`, the `vlanhop persist` row is gone, and
the README's check-model section names the sustain rule and the window
detail. `matrix_test.go` adds a check that no `Observable` cell starts
with `pending: no observable` for a behavior whose doc comment names a
predicate, by listing the behaviors that remain pending by design
(`lldpspoof`, `icmpredirect`, `raflood`, `daddos`, `mld`, `scan`).
Tests: `TestValidationMatrixCompleteness`, `TestMatrixPendingRowsAreTheDesignedOnes`.
Verify: `uv run tools/scripts/run.py verify change -- src/edge/netpen/test/integration src/edge/netpen/README.md`

Waves: U1 | U2 U3 U4 U5 | U6

## Verification

```bash
cd src/edge/netpen && go vet ./... && GOOS=linux go vet ./... && go test -race -count=1 ./check/... ./runner/... ./attacks/... ./cmd/... ./test/integration/...
uv run tools/scripts/run.py verify change -- src/edge/netpen docs/plans/2026-10-12-1259-feat-netpen-check-model-phase2-plan.md
```

Vendor truth for the new predicates needs a `t2` harness with a parser
for each device observable, as the lab direction record requires
(`docs/architecture/2026-09-23-netpen-lab-vendor-validation-direction.md`),
and only `ospf` has one (`test/integration/t2_lab_ospf_test.go`). That
harness is outside this phase.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] The matrix's `Observable` column quotes each predicate.
- [ ] Every response fixture's harvest header names its source: the
      Wireshark capture file and frame number, or the RFC section it was
      authored from.
- [ ] `src/edge/netpen/README.md` and `attacks/routing/README.md` updated
      in the same change.
- [ ] `uv run tools/scripts/run.py plan record implemented <phase> --units <n> --from <t> --to <t>`.
- [ ] No plan labels in code.

## Open questions

- `vlanhop` stays as a second name for `doubletag`'s frame with cost none
  (Decision, unconfirmed), two rows that cannot differ. The alternative is
  deleting the entry, which also touches `full/gates.go:116`, the `full`
  tests, and the phase 3 plan's recon table. The implementer keeps the
  entry unless told otherwise.
- The `camflood` count of 10,000 frames (Decision, unconfirmed). No
  vendored source states a CAM size, and at the default rate the burst is
  what bounds the check's run time.
- The `stproot` timer unit, carried from phase 1: the BPDU takes the
  baseline's max age 6 and hello time 1 (Decision), which matches the
  catalog's 6 s bound only if the field holds whole seconds. No vendored
  source gives the unit, so the bound stays 6 s and unverified.
- The DTP trunk status byte is read as a bitfield with `0x80` meaning
  trunking (Requirement 10), from the owned layer's `0x81` constant
  alone. Unverified against Cisco's format, which no vendored source
  gives.
- Whether a switch sends LLDP-MED TLVs only after hearing a MED endpoint
  (Requirement 17) is unverified, since ANSI/TIA-1057 is not vendored. A
  switch that sends them unprompted makes the check `pending` by the
  pre-read rule, never `confirmed`.
- The GLBP state transitions on a higher-priority hello and the LACP
  partner's reaction to the departure LACPDU are unverified (no RFC, no
  vendored standard). The predicates read the frames the owned decoders
  parse, and the matrix rows say the semantics wait for the lab.
- `ndpspoof` reads `Server6` as the address it claims. If the operator's
  segment has no IPv6 host worth impersonating, the check skips with the
  target error, which is the parent's requirement 7 shape.
