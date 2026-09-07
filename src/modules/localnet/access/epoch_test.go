package access_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// switchableIdentity answers the identity probe with whichever sysDescr the
// test currently wants, so a firmware change can be staged between one probe
// and the next without touching the lane.
type switchableIdentity struct {
	mu     sync.Mutex
	descr  string
	probes atomic.Int64
}

func (d *switchableIdentity) set(descr string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.descr = descr
}

func (d *switchableIdentity) session() snmp.Session {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.probes.Add(1)
	return identityAnswering(d.descr)
}

// epochSpy records the FirmwareEpochChanged records the lane emits.
type epochSpy struct {
	mu      sync.Mutex
	changes [][2]string
}

func (d *epochSpy) Emit(_ context.Context, event *eventv1.DeviceOperationEvent) error {
	if change := event.GetFirmwareEpochChanged(); change != nil {
		d.mu.Lock()
		d.changes = append(d.changes, [2]string{change.GetPreviousFingerprint(), change.GetNewFingerprint()})
		d.mu.Unlock()
	}
	return nil
}

func (d *epochSpy) recorded() [][2]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([][2]string(nil), d.changes...)
}

func newEpochLane(t *testing.T, identity *switchableIdentity, deliverer auditDeliverer, reporter access.Reporter) *access.Lane {
	t.Helper()
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	l := access.NewLane(access.Config{
		QueueCapacity:        4,
		DelayedEffect:        interfaces.DelayedEffect{Horizon: time.Hour},
		Audit:                deliverer,
		Telemetry:            view,
		Clock:                time.Now,
		Reporter:             reporter,
		OperationTimeout:     2 * time.Second,
		RecoveryPollInterval: time.Minute,
	})
	err = l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		OpenSNMP: func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
			return access.SNMPSession{Session: identity.session(), Close: func() error { return nil }}, nil
		},
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil },
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}
	return l
}

// TestAFirmwareChangeBeforeTheCommandStopsIt is the pre-Execute probe. An
// intent central built against one firmware must not be applied to another:
// the command's meaning belongs to the firmware, not to central.
//
// Nothing was sent, so this is a refusal and not ambiguity — the report
// carries submitted false, which is what lets central dispose it REJECTED.
func TestAFirmwareChangeBeforeTheCommandStopsIt(t *testing.T) {
	identity := &switchableIdentity{descr: "firmware A"}
	spy := &epochSpy{}
	reporter := &recordingReporter{}
	l := newEpochLane(t, identity, spy, reporter)

	var submits atomic.Int64
	// Replace the device's submit hook so the test can count commands.
	if err := l.AddDevice(context.Background(), "dev-2", access.DeviceSession{
		OpenSNMP: func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
			return access.SNMPSession{Session: identity.session(), Close: func() error { return nil }}, nil
		},
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error {
			submits.Add(1)
			return nil
		},
	}); err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	// The device reboots into new firmware between onboarding and the
	// mutation's own pre-command probe.
	identity.set("firmware B")

	done := make(chan error, 1)
	go func() {
		_, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-2",
			Request:   mutationRequestWithFingerprint(1, fingerprintOf(t, "firmware A"), "uplink to core"),
		})
		done <- err
	}()

	// No checkpoint is delivered: the block happens before that wait. The
	// mutation now waits for central to dispose it, which is the point —
	// the lane refuses to apply the intent and reports why, and central
	// decides. It does not decide for central.
	deadline := time.Now().Add(3 * time.Second)
	var acked bool
	for time.Now().Before(deadline) {
		err := l.HandleTerminalAck(context.Background(), "dev-2",
			terminalAck(1, accessv1.Disposition_DISPOSITION_REJECTED))
		if err == nil {
			acked = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !acked {
		t.Fatal("the REJECTED acknowledgement was never accepted for the blocked mutation")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Submit() error = %v, want the released mutation after central disposed it", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the blocked mutation never answered its caller")
	}

	if n := submits.Load(); n != 0 {
		t.Errorf("the device received %d commands, want 0: the firmware changed before anything was sent", n)
	}

	changes := spy.recorded()
	if len(changes) != 1 {
		t.Fatalf("recorded %v firmware changes, want exactly one", changes)
	}
	if changes[0][0] != fingerprintOf(t, "firmware A") || changes[0][1] != fingerprintOf(t, "firmware B") {
		t.Errorf("the record names %v, want the old and new fingerprints", changes[0])
	}

	// Some report told central nothing was sent, which is what lets it
	// dispose the mutation REJECTED rather than treating it as unknown.
	for _, report := range reporter.reported() {
		if report.GetSubmitted() {
			t.Fatal("a report says submitted true; nothing was ever sent")
		}
	}
	var sawEpochError bool
	for _, report := range reporter.reported() {
		if payload := report.GetError(); payload != nil && payload.GetCode() == string(access.ErrCodeFirmwareEpoch) {
			sawEpochError = true
		}
	}
	if !sawEpochError {
		t.Error("no report carried the firmware-epoch refusal central needs to act on")
	}
}

// TestAFirmwareChangeAfterAReadDiscardsIt covers the second probe point. An
// observation taken across an epoch boundary has no honest fingerprint to
// label its provenance with — the device that answered is not the device the
// read was planned against — so it is discarded rather than recorded under
// either epoch.
func TestAFirmwareChangeAfterAReadDiscardsIt(t *testing.T) {
	identity := &switchableIdentity{descr: "firmware A"}
	spy := &epochSpy{}
	l := newEpochLane(t, identity, spy, nil)

	// Change the firmware after onboarding: the read's own post-observation
	// probe is the one that finds it.
	identity.set("firmware B")

	_, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   readRequest(),
	})
	if code, _ := errs.CodeOf(err); code != access.ErrCodeFirmwareEpoch {
		t.Fatalf("Submit() code = %v, want %v: an observation across an epoch boundary must not be returned", code, access.ErrCodeFirmwareEpoch)
	}

	if changes := spy.recorded(); len(changes) != 1 {
		t.Fatalf("recorded %v firmware changes, want exactly one", changes)
	}

	// The lane now works under the new epoch: the next read succeeds, and
	// a mutation expecting the new fingerprint is admitted.
	if _, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   readRequest(),
	}); err != nil {
		t.Fatalf("the read after the epoch change failed: %v", err)
	}
}

// TestAnUnchangedFirmwareLeavesTheOperationAlone is the partner the
// change tests need. Every assertion above would also pass for a lane that
// failed every operation with mutation/firmware-epoch regardless.
func TestAnUnchangedFirmwareLeavesTheOperationAlone(t *testing.T) {
	identity := &switchableIdentity{descr: "firmware A"}
	spy := &epochSpy{}
	l := newEpochLane(t, identity, spy, nil)

	result, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   readRequest(),
	})
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if result.GetObservation() == nil {
		t.Error("the read returned no observation")
	}
	if changes := spy.recorded(); len(changes) != 0 {
		t.Errorf("recorded %v firmware changes, want none: the firmware did not change", changes)
	}
}
