---
title: Simulation Follow-Ups - Plan
type: refactor
date: 2026-10-10
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Simulation Follow-Ups - Plan

## Goal

Resolve the simulation obligations that bounded corpus, transmitter, and
size close-out leaves open. This standalone plan has no parent and needs
decisions before implementation. Its inventory preserves acceptance gaps
and source qualifications. No residual fix is implemented by writing it.

## Decisions

- Detailed reporting is settled by the
  [virtual-device record](../architecture/2026-09-10-virtual-device-direction.md#reporting-granularity).
  Members need their own records, distinct causes need distinct reasons,
  and locations must be named. The unresolved choice is the runtime design.
- Passively learned stale neighbors are unbounded. No configured capacity
  with eviction or inactivity expiry is selected. Negative measured latency
  remains malformed. Neither fixed policy is changed here.
- Keep landed history, including labels in `b6cde757` and `b3e894c4`.
  No history rewrite or new label in code, comments, or commits is authorized.
- Preserve standards and mode limits individually. An unread source stays
  unverified. A README or a passing corpus case is not independent evidence.
- The ICX7150 check remains manual and outside the unit graph. No unit or
  parent completion waits for it. No live traffic is authorized here.

## Requirements

Every residual row below survives with its deciding source, acceptance gap,
and qualification. Repeated carries retain their primary-entry references.
The historical dispositions quote the close-out inventory. References to its
units describe that inventory's ownership, not executable units in this plan.
The corpus validation and unsupported allocation-number rows are resolved
close-out evidence. They remain visible so all eighteen retirement items can
be compared. No new defect or standard-conformance claim is inferred from a
historical row.

### Partial parent obligations and retirement

Inventory accounting does not fulfill parent R5 while live defects remain.
Unread standards and unresolved behavior leave R6 incomplete. Independent
known-byte source gaps leave R7 incomplete. C168 still misses the 2 GiB
cumulative allocation target. Historical labels leave the parent's no-label
condition unmet. The substantive parent outcome remains partial.

Retire the finished phase and then its computed-complete parent through
[land](../../.claude/skills/land/SKILL.md). Computed status reflects finished
phases rather than fulfilled substantive obligations
([plans.py](../../tools/scripts/lib/plans.py)). Preserve the command's printed
retirement lines. Add the explicit partial substantive outcome, unmet
requirements, and `docs/plans/2026-10-10-1624-refactor-simulation-followups-plan.md`
to the parent retirement commit
([retire-plan.md](../../.claude/skills/land/references/retire-plan.md)).
This follow-up plan survives that retirement. No parent edit or policy
change is part of this work.

### Residual scope index

The scope index retains the close-out group names and qualifications. Its
unit labels refer to that source document.

| Leftover | Contents copied by U22 |
| --- | --- |
| Fabric runtime, comparison, construction and ownership | C001-C005, C007-C012, C014-C018, including every subitem and present source. |
| Switch runtime, traces, cleanup, API comments and tests | C019-C020, C022-C024, C037, C039-C041, C058-C059. C040 cross-references C024. |
| Stream validation and its documentation | C031-C032. External language claims stay unverified until read. |
| STP discriminating tests | C043-C056. Preserve source and mutation limits individually. |
| Multicast/routing cleanup and hardening | C060-C063, C065. C062 states: passively learned stale neighbors are unbounded. No capacity or expiry is selected. |
| Aggregate allocation target | C168 and fabric/README.md:415-421. Measured allocation exceeds 2 GiB. No performance remedy is selected. |
| Historical labels and standards/mode limits | C057, C067-C085, C179-C180, C187, C189. Keep labels in history and each unread-source qualification. No history rewrite or standards result is claimed. |
| Detailed reporting | G001-G004 in the granularity inventory below, with present sources and the reason each needs runtime work outside this graph. |
| Owner-run evidence | C169. ICX7150 remains manual, blocks no unit, and parent completion does not await it. Copy the command, prerequisites and evidence criteria below. |

The relocated original inventory also retains this allocation obligation:

| Item | Current source | Disposition |
| --- | --- | --- |
| Aggregate scale memory target missed | S/fabric/README.md:415-421 | Separately scoped leftover. Existing million-frame figures meet 120 s and exceed 2 GiB cumulative allocation. No improvement is claimed. |

### Detailed reporting inventory

`S` means `src/common/sim`, `C` and `ST` mean its `internal/simtest`, and `E`
means `src/edge/simload`. Source coordinates below preserve the close-out
snapshot. Relocate by enclosing declaration before implementing.

| Item | Current coarser reading | Required follow-up |
| --- | --- | --- |
| G001 Whole-state differences | S/device/state.go:329-337 serializes the complete top-level field. C001 is already deferred. | U22 carries member-level differences with named row/field locations into the follow-up plan. New runtime/API work needs its own unit and unequal-row tests. No open choice between snapshots and member records remains. |
| G002 Queue and drop locations/reasons | C003/C004/C019/C020 at traffic/layer.go:101, vswitch/timers.go:190, route.go:288 and intercept.go:178 lose member, no-member outcome or precise producer/cause. | U22 carries distinct cause and actual member/location requirements. Keep configured-owner lookup separately. These switch/fabric output changes exceed corpus and transmitter closure, so they remain new runtime work. The detail preference is settled. |
| G003 Aggregate traversal retention | S/fabric/flow.go:128-151 frees settled aggregate/protocol journeys and :153-199 retains flow totals. The virtual-device record:91-110 and offered-load record:84-97 describe this coarser output. | U22 records the obligation to report every frame's path, drop location and reason under these retentions. A per-frame export/retention mechanism, ownership and memory measurements are new fabric work, coupled to C168. A totals-only result does not meet the parent's detail obligation. Preserve the fixed pure flow move and partial outcome. |
| G004 Candidate lookup snapshots | S/layer/routing/fact.go:39-74 puts the chosen route and candidate set in one fact. U18 changes address spelling only. | U22 carries per-candidate records with stable identities and explicit selection/rejection reasons. New trace shape, matching expectations and all callers need a runtime unit. The move and text-only unit cannot absorb it without changing their contracts. |

### Fabric retirement inventory

Source for every row: retirement commit `ef213026`, body, Review follow-ups.
`S` is `src/common/sim`. These were explicitly left open after the review
verdict. The validation and allocation-documentation close-out items become U15 and U20. The other items stay
named residual work under the separately scoped leftovers. No deferred fix is claimed done.

| Item | Source | Disposition and reason |
| --- | --- | --- |
| State.Diff top-level current/expected fields | S/device/state.go:337, `ef213026` gap | Deferred. Device state projection is outside corpus/transmitter closure. Parent outcome remains partial. |
| Past-dated egress head and same-instant higher PCP | S/fabric/egress.go:123, gap | Deferred. Event ordering needs a fabric regression and runtime remedy. U17 changes capture shutdown only. |
| LAG queue step subject owner | S/layer/traffic/layer.go:101, gap | Deferred runtime work. G002 derives actual member attribution from the parent, preserving configured-owner lookup separately. |
| Protocol LAG emission with no member | S/device/vswitch/timers.go:190, gap | Deferred runtime work. G002 requires a distinct no-member record. It needs a switch timer/drop-record unit. |
| Construction start journeys never settled | S/fabric/topology.go:69, gap | Deferred. Replay and start-emission lifetime are runtime work. |
| Corpus validation counting misses omitted call | C/corpus_test.go:1510, gap | U15. Its present test counts successful cases, so source fixtures pin omission separately. |
| Held scans still constrain the final entry | S/fabric/routing_test.go:918, :1067, :1212, gap | Deferred. The scans still assert the last entry at :926, :1075 and :1220. Requires a held journey with a later entry and removal of the obsolete constraint. Corpus assertions do not cover these tests. |
| SelectVLANs row absent for released mirror | S/device/vswitch/mirror_once_test.go:88, gap | Deferred. Selection differs between held ingress VLAN and routed FID 0. Choosing equal semantics is caller-visible. |
| Unreachable copy-origin diff branches | S/fabric/compare_journey.go:51, compare.go, convention | Deferred. Removing or making comparison branches reachable requires a fabric comparison unit and caller audit. |
| Unreachable hand-built supports and DefaultQueue | S/fabric/topology.go:154, device/vswitch/mirror.go, fabric/executor.go, egress.go, output.go, fork.go, queue.go, convention | Deferred. Establishing the supported construction boundary is runtime work, with separate deletion tests. |
| Unused isJourneyHeld and test exports | S/fabric/journey.go:295, export_test.go, convention | Deferred. Dead-code cleanup is separate from the size refactor and corpus gate. Parent residual remains explicit. |
| Four misleading comments | S/fabric/comparison_location.go:28, result.go:56, journey.go:209, routing_test.go:954, convention | Deferred. U20-U22 owns Markdown only. Comment corrections require a code-scoped follow-up with verifier receipts. |
| Per-step allocation figures lack reproduction | S/fabric/README.md:1360, convention | U20-U22. Reproduce the existing command or remove unsupported numbers. |
| Replace rollback and old hold cleanup | S/fabric/topology.go:100, hardening | Deferred. Custom device replacement and error atomicity need an explicit runtime test seam. |
| Node.Snapshot used as switch discriminator | S/fabric/topology.go:143, attach.go, egress.go, metadata.go, hardening | Deferred. A generic-node contract question, outside close-out. |
| AttachStream fallback kind and Emissions[0] | S/fabric/attach.go:49, injection.go:78, hardening | Deferred. Fourth-device support is out of scope. Current accepted seam remains documented. |
| Action.Diff nil Operation, ignored Encode error, fallback pairing | S/fabric/scenario.go:323, align.go, hardening | Deferred. Direct unvalidated-action API behavior needs its own requirement and input-validity decision. |
| Frame sharing documented only in README | S/fabric/injection.go:13, hardening | Deferred. Ownership guarantees on Inject, Step, Snapshot and Device need a code/API documentation unit. U20-U22 preserves the current README claim. |

### Historical residual and repeated carries

P1-P11 and P13 mean the corresponding retired phase plan immediately before
its retirement. P12 means the original inventory at
`51a10ed1:docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase12-plan.md`,
recoverable with `git show 51a10ed1:docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase12-plan.md`. Sources are recoverable with `git show <retire>^:docs/plans/<plan>`.
Retirements: P1 d7ac43ad, P2 a51692c5, P3 e152c4ed, P4 6c4f1915,
P5 492f3488, P6 830736a9, P7 06c14cac, P8 14e4a39b, P9 26705dbe,
P10 ef213026, P11 51a10ed1, P13 cf429b87. `parent` means the open parent
plan at this snapshot, `shape` means the package-shape record, and `ST`
means C. Repeated carries cross-reference their first entry.

This ledger accounts for carries rather than accepting their proposed
remedies. A deferred row remains an unmet parent obligation under
the partial-outcome decision. A source interpretation marked unverified supplies no protocol
requirement or failing-test expectation. U20-U22 preserves that qualification.

| Item | Source | Disposition and present evidence |
| --- | --- | --- |
| C001 State.Diff returns complete top-level fields | P10:1106, ef213026 body:15, P11:498 | Separately scoped leftover. Follow-up evidence, not an authorized unit: State unit. `S/device/state.go:329` calls whole-field serialization. Add unequal-row tests that exclude both the differing row from Current and unrelated rows from Expected, then implement row-level differences. |
| C002 Past-dated egress head hides same-instant priority | P10:1107, ef213026 body:16, P11:499 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Queue unit. `S/fabric/egress.go:123` still requires `At.Equal(now)`. Pin a higher-PCP frame behind an overdue head before changing batching. |
| C003 Queue subject names owner instead of member | P10:1108, ef213026 body:17, P11:500 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Queue unit. `S/layer/traffic/layer.go:101` passes the configuration owner into the decision subject. Keep owner-based lookup and pin the actual member subject for tail-drop and threshold steps. |
| C004 Protocol LAG emission without a member disappears | P10:1109, ef213026 body:18, P11:501 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Switch unit. `S/device/vswitch/timers.go:190` returns false when member selection fails. Add a loop-protection emission case and a recorded no-member outcome. |
| C005 Construction does not settle touched journeys | P10:1110, ef213026 body:19, P11:502 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Topology unit. `S/fabric/topology.go:69` applies start output without the settlement used by replacement at :101. Replay a start emission with no link and settle it consistently. |
| C006 Receive-outcome gate counts cases rather than validations | P10:1111, ef213026 body:20, P11:503 | U15. Corpus gate unit. `ST/corpus_test.go:1512` counts successful AssertCase calls, not validated receives. `ST/loopprotect_cases.go:260` runs and reads a journey without the report validator used at `ST/stp_cases.go:349`. Instrument actual validation coverage and make deleting one executor validation fail. |
| C007 Held assertions still constrain the final entry | P10:1112, ef213026 body:21, P11:504 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Journey test unit. `S/fabric/routing_test.go:918`, :1067 and :1212 scan entries, but :926, :1075 and :1220 still assert on the last entry. Add a held journey with a later entry and remove the obsolete last-entry constraint. |
| C008 Mirror selector matrix omits SelectVLANs | P10:1113, ef213026 body:22, P11:505 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Mirror unit. `S/device/vswitch/mirror_once_test.go:75` has seven selectors and no VLAN selector. Held memory stores ingress VID at `mirror.go:106`, while live routed selection uses the routed result. Pin the differing paths, then make their VLAN-selection rule consistent before adding the row. |
| C009 Unreachable journey comparison branches | P10:1117, ef213026 body:23, P11:506, P8:394, P10:431 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Comparison unit. `S/fabric/compare_journey.go:59` compares origin kind/name after pairing by those values. `S/fabric/compare.go:258` retains the journey half of diffFabricRuns while Compare uses root-based comparison. Reach the branches through the public API or delete dead code. Direct helper tests alone do not establish reachability. |
| C010 Hand-built fabric supports survive | P10:1118, ef213026 body:24, P11:507 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Construction cleanup unit. `S/fabric/topology.go:154` retains DefaultQueue, `S/fabric/egress.go:70` a fallback, and `S/device/vswitch/mirror.go:20` nil-receiver support. Audit executor/output/fork/queue guards individually. Preserve valid zero-ReplaySpec handling at `fabric/executor.go:62` and queue initialization. Remove only supports with no caller. |
| C011 Unused held helper and test exports | P10:1119, ef213026 body:25, P11:508 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Cleanup unit. `S/fabric/journey.go:298` declares isJourneyHeld. `S/fabric/export_test.go:102`, :222 and :241 declare ExportIsJourneyHeld, ExportFabricFields and ExportLinkTrust without readers. Delete these declarations after a reference check. |
| C012 Four misleading comments | P10:1120, ef213026 body:26, P11:509 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Documentation unit. Correct each independently: `S/fabric/comparison_location.go:28` only supports stable numbered-action locations, `result.go:56` must respect fabric concurrency, `journey.go:209` excludes freed settled protocol journeys, and `routing_test.go:954` describes a held journey rather than a released one. |
| C013 Per-step allocation figures lack provenance | P10:1121, ef213026 body:27, P11:510 | U20-U22 documentation audit. Documentation/performance unit. `S/fabric/README.md:1354` gives a benchmark command without benchmem and :1360 asserts 303/294 allocations without a reproducible capture. Record command, platform, revision and output or remove the numbers. Do not manufacture a historical measurement. |
| C014 Replace rollback and hold-key cleanup | P10:1125, ef213026 body:28, P11:511 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Topology unit. `S/fabric/topology.go:100` changes links/trust and applies start/peer output before only restoring the node on error. :226 accepts a supplied Device. Test failed replacement and a successful replacement with no drops, including old hold keys. Make the entire mutation transactional or define an explicitly narrower contract through re-planning. |
| C015 Snapshot doubles as switch classification | P10:1126, ef213026 body:29, P11:512 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Open seam decision. `S/fabric/topology.go:143` documents Snapshot as a projection, but `S/fabric/attach.go:57`, `egress.go:123` and metadata paths classify devices through it. Decide an explicit capability or device contract before implementing non-switch-with-Snapshot and switch-without-Snapshot tests. |
| C016 Fourth device kind and unchecked emissions indexing | P10:1127, ef213026 body:30, P11:513 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Open seam decision. `S/fabric/attach.go:49` falls through to reflector origin and `S/fabric/injection.go:78` indexes the first emission. Define stream-attachment eligibility and emission shape, then test an additional device kind and empty emissions. Do not silently prescribe a new interface here. |
| C017 Unvalidated Action.Diff, encoding errors and source fallback | P10:1128, ef213026 body:31, P11:514 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Scenario unit, all three subitems. `S/fabric/scenario.go:323` calls Operation without validation, :389 ignores Encode errors, and `S/fabric/align.go:43` reconstructs source roots beneath an action. `S/assembly/scenario.go:33` can construct a nil operation from an empty Mcheck port. Pin direct invalid calls, failed encoding, and source-generated root alignment. Define refusal/error behavior in the re-plan. |
| C018 Frame-sharing contract missing on four APIs | P10:1129, ef213026 body:32, P11:515 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Documentation unit. `S/fabric/README.md:1352` states immutable shared buffers. State ownership on `Inject` at `injection.go:13`, `Step` at `run.go:155`, `Snapshot` at `snapshot.go:13`, and `Device` at `topology.go:262`. The fork comment at `fork.go:10` already documents sharing. Distinguish independent containers from shared payload storage. |
| C019 Released mirror refusal has routing rather than traffic trace | P9:1177, 8647d908, P11:496 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Switch unit. `S/device/vswitch/route.go:288` constructs a routing-layer egress drop and :367 routes mirror refusal through it. Pin live/released mirror refusal parity with the traffic decision fact, including a terminal drop rather than a transmit. |
| C020 Untracked up-port BPDU refusal misstates cause/scope | P9:1178, 8647d908, P11:496 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Switch unit. `S/device/vswitch/intercept.go:178` rejects an untracked port with the forwarding result, and early return at :251 avoids decoding. Cover STP cause/scope on the up untracked port and malformed SSTP BadBPDU accounting. The original defect is narrower than generic unknown-port handling. |
| C022 Unused layer age field, time parameter and interface method | P9:1180, 8647d908, P11:496 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Cleanup unit. `S/device/vswitch/layers.go:30` declares timed.Advance while timers use NextWake. :44 keeps age, :49 ignores time, and :76/:80/:88 write age. Remove each unused part without altering the advance callbacks. |
| C023 badBPDU walks trees for a discarded result | P9:1181, 8647d908, P11:496 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Switch cleanup unit. `S/device/vswitch/intercept.go:240` passes nil before into bpduResult at :213. Avoid the unused transition walk on the bad-BPDU path and preserve its error output. |
| C024 Test comment narrates a unit promise | P9:1182, 8647d908, P11:496 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Documentation unit. `S/device/vswitch/stp_test.go:1904` retains the unit-promise wording. Describe the emitted BPDU and trace rule instead. |
| C031 Stream embedding wrapper escapes validation | P8:392 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Stream hardening unit. `S/stream/stream.go:68` has no default type refusal. The carried embedding wrapper should be a failing Validate test before source changes. The Go promotion rule is recorded in the embedding solution. Its external language source was not independently re-read here, so that external claim remains unverified in this report. |
| C032 Variation set described as closed | P8:393 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Documentation unit coupled to the preceding guard. `S/stream/variation.go:15` and `S/stream/README.md` claim closure. An unexported method alone is insufficient according to the recorded source investigation. State the actually enforced concrete-variation set after fixing validation. |
| C037 Phase 9 seven understated API comments | P9:398-401, :1100-1101 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Documentation audit unit. Check package at `S/device/vswitch/config.go:1`, Switch at `switch.go:20`, NextWake at `timers.go:133`, Drain in `output.go`, Diff in `diff.go`, Derive at `derive.go:26`, and Compare at `compare.go:63` against the composed APIs. Retired switch.go line numbers no longer identify these declarations. |
| C039 Five either-Forwarded-or-Flooded assertions | P9:425-426, :1105-1106 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Test hygiene audit. The source was split into `S/device/vswitch/route_test.go`, `lag_test.go`, `mirror_test.go` and `held_test.go`. Pin each original assertion to its observed outcome. Historical line numbers must be mapped by test name before calling all five closed. Execution/mutation verification was not run here. |
| C040 Historical test comments and acceptance_review file | P9:432-434, :1104-1107 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Partly landed. Tests now live in subject files and acceptance_review_test.go is absent. The separate narrative at `S/device/vswitch/stp_test.go:1904` remains and is tracked in the phase 9 follow-up. Audit the other named historical comments rather than treating file deletion as proof for all of them. |
| C041 Three nondeterministic wall-clock fixtures | P9:435-436, :1107-1119 | Separately scoped leftover. Follow-up evidence, not an authorized unit: No longer an automatic repair item. Phase 9 specified fixed instants and a separate monotonic-time representation. Audit `S/device/vswitch/construction_result_test.go` and the moved subject tests for those exact fixtures. Preserve the monotonic-removal test rather than replacing its distinction with identical timestamps. |
| C043 MSTI remaining-hop boundary | P3:1603 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/receive.go:169` still uses <=1. Pin one discarded and two stored through an internal test if public observations cannot distinguish them. Mutation result unverified here. |
| C044 Boundary MSTI inherits CIST agreed | P3:1604 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/receive.go:565` copies the CIST agreement. Pin forwarding of the boundary Designated MSTI using that agreement. Do not infer coverage from a generic MSTP corpus case. |
| C045 Boundary role change clears agreed | P3:1605 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/roles.go:194` contains role-transition agreement clearing. Pin a boundary MSTI role change requiring a fresh agreement. |
| C046 Edge forwarding raises no topology change | P3:1606 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/roles.go:328` treats edge links specially. Add or identify an internal transition assertion that fails when the edge operand is removed. |
| C047 Inactive boundary MSTI ignores topology-change flag | P3:1607 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/receive.go:604` checks mp.tcActive. Pin an inactive MSTI separately from active propagation. |
| C048 Old isSynced boundary branch | P3:1608, P13 handshake work | Separately scoped leftover. Follow-up evidence, not an authorized unit: Historical branch remedy no longer applies directly. `S/layer/stp/roles.go:10` now implements the later handshake decision and boundary scope. Audit the current boundary-unagreed emission test against that implementation, rather than restoring the old branch. |
| C049 Sync request OR operands | P3:1615 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/receive.go:11` owns syncTree. Pin both a cut forwarding proposer and a Discarding port beginning proposal so either operand removal fails. |
| C050 Boundary mirror request guard | P3:1616 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/receive.go:56` mirrors boundary state and `link_state_internal_test.go:654` asserts an information request. Add the already-proposing-and-Discarding negative case with a distinguishable output. |
| C051 stored guard on early CIST election | P3:1617 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/receive.go:530` retains the internal CIST stored guard. Pin an un-stored BPDU whose later recompute differs from early election. |
| C052 Restore prior root information before topology change | P3:1618 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/receive.go:522` saves/reselects information and :592 propagates received TC. Verify a same-root move between ports and an emitted request, including each restore line. |
| C053 Boundary mirror test asserts flag without distinct emission | P3:1619 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/link_state_internal_test.go:654` targets sync request state. Pin a distinct emitted MSTI record or delete an unreachable redundant request. A boolean assertion that another flag masks is insufficient. |
| C054 Agreement test fails to distinguish Alternate vector | P3:1620 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/agreement_test.go:264` is the inherited-vector case. Add an Alternate-ending call that distinguishes role-dependent vector selection. |
| C055 stored guard must cover VLAN and internal MST prior times | P3:1631 | Separately scoped leftover. Follow-up evidence, not an authorized unit: STP test unit. `S/layer/stp/receive.go:574` restores before :592. Pin SSTP new times on the unchanged VLAN root and internal MST new times on unchanged CIST root. Neither restricting stored to CIST nor to external frames may pass. |
| C056 Retired timer-rule paragraph lacks test/mutation names | P3:1632 | Separately scoped leftover. Follow-up evidence, not an authorized unit: No edit to retired plan. Record current evidence for the prior-times rule from `S/layer/stp/receive_cist_test.go:13` and `receive.go:574` in the new plan. The old paragraph remains historical. Test presence does not establish the requested mutation sensitivity, which is unverified here. |
| C057 Landed phase/U labels in commit messages | P4:583, b6cde757, b3e894c4 | Partial parent outcome. Preserve landed history. The source explicitly says “Not fixable without rewriting landed history, so it stays as recorded.” Preserve that fact. No new phase labels in code or commits, and no history rewrite is authorized by this research. |
| C058 LACP refusal chooses wrong decode fact without failing test | P4:584, P9:453 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Switch test audit unit. `S/device/vswitch/lag_test.go:104` owns TestLACPDUHandlingAtSwitch and `intercept.go:35` owns the refusal. Pin literal invalid LACP TypeID/canonical inputs on both Peek and Receive. Current API no longer calls this Forward. |
| C059 SlowProtocolsDecodeFact expectations reuse implementation | P4:585 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Fact test unit. `S/layer/lag/fact_test.go:216` selects subtype. Replace derived wants with literal type IDs and canonical text for both LACP and Marker, retaining independent Marker cases at :196. |
| C060 Unreachable multicast recToInclude guard | P5:493 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Cleanup unit. `S/layer/mcast/state.go:97` repeats the caller guard in applyToPort. Trace the single call and remove the unreachable guard if still redundant. No new suppression or policy exemption. |
| C061 One-caller Neighbor Discovery helpers | P6:686 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Routing cleanup unit. `S/layer/routing/receive.go:164` and :179 define ndpReceiver/isSolicitedNode. Inline if each remains single-use and if the resulting receive branch stays readable and within R9. |
| C062 Unbounded passively learned Stale neighbor entries | P6:687 | Separately scoped leftover. No bound is added in this phase. `S/layer/routing/receive.go:63` and :118 insert learned entries. The routing README states that passively learned stale neighbors are unbounded. Future aging or capacity work needs its own decision. Source confirms insertion, but policy and standards behavior are unverified here. |
| C063 Ethernet all-nodes plus IPv6 unicast can confirm reachability | P6:688 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Routing hardening unit. `S/layer/routing/receive.go:27` uses link destination admission while :93 uses header destination for Solicited validation. Pin the mixed-destination frame and check the required destination relationship against a read RFC source before changing the rule. |
| C065 Zero/group sender MAC cached and answered | P6:690 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Routing hardening unit. `S/layer/routing/receive.go:59` and :115 process peer hardware addresses without a visible local unicast check. Add invalid-ARP and invalid-NDP-option cases after checking the codecs. Codec refusal and RFC requirements must be sourced before claiming the runtime defect. |
| C067 Published STP editions and RSTP forward-delay step | P3:1548-1550, parent:261-268 | U20-U22 documentation audit. Documentation/source-reading unit. `S/layer/stp/README.md:514` and :622 retain draft/published limits. Published IEEE 802.1Q-2011 and 802.1D-2004 were not read for this report. Keep unverified wording until their text is obtained. |
| C068 Draft-only transmit/role rules T2, T6, C2, A3, N2 | P3:1562-1566 | U20-U22 documentation audit. Documentation/source-reading unit. `S/layer/stp/README.md:437` and :514 identify draft-derived rules. Preserve each named rule and its source distinction. No new failing test may be asserted as published-standard conformance from this report. |
| C069 MST BPDU length-band overlap at 102 octets | P3:1567-1569 | U20-U22 documentation audit. Documentation/source-reading unit. `S/layer/stp/README.md:622` owns limits and `src/common/net/bpdu` owns wire parsing. Keep the Q2003 overlap interpretation distinct from any later-edition claim. Later text unverified. |
| C070 Overdue hello event-clock semantics | P3:1570-1573 | U20-U22 documentation audit. Documentation unit. `S/layer/stp/README.md:437` describes transmission timing. State the event-driven model rule without claiming the unread published Port Transmit figure endorses it. |
| C071 Published MSTI Proposal/Agreement wording | P3:1574-1576 | U20-U22 documentation audit. Documentation/source-reading unit. `S/layer/stp/README.md:514` records edition limitations. Q2003/D2009 agreement is historical source evidence, not independent verification of published 2011 wording. |
| C072 SSTP TCN address, TLV and acknowledgment behavior | P3:1577-1583 | U20-U22 documentation audit. Documentation/source-reading or capture unit. `S/layer/stp/README.md:622` keeps the interoperation limits. Preserve all three uncertain behaviors and the VLAN-1 STP-mode address difference. No device capture was read here. |
| C073 Loop-guard recovery VLAN and inconsistent BPDU rules | P3:1584-1588 | U20-U22 documentation audit. Documentation/source-reading unit. `S/layer/stp/README.md:622` retains recovery limits. Vendor revisions beyond the cited archived sources, disallowed VLAN behavior and PVID inconsistency remain unverified. |
| C074 Designated root-information change waits until hello | P3:1589-1594 | U20-U22 documentation audit. Open protocol follow-up beyond simple prose. `S/layer/stp/README.md:636` names an unmodeled agree flag and the receive/role machines remain event-driven. Preserve the carried immediate-request divergence in the close-out limits. A new protocol fix needs source-backed scope, not a corpus-only rewrite. |
| C075 Published LACP text, IEEE power table and source-address meaning | P4:565-568 | U20-U22 documentation audit. Documentation/source-reading unit, three subitems. `S/layer/lag/README.md:467` marks draft equivalence unverified and `S/layer/phy/README.md:129` bounds physical/power modeling. Published 802.1AX-2014 text, 802.3 class table and AX Figure 6-28 SA meaning were not independently read here. |
| C076 Local experimental EtherType behavior | P5:474-476 | U20-U22 documentation audit. Documentation unit. `S/layer/loopprotect/README.md` cites the IANA assignment and bounds the protocol. Keep IEEE Std 802 behavior unverified. IANA assignment alone does not prove that behavior. |
| C077 MLD query and Done from unspecified source | P5:477-479 | U20-U22 documentation audit. Documentation/source-reading unit. `S/layer/mcast/README.md` states refused forms. Keep both refusals as model limits until an applicable source resolves query and Done separately. |
| C078 ARP/ND ICMP error generation absent | P6:657-659 | U20-U22 documentation audit. No implementation silently added. `S/layer/routing/README.md` Limits owns missing ICMP Address Unreachable generation. Record a separate future plan if wanted. No parent phase owns that generator. |
| C079 IEEE relay rules after Q2003 and 802.1ad | P7:528-529, P9:1153-1155 | U20-U22 documentation audit. Documentation/source-reading unit. `S/layer/bridge/README.md:226` scopes the relay to the read edition. Later 802.1Q and 802.1ad remain unverified. |
| C080 Routed VLAN-interface unicast isolation rule | P9:1156-1158 | U20-U22 documentation audit. Documentation unit. `S/device/vswitch/README.md` describes local receive/interception, while `S/layer/bridge/README.md:226` scopes relay limits. Keep switch policy distinct from what Q2003 permits. Vendor behavior unverified. |
| C081 Loop-protected-port BPDU/LACPDU vendor behavior | P9:1159-1160 | U20-U22 documentation audit. Documentation unit. `S/device/vswitch/timers.go:190` keeps current protocol emission behavior. Preserve the model rule without claiming vendor equivalence. |
| C082 Aggregate speed and Slow Protocols learning source limits | P9:1161-1163 | U20-U22 documentation audit. Documentation unit. `S/device/vswitch/link.go:140` uses the fastest member, while `S/layer/bridge/README.md:226` limits learning at intercepted addresses. Keep the accepted model decisions and published/draft source limits separate. |
| C083 MST reroot recent-root cut versus backup delay | P13:922-928 | U20-U22 documentation audit. Documentation/source-reading unit. `S/layer/stp/README.md:622` preserves limits and the role machine at `roles.go:281` owns transition conditions. Published 2011 remains unverified. R10 cases cannot by themselves distinguish the two readings, so changing the condition requires a discriminating case and the text. |
| C084 IEEE 802.1D-1998 timer definitions | P13:929-931 | U20-U22 documentation audit. Documentation/source-reading unit. `S/layer/stp/README.md:622` retains timer limits. The cited figures support implemented assignments in the retired plan, but section 17.15 was not read here. Keep that qualification. |
| C085 Remaining STP role-machine and clock limits | P13:176-178, README work and P3 remaining limits | U20-U22 documentation audit. Documentation close-out unit. `S/layer/stp/README.md:622` already lists remaining limits. Preserve runtime aging/transition limits as well as the published-source questions. Treat further protocol work as a separately scoped unit if it cannot fit a bounded source-only close-out. |
| C086 Switch timing, fork, derive, receive and link loops | P9:446, P2 Out of scope | Preserve current source in U20-U22 audit. Landed composition. `S/device/vswitch/layers.go:49` centralizes capabilities, :167 advances them, `output.go` converts outputs, `fork.go:17` forks and `derive.go:26` derives. Remaining unused table parts are C022. No repeat composition phase. |
| C091 LAG STP speed uses fastest enabled member | P9:451, P4:350, :572-576 | Preserve current source in U20-U22 audit. Accepted model decision. `S/device/vswitch/link.go:140` computes highestSpeed. Document it without converting aggregate data rate to a path-cost rule from an unread edition. Source limitations are C076. |
| C093 Refused LACP decode assertion | P9:453, P4:584 | Preserve current source in U20-U22 audit. Same source item as C058. `S/device/vswitch/lag_test.go:104` is the current test owner. Do not count the phase 9 carry as a different defect. |
| C097 Filter applied to released frame | P9:457, P6 Out of scope | Preserve current source in U20-U22 audit. Landed release processing. `S/device/vswitch/route.go:308` applies deferred/egress filtering and mirror selection. Specific mirror-refusal trace parity remains C019 and SelectVLANs remains C008. |
| C100 ARP/ND directed to interface MAC relayed or mis-dropped | P9:460, P6:664-668 | Preserve current source in U20-U22 audit. Landed receive interception. `S/layer/routing/receive.go:18` exposes Receives and `S/device/vswitch/forward.go` dispatches the control candidate. Preserve protocol receive tests, including non-ICMPv6 rejection C064. Generic neighbor-hardening items remain C062/C063/C065. |
| C107 Learning source addressed to reserved protocol MAC | P9:467, P7:523-525 | Preserve current source in U20-U22 audit. Accepted relay limit. `S/layer/bridge/README.md:226` owns the learning limit and `S/device/vswitch/intercept.go:35` intercepts protocol frames. Preserve the documented exception. Later standard claims remain C073/C076. |
| C110 LACP mirror ingress names logical port | P9:470 | Preserve current source in U20-U22 audit. Landed normalization. `S/device/vswitch/intercept.go:35` handles LACP and `mirror.go:221` resolves member ingress. Keep selector tests for a LAG member. Additional VLAN-selector gap is C008. |
| C119 Held release continues its original journey | P10:428, P6 Out of scope | Preserve current source in U20-U22 audit. Landed token seam. `S/fabric/flow.go:108` counts held journeys, `S/device/vswitch/route.go:249` applies token exits, and fabric output handling routes them to held frame IDs. Test weakness C007 still prevents declaring every held assertion sufficient. |
| C121 Egress queue scheduler belongs to fabric | P10:430, P7:518-519 | Preserve current source in U20-U22 audit. Accepted ownership. `S/fabric/egress.go:123` schedules queue events and `S/layer/traffic/layer.go:101` decides admission from configured owner. Timing/subject defects remain C002/C003. Do not move the scheduler back to traffic. |
| C122 Dead journey comparisons still open after U14 promise | P10:431, P8:394 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Same unresolved carry as C009. `S/fabric/compare_journey.go:59` and `compare.go:258` remain. A promised unit disposition is not evidence of closure. |
| C123 State/accessor seam | P10:432, P9 Out of scope | Preserve current source in U20-U22 audit. Landed device seam. `S/device/state.go:329` owns comparison and `S/device/vswitch/inspect.go` projects switch state. Whole-row Diff weakness remains C001, Snapshot classification C015. |
| C124 Member selection, queue limits and MTU through seam | P10:433, P9 Out of scope | Preserve current source in U20-U22 audit. Landed. `S/device/vswitch/mirror.go:20` implements AdmitEgress and `S/device/vswitch/lag.go:18` selects members. Fabric uses device-provided decisions. Queue-member subject C003 and no-member protocol emission C004 remain. |
| C127 Routed-LAG solicitation member selected once | P10:436 | Preserve current source in U20-U22 audit. Landed emission seam. `S/device/vswitch/route.go:202` emits routing output using device-side member choice. Retain a routed LAG with wire VID differing from route context to detect a second fabric selection. No-member protocol path is the separate C004. |
| C128 Released egress-selected mirror copy | P10:437 | Preserve current source in U20-U22 audit. Landed baseline. `S/device/vswitch/route.go:308` releases with mirror selection and `mirror.go:106` remembers held mirrors. Remaining live/held selector parity is C008, refusal-trace parity C019. |
| C131 Other phase 9 review gaps excluded from fabric units | P10:440, 8647d908 | Separately scoped leftover. Follow-up evidence, not an authorized unit: Remain explicitly assigned here to C019/C020/C022/C023/C024. `S/device/vswitch/route.go:288`, `intercept.go:178`, `layers.go:44` and `stp_test.go:1904` still identify the work. Excluding them from phase 10 ownership did not close them. |
| C140 Phase 11 repeats all external follow-ups | P11:496-515 | Preserve current source in U20-U22 audit. Mapped without omission: C019-C024 cover phase 9, C001-C018 cover all 18 phase 10 items. `S/netmodel/load_mst.go:31` addresses loader work, not those runtime/gate findings. This row is a source cross-reference, not a newly invented defect. |
| C168 Scale allocation figure exceeds target | P12 tests:122-123 | Separately scoped leftover. S/fabric/README.md:420-421 records cumulative allocation above the 2 GiB target. A benchmark does not meet that target. |
| C169 Lab hardware check | P12 open question:127-128, goal:17-19, parent:243-245 | Manual owner-run check outside the unit graph, with command and expectations below. No unit or parent completion waits for this evidence. |
| C179 R6: protocol behavior matches named edition/clauses | Parent:88-93 | Retain qualified limits and a partial parent outcome. STP README:514, LAG README:467, bridge README:226 and phy README:129 retain qualified sources. U20-U22 cannot make unread editions verified. |
| C180 R7: independent known-byte wire fixtures | Parent:95-100, P3/P4 retirements | Remaining byte-provenance work is separately scoped. Preserve codec known-byte fixtures and source qualifications. No codec behavior or wire golden changes are proposed. |
| C181 R8: fabric reaches devices through sim/device | Parent:102-104, P10 retirement ef213026 | Preserve the landed device seam at S/fabric/topology.go:61 and :262, vswitch/fork.go:17. C015/C016 remain deferred hardening, not a reason for layer imports. |
| C187 DoD: no plan labels in code/comments/commit messages | Parent:256, P4:583 | Keep history and record a partial parent outcome. P4:583 records the historical label conflict. Maintain the rule for new artifacts. No rewrite or policy exception is authorized. |
| C188 DoD: parent status and outcome | Parent:257 | Future parent outcome remains open under the partial-outcome decision until implementation and every residual obligation are resolved. This planning change does not mark the parent complete. |
| C189 Open question: protocol editions | Parent:261-268 | Retain qualified limits and a partial parent outcome. Preserve settled edition choices and individual unread-source qualifications C067-C085. |
| C190 Open question: Marker protocol scope | Parent:269-270, P4 decisions | Settled responder scope in P4. net/lacp/README.md:132 describes MarkerResponse and layer/lag/fact_test.go:196 covers it. An initiator is new scope. published-edition qualification remains. |

### Current prior-times evidence

C056's historical line 13 is now `TestRootPortDoesNotAcknowledgeTCN`.
The prior-times assertions are `TestInternalMSTTopologyChangeUsesPreviousRootTimes`,
`TestRSTPTopologyChangeUsesPreviousRootTimes`, and
`TestTopologyChangeKeepsOldTimesOnAnUnchangedRootPort` in
[`receive_cist_test.go`](../../src/common/sim/layer/stp/receive_cist_test.go).
`applyBPDU` in [`receive.go`](../../src/common/sim/layer/stp/receive.go)
saves received times, restores prior times while propagating received TC,
then restores received times before recomputation. C043-C056's individual
mutation gaps remain unverified here. These assertions do not close them.

## Units

No executable units are selected. Re-planning must bound runtime changes,
source-reading work, and discriminating tests before implementation. Keep
G003 coupled to C168 so a per-frame mechanism includes memory measurements.

## Verification

Compare each preserved row with
the close-out inventory as it stood before its plan was retired
(`git show c9a9900d:docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase12-plan.md`)
and every deferred subitem in retirement commit `ef213026`. Check repeated
references and each standards qualification separately. Runtime work needs
failing-first and mutation-sensitive tests at its eventual public entry point.
No corpus case or expectation changes are authorized by this document.

```sh
uv run tools/scripts/run.py verify check-prose --strict docs/plans/2026-10-10-1624-refactor-simulation-followups-plan.md
uv run tools/scripts/run.py verify check-plan-state docs/plans/2026-10-10-1624-refactor-simulation-followups-plan.state.json
```

### Owner-run ICX7150 evidence

The ICX7150 manual check is outside the unit graph. On the configured Linux
lab host, after its owner approves the documented data-plane traffic, run.
The two subtests together send 20,000 frames of 128 octets, 2.56 MB across about
20 seconds (`lab_test.go:346-366`). This traffic updates data-plane counters
and learned state. The owner supplies the observed wiring.

```sh
go test -tags=simload_lab -run '^TestICX7150Comparison$' -count=1 -v ./src/edge/simload/test/integration
```

Set the observed SIMLOAD_LAB_* wiring variables exactly as
`src/edge/simload/test/integration/README.md:12-18` shows. The test must run,
rather than skip. Expected harness result: single and paired-flow subtests pass, each flow
receives at least one signed frame, successful sends equal unique receives
plus missing sequences, pacing is within 1% of the scheduled 9.999 s span,
and interface capture drops are zero (lab_test.go:341, :395, :411). PASS
does not assert zero missing, latency equality, or simulator/lab delivery
equality. The operator separately compares the printed normalized rows. Record disagreement as the
configuration, full JSON, missing sequences, capture drops, and the failing
assertion. Keep lab missing frames separate from simulator drop and cable
loss. Do not tune either model to erase the disagreement.

## Definition of done

- Every residual has a source-backed disposition and its complete subitems.
- Runtime requirements have bounded units and independent observable proofs.
- R5, R6, R7, allocation, and historical-label outcomes are reported honestly.

## Open questions

Choose the transactional replacement and generic-device contracts before
C014-C017. Select the per-frame export and retention mechanism, ownership,
and memory acceptance measurements for G003/C168. Stale-neighbor capacity
or expiry needs a separate decision. Obtain the unread editions and captures
before settling qualified protocol behavior. No remedy is selected here.
