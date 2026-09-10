package access_test

import (
	"context"
	"errors"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
)

// submitFailing registers a device whose write always fails with err, and
// whose read never shows the change.
func submitFailing(t *testing.T, l *access.Lane, err error) {
	t.Helper()
	if addErr := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: 12 * time.Second},
		OpenSNMP:      probeFactory(),
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("as found"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error { return err },
	}); addErr != nil {
		t.Fatalf("AddDevice() error: %v", addErr)
	}
}

// A submit failure the adapter cannot prove anything about leaves the effect
// unknown, and this is the property that must not erode.
//
// It is written first and deliberately: the interesting case is the one below,
// where an adapter proves nothing was sent and the mutation is rejected — but
// that case is only safe while this one is the default. An adapter that grows
// a new refusal path and forgets to mark it must land here, on "the effect is
// unknown", which is wrong in the direction that costs an operator a
// resolution rather than in the direction that tells them a switch was
// untouched when it may not have been.
//
// Asserted through what the edge reports, because `submitted` is central's
// input for the whole disposition: false disposes the mutation rejected, true
// leaves it indeterminate.
func TestASubmitFailureThatProvesNothingLeavesTheEffectUnknown(t *testing.T) {
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, noopDeliverer{})
	submitFailing(t, l, errors.New("the session went away mid-command"))

	_, _ = runMutation(t, l, mutationRequest(1))

	if claimedNothingWasSent(reporter) {
		t.Error("an unmarked submit failure told central nothing was sent; it would be disposed rejected for a device that may have been changed")
	}
}

// A submit failure the adapter can prove sent nothing reports exactly that,
// and central disposes the mutation rejected rather than indeterminate.
//
// This is the likeliest failure of a first live write: managed_interfaces is
// asserted and never checked against the device, so a wrong or mistyped
// interface name is the most probable thing to go wrong. The FastIron adapter
// refuses it at the interface select, before the command that changes a
// description is constructed — so nothing reached the device and the adapter
// knows it. Reporting that as an unknown effect sends an operator to resolve
// an ambiguity that does not exist, on a device nothing happened to.
func TestASubmitFailureThatProvesNothingWasSentSaysSo(t *testing.T) {
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, noopDeliverer{})
	submitFailing(t, l, errs.New().Code(interfaces.ErrCodeNotSubmitted).
		Msg("device did not accept interface \"ethernet 9/9/9\""))

	_, _ = runMutation(t, l, mutationRequest(1))

	if !claimedNothingWasSent(reporter) {
		t.Error("a proven non-submission never told central so; the lane would be left indeterminate for a device it was never asked to change")
	}
}

type refusingSubmissionCredentials struct {
	err error
}

func (s refusingSubmissionCredentials) Open(context.Context, string, string, uint64) (access.SubmissionHandle, error) {
	return nil, s.err
}

func TestAPreSubmissionFailureCrossesTheProcessBoundaryWithACode(t *testing.T) {
	refusal := errors.New("submission credential service unavailable")
	l := laneWithReporter(t, &recordingReporter{}, noopDeliverer{}, refusingSubmissionCredentials{err: refusal})
	submitFailing(t, l, nil)

	_, err := runMutation(t, l, mutationRequest(1))
	if !errors.Is(err, refusal) {
		t.Errorf("Submit() error = %v, want it to wrap %v", err, refusal)
	}
	if code, _ := errs.CodeOf(err); code != access.ErrCodeNotSubmitted {
		t.Errorf("Submit() code = %v, want %v: the process boundary cannot carry an uncoded refusal", code, access.ErrCodeNotSubmitted)
	}
}

// claimedNothingWasSent reports whether the lane ever told central, on an
// error result, that the command was not submitted.
//
// That flag is central's whole input for the disposition: an error report
// carrying submitted false is disposed rejected, and anything else leaves the
// mutation's effect unestablished. So this is the question both tests are
// really asking, and asking it this way rather than about a particular report
// is what makes the two cases comparable — they do not produce the same
// reports at all. A proven non-submission fails its caller outright; an
// unproven failure enters recovery and reports progress instead.
func claimedNothingWasSent(reporter *recordingReporter) bool {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()

	for _, result := range reporter.results {
		if result.HasError() && !result.GetSubmitted() {
			return true
		}
	}
	return false
}

// TestAProvenNonSubmissionLeavesTheLaneOpen is the observable that tells a
// wedged lane from a healthy one, and the only one there is.
//
// A step that provably sent nothing is reported with submitted false, and
// central disposes such a mutation REJECTED — a record that owes no
// HoldResolved. A hold engaged here would therefore be one central never
// learns of and never resolves, refusing every later mutation on the device
// with a code central reads as retryable: a dispatch loop against a device
// only an operator can free.
func TestAProvenNonSubmissionLeavesTheLaneOpen(t *testing.T) {
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, noopDeliverer{})
	submitFailing(t, l, errs.New().Code(interfaces.ErrCodeNotSubmitted).
		Msg("the device rejected the interface name"))

	_, _ = runMutation(t, l, mutationRequest(1))
	if !claimedNothingWasSent(reporter) {
		t.Fatal("the report did not claim the command was never sent")
	}

	// The device's lane must still admit work.
	_, err := runMutation(t, l, mutationRequest(2))
	if code, ok := errs.CodeOf(err); ok && code == access.ErrCodeDesynchronized {
		t.Fatal("a mutation that provably sent nothing left the device's lane held")
	}
}
