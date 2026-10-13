package integration_test

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	identityapiv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	ingestapiv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/ingest/v1/ingestv1connect"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// rawWindowDeployment is central, an agent with a syslog listener, and the one
// device the registry lists at the loopback address the test sends from.
type rawWindowDeployment struct {
	central    *central
	agent      *agent
	dir        string
	edgeID     string
	syslogAddr string
	agentOpts  agentOptions
	windows    ingestv1connect.RawWindowServiceClient
	provision  *edgev1.EdgeProvisioning
}

func freeUDPAddress(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a UDP port: %v", err)
	}
	defer func() { _ = conn.Close() }()
	return conn.LocalAddr().String()
}

// assembleRawWindow brings up central with a per-edge allowance and an agent
// with a syslog listener, and gives the operator an active full-payload grant.
func assembleRawWindow(t *testing.T) *rawWindowDeployment {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))
	loopback := []byte{127, 0, 0, 1}
	registryPath := writeRegistryAt(t, filepath.Join(dir, "registry.textproto"),
		"0192e6a0-0000-7000-8000-00000000dead", fixtureHorizon, loopback)

	c := newCentral(t, dir, registryPath)
	c.rawWindowAllowance = 1 << 20
	c.start()
	created, err := c.admin().CreateEdge(ctx, connect.NewRequest(&apiedgev1.CreateEdgeRequest{}))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	edgeID := created.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId()
	c.shutdown()
	writeRegistryAt(t, registryPath, edgeID, fixtureHorizon, loopback)
	c.start()

	member := identityv1.OperatorRef_builder{Issuer: new(c.issuer.URL()), Subject: new("e2e-operator")}.Build()
	if _, err := c.tenantAdmin().EnrollMember(ctx, connect.NewRequest(identityapiv1.EnrollMemberRequest_builder{Member: member}.Build())); err != nil {
		t.Fatalf("enroll the operator: %v", err)
	}
	if _, err := c.tenantAdmin().GrantFullPayload(ctx, connect.NewRequest(identityapiv1.GrantFullPayloadRequest_builder{
		Member: member, Lifetime: durationpb.New(time.Hour), Reason: new("raw window integration test"),
	}.Build())); err != nil {
		t.Fatalf("grant full payload: %v", err)
	}

	d := &rawWindowDeployment{
		central:    c,
		dir:        dir,
		edgeID:     edgeID,
		syslogAddr: freeUDPAddress(t),
		windows:    ingestv1connect.NewRawWindowServiceClient(c.client, c.baseURL()),
		provision:  created.Msg.GetProvisioning(),
	}
	d.agentOpts = agentOptions{device: newFakeDevice("as found"), syslogAddress: d.syslogAddr}
	d.agent = startAgentWithOptions(t, dir, d.provision, c.edgeURL(), d.agentOpts)
	return d
}

func (d *rawWindowDeployment) edgeRef() *edgev1.EdgeGlobalRef {
	return edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: new(d.edgeID)}.Build()}.Build()
}

func rawWindowDeviceSelector() *rawwindowv1.RawWindowSelector {
	return rawwindowv1.RawWindowSelector_builder{Device: inventoryv1.DeviceGlobalRef_builder{
		Device: inventoryv1.DeviceLocalRef_builder{Id: new(fixtureDeviceID)}.Build(),
	}.Build()}.Build()
}

func rawWindowIntegrationSelector() *rawwindowv1.RawWindowSelector {
	return rawwindowv1.RawWindowSelector_builder{Integration: inventoryv1.IntegrationGlobalRef_builder{
		Integration: inventoryv1.IntegrationLocalRef_builder{Id: new(fixtureIntegrationID)}.Build(),
	}.Build()}.Build()
}

func rawWindowSourceSelector() *rawwindowv1.RawWindowSelector {
	return rawwindowv1.RawWindowSelector_builder{Source: rawwindowv1.RawWindowSource_builder{Key: new("syslog")}.Build()}.Build()
}

// open admits a window, retrying while the agent has not yet subscribed, which
// central answers with FailedPrecondition.
func (d *rawWindowDeployment) open(t *testing.T, selector *rawwindowv1.RawWindowSelector, maxBytes uint64) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		resp, err := d.windows.OpenRawWindow(context.Background(), connect.NewRequest(ingestapiv1.OpenRawWindowRequest_builder{
			Edge:      d.edgeRef(),
			Selector:  selector,
			ExpiresAt: timestamppb.New(time.Now().Add(30 * time.Minute)),
			MaxBytes:  new(maxBytes),
			Authorization: rawwindowv1.RawWindowAuthorization_builder{
				Reason: new("integration test"),
			}.Build(),
		}.Build()))
		if err == nil {
			if got := resp.Msg.GetWindow().GetState().GetLifecycle(); got != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
				t.Fatalf("a window the edge has not confirmed reads %v, want PENDING", got)
			}
			return resp.Msg.GetWindow().GetConfig().GetRef().GetRawWindow().GetId()
		}
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || time.Now().After(deadline) {
			t.Fatalf("OpenRawWindow: %v", err)
		}
		time.Sleep(200 * time.Millisecond) // polling until the agent's subscription is recorded
	}
}

func (d *rawWindowDeployment) get(t *testing.T, id string) *rawwindowv1.RawWindowRecord {
	t.Helper()
	resp, err := d.windows.GetRawWindow(context.Background(), connect.NewRequest(ingestapiv1.GetRawWindowRequest_builder{
		RawWindow: rawwindowv1.RawWindowLocalRef_builder{Id: new(id)}.Build(),
	}.Build()))
	if err != nil {
		t.Fatalf("GetRawWindow: %v", err)
	}
	return resp.Msg.GetWindow()
}

func (d *rawWindowDeployment) waitLifecycle(t *testing.T, id string, want rawwindowv1.RawWindowLifecycle) *rawwindowv1.RawWindowRecord {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		record := d.get(t, id)
		if record.GetState().GetLifecycle() == want {
			return record
		}
		if time.Now().After(deadline) {
			t.Fatalf("window %s stayed %v, want %v", id, record.GetState().GetLifecycle(), want)
		}
		time.Sleep(200 * time.Millisecond) // polling central's view of the edge's reports
	}
}

func (d *rawWindowDeployment) closeWindow(t *testing.T, id string) {
	t.Helper()
	if _, err := d.windows.CloseRawWindow(context.Background(), connect.NewRequest(ingestapiv1.CloseRawWindowRequest_builder{
		RawWindow: rawwindowv1.RawWindowLocalRef_builder{Id: new(id)}.Build(),
	}.Build())); err != nil {
		t.Fatalf("CloseRawWindow: %v", err)
	}
}

func (d *rawWindowDeployment) send(t *testing.T, text string) {
	t.Helper()
	conn, err := net.Dial("udp", d.syslogAddr)
	if err != nil {
		t.Fatalf("dial the agent's syslog listener: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - " + text)); err != nil {
		t.Fatalf("send syslog: %v", err)
	}
}

// ingested returns the typed record and the evidence message that carry text,
// waiting for the typed record. Evidence is nil when none was stored.
func (d *rawWindowDeployment) typedRecord(t *testing.T, text string) *ingestv1.IngestRecord {
	t.Helper()
	d.central.mu.Lock()
	hub := d.central.hub
	d.central.mu.Unlock()
	typed, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("open the typed stream: %v", err)
	}
	subject := edgebus.CentralIngestSubject(d.central.tenant, edgebus.IngestRecordTypeSyslog, fixtureDeviceID)
	return waitForRecord(t, func() *ingestv1.IngestRecord {
		return lastRecordWith(context.Background(), typed, subject, text, func(r *ingestv1.IngestRecord) bool {
			return strings.Contains(string(r.GetSyslog().GetMessage()), text)
		})
	}, "the typed record for "+text)
}

func (d *rawWindowDeployment) evidenceFor(t *testing.T, text string) *ingestv1.IngestRecord {
	t.Helper()
	d.central.mu.Lock()
	hub := d.central.hub
	d.central.mu.Unlock()
	evidence, err := hub.JetStream().Stream(context.Background(), edgebus.EvidenceStream)
	if err != nil {
		t.Fatalf("open the evidence stream: %v", err)
	}
	subject := edgebus.EvidenceSubject(d.central.tenant, edgebus.IngestRecordTypeSyslog, fixtureDeviceID)
	return waitForRecord(t, func() *ingestv1.IngestRecord {
		return lastRecordWith(context.Background(), evidence, subject, text, func(r *ingestv1.IngestRecord) bool {
			return bytes.Contains(r.GetRaw().GetData(), []byte(text))
		})
	}, "the evidence for "+text)
}

// noEvidenceFor reports whether the evidence stream holds a record for text.
func (d *rawWindowDeployment) hasEvidenceFor(t *testing.T, text string) bool {
	t.Helper()
	d.central.mu.Lock()
	hub := d.central.hub
	d.central.mu.Unlock()
	evidence, err := hub.JetStream().Stream(context.Background(), edgebus.EvidenceStream)
	if err != nil {
		t.Fatalf("open the evidence stream: %v", err)
	}
	subject := edgebus.EvidenceSubject(d.central.tenant, edgebus.IngestRecordTypeSyslog, fixtureDeviceID)
	return lastRecordWith(context.Background(), evidence, subject, text, func(r *ingestv1.IngestRecord) bool {
		return bytes.Contains(r.GetRaw().GetData(), []byte(text))
	}) != nil
}

func waitForRecord(t *testing.T, find func() *ingestv1.IngestRecord, what string) *ingestv1.IngestRecord {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if record := find(); record != nil {
			return record
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond) // polling a stream another process fills
	}
}

// waitDeviceResolved sends probes until central stores one, which proves the
// agent has listed the device and can resolve the sender. Probes sent before
// that are dropped as unknown, which is why the probe repeats.
func (d *rawWindowDeployment) waitDeviceResolved(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for i := 0; ; i++ {
		text := "probe-" + strings.Repeat("x", i%3) + time.Now().Format("150405.000")
		d.send(t, text)
		time.Sleep(500 * time.Millisecond) // a dropped probe never arrives, so the wait is the signal
		if d.probeStored(t) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent never resolved the device as a syslog sender")
		}
	}
}

func (d *rawWindowDeployment) probeStored(t *testing.T) bool {
	t.Helper()
	d.central.mu.Lock()
	hub := d.central.hub
	d.central.mu.Unlock()
	typed, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("open the typed stream: %v", err)
	}
	info, err := typed.Info(context.Background())
	return err == nil && info.State.Msgs > 0
}

func hasWindow(record *ingestv1.IngestRecord, id string) bool {
	for _, ref := range record.GetRaw().GetWindows() {
		if ref.GetId() == id {
			return true
		}
	}
	return false
}

// TestRawWindowSyslogThroughAgentAndIntakeUnderEachScope opens a window under
// each selector kind, sends one syslog record through the real agent, and reads
// what central stored. The evidence stream holds the bytes and the window ref.
// The typed stream holds the same record with neither.
func TestRawWindowSyslogThroughAgentAndIntakeUnderEachScope(t *testing.T) {
	d := assembleRawWindow(t)
	d.waitDeviceResolved(t)

	scopes := []struct {
		name     string
		selector *rawwindowv1.RawWindowSelector
	}{
		{name: "device", selector: rawWindowDeviceSelector()},
		{name: "integration", selector: rawWindowIntegrationSelector()},
		{name: "source", selector: rawWindowSourceSelector()},
	}
	for _, scope := range scopes {
		t.Run(scope.name, func(t *testing.T) {
			id := d.open(t, scope.selector, 4096)
			d.waitLifecycle(t, id, rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE)

			text := "scope-" + scope.name
			d.send(t, text)

			evidence := d.evidenceFor(t, text)
			if evidence.GetRaw().GetReason() != ingestv1.RawReason_RAW_REASON_WINDOW || !hasWindow(evidence, id) {
				t.Errorf("evidence raw = %v, want WINDOW evidence naming %s", evidence.GetRaw(), id)
			}
			typed := d.typedRecord(t, text)
			if typed.HasRaw() {
				t.Errorf("typed record carries raw evidence %v, want none", typed.GetRaw())
			}
			if typed.GetRecordId() != evidence.GetRecordId() {
				t.Errorf("typed record id %q differs from the evidence's %q", typed.GetRecordId(), evidence.GetRecordId())
			}

			d.closeWindow(t, id)
			ended := d.waitLifecycle(t, id, rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL)
			if ended.GetState().GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_OPERATOR {
				t.Errorf("stop reason = %v, want the operator's close", ended.GetState().GetStopReason())
			}
		})
	}
}

// TestRawWindowBudgetEndsTheWindowWithoutTruncating spends a budget that holds
// one record and not a second. The second record is stored typed, without
// evidence, and the window ends with the byte budget as its reason.
func TestRawWindowBudgetEndsTheWindowWithoutTruncating(t *testing.T) {
	d := assembleRawWindow(t)
	d.waitDeviceResolved(t)

	first := "budget-first"
	payloadLen := uint64(len("<34>1 2026-10-03T10:00:00Z sw1 app - - - " + first))
	id := d.open(t, rawWindowDeviceSelector(), payloadLen+5)
	d.waitLifecycle(t, id, rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE)

	d.send(t, first)
	if evidence := d.evidenceFor(t, first); !hasWindow(evidence, id) {
		t.Fatalf("first record evidence = %v, want it to name %s", evidence.GetRaw(), id)
	}

	second := "budget-second-record-that-does-not-fit"
	d.send(t, second)
	d.typedRecord(t, second)
	ended := d.waitLifecycle(t, id, rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL)
	if ended.GetState().GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_BYTE_BUDGET {
		t.Errorf("stop reason = %v, want the byte budget", ended.GetState().GetStopReason())
	}
	if ended.GetState().GetUsage().GetSelectedBytes() != payloadLen {
		t.Errorf("selected bytes = %d, want only the first record's %d", ended.GetState().GetUsage().GetSelectedBytes(), payloadLen)
	}
	if d.hasEvidenceFor(t, second) {
		t.Error("the record that did not fit left evidence behind")
	}
}

// TestRawWindowCloseOwedAcrossAnAgentRestartEndsWithANewEpoch closes a window
// while the agent is down. Central keeps the close owed, and the restarted
// agent runs under a new boot generation and epoch, so central ends the window
// of the process that is gone rather than reissuing it.
func TestRawWindowCloseOwedAcrossAnAgentRestartEndsWithANewEpoch(t *testing.T) {
	d := assembleRawWindow(t)
	d.waitDeviceResolved(t)
	id := d.open(t, rawWindowDeviceSelector(), 4096)
	d.waitLifecycle(t, id, rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_ACTIVE)

	d.agent.shutdown()
	d.closeWindow(t, id)
	if got := d.get(t, id).GetState().GetLifecycle(); got == rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL {
		t.Fatalf("a window closed while its edge was down reads %v, want it still owed", got)
	}

	d.agent = startAgentWithOptions(t, d.dir, d.provision, d.central.edgeURL(), d.agentOpts)
	ended := d.waitLifecycle(t, id, rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_TERMINAL)
	if reason := ended.GetState().GetStopReason(); reason != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EDGE_RESTART &&
		reason != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_OPERATOR {
		t.Errorf("stop reason = %v, want the restart or the close", reason)
	}

	d.waitDeviceResolved(t)
	d.send(t, "after-restart")
	d.typedRecord(t, "after-restart")
	if d.hasEvidenceFor(t, "after-restart") {
		t.Error("a restarted agent retained evidence for a window of its previous process")
	}
}

// lastRecordWith scans a stream for the newest record on subject that match
// accepts, or nil. The scan is over what the stream holds, since two records
// from one device share a subject and only their content tells them apart.
func lastRecordWith(ctx context.Context, stream jetstream.Stream, subject, _ string, match func(*ingestv1.IngestRecord) bool) *ingestv1.IngestRecord {
	info, err := stream.Info(ctx)
	if err != nil {
		return nil
	}
	for seq := info.State.LastSeq; seq >= info.State.FirstSeq && seq > 0; seq-- {
		msg, err := stream.GetMsg(ctx, seq)
		if err != nil || msg.Subject != subject {
			continue
		}
		record := &ingestv1.IngestRecord{}
		if proto.Unmarshal(msg.Data, record) == nil && match(record) {
			return record
		}
	}
	return nil
}
