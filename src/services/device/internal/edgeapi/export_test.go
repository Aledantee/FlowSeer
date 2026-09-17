package edgeapi

import (
	"context"
	"crypto/ed25519"
	"time"

	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
)

// ContextWithVerifiedAssertion is what [Middleware] hands the handlers, exposed
// to this package's tests so a handler can be exercised without standing up the
// HTTP stack the middleware lives in. Test-only: it is not part of the
// package's API.
func ContextWithVerifiedAssertion(ctx context.Context, assertion *edgev1.EdgeAssertion) context.Context {
	return context.WithValue(ctx, assertionKey{}, assertion)
}

// OpenSubmission runs the submission stream against sender. Test-only: the
// production path is [Service.OpenDeviceSubmission], whose Connect ServerStream
// has no exported constructor.
func (s *Service) OpenSubmission(ctx context.Context, msg *apiedgev1.OpenDeviceSubmissionRequest, sender submissionSender) error {
	return s.openSubmission(ctx, msg, sender)
}

// ConsumeSetupKey is the enrollment write the store runs under its
// compare-and-set. Test-only, so the record it is handed can be the one a
// concurrent operator write would have left rather than one a race has to
// produce.
func ConsumeSetupKey(current *storev1.StoredEdge, key, edgeID string, public ed25519.PublicKey, now time.Time) (*storev1.StoredEdge, error) {
	return consumeSetupKey(current, key, edgeID, public, now)
}
