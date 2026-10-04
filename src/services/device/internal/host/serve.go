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
	"time"

	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1/devicev1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1/attachv1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/audit/v1/auditv1connect"
	captureedgev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1/dispatchv1connect"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/actiontrail"
	"go.aledante.io/FlowSeer/src/services/device/internal/auditapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/captureapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/connecterr"
	"go.aledante.io/FlowSeer/src/services/device/internal/deviceapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/edge"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/identityapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
	"go.aledante.io/FlowSeer/src/services/device/internal/projector"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

// panicRecovery turns a panic in any handler — unary and streaming alike —
// into an internal error on the ordinary error path, so the telemetry
// interceptor records a duration for it and the caller gets an answer rather
// than a transport reset. The panic value itself is not put on the wire.
func panicRecovery() connect.HandlerOption {
	return connect.WithRecover(func(_ context.Context, _ connect.Spec, _ http.Header, p any) error {
		return panicError(p)
	})
}

type panicInterceptor struct{}

func (panicInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (resp connect.AnyResponse, err error) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					mustRepanicAbortHandler()
				}
				err = panicError(p)
				resp = nil
			}
		}()
		return next(ctx, req)
	}
}

func (panicInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) (err error) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					mustRepanicAbortHandler()
				}
				err = panicError(p)
			}
		}()
		return next(ctx, conn)
	}
}

// mustRepanicAbortHandler preserves the [http.ErrAbortHandler] panic after the
// interceptor has recovered it. The caller has established the invariant that
// the panic value is exactly that sentinel, so this function must panic with it
// again. The net/http server recovers it at the ServeHTTP boundary, aborts the
// response by closing the connection or resetting the HTTP/2 stream, and
// suppresses the stack trace it logs for other handler panics.
func mustRepanicAbortHandler() {
	panic(http.ErrAbortHandler)
}

func (panicInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func panicError(p any) error {
	return connecterr.WrapAs(connect.CodeInternal, "handler panicked", errs.New().Code(ErrCodePanic).
		Attr("panic", fmt.Sprintf("%T", p)).Msg("handler panicked"))
}

// mux builds the served surface: the edge-facing services behind the
// assertion middleware, and the operator-facing ones enforcing authentication,
// validation, relationship authorization, and action trail recording.
//
// Which side a service sits on is decided by who calls it. An edge signs every
// call with the key central registered at enrollment, so EdgeService,
// DispatchService, AuditService and CaptureEdgeService are verified before
// Connect decodes anything. An operator holds no edge key, so EdgeAdminService,
// DeviceService, CaptureService, TenantService, and TenantAdminService use
// the operator chain. It enforces
// operator authentication, schema validation, relationship-based authorization,
// and action trail recording.
//
// Two edge procedures are mounted in front of the middleware, each for the
// same reason and each paying for it explicitly. Enroll happens before central
// holds a key to verify with. UploadCapture holds a stream open for the length
// of a capture, which no body-hashing middleware can read, so it authenticates
// from the stream's own assertions instead. Both are bounded where the
// middleware's limit would have been.
func (h *assembly) mux(ctx context.Context, resources *busResources, log *slog.Logger, view *telemetry.View) (http.Handler, error) {
	access, err := openAccessStore(ctx, resources.hub)
	if err != nil {
		return nil, err
	}
	recoverPanic := panicRecovery()
	recoverInterceptor := panicInterceptor{}

	edgeInterceptors := connect.WithInterceptors(
		TelemetryInterceptor(log, view),
		recoverInterceptor,
		ValidatingInterceptor(),
	)

	authnCfg := h.cfg.Authentication()
	var issuers []authn.IssuerConfig
	var issuerURLs []string
	if authnCfg != nil {
		for _, iss := range authnCfg.GetIssuers() {
			issuerURLs = append(issuerURLs, iss.GetIssuer())
			issuers = append(issuers, authn.IssuerConfig{
				Issuer:                iss.GetIssuer(),
				Audience:              iss.GetAudience(),
				OrganizationClaimName: iss.GetOrganizationClaimName(),
			})
		}
	}
	var platform authn.PlatformConfig
	if p := h.cfg.PlatformAdmin(); p != nil {
		platform = authn.PlatformConfig{
			Issuer:       p.GetIssuer(),
			ClaimName:    p.GetOrganizationClaimName(),
			Organization: p.GetOrganization(),
		}
	}
	tokenVerifier, err := authn.NewVerifier(authn.Options{
		Issuers:  issuers,
		Platform: platform,
		Resolver: resources.tenants.LookupByOrg,
		Client:   h.authnClient,
		Clock:    time.Now,
	})
	if err != nil {
		return nil, err
	}

	operatorInterceptors := connect.WithInterceptors(
		TelemetryInterceptor(log, view),
		recoverInterceptor,
		authn.NewInterceptor(tokenVerifier),
		OperatorValidatingInterceptor(),
		authz.NewInterceptor(h.engine),
		actiontrail.NewInterceptor(auditPublisher(resources.hub), time.Now, log),
	)

	projectHook := func(ctx context.Context, objectType, id string) {
		t, err := tenant.FromContext(ctx)
		if err != nil {
			log.ErrorContext(ctx, "projector hook missing tenant", slog.String("error.type", telemetry.ErrorType(err)))
			return
		}
		if err := resources.projector.Sync(ctx, projector.Object{
			Type:   objectType,
			ID:     id,
			Tenant: t,
		}); err != nil {
			log.ErrorContext(ctx, "failed to project object relationship",
				slog.String("flowseer.authz.object.type", objectType),
				slog.String("flowseer.authz.object.id", id),
				slog.String("error.type", telemetry.ErrorType(err)),
			)
		}
	}

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
		nil,
		projectHook,
	)
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
			Project:      projectHook,
			FullPayload:  access.FullPayloadActive,
		},
	)
	mux := http.NewServeMux()
	tenantPath, tenantHandler := identityv1connect.NewTenantServiceHandler(
		identityapi.NewTenantService(resources.tenants, resources.projector), operatorInterceptors, recoverPanic,
	)
	mux.Handle(tenantPath, tenantHandler)
	tenantAdminPath, tenantAdminHandler := identityv1connect.NewTenantAdminServiceHandler(
		identityapi.NewAdminService(resources.tenants, access, issuerURLs, time.Now, resources.projector), operatorInterceptors, recoverPanic,
	)
	mux.Handle(tenantAdminPath, tenantAdminHandler)
	edgePath, edgeHandler := attachv1connect.NewEdgeServiceHandler(edgeService, edgeInterceptors, recoverPanic)
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

	dispatchPath, dispatchHandler := dispatchv1connect.NewDispatchServiceHandler(resources.dispatch, edgeInterceptors, recoverPanic)
	mux.Handle(dispatchPath, middleware.Wrap(dispatchHandler))

	auditPath, auditHandler := auditv1connect.NewAuditServiceHandler(auditService, edgeInterceptors, recoverPanic)
	mux.Handle(auditPath, middleware.Wrap(auditHandler))

	adminPath, adminHandler := edgev1connect.NewEdgeAdminServiceHandler(adminService, operatorInterceptors, recoverPanic)
	mux.Handle(adminPath, adminHandler)

	devicePath, deviceHandler := devicev1connect.NewDeviceServiceHandler(deviceService, operatorInterceptors, recoverPanic)
	mux.Handle(devicePath, deviceHandler)

	capturePath, captureHandler := capturev1connect.NewCaptureServiceHandler(captureOperatorService, operatorInterceptors, recoverPanic)
	mux.Handle(capturePath, captureHandler)

	// Every message on the upload stream is bounded, in place of the body
	// limit the assertion middleware applies to a unary edge call. The
	// middleware cannot do it here: it reads the body whole to hash it, and
	// an upload stream has no whole.
	captureEdgePath, captureEdgeHandler := captureedgev1connect.NewCaptureEdgeServiceHandler(
		captureEdgeService, edgeInterceptors, recoverPanic, connect.WithReadMaxBytes(maxCaptureChunk),
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
