package host

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/audit/v1/auditv1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1/dispatchv1connect"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/busattach"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/dispatch"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/identity"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/report"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
)

// ErrCodeStart is an agent that cannot be assembled from what it was given.
var ErrCodeStart = errs.NewCode("agent/start")

// loopbackIsInsecure is the value the telemetry connection pins Insecure to.
// The exporter's target is a loopback address this process bound itself, so
// there is no transport to secure; taken by address because the setting is a
// *bool, where nil means "read the environment".
var loopbackIsInsecure = true

// serviceName and serviceNamespace identify this process to the runtime and
// to every signal it exports.
const (
	serviceName      = "agent"
	serviceNamespace = "flowseer"
	// resendInterval is how often the queue re-sends what central has not
	// confirmed. It is not configurable: the queue supersedes rather than
	// accumulates, so this is a retry cadence rather than a bound on
	// anything, and a deployment has no information about it an operator
	// could act on.
	resendInterval = 15 * time.Second
)

// Run brings the agent up and runs it until ctx ends.
//
// # The order, and what forces each step
//
// Every link below is required by a different fact, and none of them is
// visible from the call it constrains. Written together because a reader
// meeting any one of them alone would read it as arbitrary sequencing, and
// each of them fails silently rather than loudly.
//
//  1. The identity comes first, and its failure is fatal. Every call but
//     Enroll carries an assertion signed by the key central registered, so an
//     agent that could not enroll cannot make any other call — there is
//     nothing to carry on with, which is why this is the one failure that
//     ends the process rather than being logged and retried.
//
//  2. The signing client is built from the enrollment's trust anchors, not
//     the provisioned ones. EnrollResponse is the authoritative set and
//     replaces what the edge shipped with, so the two dialers here and the
//     leaf below cannot pin different certificates.
//
//     It is the set this edge pins for the rest of its life. Nothing central
//     sends afterwards carries a replacement — trust_anchors appears on the
//     provisioning and on the enrollment answer and nowhere else — and an
//     enrolled edge never calls Enroll again, so rotating central's chain
//     means re-enrolling every edge in the field, not re-provisioning it.
//
//  3. The bus attachment comes next, and its failure is fatal too. AttachBus
//     is a signed call, so it cannot precede the identity, and the receiver
//     it binds is where this agent's own telemetry goes. Fatal because the
//     alternative is an agent whose observability is silently absent, which
//     is the state hardest to notice from outside.
//
//  4. The telemetry export is pointed at the receiver's own bound address,
//     which is fresh on every start: the receiver binds 127.0.0.1:0 so a
//     port collision cannot stop the agent. An exporter built before the
//     receiver, or against a remembered address, exports into nothing and
//     says nothing. The protocol and compression are pinned rather than left
//     to OTEL_EXPORTER_OTLP_*, because the receiver forwards bodies verbatim
//     and answers 415 to anything it cannot pass on.
//
//  5. The lane is built before the loops that drive it, and the onboarder
//     before the dispatch loop. A dispatch for a device with no lane has
//     nowhere to go, and the loop re-lists before every attempt precisely so
//     that a dispatch never arrives for a device this edge has not onboarded.
//
// The attachment is closed after the runtime returns, in that order: the
// telemetry export lives as long as the attachment and ends with it.
func Run(ctx context.Context, cfg *Config, version string, opts Options) error {
	if cfg == nil {
		return errs.New().Code(ErrCodeStart).Msg("the agent was given no configuration")
	}
	base := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel()}))

	store, err := identity.NewStore(cfg.StateDir())
	if err != nil {
		return err
	}
	enrolment := edgev1connect.NewEdgeServiceClient(
		identity.PinnedClient(cfg.ProvisionedAnchors()), cfg.CentralURL())
	edge, err := identity.Establish(ctx, store, enrolment, cfg.SetupKey())
	if err != nil {
		return err
	}
	edgeID := edge.Enrollment.GetEdge().GetEdge().GetId()
	base = base.With(slog.String("flowseer.edge.id", edgeID))

	// 2. The clients every later call goes through.
	signer := edge.Signer(ctx, time.Now, base)
	signed := identity.SigningClient(edge.TrustAnchors(), signer)
	edgeClient := edgev1connect.NewEdgeServiceClient(signed, cfg.CentralURL())
	dispatchClient := dispatchv1connect.NewDispatchServiceClient(signed, cfg.CentralURL())
	auditClient := auditv1connect.NewAuditServiceClient(signed, cfg.CentralURL())

	bufferBytes, bufferAge := cfg.Buffer()
	attachment, err := busattach.Attach(ctx, edgeClient, busattach.Config{
		StateDir:       cfg.StateDir(),
		EdgeID:         edgeID,
		TrustAnchors:   edge.TrustAnchors(),
		BufferMaxBytes: bufferBytes,
		BufferMaxAge:   bufferAge,
		Logger:         base,
	})
	if err != nil {
		return err
	}
	defer attachment.Close(context.WithoutCancel(ctx))

	assembly := &assembly{
		cfg:      cfg,
		opts:     opts,
		edgeID:   edgeID,
		signer:   signer,
		edge:     edgeClient,
		dispatch: dispatchClient,
		audit:    auditClient,
	}

	// One module: the lane and the three loops that drive it share its
	// lifetime, and a restart that rebuilt the lane without them — or them
	// without it — would leave a dispatch loop submitting into a lane nobody
	// drains.
	return service.Run(ctx, service.Config{
		Identity: service.Identity{Name: serviceName, Namespace: serviceNamespace, Version: version},
		Logger:   base,
		Telemetry: service.TelemetryConfig{
			Endpoint: attachment.Endpoint,
			// Pinned, not defaulted, and pinned whole. The receiver stores
			// and forwards each body unchanged, so it accepts only what it
			// can pass on: an environment that chose grpc or gzip would fail
			// every export with a 415 nothing here would explain.
			//
			// The connection settings are pinned for a sharper reason. This
			// endpoint is a loopback address this process bound itself, so
			// there is no transport security to configure and no collector
			// to authenticate to — but every one of these settings falls
			// back to OTEL_EXPORTER_OTLP_* when the field is unset. A host
			// carrying those for an unrelated collector would have the agent
			// refuse its own configuration at startup, after enrolling and
			// attaching, and restart into the same refusal for as long as
			// the variable is set.
			Protocol:    "http/protobuf",
			Compression: "none",
			Insecure:    &loopbackIsInsecure,
			Headers:     map[string]string{},
		},
		Modules: []service.Module{{Name: "lane", Leaf: &service.Leaf{Setup: assembly.setup}}},
	})
}

// assembly holds what the lane module draws from: the things read or
// established once, before the runtime started.
type assembly struct {
	cfg      *Config
	opts     Options
	edgeID   string
	signer   *identity.Signer
	edge     edgev1connect.EdgeServiceClient
	dispatch dispatchv1connect.DispatchServiceClient
	audit    auditv1connect.AuditServiceClient
}

// setup builds one attempt: the lane, the onboarder, the report queue, and
// the loops that drive them.
func (a *assembly) setup(ctx context.Context) (service.Attempt, error) {
	log := service.Logger(ctx)

	telemetry, err := access.NewTelemetry(access.TelemetryConfig{
		TracerProvider: service.TracerProvider(ctx),
		MeterProvider:  service.MeterProvider(ctx),
		Propagator:     service.Propagator(ctx),
		Logger:         log,
	})
	if err != nil {
		return service.Attempt{}, err
	}

	reporter := &laneReporter{}
	lane := access.NewLane(a.laneConfig(reporter, telemetry))

	// The demultiplexer and the queue each need the other: the demultiplexer
	// reports through the queue, and the queue tells the demultiplexer when
	// central has taken a report that ends an operation, so it can release
	// what it remembers about it. The cycle is closed here rather than broken
	// by giving one of them its own answer.
	confirmations := &confirmations{}
	queue, err := report.New(report.Config{Client: a.dispatch, Confirm: confirmations, Logger: log})
	if err != nil {
		return service.Attempt{}, err
	}
	demux := dispatch.NewDemux(lane, queue, log)
	confirmations.demux = demux
	reporter.out = queue

	onboarder, err := lanehost.NewOnboarder(lanehost.OnboardConfig{
		Client: a.edge,
		Lane:   lane,
		Edge:   edgeRefOf(a.edgeID),
		// Nil for a packaged deployment, which is what NewOnboarder reads
		// as the real dialers.
		OpenSNMP:  a.opts.OpenSNMP,
		OpenShell: a.opts.OpenShell,
		// PerDeviceTimeout left at the onboarder's default. It bounds one
		// device's probe, which matters because this runs before every
		// attempt to open the dispatch stream and a device that is powered
		// off does not refuse the probe, it says nothing. Not configurable:
		// no deployment has information about it an operator could act on.
		Logger: log,
	})
	if err != nil {
		return service.Attempt{}, err
	}

	backoffFloor, backoffCeiling := a.cfg.DispatchBackoff()
	contact := &dispatch.Contact{}
	if err := registerContactInstruments(ctx, contact, queue); err != nil {
		return service.Attempt{}, err
	}

	return service.Attempt{Runner: func(ctx context.Context) error {
		defer func() { _, _ = lane.Close(context.WithoutCancel(ctx)) }()
		// Before the lane closes: an operation still in the lane reports
		// through the queue, and the queue's own loop has returned by the
		// time this runs. Without the wait those terminal reports are made
		// into a queue nothing will drain and central never hears them.
		defer demux.Wait()

		return runAll(ctx,
			func(ctx context.Context) error { return queue.Run(ctx, resendInterval) },
			func(ctx context.Context) error {
				return lanehost.RunHeartbeat(ctx, lanehost.HeartbeatConfig{
					Client:          a.edge,
					Lane:            lane,
					AdoptServerTime: a.signer.AdoptServerTime,
					AgentVersion:    service.Version(ctx),
					Interval:        a.cfg.Heartbeat(),
					Logger:          log,
				})
			},
			func(ctx context.Context) error {
				return dispatch.Run(ctx, dispatch.Config{
					Client:     a.dispatch,
					Handler:    demux,
					Resync:     onboarder.Sync,
					MinBackoff: backoffFloor,
					MaxBackoff: backoffCeiling,
					Logger:     log,
				}, contact)
			})
	}}, nil
}

// registerContactInstruments exports what the dispatch loop and the report
// queue count, so the numbers an operator is told to read together can be
// reached from outside the process.
//
// The two loops here act when nothing is happening, and the failure that hides
// is total: a dispatch loop that has never connected looks exactly like one
// with nothing to do. The events each loop emits carry a connection or a
// disconnection as it happens; what they cannot carry is the state — a client
// at zero connections and climbing failures, which emits one record per
// attempt and no total, or a first stream still open, which emits nothing at
// all. These are the totals that tell those apart.
func registerContactInstruments(ctx context.Context, contact *dispatch.Contact, queue *report.Queue) error {
	meter := service.Meter(ctx)
	observe := func(name, unit, description string, read func() int64) error {
		_, err := meter.Int64ObservableCounter(name,
			metric.WithUnit(unit),
			metric.WithDescription(description),
			metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
				o.Observe(read())
				return nil
			}))
		return err
	}

	return errors.Join(
		observe("flowseer.edge.dispatch.connections", "{stream}",
			"dispatch streams central has served this agent", contact.Connections),
		observe("flowseer.edge.dispatch.failures", "{attempt}",
			"dispatch stream attempts that failed, opening or mid-stream", contact.Failures),
		observe("flowseer.edge.dispatch.messages", "{message}",
			"dispatches received on the stream", contact.Messages),
		observe("flowseer.edge.reports.sent", "{report}",
			"reports central has confirmed", queue.Sent),
		observe("flowseer.edge.reports.dropped", "{report}",
			"reports discarded at the queue ceiling", queue.Dropped),
	)
}

func (a *assembly) laneConfig(reporter *laneReporter, telemetry *access.Telemetry) access.Config {
	read, submission := access.NewConnectCredentials(a.edge)
	return access.Config{
		QueueCapacity:         4,
		ReadCredentials:       read,
		SubmissionCredentials: submission,
		Reporter:              reporter,
		Audit:                 report.NewDeliverer(a.audit, a.edgeID),
		Telemetry:             telemetry,
		Clock:                 a.clock(),
	}
}

// clock is the lane's source of time: what the deployment substituted, or the
// wall clock. The lane treats a nil Clock as time.Now too, but reads it in
// enough places that a nil arriving there would be a nil this package failed
// to resolve rather than a default it chose.
func (a *assembly) clock() func() time.Time {
	if a.opts.Clock != nil {
		return a.opts.Clock
	}
	return time.Now
}

// confirmations forwards a confirmation from the report queue to the
// demultiplexer that is waiting for it. It exists only to close the cycle
// between the two, and its field is written once during setup, before the
// goroutines that read it are started.
type confirmations struct{ demux *dispatch.Demux }

func (c *confirmations) Confirmed(device string, sequence uint64) {
	c.demux.Confirmed(device, sequence)
}

// edgeRefOf names this edge on every observation it makes.
func edgeRefOf(edgeID string) *edgev1.EdgeGlobalRef {
	return edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
	}.Build()
}

// runAll runs every loop until one of them returns or ctx ends, and answers
// with the first error any of them gave.
//
// It is a dozen lines rather than errgroup.WithContext because nothing else
// in this repository uses errgroup: it is an indirect dependency, and one
// join is a thin reason to promote it to a direct one. If a second caller
// ever wants the same shape, take the library and delete this.
//
// One returning ends the attempt, whether it failed or not. These three share
// the lane: a heartbeat that stopped would leave nothing to freeze the lane
// on lost contact, and a dispatch loop that stopped would leave a queue
// re-sending reports for operations nothing is dispatching. A supervisor
// rebuilding all three together is the only shape that keeps them consistent.
//
// The join is a channel each goroutine sends its own result to exactly once,
// not a sync.WaitGroup: a WaitGroup's Done only proves the goroutine's own
// deferred calls ran, and on the panic path those run before the recovered
// panic reaches results, so waiting on Done and then peeking the channel
// could read it before that send lands. Receiving len(loops) times waits on
// the channel itself, which a send always precedes.
func runAll(ctx context.Context, loops ...func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan error, len(loops))
	for _, loop := range loops {
		spawn.Go(ctx, "runAll loop", func() {
			// Canceled on the way out, so the first loop to return stops the
			// others rather than leaving them running under a module attempt
			// that has already ended.
			defer cancel()
			results <- loop(ctx)
		}, spawn.ReportTo(func(err error) { results <- err }))
	}

	var firstErr error
	for range loops {
		if err := <-results; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
