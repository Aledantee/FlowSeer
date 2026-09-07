package host

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/centralaudit"
	"go.aledante.io/FlowSeer/src/services/device/internal/credential"
	"go.aledante.io/FlowSeer/src/services/device/internal/dispatchapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/drift"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

// ErrCodeStart is a service that cannot be assembled from what it was given.
var ErrCodeStart = errs.NewCode("host/start")

// serviceName and serviceNamespace identify this process to the runtime and
// to every signal it exports.
const (
	serviceName      = "device"
	serviceNamespace = "flowseer"
	// shutdownGrace bounds how long the API listener waits for calls in
	// flight before they are cut. An edge holds a dispatch stream open
	// indefinitely, so for those this is not a deadline they respect: the
	// grace expires and the connections are closed underneath them.
	shutdownGrace = 5 * time.Second
	// maxEdgeBody bounds a request body from an edge. The middleware reads it
	// whole to hash it, so the bound is what stops an unauthenticated caller
	// making central buffer as much as it likes.
	maxEdgeBody = 1 << 20
)

// Run assembles the device service and runs it until ctx ends or the runtime
// stops it.
//
// The five modules are declared in dependency order and supervised
// RestForOne, which is what makes the hub handle safe: see [hubHandle]. The
// service declares no local message bus — its durability is the hub's
// JetStream, and a second embedded broker would be a second store to keep.
func Run(ctx context.Context, cfg *Config, version string) error {
	if cfg == nil {
		return errs.New().Code(ErrCodeStart).Msg("the service was given no configuration")
	}

	// From configuration, not hard-coded: the request interceptor grades a
	// refused call's reason at DEBUG on the argument that an operator can
	// raise the level when they need the detail, and that argument only
	// holds if raising it is possible without a rebuild.
	base := slog.New(slog.NewJSONHandler(newStderr(), &slog.HandlerOptions{Level: cfg.LogLevel()}))
	reg, err := registry.Load(cfg.RegistryPath())
	if err != nil {
		return err
	}
	credentials, err := credential.Open(cfg.CredentialRoot())
	if err != nil {
		return err
	}
	defer func() { _ = credentials.Close() }()
	certificate, err := ObtainCertificate(cfg, base)
	if err != nil {
		return err
	}

	h := &assembly{
		cfg:         cfg,
		registry:    reg,
		credentials: credentials,
		certificate: certificate,
		hub:         newHubHandle(),
	}

	return service.Run(ctx, service.Config{
		Identity: service.Identity{
			Name:      serviceName,
			Namespace: serviceNamespace,
			Version:   version,
		},
		Logger:    base,
		Telemetry: h.telemetryConfig(),
		// RestForOne, with the hub first: every module below it holds
		// resources the hub owns, so a hub that is rebuilt must take them
		// with it. See hubHandle for what a stale one would do.
		Strategy: service.RestForOne,
		Modules: []service.Module{
			{Name: "hub", Leaf: &service.Leaf{Setup: h.setupHub}},
			{Name: "forwarder", Gate: h.forwarderGate(), Leaf: &service.Leaf{Setup: h.setupForwarder}},
			{Name: "journal", Leaf: &service.Leaf{Setup: h.setupJournal}},
			{Name: "connect", Leaf: &service.Leaf{Setup: h.setupConnect}},
			{Name: "drift", Leaf: &service.Leaf{Setup: h.setupDrift}},
		},
	})
}

// assembly holds what every attempt of every module draws from: the things
// that outlive a module attempt because they are read from disk once, and the
// handle to the things that do not.
type assembly struct {
	cfg         *Config
	registry    *registry.Registry
	credentials *credential.Provider
	certificate *Certificate
	hub         *hubHandle
}

func (h *assembly) telemetryConfig() service.TelemetryConfig {
	return service.TelemetryConfig{
		Endpoint: h.cfg.TelemetryEndpoint(),
		Headers:  h.cfg.TelemetryHeaders(),
	}
}

// forwarderGate disables the forwarder when the deployment names no
// collector. Without one there is nowhere to forward an edge's telemetry to,
// and a module that starts only to fail on its first record is worse than one
// that says it is off.
func (h *assembly) forwarderGate() service.Gate {
	return service.FixedGate(h.cfg.TelemetryEndpoint() != "")
}

// setupHub starts the embedded hub and builds everything that lives as long
// as it does.
func (h *assembly) setupHub(ctx context.Context) (service.Attempt, error) {
	log := service.Logger(ctx)
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:    h.cfg.StateDir(),
		FsyncPolicy: service.BusFsyncPerMessage,
		ListenHost:  hostOf(h.cfg.BusAddress()),
		ListenPort:  portOf(h.cfg.BusAddress()),
		TLS:         h.serverTLS(),
		Logger:      log,
	})
	if err != nil {
		return service.Attempt{}, err
	}

	resources, err := h.buildResources(ctx, hub, log)
	if err != nil {
		hub.Close()
		return service.Attempt{}, err
	}
	h.hub.publish(resources)

	// The runtime does not guarantee the Runner below ever runs: it
	// re-checks the coordinator after Setup returns, and a module context
	// canceled while Setup was in flight ends the attempt without calling
	// it. StartHub generating keys and initializing JetStream is the
	// slowest thing in startup, so that window is reachable — and an
	// attempt that ended there would leave the embedded server, its
	// WebSocket listener and its JetStream store running, with the handle
	// still pointing at a live generation nobody owns.
	//
	// This narrows the window rather than closing it: cancellation between
	// this check and the runtime's own leaves the same leak. Closing it
	// needs a cleanup hook on service.Attempt, whose lifetime contract this
	// Setup does not currently satisfy, and that is a runtime change.
	if err := ctx.Err(); err != nil {
		h.hub.withdraw()
		hub.Close()
		return service.Attempt{}, err
	}

	return service.Attempt{Runner: func(ctx context.Context) error {
		<-ctx.Done()
		// Withdraw before closing, so a module starting in the gap waits for
		// the next hub rather than taking this one as it goes down.
		h.hub.withdraw()
		hub.Close()
		return nil
	}}, nil
}

func (h *assembly) buildResources(ctx context.Context, hub *edgebus.Hub, log *slog.Logger) (*busResources, error) {
	lanes, err := hub.JetStream().KeyValue(ctx, edgebus.LaneBucket)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Msg("open the lane bucket")
	}
	edges, err := hub.JetStream().KeyValue(ctx, edgebus.EdgeBucket)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Msg("open the edge bucket")
	}

	lane := journal.New(lanes, nil)
	audit := centralaudit.New(auditPublisher(hub), hub.Tenant(), nil)
	intervals := h.cfg.Intervals()
	dispatch := dispatchapi.New(dispatchapi.Config{
		Journal:       lane,
		Resolver:      h.registry,
		Watch:         lanes,
		EdgeID:        edgeIDFromContext,
		Resend:        intervals.DispatchResend,
		SweepInterval: intervals.ReadSweep,
		Audit:         audit,
		Logger:        log,
	})

	return &busResources{
		hub:      hub,
		lanes:    lanes,
		journal:  lane,
		edges:    edgestore.New(edges),
		dispatch: dispatch,
	}, nil
}

// setupForwarder ships every edge's buffered telemetry on to the collector.
func (h *assembly) setupForwarder(ctx context.Context) (service.Attempt, error) {
	resources, err := h.hub.await(ctx)
	if err != nil {
		return service.Attempt{}, err
	}
	forwarder, err := edgebus.StartForwarder(ctx, resources.hub, edgebus.ForwarderConfig{
		Endpoint:      h.cfg.TelemetryEndpoint(),
		Headers:       h.cfg.TelemetryHeaders(),
		Logger:        service.Logger(ctx),
		MeterProvider: service.MeterProvider(ctx),
	})
	if err != nil {
		return service.Attempt{}, err
	}
	// See setupHub: the Runner is not guaranteed to run, so an attempt
	// canceled during Setup would leave this forwarder's goroutines and
	// its subscription behind.
	if err := ctx.Err(); err != nil {
		forwarder.Close()
		return service.Attempt{}, err
	}

	return service.Attempt{Runner: func(ctx context.Context) error {
		<-ctx.Done()
		forwarder.Close()
		return nil
	}}, nil
}

// setupJournal runs the read sweeper: the one piece of the journal's work
// that happens on a clock rather than in answer to a call. A read whose edge
// holds no open stream is closed here when its deadline passes, so its waiter
// is not left hanging on a stream that will never carry the answer.
func (h *assembly) setupJournal(ctx context.Context) (service.Attempt, error) {
	resources, err := h.hub.await(ctx)
	if err != nil {
		return service.Attempt{}, err
	}

	return service.Attempt{Runner: func(ctx context.Context) error {
		<-resources.dispatch.RunSweeper(ctx, resources.lanes)
		return nil
	}}, nil
}

// setupDrift polls every managed interface and compares it against what
// central expects.
func (h *assembly) setupDrift(ctx context.Context) (service.Attempt, error) {
	resources, err := h.hub.await(ctx)
	if err != nil {
		return service.Attempt{}, err
	}
	view, err := telemetry.NewView(telemetry.ViewConfig{
		MeterProvider: service.MeterProvider(ctx),
		Logger:        service.Logger(ctx),
	})
	if err != nil {
		return service.Attempt{}, err
	}

	intervals := h.cfg.Intervals()
	poller, err := drift.New(drift.Config{
		Journal:      resources.journal,
		Resolver:     h.registry,
		Audit:        centralaudit.New(auditPublisher(resources.hub), resources.hub.Tenant(), nil),
		Telemetry:    view,
		Interval:     intervals.Drift,
		ReadDeadline: intervals.DriftReadDeadline,
		Logger:       service.Logger(ctx),
	})
	if err != nil {
		return service.Attempt{}, err
	}

	return service.Attempt{Runner: poller.Run}, nil
}

// setupConnect serves the four edge-facing services and the two an operator
// calls.
func (h *assembly) setupConnect(ctx context.Context) (service.Attempt, error) {
	resources, err := h.hub.await(ctx)
	if err != nil {
		return service.Attempt{}, err
	}
	view, err := telemetry.NewView(telemetry.ViewConfig{
		MeterProvider: service.MeterProvider(ctx),
		Logger:        service.Logger(ctx),
	})
	if err != nil {
		return service.Attempt{}, err
	}

	handler, err := h.mux(resources, service.Logger(ctx), view)
	if err != nil {
		return service.Attempt{}, err
	}
	server := &http.Server{
		Addr:              h.cfg.APIAddress(),
		Handler:           handler,
		TLSConfig:         h.serverTLS(),
		ReadHeaderTimeout: 10 * time.Second,
		// Without this, net/http writes a recovered handler panic and every
		// TLS handshake failure to the standard logger: plain lines on
		// stderr, outside the JSON stream everything else in this process
		// goes to, with no service attributes and no trace correlation. A
		// handshake failure is how a misprovisioned edge presents, so these
		// are lines an operator needs to be able to find with the rest.
		ErrorLog: slog.NewLogLogger(service.Logger(ctx).Handler(), slog.LevelWarn),
	}

	return service.Attempt{Runner: func(ctx context.Context) error {
		errCh := make(chan error, 1)
		go func() {
			// The certificate and key are already in TLSConfig; ServeTLS
			// takes them from there when both paths are empty.
			errCh <- server.ListenAndServeTLS("", "")
		}()

		select {
		case err := <-errCh:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return errs.From(err).Code(ErrCodeStart).Attr("address", h.cfg.APIAddress()).Msg("serve the device api")
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
			defer cancel()
			// Shutdown never cuts an active connection: it closes the
			// listeners, closes idle connections, and returns the
			// context's error once the grace passes, leaving in-flight
			// handlers running. An edge holds a dispatch stream open
			// indefinitely, so that error is the ordinary case here, not
			// an anomaly — and discarding it returned from Run with those
			// handler goroutines still looping against a bus their module
			// had already closed.
			//
			// In production the process exits and takes them with it. In
			// an in-process host, which the end-to-end test needs so it
			// can start and stop central repeatedly, they survive the test
			// that created them.
			if err := server.Shutdown(shutdown); err != nil {
				_ = server.Close()
			}
			<-errCh
			return nil
		}
	}}, nil
}
