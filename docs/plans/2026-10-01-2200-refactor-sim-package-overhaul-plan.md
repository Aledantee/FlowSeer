---
title: Simulator Package Overhaul - Plan
type: refactor
date: 2026-10-01
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
---

# Simulator Package Overhaul - Plan

## Goal

The simulator under `src/common/netsim` becomes `src/common/sim`: a tree whose
shape takes further device kinds and media, whose capability packages share
one contract, whose protocols follow their standards, and whose known defects
are fixed with a test each. The means is twelve phases: a move with no
behaviour change, then the contract, then one phase per package cluster from
the layers up to the fabric.

Stop condition: if the device seam in U9 and U10 cannot carry what `fabric`
reads from a switch today without `fabric` importing a capability package,
the import rule in the direction record is wrong and the record is revised
before U10 starts.

## Decisions

- The root package is `src/common/sim` (decided by the user, 2026-10-01).
- The package shape allows further simulation of clients and of wireless
  radio (decided by the user, 2026-10-01). Why: those are the next subjects.
  The shape is in
  [`simulation-package-shape-direction`](../architecture/2026-10-01-simulation-package-shape-direction.md),
  accepted 2026-10-01.
- Protocols are implemented to their standards (decided by the user,
  2026-10-01). A gap between a layer's README and its standard is closed in
  code. A limit that remains is stated in the README with its reason.
- Every landed shape may break: schemas, Go APIs, trace identifiers
  (`AGENTS.md`, Agent behavior). No shim or alias survives a phase.
- The move comes first and changes no behaviour. Why: every later phase is
  re-planned on the final paths, and a move reviewed alone is a diff of
  import lines.
- Layer phases come before the switch and fabric phases. Why: the switch's
  fixes need layer APIs that do not exist yet (a multicast restore, a hold
  token on a held frame), and `After` between phases names real imports.
- No interface is written for a single implementation
  (`docs/code-style.md`, Scope and simplicity). The device interface arrives
  with the host as its second implementation. The medium stays a concrete
  cable package.
- `src/edge/netsimload` is renamed `src/edge/simload`. Why: it is named
  after the package it drives (decided by the user, 2026-10-01).
- The audit findings live in the phase plans as inventories. Each entry is a
  claim read from the cited lines at commit `61775c73`. A unit starts an
  entry by writing its failing test. An entry whose test passes is struck
  with a note, since most entries were read and not run.
- This plan stays whole as a parent. Why: the twelve clusters below have
  disjoint packages inside a wave, which `go list -deps` confirms for the
  layer packages (only `traffic` imports a sibling, `bridge`).

## Requirements

R1. No Go file imports a path under `src/common/netsim`, and the directory
does not exist. Example: `grep -rn 'common/netsim' --include='*.go' src`
prints nothing after U1.

R2. The import rules of the direction record hold. Example: after U10,
`go list -f '{{join .Imports "\n"}}' ./src/common/sim/fabric` prints no
package under `sim/layer/`, and `go list -deps` on it prints `sim/layer/phy`
as the only one, reached through `medium/cable`. After U2, no package under
`sim/layer/` lists a sibling in its `.Imports`.

R3. Every package under `sim/layer/` has the capability contract's shape.
Example: a conformance test under `test/conformance/` fails when a layer
package lacks `Config.Normalize(layer.Env)` or exports a type whose name ends
in `Fact`.

R4. A fork of a device equals its source in behaviour for every configured
capability. Example: a switch with a `Default: Drop` filter bound inbound
drops a frame, and so does `sw.Fork()`.

R5. Every inventory entry in a phase plan ends as a test that fails before
its fix, or as a struck entry with the reason. Example: the entry "an aging
seed overwrites a static FDB entry" becomes a `bridge` test that learns a
static entry, applies an aging seed for the same key, and still reads the
static port.

R6. Each protocol layer's README names its standard, edition, and clauses,
and the layer's behaviour matches them in every claimed mode. Example: an
MSTI Designated port on a point-to-point link reaches Forwarding through a
proposal and agreement exchange, without waiting two forward delays. That
MSTP specifies this exchange per instance is unverified here. U3 cites the
clause or replaces the example.

R7. A wire format has a known-bytes test from a source other than the
repository's own codec. Example: an MST BPDU whose MSTI record carries bridge
priority `0x8000` encodes the priority in the high nibble of the record's
octet, checked against bytes from the standard's layout or a device capture.
Which nibble the standard assigns is unverified here. The fixture decides
it, not the code on either side.

R8. `fabric` reaches a device only through `sim/device`. Example: a host and
a switch attach to one fabric through the same interface, and a filter
change on a switch shows as `Different` in `fabric.Compare`.

R9. No non-test file under the tree exceeds 1,200 lines and no function 150,
unless a comment at its head states why it is one unit. Example:
`netmodel.Load`, 2,050 lines today, is a coordinator over per-layer loaders.

## Out of scope

- A wireless access point, a client with behaviour, and a radio medium. The
  shape makes room and nothing more.
- Protocols the simulator does not run today: BGP, OSPF, an IGMP or MLD
  querier, LLDP, DHCP.
- A `flowseer/netsim/v1` schema, the shadow-projection wiring, and any
  service or module that consumes the simulator.
- The model registry's calibration tasks. They name
  `src/common/netsim/fabric` at fixed base commits
  (`.agents/skills/tune/references/calibration.md`) and are unaffected.
- `netmodel` reads protobuf messages produced by FlowSeer's own collectors.
  That input is trusted for shape and untrusted for completeness.

## Units

### U1. Move the tree to `src/common/sim`
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase1-plan.md
After: none
Landed: `d2c52250..bd684278`
Change: every package sits at its final path and every doc names it. No
behaviour changes.
Tests: the existing suites pass at the new paths.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/sim src/edge/simload`

### U2. Capability contract and surface trim
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase2-plan.md
After: U1
Landed: `414ffd79..6a2ea03a`
Change: `sim/layer` holds the shared contract types, every capability
package has the contract's shape, rule identifiers belong to their
producers, fact types and dead exports are gone, and the BPDU codec lives
under `src/common/net`.
Tests: a conformance gate for the contract, and the existing suites.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase2-plan.md`

### U3. Spanning tree to standard
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase3-plan.md
After: U2
Landed: `a8a26462..3f90b02d`
Change: `layer/stp` follows IEEE 802.1D and 802.1Q for RSTP, MSTP, and
legacy interoperation, with link state owned once per port.
Tests: one failing-first test per inventory entry, and known-bytes BPDU
fixtures.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase3-plan.md`

### U4. Link aggregation and physical layer to standard
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase4-plan.md
After: U2
Landed: `9077fffa..db130681`
Change: `layer/lag` follows IEEE 802.1AX, and `layer/phy` resolves speed,
duplex, and PoE within the stated bounds.
Tests: one failing-first test per inventory entry.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase4-plan.md`

### U5. Multicast snooping and loop protection
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase5-plan.md
After: U2
Landed:
Change: `layer/mcast` ages lazily and restores retained state with its
source filters, and `layer/loopprotect` arms its timers on every path.
Tests: one failing-first test per inventory entry.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase5-plan.md`

### U6. Routing, neighbor resolution, and filtering
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase6-plan.md
After: U2
Landed:
Change: `layer/routing` resolves neighbors as ARP and Neighbor Discovery
specify, a held frame carries what its release needs, and `layer/filter`
never widens a rule it cannot evaluate.
Tests: one failing-first test per inventory entry.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase6-plan.md`

### U7. Relay, port table, and traffic
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase7-plan.md
After: U2
Landed:
Change: `layer/bridge`, `sim/port`, and `layer/traffic` keep static entries
authoritative, validate deterministically, and mirror stacked tags intact.
Tests: one failing-first test per inventory entry.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase7-plan.md`

### U8. Analysis, trace, stream, and search
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase8-plan.md
After: U2
Landed:
Change: stream sources yield independent frames, alignment and minimization
name the journey they judged, and the leaves drop what nothing uses.
Tests: one failing-first test per inventory entry.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase8-plan.md`

### U9. Switch composition
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase9-plan.md
After: U3, U4, U5, U6, U7
Landed:
Change: `device/vswitch` has one receive entry returning the result with its
emissions, copies, and drops, drives its layers through the contract, forks
and derives every capability, and is split into files by subject.
Tests: one failing-first test per inventory entry, and a fork gate that
fails on an unconfigured capability.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase9-plan.md`

### U10. Fabric, device seam, and cable
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase10-plan.md
After: U8, U9
Landed:
Change: `fabric` drives switches and hosts through `sim/device`, the cable
model is `medium/cable`, every input is a queue event, and a held frame
continues its own journey.
Tests: one failing-first test per inventory entry, and the import rule of R2.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase10-plan.md`

### U11. Network model boundary
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase11-plan.md
After: U9, U10
Landed:
Change: `netmodel.Load` takes one input value and is a coordinator over
per-layer loaders, and an input it cannot translate raises an issue instead
of widening a rule.
Tests: one failing-first test per inventory entry.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase11-plan.md`

### U12. Corpus, load transmitter, and documentation close-out
Files: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase12-plan.md
After: U10, U11
Landed:
Change: the corpus and `simload` use the final APIs, their own defects are
fixed, and the READMEs and direction records match the tree.
Tests: one failing-first test per inventory entry, and `check-prose.py` on
every README in the tree.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase12-plan.md`

Waves: U1 | U2 | U3 U4 U5 U6 U7 U8 | U9 | U10 | U11 | U12

## Verification

Per phase, the verifier on the union of changed paths, never `--full`
(it race-tests `generated/go/yang` and exhausts host memory). For the whole
change:

```bash
go build ./src/common/sim/... ./src/edge/simload/...
go test -race ./src/common/sim/... ./src/edge/simload/... ./test/conformance/...
golangci-lint run ./src/common/sim/... ./src/edge/simload/...
go list -f '{{join .Imports "\n"}}' ./src/common/sim/fabric | grep 'sim/layer/' ; test $? -eq 1
```

The lab comparison of `simload` against the ICX7150
(`src/edge/netsimload/test/integration/README.md`) is a manual check in U12
and needs the switch powered on.

## Definition of done

- [ ] Verifier green for every changed path of every phase.
- [ ] Each phase plan reads `implemented` and its `Landed:` line here holds
      the commit range.
- [ ] Package READMEs, `CONCEPTS.md`, `GOALS.md`, and the three simulation
      records match the tree.
- [ ] The package shape record, accepted 2026-10-01, is amended wherever a
      phase departed from it.
- [ ] No plan label appears in code, comments, or commit messages.
- [ ] This plan's `status` is set, with an outcome note under its title.

## Open questions

- Which editions the protocol phases target is unverified: `stp/README.md`
  names its references, and no IEEE text is vendored under `spec/`. Each
  protocol phase fetches and cites its standard when it is re-planned, and
  says "unverified" where it cannot. Until then every statement in a phase
  plan about what IEEE 802.1D, 802.1Q, or 802.1AX requires is unverified,
  and no failing test is written from one. RFC 4861 section 7.3.2 does name
  the five neighbor cache states U6 uses
  (https://www.rfc-editor.org/rfc/rfc4861.html#section-7.3.2).
- Whether LACP "to standard" includes the Marker protocol is decided in U4
  against IEEE 802.1AX. Unverified here.
- Whether the layer engines move under `internal/` is reopened after U10,
  as the direction record's alternatives say.
