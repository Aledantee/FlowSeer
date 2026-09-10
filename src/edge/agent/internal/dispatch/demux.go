package dispatch

import (
	"context"
	"log/slog"
	"sync"

	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
)

// Lane is the device access lane a dispatch drives. Satisfied by
// *access.Lane; an interface so the routing can be tested without devices.
type Lane interface {
	Submit(ctx context.Context, opts access.SubmitOptions) (*integrationv1.ExecuteResult, error)
	HandleCheckpoint(deviceKey string, req *integrationv1.CheckpointRequest) error
	HandleTerminalAck(ctx context.Context, deviceKey string, ack *integrationv1.TerminalResultAck) error
	ResolveHold(ctx context.Context, deviceKey string, resolved *integrationv1.HoldResolved) error
}

// Outbound is where a report goes. It must not block: the demultiplexer
// calls it while reading central's stream, and a slow send would stall every
// other device's dispatches behind it. The host's re-send queue implements it.
type Outbound interface {
	Report(ctx context.Context, report *integrationv1.ReportRequest)
}

// Demux turns each message on the dispatch stream into a lane call, and
// answers central for the ones it cannot make.
type Demux struct {
	lane     Lane
	out      Outbound
	registry *registry
	log      *slog.Logger

	// running tracks the operations still in the lane, so Wait can hold a
	// shutdown until they have made their reports rather than letting the
	// queue that carries those reports stop first.
	running sync.WaitGroup
}

// NewDemux builds the handler the dispatch loop drives.
func NewDemux(lane Lane, out Outbound, log *slog.Logger) *Demux {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Demux{lane: lane, out: out, registry: newRegistry(), log: log}
}

// Wait blocks until every operation this demultiplexer started has finished.
func (d *Demux) Wait() { d.running.Wait() }

// Confirmed releases what this edge remembers about an operation, once
// central has acknowledged a report that ends it.
//
// A report that ends it, not any report about it. Central takes a progress
// report and an acknowledgement while the operation is still running, and
// releasing here on one of those would leave the sequence admittable again
// while the lane still holds it — a re-dispatch would then run it on the
// device a second time. The report queue draws that line and is the only
// caller.
//
// Not when the operation finishes, either. Central re-dispatches a sequence exactly
// because it has no report recorded, so a completed operation is the case
// most likely to be dispatched again — forgetting at completion means the
// next re-dispatch runs it on the device a second time, which is what this
// registry exists to prevent. The entry lives until central says it has the
// report, which is what the host's re-send queue is tracking anyway.
//
// That makes the bound on this map "reports not yet confirmed" rather than
// "operations ever run". A central that never confirms grows it, and that
// is the same condition as a re-send queue that never drains: one problem
// with one symptom, not two.
func (d *Demux) Confirmed(device string, sequence uint64) {
	d.registry.forget(device, sequence)
}

// Handle routes one dispatch. It returns an error only for a message this
// edge could not act on at all; the loop logs those and keeps the stream,
// because central re-sends what it is owed and one device's problem must not
// cost every other device's messages.
func (d *Demux) Handle(ctx context.Context, message *integrationv1.SubscribeResponse) error {
	device := message.GetDeviceId()
	switch {
	case message.HasExecute():
		return d.execute(ctx, device, message.GetExecute())
	case message.HasCheckpoint():
		d.deliver(ctx, device, message.GetCheckpoint().GetSequence(),
			integrationv1.DispatchKind_DISPATCH_KIND_CHECKPOINT,
			func() error { return d.lane.HandleCheckpoint(device, message.GetCheckpoint()) })
		return nil
	case message.HasTerminalAck():
		d.deliver(ctx, device, message.GetTerminalAck().GetSequence(),
			integrationv1.DispatchKind_DISPATCH_KIND_TERMINAL_ACK,
			func() error { return d.lane.HandleTerminalAck(ctx, device, message.GetTerminalAck()) })
		return nil
	case message.HasHoldResolved():
		d.deliver(ctx, device, message.GetHoldResolved().GetSequence(),
			integrationv1.DispatchKind_DISPATCH_KIND_HOLD_RESOLVED,
			func() error { return d.lane.ResolveHold(ctx, device, message.GetHoldResolved()) })
		return nil
	default:
		// A dispatch arm this build does not know. Not refusable — Refused
		// needs a sequence and a kind, and neither is readable here — so it
		// is logged and dropped, which is what an edge older than its
		// central does.
		return errs.New().Code(ErrCodeSubscribe).Attr("device", device).
			Msg("dispatch carries no arm this agent understands")
	}
}

// execute admits a mutation or a read, or answers a duplicate from what this
// edge already reported.
//
// The duplicate case is the reason the registry exists. Central re-dispatches
// a sequence whenever it has no report recorded for it — after an Onboarded
// report clears a record's confirmations, most often — and running it again
// would apply the operation to the device twice. What central wants is the
// report, so it gets the report.
func (d *Demux) execute(ctx context.Context, device string, request *integrationv1.ExecuteRequest) error {
	sequence := request.GetSequence()
	if !d.registry.admit(device, sequence) {
		report, known := d.registry.report(device, sequence)
		switch {
		case report != nil:
			d.out.Report(ctx, reportOf(func(r *integrationv1.ReportRequest) { r.SetResult(report) }, device))
		case known:
			// In flight, and nothing said about it yet. Silence is right:
			// the report is made when the operation reaches a phase worth
			// reporting, and inventing one here would tell central something
			// this edge does not know.
			d.log.DebugContext(ctx, "duplicate dispatch for an operation still running",
				slog.String("flowseer.device.id", device),
				slog.Uint64("flowseer.device.sequence", sequence))
		}
		return nil
	}

	d.running.Add(1)
	go func() {
		defer d.running.Done()

		// Submit blocks until the operation reaches a terminal result, which
		// for a mutation means central has acknowledged it. It runs on its
		// own goroutine so the dispatch stream keeps being read: one device's
		// operation must not stop every other device's messages.
		result, err := d.lane.Submit(ctx, access.SubmitOptions{DeviceKey: device, Request: request})
		if err != nil {
			d.registry.discard(device, sequence)
			d.refuse(ctx, device, sequence, integrationv1.DispatchKind_DISPATCH_KIND_EXECUTE, err)
			return
		}
		d.registry.record(device, result)
		d.out.Report(ctx, reportOf(func(r *integrationv1.ReportRequest) { r.SetResult(result) }, device))
	}()
	return nil
}

// deliver hands central's message to the lane and refuses it if the lane will
// not take it. A refusal is an answer central acts on, not a failure of this
// edge to handle the dispatch, so nothing is returned to the stream loop.
func (d *Demux) deliver(ctx context.Context, device string, sequence uint64, kind integrationv1.DispatchKind, call func() error) {
	if err := call(); err != nil {
		d.refuse(ctx, device, sequence, kind, err)
	}
}

// refuse tells central this edge will not act on a dispatch, and why.
//
// The lane's own error code goes on the wire unchanged. Which codes are
// retryable is central's policy — it holds the record and decides what a
// refusal means for it — and an edge that translated them would be deciding
// on central's behalf with less to go on.
func (d *Demux) refuse(ctx context.Context, device string, sequence uint64, kind integrationv1.DispatchKind, cause error) {
	code, ok := errs.CodeOf(cause)
	if !ok {
		// Refused's code is a required, pattern-constrained field, so an
		// uncoded error cannot be sent as itself. It goes under this code
		// rather than being dropped, because central needs to know the
		// dispatch was not taken.
		//
		// Its own code, not one of the lane's. Central classifies a refusal
		// by code, and every code it names carries a decision: unknown-device
		// resolves against the registry and disposes the mutation REJECTED
		// when the device is no longer listed. Sending one of those for an
		// error that is not that condition would have central act terminally
		// on a cause that never occurred. A code central does not name falls
		// to its default, which leaves the row owed and re-dispatched — the
		// right answer for a failure this edge could not classify.
		code = ErrCodeUncodedRefusal
		d.log.WarnContext(ctx, "refusing a dispatch for an uncoded error",
			slog.String("flowseer.device.id", device),
			slog.String("error.type", string(code)),
			slog.String("flowseer.edge.refusal.cause", cause.Error()))
	}

	refused := &integrationv1.Refused{}
	refused.SetSequence(sequence)
	refused.SetKind(kind)
	refused.SetCode(string(code))
	d.out.Report(ctx, reportOf(func(r *integrationv1.ReportRequest) { r.SetRefused(refused) }, device))
}

// reportOf builds a ReportRequest for one device with one arm set.
func reportOf(set func(*integrationv1.ReportRequest), device string) *integrationv1.ReportRequest {
	report := &integrationv1.ReportRequest{}
	report.SetDeviceId(device)
	set(report)
	return report
}
