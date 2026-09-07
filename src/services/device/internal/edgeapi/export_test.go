package edgeapi

import (
	"context"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
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
func (s *Service) OpenSubmission(ctx context.Context, msg *edgev1.OpenDeviceSubmissionRequest, sender submissionSender) error {
	return s.openSubmission(ctx, msg, sender)
}
