package conformance

import (
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/credential/v1"
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

func deviceCredential(key string) *edgev1.DeviceCredential {
	return edgev1.DeviceCredential_builder{
		Credential: policyv1.CredentialHandle_builder{
			Key:     proto.String(key),
			Version: proto.Uint64(1),
		}.Build(),
		TypedMaterial: snmpMaterial(),
	}.Build()
}

func shellDeviceCredential(key string) *edgev1.DeviceCredential {
	return edgev1.DeviceCredential_builder{
		Credential: policyv1.CredentialHandle_builder{
			Key:     proto.String(key),
			Version: proto.Uint64(1),
		}.Build(),
		TypedMaterial: shellMaterial(),
	}.Build()
}

const hostKeyPin = "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU"

func hostTrust(key string) *policyv1.HostTrustHandle {
	return policyv1.HostTrustHandle_builder{
		Key:     proto.String(key),
		Version: proto.Uint64(1),
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
				Credential: deviceCredential("icx7150-lab-snmp"),
				HostTrust:  hostTrust("icx7150-lab-hostkey"),
				ExpiresAt:  timestamppb.New(time.Now().Add(time.Minute)),
			}.Build(),
			wantValid: true,
		},
		{
			name: "response missing expires_at is rejected",
			message: edgev1.AcquireReadCredentialResponse_builder{
				Credential: deviceCredential("icx7150-lab-snmp"),
				HostTrust:  hostTrust("icx7150-lab-hostkey"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "shell material carries the host key pin",
			message: edgev1.AcquireReadCredentialResponse_builder{
				Credential:       shellDeviceCredential("icx7150-lab-ssh"),
				HostTrust:        hostTrust("icx7150-lab-hostkey"),
				ExpiresAt:        timestamppb.New(time.Now().Add(time.Minute)),
				SshHostKeySha256: proto.String(hostKeyPin),
			}.Build(),
			wantValid: true,
		},
		{
			name: "shell material without a pin is rejected",
			message: edgev1.AcquireReadCredentialResponse_builder{
				Credential: shellDeviceCredential("icx7150-lab-ssh"),
				HostTrust:  hostTrust("icx7150-lab-hostkey"),
				ExpiresAt:  timestamppb.New(time.Now().Add(time.Minute)),
			}.Build(),
			wantValid: false,
		},
		{
			name: "snmp material with a pin is rejected",
			message: edgev1.AcquireReadCredentialResponse_builder{
				Credential:       deviceCredential("icx7150-lab-snmp"),
				HostTrust:        hostTrust("icx7150-lab-hostkey"),
				ExpiresAt:        timestamppb.New(time.Now().Add(time.Minute)),
				SshHostKeySha256: proto.String(hostKeyPin),
			}.Build(),
			wantValid: false,
		},
		{
			name: "a pin that is not a sha256 fingerprint is rejected",
			message: edgev1.AcquireReadCredentialResponse_builder{
				Credential:       shellDeviceCredential("icx7150-lab-ssh"),
				HostTrust:        hostTrust("icx7150-lab-hostkey"),
				ExpiresAt:        timestamppb.New(time.Now().Add(time.Minute)),
				SshHostKeySha256: proto.String("MD5:aa:bb"),
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
			Credential: deviceCredential("icx7150-lab-submit"),
			HostTrust:  hostTrust("icx7150-lab-hostkey"),
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

	shellGrant := edgev1.SubmissionGrant_builder{
		Credential:       shellDeviceCredential("icx7150-lab-submit"),
		HostTrust:        hostTrust("icx7150-lab-hostkey"),
		Deadline:         timestamppb.New(time.Now().Add(time.Minute)),
		SshHostKeySha256: proto.String(hostKeyPin),
	}.Build()
	shellGrantWithoutPin := edgev1.SubmissionGrant_builder{
		Credential: shellDeviceCredential("icx7150-lab-submit"),
		HostTrust:  hostTrust("icx7150-lab-hostkey"),
		Deadline:   timestamppb.New(time.Now().Add(time.Minute)),
	}.Build()

	snmpGrantWithPin := edgev1.SubmissionGrant_builder{
		Credential:       deviceCredential("icx7150-lab-submit"),
		HostTrust:        hostTrust("icx7150-lab-hostkey"),
		Deadline:         timestamppb.New(time.Now().Add(time.Minute)),
		SshHostKeySha256: proto.String(hostKeyPin),
	}.Build()
	unprefixedPin := edgev1.SubmissionGrant_builder{
		Credential:       shellDeviceCredential("icx7150-lab-submit"),
		HostTrust:        hostTrust("icx7150-lab-hostkey"),
		Deadline:         timestamppb.New(time.Now().Add(time.Minute)),
		SshHostKeySha256: proto.String("47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="),
	}.Build()

	runValidationCases(t, []validationCase{
		{name: "shell grant carries the host key pin", message: shellGrant, wantValid: true},
		{name: "shell grant without a pin is rejected", message: shellGrantWithoutPin},
		{name: "snmp grant with a pin is rejected", message: snmpGrantWithPin},
		{name: "a pin without the SHA256 prefix is rejected", message: unprefixedPin},
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

func snmpMaterial() *credentialv1.CredentialMaterial {
	return credentialv1.CredentialMaterial_builder{
		SnmpV3: credentialv1.SnmpV3Credential_builder{
			User:           proto.String("flowseer-ro"),
			AuthProtocol:   credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA256.Enum(),
			AuthPassphrase: proto.String("auth-passphrase"),
			PrivProtocol:   credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_AES128.Enum(),
			PrivPassphrase: proto.String("priv-passphrase"),
		}.Build(),
	}.Build()
}

func shellMaterial() *credentialv1.CredentialMaterial {
	return credentialv1.CredentialMaterial_builder{
		Shell: credentialv1.ShellCredential_builder{
			Username: proto.String("flowseer"),
			Password: proto.String("shell-password"),
		}.Build(),
	}.Build()
}
