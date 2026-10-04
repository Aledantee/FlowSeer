package identityapi

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/accessstore"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

// AdminService manages access records in the admitted tenant. It is safe for
// concurrent use when its stores, clock, and projector are. Its zero value is
// unusable. Every mutation projects before success except CreateRole.
type AdminService struct {
	tenants   *tenantstore.Store
	access    *accessstore.Store
	issuers   []string
	clock     func() time.Time
	projector Projector
	log       *slog.Logger
}

// NewAdminService requires both stores, configured issuer URLs, a central clock,
// and a projector. The issuer list is copied. Interceptors authorize each call.
// A nil logger discards projection warnings.
func NewAdminService(tenants *tenantstore.Store, access *accessstore.Store, issuers []string, clock func() time.Time, projector Projector, log *slog.Logger) *AdminService {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &AdminService{tenants: tenants, access: access, issuers: slices.Clone(issuers), clock: clock, projector: projector, log: log}
}

// EnrollMember preserves an existing enrollment and its grants on retry.
// An unconfigured issuer is refused before any record is written.
func (s *AdminService) EnrollMember(ctx context.Context, req *connect.Request[apiv1.EnrollMemberRequest]) (*connect.Response[apiv1.EnrollMemberResponse], error) {
	p, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(s.issuers, req.Msg.GetMember().GetIssuer()) {
		return nil, connectErr(ctx, errs.New().Code(ErrCodeUnknownIssuer).Msg("member issuer is not configured"))
	}
	member, err := s.access.CreateMember(ctx, tenantID, identityv1.Member_builder{
		Operator: req.Msg.GetMember(), EnrolledAt: timestamppb.New(s.clock()), EnrolledBy: operatorRef(p),
	}.Build())
	if err != nil {
		return nil, connectErr(ctx, err)
	}
	if err := s.projector.SyncTenant(ctx, tenantID); err != nil {
		return nil, projectionError(err)
	}
	return connect.NewResponse(apiv1.EnrollMemberResponse_builder{Member: member}.Build()), nil
}

// RemoveMember deletes enrollment and grants before projecting the tenant and
// then the member's requested sessions. An absent member projects again on retry.
func (s *AdminService) RemoveMember(ctx context.Context, req *connect.Request[apiv1.RemoveMemberRequest]) (*connect.Response[apiv1.RemoveMemberResponse], error) {
	_, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	if _, err := s.access.DeleteMember(ctx, tenantID, req.Msg.GetMember()); err != nil {
		return nil, connectErr(ctx, err)
	}
	if err := s.projector.SyncTenant(ctx, tenantID); err != nil {
		return nil, projectionError(err)
	}
	if err := s.projector.SyncRequester(ctx, tenantID, req.Msg.GetMember()); err != nil {
		return nil, projectionError(err)
	}
	return connect.NewResponse(&apiv1.RemoveMemberResponse{}), nil
}

// CreateRole mints an independent role, allowing repeated display names.
// Projection failure is logged and the role returned because retry mints an id.
func (s *AdminService) CreateRole(ctx context.Context, req *connect.Request[apiv1.CreateRoleRequest]) (*connect.Response[apiv1.CreateRoleResponse], error) {
	_, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, connectErr(ctx, errs.Wrap(err, "mint role id"))
	}
	role := identityv1.Role_builder{
		Ref:  identityv1.RoleGlobalRef_builder{Role: identityv1.RoleLocalRef_builder{Id: new(id.String())}.Build()}.Build(),
		Name: new(req.Msg.GetName()), Relations: req.Msg.GetRelations(),
	}.Build()
	if req.Msg.HasDescription() {
		role.SetDescription(req.Msg.GetDescription())
	}
	role, err = s.access.CreateRole(ctx, tenantID, role)
	if err != nil {
		return nil, connectErr(ctx, err)
	}
	if err := s.projector.SyncTenant(ctx, tenantID); err != nil {
		s.log.WarnContext(ctx, "role projection failed", "error.type", telemetry.ErrorType(err), "flowseer.tenant.id", tenantID, "flowseer.role.id", id.String())
	}
	return connect.NewResponse(apiv1.CreateRoleResponse_builder{Role: role}.Build()), nil
}

// DeleteRole refuses an assigned role. An absent role projects before success.
func (s *AdminService) DeleteRole(ctx context.Context, req *connect.Request[apiv1.DeleteRoleRequest]) (*connect.Response[apiv1.DeleteRoleResponse], error) {
	_, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	if _, err := s.access.DeleteRole(ctx, tenantID, req.Msg.GetRole()); err != nil {
		if code, _ := errs.CodeOf(err); code == accessstore.ErrCodeRoleAssigned {
			err = errs.From(err).Code(ErrCodeRoleAssigned).Msg("role still has assignees")
		}
		return nil, connectErr(ctx, err)
	}
	if err := s.projector.SyncTenant(ctx, tenantID); err != nil {
		return nil, projectionError(err)
	}
	return connect.NewResponse(&apiv1.DeleteRoleResponse{}), nil
}

// AssignRole requires an enrolled member and a role in this tenant. Repeating
// an assignment preserves one ref and projects again. Members hold at most 64.
func (s *AdminService) AssignRole(ctx context.Context, req *connect.Request[apiv1.AssignRoleRequest]) (*connect.Response[apiv1.AssignRoleResponse], error) {
	_, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	member, err := s.access.MutateMember(ctx, tenantID, req.Msg.GetMember(), func(current *identityv1.Member) error {
		role, err := s.access.Role(ctx, tenantID, req.Msg.GetRole())
		if err != nil {
			return err
		}
		if role == nil {
			return errs.New().Code(ErrCodeUnknownRole).Msg("role has no record in the admitted tenant")
		}
		if !slices.ContainsFunc(current.GetRoles(), func(ref *identityv1.RoleGlobalRef) bool {
			return ref.GetRole().GetId() == req.Msg.GetRole().GetRole().GetId()
		}) {
			if len(current.GetRoles()) >= 64 {
				return invalidRequest(errs.Msg("member already holds 64 roles"))
			}
			current.SetRoles(append(current.GetRoles(), req.Msg.GetRole()))
		}
		return nil
	})
	if err != nil {
		return nil, memberError(ctx, err)
	}
	if err := s.projector.SyncTenant(ctx, tenantID); err != nil {
		return nil, projectionError(err)
	}
	return connect.NewResponse(apiv1.AssignRoleResponse_builder{Member: member}.Build()), nil
}

// UnassignRole removes a ref without requiring the role or member to remain.
// An absent assignment projects before answering success.
func (s *AdminService) UnassignRole(ctx context.Context, req *connect.Request[apiv1.UnassignRoleRequest]) (*connect.Response[apiv1.UnassignRoleResponse], error) {
	_, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	_, err = s.access.MutateMember(ctx, tenantID, req.Msg.GetMember(), func(current *identityv1.Member) error {
		current.SetRoles(slices.DeleteFunc(current.GetRoles(), func(ref *identityv1.RoleGlobalRef) bool {
			return ref.GetRole().GetId() == req.Msg.GetRole().GetRole().GetId()
		}))
		return nil
	})
	if code, _ := errs.CodeOf(err); err != nil && code != accessstore.ErrCodeNotFound {
		return nil, connectErr(ctx, err)
	}
	if err := s.projector.SyncTenant(ctx, tenantID); err != nil {
		return nil, projectionError(err)
	}
	return connect.NewResponse(&apiv1.UnassignRoleResponse{}), nil
}

// ConnectPartner requires another committed tenant and excludes admin access.
// Reconnection replaces relations while preserving the connection provenance.
// Repeated concurrent disconnections return a retryable Unavailable error.
func (s *AdminService) ConnectPartner(ctx context.Context, req *connect.Request[apiv1.ConnectPartnerRequest]) (*connect.Response[apiv1.ConnectPartnerResponse], error) {
	p, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	provider := req.Msg.GetPartner().GetTenant().GetId()
	if provider == tenantID {
		return nil, invalidRequest(errs.Msg("a tenant cannot partner with itself"))
	}
	record, err := s.tenants.Get(ctx, provider)
	if err != nil {
		return nil, connectErr(ctx, err)
	}
	if record == nil {
		return nil, connectErr(ctx, errs.New().Code(tenantstore.ErrCodeNotFound).Msg("provider has no tenant record"))
	}
	proposed := identityv1.Partner_builder{
		Tenant: req.Msg.GetPartner(), Relations: req.Msg.GetRelations(), ConnectedAt: timestamppb.New(s.clock()), ConnectedBy: operatorRef(p),
	}.Build()
	var partner *identityv1.Partner
	for range 8 {
		partner, err = s.access.CreatePartner(ctx, tenantID, proposed)
		if err != nil {
			return nil, connectErr(ctx, err)
		}
		if slices.Equal(partner.GetRelations(), req.Msg.GetRelations()) {
			break
		}
		partner, err = s.access.UpdatePartner(ctx, tenantID, req.Msg.GetPartner(), req.Msg.GetRelations())
		if code, _ := errs.CodeOf(err); code == accessstore.ErrCodeNotFound {
			continue
		}
		if err != nil {
			return nil, connectErr(ctx, err)
		}
		break
	}
	if err != nil {
		return nil, connectErr(ctx, errs.From(err).Code(accessstore.ErrCodeConflict).Msg("partner connection did not settle"))
	}
	if err := s.projector.SyncTenant(ctx, tenantID); err != nil {
		return nil, projectionError(err)
	}
	return connect.NewResponse(apiv1.ConnectPartnerResponse_builder{Partner: partner}.Build()), nil
}

// DisconnectPartner deletes the customer's link and projects even when absent.
func (s *AdminService) DisconnectPartner(ctx context.Context, req *connect.Request[apiv1.DisconnectPartnerRequest]) (*connect.Response[apiv1.DisconnectPartnerResponse], error) {
	_, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	if _, err := s.access.DeletePartner(ctx, tenantID, req.Msg.GetPartner()); err != nil {
		return nil, connectErr(ctx, err)
	}
	if err := s.projector.SyncTenant(ctx, tenantID); err != nil {
		return nil, projectionError(err)
	}
	return connect.NewResponse(&apiv1.DisconnectPartnerResponse{}), nil
}

// GrantFullPayload replaces an enrolled member's grant with an expiry from the
// central clock. The lifetime must be positive and at most 24 hours.
func (s *AdminService) GrantFullPayload(ctx context.Context, req *connect.Request[apiv1.GrantFullPayloadRequest]) (*connect.Response[apiv1.GrantFullPayloadResponse], error) {
	p, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	member, err := s.access.MutateMember(ctx, tenantID, req.Msg.GetMember(), func(current *identityv1.Member) error {
		current.SetFullPayload(identityv1.FullPayloadGrant_builder{
			ExpiresAt: timestamppb.New(now.Add(req.Msg.GetLifetime().AsDuration())), Reason: new(req.Msg.GetReason()), GrantedBy: operatorRef(p), GrantedAt: timestamppb.New(now),
		}.Build())
		return nil
	})
	if err != nil {
		return nil, memberError(ctx, err)
	}
	if err := s.projector.SyncTenant(ctx, tenantID); err != nil {
		return nil, projectionError(err)
	}
	return connect.NewResponse(apiv1.GrantFullPayloadResponse_builder{Member: member}.Build()), nil
}

// RevokeFullPayload clears the grant and projects even when no member remains.
func (s *AdminService) RevokeFullPayload(ctx context.Context, req *connect.Request[apiv1.RevokeFullPayloadRequest]) (*connect.Response[apiv1.RevokeFullPayloadResponse], error) {
	_, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	_, err = s.access.MutateMember(ctx, tenantID, req.Msg.GetMember(), func(current *identityv1.Member) error { current.ClearFullPayload(); return nil })
	if code, _ := errs.CodeOf(err); err != nil && code != accessstore.ErrCodeNotFound {
		return nil, connectErr(ctx, err)
	}
	if err := s.projector.SyncTenant(ctx, tenantID); err != nil {
		return nil, projectionError(err)
	}
	return connect.NewResponse(&apiv1.RevokeFullPayloadResponse{}), nil
}

// ListMembers pages only the admitted tenant's members in principal-id order.
func (s *AdminService) ListMembers(ctx context.Context, req *connect.Request[apiv1.ListMembersRequest]) (*connect.Response[apiv1.ListMembersResponse], error) {
	_, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	records, err := s.access.ListMembers(ctx, tenantID)
	if err != nil {
		return nil, connectErr(ctx, err)
	}
	page, token, err := pageRecords(records, req.Msg.GetPageSize(), req.Msg.GetPageToken(), func(m *identityv1.Member) string {
		return authn.ComputePrincipalID(m.GetOperator().GetIssuer(), m.GetOperator().GetSubject())
	})
	if err != nil {
		return nil, err
	}
	resp := apiv1.ListMembersResponse_builder{Members: page}.Build()
	if token != "" {
		resp.SetNextPageToken(token)
	}
	return connect.NewResponse(resp), nil
}

// ListRoles pages only the admitted tenant's roles in UUID order.
func (s *AdminService) ListRoles(ctx context.Context, req *connect.Request[apiv1.ListRolesRequest]) (*connect.Response[apiv1.ListRolesResponse], error) {
	_, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	records, err := s.access.ListRoles(ctx, tenantID)
	if err != nil {
		return nil, connectErr(ctx, err)
	}
	page, token, err := pageRecords(records, req.Msg.GetPageSize(), req.Msg.GetPageToken(), func(r *identityv1.Role) string { return r.GetRef().GetRole().GetId() })
	if err != nil {
		return nil, err
	}
	resp := apiv1.ListRolesResponse_builder{Roles: page}.Build()
	if token != "" {
		resp.SetNextPageToken(token)
	}
	return connect.NewResponse(resp), nil
}

// ListPartners pages only the admitted customer's links in provider UUID order.
func (s *AdminService) ListPartners(ctx context.Context, req *connect.Request[apiv1.ListPartnersRequest]) (*connect.Response[apiv1.ListPartnersResponse], error) {
	_, tenantID, err := adminRequest(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	records, err := s.access.ListPartners(ctx, tenantID)
	if err != nil {
		return nil, connectErr(ctx, err)
	}
	page, token, err := pageRecords(records, req.Msg.GetPageSize(), req.Msg.GetPageToken(), func(p *identityv1.Partner) string { return p.GetTenant().GetTenant().GetId() })
	if err != nil {
		return nil, err
	}
	resp := apiv1.ListPartnersResponse_builder{Partners: page}.Build()
	if token != "" {
		resp.SetNextPageToken(token)
	}
	return connect.NewResponse(resp), nil
}

func operatorRef(p authn.Principal) *identityv1.OperatorRef {
	return identityv1.OperatorRef_builder{Issuer: new(p.Issuer), Subject: new(p.Subject)}.Build()
}
