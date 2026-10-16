# netpen Validation Matrix

This matrix inventories every (behavior, mode) pair's intended ground-truth
source, per KTD15. The 2026-09-27 T1 run records source-(c)
self-consistency for all eight AE6 attacks: two executions produced the same
record-kind and finding-module class against the same FRR segment. These
results do not establish wire shape or a target response. The `ospf` T2 cell
separately records the 2026-09-25 vendor result. Fixture provenance labels do
not establish that this integration suite has executed a wire or vendor
assertion.

## Ground-truth sources

- **(a)** Python-fixture wire-shape truth: the characterization fixture
  harvested from `l2l3-audit` (KTD14) pins the protocol structure.
  The Go behavior's TX is compared byte-for-byte (deterministic) or by
  field-set (randomized) against the fixture. GLBP is the exception: its
  fixture follows Wireshark
  [`packet-glbp.c`](https://github.com/wireshark/wireshark/blob/1dbb8baf9c5bb2e9501b15cce98cea6a3c0f41a3/epan/dissectors/packet-glbp.c#L289-L365)
  at commit `1dbb8baf9c5bb2e9501b15cce98cea6a3c0f41a3`, with a 12-byte header and
  one-byte TLV type and length. No device capture confirms it.
- **(b)** T2 vendor behavioral truth: the live lab injects from a Linux host and
  reads the Cisco target's own observable over SSH. The OSPF path asserts the
  spoofed neighbor's appearance and removal. The other behaviors remain pending.
- **(c)** T1 self-consistency: two complete JSONL streams from the same command
  and target segment have matching record-kind and finding-module sets. This
  proves repeatability of netpen's reported class, not wire shape or vendor
  behavior.

## Fixture provenance key

| Class | Meaning |
|-------|---------|
| (a)   | Python-fixture wire-shape truth (KTD14 pcap pin) |
| (b)   | t2 vendor behavioral truth (recorded for `ospf`) |
| (c)   | T1 self-consistency (no wire or vendor truth) |

## Columns

| Column | Meaning |
|--------|---------|
| Behavior | Catalog behavior name |
| Mode | Mode flag (base = mode-less) |
| Impact | Catalog impact: none, temporary, or permanent. The ceiling refuses an entry above it |
| Restore | Catalog restore kind, with the decay bound the probe announces when one is known |
| Affects | Whose state the check changes: none, device, clients, or fabric |
| Observable | The predicate the check reads off the wire and the detail strings its verdicts carry, quoted from the behavior. A cell that opens with a pending reason belongs to a behavior that is pending by design, which `TestMatrixPendingRowsAreTheDesignedOnes` lists |
| Fixture provenance | (a) Python-fixture / (c) ring-only / (a)+(c) both |
| t1 AE6 | dated source-(c) result = matching live T1 finding classes, N/A = outside the AE6 list |
| t2 | never = no t2 assertion exists, pending live run = assertion implemented but not executed, dated result = live vendor observable on that date |

## Matrix

The first five columns are the catalog's own values. `TestValidationMatrixCompleteness`
compares them row for row with the committed catalog and fails on any
difference, so a changed cost field is edited here in the same change.

| Behavior | Mode | Impact | Restore | Affects | Observable | Fixture provenance | t1 AE6 | t2 |
|----------|------|--------|---------|---------|------------|-------------------|--------|----|
| arpspoof | base | temporary | active | clients | the echo request to the victim's MAC from the gateway's IPv4 after the two poison replies, with the two neighbor repairs armed. Confirmed on an echo reply from the victim's MAC addressed to the attack leg's MAC: `the victim sent its gateway traffic to the attack leg`. Resisted on a reply to another MAC (`the victim sent its reply for <gw> to <mac>`) or an ARP request from the victim for the gateway (`the victim asked who-has <gw>`). Pending: `no reply from the victim in <window>` | (a) | N/A | never |
| arpsweep | base | none | none | none | confirmed with the requested hosts as the inventory, and replies are not read | (a) | N/A | never |
| camflood | base | temporary | decay | device | the unicast probe to the victim's MAC with payload `netpen-camflood` after 10,000 stamped frames, read on the watch leg. Confirmed when a switch that lost the victim's address forwards it there: `the switch flooded the unicast probe for <mac> to the watch leg`. Resisted: `the unicast probe did not reach the watch leg in <window>`. Pending without a watch leg. The count of 10,000 is unconfirmed, since no vendored source states a CAM size | (a) | N/A | never |
| daddos | base | temporary | decay | clients | a neighbor advertisement for the target within the window, the defense of a host that holds the address. Resisted: `a host holds <addr> and defended it`. Pending: `no defense in <window>: abandonment leaves no frame` | (a) | N/A | never |
| dhcpstarve | base | temporary | decay | device | the server's OFFERs (RFC 2131 section 3.1 step 2) to the five spoofed chaddr values, counted for the window. Confirmed: `the server leased to <n> of 5 spoofed clients`. Resisted: `no offer in <window>` | (a) | N/A | never |
| doubletag | base | none | none | none | the echo request inside the double-tagged frame on the watch leg: from the attack leg's MAC, 10.0.0.1 to 10.0.0.2, tagged with the inner VLAN or untagged. Confirmed: `the echo request crossed into VLAN <n>`. Resisted: `no echo request in VLAN <n> on the watch leg in <window>`. Pending without a watch leg | (a) | N/A | never |
| dtp | base | temporary | active | device | a pre-read of one DTP frame from another sender (bounded by half the window, since no vendored source states the DTP interval), the desirable frame, then the next DTP frame from another sender. Confirmed when its trunk status carries the `0x80` bit the owned layer's `0x81` holds: `the port reports trunk status trunk (0x81) after the probe`. Resisted on any other status, named in the detail (`access (0x02)`, `desirable (0x03)`, `auto (0x04)`, `unknown (0x..)`), or `no DTP reply in <window>`. Pending: `the port was trunking before the probe`. The bit reading is unverified against Cisco's format, which no vendored source gives | (c) | N/A | never |
| dtp | keep-trunk | permanent | none | device | the base entry's predicate, with no restore armed | (c) | N/A | never |
| eigrp | base | temporary | active | fabric | a hello in AS 100 from another sender after the hello with the Parameters TLV, then the target's unicast hello to the attack leg's IPv4 with a non-zero Ack number after the null update (RFC 7868 sections 5.2 and 5.3.3). Confirmed: `the target acknowledged the adjacency in AS 100`, and the route update follows. Resisted: `adjacency refused` on a hello carrying the Peer Termination TLV or K-values of 255, `no EIGRP hello for AS 100 in <window>`, or `the null update drew no acknowledgment in <window>` | (a) | 2026-09-27: source-(c), finding:eigrp | never |
| etherchannel | base | temporary | active | device | an LACPDU from another sender whose partner system is the attack leg's MAC with the Synchronization bit set, or a PAgP frame whose partner device id is the attack leg's MAC. Confirmed: `an LACPDU from the switch names the attack leg as its synchronized partner` or `a PAgP frame from the switch names the attack leg as its partner`. Resisted: `the switch names partner <mac>`, or `no LACP or PAgP from the switch in <window>`. The armed `port-channel-release` sends an LACPDU with no Synchronization, Collecting, or Distributing bit. The partner's reaction is unverified against IEEE 802.1AX, which is not vendored | (a) | 2026-09-27: source-(c), finding:lacp | never |
| ghost | base | none | none | none | confirmed on a ghost frame crossing to the watch leg within the window, resisted at the window's end without one, whatever else the watch leg carried | (a) | N/A | never |
| glbp | base | temporary | active | clients | a pre-read of one hello interval (3 s) for an Active hello for the group from another sender, then the priority-255 active hello every 3 s. Confirmed when that sender's hello leaves Active (`the active gateway for group <n> left the active state for state <s>`) or it falls silent for the 10 s hold time (`fell silent for the 10s hold time`). Resisted: `the active gateway for group <n> still sends hellos in state active`. Pending: `no active gateway for group <n> in <window>`, or a window under 6 s naming `--observe 15s`. The state transitions are unverified (no RFC, no vendored source) | (a) | 2026-09-27: source-(c), finding:glbp | never |
| gratarp | base | temporary | decay | clients | the echo request to the gateway's MAC from the virtual IP after the three announcements. Confirmed on the gateway's reply to the attack leg's MAC: `the gateway sent its traffic for <vip> to the attack leg`. Resisted on a reply to another MAC or `the gateway asked who-has <vip>`. Pending: `no reply from the gateway in <window>` | (a) | N/A | never |
| hsrp | base | temporary | active | clients | a pre-read of one Hellotime (3 s, RFC 2281 section 5.1) for an Active hello for the group from another sender, then the Coup and a hello every 3 s with the Resign armed. Confirmed on a Resign from that sender (`sent a Resign after the coup`), a hello from it in another state (`left the active state for state <s>`), or its silence for the 10 s hold time (section 5.7, event j in Active). Resisted: `the active router for group <n> still sends hellos in state active`. Pending: `no active router for group <n> in <window>`, or a window under 6 s naming `--observe 15s` | (a) | N/A | never |
| icmpredirect | base | temporary | decay | clients | a frame from the victim's MAC to the attack leg's MAC whose IPv4 destination is 8.8.8.8, the redirected address. Confirmed: `the victim routes 8.8.8.8 through the attack leg`. Pending: `the victim sends to 8.8.8.8 on its own schedule: nothing in <window>` | (a) | N/A | never |
| lldpspoof | base | temporary | decay | device | pending: `the neighbor table is readable only over management`. The behavior reads no frame | (a) | 2026-09-27: source-(c), finding:lldp | never |
| llmnr | base | temporary | decay 30s | clients | an LLMNR query (UDP 5355 to 224.0.0.252, QR clear) from the victim's MAC within the window, answered unicast to its source with its id and name (RFC 4795 sections 2.1.1 and 2.3). Confirmed: `answered <name> for <victim>; acceptance of the answer is not observed`. Pending: `no LLMNR query from the victim in <window>` | (a) | N/A | never |
| mld | base | temporary | decay | device | an IPv6 packet to ff0e::1234 from another sender after the five reports. Confirmed: `traffic to ff0e::1234 reached the port`. Pending: `no group traffic in <window>: a Report draws no reply (RFC 2710 section 4)` | (a) | 2026-09-27: source-(c), finding:mld | never |
| mvrp | base | temporary | decay | fabric | an MVRP frame from another sender carrying VLAN 10 or 20 with a JoinIn event. Confirmed: `the switch declares VLAN <n> with a JoinIn`. Resisted: `no MVRP JoinIn for VLAN 10 or 20 from the switch in <window>`. The registrar semantics are unverified (IEEE 802.1Q clause 10 is not vendored) and the owned decoder defines no plain In event | (c) | N/A | never |
| ndpspoof | base | temporary | decay | clients | the ICMPv6 echo request to the victim from the server's address after the unsolicited advertisement (Override set, Solicited clear) naming the server's address at the attack leg's MAC. Confirmed on a reply to the attack leg's MAC: `the victim sent its traffic for <server6> to the attack leg`. Resisted on a reply to another MAC, or `the victim solicited <server6> itself: a victim with no entry for the address discards the advertisement (RFC 4861 section 7.2.5)`. Pending: `no reply from the victim in <window>` | (a) | N/A | never |
| ospf | base | temporary | active | fabric | the target's hello after the first hello, then its hello listing the attack leg's router ID (RFC 2328 section 10.5, 2-WayReceived) while the hello listing the target is sent every 10 s. Confirmed: `<target> lists router ID <id> as a neighbor`, and the database description and router-LSA update follow. Resisted: `the target's hello does not list router ID <id>`, `no OSPF hello in <window>`, or `no OSPF hello from <target> after the probe in <window>`. Pending under a 10 s window naming `--observe 20s` | (a) | 2026-09-27: source-(c), finding:ospf | 2026-09-25: IOS-XE (172.16.0.42) accepted neighbor 10.0.0.99 and cleared it after teardown; finding:ospf |
| portsteal | base | temporary | decay | clients | the echo request to the gateway's MAC from the victim's IPv4 after the three announcements. Confirmed on the gateway's reply to the attack leg's MAC: `the gateway sent its traffic for <victim> to the attack leg`. Resisted on a reply to another MAC or `the gateway asked who-has <victim>`. Pending: `no reply from the gateway in <window>` | (a) | N/A | never |
| portsteal | relay | temporary | active | clients | the base entry's predicate, with `net.ipv4.ip_forward` set to 1 through the sysctl seam before the first frame and the value read before written back by the armed `ip-forward-restore` | (a) | N/A | never |
| raflood | base | temporary | decay | clients | a neighbor solicitation whose target is one of the five stamped link-local sources. Confirmed: `a host probed the flood router <addr>`. Pending: `no host probed a flood router in <window>: a host probes a router only when it sends traffic` (RFC 4861 section 7.3.3) | (a) | 2026-09-27: source-(c), finding:ra | never |
| raguard | base | none | none | none | the probe is a Router Advertisement with router lifetime, hop limit, reachable time, and retransmit timer all zero, no source link-layer address or MTU option, and one prefix option with the L and A flags clear and zero lifetimes, so a host installs no router, prefix, address, or link parameter from it. A host already holding a neighbor cache entry for the probe's link-local source marks that entry a router. Confirmed on a Router Advertisement from the attack leg's MAC carrying the target prefix crossing to the watch leg within the window, resisted at the window's end without one, pending without a watch leg. Whether a vendor's RA-Guard or host stack treats the zero-lifetime probe as RFC 4861, 4862, and 6105 describe is unverified until the lab tier runs it | (a) | N/A | never |
| roguedhcp | base | temporary | decay 30m0s | clients | the victim's DISCOVER within the window, answered with an OFFER carrying its xid, then the victim's REQUEST (RFC 2131 section 4.4.1). Confirmed when its server identifier is the attack leg's IPv4: `the victim requested <ip> from the attack leg`, and the ACK follows. Resisted: `the victim requested from server <id>`, or `the victim did not request <ip>`. Pending: `no DISCOVER from the victim in <window>` with nothing sent | (a) | N/A | never |
| roguedhcp6 | base | temporary | decay 10m0s | clients | the victim's SOLICIT within the window, answered with the ADVERTISE, then its REQUEST (RFC 8415 sections 16.4 and 18.2.2). Confirmed when its Server Identifier is the attack leg's DUID: `the client requested fd00::200 from the attack leg`. Resisted: `the client requested from server DUID <d>`, or `the client did not request the lease`. Pending: `no solicit in <time listened>` with nothing sent | (a) | N/A | never |
| roguera | base | temporary | decay 30m0s | clients | a neighbor solicitation from another sender with an unspecified source whose target lies within the prefix, the duplicate address detection a host runs on an address it formed from it (RFC 4862 sections 5.4 and 5.5.3). Confirmed: `a host formed <addr> in <prefix> and ran duplicate address detection for it`. Resisted: `no host formed an address in <prefix> within <window>` | (a) | N/A | never |
| scan | base | none | none | none | orchestration entry, which reports discovery findings and carries no verdict | (a) | N/A | never |
| stproot | base | temporary | decay 6s | fabric | a pre-read of one hello time for a BPDU from another sender, then the superior BPDU (root priority 0, max age 6, hello time 1, forward delay 4) every hello time. Confirmed on a BPDU from another sender whose root identifier is priority 0 and the attack leg's MAC: `a neighbor bridge names the attack leg as root`. Resisted: `the neighbor keeps root <id>`, or `no BPDU in <window>`. The timer unit is unverified (IEEE 802.1D is not vendored) | (a) | N/A | never |
| vlanenum | base | none | none | none | confirmed with the VLAN ids 1 to 4094 of the tagged frames seen during the window as the inventory, pending: `no tagged frames in <window>` otherwise. VID 0 and 4095 are skipped. The Linux leg re-inserts the 802.1Q header AF_PACKET strips only for a non-zero TCI, so a frame with VID 0, PCP 0, and DEI 0 arrives untagged and is not counted, while a priority-tagged frame arrives tagged with VID 0 | (a) | N/A | never |
| vlanhop | base | none | none | none | doubletag's predicate on the same frame: the echo request on the watch leg tagged with the inner VLAN or untagged. Confirmed: `the echo request crossed into VLAN <n>`. Resisted: `no echo request in VLAN <n> on the watch leg in <window>`. Pending without a watch leg. The entry stays as a second name for the frame because the full command arms it from recon | (a) | N/A | never |
| voicevlan | base | temporary | active | clients | a pre-read of 30 s or half the window for an LLDP-MED network policy TLV from another sender naming the voice VLAN, then the LLDP-MED frame (MED Capabilities and Network Policy TLVs, OUI 00:12:bb) with the TTL-0 shutdown armed. Confirmed: `the switch advertised the MED policy for VLAN <n> after the probe`. Resisted: `no MED policy for VLAN <n> in <window>`. Pending: `the switch advertised the policy before the probe`. Whether a switch sends LLDP-MED TLVs only after hearing a MED endpoint is unverified (ANSI/TIA-1057 is not vendored) | (a) | N/A | never |
| voicevlan | persist | temporary | decay 2m0s | clients | the base entry's predicate, with nothing armed and the 120 s TTL of the frame as the decay bound | (a) | N/A | never |
| vrrp | base | temporary | decay | clients | a pre-read of one Advertisement_Interval (1 s, RFC 3768 section 6.1) for an advertisement for the VRID from another sender with priority below 120, then the advertisement (with the section 5.3.8 checksum) every second. Confirmed when that sender falls silent for 3 s + 1 s after the first probe (section 6.4.3): `the master for VRID <n> fell silent for <d> after the advertisement`. Resisted: `the master for VRID <n> kept advertising`. Pending: `no master for VRID <n> in <window>`, or a window under 5 s naming `--observe 6s` | (a) | N/A | never |
| vtp | base | temporary | decay | fabric | a pre-read of one summary advertisement for the domain from another sender (bounded by half the window, since no vendored source states the summary interval), the summary with that revision plus one, then the next summary. Confirmed on a revision at or above the sent one: `the domain <d> advertises revision <r> after the sent <s>`. Resisted: `the domain <d> still advertises revision <r>`. Pending: `no VTP summary for domain <d> in <window>` with nothing sent, or `no summary in <window>` | (c) | N/A | never |
| vtp | set | permanent | none | fabric | the base entry's pre-read and predicate, with the subset advertisement carrying that revision plus one | (c) | N/A | never |
| vtp | wipe | permanent | none | fabric | the base entry's pre-read, the summary with revision 0, then the next summary. Confirmed on revision 0: `the domain <d> advertises revision 0 after the wipe`. Resisted: `the domain <d> still advertises revision <r>`. Pending as the base entry | (c) | N/A | never |
| wpad | base | temporary | decay 30s | clients | an NBT-NS or LLMNR query for `wpad` from another sender within the window, answered unicast to its source with its id while the PAC listener holds the attack leg's IPv4 on port 80, then the listener's request log. Confirmed on a request for `/wpad.dat`: `a client fetched the PAC`. Resisted: `no PAC fetch in <window>`. Pending: `no wpad query in <window>` with nothing sent | (a) | 2026-09-27: source-(c), finding:wpad | never |

## Live OSPF source-(b) recording path

The `netpen_t2` lab tier runs netpen on the Linux injector, reads
`show ip ospf neighbor` from IOS-XE, and emits a matrix-ready evidence line when
router ID `10.0.0.99` appears and later clears. That line is recorded only after
the live run passes; the 2026-09-25 run below supplied it, so both OSPF cells now
carry the dated result rather than a pending-plan citation.

Recorded from a live run on 2026-09-25 against IOS-XE `172.16.0.42`: netpen
injected the OSPF hello/DB-desc/LSA from the Linux injector, the device accepted
a spoofed neighbor with router ID `10.0.0.99`, and the neighbor cleared within the
dead interval after netpen's goodbye/flush teardown; `TestT2OSPFLiveLab` passed.
Two fixes landed to reach this: the flowssh reader fix (opt-in echo anchoring, so
`Session.Run` returns a command's own output against a prompt-reprinting shell)
and the netpen router-id fix (`attacks/routing/helpers.go` `attackerRouterID` was
`0x0a000099` = `10.0.0.153`, corrected to `0x0a000063` = `10.0.0.99`, with the
harvest generator taught to compute OSPF/LSA checksums).

The target needs an OSPF interface in `10.0.0.0/24`, area 0, with broadcast
network type, hello 10, dead 40, and no authentication. The injector interface
must share that data segment, with wiring inspectable through the EVE-NG manager
at `10.20.0.101`. The injector runs the deployed netpen binary through sudo, so
the live environment must include `NETPEN_LAB_INJECTOR_SUDO_PASSWORD` along with
the SSH credentials and host-key pins documented in [the T2 README](t2/README.md).

## AE6 superset attacks

AE6 runs each listed command twice inside FRR r1 on `eth0`. A pass requires
successful command exits and matching record-kind/finding-module sets from
complete JSONL streams. Command errors, missing binaries, malformed output, and
empty streams fail. A skipped tier supplies no live evidence. Results must be
recorded manually; the tests do not update this file.

| Superset | Fixture provenance | t1 target | t1 result | t2 target | Caveat |
|----------|-------------------|-----------|-----------|-----------|--------|
| ospf | (a) | FRR r1 lab segment | 2026-09-27: source-(c), finding:ospf | IOS-XE live lab | T1 checks reported-class repeatability; the separate T2 result supplies vendor truth |
| eigrp | (a) | FRR r1 lab segment | 2026-09-27: source-(c), finding:eigrp | live lab (pending) | No EIGRP responder is configured |
| wpad | (a) | lab segment | 2026-09-27: source-(c), finding:wpad | live lab (pending) | LLMNR/NBNS-based; no responder observable is asserted |
| etherchannel | (a) | FRR r1 lab segment | 2026-09-27: source-(c), finding:lacp | live lab (pending) | No LACP/PAgP responder or wire assertion |
| mld | (a) | lab segment | 2026-09-27: source-(c), finding:mld | live lab (pending) | IPv6 multicast; no receiver observable is asserted |
| raflood | (a) | lab segment | 2026-09-27: source-(c), finding:ra | live lab (pending) | IPv6 RA; no receiver observable is asserted |
| lldpspoof | (a) | FRR r1 lab segment | 2026-09-27: source-(c), finding:lldp | live lab (pending) | No LLDP decoder assertion |
| glbp | (a) | FRR r1 lab segment | 2026-09-27: source-(c), finding:glbp | live lab (pending) | `packet-glbp.c` byte layout pinned, no GLBP responder assertion |

### Source-(c) caveat

The live T1 run executes netpen inside FRR r1 on the shared `10.99.0.0/24`
segment. The assertion compares netpen's output classes only. It does not
inspect captured frames or any peer's protocol state. The separate ring fixture
is not started by TestMain and still supplies no assertion. Vendor responses
remain source-(b) evidence from T2.

### Live T1 prerequisites and limits

The harness builds FRR r1 from the repository root with a static Linux netpen
binary installed at `/usr/local/bin/netpen`. AE6 passes `--duration 2s` and
`--timeout 20s`; per-attack duration is currently parsed but not consumed by
the CLI, so the timeout is the enforced bound. A timeout is a test failure, not
a reproduced finding.

AE5 checks successful full-command completion with `--duration 3s`. A separate
test checks static linking of the local release artifact. Neither test verifies
that the container runs that same artifact or has no network egress.

## Structural guard test

A guard test (`TestValidationMatrixCompleteness` in
`src/edge/netpen/test/integration/matrix_test.go`) walks the catalog and asserts every
(behavior, mode) pair appears in this file. This is the single-source
guard (KTD8 spirit): the catalog is the source of truth, and the matrix
cannot drift from it.
