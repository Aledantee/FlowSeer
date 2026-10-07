---
title: A State Transition Test Must Assert Observables Unique to the Destination State
date: 2026-10-04
last_verified: 2026-10-07
category: conventions
module: src/common/sim/layer
problem_type: convention
component: sim
severity: high
applies_when:
  - "Writing or reviewing tests for protocol or simulated state machines with sequential states that share quiescent or inactive conditions"
  - "Investigating a false test that passes when a state transition guard or state assignment is mutated or deleted"
  - "Testing multi-step timer expirations where intermediate and terminal states produce identical external forwarding behavior"
  - "Testing a state-machine branch where an internal request flag, sibling tree, or held emission can mask the state or frame under test"
related_components: [conformance-gates, bpdu]
tags: [testing, state-machine, mutation-testing, false-test, lacp, stp]
---

# A state transition test must assert observables unique to the destination state

## The situation

In protocol engines and simulation models, state machines frequently step through consecutive states that share the same external symptoms. In `src/common/sim/layer/lag`, an active LACP member without partner traffic transitions from `Current` to `Expired` after its receive timeout, and then from `Expired` to `Defaulted` after an additional timeout (`src/common/sim/layer/lag/layer.go:670-681`).

In `Expired`, the member clears partner synchronization, detaching from the group and disabling packet forwarding (`Attached: []`, `Enabled: []`). When `Fallback` is disabled, `Defaulted` also leaves the member detached and disabled (`Attached: []`, `Enabled: []`).

An earlier implementation of `TestZeroSystemPeerDefaultingWithoutFallbackDisables` in `src/common/sim/layer/lag/layer_test.go:1963` advanced time past both timeouts and asserted only that the aggregate's enabled and attached slices were empty. That assertion passed, but deleting the assignment `m.status = Defaulted` in `layer.go:678` left the test green. Because `Enabled` and `Attached` were already empty in the preceding `Expired` state, the test could not tell whether the member reached `Defaulted` or remained stuck in `Expired`.

## Why it bites

External symptoms like traffic forwarding, packet drops, or empty membership lists are coarse. When multiple lifecycle states produce the same inactive symptom, asserting on that symptom verifies that traffic stopped, not which state stopped it.

Advancing simulated time directly across multiple deadlines hides intermediate transitions. Mutation testing exposes this: deleting a state assignment or transition condition leaves the suite green because the negative assertion was satisfied before the mutated step ever executed.

## What to do instead

Assert observables that distinguish the destination state from its predecessors:

1. Assert the member or engine state directly (`portInfo.Status == lag.Defaulted`).
2. Assert protocol flags or advertised frame fields unique to the state (`portInfo.Actor.State&lacp.StateDefaulted != 0` and `portInfo.Actor.State&lacp.StateExpired == 0`).
3. Assert distinct downstream lifecycle behavior. When a learned partner defaults, it schedules an aggregate wait for reselection rather than remaining inert (`NextWake` returns the scheduled aggregate wait).
4. Step through each intermediate state in sequence rather than advancing across multiple timeouts in a single call. Verify the intermediate state with its markers, then advance to the destination state.

```go
// Step through Expired first, verifying the intermediate state.
l.Advance(t0.Add(3 * time.Second))
if portInfo := l.PortInfo("1/1/1"); portInfo.Status != lag.Expired || portInfo.Enabled {
    t.Fatalf("at first timeout: status = %v, enabled = %t, want Expired and disabled", portInfo.Status, portInfo.Enabled)
}

// Advance past the second timeout to Defaulted. Assert the destination state
// and its wire flags, not only that forwarding remains disabled.
l.Advance(t0.Add(6 * time.Second))
portInfo := l.PortInfo("1/1/1")
lagInfo := l.Info("lag1")
if portInfo.Status != lag.Defaulted ||
    portInfo.Actor.State&lacp.StateDefaulted == 0 ||
    portInfo.Actor.State&lacp.StateExpired != 0 ||
    len(lagInfo.Enabled) != 0 || len(lagInfo.Attached) != 0 {
    t.Fatalf("after second timeout: port = %+v, want Defaulted with StateDefaulted set and StateExpired clear", portInfo)
}
```

`TestDefaultingReselectsWhenAdministrativePartnerDiffers` (`src/common/sim/layer/lag/layer_test.go:2400-2435`) applies the same pattern across four sequential checks: converged operation at `t0+2s`, `Expired` at `t0+3s`, `Defaulted` with detachment and pending aggregate wait at `t0+7s`, and re-attachment after the wait at `t0+9s`.

The same trap appears when several protocol machines share a transmit record.
`TestMSTIAgreementUsesTheCISTPortVector` keeps a Designated case Discarding, a
Root case Forwarding, and a Root-to-Designated return Discarding
(`src/common/sim/layer/stp/agreement_test.go:273-350`). A test with only the
Root case would leave the role-dependent agreement rule untested. The
transmit-side test reads the emitted frames and counts their topology-change
flags by port (`src/common/sim/layer/stp/transmit_test.go:664-733`). That
observable distinguishes a request from a frame built with the final state.
When two internal flags share a record, assert the wire or public state they
control when that is the behavior under test. A helper-level test may inspect
the flag directly when the helper's contract is the subject.

## Evidence

- In `src/common/sim/layer/lag/layer.go:678`, deleting `m.status = Defaulted` left `TestZeroSystemPeerDefaultingWithoutFallbackDisables` green before commit `d329977f`. Updating the assertion in `src/common/sim/layer/lag/layer_test.go:1983-1988` to check `portInfo.Status` and the actor state bits makes the mutation fail:
  `--- FAIL: TestZeroSystemPeerDefaultingWithoutFallbackDisables (0.00s)`
- In `src/common/sim/layer/lag/layer.go:674`, changing `if !sameAggregationPort(...)` to `if !m.enabled || !sameAggregationPort(...)` passed in `TestDefaultedRequestsReselectionAfterLearnedPartner` (`src/common/sim/layer/lag/layer_internal_test.go:210-225`) when the test advanced directly to `t0.Add(6 * time.Second)`. Stepping through `t0.Add(3 * time.Second)` to verify `Expired` status with attachment preserved and forwarding stopped makes the mutation fail:
  `--- FAIL: TestDefaultedRequestsReselectionAfterLearnedPartner (0.00s)`
- `src/common/sim/layer/stp/transmit.go:39-66,123-135` shows why an internal request is not always the behavior to assert: the transmit pass turns request flags into one frame and clears them after the frame is built. The spanning-tree tests therefore inspect role, state, and decoded emissions in `src/common/sim/layer/stp/agreement_test.go:287-350` and `src/common/sim/layer/stp/transmit_test.go:676-733`.

## What this does not cover

This rule does not apply to stateless functions with a single return value, which [a refusal test needs an input only the refusal rejects](a-refusal-test-needs-an-input-only-the-refusal-rejects.md) addresses. It applies to stateful systems where distinct lifecycle states share identical external or quiescent outputs.
