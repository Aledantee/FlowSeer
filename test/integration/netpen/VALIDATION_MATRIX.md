# netpen Validation Matrix

This matrix labels every (behavior, mode) pair's ground-truth source and
tier-1 validation status, per KTD15. It is the single source a reviewer
consults to know what validates each attack.

## Ground-truth sources

- **(a)** Python-fixture wire-shape truth: the characterization fixture
  harvested from `l2l3-audit` (KTD14) pins the protocol structure.
  The Go behavior's TX is compared byte-for-byte (deterministic) or by
  field-set (randomized) against the fixture.
- **(b)** t2 vendor behavioral truth: a real Cisco NOS (operator-supplied
  vIOS-class image) validates the attack produces the expected finding.
  **None yet** — t2 is opt-in and operator-run; rows default to (c) until
  t2 covers them.
- **(c)** Ring-only self-consistency: the netpen-vs-netpen ring validates
  wire shape (the crafter's frames decode correctly), but **not** vendor
  behavior. These rows carry the explicit caveat: **"wire-shape-validated,
  behavior-unvalidated"** until t2 covers them.

## Fixture provenance key

| Class | Meaning |
|-------|---------|
| (a)   | Python-fixture wire-shape truth (KTD14 pcap pin) |
| (b)   | t2 vendor behavioral truth (none yet) |
| (c)   | Ring-only self-consistency (wire-shape-validated, behavior-unvalidated) |

## Columns

| Column | Meaning |
|--------|---------|
| Behavior | Catalog behavior name |
| Mode | Mode flag (base = mode-less) |
| Durability | Catalog durability class |
| Fixture provenance | (a) Python-fixture / (c) ring-only / (a)+(c) both |
| t1 AE6 status | pass = reproduced; skip+docker-state = tier skipped (daemon absent); N/A = no t1 target |
| t2 status | never (t2 is opt-in, not yet run) |
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
| eigrp | base | temporary-restored | (a) | pass/skip | never | goodbye/flush teardown |
| etherchannel | base | temporary-restored | (a) | pass/skip | never | port-channel release |
| ghost | base | non-destructive | (a) | N/A | never | |
| glbp | base | temporary-restored | (a) | pass/skip | never | resign teardown |
| gratarp | base | transient-decay | (a) | N/A | never | neighbor cache ages |
| hsrp | base | temporary-restored | (a) | N/A | never | resign teardown |
| icmpredirect | base | transient-decay | (a) | N/A | never | victim route cache decays |
| lldpspoof | base | transient-decay | (a) | pass/skip | never | bounded bursts / holdtimes |
| llmnr | base | transient-decay | (a) | N/A | never | spoofed answers expire |
| mld | base | transient-decay | (a) | pass/skip | never | bounded bursts / holdtimes |
| mvrp | base | transient-decay | (c) | N/A | never | MRP timers, minutes |
| ndpspoof | base | transient-decay | (a) | N/A | never | NUD / real master resumes |
| ospf | base | temporary-restored | (a) | pass/skip | never | goodbye/flush teardown |
| portsteal | base | transient-decay | (a) | N/A | never | CAM aging |
| portsteal | relay | temporary-restored | (a) | N/A | never | ip_forward restore armed |
| raflood | base | transient-decay | (a) | pass/skip | never | bounded bursts / holdtimes |
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
| wpad | base | transient-decay | (a) | pass/skip | never | client proxy config residue, bound = TTL |

## AE6 superset attacks (R4)

The eight superset attacks earn the AE6 reproducibility shape in t1.
Each runs twice against the same target and must produce the same
findings class. "pass/skip" in the t1 AE6 column means: **pass** when
the Docker daemon is running (tier executes, attack reproduces);
**skip+docker-state** when the daemon is absent (tier skips cleanly,
no live evidence).

| Superset | Fixture provenance | t1 target | t2 target | Caveat |
|----------|-------------------|-----------|-----------|--------|
| ospf | (a) | FRR r1 (10.99.0.11) | vIOS (operator) | OSPF adjacency is FRR-impersonatable |
| eigrp | (a) | ring-only | vIOS (operator) | EIGRP is Cisco-proprietary; FRR has no EIGRP — ring validates wire shape only |
| wpad | (a) | lab segment | vIOS (operator) | LLMNR/NBNS-based; lab segment target |
| etherchannel | (a) | ring-only | vIOS (operator) | LACP/PAgP; ring validates wire shape only |
| mld | (a) | lab segment | vIOS (operator) | IPv6 multicast; lab segment target |
| raflood | (a) | lab segment | vIOS (operator) | IPv6 RA; lab segment target |
| lldpspoof | (a) | ring-only | vIOS (operator) | LLDP; ring validates wire shape only |
| glbp | (a) | ring-only | vIOS (operator) | GLBP; ring validates wire shape only |

### Ring-only caveat

Rows labeled (c) — DTP, VTP, MVRP — and ring-only supersets
(EIGRP, EtherChannel, LLDP-spoof, GLBP) carry the explicit caveat:
**"wire-shape-validated, behavior-unvalidated"** until t2 covers them.
The netpen-vs-netpen ring proves the crafter's frames decode correctly;
it does not prove a real switch accepts or is affected by them.

## Structural guard test

A guard test (`TestValidationMatrixCompleteness` in
`test/integration/netpen/matrix_test.go`) walks the catalog and asserts every
(behavior, mode) pair appears in this file. This is the single-source
guard (KTD8 spirit): the catalog is the source of truth, and the matrix
cannot drift from it.
