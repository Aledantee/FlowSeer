package conformance

import (
	"testing"

	auditv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/audit/v1"
)

func TestDeliverRequestRules(t *testing.T) {
	runValidationCases(t, []validationCase{
		{name: "delivery carries one event", message: auditv1.DeliverRequest_builder{Event: deviceOperationEvent().Build()}.Build(), wantValid: true},
		{name: "delivery without an event is rejected", message: auditv1.DeliverRequest_builder{}.Build()},
	})
}
