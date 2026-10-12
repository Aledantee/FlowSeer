---
title: netpen Check Model Phase 3, Recon and Orchestration - Plan
type: feat
date: 2026-10-12
artifact_contract: flowseer-plan/v2
execution: code
---

# netpen Check Model Phase 3, Recon and Orchestration - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`netpen full` discovers the segment, fills the target from what it found,
runs the burst concurrently under the impact ceiling, takes each check's
verdict as the verdict, and reports a summary an operator can act on. The
means: a typed `Evidence` struct, a recon that sends from the leg's own
addresses, a burst phase with one runner per worker, and a selection table
driven by the catalog's preconditions.

**Stop condition:** the shared attack leg cannot serve concurrent workers
because AF_PACKET delivers each frame to one `Receive` channel only. The
burst then stays sequential and the parent's requirement 12 is met without
concurrency.

## Decisions

The parent plan's Decisions apply. Local to this phase:

- `Evidence` is a struct with typed fields (`MACs`, `VLANs`, `VRIDs`,
  `HSRPGroups`, `VTPDomain`, `VTPRevision`, `VoiceVLAN`, `RA6`, `MVRP`,
  `SweepNet`) and a method `Target()` that fills `check.Target`. Why:
  `full/evidence.go` is a stringly keyed map, and `full.go:303` reads a
  magic key.
- The sweep request carries the leg's MAC and an IPv4 address of the leg.
  Why: `afpacket.TPacket.WritePacketData` is a plain write
  (`~/go/pkg/mod/github.com/gopacket/gopacket@v1.7.3/afpacket/afpacket.go:567`),
  so the zero source `full/recon.go:180` sends today goes on the wire as
  written, and the comment claiming the kernel fills it is false.
- Preconditions live in the catalog entry as typed requirements on
  `Evidence` (`NeedsRA6`, `NeedsVLANs`, `NeedsVTPDomain`, `NeedsMACs`,
  `NeedsWatchLeg`), and `full` selects by them. Why: `full/gates.go:3`
  calls itself a mirror of the catalog's preconditions and the two already
  differ (`vrrp` is armed by VRIDs in `gates.go:70` and has no
  precondition in `registrations.go`).
- The burst runs one runner per worker on the shared leg, each with its
  own `Receive`, when the stop condition does not bite. Why: the comment in
  `full/full.go:14` promises it and the duration flag means nothing
  without it.
- `scan` and recon read the leg's addresses for their probes. Why: the
  sweep request and the passive probes carry fixture constants today, and
  `vlanenum` sends nothing (`src/edge/netpen/attacks/l2/vlanenum.go:31-32`),
  so it needs no change here.

## Requirements

Parent requirement 12 is claimed here. The re-plan pins:

1. Recon fills the target. Example: a recon stub returning two MACs and
   VLAN 20 makes `arpspoof` run with the first MAC as victim and `vlanhop`
   run once with VLAN 20, and the progress record names them.
2. The burst honors the ceiling. Example: `netpen full --max-impact none`
   runs only the entries whose impact is `none` and emits a `refusal`
   record for each other burst entry with the ceiling reason.
3. Verdicts come from the checks. Example: the summary of a stubbed burst
   equals the sum of the stubs' returned verdicts.
4. The sweep request is well-formed. Example: the recorded sweep frame on
   the test leg carries the leg's MAC in the Ethernet source and ARP sender
   fields and the leg's IPv4 address as the sender protocol address.

## Out of scope

- Observables inside the behaviors. Phase 2.
- The passive detectors under `full/scan.go` beyond the address fix.

## Units

Written at re-plan time: evidence struct, recon, burst, selection, each
with its tests.

## Verification

```bash
cd src/edge/netpen && go vet ./... && CGO_ENABLED=0 go test -race -count=1 ./full/... ./cmd/...
```

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `uv run tools/scripts/run.py plan record implemented <phase> --units <n> --from <t> --to <t>`.

## Open questions

- Whether AF_PACKET fan-out to several `Receive` channels on one socket is
  possible without a second socket per worker. Unverified until the
  afpacket source and the kernel documentation are read at re-plan.
- Whether the sweep network should default to the leg's own IPv4 prefix
  rather than a flag. The parent's `--sweep-net` flag stays either way.
