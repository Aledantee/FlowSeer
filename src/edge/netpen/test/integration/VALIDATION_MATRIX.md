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
| Observable | What decides the verdict today. A pending reason names the observable the check still lacks |
| Fixture provenance | (a) Python-fixture / (c) ring-only / (a)+(c) both |
| t1 AE6 | dated source-(c) result = matching live T1 finding classes, N/A = outside the AE6 list |
| t2 | never = no t2 assertion exists, pending live run = assertion implemented but not executed, dated result = live vendor observable on that date |

## Matrix

The first five columns are the catalog's own values. `TestValidationMatrixCompleteness`
compares them row for row with the committed catalog and fails on any
difference, so a changed cost field is edited here in the same change.

| Behavior | Mode | Impact | Restore | Affects | Observable | Fixture provenance | t1 AE6 | t2 |
|----------|------|--------|---------|---------|------------|-------------------|--------|----|
| arpspoof | base | temporary | active | clients | pending: no observable: poison acceptance needs a probe through the attacker, and the ip-forward-restore step is a no-op | (a) | N/A | never |
| arpsweep | base | none | none | none | confirmed with the requested hosts as the inventory, and replies are not read | (a) | N/A | never |
| camflood | base | temporary | decay | device | pending: no observable: fail-open needs a unicast probe seen on the watch leg | (a) | N/A | never |
| daddos | base | temporary | decay | clients | pending: no observable: address denial needs a later probe of the victim | (a) | N/A | never |
| dhcpstarve | base | temporary | decay | device | pending: no observable: pool exhaustion needs an offer listener | (a) | N/A | never |
| doubletag | base | none | none | none | pending: no observable: a hop into the inner VLAN needs a watch leg there | (a) | N/A | never |
| dtp | base | temporary | active | device | pending: no observable: trunk formation needs a tagged probe across the port | (c) | N/A | never |
| dtp | keep-trunk | permanent | none | device | pending: no observable: trunk formation needs a tagged probe across the port | (c) | N/A | never |
| eigrp | base | temporary | active | fabric | resisted on a goodbye within 100 ms, otherwise pending: no observable: route acceptance needs an update listener | (a) | 2026-09-27: source-(c), finding:eigrp | never |
| etherchannel | base | temporary | active | device | pending: no observable: channel formation needs a partner-state listener, and the port-channel-release step is a no-op | (a) | 2026-09-27: source-(c), finding:lacp | never |
| ghost | base | none | none | none | confirmed on a ghost frame crossing to the watch leg within the window, resisted on an empty window | (a) | N/A | never |
| glbp | base | temporary | active | clients | pending: no observable: AVG takeover needs a hello listener | (a) | 2026-09-27: source-(c), finding:glbp | never |
| gratarp | base | temporary | decay | clients | pending: no observable: cache acceptance needs a probe of the announced address | (a) | N/A | never |
| hsrp | base | temporary | active | clients | pending: no observable: coup acceptance needs a hello listener | (a) | N/A | never |
| icmpredirect | base | temporary | decay | clients | pending: no observable: route cache acceptance needs a probe of the victim | (a) | N/A | never |
| lldpspoof | base | temporary | decay | device | pending: no observable: neighbor table acceptance needs a read of the device | (a) | 2026-09-27: source-(c), finding:lldp | never |
| llmnr | base | temporary | decay 30s | clients | pending: no observable: answer acceptance needs a listener for the victim's follow-up connection | (a) | N/A | never |
| mld | base | temporary | decay | device | pending: no observable: membership acceptance needs a read of the snooping table | (a) | 2026-09-27: source-(c), finding:mld | never |
| mvrp | base | temporary | decay | fabric | pending: no observable: registration needs a read of the device's VLAN table | (c) | N/A | never |
| ndpspoof | base | temporary | decay | clients | pending: no observable: cache acceptance needs a probe of the victim | (a) | N/A | never |
| ospf | base | temporary | active | fabric | pending: no observable: neighbor formation needs a hello listener | (a) | 2026-09-27: source-(c), finding:ospf | 2026-09-25: IOS-XE (172.16.0.42) accepted neighbor 10.0.0.99 and cleared it after teardown; finding:ospf |
| portsteal | base | temporary | decay | device | pending: no observable: port move needs a probe of the victim | (a) | N/A | never |
| portsteal | relay | temporary | active | device | pending: no observable: port move needs a probe of the victim, and the ip-forward-restore step is a no-op | (a) | N/A | never |
| raflood | base | temporary | decay | clients | pending: no observable: router acceptance needs a probe of a host's router list | (a) | 2026-09-27: source-(c), finding:ra | never |
| raguard | base | none | none | none | confirmed on the forged RA crossing to the watch leg within the window, resisted on an empty window, pending without a watch leg | (a) | N/A | never |
| roguedhcp | base | temporary | decay 30m0s | clients | pending: no observable: lease acceptance needs a REQUEST listener | (a) | N/A | never |
| roguedhcp6 | base | temporary | decay 5m0s | clients | pending: no observable: lease acceptance needs a REQUEST listener | (a) | N/A | never |
| roguera | base | temporary | decay 30m0s | clients | pending: no observable: router acceptance needs a probe of a host's router list | (a) | N/A | never |
| scan | base | none | none | none | orchestration entry, which reports discovery findings and carries no verdict | (a) | N/A | never |
| stproot | base | temporary | decay 6s | fabric | pending: no observable: root acceptance needs a BPDU listener | (a) | N/A | never |
| vlanenum | base | none | none | none | confirmed with the observed VLAN ids as the inventory | (a) | N/A | never |
| vlanhop | base | temporary | active | device | pending: no observable: a hop into the inner VLAN needs a watch leg there, and the vlan-interface-restore step is a no-op | (a) | N/A | never |
| vlanhop | persist | permanent | none | device | pending: no observable: a hop into the inner VLAN needs a watch leg there | (a) | N/A | never |
| voicevlan | base | temporary | active | clients | pending: no observable: a phone's move needs a listener on the voice VLAN, and the lldp-voice-vlan-restore step is a no-op | (a) | N/A | never |
| voicevlan | persist | permanent | none | clients | pending: no observable: a phone's move needs a listener on the voice VLAN | (a) | N/A | never |
| vrrp | base | temporary | decay | clients | pending: no observable: master preemption needs an advertisement listener | (a) | N/A | never |
| vtp | base | temporary | decay | fabric | pending: no observable: revision acceptance needs a read of the device's VTP status | (c) | N/A | never |
| vtp | set | permanent | none | fabric | pending: no observable: revision acceptance needs a read of the device's VTP status | (c) | N/A | never |
| vtp | wipe | permanent | none | fabric | pending: no observable: revision acceptance needs a read of the device's VTP status | (c) | N/A | never |
| wpad | base | temporary | decay 30s | clients | pending: no observable: PAC fetch needs the listener's request log | (a) | 2026-09-27: source-(c), finding:wpad | never |

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
