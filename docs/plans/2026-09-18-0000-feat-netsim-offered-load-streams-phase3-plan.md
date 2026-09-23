---
title: Offered-Load Streams Phase 3 - Stream Package and the Pull Loop - Plan
type: feat
date: 2026-09-18
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-plan.md
---

# Offered-Load Streams Phase 3 - Stream Package and the Pull Loop - Plan

> Re-planned 2026-09-23 against the tree that holds phases 1 and 2.

## Goal

A caller attaches streams to a fabric and reads per-stream results. The means:
`src/common/netsim/stream` holding the spec, the field variations, SplitMix64,
and a `Source` iterator; `Fabric.Attach`; and a pull in `Step` that injects every
source frame due at or before the earliest queued arrival. The plan is wrong if
a pulled stream cannot reproduce the same frames injected eagerly, requirement
11, because the two orders then give two different answers and only one can be
documented as the model.

## Decisions

- The parent's decisions and the offered-load direction record apply. The
  direction record is `proposed-direction`; this phase lands its stream bullets
  (a stream as a plain value, a source the run pulls, SplitMix64 with a spec
  seed, and the corpus load cases). It promotes nothing new, so it adds no
  direction record of its own.
- One wire-octet figure serves both packages. `ethernet.Frame.WireOctets() int`
  is `max(len(Encode()), 60+4*len(Tags)) + 24`, exactly the body of `fabric`'s
  `wireOctets` (`src/common/netsim/fabric/run.go:127-136`). Why: `stream` may
  not import `fabric` (`docs/architecture/2026-09-18-offered-load-streams-direction.md:114`),
  yet a bits-per-second stream must convert the same 84-to-1542 figure the run
  charges, and requirement 11 fails if the two differ. It moves as a method,
  matching the frame-property accessors `Frame.Priority` and `Frame.OuterVID`
  (`src/common/net/ethernet/ethernet.go:208`, `:230`).
- `stream` imports only `src/common/net` (`ethernet`, `netaddr`, `vlan`, `ip`,
  `udp`) and nothing under `netsim`; `fabric` imports `stream`. Why: the same
  value runs on an edge transmitter with no simulator (`direction:84-87`), and
  the direction fixes the edge.
- A stream's rate is frames per second or bits per second **of wire octets**,
  never both and never neither. The interval is exact integer nanoseconds, `n`
  frames' offset computed as one 128-bit multiply-divide, `(n*8*WireOctets*1e9)/rate`
  for a bit rate or `(n*1e9)/rate` for a frame rate, so a million frames
  accumulate no drift, the property `rateInterval` has
  (`src/common/netsim/fabric/run.go:144-163`). Why: line rate is a wire figure,
  and a stream at 100% of 1 Gbit/s must fit exactly.
- A burst is `Burst` frames at the interval then `Gap` before the next burst:
  frame `n` is at `(n/Burst)*(Burst*interval + Gap) + (n%Burst)*interval`, with
  `Burst` defaulting to 1 and `Gap` defaulting to 0. Why: `Gap` is the idle
  between bursts a shaper expresses; the parent's "ten frames back to back at
  line spacing and then the gap" is a bits-per-second stream whose interval is
  the line's own serialization, because the source is link-agnostic and cannot
  know a negotiated rate the fabric owns.
- The stream's end is a frame count or a duration, exactly one. `Normalize`
  fills `Count` from `Duration` as `ceil(Duration/interval)`; `Validate` refuses
  both zero and both set. Why: the direction names both and a stream with no end
  would only ever stop on the run's budget.
- `Source` is an interface, not a spec:
  ```go
  type Source interface {
      Next() (at time.Duration, frame ethernet.Frame, ok bool)
      Clone() Source
  }
  ```
  `at` is the frame's offset **from the stream's start**, never an absolute
  time, so a wall-clock transmitter and the simulated clock each add their own
  epoch: the direction's reason for the edge package is that the same value
  "has to be executable by an edge transmitter that has no simulator in its
  process" (`docs/architecture/2026-09-18-offered-load-streams-direction.md:45-46`).
  `Clone` is what lets `Fabric.Fork` copy a
  half-pulled source; without it a fork would silently drop the attachment,
  the failure the third-kind solution names
  (`docs/solutions/architecture-patterns/a-third-kind-joins-a-two-kind-system-silently.md`).
  `Spec.Source() (Source, error)` returns an implementation over a normalized
  spec, and `Spec.Start` is the offset a caller gives `Attachment.Start`; the
  source does not include it, so one `Source` can be retimed.
- `Spec.Start` is an offset from the consumer's epoch; `Attachment.Start` is an
  offset from `Fabric.Config.Start`; a frame's absolute time is
  `Config.Start + Attachment.Start + Next.at`. Why: requirement 9's `t0` is the
  fabric's start, phase 4 attaches "at `h1` with start `t0`"
  (`docs/plans/2026-09-18-0000-feat-netsim-offered-load-streams-phase4-plan.md`,
  requirement 15), and `Inject.At` is absolute, so the placement is explicit
  while the source stays epoch-free.
- SplitMix64 lives in the package. The reference is Vigna's
  `https://prng.di.unimi.it/splitmix64.c`: state incremented by
  `0x9e3779b97f4a7c15`, then `z ^= z>>30; z *= 0xbf58476d1ce4e5b9;
  z ^= z>>27; z *= 0x94d049bb133111eb; return z ^ (z>>31)`. The reference source
  publishes no test vectors, so the re-plan states that and pins the vectors
  under Requirement 10, computed from the reference algorithm and cross-checked
  against the widely cited first output for seed 0, `0xe220a8397b1dcdaf`. Go's
  standard library ships no SplitMix64 (`grep` of Go 1.27.1's `src` finds
  neither constant), so the package owns it.
- A variation is a deterministic function of the frame index and, in draw mode,
  the spec's seed: `Variation` writes one field of the frame. Concrete
  variations are a MAC field increment/decrement or draw, a frame size from a
  list, and a UDP port step or draw. Why: requirement 10 needs step and draw,
  and requirement 12 needs the size sweep. An L3/L4 field is regenerated
  through `udp.Encode` and `ip.Header.Encode`, which recompute the RFC 768
  pseudo-header checksum and the RFC 1071 IPv4 header checksum by construction
  (`src/common/net/udp/udp.go:72-108`, `src/common/net/ip/ip.go:205-279`); no
  variation patches bytes in place, so a checksum can never go stale.
- A pulled frame takes the same path a caller's `Inject` takes, one call per
  frame, so its journey, counters, drop reasons, and flow fold are identical to
  an eager injection. Validation is repeated per frame rather than hoisted; the
  scale contract is phase 1's and phase 3 adds no per-frame state. Why: this is
  what makes requirement 11 true by construction instead of by coincidence.
- The pull is one-frame-lookahead and runs at the top of `Step`:
  1. each attachment holds at most one peeked frame;
  2. inject every peeked frame whose absolute time is **not after** the earliest
     queued arrival, in attachment order then frame order, refilling the peek
     after each;
  3. when the queue is empty, inject the earliest peeked frame and continue, so
     an attached stream is enough to step a run with nothing else queued.
  Why attachment order: requirement 11's eager baseline injects in time order
  with the same tie-break, and `compareArrival`'s tie-break is `Seq`
  (`src/common/netsim/fabric/queue.go:41-56`), which both orders must assign
  alike.
- `Run`/`runWithActions` pulls before its empty-queue and action checks, so a
  streamed run does not end at `StopQueueDrained` while a source has frames; the
  convergence condition also requires no attachment has a frame left, or a
  long-idle stream behind periodic wakes would let `StopConverged` fire with the
  stream unfinished. Why not both: the action ordering uses `pendingActions[0].At`
  against `f.queue[0].At`, and a source frame pulled past an action's time would
  reorder them. Combining `Attach` with scenario actions is out of scope.
- Each attachment names a nonzero `Flow`; its frames use `RetainAggregate`
  unless the attachment asks for `RetainJourney`. Why: the direction's
  millions-of-frames figure needs journeys freed, and the corpus cases and the
  requirement 11 test need journeys kept; one knob serves both.
- A host origin whose link is `Down` ends its stream: the pull drops the
  attachment before injecting. An `Unknown` link injects, and `Inject` records
  `EntryUnresolved` per frame as it already does (`run.go:327-340`); a
  switch-port origin is not link-checked, because `Inject` queues it directly
  and the forwarding path decides. Why not a third endpoint kind: a stream is
  frames the fabric injects on the caller's behalf, and the host stays passive.
  It does not react to an arriving frame the way the reflector does, so the
  local network analysis record's "adding a third that reacts is a new decision
  against this record" (`docs/architecture/2026-09-16-local-network-analysis-direction.md:106-107`)
  is not triggered and that record is not amended.
- `Fabric.Fork` clones each attachment's `Source` through `Source.Clone` and
  shares the immutable `Origin`, `Flow`, and `Retention`, so a fork continues
  the same pull position. Why: `Compare` and `search` fork fabrics
  (`fabric/README.md` § Internal fork lifecycle), and a fork without the
  attachment would compare two different runs.
- The three corpus load cases attach small streams with `RetainJourney` and
  return one decisive journey, so the case's exact ordered trace is the frame
  the load decided: the tail-dropped frame, the frame whose metadata carries
  `queue-buffer-unstated`, or the policed frame. Why not `RetainAggregate`: the
  corpus admission bar needs ordered steps, and a freed journey has none.

## Requirements

Numbers are the parent's; letters are this phase's acceptance examples.

9. A stream emits frames at its stated rate, burst, count, and start time.
   9a. Plain rate. A `Spec` whose `Frame` is untagged with a 64-octet payload,
   `Rate{FramesPerSecond: 10000}`, `Burst: 1`, `Gap: 0`, `Count: 1000`,
   `Start: 0`, attached at `h1` with `Start: 0` on a fabric whose
   `Config.Start` is `t0`, injects at `t0 + n*100µs`; the source's 1000th offset
   is `99.9ms` and its 1001st `Next` returns `ok == false`.
   9b. Burst and gap. A bits-per-second stream at 1 Gbit/s of untagged
   64-octet-payload frames (`84` wire octets, so `672ns` interval),
   `Burst: 10`, `Gap: 1ms`, `Count: 20` yields offsets `0, 672ns, …, 6048ns`
   for the first ten frames, `1.00672ms` for the eleventh, and
   `1.00672ms + 9*672ns` for the twentieth.
10. A field variation steps or draws deterministically; SplitMix64 matches its
    published vectors.
    10a. MAC step. A destination-MAC variation over base `02:00:00:00:00:00`,
    step `1`, count `256` yields 256 pairwise-distinct addresses on frames
    0 through 255, and frame 256 repeats frame 0's address.
    10b. Determinism. Two `Source` values built from one `Spec` carrying a seed
    and a draw variation yield frames whose `Encode()` bytes are equal at every
    index.
    10c. Vectors. SplitMix64 seeded 0 returns, in order,
    `0xe220a8397b1dcdaf`, `0x6e789e6aa1b965f4`, `0x06c45d188009454f`,
    `0xf88bb8a8724c81ec`, `0x1b39896a51a8749b`; seeded 1 returns
    `0x910a2dec89025cc1`, `0xbeeb8da1658eec67`, `0xf893a2eefb32555e`,
    `0x71c18690ee42c90b`, `0x71bb54d8d101b5b9`.
    10d. Checksum. A UDP destination-port variation stepping from 1000 by 1
    produces frames whose every `udp.Decode` then `udp.Verify` accepts, and
    whose consecutive UDP checksums differ.
11. Four streams across a LAG trunk, attached, give the same `Flows()` and the
    same per-host delivery times as the same frames injected eagerly in time
    order before the first step.
    11a. Equivalence. The `newLagTopology` fabric
    (`src/common/netsim/fabric/lag_test.go:20-88`), VLAN 10 onboard, four
    streams from `h1`, each a distinct source MAC with 40 untagged frames at
    1000 frames per second and `RetainJourney`, appended as flows 1 through 4.
    Eager: `Inject` every frame sorted by (`At`, stream) before `Run`. Lazy:
    `Attach` the four `Source` values in the same stream order and `Run`. Both
    runs give equal `Flows()` maps and equal per-flow sorted delivery
    timestamps from `Report()`.
12. A size variation over `64, 128, 256, 512, 1024, 1280, 1518`, the sizes of
    RFC 2544 section 9.1 (https://www.rfc-editor.org/rfc/rfc2544.txt), yields
    frames whose encoded length plus FCS equals each size in turn.
    12a. Sizes. An untagged size variation over those seven values produces, at
    index `n`, a frame whose `len(frame.Encode()) + 4` equals the `n mod 7`th
    size: `46, 110, 238, 494, 1006, 1262, 1500` payload octets.
13. The corpus admits three cases: an oversubscribed trunk with a stated
    buffer, the same with none, and a policed stream.
    13a. Stated buffer. `planning/oversubscribed-trunk-stated-buffer`: a stream
    oversubscribes a trunk whose `Queues` states a buffer; the decisive journey
    is the tail-dropped frame, outcome `Dropped`, reason `queue-full`, carrying
    the `traffic.queue.drop` step with a `traffic.queue_decision` fact and
    `Complete` metadata.
    13b. No buffer. `planning/oversubscribed-trunk-unstated-buffer`: the same
    trunk with no `Queues` entry; no frame drops, and the decisive journey's
    metadata holds `queue-buffer-unstated` at `Incomplete` on the backed-up
    port's scope.
    13c. Policed. `planning/policed-stream`: a stream into a port with a
    `traffic.Policer`; the decisive journey, outcome `Dropped`, reason
    `policed`, carries the `traffic.policer.refuse` step with a
    `traffic.policer_decision` fact and `Complete` metadata.

## Out of scope

- Combining `Attach` with `RunScenario` actions. The pull's placement rule and
  the action loop's ordering rule disagree on a frame due past a pending
  action, and nothing here needs both.
- A pcap source, and the capture adapter (phase 4).
- The on-wire transmitter and the lab comparison (phase 5).
- A `flowseer/netsim/v1` schema for streams; a stream is a Go value until a
  service carries a scenario.
- TCP, stateful streams, congestion control, and retransmission.
- Source retiming across a fork beyond `Source.Clone`; a fork continues the
  same pull position and no more.

## Units

### U1. One wire-octet figure in `ethernet`
Files: `src/common/net/ethernet/ethernet.go`,
`src/common/net/ethernet/ethernet_test.go`,
`src/common/netsim/fabric/run.go`
After: none
Change: `ethernet` gains `Frame.WireOctets() int`, returning
`max(len(raw), 60+4*len(f.Tags)) + 24` over `raw, _ := f.Encode()`, the exact
body of `fabric.wireOctets` (`run.go:127-136`) with its doc comment stating the
24 is preamble, start delimiter, interpacket gap, and FCS. `fabric` deletes
`wireOctets` and reads `arr.Frame.WireOctets()` in `serialization`
(`run.go:141`) and `Step` (`run.go:505`); `serialization` still takes
`ethernet.Frame`.
Tests: `ethernet_test.go` gains a table: an untagged 64-octet-payload frame is
84; a tagged frame whose `Encode` is shorter than `60+4*len(Tags)` takes the
pad; a 1518-octet frame is 1542; a frame whose `Encode` errors still pads (the
frame-accessor contract is that `Encode`'s error is the caller's; `WireOctets`
ignores it as `wireOctets` did), pinned as a case.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/ethernet src/common/netsim/fabric`

### U2. The stream spec, SplitMix64, and the source iterator
Files: `src/common/netsim/stream/doc.go`,
`src/common/netsim/stream/stream.go`,
`src/common/netsim/stream/source.go`,
`src/common/netsim/stream/splitmix.go`,
`src/common/netsim/stream/stream_test.go`,
`src/common/netsim/stream/source_test.go`,
`src/common/netsim/stream/splitmix_test.go`,
`src/common/netsim/stream/README.md`,
`src/common/netsim/README.md`
After: U1
Change: the package defines `Rate{FramesPerSecond, BitsPerSecond uint64}`,
`Spec{Frame ethernet.Frame; Rate Rate; Burst int; Gap time.Duration; Count int;
Duration time.Duration; Start time.Duration; Seed uint64}`, `Normalize`,
`Validate`, `Clone` (a value copy; the `Frame` is shared as immutable, per the
direction record), and `Spec.Source() (Source, error)` over a normalized spec.
The source's `Next` yields the burst/gap offsets above for `n < Count`, `at`
relative to the stream's start, and `ok == false` after;
`Clone` returns an iterator at the same cursor. `SplitMix64` is a struct with
`NewSplitMix64(seed uint64) SplitMix64` and `Next() uint64` over the reference
algorithm. `src/common/netsim/README.md` gains a `stream` row in its package
table, and the new README states the package's boundary (imports `src/common/net`
only).
Tests: `splitmix_test.go` with 10c; `stream_test.go` with a `Validate` table
(refuses both or neither rate, negative `Gap` or `Burst`, both or neither of
`Count`/`Duration`, a frame whose `Encode` errors), `Normalize` (Burst 0 to 1,
`Duration` to `Count` by `ceil`), and the offset sequence `n*100µs` for a
frames-per-second spec (9a's source half); `source_test.go` with 9b, a
nondecreasing-offsets case, an exhaustion case, and a `Clone` case where
advancing one clone leaves the other's next frame unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/stream src/common/netsim`

### U3. Field variations
Files: `src/common/netsim/stream/variation.go`,
`src/common/netsim/stream/variation_test.go`,
`src/common/netsim/stream/stream.go`,
`src/common/netsim/stream/source.go`,
`src/common/netsim/stream/README.md`
After: U2
Change: `Spec` gains `Variations []Variation`, carried by
`Normalize`/`Validate`/`Clone` (a deep copy of the slice). `Variation` is
`Validate() error` plus `Apply(n int, frame ethernet.Frame, rng *SplitMix64)
ethernet.Frame`; `Spec.Validate` runs every variation's `Validate`, so
`Spec.Source()` returns the error and `Next` stays `ok`-only. The source applies
the variations in order after the template, so a later variation sees an earlier
one's frame. `MACVariation` (`Field` naming destination or source, `Step int64`,
`Count int`, `Draw bool`) adds `(n mod Count)*Step` to the 48-bit big-endian
address and wraps at `Count`; `SizeVariation` (`Sizes []int`) sets the payload
length to `size - 18 - 4*len(frame.Tags)` so `Encode` plus FCS is the stated
size; `UDPPortVariation` (`Dst bool`, `Step int64`, `Count int`, `Draw bool`)
decodes the payload with `ip.Decode` and `udp.Decode`, steps the port, and
re-encodes with `udp.Encode` then `ip.Header.Encode`, so both checksums are
recomputed. A `Draw` mode takes the offset from `rng.Next()`. The README
documents the three variations and states that an L3/L4 change goes back through
the encoders, never a byte patch.
Tests: `variation_test.go` with 10a, 10b, 10d, 12a, a `Draw` mode case that two
seeded sources agree on, a `SizeVariation` case with a tagged template where the
tag's four octets are subtracted, and a `Validate` refusal case (a
`UDPPortVariation` whose template payload is not IP/UDP).
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/stream`

### U4. Attach and the pull loop
Files: `src/common/netsim/fabric/attach.go`,
`src/common/netsim/fabric/attach_test.go`,
`src/common/netsim/fabric/attach_internal_test.go`,
`src/common/netsim/fabric/run.go`,
`src/common/netsim/fabric/fabric.go`,
`src/common/netsim/fabric/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`
After: U1, U2
Change: `fabric` declares
`Attachment{Origin Endpoint; Source stream.Source; Start time.Duration;
Flow FlowID; Retention Retention}` and `Fabric.Attach(att Attachment) error`,
mirroring `Inject`'s origin, retention, and flow refusals (`run.go:199-208`,
`:218-292`) plus a nil source and a start before the clock once the run has
started. `Fabric` carries `attachments []attachedSource`, where
`attachedSource` holds the attachment, a peeked frame, and an `ended` flag;
`Fork` clones it through `Source.Clone`. `pullSources()` implements the pull rule
above: it peeks each source once, injects due frames in attachment then frame order
through `Inject`, ends a host-origin source whose link is `Down`, and refills
peeks; `Step` calls it before its empty check and `runWithActions` calls it
before its empty-queue and convergence checks, and the convergence predicate
gains `!f.sourcesPending()`. The README gains an Attached streams section
stating the pull rule, the epoch arithmetic, link-down ending, the flow and
retention, and that `Fork` continues the pull. The virtual device record's run
bullet (`2026-09-10-virtual-device-direction.md:71-107`) is rewritten to state
that a run also pulls frames from attached sources before each step; the cable
bullet's "Nothing in a run is random, so a run reproduces" (`:111-113`) becomes
"a run is a function of its configuration, its injections and streams, and their
seeds"; and the scenario sentence's "and no pseudo-random seed" (`:359-361`)
becomes accurate for a scenario that names streams.
Tests: `attach_test.go` (package `fabric_test`) with 9a's absolute placement
(`t0 + n*100µs`), 11a, the attach refusals, a host source whose link is `Down`
ends after the frames due before the fault, an `Unknown` link produces
`EntryUnresolved` per frame, and a one-stream flow equal to an eager injection's
`Flows()`; `attach_internal_test.go` (package `fabric`) with the pull ordering
(a source frame due at the earliest arrival's instant is injected before that
step; one due after is not), the queue-empty pull injecting one frame, a
convergence check that does not fire while a source has frames behind periodic
wakes, and a mid-run `Fork` whose two forks yield the same remaining frames.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric docs/architecture/2026-09-10-virtual-device-direction.md`

### U5. Conformance corpus load cases
Files: `src/common/netsim/internal/netsimtest/load_cases.go`,
`src/common/netsim/internal/netsimtest/load_cases_test.go`,
`src/common/netsim/internal/netsimtest/cases.go`,
`src/common/netsim/internal/netsimtest/README.md`
After: U3, U4
Change: three `Case` values, each a `fabric.ConstructionSpec` with one switch
and two hosts, a `stream.Spec`, and a `Fabric.Attach` with `RetainJourney`;
`Execute` selects the decisive journey (the frame with a `queue-full` drop, the
first frame whose metadata carries `queue-buffer-unstated`, or the frame with a
`policed` drop), verifies `Flows()` consistency, and returns it with its ordered
steps and metadata. `DefaultRegistry` registers the three; the README's case
table gains their rows. Trace rules are `traffic.RuleQueueDrop` with
`traffic.QueueDropFact` for 13a, `traffic.RulePolicerRefuse` with
`traffic.PolicerDecisionFact` for 13c; 13b's decisive element is a delivered
frame with `IssueQueueBufferUnstated` on `analysis.PortScope`.
Tests: `load_cases_test.go` runs each case through the corpus's own execution
and expectation checks and fails on a mismatch; the corpus `ValidateCase` bar
(`src/common/netsim/internal/netsimtest/corpus.go:536-648`) is satisfied by
non-empty rules, subjects, facts, and steps.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/internal/netsimtest`

Waves: U1 | U2 | U3 U4 | U5

The wider cut is available because U3 and U4 touch disjoint files: U3 edits only
`stream`, U4 edits only `fabric` and the direction record. U2 chains after U1
because it calls `Frame.WireOctets`; U3 chains after U2 because it edits the same
files; U4 chains after U1 and U2 because it edits `run.go` and imports `stream`.
U5 needs both U3's variations and U4's `Attach`.

## Verification

```bash
go test -race ./src/common/net/... ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- \
  src/common/net/ethernet src/common/netsim/stream src/common/netsim/fabric \
  src/common/netsim/internal/netsimtest docs/architecture/2026-09-10-virtual-device-direction.md
```

The phase-1 million-frame benchmark is optional here: phase 3 adds no per-frame
state and keeps the same retention fold, and U1 only moves the wire-octet call
behind a method. Run it when a changed path is suspected on the hot path. No lab
check: the phase-5 lab comparison is where a real queue and policer are checked,
and it needs the owner's approval per run.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `stream` and `fabric` READMEs and the `netsim` package table updated, and
      the direction record's run bullet, randomness sentence, and scenario seed
      sentence rewritten in the change that makes them false.
- [ ] This plan's `status` set with an outcome note under its title, and the
      parent's `Landed:` line for U3 filled.
- [ ] No plan labels in code.

## Open questions

- The reference SplitMix64 source publishes no vectors; the five per seed under
  Requirement 10c are computed from the reference algorithm and cross-checked
  against the first output for seed 0. The implementer confirms the vectors
  against a second independent implementation before treating them as fixed.
- The parent's "ten frames back to back at line spacing" is read as the
  interval at the stream's own bit rate, because the source cannot know the
  fabric's negotiated rate. If the intended burst is instead paced by
  the fabric's link rate, that is a wire-shape change the implementer raises
  before U3 rather than coding around.
- The exact canonical fact strings and ordered steps for the three corpus cases
  come from the producers; the implementer records them from a run and pins
  them, per the corpus admission bar.
