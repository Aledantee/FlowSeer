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

func validAccessRoleRef() *identityv1.RoleGlobalRef {
	return identityv1.RoleGlobalRef_builder{
		Role: identityv1.RoleLocalRef_builder{
			Id: proto.String("0192e6a0-0000-7000-8000-0000000000b1"),
		}.Build(),
	}.Build()
}

func validAccessMember() *identityv1.Member {
	return identityv1.Member_builder{
		Operator:   identityv1.OperatorRef_builder{Issuer: proto.String("https://idp.example.com"), Subject: proto.String("member-1")}.Build(),
		EnrolledAt: timestamppb.New(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)),
		EnrolledBy: identityv1.OperatorRef_builder{Issuer: proto.String("https://idp.example.com"), Subject: proto.String("admin-1")}.Build(),
		Roles:      []*identityv1.RoleGlobalRef{validAccessRoleRef()},
	}.Build()
}

func TestAccessRecordsRules(t *testing.T) {
	validRole := identityv1.Role_builder{
		Ref:         validAccessRoleRef(),
		Name:        proto.String("capture operator"),
		Description: proto.String("May operate captures"),
		Relations:   []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_OPERATOR},
	}.Build()
	validGrant := identityv1.FullPayloadGrant_builder{
		ExpiresAt: timestamppb.New(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)),
		Reason:    proto.String("case 42"),
		GrantedBy: validOperatorRef(),
		GrantedAt: timestamppb.New(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)),
	}.Build()
	validPartner := identityv1.Partner_builder{
		Tenant:      validTenantRef(),
		Relations:   []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER},
		ConnectedAt: timestamppb.New(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)),
		ConnectedBy: validOperatorRef(),
	}.Build()

	roleWithoutRelations := proto.Clone(validRole).(*identityv1.Role)
	roleWithoutRelations.SetRelations(nil)
	roleWithoutName := proto.Clone(validRole).(*identityv1.Role)
	roleWithoutName.SetName("")
	roleWithLongName := proto.Clone(validRole).(*identityv1.Role)
	roleWithLongName.SetName(strings.Repeat("n", 129))
	roleWithRepeatedRelation := proto.Clone(validRole).(*identityv1.Role)
	roleWithRepeatedRelation.SetRelations([]identityv1.TenantRelation{
		identityv1.TenantRelation_TENANT_RELATION_OPERATOR,
		identityv1.TenantRelation_TENANT_RELATION_OPERATOR,
	})
	roleWithUndefinedRelation := proto.Clone(validRole).(*identityv1.Role)
	roleWithUndefinedRelation.SetRelations([]identityv1.TenantRelation{identityv1.TenantRelation(99)})
	grantWithoutReason := proto.Clone(validGrant).(*identityv1.FullPayloadGrant)
	grantWithoutReason.SetReason("")
	grantWithLongReason := proto.Clone(validGrant).(*identityv1.FullPayloadGrant)
	grantWithLongReason.SetReason(strings.Repeat("r", 513))
	partnerWithAdmin := proto.Clone(validPartner).(*identityv1.Partner)
	partnerWithAdmin.SetRelations([]identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_ADMIN})
	memberWithTooManyRoles := proto.Clone(validAccessMember()).(*identityv1.Member)
	memberWithTooManyRoles.SetRoles(make([]*identityv1.RoleGlobalRef, 65))

	runValidationCases(t, []validationCase{
		{name: "role record validates", message: validRole, wantValid: true},
		{name: "member record validates", message: validAccessMember(), wantValid: true},
		{name: "full payload grant validates", message: validGrant, wantValid: true},
		{name: "partner record validates", message: validPartner, wantValid: true},
		{name: "role without a name is rejected", message: roleWithoutName},
		{name: "role with a 129-character name is rejected", message: roleWithLongName},
		{name: "role without a relation is rejected", message: roleWithoutRelations},
		{name: "role with a repeated relation is rejected", message: roleWithRepeatedRelation},
		{name: "role with an undefined relation is rejected", message: roleWithUndefinedRelation},
		{name: "full payload grant without a reason is rejected", message: grantWithoutReason},
		{name: "full payload grant with a 513-character reason is rejected", message: grantWithLongReason},
		{name: "partner with admin is rejected", message: partnerWithAdmin},
		{name: "member with 65 roles is rejected", message: memberWithTooManyRoles},
	})
}
