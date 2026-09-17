package conformance

import (
	"crypto/ed25519"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
)

const cloudIntegrationID = "0192e6a0-0000-7000-8000-0000000000c2"

func TestIntegrationConfigHostRules(t *testing.T) {
	local := func(edge *edgev1.EdgeGlobalRef) *inventoryv1.IntegrationConfig {
		return inventoryv1.IntegrationConfig_builder{
			Ref:           integrationRef(integrationID),
			CredentialRef: proto.String("secret/local"),
			Edge:          edge,
			LocalNetwork:  inventoryv1.LocalNetworkConfig_builder{}.Build(),
		}.Build()
	}
	runValidationCases(t, []validationCase{
		{
			name:      "local-network integration names its edge",
			message:   local(edgeRef()),
			wantValid: true,
		},
		{
			name:      "local-network integration without an edge is rejected",
			message:   local(nil),
			wantValid: false,
		},
		{
			name: "third-party integration may run centrally",
			message: inventoryv1.IntegrationConfig_builder{
				Ref:           integrationRef(cloudIntegrationID),
				CredentialRef: proto.String("secret/cloud"),
				ThirdParty: inventoryv1.ThirdPartyConfig_builder{
					KindName:           proto.String("meraki"),
					TypeName:           proto.String("vendor.meraki.v1.Config"),
					Value:              []byte{},
					DescriptorRevision: proto.String("r1"),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
	})
}

func TestEdgeRequestRules(t *testing.T) {
	proof := edgev1.KeyProof_builder{
		Payload:   []byte{1},
		Signature: make([]byte, ed25519.SignatureSize),
	}.Build()
	runValidationCases(t, []validationCase{
		{
			name: "well-formed enroll request is valid",
			message: apiedgev1.EnrollRequest_builder{
				SetupKey: proto.String(setupKey),
				Proof:    proof,
			}.Build(),
			wantValid: true,
		},
		{
			name: "enroll request needs a well-formed setup key",
			message: apiedgev1.EnrollRequest_builder{
				SetupKey: proto.String("fse1_abc"),
				Proof: edgev1.KeyProof_builder{
					Payload:   []byte{1},
					Signature: make([]byte, ed25519.SignatureSize),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "heartbeat with a version is valid",
			message: apiedgev1.HeartbeatRequest_builder{
				AgentVersion: proto.String("0.1.0"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "create edge with a future setup key expiry is valid",
			message: apiedgev1.CreateEdgeRequest_builder{
				Name:              proto.String("Berlin DC-1"),
				SetupKeyExpiresAt: timestamppb.New(time.Now().Add(time.Hour)),
			}.Build(),
			wantValid: true,
		},
		{
			name: "create edge with a past setup key expiry is rejected",
			message: apiedgev1.CreateEdgeRequest_builder{
				SetupKeyExpiresAt: timestamppb.New(time.Now().Add(-time.Hour)),
			}.Build(),
			wantValid: false,
		},
		{
			name: "issue setup key with a past expiry is rejected",
			message: apiedgev1.IssueSetupKeyRequest_builder{
				Edge:      edgeRef(),
				ExpiresAt: timestamppb.New(time.Now().Add(-time.Hour)),
			}.Build(),
			wantValid: false,
		},
		{
			name: "list page size above the cap is rejected",
			message: apiedgev1.ListEdgesRequest_builder{
				PageSize: proto.Uint32(501),
			}.Build(),
			wantValid: false,
		},
		{
			name:      "list request with defaults is valid",
			message:   apiedgev1.ListEdgesRequest_builder{}.Build(),
			wantValid: true,
		},
	})
}
