package access_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/epoch"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/lane"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

type noopDeliverer struct{}

func (noopDeliverer) Emit(context.Context, *eventv1.DeviceOperationEvent) error { return nil }

// failsOnDriftDeliverer fails only for a DriftDetected event, so the
// onboarding and mutation machinery's own audit calls succeed and only the
// drift check itself sees the simulated outage.
type failsOnDriftDeliverer struct{}

func (failsOnDriftDeliverer) Emit(_ context.Context, event *eventv1.DeviceOperationEvent) error {
	if event.GetDriftDetected() != nil {
		return errors.New("audit delivery unavailable")
	}
	return nil
}

// spyDeliverer records every event handed to it, guarded by mu since
// EvaluateDrift and Submit may call it from different goroutines in a test.
type spyDeliverer struct {
	mu     sync.Mutex
	events []*eventv1.DeviceOperationEvent
}

func (d *spyDeliverer) Emit(_ context.Context, event *eventv1.DeviceOperationEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, event)
	return nil
}

func (d *spyDeliverer) recorded() []*eventv1.DeviceOperationEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]*eventv1.DeviceOperationEvent(nil), d.events...)
}

func newTestLane(t *testing.T) *access.Lane {
	t.Helper()
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	return access.NewLane(access.Config{
		QueueCapacity: 4,
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		Audit:         noopDeliverer{},
		Telemetry:     view,
		Clock:         time.Now,
	})
}

func completeObservation(description string) *accessv1.InterfaceObservation {
	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName("ethernet 1/1/1")
	obs.SetDescription(description)
	obs.SetAdminStatus(interfacev1.AdminStatus_ADMIN_STATUS_UP)
	obs.SetOperStatus(interfacev1.OperStatus_OPER_STATUS_UP)
	obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)
	return obs
}

// mutationRequest always expects fingerprint "fw-A", matching every
// newTestLane device's FingerprintOverride.
func mutationRequest(sequence uint64, description string) *integrationv1.ExecuteRequest {
	return mutationRequestWithFingerprint(sequence, "fw-A", description)
}

func mutationRequestWithFingerprint(sequence uint64, fingerprint, description string) *integrationv1.ExecuteRequest {
	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName("ethernet 1/1/1")
	change.SetDescription(description)
	intent := &accessv1.MutationIntent{}
	intent.SetExpectedFirmwareFingerprint(fingerprint)
	intent.SetInterfaceDescription(change)
	req := &integrationv1.ExecuteRequest{}
	req.SetSequence(sequence)
	req.SetMutation(intent)
	return req
}

func readRequest() *integrationv1.ExecuteRequest {
	readIntent := &accessv1.InterfaceReadIntent{}
	readIntent.SetInterfaceName("ethernet 1/1/1")
	typedRead := &accessv1.TypedRead{}
	typedRead.SetInterface(readIntent)
	req := &integrationv1.ExecuteRequest{}
	req.SetRead(typedRead)
	return req
}

// addDevice registers "dev-1" whose observation always reads back
// "uplink to core", with no SNMP or SSH fake needed.
func addDevice(t *testing.T, l *access.Lane) {
	t.Helper()
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		FingerprintOverride: "fw-A",
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil },
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}
}

// runMutation drives one mutation admission for "dev-1" through checkpoint
// and terminal ack concurrently with Submit, since both block on external
// delivery.
func runMutation(t *testing.T, l *access.Lane, req *integrationv1.ExecuteRequest) (*integrationv1.ExecuteResult, error) {
	t.Helper()
	const deviceKey = "dev-1"
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			checkpointReq := &integrationv1.CheckpointRequest{}
			checkpointReq.SetSequence(req.GetSequence())
			if err := l.HandleCheckpoint(deviceKey, checkpointReq); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			ack := &integrationv1.TerminalResultAck{}
			ack.SetSequence(req.GetSequence())
			ack.SetDisposition(accessv1.Disposition_DISPOSITION_VERIFIED)
			if err := l.HandleTerminalAck(deviceKey, ack); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}()

	result, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: deviceKey,
		Request:   req,
		Priority:  lane.PriorityNormal,
	})
	wg.Wait()
	return result, err
}

func TestLaneMutationHappyPath(t *testing.T) {
	l := newTestLane(t)
	addDevice(t, l)

	req := mutationRequest(1, "uplink to core")
	result, err := runMutation(t, l, req)
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Errorf("PhaseReached = %v, want RELEASED", result.GetPhaseReached())
	}
}

func TestLaneReadHappyPath(t *testing.T) {
	l := newTestLane(t)
	addDevice(t, l)

	result, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   readRequest(),
		Priority:  lane.PriorityLow,
	})
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if result.GetObservation().GetDescription() != "uplink to core" {
		t.Errorf("observation description = %q, want %q", result.GetObservation().GetDescription(), "uplink to core")
	}
}

// TestCoalescedReadKeepsCallersLongerDeadlineInsteadOfOperationTimeout
// proves a caller's own, longer deadline governs the detached read rather
// than being clipped to Config.OperationTimeout: OperationTimeout is only
// a fallback for a caller with no deadline at all, not a ceiling on one
// that already has a longer one of its own.
func TestCoalescedReadKeepsCallersLongerDeadlineInsteadOfOperationTimeout(t *testing.T) {
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	const shortOperationTimeout = 50 * time.Millisecond
	const slowRead = 200 * time.Millisecond // longer than shortOperationTimeout
	l := access.NewLane(access.Config{
		QueueCapacity:    4,
		DelayedEffect:    interfaces.DelayedEffect{Horizon: time.Minute},
		Audit:            noopDeliverer{},
		Telemetry:        view,
		Clock:            time.Now,
		OperationTimeout: shortOperationTimeout,
	})
	err = l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		FingerprintOverride: "fw-A",
		ReadOverride: func(ctx context.Context, _ string) (*accessv1.InterfaceObservation, error) {
			select {
			case <-time.After(slowRead):
				return completeObservation("uplink to core"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	longCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := l.Submit(longCtx, access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   readRequest(),
		Priority:  lane.PriorityLow,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v, want the read to survive past the %v OperationTimeout under the caller's own longer deadline", err, shortOperationTimeout)
	}
	if result.GetObservation().GetDescription() != "uplink to core" {
		t.Errorf("observation description = %q, want %q", result.GetObservation().GetDescription(), "uplink to core")
	}
}

func TestLaneOverloadRejectsWithoutDroppingExisting(t *testing.T) {
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	l := access.NewLane(access.Config{
		QueueCapacity: 1,
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		Audit:         noopDeliverer{},
		Telemetry:     view,
		Clock:         time.Now,
	})

	// A read closure that blocks until released lets this test hold one
	// item "in flight" (dequeued but not yet resolved) so a capacity-1
	// queue is genuinely full for the overload check.
	release := make(chan struct{})
	err = l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		FingerprintOverride: "fw-A",
		ReadOverride: func(_ context.Context, _ string) (*accessv1.InterfaceObservation, error) {
			<-release
			return completeObservation("x"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		req := &integrationv1.ExecuteRequest{}
		readIntent := &accessv1.InterfaceReadIntent{}
		readIntent.SetInterfaceName("eth-first")
		typedRead := &accessv1.TypedRead{}
		typedRead.SetInterface(readIntent)
		req.SetRead(typedRead)
		_, _ = l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: req, Priority: lane.PriorityLow})
	}()

	// Give the first Submit time to become the active drainer and block
	// inside ReadOverride.
	time.Sleep(50 * time.Millisecond)

	secondReq := &integrationv1.ExecuteRequest{}
	readIntent2 := &accessv1.InterfaceReadIntent{}
	readIntent2.SetInterfaceName("eth-second")
	typedRead2 := &accessv1.TypedRead{}
	typedRead2.SetInterface(readIntent2)
	secondReq.SetRead(typedRead2)

	// The second item is admitted (capacity 1, queue currently empty since
	// the first was dequeued into processing) but a third must overload.
	// Its own context has no deadline: it must complete once release
	// closes, proving requirement 2's "every previously admitted item is
	// still present and later executed" rather than merely not erroring.
	secondDone := make(chan submitResult, 1)
	go func() {
		result, err := l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: secondReq, Priority: lane.PriorityLow})
		secondDone <- submitResult{result: result, err: err}
	}()
	time.Sleep(50 * time.Millisecond)

	thirdReq := &integrationv1.ExecuteRequest{}
	readIntent3 := &accessv1.InterfaceReadIntent{}
	readIntent3.SetInterfaceName("eth-third")
	typedRead3 := &accessv1.TypedRead{}
	typedRead3.SetInterface(readIntent3)
	thirdReq.SetRead(typedRead3)

	_, err = l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: thirdReq, Priority: lane.PriorityLow})
	if err == nil {
		t.Fatal("Submit() error = nil, want an overload error for the third concurrent admission")
	}
	if code, ok := errs.CodeOf(err); !ok || code != lane.ErrCodeOverload {
		t.Errorf("Submit() code = %v, want %v", code, lane.ErrCodeOverload)
	}

	close(release)
	<-firstDone

	select {
	case r := <-secondDone:
		if r.err != nil {
			t.Fatalf("second Submit() error = %v, want nil — the previously admitted item must still execute", r.err)
		}
		if got := r.result.GetObservation().GetDescription(); got != "x" {
			t.Errorf("second Submit() observation description = %q, want %q", got, "x")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the previously admitted second item never completed after release")
	}
}

type submitResult struct {
	result *integrationv1.ExecuteResult
	err    error
}

func TestLanePollCoalescing(t *testing.T) {
	l := newTestLane(t)

	var reads int
	var mu sync.Mutex
	release := make(chan struct{})
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		FingerprintOverride: "fw-A",
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			mu.Lock()
			reads++
			mu.Unlock()
			<-release
			return completeObservation("uplink to core"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]*integrationv1.ExecuteResult, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := l.Submit(context.Background(), access.SubmitOptions{
				DeviceKey: "dev-1",
				Request:   readRequest(),
				Priority:  lane.PriorityLow,
			})
			if err != nil {
				t.Errorf("Submit() error: %v", err)
				return
			}
			results[i] = result
		}(i)
	}

	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	got := reads
	mu.Unlock()
	if got != 1 {
		t.Errorf("read closure called %d times, want exactly 1 (coalesced)", got)
	}
	if results[0].GetObservation().GetDescription() != results[1].GetObservation().GetDescription() {
		t.Error("coalesced callers received different observations")
	}
}

// TestLaneTwoIdenticalMutationsNeverCoalesce proves the actual invariant
// the plan names ("a MutationIntent never coalesces even with an identical
// interface target") where it actually lives — Lane.Submit only builds a
// coalescing key for a TypedRead — rather than at the Coalescer's own
// struct-equality level, which internal/lane/coalesce_test.go already
// covers but cannot speak to Lane's behavior.
func TestLaneTwoIdenticalMutationsNeverCoalesce(t *testing.T) {
	l := newTestLane(t)

	var submits int
	var mu sync.Mutex
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		FingerprintOverride: "fw-A",
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error {
			mu.Lock()
			submits++
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	if _, err := runMutation(t, l, mutationRequest(1, "uplink to core")); err != nil {
		t.Fatalf("first Submit() error: %v", err)
	}
	if _, err := runMutation(t, l, mutationRequest(2, "uplink to core")); err != nil {
		t.Fatalf("second Submit() error: %v", err)
	}

	mu.Lock()
	got := submits
	mu.Unlock()
	if got != 2 {
		t.Errorf("Submit closure called %d times, want 2 — two identical mutations must never coalesce into one device round-trip", got)
	}
}

// TestLaneOnboardingProbedFingerprintGatesTheFirstMutation proves the
// identity probe's result is not merely run, but is actually what the
// first mutation's fingerprint gate checks against: a mutation whose
// expected fingerprint matches what the probe (via fakeIdentitySession,
// which always answers "fw-A") returned is admitted, and one that names a
// different fingerprint is blocked — the ordering requirement 16 asks for
// stated as an observable effect rather than a bare "was it called" flag.
func TestLaneOnboardingProbedFingerprintGatesTheFirstMutation(t *testing.T) {
	l := newTestLane(t)

	fakeSess := fakeIdentitySession{onGet: func() {}}
	// The exact fingerprint format is epoch.Probe's own concern (and is
	// deliberately hashed there to stay within schema bounds — see
	// TestProbeFingerprintNeverExceedsSchemaBound); this test only needs
	// to know what the fake session's answer maps to, so it asks Probe
	// directly rather than hardcoding the format.
	wantFingerprint, err := epoch.Probe(context.Background(), fakeSess)
	if err != nil {
		t.Fatalf("epoch.Probe() error: %v", err)
	}

	var probed bool
	err = l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		Sess: fakeIdentitySession{onGet: func() { probed = true }},
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil },
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}
	if !probed {
		t.Fatal("AddDevice did not run the identity probe")
	}

	// A mutation naming a different expected fingerprint must be blocked
	// before any device contact.
	if _, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   mutationRequestWithFingerprint(1, "some-other-firmware", "x"),
		Priority:  lane.PriorityNormal,
	}); err == nil {
		t.Fatal("Submit() with a mismatched fingerprint error = nil, want the firmware-epoch gate to block it")
	}

	// The matching fingerprint (what the probe actually returned) must be
	// admitted and reach a terminal result.
	if _, err := runMutation(t, l, mutationRequestWithFingerprint(2, wantFingerprint, "uplink to core")); err != nil {
		t.Fatalf("Submit() with the probed fingerprint error = %v, want nil", err)
	}
}

func TestLaneManagementModeDrift(t *testing.T) {
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	l := access.NewLane(access.Config{
		QueueCapacity:  4,
		DelayedEffect:  interfaces.DelayedEffect{Horizon: time.Minute},
		Audit:          noopDeliverer{},
		Telemetry:      view,
		Clock:          time.Now,
		ManagementMode: inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED,
	})
	addDevice(t, l)

	req := mutationRequest(1, "uplink to core")
	if _, err := runMutation(t, l, req); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	drifted := completeObservation("manual edit")
	outcome, err := l.EvaluateDrift(context.Background(), "dev-1", drifted, false)
	if err != nil {
		t.Fatalf("EvaluateDrift() error: %v", err)
	}
	if !outcome.Drifted {
		t.Fatal("EvaluateDrift() reported no drift after an unexplained change")
	}
	if !outcome.Blocked {
		t.Error("default ManagementMode should behave as OPERATOR_MANAGED (blocked)")
	}

	blockedCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := l.Submit(blockedCtx, access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   mutationRequest(2, "another change"),
		Priority:  lane.PriorityNormal,
	}); err == nil {
		t.Fatal("Submit() error = nil, want the hold to block a new mutation")
	}

	if err := l.ResolveHold("dev-1"); err != nil {
		t.Fatalf("ResolveHold() error: %v", err)
	}
}

func TestEvaluateDriftReportsSuppressedWhenAlreadyHeld(t *testing.T) {
	l := newTestLane(t)
	addDevice(t, l)

	req := mutationRequest(1, "uplink to core")
	if _, err := runMutation(t, l, req); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	drifted := completeObservation("manual edit")
	outcome, err := l.EvaluateDrift(context.Background(), "dev-1", drifted, false)
	if err != nil {
		t.Fatalf("EvaluateDrift() error: %v", err)
	}
	if !outcome.Blocked {
		t.Fatal("expected the first drift evaluation to block and engage the hold")
	}

	// A second evaluation while the hold from the first is still active
	// must report that it was not evaluated at all, not a bare zero value
	// indistinguishable from "evaluated, clean" — a periodic drift poll
	// must be able to tell the two apart.
	outcome, err = l.EvaluateDrift(context.Background(), "dev-1", drifted, false)
	if err != nil {
		t.Fatalf("EvaluateDrift() error: %v", err)
	}
	if !outcome.Suppressed {
		t.Error("expected Suppressed = true for a device already under a hold")
	}
	if outcome.Drifted {
		t.Error("expected Drifted = false alongside Suppressed, not a real evaluation result")
	}

	if err := l.ResolveHold("dev-1"); err != nil {
		t.Fatalf("ResolveHold() error: %v", err)
	}
}

func TestEvaluateDriftEngagesHoldEvenWhenAuditDeliveryFails(t *testing.T) {
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	// failsOnDriftDeliverer only fails a DriftDetected event, so
	// onboarding and the mutation this test drives through first succeed
	// normally, and only the drift check itself sees the simulated
	// outage.
	l := access.NewLane(access.Config{
		QueueCapacity:  4,
		DelayedEffect:  interfaces.DelayedEffect{Horizon: time.Minute},
		Audit:          failsOnDriftDeliverer{},
		Telemetry:      view,
		Clock:          time.Now,
		ManagementMode: inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED,
	})
	addDevice(t, l)
	if _, err := runMutation(t, l, mutationRequest(1, "uplink to core")); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	drifted := completeObservation("manual edit")
	if _, err := l.EvaluateDrift(context.Background(), "dev-1", drifted, false); err == nil {
		t.Fatal("EvaluateDrift() error = nil, want the audit delivery failure surfaced")
	}

	// The hold must already be engaged despite the audit failure: an audit
	// outage must not disable the safety block that keeps Submit from
	// admitting another mutation over an unexplained change. Submit's
	// hold check is synchronous — it never delivers a checkpoint or waits
	// on anything — so a plain Background() context that would hang
	// forever on the pre-fix ordering (hold engaged only after a
	// successful audit emit, which never arrives) discriminates instead
	// of a timeout that could also fire from an unrelated hang.
	_, err = l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   mutationRequest(2, "another change"),
		Priority:  lane.PriorityNormal,
	})
	if err == nil {
		t.Fatal("Submit() error = nil, want the hold engaged despite the audit delivery failure")
	}
	if code, _ := errs.CodeOf(err); code != access.ErrCodeDesynchronized {
		t.Fatalf("Submit() error code = %v, want %v", code, access.ErrCodeDesynchronized)
	}
}

func TestLaneDriftDeliversAuditEvent(t *testing.T) {
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	deliverer := &spyDeliverer{}
	l := access.NewLane(access.Config{
		QueueCapacity:  4,
		DelayedEffect:  interfaces.DelayedEffect{Horizon: time.Minute},
		Audit:          deliverer,
		Telemetry:      view,
		Clock:          time.Now,
		ManagementMode: inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED,
	})
	addDevice(t, l)

	req := mutationRequest(1, "uplink to core")
	if _, err := runMutation(t, l, req); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	drifted := completeObservation("manual edit")
	if _, err := l.EvaluateDrift(context.Background(), "dev-1", drifted, false); err != nil {
		t.Fatalf("EvaluateDrift() error: %v", err)
	}

	events := deliverer.recorded()
	if len(events) == 0 {
		t.Fatal("expected at least one audit event")
	}
	detail := events[len(events)-1].GetDriftDetected()
	if detail == nil {
		t.Fatal("expected the last delivered event to carry DriftDetected")
	}
	wantFieldName := drifted.GetInterfaceName() + ".description"
	if detail.GetFieldName() != wantFieldName {
		t.Fatalf("DriftDetected.FieldName = %q, want %q", detail.GetFieldName(), wantFieldName)
	}
}

func TestLaneProcessRecordsOperationSpanAndMetric(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	spanRecorder := tracetest.NewSpanRecorder()
	view, err := telemetry.NewView(telemetry.ViewConfig{
		MeterProvider:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder)),
	})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	l := access.NewLane(access.Config{
		QueueCapacity: 4,
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		Audit:         noopDeliverer{},
		Telemetry:     view,
		Clock:         time.Now,
	})
	addDevice(t, l)

	req := mutationRequest(1, "uplink to core")
	if _, err := runMutation(t, l, req); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect() error: %v", err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "flowseer.device.operation.duration" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected process() to record flowseer.device.operation.duration")
	}

	spans := spanRecorder.Ended()
	sawOperationSpan := false
	for _, span := range spans {
		if span.Name() == "flowseer.device.operation" {
			sawOperationSpan = true
		}
	}
	if !sawOperationSpan {
		t.Error("expected process() to start the flowseer.device.operation span")
	}
}

func TestLaneClosedRejectsSubmission(t *testing.T) {
	l := newTestLane(t)
	addDevice(t, l)

	if _, err := l.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	_, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   readRequest(),
		Priority:  lane.PriorityLow,
	})
	if err == nil {
		t.Fatal("Submit() error = nil after Close, want a rejection")
	}
}

// TestLaneCloseStillDeliversCheckpointAndAckToAnAlreadyAdmittedMutation
// proves Close's own doc promise: an item admitted before Close still
// reaches its terminal result, which requires HandleCheckpoint and
// HandleTerminalAck to keep working for it after Close stops new
// admissions.
func TestLaneCloseStillDeliversCheckpointAndAckToAnAlreadyAdmittedMutation(t *testing.T) {
	l := newTestLane(t)
	addDevice(t, l)

	req := mutationRequest(1, "uplink to core")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)

		if _, err := l.Close(context.Background()); err != nil {
			t.Errorf("Close() error: %v", err)
		}

		checkpointReq := &integrationv1.CheckpointRequest{}
		checkpointReq.SetSequence(1)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if err := l.HandleCheckpoint("dev-1", checkpointReq); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}

		ack := &integrationv1.TerminalResultAck{}
		ack.SetSequence(1)
		ack.SetDisposition(accessv1.Disposition_DISPOSITION_VERIFIED)
		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if err := l.HandleTerminalAck("dev-1", ack); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}()

	result, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   req,
		Priority:  lane.PriorityNormal,
	})
	wg.Wait()
	if err != nil {
		t.Fatalf("Submit() error = %v, want the already-admitted mutation to still complete after Close", err)
	}
	if result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Errorf("PhaseReached = %v, want RELEASED", result.GetPhaseReached())
	}
}

func readRequestFor(interfaceName string) *integrationv1.ExecuteRequest {
	readIntent := &accessv1.InterfaceReadIntent{}
	readIntent.SetInterfaceName(interfaceName)
	typedRead := &accessv1.TypedRead{}
	typedRead.SetInterface(readIntent)
	req := &integrationv1.ExecuteRequest{}
	req.SetRead(typedRead)
	return req
}

// TestLaneOneCallersCancellationDoesNotPoisonAnother proves the drain loop
// processes each queued item under its own submitter's context rather than
// whichever caller happened to become the device's active drainer: A's
// context is canceled while A is blocked as the active drainer, but B,
// queued behind A on the same device with its own independent context,
// still completes successfully.
func TestLaneOneCallersCancellationDoesNotPoisonAnother(t *testing.T) {
	l := newTestLane(t)

	releaseA := make(chan struct{})
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		FingerprintOverride: "fw-A",
		ReadOverride: func(ctx context.Context, name string) (*accessv1.InterfaceObservation, error) {
			if name == "eth-A" {
				<-releaseA
				return nil, ctx.Err()
			}
			return completeObservation("uplink to core"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	ctxA, cancelA := context.WithCancel(context.Background())
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		_, _ = l.Submit(ctxA, access.SubmitOptions{DeviceKey: "dev-1", Request: readRequestFor("eth-A"), Priority: lane.PriorityLow})
	}()
	time.Sleep(50 * time.Millisecond) // let A become the active drainer and block in ReadOverride

	var resultB *integrationv1.ExecuteResult
	var errB error
	doneB := make(chan struct{})
	go func() {
		defer close(doneB)
		resultB, errB = l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: readRequestFor("eth-B"), Priority: lane.PriorityLow})
	}()
	time.Sleep(50 * time.Millisecond) // let B enqueue behind A

	cancelA()
	close(releaseA)

	<-doneA
	<-doneB

	if errB != nil {
		t.Fatalf("Submit() for B error = %v, want nil — B's own context was never canceled", errB)
	}
	if got := resultB.GetObservation().GetDescription(); got != "uplink to core" {
		t.Errorf("B's observation description = %q, want %q", got, "uplink to core")
	}
}

// fakeIdentitySession answers epoch.Probe's Get call and nothing else.
type fakeIdentitySession struct {
	onGet func()
}

func (f fakeIdentitySession) Get(_ context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	f.onGet()
	vbs := make([]snmp.VarBind, len(oids))
	for i, oid := range oids {
		if i == 1 {
			vbs[i] = snmp.ObjectIDVar{Header: snmp.Header{OID: oid, Kind: snmp.KindObjectID}, Value: snmp.MustOID(1, 3, 6, 1, 4, 1, 1)}
			continue
		}
		vbs[i] = snmp.OctetStringVar{Header: snmp.Header{OID: oid, Kind: snmp.KindOctetString}, Value: []byte("fw-A")}
	}
	return vbs, nil
}

func (fakeIdentitySession) GetNext(context.Context, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (fakeIdentitySession) GetBulk(context.Context, uint8, uint8, []snmp.OID, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (fakeIdentitySession) Set(context.Context, []snmp.VarBind, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (fakeIdentitySession) Close() error { return nil }

func (fakeIdentitySession) Walk(context.Context, snmp.OID, ...snmp.CallOption) *snmp.Walker {
	return nil
}

func (fakeIdentitySession) BulkWalk(context.Context, snmp.OID, ...snmp.CallOption) *snmp.Walker {
	return nil
}

func (fakeIdentitySession) BulkWalkRaw(context.Context, snmp.OID, ...snmp.CallOption) *snmp.RawWalker {
	return nil
}

var _ = inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP

// TestLaneRapidConcurrentSubmissionsAllComplete stress-tests the drain
// handoff (the lost-wakeup window between a drainer's last empty Next() and
// its Unlock) with many goroutines submitting to one device's queue back to
// back, no synchronizing sleep between them. Before the recheck-and-retry
// loop in Lane.drain, a lucky interleaving could leave an admitted item
// with no active drainer, hanging its Submit call forever; running many
// iterations under -race gives that interleaving many chances to occur.
func TestLaneRapidConcurrentSubmissionsAllComplete(t *testing.T) {
	const n = 200

	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	l := access.NewLane(access.Config{
		QueueCapacity: n,
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		Audit:         noopDeliverer{},
		Telemetry:     view,
		Clock:         time.Now,
	})
	addDevice(t, l)
	var wg sync.WaitGroup
	submitErrs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, submitErrs[i] = l.Submit(ctx, access.SubmitOptions{
				DeviceKey: "dev-1",
				Request:   readRequestFor(fmt.Sprintf("eth-%d", i)),
				Priority:  lane.PriorityLow,
			})
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("not every concurrent Submit returned within 10s — an admitted item was likely stranded with no drainer")
	}

	for i, err := range submitErrs {
		if err != nil {
			t.Errorf("Submit(%d) error: %v", i, err)
		}
	}
}

// TestLaneDrainerDoesNotBlockOnAnotherCallersExpiredWork proves the active
// drainer's own goroutine does not gate a caller's Submit past that
// caller's own item completing: A becomes the drainer, B enqueues behind A
// while A is still inside its own read, and once A's own read returns, the
// SAME drainer goroutine moves on to B's item and blocks there — forever,
// in this test. A's own Submit call must still return promptly: drain runs
// on its own goroutine, so Submit's own select waits only on its own result
// channel, never on the drain loop finishing every item it was handed.
// Before that fix, l.drain(ds) ran synchronously inside Submit's own call,
// so A's Submit would not return until B's item did too.
func TestLaneDrainerDoesNotBlockOnAnotherCallersExpiredWork(t *testing.T) {
	l := newTestLane(t)

	bEnqueued := make(chan struct{})
	releaseB := make(chan struct{})
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		FingerprintOverride: "fw-A",
		ReadOverride: func(_ context.Context, name string) (*accessv1.InterfaceObservation, error) {
			switch name {
			case "eth-A":
				<-bEnqueued // hold A in its own item until B is queued behind it
			case "eth-B":
				<-releaseB // then trap the same drainer goroutine in B's item
			}
			return completeObservation("uplink to core"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	aCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	doneA := make(chan struct{})
	var errA error
	go func() {
		defer close(doneA)
		_, errA = l.Submit(aCtx, access.SubmitOptions{DeviceKey: "dev-1", Request: readRequestFor("eth-A"), Priority: lane.PriorityLow})
	}()
	time.Sleep(50 * time.Millisecond) // let A become the active drainer and block in its own read

	doneB := make(chan struct{})
	go func() {
		defer close(doneB)
		_, _ = l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: readRequestFor("eth-B"), Priority: lane.PriorityLow})
	}()
	time.Sleep(50 * time.Millisecond) // let B enqueue behind A on the same drainer

	close(bEnqueued) // A's own read may now complete; the drainer moves on to B's, which blocks on releaseB

	start := time.Now()
	select {
	case <-doneA:
	case <-time.After(2 * time.Second):
		t.Fatal("A's Submit() did not return after its own item completed; the drainer's continued work on B's item blocked it")
	}
	elapsed := time.Since(start)

	if errA != nil {
		t.Fatalf("Submit() for A error = %v, want nil", errA)
	}
	if elapsed > time.Second {
		t.Fatalf("Submit() for A took %v after B's item was already queued, want it to return promptly once its own item finished", elapsed)
	}

	// B's item is still blocked on releaseB, the same drainer goroutine
	// still inside it, since this test's assertion concerns A alone —
	// release and join it now so no goroutine leaks past this test.
	close(releaseB)
	<-doneB
}

// TestLaneCoalescedJoinerReceivesRealResultDespiteOwnersExpiry proves the
// coalescing owner's own cancellation is never handed to a joiner as if it
// were the shared work's outcome, and that the underlying read is not
// killed by the owner's shorter deadline.
func TestLaneCoalescedJoinerReceivesRealResultDespiteOwnersExpiry(t *testing.T) {
	l := newTestLane(t)

	release := make(chan struct{})
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		FingerprintOverride: "fw-A",
		ReadOverride: func(_ context.Context, _ string) (*accessv1.InterfaceObservation, error) {
			<-release
			// If the owner's cancellation reached this call's own ctx, the
			// real bug would manifest here too; assert nothing on ctx
			// itself since workCtx is deliberately detached.
			return completeObservation("uplink to core"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	ownerCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	ownerDone := make(chan error, 1)
	go func() {
		_, err := l.Submit(ownerCtx, access.SubmitOptions{DeviceKey: "dev-1", Request: readRequest(), Priority: lane.PriorityLow})
		ownerDone <- err
	}()

	var joinerResult *integrationv1.ExecuteResult
	var joinerErr error
	joinerDone := make(chan struct{})
	go func() {
		defer close(joinerDone)
		joinerResult, joinerErr = l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: readRequest(), Priority: lane.PriorityLow})
	}()

	time.Sleep(50 * time.Millisecond) // let both join the same coalescing key

	if err := <-ownerDone; err == nil {
		t.Fatal("owner's Submit() error = nil, want its own deadline to expire")
	}

	close(release)
	<-joinerDone

	if joinerErr != nil {
		t.Fatalf("joiner's Submit() error = %v, want nil — the owner's expiry must not be handed to the joiner", joinerErr)
	}
	if got := joinerResult.GetObservation().GetDescription(); got != "uplink to core" {
		t.Errorf("joiner's observation description = %q, want %q", got, "uplink to core")
	}
}

// TestLaneCoalescedLongerDeadlineJoinerSurvivesShortDeadlineOwner proves
// OperationTimeout is a floor, not a ceiling: a short-deadline owner's own
// deadline must never become the detached work's deadline when a joiner
// behind it has a longer one — that would hand the owner's expiry to the
// joiner as its own outcome after only a fraction of the joiner's own
// budget, the exact failure bug 542b303f fixed at the plain queue path.
// The device answers after the owner's short deadline has already elapsed
// but well within the joiner's own longer one and within OperationTimeout.
func TestLaneCoalescedLongerDeadlineJoinerSurvivesShortDeadlineOwner(t *testing.T) {
	l := newTestLane(t) // OperationTimeout defaults to 30s

	release := make(chan struct{})
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		FingerprintOverride: "fw-A",
		ReadOverride: func(_ context.Context, _ string) (*accessv1.InterfaceObservation, error) {
			<-release
			return completeObservation("uplink to core"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	ownerCtx, ownerCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer ownerCancel()
	joinerCtx, joinerCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer joinerCancel()

	ownerDone := make(chan error, 1)
	go func() {
		_, err := l.Submit(ownerCtx, access.SubmitOptions{DeviceKey: "dev-1", Request: readRequest(), Priority: lane.PriorityLow})
		ownerDone <- err
	}()
	time.Sleep(20 * time.Millisecond) // let the owner win coalescer.Start first

	var joinerResult *integrationv1.ExecuteResult
	var joinerErr error
	joinerDone := make(chan struct{})
	go func() {
		defer close(joinerDone)
		joinerResult, joinerErr = l.Submit(joinerCtx, access.SubmitOptions{DeviceKey: "dev-1", Request: readRequest(), Priority: lane.PriorityLow})
	}()
	time.Sleep(30 * time.Millisecond) // let the joiner join the same coalescing key

	if err := <-ownerDone; err == nil {
		t.Fatal("owner's Submit() error = nil, want its own 100ms deadline to expire")
	}

	// The device answers well after the owner's deadline has elapsed but
	// long before the joiner's own 5s deadline.
	time.Sleep(200 * time.Millisecond)
	close(release)
	<-joinerDone

	if joinerErr != nil {
		t.Fatalf("joiner's Submit() error = %v, want nil — the owner's short deadline must not become the detached work's deadline", joinerErr)
	}
	if got := joinerResult.GetObservation().GetDescription(); got != "uplink to core" {
		t.Errorf("joiner's observation description = %q, want %q", got, "uplink to core")
	}
}

// TestLaneDuplicateCheckpointDeliveryReturnsErrorNotBlock proves a
// redelivered CheckpointRequest for an already-satisfied wait returns an
// error rather than blocking the caller forever on an orphaned channel.
// TestLaneDuplicateCheckpointDeliveryReturnsErrorNotBlock proves three
// concurrent CheckpointRequest deliveries for the same sequence, while the
// mutation is genuinely parked in awaitCheckpoint, never leave a caller
// blocked: exactly one succeeds and the rest return an error promptly. A
// capacity-1 buffered channel checked and sent to as two separate,
// non-atomic steps lets a second delivery silently refill the buffer right
// after the drainer consumes the first, leaving a third with nobody ever
// left to receive it — this needs three concurrent deliveries to
// reproduce, since two never fill the buffer twice.
func TestLaneDuplicateCheckpointDeliveryReturnsErrorNotBlock(t *testing.T) {
	l := newTestLane(t)
	addDevice(t, l)

	req := mutationRequest(1, "uplink to core")

	submitDone := make(chan error, 1)
	go func() {
		_, err := l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: req, Priority: lane.PriorityNormal})
		submitDone <- err
	}()
	time.Sleep(50 * time.Millisecond) // let Submit become the drainer and park in awaitCheckpoint

	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(1)

	const attempts = 3
	type attemptResult struct {
		err     error
		blocked bool
	}
	results := make(chan attemptResult, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := make(chan error, 1)
			go func() { done <- l.HandleCheckpoint("dev-1", checkpointReq) }()
			select {
			case err := <-done:
				results <- attemptResult{err: err}
			case <-time.After(2 * time.Second):
				results <- attemptResult{blocked: true}
			}
		}()
	}
	wg.Wait()
	close(results)

	successes, blocked := 0, 0
	for r := range results {
		if r.blocked {
			blocked++
			continue
		}
		if r.err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("expected exactly one of %d concurrent HandleCheckpoint deliveries to succeed, got %d", attempts, successes)
	}
	if blocked != 0 {
		t.Errorf("%d HandleCheckpoint call(s) blocked instead of returning an error", blocked)
	}

	ack := &integrationv1.TerminalResultAck{}
	ack.SetSequence(1)
	ack.SetDisposition(accessv1.Disposition_DISPOSITION_VERIFIED)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := l.HandleTerminalAck("dev-1", ack); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}

	if err := <-submitDone; err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
}
