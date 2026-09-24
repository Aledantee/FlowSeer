---
title: Fix netpen OSPF Zero Checksum (rejected by real IOS-XE) - Plan
type: fix
date: 2026-09-24
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept
execution: code
compound: docs/solutions/conventions/a-permissive-synthetic-tier-passes-attacks-a-real-vendor-rejects.md
amends: docs/plans/2026-08-23-1042-feat-netpen-port-plan.md
---

# Fix netpen OSPF Zero Checksum (rejected by real IOS-XE) - Plan

> Implemented. 4 units, 2026-09-24T18:56:44Z.

> Discovered by the first live vendor-truth run of the lab-backed harness
> ([the lab-validation plan](2026-09-23-2228-feat-netpen-lab-vendor-validation-plan.md),
> [direction record](../architecture/2026-09-23-netpen-lab-vendor-validation-direction.md)).
> The `ospf.go` change below is written and was proven on hardware, but is
> reverted in the tree pending clean formalization (fixtures + pin tests).

## Goal

netpen's `ospf` attack forms an OSPF adjacency on real Cisco IOS-XE. The means
is computing a valid OSPFv2 packet checksum on every crafted OSPF packet
instead of leaving it zero. This plan is wrong only if a stack rejects the
packets for a further reason after the checksum is valid — but the live run
already disproved that (see Evidence), so the remaining risk is limited to the
LSA-body Fletcher checksum for the LSA-update phase (U3).

## Evidence (live, 2026-09-24)

Against `LABRT42` (Cisco IOS-XE 17.3.2) over the lab injection segment
(VLAN 999, `10.0.0.0/24`), injecting from the Kali host on `eth1.999`:

- **Before the fix:** netpen sent 55 hellos (src `10.0.0.99`, router-id
  `10.0.0.153`); the target sent hellos back but **never listed the neighbor**
  — no adjacency. Hexdump of the OSPF header showed the checksum field
  (`0x20`–`0x21`) as `0x0000`. IOS-XE validates the OSPF checksum per RFC 2328
  and silently drops packets whose checksum is wrong.
- **After the fix:** the OSPF checksum on the wire was non-zero (e.g. `0xf205`),
  and the target's own hellos grew to include a neighbor list:
  `Designated Router 10.0.0.42, Backup Designated Router 10.0.0.99 /
  Neighbor List: 10.0.0.153`. The spoofed router was accepted and the attacker
  (`10.0.0.99`, priority 1) was elected Backup Designated Router.

The containerised t1 tier (FRR) and the reproducibility-only AE6 test never
caught this: FRR did not reject the zero checksum in that path, and AE6 only
checks that netpen emits a well-formed finding, not that a target accepts the
attack. This is the value the direction record claims for live vendor truth.

## Decisions

- Compute a valid OSPFv2 checksum on every crafted OSPF packet. Why: the
  Evidence shows real IOS-XE drops zero-checksum OSPF packets; a security tool
  whose frames a production router discards is not exercising the target.
- Correct the packet even though it diverges from the KTD14 characterization
  fixtures. Why: those fixtures were harvested from the Python `l2l3-audit`,
  which had the same zero-checksum defect
  (`docs/plans/2026-08-23-1042-feat-netpen-port-plan.md:272`, KTD14). The
  characterization pinned a bug; vendor correctness wins over fidelity to a
  buggy reference, which is the methodology the lab-validation direction record
  establishes. The regenerated fixtures are the new reference.
- The checksum excludes the 64-bit authentication field (`[16:24]`) per
  RFC 2328 section D.4. Why: correctness for any future non-zero AuType; with
  AuType 0 today the excluded bytes are zero, so the result is unchanged.
- The implement stage landed all four units and passed the verifier but left
  `status: planned`; the user ruled (2026-09-24, via drive) that the drive
  writes `status: implemented` and the `> Implemented.` marker directly rather
  than re-running implement, since the code work was complete and verified.

## Requirements

1. Every crafted OSPF packet carries a valid OSPFv2 checksum. Acceptance: for
   each of hello, db-desc, lsa-update, flush, goodbye, recomputing the RFC 2328
   checksum over the emitted packet (checksum field zeroed, auth field
   excluded) equals the value in the packet's checksum field, and that field is
   non-zero.
2. The OSPF fixture pins pass against regenerated fixtures. Acceptance:
   `TestOSPF_FixturePins` and `TestOSPF_TeardownOrder` are green with the
   regenerated `ospf.pcap` / `ospf_restore.pcap`, and the regenerated frames'
   checksums are valid per R1 (not merely equal to whatever the code emits).
3. The `ospf` VALIDATION_MATRIX cells record the live source-(b) evidence.
   Acceptance: the `ospf` row's `t2` cell states the IOS-XE 17.3.2 result
   (neighbor `10.0.0.153` accepted, `10.0.0.99` elected BDR) with the date, and
   its `t1 AE6` cell is no longer `not recorded`.

## Out of scope

- The LSA-body Fletcher checksum beyond U3 (other LSA types), and any attack
  other than `ospf`.
- The lab-backed harness code (U4/U5 of the lab-validation plan); this fix is
  its prerequisite for a positive `ospf` result and is referenced there.

## Units

### U1. Compute the OSPF header checksum
Files: `src/edge/netpen/attacks/routing/ospf.go`,
`src/edge/netpen/attacks/routing/routing_test.go`
After: none
Change: Add an `ospfChecksum(p []byte) uint16` helper (RFC 2328 D.4: 16-bit
ones-complement Internet checksum over the packet, checksum field `[12:14]`
zero, auth field `[16:24]` excluded) as its own top-level function — placed
**after** `craftOSPFHello` and its doc comment, not between the comment and the
func. In `craftOSPFHello`, `craftDBDesc`, `craftLSAUpdate`, and `craftLSAFlush`,
set `binary.BigEndian.PutUint16(payload[12:14], ospfChecksum(payload))`
immediately before `return craft.Default(...)`. The exact edit was proven on
hardware; its diff is in this session's history and reproduced in the Appendix.
Tests: a new `TestOSPF_ChecksumValid` in `routing_test.go` that crafts each of
the five packets and asserts R1 (recomputed checksum matches the field and is
non-zero).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/attacks/routing/ospf.go src/edge/netpen/attacks/routing/routing_test.go`

### U2. Regenerate the OSPF fixtures
Files: `src/edge/netpen/attacks/routing/testdata/ospf.pcap`,
`src/edge/netpen/attacks/routing/testdata/ospf_restore.pcap`
After: U1
Change: Regenerate the two pcap fixtures so their frames carry the corrected
(valid) OSPF checksums, matching U1's output byte-for-byte. Record in the test
or a testdata README that these diverge intentionally from the original Python
characterization capture (which had the zero-checksum defect), per Decisions.
`TestOSPF_FixturePins` and `TestOSPF_TeardownOrder` then pass unchanged (they
compare against these fixtures); confirm the regenerated frames satisfy R1 so
the pin is not merely circular.
Tests: `TestOSPF_FixturePins`, `TestOSPF_TeardownOrder` green.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/attacks/routing/testdata/ospf.pcap src/edge/netpen/attacks/routing/testdata/ospf_restore.pcap`

### U3. Valid LSA-body Fletcher checksum for the LSA-update
Files: `src/edge/netpen/attacks/routing/ospf.go`,
`src/edge/netpen/attacks/routing/routing_test.go`
After: U1
Change: In `craftLSAUpdate` (and the flushed LSA in `craftLSAFlush`), compute
the RFC 2328 section 12.1.7 LSA checksum (Fletcher over the LSA from the
options field onward, LSA checksum field zero) instead of leaving the LSA
checksum at `[16:18]` zero, so IOS-XE accepts the injected LSA in the
post-adjacency LSU rather than discarding it. Regenerate any fixture bytes this
changes (folds into U2 if run together).
Tests: a `routing_test.go` case asserting the injected LSA's Fletcher checksum
is valid.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/attacks/routing/ospf.go src/edge/netpen/attacks/routing/routing_test.go`

### U4. Record the vendor evidence in the matrix
Files: `src/edge/netpen/test/integration/VALIDATION_MATRIX.md`
After: U1
Change: Update the `ospf` row's `t2` and `t1 AE6` cells with the live IOS-XE
17.3.2 result from Evidence (date, neighbor accepted, BDR election), and add a
short note that the source-(b) result was obtained through the lab-backed
`netpen_t2` path and required the U1 checksum fix.
Tests: `matrix_test.go` structure guard stays green.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen/test/integration/VALIDATION_MATRIX.md`

Waves: U1 | U2 U3 U4

## Verification

- `go -C src/edge/netpen test ./attacks/routing/...` green for the OSPF cases
  (`TestOSPF_FixturePins`, `TestOSPF_TeardownOrder`, `TestOSPF_ChecksumValid`).
  Note: TX-dependent tests in that package (e.g. WPAD) fail on a macOS dev host
  because netpen's link is Linux-AF_PACKET-only; run the package on Linux or
  rely on the diff-aware verifier's environment.
- `verify-change` green for every changed path.
- Optional live re-confirmation against `LABRT42` if the lab is still up:
  redeploy the rebuilt binary to the Kali injector (`setcap
  cap_net_raw,cap_net_admin+eip`), inject `netpen ospf -i eth1.999`, and confirm
  the target lists neighbor `10.0.0.153` (as in Evidence).

## Definition of done

- Verifier green for every changed path; OSPF routing tests green.
- Fixtures regenerated with valid checksums; the divergence from the Python
  characterization is noted in testdata.
- VALIDATION_MATRIX `ospf` cells carry the live evidence.
- No `R#`/`U#` labels in code or commits.

## Open questions

- Fixture regeneration mechanism: is there a helper to emit the crafted frames
  to pcap, or are the fixtures written by a small one-off in the test package?
  The implementer picks the least-circular method that still satisfies R2.
- Whether to also compound the lesson (synthetic tiers passed a broken attack;
  live vendor truth caught it) as a `docs/solutions/` entry — recommended, via
  the `compound` skill, after this lands.
## Appendix: the proven ospf.go diff

Add the helper (as its own function, after `craftOSPFHello`) and, in each of the
four crafters, insert before `return craft.Default(...)`:

```go
	binary.BigEndian.PutUint16(payload[12:14], ospfChecksum(payload))
```

```go
// ospfChecksum computes the OSPFv2 packet checksum (RFC 2328 section D.4): the
// standard 16-bit ones-complement Internet checksum over the whole packet with
// the checksum field zero and the 64-bit authentication field ([16:24])
// excluded.
func ospfChecksum(p []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(p); i += 2 {
		if i == 12 || (i >= 16 && i < 24) {
			continue // checksum field and 64-bit auth field are excluded
		}
		sum += uint32(p[i])<<8 | uint32(p[i+1])
	}
	if len(p)%2 == 1 {
		sum += uint32(p[len(p)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
```
