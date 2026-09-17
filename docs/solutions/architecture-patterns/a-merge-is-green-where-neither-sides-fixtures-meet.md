---
title: A Merge of Two Feature Branches Is Green Exactly Where Neither Side's Fixtures Meet
date: 2026-09-17
last_verified: 2026-09-17
category: architecture-patterns
module: src/common/netsim/vswitch
problem_type: architecture_pattern
component: netsim
severity: high
applies_when:
  - "Merging two branches that each added a feature to the same package, and deciding what the merged tree's green suite proves"
  - "Resolving a merge conflict in a predicate that one side narrowed and the other side had copied elsewhere"
  - "Planning the commits that follow a merge, and deciding which composition of the two features needs a test of its own"
  - "Reviewing a merge commit, to decide whether a defect lives in the resolution or in what auto-merged around it"
related_components: [vswitch, routing, fabric]
tags: [merge, composition, test-fixtures, defect-class, netsim]
---

# A merge is green exactly where neither side's fixtures meet

## The situation

Two branches grew from one base. `main` added routed sub-interfaces, an
interface carrying both a parent port and a VLAN. The other branch added a
neighbor lifecycle, where a frame with no resolved next hop waits in a hold
queue until an advertisement releases it.

Both suites passed after `git merge`, and the merged tree carried a nil-pointer
dereference on the first frame ever held on a sub-interface.

Neither suite could see it, and not by oversight. Main's sub-interface tests
configure static neighbors, so a frame routed out a sub-interface resolves
immediately and never reaches a hold queue. The branch's held-frame fixtures
configure plain routed ports and VLAN interfaces, so nothing they hold is ever
a sub-interface. The cross product — hold a frame *and* route it out a
sub-interface — existed in no fixture on either side, because on each side the
other feature did not exist to put there.

## What makes it worse than an ordinary gap

The defect was in a predicate `main` had already fixed.

Both branches read `routing.Interface` through `egressIface.VLAN != 0`, meaning
"a VLAN interface, hand the frame to the bridge". A sub-interface satisfies it
too, so main narrowed its own reader to `VLAN != 0 && Port == ""`. The branch
had copied that predicate into its own release path, which did not exist on
main at all, so main's narrowing had nothing there to reach. The merge combined main's narrowed reader with
the branch's wide one, and `git` reported no conflict, because the two lines
are in different functions and only one side touched each.

This is [one slot, two roles](one-slot-two-roles-is-a-defect-class-not-a-defect.md)
with a branch boundary through the middle of the defect class: each reader is a
separate instance, and the instances the other branch owns are the ones the fix
does not reach.

## What to do

Before trusting the merge, write down each side's new feature and each side's
new fixture axis, and take the cross product. The cells no fixture on either
side occupies are where the composition defects are. Here there was one cell,
and it held the bug.

Then, in the plan for the merge, separate three kinds of work that want three
different commits:

- The conflict resolutions, which a reviewer reads against both parents.
- The build breaks that auto-merge into code that does not compile. These
  cannot wait for a later commit, because there is no compiling intermediate
  state to land them after.
- The latent defects, which compile and pass. These belong in their own commit
  after the merge, reviewable as an ordinary fix, with the failing test written
  first.

Say in the merge commit's message which latent defect it is leaving behind and
which commit repairs it. A merge that silently carries one reads, to the next
session, as a merge that was verified.

## Why the usual signals are absent

- **The compiler.** Both predicates are valid over the same struct. A copy of a
  predicate is not a symbol anything resolves.
- **`git`.** Conflict detection is textual and per-hunk. A rule duplicated in
  two functions, each touched by one side, merges cleanly in both places.
- **Both suites.** Each is complete for its own branch and passes on the merged
  tree, which is exactly the reassurance that makes the merge look finished.
- **The panic gate.** `test/conformance/` walks the syntax tree for `panic`
  call expressions. A nil-pointer dereference through a field on a nil struct
  pointer is invisible to it, as
  [a third kind joins a two-kind system silently](a-third-kind-joins-a-two-kind-system-silently.md)
  records for the same gate.

## Evidence

- The two readers of the same slot at the merge commit. `assembleRouteResult`,
  narrowed by main: `if egressIface.VLAN != 0 && egressIface.Port == "" {`.
  `releaseHeldFrame`, the branch's untouched copy:
  `if egressIface.VLAN != 0 {` followed immediately by
  `res := s.bridge.Egress(...)`. Both in
  `src/common/netsim/vswitch/switch.go` at merge commit `19b495c9`; the second
  is now narrowed to match, at `src/common/netsim/vswitch/switch.go:2988`.
- Main's sub-interface fixture resolves its neighbor statically, so it never
  holds: `Neighbors: []routing.Neighbor{{Interface: "eth1.20", Addr: ipH2, MAC: neighbor20}}`
  in `TestRoutedSubInterfaceForwardingAndTagMiss`
  (`src/common/netsim/vswitch/switch_test.go:4319-4321`).
- The branch's held-frame fixtures carry no sub-interface:
  `buildBaseRoutingSwitch` (`switch_test.go:3011`), `buildRoutedPortSwitch`
  (`:8170`), `buildHeldEgressSwitch` (`:8908`).
- `go test -race ./src/common/netsim/... ./src/common/net/...` passed at the
  merge commit, watched on 2026-09-17, darwin/arm64.
- The missing cell, once written, panics against that same commit:
  `TestARPObservationReleasesHeldFrameOnSubInterface`
  (`switch_test.go:8450`) fails with
  `panic: runtime error: invalid memory address or nil pointer dereference`
  through `bridge.go:1549`, watched on 2026-09-17.

## What this does not cover

It says nothing about which side's design should win a conflict; that is a
decision for the merge's plan, or for a `docs/architecture/` record when the
two records disagree. It also assumes both sides have suites worth trusting
individually — the cross product is a way to find what two good suites still
miss together, not a substitute for either.
