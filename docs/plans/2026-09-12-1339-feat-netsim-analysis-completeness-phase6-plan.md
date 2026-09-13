---
title: Network Simulation Analysis Completeness, Phase 6 - Plan
type: feat
date: 2026-09-12
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: mixed
amends: docs/architecture/2026-09-10-virtual-device-direction.md
parent: docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-plan.md
---

# Network simulation analysis completeness, phase 6: Scenarios, replay, and run lifecycle - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Turn one-off fabric executions into reusable deterministic analyses with honest
stopping conditions. A caller can replay timed inputs and faults, distinguish
convergence from exhaustion or oscillation, and account for every journey.

This phase claims parent requirements R27-R31. It consumes R13, R26, R37, and R39
by mapping phase 2 host acceptance to journey result states, extending phase 5
Fork, and completing the troubleshooting corpus workflow.

## Decisions

- A scenario is an immutable library value with a name, seed, starting facts,
  and typed timed actions. Go callers evaluate the typed result; the scenario
  does not define an assertion language, persistence, or file I/O.
- Replay accepts already-decoded records or raw record bytes plus capture
  metadata. Capture readers and live packet collection belong outside netsim.
- Determine convergence from stable normalized state across relevant protocol
  refresh boundaries, not from an empty event queue alone.
- Ignore no-op periodic wakes for convergence while retaining them in diagnostic
  trace. Detect repeated changing fingerprints as instability.
- Make journey outcome and analysis completion independent axes.
- Every run result carries the immutable replay specification required by the
  parent, including simulator contract version and rule-set identity. Status is
  derived from all scoped issues; stop reason is separate. Incompatible replay
  identity is rejected before execution.

## Requirements

1. **R27:** Named scenarios replay typed injections and faults with deterministic
   ordering and seed. **Acceptance example:** the same link flap and injection
   sequence yields the same semantic trace.
2. **R28:** Record replay preserves timestamp, source, truncation, and decode
   evidence without doing capture I/O. **Acceptance example:** a truncated record
   becomes incomplete input with its source metadata intact.
3. **R29:** Run returns stop reason, logical time, event count, pending work,
   scoped status, all issues, trace, and immutable replay specification.
   **Acceptance example:** reaching a budget with queued work records a budget
   stop and `Exhausted` issue without discarding another unsupported cause.
4. **R30:** Convergence and oscillation use canonical state fingerprints over a
   declared observation window. **Acceptance example:** alternating STP roots
   return `Unstable` with the repeating fingerprint sequence.
5. **R31:** Every journey has one result state and causal path. `Pending` is
   nonterminal; all other defined states are terminal at the run boundary.
   **Acceptance example:** host rejection differs from delivery and switch drop.
6. **R13/R26/R37:** Scenario runs use executable forks and phase 2 endpoint
   acceptance while preserving deterministic ordering. **Acceptance example:**
   rejection is excluded from deliveries, maps to `JourneyRejected`, and running
   the candidate first cannot change the current result.
7. **R39:** Add the troubleshooting golden workflow to the analysis corpus.
   **Acceptance example:** an injected frame ends in one result state with a trace
   that identifies the first decisive rule.

## Out of scope

- Scenario databases, orchestration services, cron scheduling, telemetry export,
  UI editors, pcap readers, live capture, or external event buses.
- Wall-clock execution and real-time fidelity.
- Comparison and input-domain enumeration assigned to phase 7.

## Units

### U1: Re-plan scenario and run contracts

- **Files:** scenario, replay, journey, event, and run contracts under
  `src/common/netsim/fabric/`; affected protocol integration
- **After:** parent U5
- **Change:** Specify typed actions, ordering, record metadata, replay
  specification and identity, journey result states, stop reasons, pending-work
  summaries, state fingerprints, observation windows, and oscillation detection.
  Extend the phase 5 owner/origin/lifetime and Fork inventory for every new field.
- **Tests:** Lifecycle and event-order tables with failure scenarios.
- **Verify:** Re-plan before editing.

### U2: Implement immutable scenarios and record replay

- **Files:** new scenario and record files under `src/common/netsim/fabric/` and
  their tests
- **After:** U1
- **Change:** Add typed timed frame, link, device, and protocol actions; stable
  ordering; seeds; decoded/byte record replay with evidence; and a versioned
  replay specification. Deep-copy scenario actions, pending work, and seed state
  in executable forks.
- **Tests:** Same-time ordering, seed replay, contract-version mismatch, link and
  device faults, decoded and byte records, truncation, malformed input, and fork
  isolation.
- **Verify:** Focused scenario, network, and fabric tests.

### U3: Replace run count with typed lifecycle result

- **Files:** `src/common/netsim/fabric/run.go`, journey and event types, callers
  and tests
- **After:** U1, U2
- **Change:** Return complete run metadata, map phase 2 host acceptance to exactly
  one journey result state, keep `Pending` nonterminal, track pending work,
  implement convergence observation, ignore no-op periodic wakes, and detect
  oscillation. Add the troubleshooting corpus case.
- **Tests:** Quiescent, converged, exhausted, incomplete, unsupported, unstable,
  pending, looped, truncated, delivered, rejected, and dropped cases.
- **Verify:** Focused run and protocol integration tests.

### U4: Document scenario and lifecycle use

- **Files:** `src/common/netsim/fabric/README.md`, related package READMEs, parent
  architecture record
- **After:** U2, U3
- **Change:** Add working planning, topology-shadowing, troubleshooting, replay,
  and convergence examples; document logical-time and file-I/O boundaries.
- **Tests:** Compile documentation examples where practical.
- **Verify:** Netsim race tests, vet, diff-aware verifier.

## Verification

The re-plan must name the fingerprint fields and observation-window rules. Run:

```bash
go test -race ./src/common/netsim/...
go vet ./src/common/netsim/...
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

## Definition of done

- [ ] Parent requirements R27-R31 pass their acceptance examples.
- [ ] Scenario replay is independent of map order and wall time.
- [ ] Every run has a stop reason, every journey has one result state, and
      `Pending` remains nonterminal.
- [ ] Periodic work cannot make exhaustion look like convergence.
- [ ] Replay remains an in-memory library boundary with no capture reader.
