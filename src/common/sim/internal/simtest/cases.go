package simtest

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// gigabitAuto is a fully reported gigabit auto-negotiating end: known speeds, supported
// auto-negotiation, and an auto Setting.
func gigabitAuto() phy.Ethernet {
	return phy.Ethernet{
		SupportedSpeedsBPS:       []uint64{1_000_000_000},
		AutoNegotiationSupported: phy.CapabilitySupported,
		Setting:                  &phy.Setting{AutoNegotiation: true},
	}
}

// journeySteps flattens a journey's decisive trace: each hop's forwarding steps in order,
// followed by a host's acceptance decision step when one was recorded.
func journeySteps(j fabric.Journey) []trace.Step {
	var out []trace.Step
	for _, e := range j.Entries {
		if e.Result != nil {
			out = append(out, e.Result.Steps...)
		}
		if e.Step != nil {
			out = append(out, *e.Step)
		}
	}

	return out
}

// journeyHopResult returns the last recorded hop's forwarding result: the switch-layer outcome
// host-level acceptance or rejection follows. A journey without a hop, such as one that never
// left its origin, returns nil.
func journeyHopResult(j fabric.Journey) *device.Result {
	for _, e := range slices.Backward(j.Entries) {
		if e.Result != nil {
			return e.Result
		}
	}

	return nil
}

// journeyHopOutcome returns the domain outcome of [journeyHopResult], or the zero outcome
// when the journey never reached a hop.
func journeyHopOutcome(j fabric.Journey) trace.Outcome {
	if r := journeyHopResult(j); r != nil {
		return r.Outcome
	}

	return ""
}

// journeyHopReason returns the reason paired with [journeyHopOutcome].
func journeyHopReason(j fabric.Journey) trace.Reason {
	if r := journeyHopResult(j); r != nil {
		return r.Reason
	}

	return ""
}

func expectedFact(typeID, canonical string) FactExpectation {
	return FactExpectation{TypeID: typeID, Canonical: canonical}
}

func expectedStep(
	layer trace.Layer,
	op trace.Op,
	ruleID trace.RuleID,
	subject trace.Subject,
	inputs []FactExpectation,
	outputs []FactExpectation,
) StepExpectation {
	return StepExpectation{
		Layer:   layer,
		Op:      op,
		RuleID:  ruleID,
		Subject: subject,
		Inputs:  inputs,
		Outputs: outputs,
	}.Canonical()
}

func expectedChange(
	layer trace.Layer,
	subject trace.Subject,
	field string,
	from *FactExpectation,
	to *FactExpectation,
) ChangeExpectation {
	return ChangeExpectation{
		Layer:   layer,
		Subject: subject,
		Field:   field,
		From:    from,
		To:      to,
	}.Canonical()
}

func reportJourney(fab *fabric.Fabric, fid fabric.FrameID) (fabric.Journey, error) {
	for _, j := range fab.Report() {
		if j.FrameID == fid {
			return j, nil
		}
	}
	return fabric.Journey{}, errs.Msgf("retained frame %d has no journey", fid)
}
