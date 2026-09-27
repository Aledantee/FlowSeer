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
  field-set (randomized) against the fixture.
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
| Durability | Catalog durability class |
| Fixture provenance | (a) Python-fixture / (c) ring-only / (a)+(c) both |
| t1 AE6 status | dated source-(c) result = matching live T1 finding classes; N/A = outside the AE6 list |
| t2 status | never = no t2 assertion exists; pending live run = assertion implemented but not executed; dated result = live vendor observable on that date |
| Teardown | teardown path from catalog (empty = none) |

## Matrix

| Behavior | Mode | Durability | Fixture provenance | t1 AE6 | t2 | Teardown |
|----------|------|------------|-------------------|--------|----|----------|
| arpspoof | base | temporary-restored | (a) | N/A | never | neighbor unicast repairs + ip_forward restore |
| arpsweep | base | non-destructive | (a) | N/A | never | |
| camflood | base | transient-decay | (a) | N/A | never | CAM table ages out |
| daddos | base | transient-decay | (a) | N/A | never | DAD window passes |
| dhcpstarve | base | transient-decay | (a) | N/A | never | lease pool recovers |
| doubletag | base | transient-decay | (a) | N/A | never | one-shot injected frames; nothing persists |
| dtp | base | temporary-restored | (c) | N/A | never | access-port restore armed by default (KTD12) |
| dtp | keep-trunk | permanent-destructive | (c) | N/A | never | opt-in per KTD12 |
| eigrp | base | temporary-restored | (a) | 2026-09-27: source-(c), finding:eigrp | never | goodbye/flush teardown |
| etherchannel | base | temporary-restored | (a) | 2026-09-27: source-(c), finding:lacp | never | port-channel release |
| ghost | base | non-destructive | (a) | N/A | never | |
| glbp | base | temporary-restored | (a) | 2026-09-27: source-(c), finding:glbp | never | resign teardown |
| gratarp | base | transient-decay | (a) | N/A | never | neighbor cache ages |
| hsrp | base | temporary-restored | (a) | N/A | never | resign teardown |
| icmpredirect | base | transient-decay | (a) | N/A | never | victim route cache decays |
| lldpspoof | base | transient-decay | (a) | 2026-09-27: source-(c), finding:lldp | never | bounded bursts / holdtimes |
| llmnr | base | transient-decay | (a) | N/A | never | spoofed answers expire |
| mld | base | transient-decay | (a) | 2026-09-27: source-(c), finding:mld | never | bounded bursts / holdtimes |
| mvrp | base | transient-decay | (c) | N/A | never | MRP timers, minutes |
| ndpspoof | base | transient-decay | (a) | N/A | never | NUD / real master resumes |
| ospf | base | temporary-restored | (a) | 2026-09-27: source-(c), finding:ospf | 2026-09-25: IOS-XE (172.16.0.42) accepted neighbor 10.0.0.99 and cleared it after teardown; finding:ospf | goodbye/flush teardown |
| portsteal | base | transient-decay | (a) | N/A | never | CAM aging |
| portsteal | relay | temporary-restored | (a) | N/A | never | ip_forward restore armed |
| raflood | base | transient-decay | (a) | 2026-09-27: source-(c), finding:ra | never | bounded bursts / holdtimes |
| raguard | base | non-destructive | (a) | N/A | never | |
| roguedhcp | base | transient-decay | (a) | N/A | never | client leases ~1800s |
| roguedhcp6 | base | transient-decay | (a) | N/A | never | ~300s lifetime announced |
| roguera | base | transient-decay | (a) | N/A | never | ~1800s lifetime announced |
| scan | base | non-destructive | (a) | N/A | never | |
| stproot | base | transient-decay | (a) | N/A | never | engineered max_age~6s |
| vlanenum | base | non-destructive | (a) | N/A | never | |
| vlanhop | base | temporary-restored | (a) | N/A | never | active restore armed |
| vlanhop | persist | permanent-destructive | (a) | N/A | never | host-side persistence, flag is the opt-in |
| voicevlan | base | temporary-restored | (a) | N/A | never | active restore armed |
| voicevlan | persist | permanent-destructive | (a) | N/A | never | host-side persistence, flag is the opt-in |
| vrrp | base | transient-decay | (a) | N/A | never | NUD / real master resumes |
| vtp | base | transient-decay | (c) | N/A | never | revision-bump side effect recorded |
| vtp | set | permanent-destructive | (c) | N/A | never | opt-in per R15 |
| vtp | wipe | permanent-destructive | (c) | N/A | never | opt-in per R15 |
| wpad | base | transient-decay | (a) | 2026-09-27: source-(c), finding:wpad | never | client proxy config residue, bound = TTL |

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
| glbp | (a) | FRR r1 lab segment | 2026-09-27: source-(c), finding:glbp | live lab (pending) | No GLBP responder or wire assertion |

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
