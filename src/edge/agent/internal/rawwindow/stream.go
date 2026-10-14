package rawwindow

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1/ingestv1connect"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	"go.aledante.io/FlowSeer/src/common/fsconv"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/subscribeloop"
)

// reportBatch is the most reports one Report call carries, the bound the schema
// puts on it.
const reportBatch = 16

// defaultReportInterval is how often a reporter flushes usage counters and
// sweeps for expired windows when nothing wakes it sooner.
const defaultReportInterval = 5 * time.Second

// Open adapts client into the opener the subscribe loop runs. Each attempt
// carries the controller's boot generation, epoch, and enabled sources, which
// is what lets central fence a delayed subscription of an earlier process.
func Open(client ingestv1connect.RawWindowEdgeServiceClient, c *Controller) subscribeloop.Opener[edgeingestv1.SubscribeResponse] {
	return func(ctx context.Context) (subscribeloop.Stream[edgeingestv1.SubscribeResponse], error) {
		sources := make([]*rawwindowv1.RawWindowSource, len(c.sources))
		for i, key := range c.sources {
			sources[i] = rawwindowv1.RawWindowSource_builder{Key: proto.String(key)}.Build()
		}
		return client.Subscribe(ctx, connect.NewRequest(edgeingestv1.SubscribeRequest_builder{
			BootGeneration: proto.Uint64(c.generation),
			ProcessEpoch:   proto.String(c.epoch),
			EnabledSources: sources,
		}.Build()))
	}
}

// Events names what the control stream's telemetry is known by.
var Events = subscribeloop.Events{
	Connected:          "flowseer.edge.raw_window.connected",
	Disconnected:       "flowseer.edge.raw_window.disconnected",
	Dropped:            "flowseer.edge.raw_window.dropped",
	ResyncFailed:       "flowseer.edge.raw_window.resync_failed",
	ConnectionCountKey: "flowseer.edge.raw_window.connections",
	MessageCountKey:    "flowseer.edge.raw_window.messages",
}

// LogAttrs returns the attributes of a dropped control: the window id it
// named, and nothing of the window's selector or reason.
func LogAttrs(message *edgeingestv1.SubscribeResponse) []slog.Attr {
	if id := message.GetControl().GetRef().GetRawWindow().GetId(); id != "" {
		return []slog.Attr{slog.String("flowseer.raw_window.id", id)}
	}
	return nil
}

// Handler applies the frames of the control stream to a controller.
type Handler struct {
	Controller *Controller
}

// Handle applies one frame. The ready frame needs nothing from the edge, since
// central resends every owed control after it. A control the controller refuses
// returns its error, which the loop counts and logs as a dropped message.
func (h Handler) Handle(_ context.Context, message *edgeingestv1.SubscribeResponse) error {
	if message.HasControl() {
		return h.Controller.Apply(message.GetControl())
	}
	return nil
}

// ReporterConfig configures [RunReporter].
type ReporterConfig struct {
	Client     ingestv1connect.RawWindowEdgeServiceClient
	Controller *Controller
	// Interval is how often the reporter flushes with nothing to wake it. Zero
	// means five seconds.
	Interval time.Duration
	// Logger receives report failures. Nil discards.
	Logger *slog.Logger
}

// RunReporter sends the controller's cumulative reports to central until ctx
// ends. A failed call leaves the reports owed and the next tick sends them
// again, so a window's end reaches central once the stream and the network are
// back. It returns nil when ctx ends.
func RunReporter(ctx context.Context, cfg ReporterConfig) error {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultReportInterval
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		flush(ctx, cfg.Client, cfg.Controller, log)
		select {
		case <-ctx.Done():
			return nil
		case <-cfg.Controller.Changed():
		case <-ticker.C:
		}
	}
}

// flush sends every owed report in batches, stopping at the first failure.
func flush(ctx context.Context, client ingestv1connect.RawWindowEdgeServiceClient, c *Controller, log *slog.Logger) {
	c.Sweep()
	for ctx.Err() == nil {
		owed := c.PendingReports(reportBatch)
		if len(owed) == 0 {
			return
		}
		messages := make([]*edgeingestv1.RawWindowReport, len(owed))
		for i, r := range owed {
			messages[i] = r.Message
		}
		_, err := client.Report(ctx, connect.NewRequest(edgeingestv1.ReportRequest_builder{
			BootGeneration: proto.Uint64(c.generation),
			ProcessEpoch:   proto.String(c.epoch),
			Reports:        messages,
		}.Build()))
		if err != nil {
			if ctx.Err() == nil {
				log.WarnContext(ctx, "raw window report failed",
					fsconv.EventName("flowseer.edge.raw_window.report_failed"),
					fsconv.ErrorTypeString(connect.CodeOf(err).String()),
					slog.Int("flowseer.raw_window.owed", len(owed)))
			}
			return
		}
		c.Reported(owed)
	}
}
