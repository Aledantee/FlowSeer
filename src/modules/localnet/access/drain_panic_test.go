package access_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// panicFirstReadSession embeds a *walkingSession for the identity probe
// (Get) and everything else snmp.Session needs, and overrides only GetBulk,
// which is what the interface capability's column walk actually calls
// (src/protocol/snmp/column_request.go). It panics on GetBulk's first call
// and answers from the embedded fixture after, so the fixture stays useful
// for the read that follows the panic.
type panicFirstReadSession struct {
	*walkingSession
	calls atomic.Int32
}

func (s *panicFirstReadSession) GetBulk(ctx context.Context, nonRepeaters, maxRepetitions uint8, oids []snmp.OID, opts ...snmp.CallOption) ([]snmp.VarBind, error) {
	if s.calls.Add(1) == 1 {
		panic("simulated device read panic")
	}
	return s.walkingSession.GetBulk(ctx, nonRepeaters, maxRepetitions, oids, opts...)
}

// TestLaneDrainReleasesItsLockAfterAPanicSoALaterDrainProceeds pins two
// properties of a device read that panics on drain's own goroutine.
//
// The submitter is told the read failed. A panic unwinds past every
// assignment to the result, so an answer that reported whatever the result
// variable happened to hold would tell the caller the work succeeded and
// produced nothing — which central records as a completed mutation.
//
// The device keeps working. ds.draining is released whatever way process
// leaves, so the next drain wins its TryLock; a release that only the normal
// return performed would leave the lock held for the life of the process,
// and every later read for that device would queue behind it forever.
func TestLaneDrainReleasesItsLockAfterAPanicSoALaterDrainProceeds(t *testing.T) {
	l := newTestLane(t)
	session := &panicFirstReadSession{walkingSession: &walkingSession{vbs: interfaceRows("ethernet 1/1/1", snmpDescription, true)}}
	if err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP: func(context.Context, *attachv1.DeviceCredential) (access.SNMPSession, error) {
			return access.SNMPSession{Session: session, Close: func() error { return nil }}, nil
		},
	}); err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	// Panics on drain's own goroutine, inside l.process, while drainOnce
	// holds ds.draining.
	result, err := l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: readRequest()})
	if err == nil {
		t.Fatalf("Submit() over a panicking read returned no error; got result %v", result)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err = l.Submit(ctx, access.SubmitOptions{DeviceKey: "dev-1", Request: readRequest()})
	if err != nil {
		t.Fatalf("second Submit() after a panicking first read: %v (drain's lock was not released)", err)
	}
	if got := result.GetObservation().GetDescription(); got != snmpDescription {
		t.Fatalf("expected the second read to reach the device's fixture, got description %q", got)
	}
}
