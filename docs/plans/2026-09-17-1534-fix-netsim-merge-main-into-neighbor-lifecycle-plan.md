---
title: Merge main's sub-interfaces and reflector into the neighbor lifecycle branch - Plan
type: fix
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
---

# Merge main's sub-interfaces and reflector into the neighbor lifecycle branch - Plan

## Goal

Branch `worktree-ban-panics` (tip `704c17c5`) merges `main` (tip `db6d2f3f`,
merge base `d4410aa4`) and lands with both features working together: a frame
routed out a sub-interface that main added holds, resolves, and leaves tagged
on its parent port the way a live frame does, and the mDNS reflector and the
hold queue keep their own accounting. The means are one merge commit that
resolves the five conflicts and nothing else, followed by three commits that
make the two features compose where the merge alone would leave a latent
defect. The plan is wrong if `git merge main` in the branch worktree conflicts
in a file other than the five named below; a sixth conflict means one side
moved since this plan was written and its Decisions need re-reading against
the new diff.

## Decisions

- `Route(now, iface, f, commit)` and `Originate(now, vrf, dst, protocol,
  payload, commit)` survive; main's two- and four-argument forms do not.
  Why: `now` sets an Incomplete entry's resolution deadline and `commit`
  keeps `Switch.Peek` from creating entries or queuing frames
  (`src/common/netsim/vswitch/routing/layer.go:690-697`). Main's
  sub-interface work adds no state of its own to either call: `ByPortVLAN`
  and `outerTagVID` are pure lookups over configuration
  (`git show main:src/common/netsim/vswitch/routing/layer.go`, `ByPortVLAN`;
  `main:src/common/netsim/vswitch/switch.go:1355`), so the wider signature
  loses nothing main needs. Every caller main added is a test of
  `ByPortVLAN` or `ByVLAN` and calls neither method, and main's
  `originateReflection` enqueues its copy with `enqueueEgress` rather than
  through `Inject` or `Originate`; the only stale callers after the merge
  are the two `s.routing.Route` sites in `switch.go`, and one of them is
  inside the conflict hunk.
- The merge commit resolves the five conflicts and the one build break the
  conflicts do not cover, and nothing more. Why: a reviewer reads a merge
  against both parents, and a behavioral change hidden in one cannot be
  diffed against either. The build break is `observationInterface`
  (`switch.go:1225`), which calls `Layer.ByPort`; main deletes `ByPort` and
  `PortLookupScopes` in favor of `ByPortVLAN` and `PortVLANLookupScopes`
  (`git diff HEAD...main -- src/common/netsim/vswitch/routing/layer.go`),
  and the call sits outside both conflict hunks, so it auto-merges into a
  tree that does not compile. Its replacement is the observation rule the
  next decision states, which is the smallest change that compiles and is
  the rule the forward path already applies; it lands in U1 with its tests
  because there is no compiling intermediate state to land it after. The
  merged tree is green on its own (the sub-interface tests main added use
  static neighbors, so they never reach the hold queue; the branch's
  held-frame tests configure no sub-interface), which makes the merge
  verifiable when it lands and leaves the release-path defect for a commit
  that can be reviewed as a fix.
- A sub-interface is a routed interface, so its neighbors resolve on it and
  its held frames leave by the port path, tagged. Why: `neighborKey` is
  `{iface, addr}` (`routing/layer.go:193`, used at `:837`), so `eth1.10` and `eth1.20`
  already keep separate entries with no change. The release path is where
  the merge breaks: `releaseHeldFrame` (`switch.go:2922`) reads
  `egressIface.VLAN != 0` as "VLAN interface, go through the bridge", which
  a sub-interface satisfies, so a released frame on `eth1.10` would enter
  `s.bridge.Egress` at FID 10 (and on a switch with no bridge, the
  sub-interface's ordinary shape, dereference nil). Main already narrowed the
  same predicate in `assembleRouteResult` to `VLAN != 0 && Port == ""` and
  prepends the C-TAG for a sub-interface before the transmit check
  (`main:switch.go:1406,1434-1441`). The release path takes both changes
  through one helper both paths call, so the two cannot drift again.
- Observation on a sub-interface keys by the ARP or NDP frame's outer VID.
  Why: `observationInterface` (`switch.go:1221`) resolves a routed port with
  `ByPort`, which the merge deletes. An ARP reply tagged 10 on `eth1`
  answers `eth1.10`'s neighbor, and the same reply tagged 20 answers
  `eth1.20`'s; resolving both to one interface would bind the reply to the
  wrong table row. The lookup mirrors the forward path's rule exactly: the
  port-state guard `resolved.Name != "" && receive.Reason == ""` stays, then
  `vid, cTag := outerTagVID(f)` and `ByPortVLAN(resolved.Name, vid)`; the
  interface is returned when `portRouted && matched && cTag`, and a routed
  port that misses (`portRouted` true, the rest not) observes nothing rather
  than falling through to the bridge, since a routed port has no bridge
  membership to fall through to. Main's `outerTagVID` returns `(0, true)`
  for an untagged frame (`main:switch.go:1355-1359`), so an untagged reply
  on a port carrying only sub-interfaces looks up VID 0, misses, and
  observes nothing, and on a plain routed port matches its untagged
  interface, which is what the forward path does with the same frame.
- `ethernet.Frame.Priority` is the one priority derivation; `fabric.framePCP`
  and main's `framePriority` go. Why: `framePCP` (`fabric/run.go:121`) is
  `Priority` minus the DEI return and the branch already deleted the
  switch's copy for the same reason (`57f3974c`, `b785181e`); main
  re-adds `framePriority` in the conflict hunk only because it never saw
  that change. The bridge and mirror derivations that the branch's phase 4c
  plan left alone stay alone: they differ in S-TAG handling and folding them
  would change classification for every frame.
- `outerTagVID` becomes `ethernet.Frame.OuterVID() (vlan.ID, bool)`, with
  the same contract main's function has, `(0, true)` untagged included, and
  the TPID test shared with `Priority` through one unexported helper. Why:
  main's comment on `outerTagVID` names "the same predicate framePriority
  uses" (`main:switch.go:1353`); two functions in two packages that must
  agree on one TPID test is the drift the branch's unification removed, and
  after the merge the function has two callers, the forward path and
  `observationInterface`. The untagged arm is not shared: `Priority` answers
  `(0, false)` for an untagged frame and `OuterVID` answers `(0, true)`,
  because the first reports whether a priority was carried and the second
  whether a C-TAG sub-interface could classify the frame, and an untagged
  frame is classifiable at VID 0. Unconfirmed: the alternative keeps
  `outerTagVID` in `switch.go` and rewords its comment to name `Priority`;
  it is a smaller diff, and the recommendation here is the move because it
  removes the TPID test's copy rather than the sentence about it.
- The corpus registry is the union, 30 cases, in ID order. Why:
  `DefaultRegistry` auto-merged and registers both sides' cases
  (the last two lines of `DefaultRegistry` in `cases.go` after the merge). The four `troubleshooting/mdns-*`
  IDs sort before `troubleshooting/neighbor-resolution-pending`. No case's
  expectations change: the mDNS and reflector cases configure no routing on
  any switch (`grep -n Routing mcast_cases.go reflector_cases.go` finds only
  the host-side `routing.addr` fact), and the neighbor case has no
  sub-interface or reflector.
- The reflector needs no unit. Why: a reflector arrival is decided in
  `Fabric.Step` before `f.switches[arr.Device]` is consulted
  (`main:fabric/run.go:392-414`) and its copies are ordinary injections, so
  `Switch.Wake`, held-frame release, and `DrainNeighborFailures` never see a
  reflector. The one seam is `Fabric.dependencies` (`fabric/journey.go:238`),
  where the branch grants an endpoint scope to a portless entry only for a
  host: main's `originateReflection` records a link-not-Up drop with no
  `Port`, and a reflector lives in `cfg.Reflectors`, not `cfg.Hosts`, so
  that entry resolved to `NodeScope(reflector)` on main and after the
  merge resolves to no endpoint scope. Nothing attaches an issue at a
  reflector's node or port scope (`main:fabric/fabric.go:685-712` scopes
  fabric issues to switch ports only), so the journey's metadata is the same
  either way and the cable scope the entry also carries is what holds the
  link's issues.
- `docs/solutions/architecture-patterns/a-decoder-wider-than-its-encoder-loses-whatever-you-queue.md`
  keeps its open paragraph. Why: main's `32c4e6e1` refuses an IPv4-mapped
  address in `netmodel.parseIP`, the configuration loader
  (`main:netmodel/netmodel.go:2120`); `src/common/net/ip` is untouched on
  main, so the question the paragraph asks about `ip.Decode` and
  `ip.Header.Encode` is still open.
- Neither direction record needs amending. Why: the virtual device record's
  neighbor subsection
  (`docs/architecture/2026-09-10-virtual-device-direction.md:545`) says a
  resolved entry releases its frames as ordinary emissions, and main's
  local-network record says a sub-interface is "tagged on egress; its parent
  port stays outside the bridge"
  (`main:docs/architecture/2026-09-16-local-network-analysis-direction.md`).
  The merged `releaseHeldFrame` contradicts the second until U3 lands; that
  is code catching up with a record, which this plan does, and no record
  changes.

- Ruled: `observationInterface`'s positive control in U1 is the untagged reply
  on the plain routed port, and the reply that binds a sub-interface moves to
  U3's release test. Why: `routing.Layer.Observe` updates an existing neighbor
  entry and creates none (`routing/neighbor.go:224-227`), so a sub-interface
  binding is observable only through the release path, which dereferences the
  nil bridge until U3; five subtests asserting "nothing happened" need a
  positive control that can pass at the merge commit. Cost if wrong: one
  subtest moves between two tests in the same file.
- Ruled: `routing.Layer.Clone`'s `byPort` copy is repaired in the merge commit
  alongside `observationInterface`. Why: main narrows `byPort` to
  `map[string]map[vlan.ID]string` and `Clone` lives in the branch-only
  `routing/neighbor.go`, so the auto-merge leaves the package uncompilable;
  this is the same kind of break the Decisions name for `observationInterface`,
  and there is no compiling intermediate state to land it after. Cost if wrong:
  a five-line deep copy moves to its own commit.
- Ruled: `outerTagVID`'s doc comment names `[ethernet.Frame.Priority]` rather
  than the deleted `framePriority`. Why: the merge deletes `framePriority`, and
  a comment citing a function the tree no longer has reads as a live reference.
  Cost if wrong: one comment line, which U3 deletes with the function.

## Requirements

1. The merge commit has two parents, `704c17c5`'s successor on the branch
   and `db6d2f3f`, and `go build ./... && go vet ./src/common/netsim/...`
   pass at that commit. Example: `git log --merges -1 --format=%P` names
   both; `go test -race ./src/common/netsim/... ./src/common/net/...` is
   green.
2. `TestRegistryDeterministicOrdering` wants 30 cases and lists the four
   `mdns-*` IDs between `loop-protect-contains-access-loop` and
   `neighbor-resolution-pending`. Example: `DefaultRegistry().All()` has
   length 30 and is strictly sorted by ID.
3. A frame held on a sub-interface releases tagged on its parent port. Example:
   `eth1.10 {Port: eth1, VLAN: 10}`, a frame arriving on `eth3` for
   `10.0.10.77` with no neighbor entry is `Held`; an ARP reply from
   `10.0.10.77` arriving on `eth1` tagged VID 10 followed by `Wake` yields one
   `Emission{Port: "eth1"}` whose `Frame.Tags` is
   `[{TPID: 0x8100, VID: 10, PCP: p, DEI: d}]` with `p`, `d` the ingress
   priority, `Frame.Dst` the learned MAC, and `DrainNeighborFailures` empty.
4. The same switch with no bridge configured does not panic on that release.
   Example: the switch in requirement 3 has `Bridge: nil`; the test in
   requirement 3 runs against it.
5. An advertisement on the parent port binds only the sub-interface its outer
   VID names. Example: with `eth1.10` and `eth1.20` and a frame held on
   `eth1.10`, the reply tagged 20 leaves the frame held and `Drain` empty; the
   reply tagged 30 (no interface), the reply tagged 10 with TPID `0x88A8`,
   the same reply untagged, and the reply tagged 10 arriving while `eth1` is
   operationally down leave it held too; the reply tagged 10 with TPID
   `0x8100` on the up port releases it.
6. A held frame on a sub-interface that times out reports the parent port.
   Example: after `ResolutionTimeout` passes and `Wake` runs,
   `DrainNeighborFailures()` has one `NeighborDrop` with `Port == "eth1"` and
   `Reason == routing.ReasonNeighborMiss`. `finishHeld` already fills
   `HeldFrame.Port` from the interface's `Port` (`routing/neighbor.go:311`),
   so this holds at the merge commit; the test is a pin, not a fix.
7. A released sub-interface frame never enters the bridge. Example: a switch
   with a bridge VLAN 10 on `swport` and `eth1.10`; the release in
   requirement 3 produces one emission on `eth1` and none on `swport`.
8. One priority derivation remains. Example: `git grep -n "framePriority\|framePCP\|outerTagVID" src/`
   is empty; `ethernet.Frame{Tags: []vlan.Tag{{TPID: 0x88A8, VID: 10, PCP: 5}}}`
   returns `Priority() == (0, false)` and `OuterVID() == (10, false)`; an
   untagged frame returns `OuterVID() == (0, true)`; a tag with TPID 0 and
   VID 7 returns `(7, true)`.
9. A fabric run delivers a released sub-interface frame to a VLAN host.
   Example: host `h2` cabled to the router's `eth1` with tag form VLAN 10,
   a packet injected at `h1` for `h2` is held, `h2`'s ARP reply releases it,
   and `h2`'s journey ends in a `Delivery` with the frame tagged 10.

## Out of scope

- Any change to `main`; the branch merges `main` and `main` is fast-forwarded
  by `close` afterwards.
- Folding the bridge's and the mirror's inline priority derivations into
  `ethernet.Frame.Priority`; their S-TAG handling differs and the branch's
  phase 4c plan left them standing for that reason.
- Adding `Port: att.Port` to the reflector's link-not-Up drop entry in
  `originateReflection`. It would give that entry the port scope its
  sibling injection entry already carries, but no issue lives at that scope
  today, so it changes nothing observable; it is left for main's
  local-network work.
- The `ip.Decode` IPv4-mapped question the decoder solution leaves open.
- Line citations in main's phase 4 plan
  (`docs/plans/2026-09-16-1625-feat-netsim-local-network-phase4-plan.md`)
  that this merge shifts; that plan is re-read when phase 4 is planned
  against the merged tree.

## Units

### U1. Merge `main`, resolve the five conflicts, and repair the one build break
Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/switch_test.go`,
`src/common/netsim/internal/netsimtest/corpus_test.go`,
`src/common/netsim/fabric/journey.go`,
`docs/solutions/README.md`,
`docs/agent-observations.md`
After: none
Change: `git merge --no-edit main` in the branch worktree, then each conflict
resolved as follows, the `observationInterface` break repaired, and nothing
else touched. `switch.go`, routed-port block: main's `ByPortVLAN`
classification, the `tag_miss` and `tag_protocol_miss` drops, and the
`portVLANScopes` consults all stand; the route call is
`s.routing.Route(now, ifaceName, f, mutate)` and the priority line is
`pcp, dei := f.Priority()`. `switch.go`, helper hunk: `framePriority` is
deleted (the branch deleted it and main only re-adds it because it never saw
that), `outerTagVID` stays as main wrote it; U2 moves it. The bridge-VLAN
route site keeps `s.routing.Route(now, iface, f, mutate)` as the branch has
it. `observationInterface` (`switch.go:1221-1236`) no longer compiles once
`ByPort` is gone; it becomes the rule the Decisions state: inside the
existing `resolved.Name != "" && receive.Reason == ""` guard,
`vid, cTag := outerTagVID(f)`, then `ByPortVLAN(resolved.Name, vid)`;
return the interface when `portRouted && matched && cTag`, return
`"", false` when `portRouted` and anything else fails, and fall through to
the bridge pass only when the port carries no routed interface. Its doc
comment (`switch.go:1213-1220`) names `[routing.Layer.ByPortVLAN]` and says
a routed port that misses observes nothing. `corpus_test.go`: the count is
30 and `wantTroubleshooting` lists the
four `mdns-*` IDs after `loop-protect-contains-access-loop` and before
`neighbor-resolution-pending`. `journey.go`, `Entry.Step` doc: one sentence
carrying both clauses, the reflector's Reflection, Rejection, or Unresolved
entry with no preceding Arrival, and the Drop entry a `vswitch.NeighborDrop`
produces. `docs/solutions/README.md`: both sides' rows, main's three and the
branch's two, in the table order each side used. `docs/agent-observations.md`:
main's three entries and the branch's one, in date order. The commit is a
merge commit titled `merge(netsim): bring main's sub-interfaces and reflector
into the neighbor lifecycle branch`; its body names the five files, names
`observationInterface` as the build break repaired outside them, and states
that `releaseHeldFrame` does not yet handle a sub-interface, which U3 does.
Tests: `switch_test.go` — a new `buildSubInterfaceSwitch` fixture with
`eth1.10`, `eth1.20` on `eth1`, `eth3` as a plain routed port, no bridge, and
`NeighborPolicy` at defaults; `TestSubInterfaceObservationBindsOnlyItsOuterVID`
proves the part of requirement 5 that holds before U3, with five subtests that
must not bind (`0x8100`/20, `0x8100`/30, `0x88A8`/10, untagged, and
`0x8100`/10 with `eth1` set `OperStatus: port.Down` through `SetOperStatus`
before the reply), each asserting `Drain` and `DrainNeighborFailures` empty,
and one positive control that holds a frame on `eth3` and releases it with an
untagged reply there. Requirement 5's releasing case is U3's
`TestARPObservationReleasesHeldFrameOnSubInterface`; see the ruling. The five
subtests cannot be watched failing against the merge's own code, since that
code does not compile; the four tag subtests are watched failing against a
variant that classifies at a fixed VID whatever the frame carries, and the
down-port subtest against a variant with the `receive.Reason == ""` guard
removed. `go build ./...`,
`go vet ./src/common/netsim/...`, and
`go test -race ./src/common/netsim/... ./src/common/net/...` pass at the merge
commit; `TestRegistryDeterministicOrdering` and
`TestRoutedSubInterfaceForwardingAndTagMiss` both run green, the second
proving main's static-neighbor sub-interface path, untagged and tagged
alike, survives the wider `Route`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/ src/common/net/ docs/solutions/README.md docs/agent-observations.md`

### U2. One C-TAG predicate in `ethernet`, and `framePCP` folded into it
Files: `src/common/net/ethernet/ethernet.go`,
`src/common/net/ethernet/ethernet_test.go`,
`src/common/netsim/fabric/run.go`
After: U1
Change: `ethernet.go` gains an unexported `isCTagTPID(tpid uint16) bool`,
true for zero and `EtherTypeDot1Q`, and `Priority` tests the outer tag's
TPID with it. `Frame.OuterVID() (vid vlan.ID, cTag bool)` returns
`(0, true)` for an untagged frame, and otherwise the outer tag's VID with
`isCTagTPID(outer.TPID)`; its doc comment says the VID is reported either
way so a caller can name it in a drop step, which is what main's
`routing.tag_protocol_miss` drop does, and says why the untagged answer
differs from `Priority`'s. `fabric/run.go` deletes `framePCP` and its three
callers (host injection, mirror copies, reflected copies) read
`pcp, _ := frame.Priority()` instead; `injectEmission` already transmits
`em.PCP`. Nothing in `switch.go` changes here; U3 replaces `outerTagVID`.
Tests: `ethernet_test.go` — `OuterVID` on an untagged frame, a `0x8100` tag,
a zero-TPID tag, and a `0x88A8` tag, the last two pinning `(7, true)` and
`(10, false)`; `Priority` on the same four frames returns what it did before
(the existing cases stay). The `framePCP` fold has no test of its own: the
evidence is that `framePCP` (`fabric/run.go:121-131`) is `Priority`
(`ethernet.go:201-211`) minus the DEI return and every caller discards DEI,
which the implementer confirms by reading both before deleting one.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/ethernet/ src/common/netsim/fabric/`

### U3. A sub-interface's held frames release tagged on its parent port
Files: `src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/switch_test.go`,
`src/common/netsim/vswitch/routing/neighbor.go`,
`src/common/netsim/vswitch/README.md`
After: U2
Change: `switch.go` gains `subInterfaceTag(iface routing.Interface, pcp
vlan.PCP, dei bool) ([]vlan.Tag, bool)` returning the one C-TAG a
sub-interface's egress carries (`TPID: EtherTypeDot1Q, VID: iface.VLAN, PCP,
DEI`) and false for any other interface shape; `assembleRouteResult` prepends
it where main inlines the tag today, and `releaseHeldFrame` prepends it to
`hf.Frame.Tags` on the port arm. `releaseHeldFrame`'s bridge arm runs only
for `egressIface.VLAN != 0 && egressIface.Port == ""`, the predicate
`assembleRouteResult` already uses, so a sub-interface takes the port arm.
`outerTagVID` is deleted and its two callers, the forward path and
`observationInterface`, call `f.OuterVID()`. `neighbor.go`'s
`HeldFrame.Port` comment (`neighbor.go:87-94`) changes "empty for a VLAN
interface" to "empty for a VLAN interface with no parent port, and the
parent port for a sub-interface". `vswitch/README.md`: the Routing bullet's
egress sentence (main's wording, "a sub-interface's egress carries one tag
naming its VLAN") gains "on the live and the released path alike", and the
`Wake` bullet that describes releasing a held frame (`README.md:322-329`
before the merge) gains one sentence: a sub-interface resolves neighbors
under its own interface name, releases held frames tagged on its parent
port, and an advertisement on that port binds only the sub-interface its
outer VID names.
Tests: `switch_test.go`, on U1's `buildSubInterfaceSwitch` fixture.
`TestARPObservationReleasesHeldFrameOnSubInterface` proves requirements 3
and 4, watched failing first: before the change the release dereferences
the nil bridge. `TestHeldFrameOnSubInterfaceTimesOutAgainstParentPort` pins
requirement 6; it passes before the change too and its comment says it is a
pin against the `HeldFrame.Port` contract, not proof of a fix.
`TestReleasedSubInterfaceFrameStaysOffTheBridge` proves requirement 7 on a
second fixture with a bridge VLAN 10 on `swport`, watched failing first:
before the change the frame floods on `swport`. The existing
`TestReleasedFrameCarriesPriorityOntoUntaggedEgress` gains a
`"sub-interface"` row whose expected egress tag carries PCP 5 and the
ingress DEI.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/`

### U4. A fabric run delivers a released sub-interface frame
Files: `src/common/netsim/fabric/routing_test.go`
After: U3
Change: none in production code. The test builds on
`TestFabricReleasesHeldFrameOnObservedARPReply`'s shape: a router switch with
`eth1.10` and `eth3`, `h1` on `eth3` untagged, `h2` on `eth1` with VLAN tag
form 10 (the shape `TestVlanHostReplacesTheCallersTag` in `run_test.go`
uses), an IPv4 packet from `h1` to `h2` injected before any ARP, then `h2`'s
ARP reply tagged 10 injected at `h2`.
Tests: `TestFabricReleasesHeldFrameOnSubInterfaceToVLANHost` proves
requirement 9: the first journey ends `Held` at the router, the release
opens a journey whose `Delivery` at `h2` carries `Tags[0].VID == 10`, the
router's port `eth1` counts the transmission, and no `NeighborDrop` entry is
recorded.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric/`

Waves: U1 | U2 | U3 | U4

The graph is a chain because each unit edits or tests against the unit before
it: U2 needs main's `outerTagVID` in the tree, U3 needs `OuterVID` and edits
the `switch_test.go` U1 wrote, and U4 needs U3's release path. No re-cut
widens it without splitting one file's edit across two units.

## Verification

```bash
git log --merges -1 --format=%P                     # two parents, one is db6d2f3f
go build ./... && go vet ./src/common/netsim/...
go test -race ./src/common/netsim/... ./src/common/net/...
git grep -n "framePriority\|framePCP\|outerTagVID" src/   # empty after U3
.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/ src/common/net/ethernet/ docs/solutions/README.md docs/agent-observations.md
```

`test/conformance/` gates run from the Stop hook. The nil-bridge
dereference U3 removes is a runtime fault the panic gate cannot see;
`TestARPObservationReleasesHeldFrameOnSubInterface` is its only cover.

## Definition of done

- [ ] The merge commit exists with `db6d2f3f` as its second parent and is
      green on its own.
- [ ] Verifier green for every changed path in U1 through U4.
- [ ] `src/common/netsim/vswitch/README.md` describes sub-interface neighbor
      resolution and release; `src/common/net/ethernet` documents `OuterVID`.
- [ ] This plan's `status` set to `implemented` with an outcome note under
      its title, and `review` and `compound` fields added by those skills
      before `close` runs.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Whether `outerTagVID` moves to `ethernet.Frame.OuterVID` (the
  recommendation, taken above) or stays in `switch.go` with its comment
  pointing at `Priority`. The implementer follows the recommendation; the
  two callers are the forward path and `observationInterface`, and the move
  changes neither's result.
- Whether the reflector's link-not-Up drop entry should carry
  `Port: att.Port` so it keeps an endpoint scope under the branch's
  `dependencies` rule. Left out here because it is unobservable today; the
  owner of the local-network work decides.
