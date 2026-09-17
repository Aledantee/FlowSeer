---
title: Local Network Analysis Phase 3 - The mDNS Reflector Node - Plan
type: feat
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/architecture-patterns/a-third-kind-joins-a-two-kind-system-silently.md
execution: code
parent: docs/plans/2026-09-16-1625-feat-netsim-local-network-plan.md
amends: docs/architecture/2026-09-16-local-network-analysis-direction.md
---

# Local Network Analysis Phase 3 - The mDNS Reflector Node - Plan

> Implemented. 5 units, 2026-09-17T09:10Z to 2026-09-17T09:10Z.

## Goal

A fabric holds an mDNS reflector on a trunk, and a journey shows a query
crossing from one VLAN to the others through it. The means: a third node kind
beside switches and hosts, acceptance rules in the shape of the host's, a
reflect step that originates one copy per other attachment, and re-entry
detection that survives the new frame identities those copies carry. The plan
is wrong if the reflector cannot be a third node kind without reshaping how the
fabric classifies nodes, which would make this a refactor of `fabric.Config`
rather than an addition to it.

## Decisions

The parent's decisions on the reflector and on `Parent` apply. Its decision on
loop detection does not, and this plan amends the direction record that carries
it; see the first decision below.

- **Re-entry detection keys on the frame, not the root.** `f.entered` stays
  `map[FrameID]map[Endpoint]bool`, and a reflected copy's entry is seeded at
  creation with a copy of its parent's set. Why: keying on a shared root cannot
  tell an ancestor from a sibling. A reflector with three attachments on one
  trunk answers one query with two copies that both egress the same port and
  both arrive at the same switch port; under a root key the second is recorded
  as a loop although it revisited nothing. Seeding from the parent gives
  exactly "an endpoint one of my ancestors entered", which is what a loop is.
  This contradicts
  `docs/architecture/2026-09-16-local-network-analysis-direction.md:54-57`
  ("re-entry detection keys on the injected root frame"); that record is
  `status: proposed-direction`, and U5 amends the bullet rather than leaving
  the tree and the record disagreeing. No `Root` field is added, so mirror
  copies and the three journey-creation sites
  (`src/common/netsim/fabric/run.go:262`, `:521`, `:905`) are untouched, and
  the protocol journeys `injectEmission` mints keep their own keys.
- **A reflector arrival is enqueued and handled on the `Step` path, never
  inline.** A cable whose far end is a host calls `arrive` inline
  (`run.go:858`) while a switch far end enqueues an arrival (`run.go:876`); the
  reflector takes the enqueueing branch, and `Step` grows a reflector branch.
  Why: `arrive` runs inside `serve`, which `enqueueEgress` calls synchronously
  (`run.go:624`), so reflecting inline would make two reflectors on one cable
  exhaust the stack instead of halting on the step budget that parent R7
  requires. `arrive` stays the host-only inline function.
- **The reflector branch sits after the re-entry mark and before the switch
  lookup.** `Step` marks `f.entered` at `run.go:374` and then reads
  `sw := f.switches[arr.Device]` at `:376`, whose result it dereferences at
  `:379` through `Ports()`, a field read on the receiver
  (`src/common/netsim/vswitch/switch.go:390-392`). Why this is a decision and
  not a detail: that lookup is unguarded, so a reflector arrival reaching it
  panics on this plan's own happy path. No gate catches it: the panic
  conformance test walks the syntax tree for `panic` calls and cannot see a nil
  dereference, so U3's own test is the only thing that will. Placement is load-bearing too: after
  the mark, the reflector's own endpoint enters the set and the two-reflector
  loop lands its first `EntryLoop` on a reflector port as R6 describes; before
  it, the loop surfaces a generation later on a switch port.
- The reflector is a third node map on `fabric.Config`, not an extension of
  `Host`. Why: the parent decided the node kind, and `Host` is deliberately
  one-port and one-VLAN because "modeling it as a one-port switch would give it
  a forwarding database it must never use"
  (`src/common/netsim/fabric/config.go:171`).
- Adding the third map costs an edit at every site that decides a node's kind.
  The ones that fall through to "unknown node" rather than failing to compile
  are `accept.go:108`, `config.go:938`, `fabric.go:958`, `fabric.go:1076`, and
  `run.go:846`; `run.go:172` fails loudly through its else at `:238-242`.
  `config.go:762` is the name-collision check and `config.go:852` decides
  whether a port may appear in `Uncabled`. `run.go:376` is a site of its own kind: it keys on `f.switches` rather than on the host map, so the
  grep U1 runs does not reach it, and it is the one that panics rather than
  misclassifying. The per-kind iteration sites, which
  drop the map silently rather than misclassify, are `fabric.go:52`, `:86`,
  `:137`, `:236`, `:287`, and `:319`. Why this is enumerated: `.golangci.yml`
  enables no exhaustive-struct check, so a missed site compiles, and
  `fabric.go:137` in particular would make every `New` drop the map and
  surface three calls later as an endpoint-not-found error naming the cable.
- A reflector's attachments are `Attachments map[string]Attachment`, and the
  word "attachment" is used throughout, including in validation field paths and
  diff field paths. Why: the plan and the code should not disagree about the
  noun that appears in an error message a test asserts.
- A reflector's ports are all cabled and none may appear in `Uncabled`. Why:
  `config.go:852` already refuses a non-switch endpoint there, and an
  attachment with no cable has no VLAN to reflect onto.
- Acceptance is a pair of rules in the shape of `Host.macRule` and
  `Host.ipRule` (`accept.go:178-236`), on layer `ReflectorLayer trace.Layer =
  "reflector"` with rule ids under `reflector.mac.*`, `reflector.ip.*`, and
  `reflector.udp.*`. Why: the corpus pins a step's layer, rule id, and subject
  kind exactly (`internal/netsimtest/corpus.go:96-104`), so leaving the
  namespace to the implementer means the corpus freezes whatever it invents.
- Each reflector step carries facts, and they are named here rather than left
  to the implementer: the tag clause reads the frame's VLAN tags fact, the MAC
  clause the destination MAC fact, the IP clause `routing.AddrFact` as the host
  path already does (`accept.go:151`), and the UDP clause a new
  `fabric.udp_ports` fact whose canonical form is `src=<n>;dst=<n>`. Why: the
  corpus compares a step's `Inputs` and `Outputs` exactly
  (`internal/netsimtest/corpus.go:101-141`), and
  `fabric/trace_producer_conformance_test.go:14-60` rejects a production step
  carrying neither, so the facts are contract surface that U5 would otherwise
  freeze in whatever shape the implementer picked. No UDP fact exists in the
  tree today; this phase adds the one.
- A frame is reflected when its tag form matches an attachment on the arrival
  port, its destination MAC is the IPv4 or IPv6 mDNS group MAC, its IP
  destination is 224.0.0.251 or `ff02::fb`, its protocol is UDP, and its UDP
  destination port is 5353. A frame that satisfies the MAC clause but whose IP
  header does not decode is `EntryUnresolved` with an `Incomplete` issue, not a
  rejection. Why: the host path already distinguishes the two
  (`accept.go:146-149`), and
  `docs/architecture/2026-09-10-virtual-device-direction.md:221-227` makes an
  undecodable header a recorded outcome of its own rather than a refusal.
- A reflector acceptance records its entry and originates copies, and adds
  nothing to `Journey.Deliveries`. Why: a delivery is documented as a frame a
  destination host accepted (`journey.go:19-24`), and `Compare` pairs journeys
  on that field (`compare.go:83-103`), so a reflector delivery would change
  every comparison that crosses one.
- A copy is rebuilt end to end: `udp.Encode` over the unchanged payload with
  the attachment's address of the datagram's family, then `ip.Header.Encode`
  over that datagram, then the Ethernet frame with the reflector's MAC and the
  attachment's tag form. Hop limit is 255, which is one field carrying IPv4 TTL
  and IPv6 hop limit (`src/common/net/ip/ip.go:32-45`), and UDP source port is
  5353. Why: the copy changes the IP source, so the IPv4 header checksum
  changes with it, and `ip.Decode` refuses a header whose checksum does not
  match (`ip.go:67`); a receiving host would otherwise record the copy as
  undecodable instead of accepting it. RFC 6762 section 11 requires hop limit
  255 and section 5.2 source port 5353 from a compliant querier. Every other
  IPv4 field, the identification, the flags, the fragment offset and the
  options, is carried from the arriving header unchanged; only the source
  address and the hop limit are replaced.
- An attachment with no address of the datagram's family gets no copy, and the
  journey records a drop naming it.
- The reflector never reflects a frame whose Ethernet source is its own MAC.
  Why: a copy it sent floods back to it, and that is its own transmission.
  Two distinct reflectors still loop, which parent R7 requires to stay visible.
- The corpus cases are built on the inject-and-report pattern of
  `CaseTopologyShadowingUnresolvedTransceiver`
  (`internal/netsimtest/cases.go:569`), and each case pins one journey, because
  `ExecutionResult.Journey` is a single journey (`corpus.go:59`) and
  `ExpectedSteps` is that journey's complete ordered trace (`corpus.go:434`).
  The precedent says so itself (`cases.go:566-568`): two journeys mean two
  cases sharing one fixture.

## Requirements

Parent R6 and parent R7 carry over, restated as R5 and R6. In addition:

1. A configured reflector validates, clones, compares equal, normalizes, and
   builds, and every node-classification site treats it as a known node.
   Example: a config with reflector `r1` on cabled port `p1` passes `Validate`;
   naming `r1` as both a host and a reflector fails; `fabric.New` on that
   config produces a fabric whose `Spec` still carries `r1`.
2. `Validate` rejects a reflector with an uncabled port, a port named in
   `Uncabled`, an attachment naming an unknown port, two attachments sharing a
   port and VLAN, or an attachment with no address. Example: attachments `a`
   and `b` both on `p1` VLAN 10 fail naming
   `reflectors.r1.attachments.b.vlan` with the port and the VLAN. Attachments
   are walked in sorted name order, as `Validate` already sorts every map it
   reports a field path from (`config.go:696`, `:702`, `:748`), so the reported
   path is the same on every run rather than whichever the map yielded second.
3. `Diff` reports a reflector under `trace.Subject{Kind: "reflector", Key:
   "r1"}` with a snapshot fact of TypeID `fabric.reflector`, and a changed
   attachment VLAN as field `attachments.a.vlan`, relative to the subject as
   the host section's fields are. Example: two configs differing only in that
   VLAN produce one change whose subject key is `r1` and whose field is
   `attachments.a.vlan`. The dotted form `reflectors.r1.attachments.b.vlan` in
   R2 is the other convention, a validation error's field attribute; the README
   states both side by side (`src/common/netsim/fabric/README.md:340-343`).
4. A frame the reflector refuses records a terminal `EntryRejection` naming the
   clause that failed and produces no copy; one whose IP header does not decode
   records `EntryUnresolved` with an `Incomplete` issue. Example: a UDP
   datagram to 224.0.0.251 port 5354 is refused naming the port clause.
5. Parent R6: a reflector receiving an mDNS query on one attachment originates
   one copy per other attachment of the same address family, each a journey
   whose `Parent` is the received frame, with its own source MAC and address,
   hop limit 255, and source port 5353, and each copy decodes cleanly at a
   receiving host. Example: a query from a host on VLAN 10 reaches `r1`, whose
   attachments are VLAN 10 and 20 on trunk `p8`; one copy leaves `p8` tagged 20
   and the VLAN 20 host accepts it.
6. Parent R7: two reflectors sharing two VLANs produce a recorded loop and the
   run halts on its budget. The `EntryLoop` entry lands on the arriving copy's
   journey, which is the journey `Step` holds (`run.go:359`, `:372`). Example:
   `r1` and `r2` both attached to VLANs 10 and 20; a copy's journey gains an
   `EntryLoop` entry and the run stops on the budget rather than the stack.
7. Two sibling copies of one query are not a loop. Example: `r1` with
   attachments on VLANs 10, 20, and 30 over one trunk answers a query on VLAN
   10 with two copies that both reach the switch; neither journey carries
   `EntryLoop`.
8. A reflected copy's journey metadata folds in the reflector's cable
   dependencies, as a host delivery does. Example: with the copy's egress cable
   carrying an unresolved transceiver, the copy's journey carries that issue.

## Out of scope

- IPv4 to IPv6 reflection, service-name filters, and DNS parsing; the copy's
  payload is byte for byte.
- The reflector emitting IGMP or MLD reports to join the groups itself, which
  the parent's first open question leaves unresolved.
- A reflector that is also a host, with addresses reachable by unicast.
- Any change to how mirror copies are identified or detected.
- The adjacency-unresolved and oper-status-conflict issues `Fabric.Metadata`
  raises from its switch walk (`fabric.go:650-675`), which no reflector port
  produces. A reflector port can still carry a port-scoped issue through the
  link-trust walk (`fabric.go:643-648`, scoped at `:848-855`), and R8's fold
  handles it the same way it handles a cable's.

## Units

### U1. The fabric holds a third node kind
Files: `src/common/netsim/fabric/config.go`, `src/common/netsim/fabric/config_test.go`, `src/common/netsim/fabric/fabric.go`, `src/common/netsim/fabric/fabric_test.go`, `src/common/netsim/fabric/README.md`
After: none
Change: `Config` and `ConstructionSpec` gain `Reflectors map[string]Reflector`,
where `Reflector` is `{Address netaddr.MAC, Ports map[string]phy.Ethernet,
Attachments map[string]Attachment}` and `Attachment` is `{Port string, VLAN
*vlan.ID, Addresses []netip.Prefix}`. Every function the Decisions enumerate
gains the reflector: `Config.Clone`, `Equal`, `Normalize`, `Validate`,
`ConstructionSpec.Clone`, `Config()`, `NewConstructionSpec`,
`normalizeConstructionSpec`, `Spec`, `build`, `configuredMACs`,
`validateEndpoint`, and the `Uncabled` check. A reflector endpoint names a real
port, unlike a host's. No forwarding behaviour yet: a frame arriving at a
reflector is refused until U3.
Tests: `config_test.go` covers R1 and R2, each rejection asserting the error's
field attribute, since three of them would otherwise pass on any error.
`fabric_test.go` covers clone, equality, normalize, and that `New` followed by
`Spec` round-trips a reflector, which is the assertion that catches the dropped
map at `fabric.go:137`.
The unit greps the fabric package for `Hosts` and reports every site it
changed and every site it deliberately did not, since a missed site compiles.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U2. Diff reports a reflector
Files: `src/common/netsim/fabric/diff.go`, `src/common/netsim/fabric/reflector_diff_test.go`, `src/common/netsim/fabric/README.md`
After: U1
Change: `Diff` gains a reflector section in the shape of the host section
(`diff.go:374-448`; `:229-275` is switches and `:277-372` cables), with subject
kind `reflector`, a snapshot fact of TypeID
`fabric.reflector` beside `hostSnapshotFact` (`diff.go:109`), and field paths
relative to the subject as the host section's are, such as
`attachments.<name>.vlan`. The README's field-path list gains the reflector's.
Tests: a new `reflector_diff_test.go` covers an added reflector, a removed one, and a changed
attachment VLAN asserting the field path from R3.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U3. The reflector accepts, refuses, or cannot decide
Files: `src/common/netsim/fabric/accept.go`, `src/common/netsim/fabric/config.go`, `src/common/netsim/fabric/run.go`, `src/common/netsim/fabric/reflector_accept_test.go`
After: U1
Change: `Step` gains its reflector branch, placed as the Decisions require,
after the re-entry mark at `run.go:374` and before the unguarded switch lookup
at `:376`. A rule pair in the shape of the host's returns a rule id and a
boolean for the tag form, the destination MAC, the IP destination, the
protocol, and the UDP destination port, on layer `reflector` with the rule-id
namespace and the facts the Decisions fix. `ReflectorLayer` joins `HostLayer`
in `config.go:80-81` rather than in `accept.go`. The arrival path turns a miss into an `EntryRejection` naming
the failed clause and an undecodable IP header into `EntryUnresolved` with an
`Incomplete` issue, reusing `accept.go:107-158`. A frame whose Ethernet source
is the reflector's own MAC is refused by its own rule. Acceptance records its
entry and appends nothing to `Deliveries`. No copy is produced yet; U4 adds it.
Tests: a new `reflector_accept_test.go` covers each clause refusing on its own with every other
clause satisfied, so each test names one rule and no input is refused twice.
It also covers the self-sourced frame, the undecodable header, a corrupt
arrival dropping as a bad frame before any clause runs, and that an accepted
frame leaves `Deliveries` empty.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U4. The reflector reflects, and a loop stays visible
Files: `src/common/netsim/fabric/run.go`, `src/common/netsim/fabric/run_test.go`, `src/common/netsim/fabric/journey_metadata_test.go`
After: U3
Change: on acceptance the reflector originates one copy per other attachment of
the datagram's address family, each with a fresh `FrameID`, `Parent` set to the
arriving frame, its `f.entered` set seeded from the parent's, and the frame
rebuilt as the Decisions describe, enqueued through `enqueueEgress` the way
`Inject` does for a host origin (`run.go:287`). The cable's far-end branch
(`run.go:846`) enqueues an arrival for a reflector rather than calling `arrive`
inline, so a reflector-to-reflector cable halts on the budget instead of the
stack. U3 has already placed the `Step` branch this builds on. An attachment with no address of that family records a drop and produces
no copy.
Tests: `run_test.go` covers R5 end to end, asserting the copy's tag, source
MAC, source address, hop limit, UDP source port, and that a receiving host
decodes it rather than recording it undecodable, which is what catches a copy
whose IPv4 header checksum was not recomputed; the no-address drop; R6's
two-reflector loop asserting an `EntryLoop` entry and a budget halt; R7's three
attachments asserting neither sibling carries `EntryLoop`; and a
reflector-to-reflector cable halting rather than recursing.
`journey_metadata_test.go` covers R8's metadata fold and that a mirror copy's re-entry
detection is unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U5. The corpus pins the reflected query and the loop, and the record is amended
Files: `src/common/netsim/internal/netsimtest/reflector_cases.go`, `src/common/netsim/internal/netsimtest/cases.go`, `src/common/netsim/internal/netsimtest/corpus_test.go`, `src/common/netsim/internal/netsimtest/README.md`, `docs/architecture/2026-09-16-local-network-analysis-direction.md`, `docs/plans/2026-09-16-1625-feat-netsim-local-network-plan.md`
After: U4
Change: two troubleshooting cases in a new file, registered in
`DefaultRegistry`, added to the troubleshooting ID list and the total, and
described in the README's case list. Each pins one journey.
`troubleshooting/mdns-reflected-across-vlans` builds a fabric with a host on
VLAN 10, a switch with snooping and unregistered flooding off, a reflector on a
trunk with attachments on VLAN 10 and 20, and a host on VLAN 20 carrying the
group MAC in its accept list; it pins the copy's journey, whose steps end in
the VLAN 20 host's delivery. `troubleshooting/mdns-two-reflectors-loop` pins the
journey carrying `EntryLoop`, and it is also where the reflector's own rule
step, its rule ids and its `fabric.udp_ports` fact are pinned, because that
journey is the arriving frame's and the reflected-query case pins a copy's. The `Parent` link and the two-journey shape are
asserted in `corpus_test.go`, which no `Expected` field can carry. Both take
their step lists from a first run's rendered trace, each step checked against
the source before it is pinned.
The direction record's loop bullet is corrected here to say re-entry detection
keys on the frame with a reflected copy seeded from its parent, and why the
root key does not work. The parent plan's R7 example is corrected the same way:
it names the root journey, which this design makes unreachable, since the
injected query enters each reflector once and never re-enters.
Tests: the corpus runner, `TestRegistryDeterministicOrdering` with the new
count and IDs, and the README case list. Read the current total from
`corpus_test.go` rather than assuming it; phase 1 left it at 26.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/internal/netsimtest docs/architecture/2026-09-16-local-network-analysis-direction.md`

Waves: U1 | U2 U3 | U4 | U5

U2 and U3 share no file and both need only the type U1 declares. U4 needs U3's
acceptance decision and edits the arrival path; U5 needs the behaviour to pin.

## Verification

```bash
go test -race ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh --base main
```

No lab device takes part and no manual step remains.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] R1 to R8 hold on the landed tree.
- [ ] `src/common/netsim/fabric/README.md` describes the reflector beside the
      switch and the host and lists its diff field paths; the corpus README
      lists both new cases.
- [ ] The direction record's loop bullet matches the landed behaviour.
- [ ] U1 reports every host-map site it changed and every one it did not.
- [ ] This plan's `status` set with an outcome note under its title; the
      parent's U3 `Landed:` line carries the commit range.
- [ ] No plan labels in code.

## Open questions

- Whether the reflector should join the mDNS groups by emitting IGMP or MLD
  reports, which the parent left open. This phase assumes not: the IPv4 group
  floods regardless, and an IPv6 reflector in a snooped VLAN is modelled by
  injecting the report, as the phase 1 cases do.
