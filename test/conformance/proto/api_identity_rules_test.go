package conformance

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	apiidentityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
)

func validGrantFullPayloadRequest() *apiidentityv1.GrantFullPayloadRequest {
	return apiidentityv1.GrantFullPayloadRequest_builder{
		Member:   validOperatorRef(),
		Lifetime: durationpb.New(time.Hour),
		Reason:   proto.String("case 42"),
	}.Build()
}

func TestTenantAdminRequestRefsAreRequired(t *testing.T) {
	validMember := validOperatorRef()
	validRole := validAccessRoleRef()
	validPartner := validTenantRef()

	cases := []validationCase{
		{name: "enroll member requires member", message: apiidentityv1.EnrollMemberRequest_builder{}.Build()},
		{name: "remove member requires member", message: apiidentityv1.RemoveMemberRequest_builder{}.Build()},
		{name: "delete role requires role", message: apiidentityv1.DeleteRoleRequest_builder{}.Build()},
		{name: "assign role requires member", message: apiidentityv1.AssignRoleRequest_builder{Role: validRole}.Build()},
		{name: "assign role requires role", message: apiidentityv1.AssignRoleRequest_builder{Member: validMember}.Build()},
		{name: "unassign role requires member", message: apiidentityv1.UnassignRoleRequest_builder{Role: validRole}.Build()},
		{name: "unassign role requires role", message: apiidentityv1.UnassignRoleRequest_builder{Member: validMember}.Build()},
		{name: "connect partner requires partner", message: apiidentityv1.ConnectPartnerRequest_builder{Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER}}.Build()},
		{name: "disconnect partner requires partner", message: apiidentityv1.DisconnectPartnerRequest_builder{}.Build()},
		{name: "grant full payload requires member", message: apiidentityv1.GrantFullPayloadRequest_builder{Lifetime: durationpb.New(time.Hour), Reason: proto.String("case 42")}.Build()},
		{name: "revoke full payload requires member", message: apiidentityv1.RevokeFullPayloadRequest_builder{}.Build()},
		{name: "valid enroll member request", message: apiidentityv1.EnrollMemberRequest_builder{Member: validMember}.Build(), wantValid: true},
		{name: "valid remove member request", message: apiidentityv1.RemoveMemberRequest_builder{Member: validMember}.Build(), wantValid: true},
		{name: "valid delete role request", message: apiidentityv1.DeleteRoleRequest_builder{Role: validRole}.Build(), wantValid: true},
		{name: "valid assign role request", message: apiidentityv1.AssignRoleRequest_builder{Member: validMember, Role: validRole}.Build(), wantValid: true},
		{name: "valid unassign role request", message: apiidentityv1.UnassignRoleRequest_builder{Member: validMember, Role: validRole}.Build(), wantValid: true},
		{name: "valid connect partner request", message: apiidentityv1.ConnectPartnerRequest_builder{Partner: validPartner, Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER}}.Build(), wantValid: true},
		{name: "valid disconnect partner request", message: apiidentityv1.DisconnectPartnerRequest_builder{Partner: validPartner}.Build(), wantValid: true},
		{name: "valid grant full payload request", message: validGrantFullPayloadRequest(), wantValid: true},
		{name: "valid revoke full payload request", message: apiidentityv1.RevokeFullPayloadRequest_builder{Member: validMember}.Build(), wantValid: true},
	}

	runValidationCases(t, cases)
}

func validCreateRoleRequest() *apiidentityv1.CreateRoleRequest {
	return apiidentityv1.CreateRoleRequest_builder{
		Name:      proto.String("capture operator"),
		Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_OPERATOR},
	}.Build()
}

func TestCreateRoleRequestRules(t *testing.T) {
	noName := validCreateRoleRequest()
	noName.ClearName()
	emptyName := validCreateRoleRequest()
	emptyName.SetName("")
	longName := validCreateRoleRequest()
	longName.SetName(strings.Repeat("n", 129))
	emptyDescription := validCreateRoleRequest()
	emptyDescription.SetDescription("")
	longDescription := validCreateRoleRequest()
	longDescription.SetDescription(strings.Repeat("d", 2049))
	noRelations := validCreateRoleRequest()
	noRelations.SetRelations(nil)
	repeatedRelation := validCreateRoleRequest()
	repeatedRelation.SetRelations([]identityv1.TenantRelation{
		identityv1.TenantRelation_TENANT_RELATION_OPERATOR,
		identityv1.TenantRelation_TENANT_RELATION_OPERATOR,
	})
	undefinedRelation := validCreateRoleRequest()
	undefinedRelation.SetRelations([]identityv1.TenantRelation{identityv1.TenantRelation(99)})

	runValidationCases(t, []validationCase{
		{name: "valid create role request validates", message: validCreateRoleRequest(), wantValid: true},
		{name: "create role request without a name is rejected", message: noName},
		{name: "create role request with an empty name is rejected", message: emptyName},
		{name: "create role request with a 129-character name is rejected", message: longName},
		{name: "create role request with an empty description is rejected", message: emptyDescription},
		{name: "create role request with a 2049-character description is rejected", message: longDescription},
		{name: "create role request without relations is rejected", message: noRelations},
		{name: "create role request with a repeated relation is rejected", message: repeatedRelation},
		{name: "create role request with an undefined relation is rejected", message: undefinedRelation},
	})
}

func TestGrantFullPayloadReasonBounds(t *testing.T) {
	empty := validGrantFullPayloadRequest()
	empty.SetReason("")
	long := validGrantFullPayloadRequest()
	long.SetReason(strings.Repeat("r", 513))

	runValidationCases(t, []validationCase{
		{name: "empty full-payload reason is rejected", message: empty},
		{name: "513-character full-payload reason is rejected", message: long},
	})
}

func TestTenantAdminListPageSizeBounds(t *testing.T) {
	runValidationCases(t, []validationCase{
		{name: "members page size 500 validates", message: apiidentityv1.ListMembersRequest_builder{PageSize: proto.Uint32(500)}.Build(), wantValid: true},
		{name: "members page size 501 is rejected", message: apiidentityv1.ListMembersRequest_builder{PageSize: proto.Uint32(501)}.Build()},
		{name: "roles page size 500 validates", message: apiidentityv1.ListRolesRequest_builder{PageSize: proto.Uint32(500)}.Build(), wantValid: true},
		{name: "roles page size 501 is rejected", message: apiidentityv1.ListRolesRequest_builder{PageSize: proto.Uint32(501)}.Build()},
		{name: "partners page size 500 validates", message: apiidentityv1.ListPartnersRequest_builder{PageSize: proto.Uint32(500)}.Build(), wantValid: true},
		{name: "partners page size 501 is rejected", message: apiidentityv1.ListPartnersRequest_builder{PageSize: proto.Uint32(501)}.Build()},
	})
}

func TestGrantFullPayloadLifetimeBounds(t *testing.T) {
	zero := validGrantFullPayloadRequest()
	zero.SetLifetime(durationpb.New(0))
	tooLong := validGrantFullPayloadRequest()
	tooLong.SetLifetime(durationpb.New(24*time.Hour + time.Second))
	unset := validGrantFullPayloadRequest()
	unset.ClearLifetime()

	runValidationCases(t, []validationCase{
		{name: "positive lifetime within one day validates", message: validGrantFullPayloadRequest(), wantValid: true},
		{name: "zero lifetime is rejected", message: zero},
		{name: "lifetime above 86400 seconds is rejected", message: tooLong},
		{name: "unset lifetime is rejected", message: unset},
	})
}
