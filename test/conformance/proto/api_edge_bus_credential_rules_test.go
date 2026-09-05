package conformance

import (
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
)

func TestAttachBusResponseRules(t *testing.T) {
	valid := func() *edgev1.AttachBusResponse {
		return edgev1.AttachBusResponse_builder{
			AccountJwt:     []byte("account-jwt"),
			UserCredential: []byte("user-credential"),
			Subjects:       map[string]string{"announce": "flowseer.edge.announce"},
			ClusterUrls:    []string{"wss://central.example.net:443"},
		}.Build()
	}

	runValidationCases(t, []validationCase{
		{
			name:      "account jwt, user credential, subjects, and one cluster url",
			message:   valid(),
			wantValid: true,
		},
		{
			name: "empty subjects map is valid for an edge with no binding yet",
			message: edgev1.AttachBusResponse_builder{
				AccountJwt:     []byte("account-jwt"),
				UserCredential: []byte("user-credential"),
				ClusterUrls:    []string{"wss://central.example.net:443"},
			}.Build(),
			wantValid: true,
		},
		{
			name: "zero cluster urls is rejected",
			message: edgev1.AttachBusResponse_builder{
				AccountJwt:     []byte("account-jwt"),
				UserCredential: []byte("user-credential"),
				ClusterUrls:    []string{},
			}.Build(),
			wantValid: false,
		},
		{
			name: "missing account jwt is rejected",
			message: edgev1.AttachBusResponse_builder{
				UserCredential: []byte("user-credential"),
				ClusterUrls:    []string{"wss://central.example.net:443"},
			}.Build(),
			wantValid: false,
		},
		{
			name: "missing user credential is rejected",
			message: edgev1.AttachBusResponse_builder{
				AccountJwt:  []byte("account-jwt"),
				ClusterUrls: []string{"wss://central.example.net:443"},
			}.Build(),
			wantValid: false,
		},
	})
}

func deviceCredential(key string, version uint64) *edgev1.DeviceCredential {
	return edgev1.DeviceCredential_builder{
		Credential: policyv1.CredentialHandle_builder{
			Key:     proto.String(key),
			Version: proto.Uint64(version),
		}.Build(),
		Material: []byte("s3cr3t"),
	}.Build()
}

func hostTrust(key string, version uint64) *policyv1.HostTrustHandle {
	return policyv1.HostTrustHandle_builder{
		Key:     proto.String(key),
		Version: proto.Uint64(version),
	}.Build()
}

func TestAcquireReadCredentialRules(t *testing.T) {
	deviceID := "0192e6a0-0000-7000-8000-0000000000d1"
	bindingID := "0192e6a0-0000-7000-8000-0000000000b1"
	policy := policyv1.AccessPolicyHandle_builder{Key: proto.String("icx7150-lab"), Version: proto.Uint64(1)}.Build()

	runValidationCases(t, []validationCase{
		{
			name: "device, binding, and access policy present",
			message: edgev1.AcquireReadCredentialRequest_builder{
				DeviceId:     proto.String(deviceID),
				BindingId:    proto.String(bindingID),
				AccessPolicy: policy,
			}.Build(),
			wantValid: true,
		},
		{
			name: "missing access policy is rejected",
			message: edgev1.AcquireReadCredentialRequest_builder{
				DeviceId:  proto.String(deviceID),
				BindingId: proto.String(bindingID),
			}.Build(),
			wantValid: false,
		},
		{
			name: "device id must be a uuid",
			message: edgev1.AcquireReadCredentialRequest_builder{
				DeviceId:     proto.String("not-a-uuid"),
				BindingId:    proto.String(bindingID),
				AccessPolicy: policy,
			}.Build(),
			wantValid: false,
		},
		{
			name: "response with credential, host trust, and expiry",
			message: edgev1.AcquireReadCredentialResponse_builder{
				Credential: deviceCredential("icx7150-lab-snmp", 1),
				HostTrust:  hostTrust("icx7150-lab-hostkey", 1),
				ExpiresAt:  timestamppb.New(time.Now().Add(time.Minute)),
			}.Build(),
			wantValid: true,
		},
		{
			name: "response missing expires_at is rejected",
			message: edgev1.AcquireReadCredentialResponse_builder{
				Credential: deviceCredential("icx7150-lab-snmp", 1),
				HostTrust:  hostTrust("icx7150-lab-hostkey", 1),
			}.Build(),
			wantValid: false,
		},
	})
}

func TestOpenDeviceSubmissionRules(t *testing.T) {
	deviceID := "0192e6a0-0000-7000-8000-0000000000d1"
	bindingID := "0192e6a0-0000-7000-8000-0000000000b1"

	runValidationCases(t, []validationCase{
		{
			name: "sequence at least one is valid",
			message: edgev1.OpenDeviceSubmissionRequest_builder{
				DeviceId:  proto.String(deviceID),
				BindingId: proto.String(bindingID),
				Sequence:  proto.Uint64(1),
			}.Build(),
			wantValid: true,
		},
		{
			name: "sequence zero is rejected",
			message: edgev1.OpenDeviceSubmissionRequest_builder{
				DeviceId:  proto.String(deviceID),
				BindingId: proto.String(bindingID),
				Sequence:  proto.Uint64(0),
			}.Build(),
			wantValid: false,
		},
	})
}

func TestOpenDeviceSubmissionResponseRules(t *testing.T) {
	grant := edgev1.OpenDeviceSubmissionResponse_builder{
		Grant: edgev1.SubmissionGrant_builder{
			Credential: deviceCredential("icx7150-lab-submit", 1),
			HostTrust:  hostTrust("icx7150-lab-hostkey", 1),
			Deadline:   timestamppb.New(time.Now().Add(time.Minute)),
		}.Build(),
	}.Build()
	pulse := edgev1.OpenDeviceSubmissionResponse_builder{
		Pulse: edgev1.AuthorityPulse_builder{
			Authority: edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED.Enum(),
			Deadline:  timestamppb.New(time.Now().Add(time.Minute)),
		}.Build(),
	}.Build()

	// The stream delivers the grant once, then pulses; both message shapes
	// must independently validate for that sequence to be well-formed.
	stream := []*edgev1.OpenDeviceSubmissionResponse{grant, pulse}
	for i, msg := range stream {
		if err := protovalidate.Validate(msg); err != nil {
			t.Errorf("stream message %d: %v", i, err)
		}
	}

	runValidationCases(t, []validationCase{
		{
			name:      "grant is a valid first message",
			message:   grant,
			wantValid: true,
		},
		{
			name:      "pulse is a valid later message",
			message:   pulse,
			wantValid: true,
		},
		{
			name:      "neither grant nor pulse set is rejected",
			message:   edgev1.OpenDeviceSubmissionResponse_builder{}.Build(),
			wantValid: false,
		},
		{
			name: "authority pulse with the zero value is rejected",
			message: edgev1.OpenDeviceSubmissionResponse_builder{
				Pulse: edgev1.AuthorityPulse_builder{
					Deadline: timestamppb.New(time.Now().Add(time.Minute)),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
	})
}
