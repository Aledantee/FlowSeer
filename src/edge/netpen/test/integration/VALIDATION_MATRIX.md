# netpen Validation Matrix

This matrix inventories every (behavior, mode) pair's intended ground-truth
source, per KTD15. No live T1 run is recorded here; the `ospf` row carries one
live T2 result below. Fixture provenance labels do not establish that this
integration suite has executed a wire or vendor assertion.

## Ground-truth sources

- **(a)** Python-fixture wire-shape truth: the characterization fixture
  harvested from `l2l3-audit` (KTD14) pins the protocol structure.
  The Go behavior's TX is compared byte-for-byte (deterministic) or by
  field-set (randomized) against the fixture.
- **(b)** T2 vendor behavioral truth: an operator-supplied Cisco NOS must
  produce expected findings. T2 logs intended runs for the other behaviors
  without executing or asserting them; the `ospf` row is the first with this
  evidence.
- **(c)** Planned ring self-consistency: captured frames would need to decode
  correctly. The ring compose fixture exists, but this suite has no capture or
  decoder test. These rows have no wire or vendor evidence from this suite.

## Fixture provenance key

| Class | Meaning |
|-------|---------|
| (a)   | Python-fixture wire-shape truth (KTD14 pcap pin) |
| (b)   | t2 vendor behavioral truth (recorded for `ospf`) |
| (c)   | Planned ring self-consistency (no executing integration assertion) |

## Columns

| Column | Meaning |
|--------|---------|
| Behavior | Catalog behavior name |
| Mode | Mode flag (base = mode-less) |
| Durability | Catalog durability class |
| Fixture provenance | (a) Python-fixture / (c) ring-only / (a)+(c) both |
| t1 AE6 status | not recorded = no live result retained; N/A = outside the AE6 list; t2 (b) = live result carried by the t2 run |
| t2 status | never = t2 not yet run; dated result = live vendor observable on that date |
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
| eigrp | base | temporary-restored | (a) | not recorded | never | goodbye/flush teardown |
| etherchannel | base | temporary-restored | (a) | not recorded | never | port-channel release |
| ghost | base | non-destructive | (a) | N/A | never | |
| glbp | base | temporary-restored | (a) | not recorded | never | resign teardown |
| gratarp | base | transient-decay | (a) | N/A | never | neighbor cache ages |
| hsrp | base | temporary-restored | (a) | N/A | never | resign teardown |
| icmpredirect | base | transient-decay | (a) | N/A | never | victim route cache decays |
| lldpspoof | base | transient-decay | (a) | not recorded | never | bounded bursts / holdtimes |
| llmnr | base | transient-decay | (a) | N/A | never | spoofed answers expire |
| mld | base | transient-decay | (a) | not recorded | never | bounded bursts / holdtimes |
| mvrp | base | transient-decay | (c) | N/A | never | MRP timers, minutes |
| ndpspoof | base | transient-decay | (a) | N/A | never | NUD / real master resumes |
| ospf | base | temporary-restored | (a) | t2 (b) | 2026-09-24 IOS-XE 17.3.2: neighbor 10.0.0.153 accepted, 10.0.0.99 elected BDR | goodbye/flush teardown |
| portsteal | base | transient-decay | (a) | N/A | never | CAM aging |
| portsteal | relay | temporary-restored | (a) | N/A | never | ip_forward restore armed |
| raflood | base | transient-decay | (a) | not recorded | never | bounded bursts / holdtimes |
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
| wpad | base | transient-decay | (a) | not recorded | never | client proxy config residue, bound = TTL |

## Live OSPF source-(b) result

The `ospf` row is this file's first source-(b) entry. It came from the
lab-backed `netpen_t2` path, which is why that row names the t2 run in its
`t1 AE6` cell. IOS-XE accepted the injection only after netpen computed the OSPF
packet checksum; zero-checksum packets are dropped per RFC 2328, so the `t1` FRR
tier and AE6 never surfaced the defect.

## AE6 superset attacks (R4)

AE6 runs each listed command twice inside FRR r1 on `eth0`. A pass requires
successful command exits and matching record-kind/finding-module sets from
complete JSONL streams. Command errors, missing binaries, malformed output, and
empty streams fail. A skipped tier supplies no live evidence. Results must be
recorded manually; the tests do not update this file.

| Superset | Fixture provenance | t1 target | t2 target | Caveat |
|----------|-------------------|-----------|-----------|--------|
| ospf | (a) | FRR r1 lab segment | vIOS (operator) | Harness waits for FRR adjacency; attack result is not asserted against FRR state |
| eigrp | (a) | FRR r1 lab segment | vIOS (operator) | No EIGRP responder is configured |
| wpad | (a) | lab segment | vIOS (operator) | LLMNR/NBNS-based; lab segment target |
| etherchannel | (a) | FRR r1 lab segment | vIOS (operator) | No LACP/PAgP responder or wire assertion |
| mld | (a) | lab segment | vIOS (operator) | IPv6 multicast; lab segment target |
| raflood | (a) | lab segment | vIOS (operator) | IPv6 RA; lab segment target |
| lldpspoof | (a) | FRR r1 lab segment | vIOS (operator) | No LLDP decoder assertion |
| glbp | (a) | FRR r1 lab segment | vIOS (operator) | No GLBP responder or wire assertion |

### Ring-only caveat

The ring fixture is not started by TestMain, and its containers have no
crafter/decoder commands configured. It supplies no live assertion today.
Running a future decoder test would establish self-consistency only; a vendor
response still needs separate evidence.

### Live T1 prerequisites and limits

The supplied FRR image does not include netpen. The operator must install a
compatible binary in `netpen-t1-frr-r1` before running AE5 or AE6. The harness
does not install it. AE6 passes `--duration 2s` and `--timeout 20s`; per-attack
duration is currently parsed but not consumed by the CLI, so the timeout is
the enforced bound. A timeout is a test failure, not a reproduced finding.

AE5 checks successful full-command completion with `--duration 3s`. A separate
test checks static linking of the local release artifact. Neither test verifies
that the container runs that same artifact or has no network egress.

## Structural guard test

A guard test (`TestValidationMatrixCompleteness` in
`src/edge/netpen/test/integration/matrix_test.go`) walks the catalog and asserts every
(behavior, mode) pair appears in this file. This is the single-source
guard (KTD8 spirit): the catalog is the source of truth, and the matrix
cannot drift from it.
