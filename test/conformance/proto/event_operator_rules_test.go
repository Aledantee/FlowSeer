package conformance

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	operatorv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/operator/v1"
	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
)

const (
	testOperatorEventID = "0192e6a0-0000-7000-8000-00000000e001"
	testCallID          = "0192e6a0-0000-7000-8000-00000000c001"
	testOperatorEdgeID  = "0192e6a0-0000-7000-8000-0000000000ed"
	testSessionID       = "0192e6a0-0000-7000-8000-000000000051"
)

func validOperatorRef() *identityv1.OperatorRef {
	return identityv1.OperatorRef_builder{
		Issuer:  proto.String("https://idp.example.com"),
		Subject: proto.String("operator-1"),
	}.Build()
}

func validOperatorEdgeGlobalRef() *edgev1.EdgeGlobalRef {
	return edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{
			Id: proto.String(testOperatorEdgeID),
		}.Build(),
	}.Build()
}

func validOperatorCaptureSessionGlobalRef() *capturev1.CaptureSessionGlobalRef {
	return capturev1.CaptureSessionGlobalRef_builder{
		Edge: validOperatorEdgeGlobalRef(),
		CaptureSession: capturev1.CaptureSessionLocalRef_builder{
			Id: proto.String(testSessionID),
		}.Build(),
	}.Build()
}

func validOperatorActionEvent() operatorv1.OperatorActionEvent_builder {
	return operatorv1.OperatorActionEvent_builder{
		EventId:    proto.String(testOperatorEventID),
		CallId:     proto.String(testCallID),
		OccurredAt: timestamppb.New(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)),
		Operator:   validOperatorRef(),
		Action:     operatorv1.OperatorAction_OPERATOR_ACTION_SETUP_KEY_ISSUE.Enum(),
		Edge:       validOperatorEdgeGlobalRef(),
		Attempted:  operatorv1.OperatorActionAttempted_builder{}.Build(),
	}
}

func TestOperatorActionEventRules(t *testing.T) {
	noEventID := validOperatorActionEvent()
	noEventID.EventId = nil

	badEventID := validOperatorActionEvent()
	badEventID.EventId = proto.String("not-a-uuid")

	noCallID := validOperatorActionEvent()
	noCallID.CallId = nil

	badCallID := validOperatorActionEvent()
	badCallID.CallId = proto.String("not-a-uuid")

	noOccurredAt := validOperatorActionEvent()
	noOccurredAt.OccurredAt = nil

	noOperator := validOperatorActionEvent()
	noOperator.Operator = nil

	noAction := validOperatorActionEvent()
	noAction.Action = nil

	unspecifiedAction := validOperatorActionEvent()
	unspecifiedAction.Action = operatorv1.OperatorAction_OPERATOR_ACTION_UNSPECIFIED.Enum()

	undefinedAction := validOperatorActionEvent()
	undefinedAction.Action = operatorv1.OperatorAction(99).Enum()

	noDetail := validOperatorActionEvent()
	noDetail.Attempted = nil

	edgeListNoObject := validOperatorActionEvent()
	edgeListNoObject.Action = operatorv1.OperatorAction_OPERATOR_ACTION_EDGE_LIST.Enum()
	edgeListNoObject.Edge = nil

	captureSessionObject := validOperatorActionEvent()
	captureSessionObject.Action = operatorv1.OperatorAction_OPERATOR_ACTION_CAPTURE_TAIL.Enum()
	captureSessionObject.Edge = nil
	captureSessionObject.CaptureSession = validOperatorCaptureSessionGlobalRef()

	completedSucceeded := validOperatorActionEvent()
	completedSucceeded.Attempted = nil
	completedSucceeded.Completed = operatorv1.OperatorActionCompleted_builder{
		Outcome: operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED.Enum(),
	}.Build()

	completedDenied := validOperatorActionEvent()
	completedDenied.Attempted = nil
	completedDenied.Completed = operatorv1.OperatorActionCompleted_builder{
		Outcome:   operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_DENIED.Enum(),
		ErrorType: proto.String("authz/denied"),
	}.Build()

	completedFailed := validOperatorActionEvent()
	completedFailed.Attempted = nil
	completedFailed.Completed = operatorv1.OperatorActionCompleted_builder{
		Outcome:   operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_FAILED.Enum(),
		ErrorType: proto.String("actiontrail/unavailable"),
	}.Build()

	tests := []validationCase{
		{name: "attempted event with edge validates", message: validOperatorActionEvent().Build(), wantValid: true},
		{name: "attempted event without object validates", message: edgeListNoObject.Build(), wantValid: true},
		{name: "attempted event with capture session object validates", message: captureSessionObject.Build(), wantValid: true},
		{name: "completed succeeded event validates", message: completedSucceeded.Build(), wantValid: true},
		{name: "completed denied event validates", message: completedDenied.Build(), wantValid: true},
		{name: "completed failed event validates", message: completedFailed.Build(), wantValid: true},
		{name: "missing event_id is rejected", message: noEventID.Build(), wantValid: false},
		{name: "non-uuid event_id is rejected", message: badEventID.Build(), wantValid: false},
		{name: "missing call_id is rejected", message: noCallID.Build(), wantValid: false},
		{name: "non-uuid call_id is rejected", message: badCallID.Build(), wantValid: false},
		{name: "missing occurred_at is rejected", message: noOccurredAt.Build(), wantValid: false},
		{name: "missing operator is rejected", message: noOperator.Build(), wantValid: false},
		{name: "missing action is rejected", message: noAction.Build(), wantValid: false},
		{name: "unspecified action is rejected", message: unspecifiedAction.Build(), wantValid: false},
		{name: "undefined action is rejected", message: undefinedAction.Build(), wantValid: false},
		{name: "missing detail is rejected", message: noDetail.Build(), wantValid: false},
	}

	runValidationCases(t, tests)
}

func TestOperatorActionCompletedRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "succeeded without error_type is valid",
			message: operatorv1.OperatorActionCompleted_builder{
				Outcome: operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "succeeded with error_type is rejected",
			message: operatorv1.OperatorActionCompleted_builder{
				Outcome:   operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED.Enum(),
				ErrorType: proto.String("unexpected_error"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "denied with error_type is valid",
			message: operatorv1.OperatorActionCompleted_builder{
				Outcome:   operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_DENIED.Enum(),
				ErrorType: proto.String("authz/denied"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "denied without error_type is rejected",
			message: operatorv1.OperatorActionCompleted_builder{
				Outcome: operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_DENIED.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "failed with error_type is valid",
			message: operatorv1.OperatorActionCompleted_builder{
				Outcome:   operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_FAILED.Enum(),
				ErrorType: proto.String("actiontrail/unavailable"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "failed without error_type is rejected",
			message: operatorv1.OperatorActionCompleted_builder{
				Outcome: operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_FAILED.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "unspecified outcome is rejected",
			message: operatorv1.OperatorActionCompleted_builder{
				Outcome:   operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_UNSPECIFIED.Enum(),
				ErrorType: proto.String("some/error"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "missing outcome is rejected",
			message: operatorv1.OperatorActionCompleted_builder{
				ErrorType: proto.String("some/error"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "undefined outcome is rejected",
			message: operatorv1.OperatorActionCompleted_builder{
				Outcome:   operatorv1.OperatorActionOutcome(99).Enum(),
				ErrorType: proto.String("some/error"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "empty error_type is rejected",
			message: operatorv1.OperatorActionCompleted_builder{
				Outcome:   operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_FAILED.Enum(),
				ErrorType: proto.String(""),
			}.Build(),
			wantValid: false,
		},
		{
			name: "error_type exceeding 128 characters is rejected",
			message: operatorv1.OperatorActionCompleted_builder{
				Outcome:   operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_FAILED.Enum(),
				ErrorType: proto.String(strings.Repeat("e", 129)),
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestOperatorActionNewObjectArms(t *testing.T) {
	role := operatorv1.OperatorActionRole_builder{
		Role:      validAccessRoleRef(),
		Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_OPERATOR},
	}.Build()
	assignment := operatorv1.OperatorActionRoleAssignment_builder{
		Role:   validAccessRoleRef(),
		Member: validOperatorRef(),
	}.Build()
	partner := operatorv1.OperatorActionPartner_builder{
		Tenant:    validTenantRef(),
		Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER},
	}.Build()
	grant := operatorv1.OperatorActionFullPayloadGrant_builder{
		Member:    validOperatorRef(),
		ExpiresAt: timestamppb.New(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)),
	}.Build()

	base := validOperatorActionEvent()
	base.Edge = nil
	base.Action = operatorv1.OperatorAction_OPERATOR_ACTION_TENANT_CREATE.Enum()
	base.Tenant = validTenantRef()

	member := validOperatorActionEvent()
	member.Edge = nil
	member.Action = operatorv1.OperatorAction_OPERATOR_ACTION_MEMBER_ENROLL.Enum()
	member.Member = validOperatorRef()

	roleEvent := validOperatorActionEvent()
	roleEvent.Edge = nil
	roleEvent.Action = operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_CREATE.Enum()
	roleEvent.Role = role

	deletedRoleEvent := validOperatorActionEvent()
	deletedRoleEvent.Edge = nil
	deletedRoleEvent.Action = operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_DELETE.Enum()
	deletedRoleEvent.Role = operatorv1.OperatorActionRole_builder{
		Role: validAccessRoleRef(),
	}.Build()

	roleCreateWithoutRelations := validOperatorActionEvent()
	roleCreateWithoutRelations.Edge = nil
	roleCreateWithoutRelations.Action = operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_CREATE.Enum()
	roleCreateWithoutRelations.Role = operatorv1.OperatorActionRole_builder{
		Role: validAccessRoleRef(),
	}.Build()
	roleDeleteWithRelations := validOperatorActionEvent()
	roleDeleteWithRelations.Edge = nil
	roleDeleteWithRelations.Action = operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_DELETE.Enum()
	roleDeleteWithRelations.Role = operatorv1.OperatorActionRole_builder{
		Role:      validAccessRoleRef(),
		Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_OPERATOR},
	}.Build()
	roleWithDuplicateRelations := validOperatorActionEvent()
	roleWithDuplicateRelations.Edge = nil
	roleWithDuplicateRelations.Action = operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_CREATE.Enum()
	roleWithDuplicateRelations.Role = operatorv1.OperatorActionRole_builder{
		Role: validAccessRoleRef(),
		Relations: []identityv1.TenantRelation{
			identityv1.TenantRelation_TENANT_RELATION_OPERATOR,
			identityv1.TenantRelation_TENANT_RELATION_OPERATOR,
		},
	}.Build()
	roleWithUndefinedRelation := validOperatorActionEvent()
	roleWithUndefinedRelation.Edge = nil
	roleWithUndefinedRelation.Action = operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_CREATE.Enum()
	roleWithUndefinedRelation.Role = operatorv1.OperatorActionRole_builder{
		Role:      validAccessRoleRef(),
		Relations: []identityv1.TenantRelation{identityv1.TenantRelation(99)},
	}.Build()
	roleWithUnspecifiedRelation := validOperatorActionEvent()
	roleWithUnspecifiedRelation.Edge = nil
	roleWithUnspecifiedRelation.Action = operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_CREATE.Enum()
	roleWithUnspecifiedRelation.Role = operatorv1.OperatorActionRole_builder{
		Role:      validAccessRoleRef(),
		Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_UNSPECIFIED},
	}.Build()

	assignmentEvent := validOperatorActionEvent()
	assignmentEvent.Edge = nil
	assignmentEvent.Action = operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_ASSIGN.Enum()
	assignmentEvent.RoleAssignment = assignment

	partnerEvent := validOperatorActionEvent()
	partnerEvent.Edge = nil
	partnerEvent.Action = operatorv1.OperatorAction_OPERATOR_ACTION_PARTNER_CONNECT.Enum()
	partnerEvent.Partner = partner
	partnerWithAdminRelation := validOperatorActionEvent()
	partnerWithAdminRelation.Edge = nil
	partnerWithAdminRelation.Action = operatorv1.OperatorAction_OPERATOR_ACTION_PARTNER_CONNECT.Enum()
	partnerWithAdminRelation.Partner = operatorv1.OperatorActionPartner_builder{
		Tenant:    validTenantRef(),
		Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_ADMIN},
	}.Build()

	grantEvent := validOperatorActionEvent()
	grantEvent.Edge = nil
	grantEvent.Action = operatorv1.OperatorAction_OPERATOR_ACTION_FULL_PAYLOAD_GRANT.Enum()
	grantEvent.FullPayloadGrant = grant

	roleWithoutRef := validOperatorActionEvent()
	roleWithoutRef.Edge = nil
	roleWithoutRef.Action = operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_CREATE.Enum()
	roleWithoutRef.Role = operatorv1.OperatorActionRole_builder{
		Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_OPERATOR},
	}.Build()
	assignmentWithoutMember := validOperatorActionEvent()
	assignmentWithoutMember.Edge = nil
	assignmentWithoutMember.Action = operatorv1.OperatorAction_OPERATOR_ACTION_ROLE_ASSIGN.Enum()
	assignmentWithoutMember.RoleAssignment = operatorv1.OperatorActionRoleAssignment_builder{
		Role: validAccessRoleRef(),
	}.Build()
	partnerWithoutTenant := validOperatorActionEvent()
	partnerWithoutTenant.Edge = nil
	partnerWithoutTenant.Action = operatorv1.OperatorAction_OPERATOR_ACTION_PARTNER_CONNECT.Enum()
	partnerWithoutTenant.Partner = operatorv1.OperatorActionPartner_builder{
		Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER},
	}.Build()
	grantWithoutExpiry := validOperatorActionEvent()
	grantWithoutExpiry.Edge = nil
	grantWithoutExpiry.Action = operatorv1.OperatorAction_OPERATOR_ACTION_FULL_PAYLOAD_GRANT.Enum()
	grantWithoutExpiry.FullPayloadGrant = operatorv1.OperatorActionFullPayloadGrant_builder{
		Member: validOperatorRef(),
	}.Build()

	runValidationCases(t, []validationCase{
		{name: "tenant object arm validates", message: base.Build(), wantValid: true},
		{name: "member object arm validates", message: member.Build(), wantValid: true},
		{name: "role object arm validates", message: roleEvent.Build(), wantValid: true},
		{name: "role deletion object without relations validates", message: deletedRoleEvent.Build(), wantValid: true},
		{name: "role creation object without relations is rejected", message: roleCreateWithoutRelations.Build()},
		{name: "role deletion object with relations is rejected", message: roleDeleteWithRelations.Build()},
		{name: "role object with repeated relation is rejected", message: roleWithDuplicateRelations.Build()},
		{name: "role object with undefined relation is rejected", message: roleWithUndefinedRelation.Build()},
		{name: "role object with unspecified relation is rejected", message: roleWithUnspecifiedRelation.Build()},
		{name: "role assignment object arm validates", message: assignmentEvent.Build(), wantValid: true},
		{name: "partner object arm validates", message: partnerEvent.Build(), wantValid: true},
		{name: "partner object with admin relation is rejected", message: partnerWithAdminRelation.Build()},
		{name: "full payload grant object arm validates", message: grantEvent.Build(), wantValid: true},
		{name: "role object without a ref is rejected", message: roleWithoutRef.Build()},
		{name: "role assignment without a member is rejected", message: assignmentWithoutMember.Build()},
		{name: "partner object without a tenant is rejected", message: partnerWithoutTenant.Build()},
		{name: "full payload grant without expiry is rejected", message: grantWithoutExpiry.Build()},
	})
}
