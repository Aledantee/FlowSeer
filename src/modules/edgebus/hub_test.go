package edgebus_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	netlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

func TestIngestStreamsKeepSeventyTwoHoursByDefault(t *testing.T) {
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)

	config := ingestStreamConfig(t, hub)
	if config.MaxAge != 72*time.Hour {
		t.Errorf("typed ingest MaxAge = %v, want 72h", config.MaxAge)
	}
	if config.MaxBytes != 256<<20 {
		t.Errorf("typed ingest MaxBytes = %d, want %d", config.MaxBytes, int64(256<<20))
	}
}

func TestIngestStreamsTakeConfiguredBounds(t *testing.T) {
	cfg := edgebus.HubConfig{
		StateDir:           t.TempDir(),
		FsyncPolicy:        service.BusFsyncPeriodic,
		MaxStoreBytes:      16 << 30,
		CentralBudgetBytes: 4 << 30,
		IngestMaxBytes:     2 << 30,
		IngestMaxAge:       72 * time.Hour,
	}
	hub, err := edgebus.StartHub(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)

	config := ingestStreamConfig(t, hub)
	if config.MaxAge != 72*time.Hour || config.MaxBytes != 2<<30 {
		t.Fatalf("typed ingest limits = %v and %d bytes, want 72h and %d", config.MaxAge, config.MaxBytes, int64(2<<30))
	}
	if config.Name != "FLOWSEER_INGEST_SYSLOG" {
		t.Fatalf("typed ingest stream = %q, want FLOWSEER_INGEST_SYSLOG", config.Name)
	}
}

func TestHubRefusesACentralBudgetBelowTheStreamSum(t *testing.T) {
	cfg := edgebus.HubConfig{
		StateDir:           t.TempDir(),
		FsyncPolicy:        service.BusFsyncPeriodic,
		MaxStoreBytes:      16 << 30,
		CentralBudgetBytes: 2 << 30,
		IngestMaxBytes:     2 << 30,
	}
	if cfg.CentralStreamBytes() <= cfg.CentralBudget() {
		t.Fatalf("fixture sum %d does not exceed its budget %d", cfg.CentralStreamBytes(), cfg.CentralBudget())
	}
	hub, err := edgebus.StartHub(context.Background(), cfg)
	if err == nil {
		hub.Close()
		t.Fatal("StartHub succeeded with a central budget below the stream sum")
	}
}

// TestCentralStreamBytesMatchesTheStreamsTheHubCreates pins the helper the
// host's configuration check relies on to the streams the hub reserves.
func TestCentralStreamBytesMatchesTheStreamsTheHubCreates(t *testing.T) {
	cfg := edgebus.HubConfig{
		StateDir:               t.TempDir(),
		FsyncPolicy:            service.BusFsyncPeriodic,
		MaxStoreBytes:          2 << 30,
		CentralBudgetBytes:     768 << 20,
		AuditMaxBytes:          100 << 20,
		OperatorActionMaxBytes: 30 << 20,
		OperatorReadMaxBytes:   10 << 20,
		IngestMaxBytes:         200 << 20,
		EvidenceMaxBytes:       50 << 20,
	}
	hub, err := edgebus.StartHub(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)

	var reserved int64
	for _, name := range []string{
		edgebus.AuditStream,
		edgebus.OperatorActionStream,
		edgebus.OperatorReadStream,
		edgebus.EvidenceStream,
	} {
		reserved += streamMaxBytes(t, hub, name)
	}
	for _, recordType := range edgebus.IngestRecordTypes() {
		reserved += streamMaxBytes(t, hub, edgebus.IngestStream(recordType))
	}
	if got := cfg.CentralStreamBytes(); got != reserved {
		t.Fatalf("CentralStreamBytes = %d, streams reserve %d", got, reserved)
	}
}

// TestStoredBytesPerSyslogMessage measures what one typed syslog record costs
// in the ingest stream. The README's sizing formula takes this number, so a
// change to the envelope, the subject, or the stream's per-message overhead
// shows here first.
func TestStoredBytesPerSyslogMessage(t *testing.T) {
	ctx := context.Background()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)

	const messages = 500
	tenant := "018f0000-0000-7000-8000-0000000000a1"
	device := "018f0000-0000-7000-8000-0000000000d1"
	subject := edgebus.CentralIngestSubject(tenant, edgebus.IngestRecordTypeSyslog, device)
	var payloadBytes int
	for range messages {
		data := syslogEnvelope(t, tenant, device)
		payloadBytes += len(data)
		msg := &nats.Msg{Subject: subject, Data: data, Header: nats.Header{}}
		msg.Header.Set(nats.MsgIdHdr, tenant+"."+uuid.NewString())
		if _, err := hub.JetStream().PublishMsg(ctx, msg); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	stream, err := hub.JetStream().Stream(ctx, edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load stream: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if info.State.Msgs != messages {
		t.Fatalf("stream holds %d messages, want %d", info.State.Msgs, messages)
	}
	perMessage := float64(info.State.Bytes) / messages
	t.Logf("typed syslog record: %d payload bytes, %.1f stored bytes per message", payloadBytes/messages, perMessage)
	// A stored message costs its subject, header, and payload plus a fixed
	// overhead per record (nats-server fileStoreMsgSizeRaw), so the stored
	// figure exceeds the payload alone and stays within twice of it for a
	// record of this size.
	average := float64(payloadBytes) / messages
	if perMessage <= average || perMessage > 2*average {
		t.Fatalf("stored bytes per message = %.1f for a %.1f byte payload, want between 1x and 2x", perMessage, average)
	}
}

func ingestStreamConfig(t *testing.T, hub *edgebus.Hub) jetstream.StreamConfig {
	t.Helper()
	stream, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load typed ingest stream: %v", err)
	}
	return stream.CachedInfo().Config
}

func streamMaxBytes(t *testing.T, hub *edgebus.Hub, name string) int64 {
	t.Helper()
	stream, err := hub.JetStream().Stream(context.Background(), name)
	if err != nil {
		t.Fatalf("load stream %s: %v", name, err)
	}
	return stream.CachedInfo().Config.MaxBytes
}

func syslogEnvelope(t *testing.T, tenant, device string) []byte {
	t.Helper()
	edge := edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String("018f0000-0000-7000-8000-0000000000e1")}.Build()}.Build()
	binding := inventoryv1.BindingGlobalRef_builder{Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String("018f0000-0000-7000-8000-0000000000b1")}.Build()}.Build()
	deviceRef := inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(device)}.Build()}.Build()
	now := time.Now().UTC()
	envelope := ingestv1.IngestRecord_builder{
		RecordId: proto.String(uuid.NewString()),
		Provenance: inventoryv1.Provenance_builder{
			Binding:    binding,
			ObservedAt: timestamppb.New(now),
			Edge:       edge,
			Log:        inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum(),
		}.Build(),
		Syslog: eventlogv1.SyslogRecord_builder{
			Device:     deviceRef,
			ReceivedAt: timestamppb.New(now),
			SentAt:     timestamppb.New(now),
			Severity:   netlogv1.SyslogSeverity_SYSLOG_SEVERITY_WARNING.Enum(),
			Facility:   netlogv1.SyslogFacility_SYSLOG_FACILITY_LOCAL7.Enum(),
			Hostname:   proto.String("sw1.example.net"),
			AppName:    proto.String("bgpd"),
			Message:    []byte(fmt.Sprintf("neighbor 192.0.2.17 Down: BGP Notification received for tenant %s", tenant)),
		}.Build(),
	}.Build()
	data, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return data
}
