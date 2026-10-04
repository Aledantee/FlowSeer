package conformance

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiidentityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
)

const (
	testTenantID = "0192e6a0-0000-7000-8000-000000000001"
)

func validTenantRef() *identityv1.TenantGlobalRef {
	return identityv1.TenantGlobalRef_builder{
		Tenant: identityv1.TenantLocalRef_builder{
			Id: proto.String(testTenantID),
		}.Build(),
	}.Build()
}

func validTenantConfig() *identityv1.TenantConfig {
	return identityv1.TenantConfig_builder{
		Ref:                    validTenantRef(),
		Issuer:                 proto.String("https://idp.example.com"),
		OrganizationClaimName:  proto.String("org_id"),
		OrganizationClaimValue: proto.String("org-123"),
		Name:                   proto.String("Acme Corp"),
		Description:            proto.String("Primary tenant"),
	}.Build()
}

func TestModelIdentityRules(t *testing.T) {
	runValidationCases(t, []validationCase{
		{
			name:      "valid tenant config with issuer and organization claim validates",
			message:   validTenantConfig(),
			wantValid: true,
		},
		{
			name: "tenant config without issuer fails validation",
			message: identityv1.TenantConfig_builder{
				Ref:                    validTenantRef(),
				OrganizationClaimName:  proto.String("org_id"),
				OrganizationClaimValue: proto.String("org-123"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "tenant config without organization claim name fails",
			message: identityv1.TenantConfig_builder{
				Ref:                    validTenantRef(),
				Issuer:                 proto.String("https://idp.example.com"),
				OrganizationClaimValue: proto.String("org-123"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "tenant config without organization claim value fails",
			message: identityv1.TenantConfig_builder{
				Ref:                   validTenantRef(),
				Issuer:                proto.String("https://idp.example.com"),
				OrganizationClaimName: proto.String("org_id"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "tenant local ref with non-UUID string fails",
			message: identityv1.TenantLocalRef_builder{
				Id: proto.String("not-a-valid-uuid"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "tenant state with unspecified lifecycle fails",
			message: identityv1.TenantState_builder{
				Ref:       validTenantRef(),
				Lifecycle: identityv1.TenantLifecycle_TENANT_LIFECYCLE_UNSPECIFIED.Enum(),
				CreatedAt: timestamppb.New(time.Now()),
			}.Build(),
			wantValid: false,
		},
		{
			name: "tenant state without created_at fails",
			message: identityv1.TenantState_builder{
				Ref:       validTenantRef(),
				Lifecycle: identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "tenant event with from equal to to fails",
			message: identityv1.TenantEvent_builder{
				Ref:  validTenantRef(),
				From: identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
				To:   identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "tenant event without to fails",
			message: identityv1.TenantEvent_builder{
				Ref:  validTenantRef(),
				From: identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "valid tenant state validates",
			message: identityv1.TenantState_builder{
				Ref:       validTenantRef(),
				Lifecycle: identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
				CreatedAt: timestamppb.New(time.Now()),
			}.Build(),
			wantValid: true,
		},
		{
			name: "valid tenant creation event with from unset validates",
			message: identityv1.TenantEvent_builder{
				Ref: validTenantRef(),
				To:  identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "valid tenant event with distinct lifecycle validates",
			message: identityv1.TenantEvent_builder{
				Ref:  validTenantRef(),
				From: identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
				To:   identityv1.TenantLifecycle_TENANT_LIFECYCLE_SUSPENDED.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "valid create tenant request validates",
			message: apiidentityv1.CreateTenantRequest_builder{
				Issuer:                 proto.String("https://idp.example.com"),
				OrganizationClaimName:  proto.String("org_id"),
				OrganizationClaimValue: proto.String("org-123"),
				Name:                   proto.String("Acme Corp"),
				Description:            proto.String("Primary tenant"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "create tenant request without issuer fails",
			message: apiidentityv1.CreateTenantRequest_builder{
				OrganizationClaimName:  proto.String("org_id"),
				OrganizationClaimValue: proto.String("org-123"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "create tenant request with invalid issuer uri fails",
			message: apiidentityv1.CreateTenantRequest_builder{
				Issuer:                 proto.String("not-a-uri"),
				OrganizationClaimName:  proto.String("org_id"),
				OrganizationClaimValue: proto.String("org-123"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "create tenant request without organization claim name fails",
			message: apiidentityv1.CreateTenantRequest_builder{
				Issuer:                 proto.String("https://idp.example.com"),
				OrganizationClaimValue: proto.String("org-123"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "create tenant request without organization claim value fails",
			message: apiidentityv1.CreateTenantRequest_builder{
				Issuer:                proto.String("https://idp.example.com"),
				OrganizationClaimName: proto.String("org_id"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "valid operator ref validates",
			message: identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://idp.example.com"),
				Subject: proto.String("user-1"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "operator ref without issuer fails",
			message: identityv1.OperatorRef_builder{
				Subject: proto.String("user-1"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "operator ref with invalid issuer uri fails",
			message: identityv1.OperatorRef_builder{
				Issuer:  proto.String("not a uri"),
				Subject: proto.String("user-1"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "operator ref with 2048-character issuer validates",
			message: identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://idp.example.com/" + strings.Repeat("a", 2048-len("https://idp.example.com/"))),
				Subject: proto.String("user-1"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "operator ref with 2049-character issuer fails",
			message: identityv1.OperatorRef_builder{
				Issuer:  proto.String("https://idp.example.com/" + strings.Repeat("a", 2049-len("https://idp.example.com/"))),
				Subject: proto.String("user-1"),
			}.Build(),
			wantValid: false,
		},
	})
}
