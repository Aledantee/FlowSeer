package access_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

// TestAddDeviceRefusesADeviceWithNoMeasuredHorizon covers the device a
// central registry lists without a measured horizon. It is refused before
// any device contact, and the refusal is checked by what did not happen —
// no credential acquired, no session opened — because a probe that failed
// on its own would produce an error too, and from the caller's side the two
// are the same shape.
func TestAddDeviceRefusesADeviceWithNoMeasuredHorizon(t *testing.T) {
	source := &recordingCredentials{}
	l := laneWithCredentials(t, source)
	var opened, closed atomic.Int64

	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		OpenSNMP:     countingProbeFactory(&opened, &closed),
		AccessPolicy: policyHandle("onboarding-policy", 3),
		BindingID:    "binding-1",
	})

	if code, _ := errs.CodeOf(err); code != access.ErrCodeHorizonUnmeasured {
		t.Fatalf("AddDevice() code = %v (err %v), want %v", code, err, access.ErrCodeHorizonUnmeasured)
	}
	if handles, _ := source.acquisitions(); len(handles) != 0 {
		t.Errorf("acquisitions = %v, want none: the refusal must precede the onboarding probe", handles)
	}
	if got := opened.Load(); got != 0 {
		t.Errorf("sessions opened = %d, want 0", got)
	}

	// Refused means not served, not "served with a horizon of zero".
	_, err = l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: readRequest()})
	if code, _ := errs.CodeOf(err); code != access.ErrCodeUnknownDevice {
		t.Errorf("Submit() after the refusal: code = %v (err %v), want %v", code, err, access.ErrCodeUnknownDevice)
	}
}

// The two horizons the test below measures, and the interval its polls run
// on. Each horizon sits just under a multiple of the interval, so the tick
// that abandons — the first one past the horizon — lands almost a full
// interval before the poll's own budget, which is the horizon plus one
// interval. A horizon landing exactly on a tick would leave the two racing,
// and a poll that lost would answer recovery-ambiguous rather than
// abandoning.
const (
	horizonPollInterval = 200 * time.Millisecond
	fastHorizon         = 395 * time.Millisecond
	slowHorizon         = 1195 * time.Millisecond
)

// TestTwoDevicesAbandonAtTheirOwnHorizons is why the horizon is a property
// of the device rather than of the lane. One edge serves devices whose
// measured horizons differ, and a lane-wide horizon abandons whichever
// device is not the one it was set for: the slow device is reported as
// never having applied a mutation that was still landing, or the fast
// device holds its lane for the slow one's horizon.
//
// It runs on real timers, because the horizon is a wall-clock bound and the
// tick-by-tick fake in recovery_loop_test.go serves one channel for the
// whole lane — with two devices polling, which poll a release goes to is
// arbitrary, so the instrument that makes the one-device tests precise
// makes this one meaningless.
func TestTwoDevicesAbandonAtTheirOwnHorizons(t *testing.T) {
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	l := access.NewLane(access.Config{
		QueueCapacity:        4,
		Audit:                noopDeliverer{},
		Telemetry:            view,
		Clock:                time.Now,
		Reporter:             &recordingReporter{},
		OperationTimeout:     5 * time.Second,
		RecoveryPollInterval: horizonPollInterval,
	})
	t.Cleanup(func() { _, _ = l.Close(context.Background()) })

	for _, device := range []struct {
		key     string
		horizon time.Duration
	}{{"dev-fast", fastHorizon}, {"dev-slow", slowHorizon}} {
		if err := l.AddDevice(context.Background(), device.key, access.DeviceSession{
			DelayedEffect: interfaces.DelayedEffect{Horizon: device.horizon},
			OpenSNMP:      probeFactory(),
			// Never shows the mutation applied, so every mutation
			// submitted to either device runs to its horizon.
			ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
				return completeObservation("stale description"), nil
			},
			SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil },
		}); err != nil {
			t.Fatalf("AddDevice(%s) error: %v", device.key, err)
		}
	}

	start := time.Now()
	fast := submitRecoveringMutation(t, l, "dev-fast")
	slow := submitRecoveringMutation(t, l, "dev-slow")

	fastAt := awaitAbandonment(t, fast, "dev-fast", start, 5*time.Second)
	if fastAt < fastHorizon {
		t.Errorf("dev-fast abandoned after %v, before its own horizon of %v", fastAt, fastHorizon)
	}
	if fastAt >= slowHorizon {
		t.Fatalf("dev-fast abandoned after %v, at or past the other device's horizon of %v: this run cannot tell the two horizons apart", fastAt, slowHorizon)
	}

	// The slow device is still recovering at the moment the fast one is
	// abandoned. This is the assertion a lane-wide horizon fails.
	select {
	case got := <-slow:
		t.Fatalf("dev-slow ended after %v, at dev-fast's horizon of %v (result %v, error %v)", time.Since(start), fastHorizon, got.result, got.err)
	default:
	}

	slowAt := awaitAbandonment(t, slow, "dev-slow", start, 5*time.Second)
	if slowAt < slowHorizon {
		t.Errorf("dev-slow abandoned after %v, before its own horizon of %v", slowAt, slowHorizon)
	}
}

// submitRecoveringMutation admits a mutation for one device and returns the
// channel its outcome arrives on. The device never shows the change
// applied, so the mutation enters recovery and is decided by the poll.
func submitRecoveringMutation(t *testing.T, l *access.Lane, deviceKey string) chan submitted {
	t.Helper()
	done := make(chan submitted, 1)
	go func() {
		result, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: deviceKey,
			Request:   mutationRequest(recoverySequence),
		})
		done <- submitted{result, err}
	}()

	// Retried until accepted: the mutation has to have reached its
	// checkpoint wait before the lane will take one.
	req := &integrationv1.CheckpointRequest{}
	req.SetSequence(recoverySequence)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := l.HandleCheckpoint(deviceKey, req); err == nil {
			return done
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s never accepted its checkpoint", deviceKey)
	return done
}

// awaitAbandonment waits for one device's mutation to end and reports how
// long after start that was.
func awaitAbandonment(t *testing.T, done chan submitted, deviceKey string, start time.Time, wait time.Duration) time.Duration {
	t.Helper()
	select {
	case got := <-done:
		elapsed := time.Since(start)
		if got.err != nil {
			t.Fatalf("%s: Submit() error = %v after %v, want the abandonment as a result", deviceKey, got.err, elapsed)
		}
		if got.result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
			t.Fatalf("%s: PhaseReached = %v after %v, want ABANDONED", deviceKey, got.result.GetPhaseReached(), elapsed)
		}
		return elapsed
	case <-time.After(wait):
		t.Fatalf("%s: no answer within %v", deviceKey, wait)
		return 0
	}
}
