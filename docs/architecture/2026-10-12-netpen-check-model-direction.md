---
title: netpen Check Model - Direction
type: direction
date: 2026-10-12
topic: netpen-check-model
status: proposed-direction
---

# netpen Check Model - Direction

netpen is the package FlowSeer uses to check a network for weaknesses on an
operator's command (`GOALS.md`). A check has four parts: it probes, it
observes, it returns a verdict, and it restores. Every behavior in the
catalog is a check, and every check declares what it costs the network
before a frame leaves.

## Context

The port of `l2l3-audit` landed on 2026-08-25 with a runner, a catalog, a
durability gate, a teardown executor, and 35 behaviors. The engine holds.
The behaviors do not yet check anything: `attacks/fh/arpspoof.go`,
`attacks/fh/hsrp.go`, `attacks/l2/dtp.go`, `attacks/routing/ospf.go`, and
their siblings send fixed frames to fixed addresses and emit a `finding`
record that describes the frames. Only `ghost`, `raguard`, and the live OSPF
lab tier (`docs/architecture/2026-09-23-netpen-lab-vendor-validation-direction.md`)
read anything back. The orchestrator in `full/full.go` turns any frame seen
on the watch leg within two seconds into a confirmed verdict for every
burst attack, which a switch's own STP hello satisfies.

The catalog's four durability classes mix two questions. "Does this hurt
the network, and for how long?" is the operator's consent question.
"Does the runner have to do something to put it back?" is the runner's
mechanism question. One enum answers both badly: a permanent mode must arm
a teardown it cannot perform (`attacks/l2/dtp.go:53`), and a decay bound is
free text the runner cannot wait for or verify.

## Decision

**A behavior is a check and returns a verdict.** The runner calls
`Run(ctx, Deps) (Verdict, error)`. The verdict is `confirmed`, `resisted`,
`pending`, or `skipped`, with the evidence that produced it and the window
it was observed in. `finding` records remain evidence. The verdict is the
claim, and the summary counts verdicts, not findings.

**Three fields describe the cost of a check.** `Impact` is one of `none`,
`temporary`, `permanent` and is the consent axis. `Restore` is one of
`none`, `decay` with a duration bound, or `active`, and is the mechanism
axis, and a `decay` bound is zero when no source states one. `Affects`
names who bears the impact: `none`, the target `device`, the `clients`
behind it, or the whole `fabric`. The runner arms a teardown only for
`active`, announces the bound for `decay` (or that it is unknown), and
requires nothing for `none`. A permanent check arms no teardown.

**A run has an impact ceiling.** `Options.MaxImpact` refuses, before any
frame, every entry whose impact exceeds it, and records the refusal. The
per-pair acknowledgment stays as the second gate on permanent checks. An
operator runs every no-impact check with one flag and no catalog knowledge.

**Targets are inputs.** The runner hands each check a typed target: the
victim, the gateway, the virtual address and group, the VLANs, the VTP
domain, the router identity, the IPv6 prefix. The attack leg reports its own
hardware and IP addresses. Flags fill the target on a single command, and
recon fills it under `full`. No non-test file carries a fixture address.

**Observation is bounded and explicit.** A check that reads the wire does
so for a window the runner supplies. An empty window is a `resisted` or
`pending` verdict, never a hang until cancellation.

**The library owns no process.** The runner package neither exits the
process nor installs signal handlers. The command does both.

**The catalog is a value.** An embedder builds a catalog from the behaviors
it wants, hands it to `Options`, and runs. Metadata and the run function
travel together in one `Behavior` value, so a registered name without an
implementation is a compile error, not a runtime refusal.

## Alternatives

- Keep the four classes and derive impact for the ceiling. Lost because
  the decay bound stays prose and the permanent modes keep arming no-op
  restores to satisfy a gate written for the wrong axis.
- Keep `error`-only behaviors and add an emitter method for verdicts. Lost
  because the rule "every check decides" would be enforced at run time on
  a tool whose purpose is to decide.
- Keep fixture addresses and add a `full`-only recon fill. Lost because a
  single command would still poison `172.16.0.10` on whatever network it
  runs on, and the tests would keep proving the fixture rather than the
  check.

## Consequences

- The JSONL contract moves to schema version 2: a `verdict` record kind
  appears, the rollup kinds fold into it, and the catalog columns change.
  Nothing outside this repository consumes version 1.
- Every behavior changes signature and reads its target from `Deps`. The
  fixture pins in `attacks/*/testdata` hold when the test supplies the
  fixture addresses as the target.
- A check without an observable returns `pending` with the reason, and the
  validation matrix shows it. That is the honest state of most of the
  catalog today and the backlog the next phases work through.
- The lab vendor validation record stays the source of vendor truth. A
  `confirmed` verdict from a check is the tool's own observation and does
  not replace a T2 cell.
- Defensive-control checks (DHCP snooping, dynamic ARP inspection, port
  security, RA-Guard, storm control) enter the catalog as checks with
  their own impact declared per probe. A probe that trips a guard which
  err-disables a port is not a no-impact check, whatever its name
  suggests.
