---
title: Corpus, Load Transmitter, and Documentation Close-Out - Plan
type: refactor
date: 2026-10-01
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
parent: docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-plan.md
---

# Corpus, Load Transmitter, and Documentation Close-Out - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The conformance corpus and `simload` sit on the final APIs with their own
defects fixed, and every README and direction record describes the tree as
it is. Stop condition: if the lab comparison against the ICX7150 disagrees
with the simulator after the earlier phases, the disagreement is a finding
for a new plan and this phase records it instead of tuning either side.

## Decisions

- The parent's Decisions apply.
- The corpus exports its registry, its assertions, and its representative
  fixtures. The `Case*` constructors are unexported. Why: no package outside
  the corpus calls one.
- `fabric` folds whole-fabric trust metadata into each flow's statistics.
  Why: `netsimload.NewReport` backfills it
  (`src/edge/netsimload/report.go:94-96`), which is the fabric's knowledge
  leaking into its consumer. If phase 10 did this, the backfill is deleted
  here.
- The three flow types `simload` uses move to a leaf package if phase 10
  left them in `fabric`. Why: the edge binary links the whole simulator for
  three value types.
- Each Correctness entry was read, not run.

## Requirements

R1. Every Correctness entry has a test that fails before its fix.

R2. `simload` records every frame the receiver delivered before the drain
ended. Example: frames buffered in the receive channel when the drain timer
fires are counted, and the run reports none of them missing.

R3. Every README under `src/common/sim` and `src/edge/simload` passes
`check-prose.py`, names only identifiers that exist, and states each
package's limits. Example: `internal/simtest/README.md` lists every
registered case, including `planning/pvst-per-vlan-root`, and its counts
match the registry.

R4. The three simulation records and the package shape record match the
tree. Example: the virtual-device record's "Remaining capability gaps" no
longer lists neighbor solicitation, and names what the protocol phases left
unmodelled.

## Inventory

Paths are as of commit `61775c73`. `C` is
`src/common/netsim/internal/netsimtest`, which phase 1 moves to
`src/common/sim/internal/simtest`. `E` is `src/edge/netsimload`, which
becomes `src/edge/simload`. No entry has a test in the tree.

### Correctness

1. High. `execute` selects between the run context and the frame channel
   and jumps out on cancellation without draining (`E/run.go:252-270`).
   Buffered frames are reported missing.
2. Medium. A negative latency marks the frame malformed
   (`E/stats.go:104-108`). Transmit and receive on separate interfaces can
   produce one from clock skew.
3. Medium. `NewReportFromSimulator` iterates the simulator's flows only
   (`E/report.go:110-127`), so a lab flow the simulator does not have is
   dropped. The command rejects that case and the library does not.
4. Medium. The accumulator closes before the receiver is cancelled
   (`E/run.go:338-340`), so a frame in that window is counted as late.
5. Low. `linuxSender.Send` returns raw errors for a short write and for
   `ENOBUFS` (`E/packetio/sender_linux.go:64-74`).
6. Low. Corpus cases index the report by `fid-1`
   (`C/loopprotect_cases.go:257`, `C/stp_cases.go:355`).

### Completeness

- `C/README.md:17-208` omits `planning/pvst-per-vlan-root`
  (`C/stp_cases.go:1197`), and `:209-211` gives counts of sixteen and seven
  where the registry has more.
- `load_cases.go` has no `RegisterLoadCases`. Its cases are registered
  inline in `DefaultRegistry` (`C/cases.go:2503-2505`).
- The offered-load record compares latency. `simload` keeps minimum,
  maximum, and sum. Decide whether percentiles are needed for the lab
  comparison or the record is narrowed.
- `E/README.md:58-61` does not say that `compare` fails on a lab flow the
  simulator lacks.
- No README names the files `phy`, `bridge`, and `port` gained in phases 4
  and 7. Check each exists.

### Design

- `preflightSources` walks a cloned stream to check payload size
  (`E/run.go:200-221`). Phase 8 gives a source a way to state it.
- `flowAccumulator` keeps every sequence number in a map
  (`E/stats.go:44-50,163-167`). Sequences are contiguous, so ranges do.
- `Sign` allocates a payload and the caller re-encodes the frame
  (`E/signature.go:39-47`, `E/run.go:312-316`), and receive decodes the
  frame to read the signature (`E/stats.go:92-97`).
- `AssertDiffCoversConfig` returns on a nil capability pointer in its seed
  (`C/diffcoverage.go:183-186`), so a package that seeds no filter passes.
- `diffcoverage.go` keeps a type-assertion table across every layer
  package. With facts unexported in phase 2, decide what it asserts on.

### Tests

- `packetio/sender_linktest_test.go` builds only with `linux` and
  `netsimload_linktest`, and the verifier's tag sweep skips it. Keep a
  compile check for it.
- The only integration test needs lab hardware (`netsimload_lab` tag). A
  run over a pair of in-memory endpoints would exercise transmit and compare
  without it.
- No test covers the drain race, negative latency, or a duplicate case
  identifier in `Registry.Register`.
- `RepresentativeFabric` has no allocation benchmark, and the fabric README
  records a scale run far over its target.

## Open questions

- The lab run needs the ICX7150 powered on, with notice. It is a manual
  check and blocks nothing else in this phase.
- Does `rawsocket.OpenLocalInterface` ignore outgoing frames on Linux? If
  not, `simload` on one interface captures its own transmissions. Read
  `src/modules/capture/rawsocket` before answering.
- How does the layer contract gate pin its own rules? Phase 2 was accepted
  with three of them changeable while every test stays green: the
  `RetentionKey` row's literal
  (`test/conformance/sim/layer_contract_test.go:79`), the `import_device`
  root (`:88`), and `exported_fact` narrowed to stateful packages. Its
  reviewer proposed fixtures derived from each row's kind and a test that
  every row refuses in both package kinds (`2db760b7`).
