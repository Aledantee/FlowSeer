// Package identityapi serves tenant definitions and tenant access records.
// Authentication and authorization interceptors enforce each RPC's schema rule.
package identityapi

import (
	"context"
	"encoding/base64"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	apiv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

// Projector converges access relationships from committed records. Methods must
// be safe for concurrent use and report failures without discarding their causes.
type Projector interface {
	SyncTenant(ctx context.Context, tenantID string) error
	SyncRequester(ctx context.Context, tenantID string, operator *identityv1.OperatorRef) error
}

// TenantService serves platform-authorized tenant creation and reads. It is safe
// for concurrent use when its store and projector are. Its zero value is unusable.
type TenantService struct {
	store     *tenantstore.Store
	projector Projector
}

// NewTenantService requires a store and a projector. The interceptors must
// authorize calls before entry.
func NewTenantService(store *tenantstore.Store, projector Projector) *TenantService {
	return &TenantService{store: store, projector: projector}
}

// CreateTenant claims an organization with a new UUID. Equal configuration
// retries return its committed record and project again before succeeding.
func (s *TenantService) CreateTenant(ctx context.Context, req *connect.Request[apiv1.CreateTenantRequest]) (*connect.Response[apiv1.CreateTenantResponse], error) {
	if _, err := validateRequest(ctx, req.Msg); err != nil {
		return nil, err
	}
	config := identityv1.TenantConfig_builder{
		Issuer: new(req.Msg.GetIssuer()), OrganizationClaimName: new(req.Msg.GetOrganizationClaimName()),
		OrganizationClaimValue: new(req.Msg.GetOrganizationClaimValue()),
	}.Build()
	if req.Msg.HasName() {
		config.SetName(req.Msg.GetName())
	}
	if req.Msg.HasDescription() {
		config.SetDescription(req.Msg.GetDescription())
	}

	record, err := s.store.LookupByOrg(ctx, config.GetIssuer(), config.GetOrganizationClaimValue())
	if err != nil {
		return nil, connectErr(ctx, err)
	}
	if record == nil {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, connectErr(ctx, errs.Wrap(err, "mint tenant id"))
		}
		config.SetRef(tenantRef(id.String()))
		record, err = s.store.Create(ctx, config)
		if code, _ := errs.CodeOf(err); code == tenantstore.ErrCodeAlreadyExists {
			// Another caller can claim the organization after the first lookup.
			record, err = s.store.LookupByOrg(ctx, config.GetIssuer(), config.GetOrganizationClaimValue())
			if err == nil && record == nil {
				err = errs.New().Code(tenantstore.ErrCodeAlreadyExists).Msg("organization is already claimed")
			}
		}
		if err != nil {
			return nil, connectErr(ctx, err)
		}
	}
	config.SetRef(record.GetConfig().GetRef())
	if !proto.Equal(config, record.GetConfig()) {
		return nil, connectErr(ctx, errs.New().Code(tenantstore.ErrCodeAlreadyExists).Msg("organization has another tenant configuration"))
	}
	if err := s.projector.SyncTenant(ctx, record.GetConfig().GetRef().GetTenant().GetId()); err != nil {
		return nil, projectionError(err)
	}
	return connect.NewResponse(apiv1.CreateTenantResponse_builder{Tenant: record}.Build()), nil
}

// GetTenant returns a committed tenant or NotFound to an authorized caller.
func (s *TenantService) GetTenant(ctx context.Context, req *connect.Request[apiv1.GetTenantRequest]) (*connect.Response[apiv1.GetTenantResponse], error) {
	if _, err := validateRequest(ctx, req.Msg); err != nil {
		return nil, err
	}
	record, err := s.store.Get(ctx, req.Msg.GetTenant().GetTenant().GetId())
	if err != nil {
		return nil, connectErr(ctx, err)
	}
	if record == nil {
		return nil, connectErr(ctx, errs.New().Code(tenantstore.ErrCodeNotFound).Msg("tenant has no committed record"))
	}
	return connect.NewResponse(apiv1.GetTenantResponse_builder{Tenant: record}.Build()), nil
}

// ListTenants returns committed tenants in ascending UUID order, at most 500.
func (s *TenantService) ListTenants(ctx context.Context, req *connect.Request[apiv1.ListTenantsRequest]) (*connect.Response[apiv1.ListTenantsResponse], error) {
	if _, err := validateRequest(ctx, req.Msg); err != nil {
		return nil, err
	}
	records, err := s.store.List(ctx)
	if err != nil {
		return nil, connectErr(ctx, err)
	}
	page, token, err := pageRecords(records, req.Msg.GetPageSize(), req.Msg.GetPageToken(), func(r *identityv1.TenantRecord) string { return r.GetConfig().GetRef().GetTenant().GetId() })
	if err != nil {
		return nil, err
	}
	resp := apiv1.ListTenantsResponse_builder{Tenants: page}.Build()
	if token != "" {
		resp.SetNextPageToken(token)
	}
	return connect.NewResponse(resp), nil
}

func tenantRef(id string) *identityv1.TenantGlobalRef {
	return identityv1.TenantGlobalRef_builder{Tenant: identityv1.TenantLocalRef_builder{Id: new(id)}.Build()}.Build()
}

func pageRecords[T any](records []T, size uint32, token string, key func(T) string) ([]T, string, error) {
	if size == 0 {
		size = 50
	}
	after, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, "", invalidRequest(errs.Wrap(err, "decode page token"))
	}
	start := 0
	for start < len(records) && key(records[start]) <= string(after) {
		start++
	}
	end := min(start+int(size), len(records))
	next := ""
	if end < len(records) {
		next = base64.RawURLEncoding.EncodeToString([]byte(key(records[end-1])))
	}
	return records[start:end], next, nil
}
