package access_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
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

// TestLaneDrainReleasesItsLockAfterAPanicSoALaterDrainProceeds is evidence
// for this change. drain's TryLock/Unlock loop used to call Unlock
// explicitly, reached only by l.process's normal return, so a panic while
// process ran left ds.draining held forever: every later drain for that
// device would TryLock, lose, and silently do nothing, stranding whatever
// it had just queued behind a lock nothing would ever release. drainOnce
// releases the lock from a defer instead, so release does not depend on
// process returning normally.
//
// The panicking read's own outcome is deliberately not asserted: l.process
// answers sub.result from its own defer regardless of whether it panicked,
// which is existing behavior this unit did not touch and is not what this
// test is evidence for. What the fix causes is that a second, independent
// read for the same device completes at all rather than blocking until its
// own context's timeout — proof that drain became this device's drainer
// again instead of finding the lock already, and permanently, taken.
func TestLaneDrainReleasesItsLockAfterAPanicSoALaterDrainProceeds(t *testing.T) {
	l := newTestLane(t)
	session := &panicFirstReadSession{walkingSession: &walkingSession{vbs: interfaceRows("ethernet 1/1/1", snmpDescription, true)}}
	if err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP: func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
			return access.SNMPSession{Session: session, Close: func() error { return nil }}, nil
		},
	}); err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	// Panics on drain's own goroutine, inside l.process, while drainOnce
	// holds ds.draining.
	_, _ = l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: readRequest()})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := l.Submit(ctx, access.SubmitOptions{DeviceKey: "dev-1", Request: readRequest()})
	if err != nil {
		t.Fatalf("second Submit() after a panicking first read: %v (drain's lock was not released)", err)
	}
	if got := result.GetObservation().GetDescription(); got != snmpDescription {
		t.Fatalf("expected the second read to reach the device's fixture, got description %q", got)
	}
}
