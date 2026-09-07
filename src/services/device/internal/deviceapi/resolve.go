package deviceapi

import (
	"context"

	connect "connectrpc.com/connect"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

// AbandonMutation ends a mutation whose effect central cannot establish. It
// refuses only a mutation that is already terminal; every open phase abandons,
// including one the edge never admitted, because the case this call exists for
// is an edge that does not come back, and refusing by phase would leave those
// lanes held forever.
//
// The lane stays blocked afterwards. What the device now carries is unknown,
// and ResolveDesynchronization is where an operator says what to do about it.
func (s *Service) AbandonMutation(ctx context.Context, req *connect.Request[devicev1.AbandonMutationRequest]) (*connect.Response[devicev1.AbandonMutationResponse], error) {
	deviceID, _, err := s.device(req.Msg.GetDevice())
	if err != nil {
		return nil, connectErr(err)
	}
	state, err := s.cfg.Journal.Dispose(ctx, deviceID, req.Msg.GetSequence())
	if err != nil {
		return nil, connectErr(err)
	}

	resp := &devicev1.AbandonMutationResponse{}
	resp.SetMutation(state)
	return connect.NewResponse(resp), nil
}

// ResolveDesynchronization ends a hold and says what takes its place. The three
// arms differ only in what they admit: accept adopts what the device carries
// and admits nothing, restore admits central's own intent to put the expected
// description back, and replace admits the intent the caller carries. All of
// them, and the hold that releases the edge, land in one journal write.
func (s *Service) ResolveDesynchronization(ctx context.Context, req *connect.Request[devicev1.ResolveDesynchronizationRequest]) (*connect.Response[devicev1.ResolveDesynchronizationResponse], error) {
	deviceID, entry, err := s.device(req.Msg.GetDevice())
	if err != nil {
		return nil, connectErr(err)
	}
	record, err := s.cfg.Journal.Record(ctx, deviceID)
	if err != nil {
		return nil, connectErr(err)
	}

	resolution, err := s.resolution(req.Msg, record, entry, deviceID)
	if err != nil {
		return nil, connectErr(err)
	}
	_, admitted, err := s.cfg.Journal.ResolveDesynchronization(ctx, deviceID, resolution)
	if err != nil {
		return nil, connectErr(err)
	}

	resp := &devicev1.ResolveDesynchronizationResponse{}
	if admitted != nil {
		resp.SetMutation(admitted)
	}
	return connect.NewResponse(resp), nil
}

// resolution turns the operator's decision into the write the journal applies.
//
// Every arm needs the interface the held sequence was about, and the record is
// the only place that says so — it is on the held mutation's intent. A
// sequence whose mutation the record already closed carries no intent, which is
// the abandon-before-dispatch case: nothing reached the device, so there is no
// difference to accept and nothing to restore, and the resolution is the hold
// alone.
func (s *Service) resolution(
	msg *devicev1.ResolveDesynchronizationRequest,
	record *storev1.DeviceLaneRecord,
	entry *storev1.RegistryDevice,
	deviceID string,
) (journal.Resolution, error) {
	sequence := msg.GetSequence()
	resolution := journal.Resolution{Sequence: sequence}

	held := record.GetMutation()
	if held != nil && held.GetSequence() != sequence {
		held = nil
	}
	iface := changedInterface(held)

	switch {
	case msg.HasAccept():
		if iface == "" {
			return resolution, nil
		}
		observed, ok := record.GetLastObservations()[iface]
		if !ok {
			// Accepting means adopting what the device carries, and central
			// has never seen it. Refusing keeps the operator from recording an
			// expectation that stands for nothing.
			return resolution, errs.New().Code(ErrCodeNoExpectation).Attr("device", deviceID).
				Attr("interface", iface).Msg("no observation of this interface to accept")
		}
		resolution.Interface = iface
		resolution.Expected = observed.GetDescription()
	case msg.HasRestore():
		if iface == "" {
			return resolution, errs.New().Code(ErrCodeNoExpectation).Attr("device", deviceID).
				Attr("sequence", sequence).Msg("the sequence names no interface to restore")
		}
		expected, ok := record.GetExpectedDescriptions()[iface]
		if !ok {
			return resolution, errs.New().Code(ErrCodeNoExpectation).Attr("device", deviceID).
				Attr("interface", iface).Msg("central holds no expected description for this interface")
		}
		if record.GetFirmwareFingerprint() == "" {
			// Central admits this intent on its own behalf, so nobody else can
			// supply the epoch it is decided against, and an intent must name
			// one. A read reported from the device supplies it.
			return resolution, errs.New().Code(ErrCodeFirmwareEpoch).Attr("device", deviceID).
				Msg("central has not learned this device's firmware epoch, so it cannot admit an intent of its own")
		}
		resolution.Intent = reconciliationIntent(msg.GetDevice(), entry, record, iface, expected)
		resolution.Edge = edgeRef(s.cfg.Resolver.EdgeID())
	case msg.HasReplace():
		replace := msg.GetReplace()
		if err := pinnedPolicy(entry, replace.GetAccessPolicy(), deviceID); err != nil {
			return resolution, err
		}
		if err := currentEpoch(record, replace.GetExpectedFirmwareFingerprint(), deviceID); err != nil {
			return resolution, err
		}
		resolution.Intent = replace
		resolution.Edge = edgeRef(s.cfg.Resolver.EdgeID())
	default:
		return resolution, errs.New().Code(ErrCodeRequest).Attr("device", deviceID).
			Msg("the resolution names no decision")
	}

	return resolution, nil
}
