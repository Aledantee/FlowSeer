package access_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
)

// frozenSpy records the device of every LaneFrozen record it is handed and
// fails for whichever devices a test names, so a test can break exactly one
// device's delivery and leave the rest working.
type frozenSpy struct {
	mu       sync.Mutex
	frozen   []string
	failFor  map[string]bool
	failures int
}

func newFrozenSpy() *frozenSpy { return &frozenSpy{failFor: map[string]bool{}} }

func (d *frozenSpy) Emit(_ context.Context, event *eventv1.DeviceOperationEvent) error {
	if event.GetLaneFrozen() == nil {
		return nil
	}
	device := event.GetDevice().GetDevice().GetId()

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failFor[device] {
		d.failures++
		return errors.New("audit delivery unavailable")
	}
	d.frozen = append(d.frozen, device)
	return nil
}

func (d *frozenSpy) recorded() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.frozen...)
}

func (d *frozenSpy) fail(devices ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failFor = map[string]bool{}
	for _, device := range devices {
		d.failFor[device] = true
	}
}

func addNamedDevice(t *testing.T, l *access.Lane, deviceKey string) error {
	t.Helper()
	return l.AddDevice(context.Background(), deviceKey, access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      probeFactory(),
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil },
	})
}

// TestFreezeRecordsEveryDeviceAndRetriesOnlyWhatFailed walks the whole
// partial-failure path in one test, because each step is only meaningful
// given the one before it.
func TestFreezeRecordsEveryDeviceAndRetriesOnlyWhatFailed(t *testing.T) {
	spy := newFrozenSpy()
	l := laneWithReporter(t, nil, spy)
	var submits atomic.Int64
	addDeviceCountingSubmits(t, l, &submits)
	for _, device := range []string{"dev-2", "dev-3"} {
		if err := addNamedDevice(t, l, device); err != nil {
			t.Fatalf("AddDevice(%s) error: %v", device, err)
		}
	}

	// The second of three fails.
	spy.fail("dev-2")
	if err := l.Freeze(context.Background()); err == nil {
		t.Fatal("Freeze() error = nil, want the failed delivery surfaced")
	}
	if got := spy.recorded(); len(got) != 2 || got[0] != "dev-1" || got[1] != "dev-3" {
		t.Fatalf("recorded %v, want dev-1 and dev-3", got)
	}

	// The gate is frozen regardless of the failed delivery. An audit
	// outage must not be able to leave the devices unfenced, which is the
	// whole reason the gate is frozen before anything is emitted.
	assertNoDeviceWriteWhileFrozen(t, l, &submits)

	// A second Freeze emits only what is still missing.
	spy.fail()
	if err := l.Freeze(context.Background()); err != nil {
		t.Fatalf("second Freeze() error: %v", err)
	}
	if got := spy.recorded(); len(got) != 3 || got[2] != "dev-2" {
		t.Fatalf("after the retry recorded %v, want exactly one more record, for dev-2", got)
	}

	// Unfreeze forgets the fence, so the next one records afresh.
	l.Unfreeze(context.Background())
	if err := l.Freeze(context.Background()); err != nil {
		t.Fatalf("third Freeze() error: %v", err)
	}
	if got := spy.recorded(); len(got) != 6 {
		t.Fatalf("after Unfreeze and a third Freeze recorded %v (%d), want three more records", got, len(got))
	}
}

// TestDeviceAddedWhileFrozenGetsItsRecord covers the device that was not
// registered when the fence was called. Without it the audit stream shows a
// fence covering only the devices that happened to exist at that moment,
// and a reader cannot tell a device was fenced from its first moment.
func TestDeviceAddedWhileFrozenGetsItsRecord(t *testing.T) {
	spy := newFrozenSpy()
	l := laneWithReporter(t, nil, spy)
	if err := addNamedDevice(t, l, "dev-1"); err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}
	if err := l.Freeze(context.Background()); err != nil {
		t.Fatalf("Freeze() error: %v", err)
	}
	if got := spy.recorded(); len(got) != 1 {
		t.Fatalf("recorded %v, want one record", got)
	}

	if err := addNamedDevice(t, l, "dev-2"); err != nil {
		t.Fatalf("AddDevice() while frozen error: %v", err)
	}
	if got := spy.recorded(); len(got) != 2 || got[1] != "dev-2" {
		t.Fatalf("recorded %v, want dev-2's record emitted at AddDevice", got)
	}

	// And it is not emitted a second time by a later Freeze, since the
	// device already has one for this fence.
	if err := l.Freeze(context.Background()); err != nil {
		t.Fatalf("Freeze() after the late AddDevice error: %v", err)
	}
	if got := spy.recorded(); len(got) != 2 {
		t.Fatalf("recorded %v, want no further record", got)
	}
}

// TestAddDeviceWhileFrozenFailsWithoutRegisteringTheDevice pins the write
// order. The record is emitted before the device is registered, so a failed
// delivery leaves nothing behind: reversing the two would register a device
// into a fenced lane while telling the caller it was not added, and the
// audit stream would never say that device was fenced.
func TestAddDeviceWhileFrozenFailsWithoutRegisteringTheDevice(t *testing.T) {
	spy := newFrozenSpy()
	l := laneWithReporter(t, nil, spy)
	if err := l.Freeze(context.Background()); err != nil {
		t.Fatalf("Freeze() error: %v", err)
	}

	spy.fail("dev-1")
	if err := addNamedDevice(t, l, "dev-1"); err == nil {
		t.Fatal("AddDevice() error = nil, want the failed LaneFrozen delivery surfaced")
	}

	// Not registered: the lane does not know this device at all.
	_, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   readRequest(),
	})
	if code, _ := errs.CodeOf(err); code != access.ErrCodeUnknownDevice {
		t.Fatalf("Submit() code = %v, want %v: the device must not have been registered", code, access.ErrCodeUnknownDevice)
	}
}

// assertNoDeviceWriteWhileFrozen proves the gate is stopping device writes,
// by counting the commands that actually reached the device.
//
// Asserting only that Submit failed would be satisfied by a Submit that
// could not have succeeded either way: it blocks on central's terminal
// acknowledgement, which this helper never delivers, so a short context
// expires and Submit returns an error whether or not the gate is frozen at
// all. The count is the only thing here that distinguishes a fenced lane
// from a lane that merely ran out of time.
func assertNoDeviceWriteWhileFrozen(t *testing.T, l *access.Lane, submits *atomic.Int64) {
	t.Helper()
	before := submits.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = l.Submit(ctx, access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(1),
		})
	}()

	// Let it past the checkpoint so it reaches the execute step, which is
	// the step the gate stops.
	req := &integrationv1.CheckpointRequest{}
	req.SetSequence(1)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := l.HandleCheckpoint("dev-1", req); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Submit() never returned")
	}

	if got := submits.Load(); got != before {
		t.Fatalf("the device received %d commands while the lane was frozen, want none: the gate is not stopping side effects", got-before)
	}
}

// blockingFrozenSpy is a frozenSpy whose LaneFrozen delivery parks until a
// test releases it, standing in for the host audit sink that is slow or
// wedged — which is the state a fence is usually called in.
type blockingFrozenSpy struct {
	frozenSpy
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingFrozenSpy() *blockingFrozenSpy {
	return &blockingFrozenSpy{
		frozenSpy: frozenSpy{failFor: map[string]bool{}},
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
	}
}

func (d *blockingFrozenSpy) Emit(ctx context.Context, event *eventv1.DeviceOperationEvent) error {
	if event.GetLaneFrozen() != nil {
		d.once.Do(func() { close(d.entered) })
		<-d.release
	}
	return d.frozenSpy.Emit(ctx, event)
}

// TestUnfreezeDoesNotWaitOnTheAuditPath is why the fence bookkeeping and the
// emission are two locks rather than one. A fence is called when something
// is already wrong, quite often the audit sink itself; holding one lock
// across every delivery would make lifting that fence — the thing that lets
// the devices be written again — wait on exactly the path that failed.
//
// The single-goroutine tests above cannot see this: they never have an
// Unfreeze running while an emission is in flight.
func TestUnfreezeDoesNotWaitOnTheAuditPath(t *testing.T) {
	spy := newBlockingFrozenSpy()
	l := laneWithReporter(t, nil, spy)
	if err := addNamedDevice(t, l, "dev-1"); err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	frozen := make(chan error, 1)
	go func() { frozen <- l.Freeze(context.Background()) }()

	<-spy.entered

	unfrozen := make(chan struct{})
	go func() {
		defer close(unfrozen)
		l.Unfreeze(context.Background())
	}()
	select {
	case <-unfrozen:
	case <-time.After(2 * time.Second):
		t.Fatal("Unfreeze() is parked behind the blocked audit delivery")
	}

	close(spy.release)
	if err := <-frozen; err != nil {
		t.Fatalf("Freeze() error: %v", err)
	}

	// The record that was in flight belonged to the fence that has since
	// been lifted, so the next fence still owes this device one. Counting
	// this is the difference between "Unfreeze returned" and "Unfreeze
	// forgot the fence".
	if err := l.Freeze(context.Background()); err != nil {
		t.Fatalf("Freeze() after the unfreeze error: %v", err)
	}
	if got := spy.recorded(); len(got) != 2 {
		t.Fatalf("recorded %v, want the fence after the unfreeze to record dev-1 again", got)
	}
}

// TestAFreezeDrainedOnAReopenedGateRecordsNothing covers the freeze whose
// drain finished only after an Unfreeze had already reopened the gate. The
// fence it was establishing does not exist, so writing LaneFrozen for it
// would put a fence in the audit stream for an open lane — and, worse, mark
// those devices as recorded, so the next real fence would emit nothing.
func TestAFreezeDrainedOnAReopenedGateRecordsNothing(t *testing.T) {
	spy := newFrozenSpy()
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, spy)

	inCommand := make(chan struct{})
	releaseCommand := make(chan struct{})
	var submits atomic.Int64
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      probeFactory(),
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error {
			if submits.Add(1) == 1 {
				close(inCommand)
				<-releaseCommand
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	// A mutation parked inside the device command holds the gate's drain
	// barrier, which is what makes the freeze below wait rather than
	// complete instantly.
	submitDone := make(chan error, 1)
	go func() {
		_, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(1),
		})
		submitDone <- err
	}()
	deliverCheckpoint(t, l, 1)
	<-inCommand

	frozen := make(chan error, 1)
	go func() { frozen <- l.Freeze(context.Background()) }()
	// Freeze sets the gate's frozen flag before it starts waiting on the
	// drain, and there is nothing exported to observe that moment through —
	// the gate is internal and a read is not gated by it. The wait is for
	// the goroutine above to have reached Freeze at all; if it has not, this
	// test unfreezes a gate nobody froze and the Freeze that follows
	// succeeds, which fails the assertion below rather than passing it.
	time.Sleep(200 * time.Millisecond)

	// Reopen the gate under the waiting freeze, then let the command finish
	// so the drain the freeze is waiting on completes.
	l.Unfreeze(context.Background())
	close(releaseCommand)

	select {
	case err := <-frozen:
		if err == nil {
			t.Fatal("Freeze() error = nil, want the reopened gate reported")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Freeze() never returned")
	}
	if got := spy.recorded(); len(got) != 0 {
		t.Fatalf("recorded %v, want nothing: the lane is open, so there is no fence to record", got)
	}

	// The other direction: the next real fence records the device, which it
	// would not have done had the drained-on-a-reopened-gate freeze marked
	// it delivered.
	waitForPhase(t, reporter, accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)
	if err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_VERIFIED)); err != nil {
		t.Fatalf("HandleTerminalAck() error: %v", err)
	}
	if err := awaitSubmit(t, submitDone); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if err := l.Freeze(context.Background()); err != nil {
		t.Fatalf("Freeze() error: %v", err)
	}
	if got := spy.recorded(); len(got) != 1 || got[0] != "dev-1" {
		t.Fatalf("recorded %v, want the real fence to record dev-1", got)
	}
}
