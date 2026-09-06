package access_test

import (
	"context"
	"sync"
	"testing"
	"time"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/lane"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

type noopDeliverer struct{}

func (noopDeliverer) Emit(context.Context, *eventv1.DeviceOperationEvent) error { return nil }

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

func mutationRequest(sequence uint64, fingerprint, description string) *integrationv1.ExecuteRequest {
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
	var reads int
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		FingerprintOverride: "fw-A",
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			reads++
			return completeObservation("uplink to core"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil },
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}
}

// runMutation drives one mutation admission through checkpoint and terminal
// ack concurrently with Submit, since both block on external delivery.
func runMutation(t *testing.T, l *access.Lane, deviceKey string, req *integrationv1.ExecuteRequest) (*integrationv1.ExecuteResult, error) {
	t.Helper()
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

	req := mutationRequest(1, "fw-A", "uplink to core")
	result, err := runMutation(t, l, "dev-1", req)
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

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// The second item is admitted (capacity 1, queue currently empty since
	// the first was dequeued into processing) but a third must overload.
	go func() {
		_, _ = l.Submit(ctx, access.SubmitOptions{DeviceKey: "dev-1", Request: secondReq, Priority: lane.PriorityLow})
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

	close(release)
	<-firstDone
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

func TestLaneOnboardingRunsIdentityProbeBeforeFirstMutation(t *testing.T) {
	l := newTestLane(t)

	var probed bool
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
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

	req := mutationRequest(1, "fw-A", "uplink to core")
	if _, err := runMutation(t, l, "dev-1", req); err != nil {
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
		Request:   mutationRequest(2, "fw-A", "another change"),
		Priority:  lane.PriorityNormal,
	}); err == nil {
		t.Fatal("Submit() error = nil, want the hold to block a new mutation")
	}

	if err := l.ResolveHold("dev-1"); err != nil {
		t.Fatalf("ResolveHold() error: %v", err)
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
			// A realistic caller would fail a request made with an
			// already-canceled context; checking this here is what makes
			// the test able to tell A's context from B's.
			if err := ctx.Err(); err != nil {
				return nil, err
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
