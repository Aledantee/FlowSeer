# netpen Validation Matrix

This matrix inventories every (behavior, mode) pair's intended ground-truth
source, per KTD15. No live T1 run is recorded here. The `ospf` live-lab path is
implemented, but its T1 AE6 and T2 cells remain pending until the deferred lab
run records the target's observable. Fixture provenance labels do not establish
that this integration suite has executed a wire or vendor assertion.

## Ground-truth sources

- **(a)** Python-fixture wire-shape truth: the characterization fixture
  harvested from `l2l3-audit` (KTD14) pins the protocol structure.
  The Go behavior's TX is compared byte-for-byte (deterministic) or by
  field-set (randomized) against the fixture.
- **(b)** T2 vendor behavioral truth: the live lab injects from a Linux host and
  reads the Cisco target's own observable over SSH. The OSPF path asserts the
  spoofed neighbor's appearance and removal. The other behaviors remain pending.
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
| t1 AE6 status | not recorded = no live result retained; N/A = outside the AE6 list; pending live run = the assertion exists but has not run in the lab; t2 (b) = live result carried by the t2 run |
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
| ospf | base | temporary-restored | (a) | pending live run ([plan](../../../../../docs/plans/2026-09-23-2228-feat-netpen-lab-vendor-validation-plan.md)) | pending live run ([plan](../../../../../docs/plans/2026-09-23-2228-feat-netpen-lab-vendor-validation-plan.md)) | goodbye/flush teardown |
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

## Live OSPF source-(b) recording path

The `netpen_t2` lab tier runs netpen on the Linux injector, reads
`show ip ospf neighbor` from IOS-XE, and emits a matrix-ready evidence line when
router ID `10.0.0.99` appears and later clears. Record that line here only after
the live run passes. Until then, both OSPF cells cite the implementation plan as
pending.

A 2026-09-25 live run against the IOS-XE lab target read the observable correctly
(after the flowssh reader fix that lets `Session.Run` return a command's own
output against a shell that reprints its prompt) and confirmed netpen's injected
OSPF adjacency forms on the device. It still does not pass the assertion: netpen
advertises router ID `10.0.0.153`, not the `10.0.0.99` this tier expects —
`attacks/routing/helpers.go` sets `attackerRouterID = 0x0a000099`, whose value is
`10.0.0.153` while its comment and the source address say `10.0.0.99`. Correcting
it also touches the harvest generator's OSPF checksums and the byte-offset teardown
tests, so it is a separate netpen change; the cells stay pending until it lands.

The target needs an OSPF interface in `10.0.0.0/24`, area 0, with broadcast
network type, hello 10, dead 40, and no authentication. The injector interface
must share that data segment, with wiring inspectable through the EVE-NG manager
at `10.20.0.101`. The injector runs the deployed netpen binary through sudo, so
the live environment must include `NETPEN_LAB_INJECTOR_SUDO_PASSWORD` along with
the SSH credentials and host-key pins documented in [the T2 README](t2/README.md).

## AE6 superset attacks (R4)

AE6 runs each listed command twice inside FRR r1 on `eth0`. A pass requires
successful command exits and matching record-kind/finding-module sets from
complete JSONL streams. Command errors, missing binaries, malformed output, and
empty streams fail. A skipped tier supplies no live evidence. Results must be
recorded manually; the tests do not update this file.

| Superset | Fixture provenance | t1 target | t2 target | Caveat |
|----------|-------------------|-----------|-----------|--------|
| ospf | (a) | FRR r1 lab segment | IOS-XE live lab | Live assertion implemented; run pending |
| eigrp | (a) | FRR r1 lab segment | live lab (pending) | No EIGRP responder is configured |
| wpad | (a) | lab segment | live lab (pending) | LLMNR/NBNS-based; lab segment target |
| etherchannel | (a) | FRR r1 lab segment | live lab (pending) | No LACP/PAgP responder or wire assertion |
| mld | (a) | lab segment | live lab (pending) | IPv6 multicast; lab segment target |
| raflood | (a) | lab segment | live lab (pending) | IPv6 RA; lab segment target |
| lldpspoof | (a) | FRR r1 lab segment | live lab (pending) | No LLDP decoder assertion |
| glbp | (a) | FRR r1 lab segment | live lab (pending) | No GLBP responder or wire assertion |

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
