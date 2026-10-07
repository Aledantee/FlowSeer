package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
)

func captureAuthorization(requester *identityv1.OperatorRef) *modelcapturev1.CaptureAuthorization {
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
			name: "named requester is valid",
			message: captureAuthorization(identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://idp.example.com"),
				Subject: proto.String("zitadel|usr_123"),
			}.Build()),
			wantValid: true,
		},
		{
			name:      "authorization without a requester is accepted",
			message:   captureAuthorization(nil),
			wantValid: true,
		},
		{
			name: "requester without an issuer is rejected",
			message: captureAuthorization(identityv1.OperatorRef_builder{
				Subject: proto.String("zitadel|usr_123"),
			}.Build()),
		},
		{
			name: "requester without a subject is rejected",
			message: captureAuthorization(identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://idp.example.com"),
				Subject: proto.String(""),
			}.Build()),
		},
	}

	runValidationCases(t, tests)
}
