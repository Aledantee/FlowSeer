package deviceapi

import (
	"context"
	"time"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// ReadInterface opens a read on the device's lane and waits for the answer.
//
// The read is a row in the record like any other, so the edge that answers it
// is the one holding the dispatch stream, and the report that closes it may
// land on a different central replica than this call. That is why the waiter
// watches the record rather than its own memory. When the caller's deadline
// passes first the read stays open and its answer still lands in the record;
// only this call gives up.
func (s *Service) ReadInterface(ctx context.Context, req *connect.Request[devicev1.ReadInterfaceRequest]) (*connect.Response[devicev1.ReadInterfaceResponse], error) {
	deviceID, entry, err := s.device(req.Msg.GetDevice())
	if err != nil {
		return nil, connectErr(err)
	}
	iface := req.Msg.GetInterfaceName()

	// The record entry's own deadline, never the caller's. The two answer
	// different questions: the caller's says how long this call will wait,
	// the record's says how long the read stays owed. Taking the caller's
	// makes the read expire at exactly the moment the caller gives up —
	// OwedRows stops offering it, the sweeper closes it with a deadline
	// error, and it was never dispatched — while this handler tells the
	// caller it is still running.
	deadline := s.clock().Add(s.defaultReadDeadline())

	read := &accessv1.TypedRead{}
	read.SetAccessPolicy(entry.GetConfig().GetAccessPolicy())
	intent := &accessv1.InterfaceReadIntent{}
	intent.SetInterfaceName(iface)
	read.SetInterface(intent)

	sequence, err := s.cfg.Journal.OpenRead(ctx, deviceID, req.Msg.GetDevice(), iface, read, uuid.NewString(), deadline)
	if err != nil {
		return nil, connectErr(err)
	}

	observation, err := s.awaitRead(ctx, deviceID, iface, sequence)
	if err != nil {
		return nil, connectErr(err)
	}

	resp := &devicev1.ReadInterfaceResponse{}
	resp.SetInterface(observation)
	return connect.NewResponse(resp), nil
}

// defaultReadDeadline bounds a read whose caller set no deadline, so an open
// read is never owed forever by a caller that has gone away.
func (s *Service) defaultReadDeadline() time.Duration { return 30 * time.Second }

// awaitRead blocks until the record says the read closed, the caller's context
// ends, or the read's own entry is replaced by a later one.
func (s *Service) awaitRead(ctx context.Context, deviceID, iface string, sequence uint64) (*accessv1.InterfaceObservation, error) {
	var (
		changed <-chan struct{}
		stop    = func() {}
	)
	if s.cfg.Watcher != nil {
		var err error
		changed, stop, err = s.cfg.Watcher.Watch(ctx, deviceID)
		if err != nil {
			return nil, err
		}
	}
	defer stop()

	ticker := time.NewTicker(s.readPoll)
	defer ticker.Stop()

	timeout := func() error {
		return errs.From(ctx.Err()).Code(ErrCodeReadTimeout).Attr("device", deviceID).
			Attr("interface", iface).Attr("sequence", sequence).
			Msg("the read did not answer before the caller's deadline")
	}

	for {
		// Read once before waiting: the answer may already be in the record,
		// and a watch started after the write would never see its change.
		record, err := s.cfg.Journal.Record(ctx, deviceID)
		if err != nil {
			if ctx.Err() != nil {
				// The record read failed because this call's own deadline
				// passed, not because the store is unreachable. Reporting the
				// store would send an operator looking at central.
				return nil, timeout()
			}
			return nil, err
		}
		obs, done, err := readOutcome(record, iface, sequence, deviceID)
		if done {
			return obs, err
		}

		select {
		case <-ctx.Done():
			return nil, timeout()
		case <-ticker.C:
		case <-changed:
		}
	}
}

// readOutcome reports the read's answer if it has one. done says
// whether the wait is over, which is not the same as having an observation: a
// read whose entry the record no longer holds at this sequence has been
// answered and swept, or replaced, and waiting longer would never end.
func readOutcome(record *storev1.DeviceLaneRecord, iface string, sequence uint64, deviceID string) (obs *accessv1.InterfaceObservation, done bool, err error) {
	entry, ok := record.GetOpenReads()[iface]
	if !ok || entry.GetSequence() != sequence {
		return nil, true, errs.New().Code(ErrCodeReadFailed).Attr("device", deviceID).
			Attr("interface", iface).Attr("sequence", sequence).
			Msg("the read's entry left the record before this call read its outcome")
	}
	switch {
	case entry.HasObservation():
		return entry.GetObservation(), true, nil
	case entry.HasError():
		return nil, true, errs.From(errs.Decode(entry.GetError())).Code(ErrCodeReadFailed).
			Attr("device", deviceID).Attr("interface", iface).Msg("the edge could not read this interface")
	default:
		return nil, false, nil
	}
}
