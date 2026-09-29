package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	principalv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/principal/v1"
)

func captureAuthorization(requester *principalv1.OperatorRef) *modelcapturev1.CaptureAuthorization {
	return modelcapturev1.CaptureAuthorization_builder{
		RequestedBy:          requester,
		Reason:               proto.String("loss on the uplink"),
		FullPayloadRequested: proto.Bool(false),
	}.Build()
}

// The service's own guard is a second line; the interceptor in front of
// CaptureService enforces these rules, so they are pinned on the schema.
func TestCaptureAuthorizationRules(t *testing.T) {
	tests := []validationCase{
		{
			name:      "named requester is valid",
			message:   captureAuthorization(principalv1.OperatorRef_builder{Subject: proto.String("zitadel|usr_123")}.Build()),
			wantValid: true,
		},
		{name: "authorization without a requester is rejected", message: captureAuthorization(nil)},
		{
			name:    "requester without a subject is rejected",
			message: captureAuthorization(principalv1.OperatorRef_builder{Subject: proto.String("")}.Build()),
		},
	}

	runValidationCases(t, tests)
}
