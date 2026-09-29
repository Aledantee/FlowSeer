package host

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1/devicev1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	apiidentityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1/attachv1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/audit/v1/auditv1connect"
	captureedgev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1/dispatchv1connect"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/auditapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/captureapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/deviceapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/edge"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

// panicRecovery turns a panic in any handler — unary and streaming alike —
// into an internal error on the ordinary error path, so the telemetry
// interceptor records a duration for it and the caller gets an answer rather
// than a transport reset. The panic value itself is not put on the wire.
func panicRecovery() connect.HandlerOption {
	return connect.WithRecover(func(_ context.Context, _ connect.Spec, _ http.Header, p any) error {
		return connect.NewError(connect.CodeInternal, errs.New().Code(ErrCodePanic).
			Attr("panic", fmt.Sprintf("%T", p)).Msg("handler panicked"))
	})
}

// mux builds the served surface: the edge-facing services behind the
// assertion middleware, and the operator-facing ones in front of it.
//
// Which side a service sits on is decided by who calls it. An edge signs every
// call with the key central registered at enrollment, so EdgeService,
// DispatchService, AuditService and CaptureEdgeService are verified before
// Connect decodes anything. An operator holds no edge key, so EdgeAdminService,
// DeviceService and CaptureService cannot be behind that check — putting them
// there would refuse every operator. None carries an authorization check of
// its own. The deployment puts the operator surface behind its own boundary.
// CaptureService is the one that makes that boundary matter most: behind it is
// other people's traffic, not only an inventory.
//
// Two edge procedures are mounted in front of the middleware, each for the
// same reason and each paying for it explicitly. Enroll happens before central
// holds a key to verify with. UploadCapture holds a stream open for the length
// of a capture, which no body-hashing middleware can read, so it authenticates
// from the stream's own assertions instead. Both are bounded where the
// middleware's limit would have been.
func (h *assembly) mux(resources *busResources, log *slog.Logger, view *telemetry.View) (http.Handler, error) {
	recoverPanic := panicRecovery()
	devTenant := h.cfg.DevTenant()
	if devTenant == "" {
		devTenant = edgebus.DefaultTenant
	}
	interceptors := connect.WithInterceptors(
		TelemetryInterceptor(log, view),
		ValidatingInterceptor(),
		TenantInterceptor(devTenant),
	)

	verifier := edge.NewVerifier(h.cfg.AssertionAudience(), h.cfg.AssertionClockSkew(), resources.edges.Lookup)
	middleware := edgeapi.NewMiddleware(verifier, maxEdgeBody, log)

	edgeService, err := edgeapi.NewService(
		resources.edges, h.registry, &edgeLaneRecords{journal: resources.journal, edgeTenant: resources.edgeTenant}, h.credentials, resources.hub,
		edgeapi.ServiceConfig{
			Audience:      h.cfg.AssertionAudience(),
			TrustAnchors:  [][]byte{h.certificate.SPKI},
			ClusterURLs:   h.cfg.ClusterURLs(),
			PulseInterval: h.cfg.Intervals().SubmissionPulse,
		}, nil, log)
	if err != nil {
		return nil, err
	}

	intervals := h.cfg.Intervals()
	adminService, err := edgeapi.NewAdminService(
		resources.edges,
		&laneAdmin{registry: h.registry, journal: resources.journal},
		edgeapi.Provisioning{
			CentralURL:   h.cfg.CentralURL(),
			TrustAnchors: [][]byte{h.certificate.SPKI},
		},
		edgeapi.NewContact(intervals.EdgeStaleAfter, intervals.EdgeDormantAfter),
		nil)
	if err != nil {
		return nil, err
	}

	deviceService, err := deviceapi.New(deviceapi.Config{
		Journal:    resources.journal,
		Resolver:   h.registry,
		EdgeTenant: resources.edgeTenant,
		Watcher:    deviceapi.NewKVWatcher(resources.lanes),
	})
	if err != nil {
		return nil, err
	}

	auditService := auditapi.New(
		auditPublisher(resources.hub),
		&auditBinding{registry: h.registry},
		resources.edgeTenant,
	)

	captureEdgeService := captureapi.NewEdgeService(
		resources.captures,
		verifier,
		resources.broadcaster,
		captureapi.EdgeServiceConfig{
			Logger:     log,
			EdgeTenant: resources.edgeTenant,
		},
	)
	captureOperatorService := captureapi.NewOperatorService(
		resources.captures,
		resources.broadcaster,
		captureapi.OperatorServiceConfig{
			NotifyChange: captureEdgeService.NotifyStoreChange,
			EdgeTenant:   resources.edgeTenant,
		},
	)

	tenantsKV, err := resources.hub.JetStream().KeyValue(context.Background(), edgebus.TenantBucket)
	if err != nil {
		return nil, err
	}
	tenantStore := tenantstore.New(tenantsKV)
	tenantSvc := &tenantService{
		admin: h.cfg.PlatformAdmin(),
		store: tenantStore,
	}

	mux := http.NewServeMux()
	edgePath, edgeHandler := attachv1connect.NewEdgeServiceHandler(edgeService, interceptors, recoverPanic)
	mux.Handle(edgePath, middleware.Wrap(edgeHandler))
	// Enroll is the one edge call made before central holds a key to verify
	// it with, so it sits in front of the middleware. It carries its own
	// proof: the setup key, and a signature by the key being registered.
	// Bounded explicitly. Every other edge procedure gets the limit from the
	// assertion middleware, which reads the body whole to hash it; Enroll is
	// served in front of that middleware because an edge has no identity to
	// sign with yet — which makes it the one procedure an unauthenticated
	// caller can reach, and the one the bound exists for.
	mux.Handle(attachv1connect.EdgeServiceEnrollProcedure, http.MaxBytesHandler(edgeHandler, maxEdgeBody))

	dispatchPath, dispatchHandler := dispatchv1connect.NewDispatchServiceHandler(resources.dispatch, interceptors, recoverPanic)
	mux.Handle(dispatchPath, middleware.Wrap(dispatchHandler))

	auditPath, auditHandler := auditv1connect.NewAuditServiceHandler(auditService, interceptors, recoverPanic)
	mux.Handle(auditPath, middleware.Wrap(auditHandler))

	adminPath, adminHandler := edgev1connect.NewEdgeAdminServiceHandler(adminService, interceptors, recoverPanic)
	mux.Handle(adminPath, adminHandler)

	devicePath, deviceHandler := devicev1connect.NewDeviceServiceHandler(deviceService, interceptors, recoverPanic)
	mux.Handle(devicePath, deviceHandler)

	capturePath, captureHandler := capturev1connect.NewCaptureServiceHandler(captureOperatorService, interceptors, recoverPanic)
	mux.Handle(capturePath, captureHandler)

	tenantPath, tenantHandler := identityv1connect.NewTenantServiceHandler(tenantSvc, interceptors, recoverPanic)
	mux.Handle(tenantPath, tenantHandler)

	// Every message on the upload stream is bounded, in place of the body
	// limit the assertion middleware applies to a unary edge call. The
	// middleware cannot do it here: it reads the body whole to hash it, and
	// an upload stream has no whole.
	captureEdgePath, captureEdgeHandler := captureedgev1connect.NewCaptureEdgeServiceHandler(
		captureEdgeService, interceptors, recoverPanic, connect.WithReadMaxBytes(maxCaptureChunk),
	)
	mux.Handle(captureEdgePath, middleware.Wrap(captureEdgeHandler))
	// UploadCapture carries its assertions as messages rather than headers,
	// so it is served in front of the middleware and authenticates itself
	// from the stream's first message. That leaves it the second procedure an
	// unauthenticated caller can reach, and the read bound above is what that
	// costs. The handler also needs the read deadline that enforces its
	// assertion window, which only this layer can hand it.
	mux.Handle(
		captureedgev1connect.CaptureEdgeServiceUploadCaptureProcedure,
		captureapi.WithUploadReadDeadline(captureEdgeHandler),
	)

	return mux, nil
}

// serverTLS is the configuration both listeners serve, so an edge pins one
// digest for the API and the bus alike.
func (h *assembly) serverTLS() *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{h.certificate.TLS},
	}
}

// laneAdmin is what retirement is allowed to do to the lanes an edge hosted:
// name its devices, forget the hold resolutions they owe it, and read the open
// mutation each still carries. It ends nothing.
type laneAdmin struct {
	registry *registry.Registry
	journal  *journal.Journal
}

func (l *laneAdmin) Devices(ctx context.Context, edgeID string) ([]string, error) {
	return l.registry.Devices(ctx, edgeID)
}

func (l *laneAdmin) DropHolds(ctx context.Context, deviceID string) error {
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return err
	}
	return l.journal.DropHolds(ctx, tenantID, deviceID)
}

func (l *laneAdmin) OpenMutation(ctx context.Context, deviceID string) (uint64, bool, error) {
	tenantID, err := tenant.FromContext(ctx)
	if err != nil {
		return 0, false, err
	}
	record, err := l.journal.Record(ctx, tenantID, deviceID)
	if err != nil {
		return 0, false, err
	}
	mutation := record.GetMutation()
	if mutation == nil {
		return 0, false, nil
	}
	return mutation.GetSequence(), true, nil
}

type edgeLaneRecords struct {
	journal    *journal.Journal
	edgeTenant func(ctx context.Context, edgeID string) (string, error)
}

func (e *edgeLaneRecords) Record(ctx context.Context, deviceID string) (*storev1.DeviceLaneRecord, error) {
	edgeID, _ := edgeapi.EdgeIDFromContext(ctx)
	if edgeID == "" {
		return nil, errs.New().Msg("no edge in context")
	}
	tenantID, err := e.edgeTenant(ctx, edgeID)
	if err != nil {
		return nil, err
	}
	return e.journal.Record(ctx, tenantID, deviceID)
}

// auditBinding binds an audit delivery to the edge its assertion named, so an
// edge cannot write a record about another edge's device onto the stream
// central owns.
type auditBinding struct {
	registry *registry.Registry
}

func (a *auditBinding) EdgeID(ctx context.Context) (string, error) {
	return edgeapi.EdgeIDFromContext(ctx)
}

func (a *auditBinding) Hosts(ctx context.Context, edgeID, deviceID string) (bool, error) {
	return a.registry.Hosts(ctx, edgeID, deviceID)
}

// edgeIDFromContext is the relay's window onto the verified assertion.
func edgeIDFromContext(ctx context.Context) (string, error) {
	return edgeapi.EdgeIDFromContext(ctx)
}

// auditPublisher writes central's own records onto the audit stream.
func auditPublisher(hub *edgebus.Hub) auditapi.JetStreamPublisher {
	return auditapi.JetStreamPublisher{JS: hub.JetStream()}
}

// portOf is the port half of a host:port.
//
// It returns -1 when the address names no parsable port, which the hub reads
// as "pick a free one". That is unreachable from a validated configuration:
// the schema requires both listener addresses to end in a port, precisely
// because an address without one starts a healthy-looking service on an
// unpredictable port while every edge dials the cluster_urls the same file
// names, with nothing logging the mismatch.
func portOf(address string) int {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return -1
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return -1
	}
	return number
}

func newStderr() *os.File { return os.Stderr }

// tenantService implements identityv1connect.TenantServiceHandler.
// Management of tenants is permitted only when a platform administrator is configured.
type tenantService struct {
	admin *storev1.PlatformAdmin
	store *tenantstore.Store
}

func (s *tenantService) checkAdmin() error {
	if s.admin == nil {
		return connect.NewError(connect.CodePermissionDenied, errs.New().Msg("platform admin is not configured"))
	}
	return nil
}

func (s *tenantService) CreateTenant(
	ctx context.Context,
	req *connect.Request[apiidentityv1.CreateTenantRequest],
) (*connect.Response[apiidentityv1.CreateTenantResponse], error) {
	if err := s.checkAdmin(); err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errs.From(err).Msg("draw tenant identifier"))
	}
	tenantID := strings.ToLower(id.String())

	var name *string
	if req.Msg.HasName() {
		name = proto.String(req.Msg.GetName())
	}
	var desc *string
	if req.Msg.HasDescription() {
		desc = proto.String(req.Msg.GetDescription())
	}

	config := identityv1.TenantConfig_builder{
		Ref: identityv1.TenantGlobalRef_builder{
			Tenant: identityv1.TenantLocalRef_builder{
				Id: proto.String(tenantID),
			}.Build(),
		}.Build(),
		Issuer:                 proto.String(req.Msg.GetIssuer()),
		OrganizationClaimName:  proto.String(req.Msg.GetOrganizationClaimName()),
		OrganizationClaimValue: proto.String(req.Msg.GetOrganizationClaimValue()),
		Name:                   name,
		Description:            desc,
	}.Build()

	record, err := s.store.Create(ctx, config)
	if err != nil {
		if code, _ := errs.CodeOf(err); code == tenantstore.ErrCodeAlreadyExists {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		if code, _ := errs.CodeOf(err); code == tenantstore.ErrCodeInvalidConfig {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := apiidentityv1.CreateTenantResponse_builder{
		Tenant: record,
	}.Build()
	return connect.NewResponse(resp), nil
}

func (s *tenantService) GetTenant(
	ctx context.Context,
	req *connect.Request[apiidentityv1.GetTenantRequest],
) (*connect.Response[apiidentityv1.GetTenantResponse], error) {
	if err := s.checkAdmin(); err != nil {
		return nil, err
	}
	ref := req.Msg.GetTenant()
	if ref == nil || ref.GetTenant() == nil || ref.GetTenant().GetId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errs.New().Msg("tenant id is required"))
	}
	tenantID := ref.GetTenant().GetId()
	record, err := s.store.Get(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if record == nil {
		return nil, connect.NewError(connect.CodeNotFound, errs.New().Attr("tenant", tenantID).Msg("tenant not found"))
	}
	resp := apiidentityv1.GetTenantResponse_builder{
		Tenant: record,
	}.Build()
	return connect.NewResponse(resp), nil
}

func (s *tenantService) ListTenants(
	ctx context.Context,
	req *connect.Request[apiidentityv1.ListTenantsRequest],
) (*connect.Response[apiidentityv1.ListTenantsResponse], error) {
	if err := s.checkAdmin(); err != nil {
		return nil, err
	}
	records, err := s.store.List(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pageSize := int(req.Msg.GetPageSize())
	if pageSize <= 0 {
		pageSize = 100
	}
	pageToken := req.Msg.GetPageToken()
	startIndex := 0
	if pageToken != "" {
		startIndex = len(records)
		for i, r := range records {
			if r.GetConfig().GetRef().GetTenant().GetId() > pageToken {
				startIndex = i
				break
			}
		}
	}
	endIndex := startIndex + pageSize
	if endIndex > len(records) {
		endIndex = len(records)
	}
	pageRecords := records[startIndex:endIndex]
	respBuilder := apiidentityv1.ListTenantsResponse_builder{
		Tenants: pageRecords,
	}
	if endIndex < len(records) && len(pageRecords) > 0 {
		nextToken := pageRecords[len(pageRecords)-1].GetConfig().GetRef().GetTenant().GetId()
		respBuilder.NextPageToken = &nextToken
	}
	return connect.NewResponse(respBuilder.Build()), nil
}
