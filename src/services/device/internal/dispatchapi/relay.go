package dispatchapi

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/types/known/timestamppb"

	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

// ErrCodeReadDeadline is the error an open read is closed with when its
// deadline passes with no observation. It is declared under this package's own
// prefix: errs.NewCode panics on a duplicate registration, and the access/
// prefix belongs to the lane module, which the end-to-end binary links
// alongside this one.
var ErrCodeReadDeadline = errs.NewCode("dispatchapi/read-deadline")

// sender is the dispatch target. A *connect.ServerStream satisfies it; a test
// captures the sent messages through the same interface.
type sender interface {
	Send(*integrationv1.SubscribeResponse) error
}

// dispatchPass derives and sends every row the edge's devices owe, sweeping
// each device's expired reads first so a read past its deadline is closed and
// never sent. A per-device store failure is logged and the pass continues; a
// send failure ends the stream.
func (s *Service) dispatchPass(ctx context.Context, edgeID string, out sender) error {
	devices, err := s.cfg.Resolver.Devices(ctx, edgeID)
	if err != nil {
		return errs.From(err).Code(ErrCodeResolve).Attr("edge", edgeID).Msg("resolve edge devices")
	}
	for _, deviceID := range devices {
		fatal, err := s.dispatchDevice(ctx, deviceID, out)
		if err != nil {
			if fatal {
				return err // the stream is broken; owed stays in the record
			}
			s.log.WarnContext(ctx, "dispatch pass skipped a device", slog.String("flowseer.device.id", deviceID), slog.String("error.type", errorType(err)))
		}
	}
	return nil
}

// dispatchDevice sweeps then sends one device's owed rows. The returned bool
// is whether the error broke the stream (a send failure) rather than the
// record store (which is transient and skipped).
func (s *Service) dispatchDevice(ctx context.Context, deviceID string, out sender) (fatal bool, err error) {
	if _, err := s.cfg.Journal.SweepExpiredReads(ctx, deviceID, s.clock(), s.sweepError()); err != nil {
		return false, errs.Wrap(err, "sweep expired reads")
	}
	rec, err := s.cfg.Journal.Record(ctx, deviceID)
	if err != nil {
		return false, errs.Wrap(err, "read lane record")
	}
	for _, owed := range journal.OwedRows(rec, s.clock()) {
		msg, ok := s.rowToDispatch(ctx, deviceID, rec, owed)
		if !ok {
			continue // a row that cannot be built this pass; logged in the builder
		}
		if err := out.Send(msg); err != nil {
			return true, errs.Wrap(err, "send dispatch")
		}
	}
	return false, nil
}

// rowToDispatch builds the wire message for one owed row, or reports that it
// cannot be built this pass (a mutation whose horizon or admission time is
// missing, or a read whose entry has gone).
func (s *Service) rowToDispatch(ctx context.Context, deviceID string, rec *storev1.DeviceLaneRecord, owed journal.Owed) (*integrationv1.SubscribeResponse, bool) {
	resp := &integrationv1.SubscribeResponse{}
	resp.SetDeviceId(deviceID)
	switch owed.Kind {
	case journal.OwedHoldResolved:
		hr := &integrationv1.HoldResolved{}
		hr.SetSequence(owed.Sequence)
		resp.SetHoldResolved(hr)
	case journal.OwedCheckpoint:
		cp := &integrationv1.CheckpointRequest{}
		cp.SetSequence(owed.Sequence)
		resp.SetCheckpoint(cp)
	case journal.OwedTerminalAck:
		ta := &integrationv1.TerminalResultAck{}
		ta.SetSequence(owed.Sequence)
		ta.SetDisposition(owed.Disposition)
		resp.SetTerminalAck(ta)
	case journal.OwedExecute:
		exec, ok := s.mutationExecute(ctx, deviceID, rec, owed)
		if !ok {
			return nil, false
		}
		resp.SetExecute(exec)
	case journal.OwedRead:
		exec, ok := readExecute(rec, owed.Sequence)
		if !ok {
			s.log.WarnContext(ctx, "owed read has no open entry", slog.String("flowseer.device.id", deviceID), slog.Uint64("flowseer.device.sequence", owed.Sequence))
			return nil, false
		}
		resp.SetExecute(exec)
	default:
		return nil, false
	}
	return resp, true
}

// mutationExecute builds the ExecuteRequest for the open mutation. Its
// deadline is the admission time plus the binding's delayed-apply horizon, so
// a resumed dispatch measures the horizon from admission — the moment the
// command was handed to the device is lost with the edge that held it.
func (s *Service) mutationExecute(ctx context.Context, deviceID string, rec *storev1.DeviceLaneRecord, owed journal.Owed) (*integrationv1.ExecuteRequest, bool) {
	m := rec.GetMutation()
	admitted := rec.GetAdmittedAt()
	if admitted == nil {
		s.log.WarnContext(ctx, "owed mutation has no admission time", slog.String("flowseer.device.id", deviceID), slog.Uint64("flowseer.device.sequence", owed.Sequence))
		return nil, false
	}
	horizon, err := s.cfg.Resolver.Horizon(ctx, deviceID)
	if err != nil {
		s.log.WarnContext(ctx, "cannot dispatch mutation without a horizon", slog.String("flowseer.device.id", deviceID), slog.Uint64("flowseer.device.sequence", owed.Sequence), slog.String("error.type", errorType(err)))
		return nil, false
	}
	exec := &integrationv1.ExecuteRequest{}
	exec.SetSequence(owed.Sequence)
	exec.SetDeadline(timestamppb.New(admitted.AsTime().Add(horizon)))
	exec.SetIdempotencyKey(m.GetIntent().GetIdempotencyKey())
	exec.SetMutation(m.GetIntent())
	if owed.Resume {
		exec.SetResume(true)
		exec.SetAdmittedAt(admitted)
	}
	return exec, true
}

// readExecute builds the ExecuteRequest for an owed read from its open entry.
func readExecute(rec *storev1.DeviceLaneRecord, sequence uint64) (*integrationv1.ExecuteRequest, bool) {
	entry, ok := findRead(rec, sequence)
	if !ok {
		return nil, false
	}
	exec := &integrationv1.ExecuteRequest{}
	exec.SetSequence(sequence)
	exec.SetDeadline(entry.GetDeadline())
	exec.SetIdempotencyKey(entry.GetIdempotencyKey())
	exec.SetRead(entry.GetRead())
	return exec, true
}

// findRead returns the open-read entry at a sequence, and the interface name
// it is keyed by, if one is open.
func findReadIface(rec *storev1.DeviceLaneRecord, sequence uint64) (*storev1.OpenRead, string, bool) {
	for iface, entry := range rec.GetOpenReads() {
		if entry.GetSequence() == sequence {
			return entry, iface, true
		}
	}
	return nil, "", false
}

func findRead(rec *storev1.DeviceLaneRecord, sequence uint64) (*storev1.OpenRead, bool) {
	entry, _, ok := findReadIface(rec, sequence)
	return entry, ok
}

func (s *Service) sweepError() *errsv1.ErrorPayload {
	if s.cfg.SweepError != nil {
		return s.cfg.SweepError()
	}
	return errs.Encode(errs.New().Code(ErrCodeReadDeadline).Msg("read deadline passed before an observation"))
}

// KeyLister lists the lane bucket's device keys; jetstream.KeyValue satisfies
// it. It is the sweeper's window onto every device, including one whose edge
// holds no open stream.
type KeyLister interface {
	Keys(ctx context.Context, opts ...jetstream.WatchOpt) ([]string, error)
}

// RunSweeper closes expired reads on every device on its own schedule, so a
// read whose edge holds no open stream is still closed when its deadline
// passes and its waiter is not left hanging (Subscribe sweeps the connected
// edges' devices on every pass; this covers the rest). It runs a goroutine
// until ctx is done and returns a channel closed when that goroutine exits, so
// the host — and a test — can join it rather than leak it.
func (s *Service) RunSweeper(ctx context.Context, bucket KeyLister) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweepAll(ctx, bucket)
			}
		}
	}()
	return done
}

func (s *Service) sweepAll(ctx context.Context, bucket KeyLister) {
	keys, err := bucket.Keys(ctx)
	if err != nil {
		if !errors.Is(err, jetstream.ErrNoKeysFound) {
			s.log.WarnContext(ctx, "sweeper could not list devices", slog.String("error.type", errorType(err)))
		}
		return // an empty bucket is not an error worth logging every tick
	}
	for _, deviceID := range keys {
		if _, err := s.cfg.Journal.SweepExpiredReads(ctx, deviceID, s.clock(), s.sweepError()); err != nil {
			s.log.WarnContext(ctx, "sweeper could not close expired reads", slog.String("flowseer.device.id", deviceID), slog.String("error.type", errorType(err)))
		}
	}
}
