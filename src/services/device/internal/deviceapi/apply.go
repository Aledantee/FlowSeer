package deviceapi

import (
	"context"

	connect "connectrpc.com/connect"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
)

// ApplyInterfaceDescription records the operator's intent and admits it to the
// device's lane, returning at admission. Everything that could refuse the
// intent is checked before the admission, so a refused call leaves the record
// as it found it: the device is listed, its horizon is measured, the policy
// version is the one it pins, the firmware epoch is the one the caller decided
// against, and the lane is free.
//
// With validate_only the same checks run and nothing is written. The lane
// check is the one that can go stale between the answer and a later apply,
// which is what makes validate_only advice rather than a reservation.
func (s *Service) ApplyInterfaceDescription(ctx context.Context, req *connect.Request[devicev1.ApplyInterfaceDescriptionRequest]) (*connect.Response[devicev1.ApplyInterfaceDescriptionResponse], error) {
	principal, ok := authn.FromContext(ctx)
	if !ok {
		return nil, connectErr(errs.New().Code(ErrCodeUnauthenticated).Msg("no authenticated principal in context"))
	}
	intent := req.Msg.GetIntent()
	if intent != nil {
		actor := &accessv1.Actor{}
		op := &identityv1.OperatorRef{}
		op.SetIssuer(principal.Issuer)
		op.SetSubject(principal.Subject)
		actor.SetOperator(op)
		intent.SetActor(actor)
	}
	deviceID, laneTenant, entry, err := s.device(ctx, intent.GetDevice())
	if err != nil {
		return nil, connectErr(err)
	}

	// An unmeasured horizon means nothing can bound how long the change may
	// take to become visible, so the mutation could never be dispatched.
	if _, err := s.cfg.Resolver.Horizon(ctx, deviceID); err != nil {
		return nil, connectErr(err)
	}
	if err := pinnedPolicy(entry, intent.GetAccessPolicy(), deviceID); err != nil {
		return nil, connectErr(err)
	}

	record, err := s.cfg.Journal.Record(ctx, laneTenant, deviceID)
	if err != nil {
		return nil, connectErr(err)
	}
	if err := currentEpoch(record, intent.GetExpectedFirmwareFingerprint(), deviceID); err != nil {
		return nil, connectErr(err)
	}
	resp := &devicev1.ApplyInterfaceDescriptionResponse{}
	if req.Msg.GetValidateOnly() {
		// The journal decides this for a real apply, and its answer is the one
		// that counts; here there is nothing to ask, so the check is repeated
		// against the record just read. Its answer can be stale by the time
		// anyone acts on it, which is what makes validate_only advice.
		if m := record.GetMutation(); m != nil && !recordedKey(record, intent.GetIdempotencyKey()) {
			return nil, connectErr(errs.New().Code(ErrCodeLaneHeld).Attr("device", deviceID).
				Attr("holding_sequence", m.GetSequence()).Msg("a mutation still holds this device's lane"))
		}
		return connect.NewResponse(resp), nil
	}

	state, err := s.cfg.Journal.Admit(ctx, laneTenant, deviceID, intent, edgeRef(s.cfg.Resolver.EdgeID()))
	if err != nil {
		return nil, connectErr(err)
	}
	resp.SetMutation(state)
	return connect.NewResponse(resp), nil
}
