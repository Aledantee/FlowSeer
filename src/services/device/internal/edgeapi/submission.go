package edgeapi

import (
	"context"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes the submission stream returns.
var (
	// ErrCodeNotCheckpointed is a submission opened for a sequence the record
	// does not hold at POSSIBLY_APPLIED: not yet checkpointed, already
	// terminal, or not the mutation holding the lane.
	ErrCodeNotCheckpointed = errs.NewCode("edgeapi/not-checkpointed")
	// ErrCodeAuthorityWithdrawn ends a stream whose mutation stopped being one
	// the edge may submit. The edge has been sent a REVOKED pulse first.
	ErrCodeAuthorityWithdrawn = errs.NewCode("edgeapi/authority-withdrawn")
	// ErrCodeGrantExpired ends a stream that reached the deadline the grant
	// was issued under.
	ErrCodeGrantExpired = errs.NewCode("edgeapi/grant-expired")
	// ErrCodeAuthorityUnknown ends a stream central can no longer answer for,
	// because it cannot read the lane record. It is neither an authorization
	// nor a revocation, and it is retryable: the edge reopens.
	ErrCodeAuthorityUnknown = errs.NewCode("edgeapi/authority-unknown")
)

// defaultPulseInterval is how often a submission stream restates the edge's
// authority. It bounds how long a revocation takes to reach an edge, and the
// edge's rule is a positive pulse immediately before it submits, so the cadence
// has to be short enough that one is always recent.
const defaultPulseInterval = 5 * time.Second

// LaneRecords reads one device's lane record. The journal implements it.
type LaneRecords interface {
	Record(ctx context.Context, deviceID string) (*storev1.DeviceLaneRecord, error)
}

// submissionSender is the one thing the stream needs of Connect's ServerStream,
// which has no exported constructor, so the logic below is reachable without
// standing up the HTTP stack around it.
type submissionSender interface {
	Send(*apiedgev1.OpenDeviceSubmissionResponse) error
}

// OpenDeviceSubmission delivers the one-use submission credential for one
// mutation sequence and then streams the authority the edge checks before every
// command it sends.
//
// Authority covers both the mutation and the edge: every tick re-reads the lane
// record and the edge's lifecycle, so retiring an edge ends a write already in
// flight rather than only its next call.
//
// Authority is a value on the wire, never an inference from what has not
// arrived. Every pulse carries AUTHORIZED or REVOKED; silence is not a
// revocation, and the edge fails closed on it, so a central that went quiet
// instead of speaking would block every write rather than allow one. When
// authority ends, the edge is sent a REVOKED pulse and only then does the stream
// end, and it ends carrying a reason, because the edge treats a bare clean end
// as non-authorized and would otherwise learn nothing about why.
//
// The stream writes nothing to the lane record. It reads the record the
// checkpoint already wrote, so it can leave neither an obligation without a
// terminator nor a closed record with work still outstanding: the mutation's
// own terminators are exactly what they were before the stream opened.
//
// The grant is delivered once, as the first message, so it is never handed out
// twice on one stream. It is one-use per stream and not per sequence: a mutation
// that reached recovery and retries opens a second stream for the same sequence,
// which the gate admits because the record still holds that sequence at
// POSSIBLY_APPLIED.
//
// Two streams for one sequence therefore deliver the same credential twice, and
// that is not a secret issued twice. One-use is a property of how the edge uses
// the credential at the device; the material is the version the policy pins,
// read from the provider, not something central mints per grant and could burn.
// The two streams are one edge reconnecting, never two principals: the gate has
// already established that the caller is the edge that hosts the device. If
// central ever mints per-grant material, one-use becomes a binding central holds
// rather than a rule the edge follows, and it needs a durable marker on the lane
// record — nothing here would enforce it.
func (s *Service) OpenDeviceSubmission(ctx context.Context, req *connect.Request[apiedgev1.OpenDeviceSubmissionRequest], stream *connect.ServerStream[apiedgev1.OpenDeviceSubmissionResponse]) error {
	return s.openSubmission(ctx, req.Msg, stream)
}

func (s *Service) openSubmission(ctx context.Context, msg *apiedgev1.OpenDeviceSubmissionRequest, stream submissionSender) error {
	edgeID, err := EdgeIDFromContext(ctx)
	if err != nil {
		return unauthenticated(err)
	}
	deviceID := msg.GetDeviceId()
	sequence := msg.GetSequence()

	policy, err := s.resolveAccess(ctx, edgeID, deviceID, msg.GetBindingId())
	if err != nil {
		return connectErr(err)
	}
	horizon, err := s.registry.Horizon(ctx, deviceID)
	if err != nil {
		return connectErr(errs.From(err).Code(ErrCodePolicy).Attr("device", deviceID).
			Msg("device has no horizon to bound a submission by"))
	}

	record, err := s.lanes.Record(ctx, deviceID)
	if err != nil {
		return connectErr(errs.From(err).Code(ErrCodeAuthorityUnknown).Retryable().Attr("device", deviceID).
			Msg("read lane record"))
	}
	if err := checkpointed(record, sequence); err != nil {
		return connectErr(err)
	}

	deadline := record.GetAdmittedAt().AsTime().Add(horizon)
	if !s.clock().Before(deadline) {
		return connectErr(errs.New().Code(ErrCodeGrantExpired).Attr("device", deviceID).Attr("sequence", sequence).
			Msg("the mutation's horizon has passed"))
	}

	handle := policy.GetSubmissionCredential()
	material, err := s.creds.Get(handle.GetKey(), handle.GetVersion())
	if err != nil {
		return connectErr(errs.From(err).Code(ErrCodeCredential).Attr("device", deviceID).
			Attr("credential_key", handle.GetKey()).Msg("read submission credential material"))
	}

	grant := apiedgev1.SubmissionGrant_builder{
		Credential: apiedgev1.DeviceCredential_builder{
			Credential:    handle,
			TypedMaterial: material,
		}.Build(),
		HostTrust: policy.GetHostTrust(),
		Deadline:  timestamppb.New(deadline),
	}
	if material.HasShell() {
		grant.SshHostKeySha256 = proto.String(policy.GetSshHostKeySha256())
	}
	if err := stream.Send(apiedgev1.OpenDeviceSubmissionResponse_builder{Grant: grant.Build()}.Build()); err != nil {
		return err
	}

	return s.pulse(ctx, edgeID, deviceID, sequence, deadline, stream)
}

// pulse restates the edge's authority until it ends, and says why it ended.
func (s *Service) pulse(ctx context.Context, edgeID, deviceID string, sequence uint64, deadline time.Time, stream submissionSender) error {
	ticker := time.NewTicker(s.pulseInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		if !s.clock().Before(deadline) {
			return connectErr(errs.New().Code(ErrCodeGrantExpired).Attr("device", deviceID).Attr("sequence", sequence).
				Msg("the submission deadline has passed"))
		}

		record, err := s.lanes.Record(ctx, deviceID)
		if err != nil {
			// Central cannot say whether the edge still holds authority, so it
			// says neither. Asserting AUTHORIZED here would authorize a write
			// against a record nobody read, and a REVOKED pulse would end a
			// live mutation over a transient bucket failure; ending with a
			// retryable reason lets the edge fail closed and reopen.
			return connectErr(errs.From(err).Code(ErrCodeAuthorityUnknown).Retryable().Attr("device", deviceID).
				Msg("read lane record"))
		}
		// The edge's standing is read every tick, not only at open. The
		// assertion verifier refuses a retired edge's next call, but this
		// stream was authorized when it opened and makes no further calls, so
		// without this central would go on stating AUTHORIZED to an edge an
		// operator has just retired, for as long as the horizon runs. Retiring
		// an edge is what ends its standing outright, and that has to be true
		// of a write already in flight.
		_, lifecycle, err := s.store.Lookup(ctx, edgeID)
		if err != nil {
			return connectErr(errs.From(err).Code(ErrCodeAuthorityUnknown).Retryable().Attr("edge", edgeID).
				Msg("read edge record"))
		}
		if lifecycle != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED {
			if err := s.send(stream, apiedgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_REVOKED, deadline); err != nil {
				return err
			}
			return connectErr(errs.New().Code(ErrCodeAuthorityWithdrawn).Attr("edge", edgeID).
				Attr("lifecycle", lifecycle.String()).Msg("edge is no longer enrolled"))
		}
		if withdrawn := checkpointed(record, sequence); withdrawn != nil {
			if err := s.send(stream, apiedgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_REVOKED, deadline); err != nil {
				return err
			}
			return connectErr(errs.From(withdrawn).Code(ErrCodeAuthorityWithdrawn).Attr("device", deviceID).
				Attr("sequence", sequence).Msg("submission authority withdrawn"))
		}

		if err := s.send(stream, apiedgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED, deadline); err != nil {
			return err
		}
	}
}

func (s *Service) send(stream submissionSender, authority apiedgev1.SubmissionAuthority, deadline time.Time) error {
	return stream.Send(apiedgev1.OpenDeviceSubmissionResponse_builder{
		Pulse: apiedgev1.AuthorityPulse_builder{
			Authority: &authority,
			Deadline:  timestamppb.New(deadline),
		}.Build(),
	}.Build())
}

func (s *Service) pulseInterval() time.Duration {
	if s.cfg.PulseInterval > 0 {
		return s.cfg.PulseInterval
	}
	return defaultPulseInterval
}

// checkpointed reports whether the record still holds sequence as the mutation
// the edge may submit: the one holding the lane, at POSSIBLY_APPLIED.
//
// The phase is matched exactly rather than "at least". ADMITTED means central
// has not durably recorded that the command may reach the device, so a grant
// would hand out a credential ahead of the record that makes the effect
// attributable. Every later phase means the mutation is being observed, is
// already decided, or was abandoned, and a fresh credential would let the edge
// act on a decision central has moved past. A mutation in recovery still reads
// POSSIBLY_APPLIED here, because a RECOVERING report moves the last reported
// phase and not the mutation's own, which is what lets its retry open a second
// grant for the same sequence.
//
// A terminal disposition refuses whatever the phase says. The schema forbids one
// alongside POSSIBLY_APPLIED, but the journal marshals its records without
// validating them, so that rule holds only as long as every journal arm
// remembers to move the phase with the disposition. A gate on a credential does
// not rest on that.
func checkpointed(record *storev1.DeviceLaneRecord, sequence uint64) error {
	mutation := record.GetMutation()
	if mutation == nil {
		return errs.New().Code(ErrCodeNotCheckpointed).Attr("sequence", sequence).Msg("no mutation holds the lane")
	}
	if mutation.GetSequence() != sequence {
		return errs.New().Code(ErrCodeNotCheckpointed).Attr("sequence", sequence).
			Attr("open_sequence", mutation.GetSequence()).Msg("another mutation holds the lane")
	}
	if mutation.GetPhase() != accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED || mutation.HasDisposition() {
		return errs.New().Code(ErrCodeNotCheckpointed).Attr("sequence", sequence).
			Attr("phase", mutation.GetPhase().String()).Msg("mutation is not checkpointed for submission")
	}
	return nil
}
