---
title: A Permissive Synthetic Peer Passes a Protocol Attack a Real Vendor Stack Rejects
date: 2026-09-24
last_verified: 2026-09-24
category: conventions
module: src/edge/netpen/attacks/routing
problem_type: bug
component: netpen
severity: high
symptoms:
  - "A packet-emitting attack passes every containerised and reproducibility test, but a live vendor device never forms the state the attack targets"
  - "A capture shows a protocol field (a checksum, a length, a reserved octet) left at zero where the specification requires a computed value"
  - "A fixture pin is green because the fixture generator and the code under test build the same bytes"
root_cause: "The OSPF behavior copied a zero-checksum defect from the Python l2l3-audit capture it was ported from. The netpen_t1 tier only waits for FRR's own r1-r2 adjacency and never asserts the attack against FRR state; AE6 only compares finding classes across two runs. FRR did not reject the zero checksum in that path, so both tiers passed a packet that Cisco IOS-XE drops per RFC 2328."
resolution_type: code_fix
applies_when:
  - "Claiming a packet-emitting behavior works against a target, where the only evidence so far is a containerised peer, a simulator, or a reproducibility test"
  - "Deciding what a netpen tier or a fixture pin proves, or reviewing text that reads a green synthetic tier as a vendor result"
  - "Porting a fixture or attack from another tool, where the source may itself carry a wire-correctness defect"
  - "A live vendor run fails while every synthetic tier passes, and you need to decide which side is wrong"
related_components: [protocol_client, packet_capture]
tags: [validation, vendor-truth, wire-format, protocol, ospf, testing, fixtures]
---

# A permissive synthetic peer passes a protocol attack a real vendor rejects

## The situation

netpen's `ospf` behavior crafted OSPFv2 packets with the header checksum field
(`[12:14]`) left at zero, the way the Python `l2l3-audit` capture it was ported
from did. Two tiers that could have caught it did not:

- The containerised `netpen_t1` tier starts FRR and waits for FRR's own
  r1–r2 adjacency to reach Full (`src/edge/netpen/test/integration/t1_lab_test.go:39`).
  The attack runs, but its result is never asserted against FRR state
  (`src/edge/netpen/test/integration/VALIDATION_MATRIX.md:105`).
- AE6 re-runs each attack and compares the finding classes of the two runs. Its
  own comment says it "checks reproducibility; it does not assert vendor
  behavior or wire shape" (`src/edge/netpen/test/integration/t1_ae6_test.go:28`).

The first live run against Cisco IOS-XE 17.3.2 (`LABRT42`) showed the gap. The
target replied to netpen's hellos but never listed the neighbor, and a hexdump
showed the checksum field as `0x0000`. IOS-XE validates the OSPF checksum per
RFC 2328 and silently drops packets whose checksum is wrong.

## What is true and why

A synthetic peer is a peer that shares your blind spots. FRR's OSPF input path
accepted the zero checksum in the scenario the tier ran, so the tier said
nothing about whether a production stack would. AE6 never asked the question
either: it reads netpen's own JSONL, which describes transmitted frames, not a
target's response. Read a green synthetic tier as evidence about your output,
never as evidence about a vendor's behavior.

The direction record states the rule this lesson applies:
[the live lab is the source of vendor truth](../../architecture/2026-09-23-netpen-lab-vendor-validation-direction.md),
and "a passing test says that the vendor accepted the injected behavior, rather
than only that netpen transmitted the expected frames." That record also warns
that "changing the harness to accept a weaker proxy would turn source-(b)
evidence into a transmission test."

Two habits follow:

- When the claim is "a device accepts this", assert the device's own observable
  (here `show ip ospf neighbor`) and record it. A skipped live run is pending
  evidence, never a pass.
- Verify a wire field from the emitted bytes, not from the constructor's own
  arithmetic. `src/edge/netpen/attacks/routing/README.md:22` puts it plainly:
  "The fixture generator mirrors the packet construction, so byte equality
  alone cannot establish protocol correctness." A fixture pin can be circular.

Compute the value from the specification and assert a residue over the wire
bytes, as `TestOSPF_ChecksumValid` does
(`src/edge/netpen/attacks/routing/routing_test.go:160`):

```go
got := binary.BigEndian.Uint16(packet[12:14])
if got == 0 {
	t.Fatal("OSPF checksum is zero")
}
// sum the packet with the auth field [16:24] excluded, fold carries...
if uint16(sum) != 0xffff {
	t.Errorf("OSPF checksum residue: got %#04x, want 0xffff", uint16(sum))
}
```

The fix is the same computation in the crafter, `ospfChecksum`
(`src/edge/netpen/attacks/routing/ospf.go:231`), plus the RFC 2328 section
12.1.7 Fletcher LSA-body checksum, `ospfLSAChecksum`
(`src/edge/netpen/attacks/routing/ospf.go:250`).

## Evidence

Live run, 2026-09-24, `LABRT42` (Cisco IOS-XE 17.3.2), from
`docs/plans/2026-09-24-2023-fix-netpen-ospf-checksum-plan.md`:

> **Before the fix:** netpen sent 55 hellos ... the target sent hellos back but
> **never listed the neighbor** — no adjacency. Hexdump of the OSPF header
> showed the checksum field (`0x20`–`0x21`) as `0x0000`. IOS-XE validates the
> OSPF checksum per RFC 2328 and silently drops packets whose checksum is wrong.

> **After the fix:** the OSPF checksum on the wire was non-zero (e.g. `0xf205`),
> and the target's own hellos grew to include a neighbor list: ... The spoofed
> router was accepted and the attacker (`10.0.0.99`, priority 1) was elected
> Backup Designated Router.

The matrix now carries the result (`VALIDATION_MATRIX.md:66`), and its note
records why the synthetic tiers missed it: "zero-checksum packets are dropped
per RFC 2328, so the `t1` FRR tier and AE6 never surfaced the defect"
(`VALIDATION_MATRIX.md:91-93`). The regenerated fixtures and their pins are the
new reference; the originals pinned the ported bug.

## What this does not cover

It does not say a live vendor run replaces the synthetic tiers. They are cheap,
repeatable, and catch many defects; the rule is narrower, that they cannot
adjudicate a claim about a vendor's response. It also does not tell you the
specification is right or that you read it correctly. The checksum computed
here is validated by the same RFC the target enforces, so it agrees with
IOS-XE; a field with no enforcing peer would still be pinned only by your
reading.
