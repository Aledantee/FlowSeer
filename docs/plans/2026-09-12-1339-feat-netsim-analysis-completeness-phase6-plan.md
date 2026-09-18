---
title: Network Simulation Analysis Completeness, Phase 6 - Plan
type: feat
date: 2026-09-17
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/conventions/a-convergence-fingerprint-lies-when-it-omits-a-resolution-axis-or-keeps-a-timer.md
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 6: Scenarios, replay, and run lifecycle - Plan

## Goal

A caller can name a fabric analysis, replay it, and read one honest answer about
how it ended. `Fabric.Run` returns a typed result carrying a stop reason, an
`analysis.Status`, the pending work left over, and the specification that
reproduces the run; a scenario carries the timed actions and the observation
window that produce it; and every journey in the report has exactly one result
state. The means: a `Scenario` value and a `RunResult` value in
`src/common/netsim/fabric`, with a canonical state fingerprint underneath the
convergence and oscillation decisions.

This phase claims parent requirements R27 through R31, and consumes R13, R26,
R37, and R39.

Two stop conditions, both about work this phase does not do.

If `Fabric.Fork` does not land in parent U5 carrying the queue, the journeys and
the frame-id counter, this plan is wrong. Scenario replay and the isolation tests
below rest on forking a *running* fabric, and nothing in the tree does that
today: `src/common/netsim/fabric/derive.go:15` builds a new fabric from a
specification and starts journeys, queue, and counters empty. Phase 5's plan, now
implementation-ready, decides exactly this
(`2026-09-12-1339-feat-netsim-analysis-completeness-phase5-plan.md:333-335`), so
the condition is expected to hold; it is written down because this phase cannot
check it.

If phase 4b's switch and fabric wiring has not landed, U3 and U5 are wrong. On
this branch only `src/common/net/arp`, `src/common/net/ndp` and the routing
layer's neighbor lifecycle are in; `trace.Held` and `Emission.Protocol` are not,
and `src/common/netsim/trace/trace.go` declares four outcomes, none of them
`Held`. Without that wiring the fabric never releases a held frame, so
`OriginRelease`, `JourneyReleased` and requirement 7 have no producer and no
test. An implementer who finds that state must stop rather than invent the
release path here.

## Decisions

- **`Run` returns a typed result and keeps its budget argument.**
  `func (f *Fabric) Run(budget int) RunResult` replaces
  `func (f *Fabric) Run(n int) int` (`src/common/netsim/fabric/run.go:959`).
  Today a caller reads a bare count and must call `Fabric.Err` to tell a
  scheduling fault from a drained queue (`run.go:767`), and when the count
  equals the budget it cannot tell a budget stop from a queue that drained on
  the last step. Keeping the budget as a positional argument keeps the 108
  existing `fab.Run(n)` call sites across 21 files compiling. Twelve read the
  returned count and change: `compare.go:44` and `:45`, `counters_test.go:40`,
  `:235`, `:249`, `run_test.go:247`, `:336`, `:379`, `:409`, `:689`, `:963`,
  `:1277`, and `run_internal_test.go:162`, plus the README example at
  `fabric/README.md:144`.
- **A scenario is the second entry point, and `Replay` is the third.**
  `func (f *Fabric) RunScenario(s Scenario) (RunResult, error)` applies timed
  actions and owns the observation window; `func Replay(spec ReplaySpec)
  (RunResult, error)` builds a fabric from the specification and runs the
  scenario inside it, and is where the contract-version check lives, because it
  is the only entry point that takes a `ReplaySpec` as input. Convergence
  detection is a scenario property in R27 and R30, and a caller asking for
  exactly n steps must still get exactly n steps. Three entry points with one
  job each beat one method with mode flags.
- **Every scenario declares a positive step budget.** `Scenario.Budget int`,
  refused by `Validate` when it is not positive. Without it a scenario over a
  fabric with a spanning tree never returns: the queue never drains because each
  wake re-arms itself, and with `Window` 0 there is no convergence stop either.
  `Window` 0 therefore means "do not detect convergence", never "do not stop";
  `StopBudget` bounds every scenario run.
- **The run's status is `analysis.Status`; the stop reason is new and lives in
  `fabric`.** `analysis` already owns `Complete`, `Incomplete`, `Exhausted`,
  `Unstable`, and `Unsupported` with a fixed conservative precedence
  (`src/common/netsim/analysis/status.go:29-44`), and `analysis/doc.go:9-10`
  says execution stop reasons belong to the simulator that produces them. So
  `RunResult.Status` is summarized from the run's scoped issues the way every
  other netsim result is, and `fabric.StopReason` is a new string type beside
  it. The two never derive from each other: a converged run can be `Incomplete`
  because an unresolved adjacency was consulted.
- **`StopReason` has no meaningful zero value.** The constants are
  `StopNotRun`, `StopQueueDrained`, `StopBudget`, `StopConverged`,
  `StopOscillating`, and `StopFault`, all non-empty strings, and a run always
  sets one. A non-positive budget is `StopNotRun` rather than the empty string,
  because a zero value that means both "never started" and "unset" is the
  defect class in
  [one-slot-two-roles](../solutions/architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md).
- **A journey's origin is a typed relation, not a reuse of `Parent`.**
  `Journey.Mirror string` and `Journey.Parent FrameID` (`journey.go:96-105`)
  become one `Journey.Origin JourneyOrigin`. Today `Parent` answers "which
  frame was this a mirror copy of", set only beside `Mirror` at `run.go:524`.
  Making it also answer "which journey held this frame before the neighbor
  resolved" gives one slot two roles, which the solution above records as a
  defect class rather than a defect. `Journey.Protocol` stays a separate field:
  phase 4b makes a released held frame a non-protocol emission, so protocol-ness
  and origin are independent axes.
- **`trace.Held` survives this phase unchanged.** Phase 4b left this open
  (`2026-09-12-1339-feat-netsim-analysis-completeness-phase4b-plan.md:676`).
  `trace.Outcome` is the direction record's *domain outcome* axis, what one
  device did with one frame at one hop
  (`docs/architecture/2026-09-10-virtual-device-direction.md:136-152`).
  `JourneyState` is a different axis on a different object: what the whole run
  concluded about one frame. Renaming `trace.Held` so the hop outcome could
  carry the run's answer would be the same one-slot-two-roles error pointing
  the other way.
- **A released held frame's journey names the journey that held it.** Phase 4b's
  other open question (`:666-675`): the released journey carries
  `Origin{Kind: OriginRelease, Of: <holding FrameID>}`, and the holding journey's
  state is `JourneyReleased`. Nothing is added to the holding journey to point
  forward; a reader indexes `Report()` by `Origin.Of`, which is what the mirror
  relation already requires and avoids a second slot that can disagree with the
  first.
- **The fingerprint is a canonical string, not a hash.** R30 asks for the
  repeating fingerprint sequence to be reported, which a hash cannot supply
  without a side table, and the repository already canonicalizes values to
  strings for comparison (`trace.Fact.Canonical() string`,
  `fabric.Endpoint.Canonical()` at `config.go:155`). It excludes the clock, the
  arrival queue, and every counter, which is exactly what makes a no-op periodic
  wake invisible to convergence: a converged fabric's queue is never empty,
  because each wake re-arms itself (`run.go:924-944`,
  `src/common/netsim/fabric/README.md:409-413`). U2 below fixes the field list,
  because getting it wrong in either direction breaks convergence silently.
- **A held frame blocks `StopConverged`.** A routing layer's hold queue reaches
  no part of `Snapshot`, so the fingerprint cannot see a frame waiting on a
  neighbor. Reporting a converged run with unfinished work the measurement
  cannot observe is the kind of silent answer this whole parent plan exists to
  remove, so the rule is that `StopConverged` requires the fingerprint window
  *and* no journey in `JourneyPending`.
- **A scenario carries no seed.** Nothing in netsim is random: the direction
  record states it at `:91-93`, determinism comes from the arrival queue's total
  order (`fabric/README.md:544-552`), and the corpus already proves it by
  executing each case twice and diffing
  (`src/common/netsim/internal/netsimtest/corpus.go:833-856`). A `Seed` field
  nothing reads is an unread slot a later reader would take for a randomness
  contract. This contradicts R27 and the previous draft of this phase; see the
  last section.
- **Replay identity is the contract version plus the construction
  specification plus the scenario.** `ReplaySpec{Contract, Spec, Scenario}` with
  `const ReplayContract = "netsim-fabric/v1"`, rejected before execution when
  the contract string differs. The previous draft also asked for "rule-set
  identity", which has nothing to identify: `trace.RuleID` is producer-owned
  with no central registry by the direction record's own decision (`:545-551`).
- **Every new configuration type gets `Validate`, `Normalize`, `Clone`, and
  `Diff` in the unit that adds it**, per
  [validate-and-derive](../solutions/architecture-patterns/validate-and-derive-judge-what-new-builds.md).
  That binds `Scenario`, `Action`, and `Record`, and nothing else: this phase
  adds no `RunOptions` type, the budget and window being fields of `Scenario`.
  `DiffScenarios` follows
  `DiffSpecs` (`src/common/netsim/fabric/diff.go:144`) and returns
  `[]trace.Change` under a `scenario` subject kind. Phase 5's reflection-driven
  `Diff` coverage gate does not reach these types: it enumerates the directories
  under `src/common/netsim/vswitch/` that hold a `diff.go`
  (`2026-09-12-1339-feat-netsim-analysis-completeness-phase5-plan.md:326-328`),
  and `fabric` is not one of them. U5 below writes the per-field `Diff` test by
  hand.
- **A scenario clones its actions and its record bytes, not the frames inside
  them.** Phase 5 decides that an `ethernet.Frame` is an immutable value and
  that `Fork` shares payload and tag backing arrays
  (`2026-09-12-1339-feat-netsim-analysis-completeness-phase5-plan.md:72-81`). A
  scenario follows the same rule for frames. It does clone the action slice and
  each `Record.Bytes`, because those are raw
  caller-owned input the caller may still be writing into, not a frame the
  simulator produced.

## Requirements

1. **R27:** A `Scenario` is an immutable named value of a construction
   specification, ordered typed timed actions, a positive step budget, and an
   observation window, and it replays to the same entry sequence.
   **Acceptance example:** a scenario that cuts a cable at `t0+1s` and injects a
   frame at `t0+2s`, replayed twice through `Replay` from the same
   `ReplaySpec`, yields two `RunResult`s whose journeys, entry times, stop
   reason, and fingerprint sequence are equal; and the same scenario with
   `Window` 0 over a spanning tree fabric returns `StopBudget` rather than
   running forever.
2. **R27b:** Two actions with the same `At` execute in declaration order, and
   `Normalize` makes that order explicit rather than resting on slice position.
   **Acceptance example:** two injections at `t0` from different origins produce
   `FrameID` 1 and 2 in declaration order after `Normalize` has reordered a
   deliberately shuffled action slice by `At`.
3. **R28:** A `Record` replays a decoded frame or raw bytes and keeps its
   timestamp, source, and truncation evidence; undecodable bytes are an input
   error, not a journey. **Acceptance example:** a record with
   `OriginalLen` 1500 and `CapturedLen` 64 injects its 64 octets, the journey's
   state is `JourneyTruncated`, and the journey metadata holds
   `IssueTruncatedRecord` at `analysis.Incomplete` naming the record's source;
   a record whose `Bytes` are not a decodable Ethernet frame makes
   `Scenario.Validate` return an error and nothing executes.
4. **R29:** `RunResult` carries the stop reason, steps taken, logical clock,
   pending-work summary, `analysis.Status`, issues, fault, and `ReplaySpec`.
   **Acceptance example:** a fabric with a spanning tree run to a budget of 5
   returns `StopBudget`, `Steps` 5, `Pending.Wakes` 1 and `Pending.Arrivals` at
   least 1, and an `Exhausted` issue scoped to the whole fabric, while a
   separately raised unsupported issue from the same run survives beside it.
5. **R30:** With a declared window, a run stops at `StopConverged` when the
   fingerprint is unchanged across that many consecutive wake arrivals, the
   queue holds only wakes, and no journey is `JourneyPending`; and at
   `StopOscillating` with `analysis.Unstable`
   when the fingerprint sequence repeats a cycle of period 2 or more at least
   twice. **Acceptance example:** a two-switch spanning tree with a window of 3
   reaches `StopConverged` while hellos keep firing; a fabric whose root
   alternates each hello returns `StopOscillating`, `Unstable`, and a
   `Cycle []string` holding the two alternating fingerprints in order.
6. **R31 / R13:** Every journey has exactly one `JourneyState`, assigned by a
   declared precedence, and `JourneyPending` is the only nonterminal one. Host
   acceptance is what separates `JourneyRejected` from `JourneyDelivered`, which
   is R13's physical-arrival-versus-acceptance split read at the journey level.
   **Acceptance example:** a frame that arrives at a host whose MAC it does not
   match is `JourneyRejected` and appears in no `Deliveries`; a frame flooded to
   two hosts where one accepts is `JourneyDelivered`; a frame held for an
   unresolved neighbor at the run boundary is `JourneyPending` and is counted in
   `RunResult.Pending.Journeys`.
7. **R31b / R26:** A journey's origin says how it began, and a forked fabric's
   scenario state is independent of its source. **Acceptance example:**
   a released held frame's journey has `Origin.Kind == OriginRelease` with
   `Origin.Of` equal to the holding journey's `FrameID`, whose state is
   `JourneyReleased`; and mutating a scenario action slice, or running a fork to
   completion, changes neither the source fabric's next `RunResult` nor the
   scenario the source still holds.
8. **R37 / R39:** The corpus admits two cases whose current answers are false
   today, and every new configuration type round-trips through `Validate`,
   `Normalize`, `Clone`, and `Diff`. **Acceptance example:** changing only
   `Scenario.Window` from 3 to 5 yields exactly one `trace.Change` with subject
   kind `scenario`, and removing that `Diff` arm fails a test.

## Out of scope

- Comparison of `RunResult` or `JourneyState` across forks, and bounded
  differential search. Parent U7 owns both.
- `Fabric.Fork` itself, snapshot-versus-fork separation, and the derivation
  invalidation rules. Parent U5 owns them; this phase consumes them.
- Packet-capture file readers and live capture. A `Record` is supplied by the
  caller already decoded or as bytes, as the parent's out-of-scope list requires.
- Wall-clock execution, a scenario assertion language, scenario persistence, and
  any file or network I/O.
- A randomness contract. See the seed decision above.

## Units

### U1. Amend the direction record

Files: `docs/architecture/2026-09-10-virtual-device-direction.md`
After: none
Change: the record gains a "Scenarios, run completion, and convergence"
subsection after "Fabric link and port state, host acceptance, and journey
metadata" (`:194`). It states that a run ends for one of six typed reasons and
that the reason is separate from the `analysis.Status` the result carries, in
the same terms the "Result contract axes" section already uses (`:136-163`);
that a journey has exactly one result state with `Pending` the only nonterminal
one; that a journey's origin is a typed relation whose kinds are a caller's
injection, a mirror copy, and a frame released from a hold, which the journey
metadata section at `:194-236` does not mention today; that convergence is
decided on a canonical fingerprint of protocol-relevant state
that deliberately excludes the clock and the queue, so a periodic hello cannot
make exhaustion look like quiescence; and that a scenario is an in-memory value
with no persistence and no capture reader. The "Remaining capability gaps" list
(`:621-643`) loses "Scenario overlays and search" in favour of a narrower entry
naming packet-generation search spaces alone, and loses "Convergence
guarantees", both of which this phase closes.
Tests: none; the record is prose, and the behavior it describes is tested in U3
through U6.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-10-virtual-device-direction.md`

### U2. Canonical fabric state fingerprint

Files: `src/common/netsim/fabric/fingerprint.go`,
`src/common/netsim/fabric/fingerprint_test.go`,
`src/common/netsim/fabric/run.go`,
`src/common/netsim/vswitch/switch.go`,
`src/common/netsim/vswitch/switch_test.go`
After: none
Change: `func (s Snapshot) Fingerprint() string` returns a canonical string over
the protocol-relevant state of a snapshot.

`Snapshot` cannot answer for spanning tree today, and this unit widens it first.
`Device.Roles` comes from `Switch.Roles()`
(`src/common/netsim/vswitch/switch.go:2606`), which calls `stp.Layer.PortInfo`,
and that is CIST-only (`src/common/netsim/vswitch/stp/layer.go:759-761`). Under
MSTP or per-VLAN RSTP an MSTI or a VLAN tree can churn while the CIST sits
still, so a fingerprint built on `Roles` alone would report `StopConverged` on a
fabric that is visibly reconverging — and the corpus already runs two such
fabrics (`planning/mstp-vlan-instances-diverge`, `planning/pvst-per-vlan-root`).
`Device` therefore gains `TreeRoles map[vlan.ID]map[string]stp.PortInfo`,
filled from `stp.Layer.VLANPortInfo` (`layer.go:776`) for each VLAN the switch
configures, and the fingerprint reads that rather than `Roles`. The per-VLAN
information reaches the fabric through a new `Switch` accessor that mirrors
`Roles()` (`src/common/netsim/vswitch/switch.go:3233`, CIST-only via
`stp.Layer.PortInfo`): a method returning `map[vlan.ID]map[string]stp.PortInfo`
over the configured VLANs from `stp.Layer.VLANPortInfo`, so the snapshot reaches
per-VLAN roles the same way it reaches CIST roles. Its test lives in
`switch_test.go` beside `Roles`'s.

The fingerprint includes, per device in name order: port operational states,
per-VLAN tree port information, forwarding database entries, multicast groups,
and multicast router ports, each in a declared key order; then the links in
canonical endpoint order. A forwarding database entry contributes its `Origin`
and `Lifetime`, which parent U5 splits out of the `Static` boolean
(`2026-09-12-1339-feat-netsim-analysis-completeness-phase5-plan.md:108-117`), so
a configured entry and an observed one at the same address never fingerprint
alike; those two fields do not exist until parent U5 lands.

From `stp.PortInfo` it takes `MSTID`, `Role`, `State`, `BlockReason`,
`Priority`, `PathCost`, `DesignatedRoot`, `Designated`, `DesignatedPort`,
`DesignatedCost`, `PointToPoint`, `Edge`, and `SendRSTP`, and explicitly not
`ForwardTransitions`, `TxBPDUs`, `RxBPDUs`, or `BadBPDUs`
(`stp/layer.go:80-83`). Those four advance on every hello, so a fingerprint
carrying them never repeats and convergence never fires. `Snapshot.Clock`,
`Queue`, `Queued`, `Busy`, `Device.Counters`, and `Device.RelayCounters` are
excluded for the same reason. `Device.Power` is included: PoE state is a
planning fact a caller compares runs on, and it does not advance on a timer.
The encoding escapes its separators the way `encodeEndpointPart` does
(`src/common/netsim/fabric/config.go:159`), so two different states cannot
encode alike.
Tests: `fingerprint_test.go` proves the encoding is injective over a table of
state pairs that differ in exactly one included field; that randomized map
insertion order into the same fabric yields byte-identical fingerprints; that
advancing the clock, queueing a wake, and sending a hello that changes nothing
else all leave the fingerprint unchanged, while a port role change, an MSTI
role change on a fabric whose CIST is unchanged, and a per-VLAN root change do
not. A reflection walk over `Snapshot` and `Device` reads a table classifying
every field as included or excluded and fails on a field with no entry, so a
field added later cannot be silently left out; it is modelled on
`walkPortStateFields`
(`src/common/netsim/vswitch/stp/link_state_internal_test.go:138`), whose lesson
is that a table nothing reads is inert. The same walk covers
`stp.PortInfo`'s fields, which is where the counters hide.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric/`

### U3. Journey identity and result state

Files: `src/common/netsim/fabric/journey.go`,
`src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/journey_state_test.go`,
`src/common/netsim/fabric/journey_metadata_test.go`,
`src/common/netsim/fabric/traffic_test.go`,
`src/common/netsim/fabric/run_test.go`,
`src/common/netsim/internal/netsimtest/corpus.go`,
`src/common/netsim/fabric/README.md`
After: U2
Change: `Journey` loses `Mirror` and `Parent` and gains
`Origin JourneyOrigin{Kind JourneyOriginKind; Of FrameID; Mirror string}` with
kinds `OriginInjection`, `OriginMirror`, and `OriginRelease`, and gains
`State JourneyState`. `JourneyState` has `JourneyPending`, `JourneyDelivered`,
`JourneyRejected`, `JourneyDropped`, `JourneyLooped`, `JourneyTruncated`,
`JourneyUnresolved`, and `JourneyReleased`, each a non-empty string. `Report`
assigns the state from the journey's entries by a precedence declared in one
table, highest first: `JourneyPending` (a copy still queued, in an egress queue,
or held), `JourneyUnresolved`, `JourneyLooped`, `JourneyTruncated`,
`JourneyReleased`, `JourneyDelivered`, `JourneyRejected`, `JourneyDropped`.
Pending outranks everything because the run has not finished with the frame;
`JourneyDelivered` outranks `JourneyDropped` because a flood that reached one
host did deliver. The mirror site at `run.go:524` sets `OriginMirror`, and the
site phase 4b adds for a released frame sets `OriginRelease`.

Three readers test `Journey.Mirror` for emptiness to mean "this journey is not a
mirror copy" and change behavior on it: the policer bypass at `run.go:412`, the
mirror-of-a-mirror guard at `run.go:506`, and the corpus determinism comparison
at `src/common/netsim/internal/netsimtest/corpus.go:1002`. Each becomes a test
of `Origin.Kind`, which is the point of the change: the emptiness of a name was
standing in for a kind. The remaining two `run.go` sites are the set site at
`:523-524` and the transmit call at `:535`, which pass the name through.
`traffic_test.go:234-243`, `:322`, `:370`, `:460`, and `:793-796` read the two
fields directly and move with them, and the fabric README's mirror paragraph at
`:190-193`, which names both fields by hand, is rewritten to name `Origin`.
Tests: `journey_state_test.go` holds the precedence table test, one case per
state and one per adjacent pair; a classification gate mapping every
`JourneyOriginKind` to whether `Of` and `Mirror` must be set, where the
assertion reads the table rather than recording it, plus a check that every
declared kind constant appears in the table so a new kind fails; and a test that
after a run with a mirror, a protocol emission, and a caller injection, no
journey in `Report()` has an empty `Origin.Kind` or an empty `State`. That last
test is there because the classification gate covers a kind misclassified and
cannot cover a call site that sets no origin at all, which is the half the
one-slot-two-roles solution names as uncovered.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric/`

### U4. Typed run result, stop reason, pending work, convergence

Files: `src/common/netsim/fabric/result.go`,
`src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/compare.go`,
`src/common/netsim/fabric/run_internal_test.go`,
`src/common/netsim/fabric/run_test.go`,
`src/common/netsim/fabric/counters_test.go`,
`src/common/netsim/fabric/result_test.go`,
`src/common/netsim/fabric/README.md`
After: U2, U3
Change: `func (f *Fabric) Run(budget int) RunResult` replaces the count-returning
form. `RunResult` holds `Stop StopReason`, `Steps int`, `Clock time.Time`,
`Pending PendingWork{Arrivals, Wakes, Egress, Journeys int}`,
`Status analysis.Status`, `Issues []analysis.Issue`, `Err error`, `Replay
ReplaySpec`, and `Fingerprints []string` with `Cycle []string`. The error field
is `Err`, not `Fault`, because `fabric.Fault` is already the cable-fault
configuration type (`config.go:125-129`) that `SetFault` and U5's `FaultAction`
use. `Status` is summarized from `Issues` through `analysis.Summarize`
(`src/common/netsim/analysis/issue.go:42`); a budget stop adds an `Exhausted`
issue scoped to the whole fabric. A scheduling fault adds `Unsupported`, which
is a stretch of that constant's documented meaning (`status.go:42`, "the
analyzer cannot evaluate a required part of the declared scope") and is chosen
anyway because a scheduling-invariant breach is a simulator bug whose results a
caller must not trust, and `Unsupported` is the most conservative value the
status vocabulary has. `Fabric.Err` stays for the `Step` path and returns the
same value.

`Run` takes the fingerprint after each step and records it; with a window
declared through `RunScenario` it stops at `StopConverged` when the last window
wake arrivals left the fingerprint unchanged, the queue holds only wakes, and no
journey is `JourneyPending`, and at `StopOscillating` when the recorded sequence
ends in a cycle of period 2 to window repeated at least twice, filling `Cycle`.
`ReplaySpec` and `ReplayContract` are declared here and populated from
`Fabric.Spec()`; U5 adds the scenario and the `Replay` entry point.
`compare.go:44` and `:45` read `.Steps`.
Tests: `result_test.go` covers each stop reason including `StopNotRun` for a
non-positive budget, a table asserting `StopReason` has no empty constant, the
budget-and-unsupported case from R29 where both issues survive, pending-work
counts against a hand-built queue, convergence on a two-switch spanning tree
with hellos still firing, a fabric whose fingerprint window is satisfied but
which holds one `JourneyPending` journey and therefore does not converge,
oscillation on a fabric whose root alternates, and a run whose fingerprint is
stable but whose status is `Incomplete` because an unknown adjacency was
consulted, proving the two axes do not derive from each other. The README's
convergence advice at `:409-413`, which tells callers to compare consecutive
snapshots themselves, is replaced by the new contract, and the example at
`:144` reads `.Steps`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric/`

### U5. Scenario, typed timed actions, record replay, replay identity

Files: `src/common/netsim/fabric/scenario.go`,
`src/common/netsim/fabric/record.go`,
`src/common/netsim/fabric/result.go`,
`src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/scenario_test.go`,
`src/common/netsim/fabric/record_test.go`
After: U4
Change: `Scenario{Name string; Spec ConstructionSpec; Actions []Action; Budget
int; Window int}` with `Validate`, `Normalize`, `Clone`, and `DiffScenarios`.
`Validate` requires a non-empty `Name`, a positive `Budget`, and a non-negative
`Window`; `DiffScenarios` reports a `Name` change like any other field, so the
name is a compared value rather than a label. `DiffScenarios` normalizes both
arguments before comparing and returns the error, exactly as `DiffSpecs` does
(`src/common/netsim/fabric/diff.go:144-153`); without that, `Normalize` filling
`Index` would make a raw value differ from its own normalized form.

`Action{At time.Time, Index int, Kind ActionKind, Inject *Injection, Fault
*FaultAction, Mcheck *McheckAction, Record *Record}` is a typed variant.
`Validate` requires exactly the one pointer its `Kind` names. There is no
`LinkAction`: the exported fabric mutators are `Inject` (`run.go:162`), `Mcheck`
(`fabric.go:979`), and `SetFault` (`fabric.go:999`), and nothing sets a link's
administrative state, so a link action would be a kind with no operation behind
it. A cut trunk is `FaultAction` carrying `Fault{Kind: FaultCut}`.

`Action.At` is the only time a scenario reads. `Injection` carries its own `At`
(`run.go:33-38`) and so does `Record`, so `Validate` refuses an inner `At` that
is set and differs from `Action.At`, and `Normalize` stamps a zero inner `At`
from `Action.At`. Two slots that can disagree about when something happens is
the same defect this plan rejects for `Journey.Parent`.

`Action.Index` is 1-based and `Validate` refuses a slice that numbers some
actions and not others, so index 0 means "not numbered" and never "first".
`Normalize` assigns indices from slice position when none is numbered, then
sorts by `At` then `Index`, so declaration order survives the sort without a
zero value carrying two meanings.

`Record{At, Source, Origin, Frame *ethernet.Frame, Bytes []byte, CapturedLen,
OriginalLen int}` decodes `Bytes` at `Validate` time, so undecodable bytes are a
Go error before execution; `OriginalLen > CapturedLen` marks the record
truncated, and the injected journey gets `IssueTruncatedRecord` at
`analysis.Incomplete` scoped to the journey and the state `JourneyTruncated`.
`Record` gets the same `Validate`, `Normalize`, `Clone`, and `Diff` treatment as
the other two.

`func (f *Fabric) RunScenario(s Scenario) (RunResult, error)` validates,
normalizes, applies each action at its time between `Step` calls under
`s.Budget`, and fills `RunResult.Replay` with `ReplayContract`, `f.Spec()`, and
an independent clone of the scenario. `func Replay(spec ReplaySpec) (RunResult,
error)` is the entry point that takes a replay specification: it refuses a
`Contract` that is not `ReplayContract` before building anything, then calls
`NewWithSpec(spec.Spec)` and `RunScenario(spec.Scenario)`. The check lives there
because `Replay` is the only entry point a wrong contract can reach.
`Scenario.Clone` copies the action slice and each `Record.Bytes` and shares
frame payloads, per the decision above.
Tests: `scenario_test.go` covers same-time declaration order, a shuffled action
slice normalizing to the declared order, the full `Validate` refusal table (a
kind with no pointer, a kind with two, a zero or negative budget, a negative
window, an empty name, a partially numbered index slice, an inner `At` that
disagrees with `Action.At`, and an action before `Spec.Start`), the `Diff` arm
for every field of `Scenario`, `Action`, and `Record` including `Budget` and
`Window`, `DiffScenarios` reporting no change between a raw value and its own
normalized form, a `Replay` whose contract differs refused before any step runs,
a scenario over a spanning tree fabric with `Window` 0 returning `StopBudget`
rather than hanging, and the R27 double-execution equality through `Replay`. It
also asserts that mutating the caller's action slice after `RunScenario` returns
changes neither `RunResult.Replay.Scenario` nor a second run's result, and that
running a
`Fabric.Fork` to completion leaves the source's next `RunResult` unchanged.
`record_test.go` covers a decoded record, a byte record, a truncated record's
issue and state, and an undecodable record's error.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric/`

### U6. Corpus cases and package documentation

Files: `src/common/netsim/internal/netsimtest/scenario_cases.go`,
`src/common/netsim/internal/netsimtest/cases.go`,
`src/common/netsim/internal/netsimtest/corpus_test.go`,
`src/common/netsim/internal/netsimtest/README.md`,
`src/common/netsim/fabric/README.md`, `src/common/netsim/README.md`
After: U4, U5
Change: a new `RegisterScenarioCases` in a new `scenario_cases.go` holds both
cases, and `DefaultRegistry` calls it after `RegisterRoutingCases`
(`cases.go:2178-2187`). It does not go in `RegisterBaselineCases`, whose doc
comment says it populates "the three initial baseline cases" (`cases.go:2142`);
every phase since has added its own function and its own file, and phase 4b adds
to `RegisterRoutingCases`.
`planning/scenario-replays-link-flap` builds a two-switch fabric, declares a
scenario that cuts a trunk at `t0+1s` and injects a frame at `t0+2s`, executes
it twice, and records the current false answer: today a caller hand-sequences
`Inject` and `Run` calls with no value that reproduces the sequence, so nothing
says the two executions were the same analysis.
`troubleshooting/periodic-protocol-hides-exhaustion` runs a spanning tree fabric
to a budget too small to converge and expects `StopBudget` with an `Exhausted`
issue; its `CurrentResult` records that `Run` returns the budget count and
`Err()` nil, which reads exactly like a completed analysis. Both bind expected
rules, subjects, facts, metadata, and an ordered `ExpectedSteps` list, all of
which `ValidateCase` requires (`corpus.go:535-573`); it also refuses an
`ExpectedRules` entry with no matching step (`:574-581`), so the two lists are
written together. The fabric README gains a scenario example and a run
lifecycle section; `src/common/netsim/README.md` gains the two new entry points;
the netsimtest README's admitted-case catalogue gains both entries.
Tests: `TestRegistryDeterministicOrdering` (`corpus_test.go:575`) moves its count
literal from 26 to 28. `planning/scenario-replays-link-flap` sorts last in
`wantPlanning`, after `planning/pvst-per-vlan-root`.
`troubleshooting/periodic-protocol-hides-exhaustion` sorts after
`troubleshooting/neighbor-resolution-pending`, which phase 4b's own corpus unit
inserts, and before `troubleshooting/recursive-route-not-installed`. The
literal is 25 on the tree
this plan was written against (`corpus_test.go:579`) and 26 after phase 4b's own
corpus unit lands; the implementer reads the literal rather than trusting this
sentence. `TestCorpusAdmitsAndExecutesEveryCaseDeterministically` picks both
cases up without a change.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/internal/netsimtest/ src/common/netsim/fabric/README.md src/common/netsim/README.md`

Waves: U1 U2 | U3 | U4 | U5 | U6

The graph is a chain after the first wave, and the reason is that U2 through U5
all land in `src/common/netsim/fabric/run.go`: U2 widens `Device` so the
fingerprint can see per-VLAN trees, U3 rewrites the four journey sites, U4
replaces `Run`, and U5 adds the scenario entry points. Only U1 is fully
independent, the record being prose. Re-cutting the fabric package to widen this
is not work this phase should invent; a narrower `run.go` is its own change.

## Verification

```bash
go test -race ./src/common/netsim/...
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

No lab or device check applies. Every requirement here runs in `go test` with
in-memory inputs and a logical clock, as parent R38 requires.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `docs/architecture/2026-09-10-virtual-device-direction.md`, the fabric
      README, the netsim README, and the netsimtest README land with their code.
- [ ] `Run` returns a `RunResult`, and no caller needs `Err()` to tell a budget
      stop from a drained queue.
- [ ] Every journey has exactly one non-empty `State` and a non-empty
      `Origin.Kind`, with `JourneyPending` the only nonterminal state.
- [ ] A periodic protocol wake cannot produce `StopConverged` on a fabric that
      is still changing, including one whose MSTI or per-VLAN tree is changing
      while its CIST is not, and cannot prevent it on one that is not.
- [ ] Every scenario terminates: `Validate` refuses a non-positive budget, and
      `Window` 0 means no convergence detection rather than no stop.
- [ ] `Scenario`, `Action`, `Record`, and every field of them appear in
      `Validate`, `Normalize`, `Clone`, and `DiffScenarios`, with a test that
      fails if any `Diff` arm is removed.
- [ ] A field added to `Snapshot`, `Device`, or `stp.PortInfo` fails the
      fingerprint classification walk until it is classified.
- [ ] Replay stays an in-memory library boundary: no file reader, no capture
      parser, no persistence.
- [ ] This plan's `status` is set with an outcome note under its title.
- [ ] No plan labels appear in code, comments, or commit messages.

## Open questions

1. **Does a `Scenario` belong in `Fabric.Fork`'s field table?** Parent U5
   classifies every `Fabric` field as `immutableShared`, `deepCopied`, or
   `resetOnFork`, and fails the walk on a field with no entry
   (`2026-09-12-1339-feat-netsim-analysis-completeness-phase5-plan.md:331-335`).
   This phase stores no scenario on the fabric — `RunScenario` takes one and
   returns it inside `RunResult` — so there is nothing to classify. If the
   implementer finds a reason to keep the last scenario on the fabric, U5's table
   gains an entry and a mutation probe, and this plan did not budget for either.
2. **What does a frame held at the run boundary count as in
   `PendingWork`?** This plan counts it in `Pending.Journeys` and not in
   `Pending.Arrivals`, because it sits in a routing layer's hold queue rather
   than the fabric's arrival queue. Whether `RunScenario` should refuse to
   report `StopConverged` while any journey is `JourneyPending` is a judgement
   the implementer should make against the first real case: this plan says it
   should, because a held frame is unfinished work the fingerprint cannot see,
   but no test here distinguishes it from the alternative.
3. **Does oscillation detection need a period bound above 2?** R30's acceptance
   example is a two-cycle. This plan allows periods up to the window, which
   costs a quadratic scan over at most `2*window` entries. If the window is ever
   set large by a caller, that cost is unbounded by anything this plan declares.
4. **Regression test for reflection through a policed or mirrored switch
   (follow-up from review).** The review fix classified a reflector-originated
   copy as `OriginMirror` for provenance, and the round-two review found that
   the two SPAN behaviors keyed on `Origin.Kind == OriginMirror` — the ingress
   policing skip and the downstream-copy suppression in `run.go` — then applied
   to reflections too. The fix re-keys both on a non-empty `Origin.Mirror`, so a
   reflection is policed and mirrorable while a SPAN copy is not; it is covered
   by mechanism and by the existing reflector/mirror/policer tests passing, but
   no test yet cables a reflector to a switch port carrying a mirror session or
   a policer and asserts the reflected frame is copied or dropped. That topology
   test is worth adding to make the property executable.

## Where the parent plan's U6 description missed the landed tree

- The parent's `Change` line says "versioned replay identity" and the previous
  draft of this phase expanded it to "simulator contract version and rule-set
  identity". There is no rule set to identify: `trace.RuleID` is producer-owned
  with no central registry, by the direction record's own decision (`:545-551`),
  and the only version constant under `src/common/netsim` is the corpus schema
  version (`internal/netsimtest/corpus.go:21`). This plan carries a contract
  version and the construction specification instead.
- The parent's R27 requires "a deterministic seed". Nothing in netsim is random,
  and no `math/rand` use exists in `fabric`. A seed would be a field nothing
  reads. This plan drops it and records why; the parent's requirement text is
  the thing that is wrong, not the tree.
- The parent's `Tests` line asks U6 for "clone-isolation tests". Clone isolation
  is parent R26, which U5 owns. What is left for this phase is isolation of the
  state this phase adds, which is the scenario and the replay specification.
- The parent cites `fabric.Run` at `run.go:903`; it is at `run.go:959` now. The
  claim itself holds: `Run` returns a bare `int`.
- The parent names "`fabric.Report`" as though it were a type. It is a method
  returning `[]Journey` (`journey.go:108`); there is no report type, and this
  phase does not add one, because `RunResult` is the envelope the parent's R29
  actually describes.
- The parent did not anticipate that a corpus case now costs a change to
  `TestRegistryDeterministicOrdering`'s count literal and its per-class ID list
  (`corpus_test.go:575-649`). Phase 4b's plan flagged it and left the naming to
  this phase; U6 above names it.
- Phase 4b is only partly landed on this branch: `src/common/net/arp`,
  `src/common/net/ndp`, and the routing layer's neighbor lifecycle and hold
  queue are in (`aeafc88c`, merged at `53d3e9e4`), but `trace.Held`,
  `Emission.Protocol`, the switch and fabric wiring, and 4b's corpus case are
  not. This plan is written as though they will be, because 4b is this phase's
  transitive prerequisite through parent U5.
