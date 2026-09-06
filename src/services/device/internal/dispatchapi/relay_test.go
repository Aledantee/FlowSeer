package dispatchapi

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

const (
	deviceID = "0192e6a0-0000-7000-8000-0000000000d1"
	edgeID   = "0192e6a0-0000-7000-8000-0000000000ed"
	horizon  = 30 * time.Minute
)

type fakeResolver struct {
	lists bool
}

func (f fakeResolver) Devices(context.Context, string) ([]string, error) {
	return []string{deviceID}, nil
}

func (f fakeResolver) Horizon(context.Context, string) (time.Duration, error) { return horizon, nil }
func (f fakeResolver) Lists(context.Context, string) bool                     { return f.lists }

type capture struct {
	msgs []*integrationv1.SubscribeResponse
}

func (c *capture) Send(m *integrationv1.SubscribeResponse) error {
	c.msgs = append(c.msgs, m)
	return nil
}

func newFixture(t *testing.T) (*Service, *journal.Journal, jetstream.KeyValue) {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	kv, err := hub.JetStream().KeyValue(context.Background(), edgebus.LaneBucket)
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	j := journal.New(kv, nil)
	svc := New(Config{
		Journal:  j,
		Resolver: fakeResolver{lists: true},
		Watch:    kv,
		EdgeID:   func(context.Context) (string, error) { return edgeID, nil },
		Resend:   50 * time.Millisecond,
	})
	return svc, j, kv
}

func mutationIntent(key string) *accessv1.MutationIntent {
	device := &inventoryv1.DeviceGlobalRef{}
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(deviceID)
	device.SetDevice(local)
	actor := &accessv1.Actor{}
	op := &accessv1.OperatorRef{}
	op.SetSubject("zitadel|1")
	actor.SetOperator(op)
	policy := &policyv1.AccessPolicyHandle{}
	policy.SetKey("icx7150-lab")
	policy.SetVersion(3)
	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName("ethernet 1/1/1")
	change.SetDescription("uplink to core")
	intent := &accessv1.MutationIntent{}
	intent.SetDevice(device)
	intent.SetIdempotencyKey(key)
	intent.SetActor(actor)
	intent.SetAccessPolicy(policy)
	intent.SetExpectedFirmwareFingerprint("ICX7150-24P SPS10010g")
	intent.SetInterfaceDescription(change)
	return intent
}

func edgeRef() *edgev1.EdgeGlobalRef {
	ref := &edgev1.EdgeGlobalRef{}
	local := &edgev1.EdgeLocalRef{}
	local.SetId(edgeID)
	ref.SetEdge(local)
	return ref
}

func typedRead() *accessv1.TypedRead {
	read := &accessv1.TypedRead{}
	policy := &policyv1.AccessPolicyHandle{}
	policy.SetKey("icx7150-lab")
	policy.SetVersion(3)
	read.SetAccessPolicy(policy)
	intent := &accessv1.InterfaceReadIntent{}
	intent.SetInterfaceName("ethernet 1/1/1")
	read.SetInterface(intent)
	return read
}

func pass(t *testing.T, svc *Service) []*integrationv1.SubscribeResponse {
	t.Helper()
	sink := &capture{}
	if err := svc.dispatchPass(context.Background(), edgeID, sink); err != nil {
		t.Fatalf("dispatch pass: %v", err)
	}
	return sink.msgs
}

func TestOwedMutationIsSentOnOpen(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000a01"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	msgs := pass(t, svc)
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msgs))
	}
	exec := msgs[0].GetExecute()
	if exec == nil || exec.GetMutation() == nil {
		t.Fatalf("first message is not an execute-mutation: %+v", msgs[0])
	}
	if exec.GetResume() {
		t.Fatal("a fresh dispatch carries resume")
	}
	if exec.GetDeadline() == nil {
		t.Fatal("execute has no deadline")
	}
}

func TestReSendsWhileOwedAndStopsOnConfirmation(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000a02"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if len(pass(t, svc)) != 1 {
		t.Fatal("an owed row was not sent on the first pass")
	}
	if len(pass(t, svc)) != 1 {
		t.Fatal("an owed row is not re-sent while it stays owed")
	}
	// Walk the mutation to release; nothing is owed after.
	for _, r := range []journal.Report{
		{Kind: journal.ReportAdmitted, Sequence: 1},
		{Kind: journal.ReportVerified, Sequence: 1},
		{Kind: journal.ReportReleased, Sequence: 1},
	} {
		if err := j.ApplyReport(ctx, deviceID, r); err != nil {
			t.Fatalf("apply %v: %v", r.Kind, err)
		}
	}
	if msgs := pass(t, svc); len(msgs) != 0 {
		t.Fatalf("a released mutation still owes %d messages", len(msgs))
	}
}

func TestMutationAndReadDispatchedSideBySide(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000a03"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if _, err := j.OpenRead(ctx, deviceID, deviceRef(deviceID), "ethernet 1/1/1", typedRead(), "0192e6a0-0000-7000-8000-000000000f01", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("open read: %v", err)
	}
	var mut, read bool
	for _, m := range pass(t, svc) {
		if e := m.GetExecute(); e != nil && e.GetMutation() != nil {
			mut = true
		}
		if e := m.GetExecute(); e != nil && e.GetRead() != nil {
			read = true
		}
	}
	if !mut || !read {
		t.Fatalf("mutation=%v read=%v, want both dispatched", mut, read)
	}
}

func TestNoTerminalAckForNeverDispatchedIntent(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000a04"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	// Abandon before the edge reports admitted: the lane closes and owes a
	// hold-resolved row, never a terminal ack.
	if _, err := j.Dispose(ctx, deviceID, 1); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	for _, m := range pass(t, svc) {
		if m.GetTerminalAck() != nil {
			t.Fatal("a never-dispatched intent owed a terminal ack")
		}
	}
}

func TestExpiredReadSweptNotSent(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.OpenRead(ctx, deviceID, deviceRef(deviceID), "ethernet 1/1/1", typedRead(), "0192e6a0-0000-7000-8000-000000000f02", time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("open read: %v", err)
	}
	if msgs := pass(t, svc); len(msgs) != 0 {
		t.Fatalf("an expired read was dispatched: %d messages", len(msgs))
	}
	rec, _ := j.Record(ctx, deviceID)
	if !rec.GetOpenReads()["ethernet 1/1/1"].HasError() {
		t.Fatal("the pass did not close the expired read with an error")
	}
}

func TestOnboardedResumeCarriesAdmissionTime(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000a05"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	if err := j.MarkOnboarded(ctx, deviceID); err != nil {
		t.Fatalf("onboarded: %v", err)
	}
	msgs := pass(t, svc)
	if len(msgs) != 1 || msgs[0].GetExecute() == nil {
		t.Fatalf("after Onboarded the mutation was not re-dispatched: %+v", msgs)
	}
	exec := msgs[0].GetExecute()
	if !exec.GetResume() || exec.GetAdmittedAt() == nil {
		t.Fatalf("resume=%v admitted_at=%v, want a resume carrying the admission time", exec.GetResume(), exec.GetAdmittedAt())
	}
}

func TestRunningSweeperClosesAnExpiredRead(t *testing.T) {
	svc, j, kv := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := j.OpenRead(ctx, deviceID, deviceRef(deviceID), "ethernet 1/1/1", typedRead(), "0192e6a0-0000-7000-8000-000000000f03", time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("open read: %v", err)
	}
	go svc.RunSweeper(ctx, kv)

	deadline := time.Now().Add(5 * time.Second)
	for {
		rec, err := j.Record(ctx, deviceID)
		if err != nil {
			t.Fatalf("record: %v", err)
		}
		if rec.GetOpenReads()["ethernet 1/1/1"].HasError() {
			return // the running relay closed the expired read
		}
		if time.Now().After(deadline) {
			t.Fatal("the running sweeper did not close the expired read")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
