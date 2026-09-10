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

	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1/devicev1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	eventv1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1/devicev1connect"
	integrationv1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1/devicev1connect"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/auditapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/deviceapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/edge"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
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
// DispatchService and AuditService are verified before Connect decodes
// anything. An operator holds no edge key, so EdgeAdminService and
// DeviceService cannot be behind that check — putting them there would refuse
// every operator. Neither carries an authorization check of its own today;
// OpenFGA is out of this plan's scope, and the deployment that runs this puts
// the operator surface behind its own boundary until it lands.
func (h *assembly) mux(resources *busResources, log *slog.Logger, view *telemetry.View) (http.Handler, error) {
	recoverPanic := panicRecovery()
	interceptors := connect.WithInterceptors(
		TelemetryInterceptor(log, view),
		ValidatingInterceptor(),
	)

	verifier := edge.NewVerifier(h.cfg.AssertionAudience(), h.cfg.AssertionClockSkew(), resources.edges.Lookup)
	middleware := edgeapi.NewMiddleware(verifier, maxEdgeBody, log)

	edgeService, err := edgeapi.NewService(
		resources.edges, h.registry, resources.journal, h.credentials, resources.hub,
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
		Journal:  resources.journal,
		Resolver: h.registry,
		Watcher:  deviceapi.NewKVWatcher(resources.lanes),
	})
	if err != nil {
		return nil, err
	}

	auditService := auditapi.New(
		auditPublisher(resources.hub),
		&auditBinding{registry: h.registry},
		resources.hub.Tenant(),
	)

	mux := http.NewServeMux()
	edgePath, edgeHandler := edgev1connect.NewEdgeServiceHandler(edgeService, interceptors, recoverPanic)
	mux.Handle(edgePath, middleware.Wrap(edgeHandler))
	// Enroll is the one edge call made before central holds a key to verify
	// it with, so it sits in front of the middleware. It carries its own
	// proof: the setup key, and a signature by the key being registered.
	// Bounded explicitly. Every other edge procedure gets the limit from the
	// assertion middleware, which reads the body whole to hash it; Enroll is
	// served in front of that middleware because an edge has no identity to
	// sign with yet — which makes it the one procedure an unauthenticated
	// caller can reach, and the one the bound exists for.
	mux.Handle(edgev1connect.EdgeServiceEnrollProcedure, http.MaxBytesHandler(edgeHandler, maxEdgeBody))

	dispatchPath, dispatchHandler := integrationv1connect.NewDispatchServiceHandler(resources.dispatch, interceptors, recoverPanic)
	mux.Handle(dispatchPath, middleware.Wrap(dispatchHandler))

	auditPath, auditHandler := eventv1connect.NewAuditServiceHandler(auditService, interceptors, recoverPanic)
	mux.Handle(auditPath, middleware.Wrap(auditHandler))

	adminPath, adminHandler := edgev1connect.NewEdgeAdminServiceHandler(adminService, interceptors, recoverPanic)
	mux.Handle(adminPath, adminHandler)

	devicePath, deviceHandler := devicev1connect.NewDeviceServiceHandler(deviceService, interceptors, recoverPanic)
	mux.Handle(devicePath, deviceHandler)

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
	return l.journal.DropHolds(ctx, deviceID)
}

func (l *laneAdmin) OpenMutation(ctx context.Context, deviceID string) (uint64, bool, error) {
	record, err := l.journal.Record(ctx, deviceID)
	if err != nil {
		return 0, false, err
	}
	mutation := record.GetMutation()
	if mutation == nil {
		return 0, false, nil
	}
	return mutation.GetSequence(), true, nil
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
// because an address without one used to start a healthy-looking service on
// an unpredictable port while every edge dialed the cluster_urls the same
// file named, with nothing logging the mismatch.
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
