//go:build history_integration

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	historyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/history/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/history/v1/historyv1connect"
	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	logv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/history"
	"go.aledante.io/FlowSeer/src/services/device/internal/history/historytest"
	centralhost "go.aledante.io/FlowSeer/src/services/device/internal/host"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

// historyDeployment is central with a history store, an agent with a syslog
// listener, the one device the registry lists at the loopback address the test
// sends from, and the ClickHouse server behind the store.
type historyDeployment struct {
	central    *central
	agent      *agent
	server     *historytest.Server
	syslogAddr string
	edgeID     string
	history    historyv1connect.HistoryServiceClient
}

// historySection renders the history block of the central configuration for
// the server.
func historySection(cfg history.Config) string {
	user := func(role string, u history.User) string {
		return fmt.Sprintf("  %s {\n    name: %q\n    password_file: %q\n  }\n", role, u.Name, u.AuthFile)
	}
	return fmt.Sprintf("history {\n  endpoints: %q\n  database: %q\n", cfg.Endpoints[0], cfg.Database) +
		user("admin", cfg.Admin) + user("writer", cfg.Writer) + user("reader", cfg.Reader) + user("analyst", cfg.Analyst) + "}\n"
}

// assembleHistory starts ClickHouse and central with a history section, runs
// the device binary's migrate mode against the configuration central reads,
// restarts central once the edge exists, and starts an agent with a syslog
// listener. The first central start has a sink that finds no schema and
// retries, which is the state a deployment is in before its migration runs.
func assembleHistory(t *testing.T) *historyDeployment {
	t.Helper()
	ctx := context.Background()
	server := historytest.Start(t, false)
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))
	loopback := []byte{127, 0, 0, 1}
	registryPath := writeRegistryAt(t, filepath.Join(dir, "registry.textproto"),
		"0192e6a0-0000-7000-8000-00000000dead", fixtureHorizon, loopback)

	c := newCentral(t, dir, registryPath)
	c.history = historySection(server.Cfg)
	c.start()
	created, err := c.admin().CreateEdge(ctx, connect.NewRequest(&apiedgev1.CreateEdgeRequest{}))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	edgeID := created.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId()

	cfg, err := centralhost.LoadConfig(filepath.Join(c.configDir, "central.textproto"))
	if err != nil {
		t.Fatalf("load the configuration central runs on: %v", err)
	}
	var migrated bytes.Buffer
	if err := centralhost.MigrateHistory(ctx, cfg, &migrated); err != nil {
		t.Fatalf("migrate mode: %v", err)
	}
	if !strings.Contains(migrated.String(), "applied step 8") {
		t.Fatalf("migrate mode printed %q, want every step applied", migrated.String())
	}

	c.shutdown()
	writeRegistryAt(t, registryPath, edgeID, fixtureHorizon, loopback)
	c.start()
	c.engine.Grant("user:"+c.principalID, "viewer", "tenant")

	d := &historyDeployment{
		central:    c,
		server:     server,
		syslogAddr: freeUDPAddress(t),
		edgeID:     edgeID,
		history:    historyv1connect.NewHistoryServiceClient(c.client, c.baseURL()),
	}
	d.agent = startAgentWithOptions(t, dir, created.Msg.GetProvisioning(), c.edgeURL(),
		agentOptions{device: newFakeDevice("as found"), syslogAddress: d.syslogAddr})
	d.warmUp(t)
	return d
}

// ingested is how many records the central syslog ingest stream accepted. It
// reads the last sequence, since cleanup removes resolved records and lowers
// the stored count.
func (d *historyDeployment) ingested(t *testing.T) uint64 {
	t.Helper()
	d.central.mu.Lock()
	hub := d.central.hub
	d.central.mu.Unlock()
	stream, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("open the ingest stream: %v", err)
	}
	info, err := stream.Info(context.Background())
	if err != nil {
		t.Fatalf("ingest stream info: %v", err)
	}
	return info.State.LastSeq
}

// send writes syslog messages to the agent's listener, one datagram each, and
// returns once central's ingest stream holds all of them. UDP drops what the
// receiver cannot keep up with, and a dropped datagram looks like a lost
// record, so the sender sends a batch and waits for it to land before the next.
func (d *historyDeployment) send(t *testing.T, messages ...string) {
	t.Helper()
	conn, err := net.Dial("udp", d.syslogAddr)
	if err != nil {
		t.Fatalf("dial the agent's syslog listener: %v", err)
	}
	defer func() { _ = conn.Close() }()
	const batch = 25
	want := d.ingested(t)
	for start := 0; start < len(messages); start += batch {
		end := min(start+batch, len(messages))
		for _, text := range messages[start:end] {
			if _, err := conn.Write([]byte("<34>1 2026-10-02T10:00:00Z sw1 app - - - " + text)); err != nil {
				t.Fatalf("send syslog: %v", err)
			}
		}
		want += uint64(end - start)
		waitUntil(t, fmt.Sprintf("%d records in the ingest stream", want), 30*time.Second, func() bool { return d.ingested(t) >= want })
	}
}

// warmUp sends messages until one reaches central's ingest stream, since a
// datagram sent before the agent has bound its listener or attached its bus is
// lost without a trace.
func (d *historyDeployment) warmUp(t *testing.T) {
	t.Helper()
	conn, err := net.Dial("udp", d.syslogAddr)
	if err != nil {
		t.Fatalf("dial the agent's syslog listener: %v", err)
	}
	defer func() { _ = conn.Close() }()
	waitUntil(t, "the agent to forward a warm-up record", 90*time.Second, func() bool {
		// A refused write is the kernel reporting that nothing listens yet.
		_, _ = conn.Write([]byte("<34>1 2026-10-02T10:00:00Z sw1 app - - - warm-up"))
		return d.ingested(t) > 0
	})
}

// list reads the device's records through the history call for the default
// tenant, in a window around now.
func (d *historyDeployment) list(t *testing.T, pageSize int32) []*historyv1.SyslogHistoryEntry {
	t.Helper()
	req := connect.NewRequest(historyv1.ListSyslogRecordsRequest_builder{
		Device:   deviceRef(),
		From:     timestamppb.New(time.Now().Add(-time.Hour)),
		To:       timestamppb.New(time.Now().Add(time.Hour)),
		PageSize: proto.Int32(pageSize),
	}.Build())
	resp, err := d.history.ListSyslogRecords(context.Background(), req)
	if err != nil {
		t.Fatalf("ListSyslogRecords: %v", err)
	}
	return resp.Msg.GetEntries()
}

// listAll pages ListSyslogRecords until no token remains and returns every
// entry in page order.
func (d *historyDeployment) listAll(t *testing.T, pageSize int32) []*historyv1.SyslogHistoryEntry {
	t.Helper()
	var out []*historyv1.SyslogHistoryEntry
	token := ""
	for page := 0; ; page++ {
		req := historyv1.ListSyslogRecordsRequest_builder{
			Device:   deviceRef(),
			From:     timestamppb.New(time.Now().Add(-time.Hour)),
			To:       timestamppb.New(time.Now().Add(time.Hour)),
			PageSize: proto.Int32(pageSize),
		}.Build()
		if token != "" {
			req.SetPageToken(token)
		}
		resp, err := d.history.ListSyslogRecords(context.Background(), connect.NewRequest(req))
		if err != nil {
			t.Fatalf("ListSyslogRecords page %d: %v", page, err)
		}
		out = append(out, resp.Msg.GetEntries()...)
		if !resp.Msg.HasNextPageToken() {
			return out
		}
		token = resp.Msg.GetNextPageToken()
		if page > 100 {
			t.Fatal("the listing never ended")
		}
	}
}

// ingestStream returns the central syslog ingest stream's configuration and
// state.
func (d *historyDeployment) ingestStream(t *testing.T) *jetstream.StreamInfo {
	t.Helper()
	d.central.mu.Lock()
	hub := d.central.hub
	d.central.mu.Unlock()
	stream, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("open the ingest stream: %v", err)
	}
	info, err := stream.Info(context.Background())
	if err != nil {
		t.Fatalf("ingest stream info: %v", err)
	}
	return info
}

func (d *historyDeployment) scalar(t *testing.T, query string) string {
	t.Helper()
	return d.server.Scalar(t, query)
}

// sinkConsumer returns the history sink's consumer on the central ingest stream.
func (d *historyDeployment) sinkConsumer(t *testing.T) (ackFloor, unacked uint64) {
	t.Helper()
	d.central.mu.Lock()
	hub := d.central.hub
	d.central.mu.Unlock()
	consumer, err := hub.JetStream().Consumer(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog), "history_sink")
	if err != nil {
		t.Fatalf("open the history sink consumer: %v", err)
	}
	info, err := consumer.Info(context.Background())
	if err != nil {
		t.Fatalf("history sink consumer info: %v", err)
	}
	return info.AckFloor.Stream, info.NumPending + uint64(info.NumAckPending)
}

// Requirements 1, 2, and 8 of the history store: a syslog message sent to the
// agent is readable through the history call within seconds, a tenant's
// retention class reaches its rows, and a ClickHouse outage loses nothing.
func TestSyslogFromAnAgentIsReadBackAndAnOutageLosesNothing(t *testing.T) {
	d := assembleHistory(t)

	t.Run("a message sent to the agent is listed once within ten seconds", func(t *testing.T) {
		d.send(t, "link down")

		var entries []*historyv1.SyslogHistoryEntry
		deadline := time.Now().Add(10 * time.Second)
		for len(entries) == 0 && time.Now().Before(deadline) {
			// The warm-up records are listed too, so the wait is for this
			// message and nothing else.
			for _, entry := range d.list(t, 100) {
				if string(entry.GetRecord().GetMessage()) == "link down" {
					entries = append(entries, entry)
				}
			}
			if len(entries) == 0 {
				time.Sleep(100 * time.Millisecond) // polling for the sink's insert
			}
		}

		if len(entries) != 1 {
			t.Fatalf("entries for the message = %d, want 1 within 10 s", len(entries))
		}
		record := entries[0].GetRecord()
		if string(record.GetMessage()) != "link down" || record.GetHostname() != "sw1" || record.GetAppName() != "app" {
			t.Errorf("record = %v, want the message, hostname, and app name sent", record)
		}
		if record.GetSeverity() != logv1.SyslogSeverity_SYSLOG_SEVERITY_CRITICAL || record.GetFacility() != logv1.SyslogFacility_SYSLOG_FACILITY_AUTH {
			t.Errorf("severity %v and facility %v, want CRITICAL and AUTH for <34>", record.GetSeverity(), record.GetFacility())
		}
		if got := entries[0].GetTenant().GetId(); got != edgebus.DefaultTenant {
			t.Errorf("entry tenant = %q, want %q", got, edgebus.DefaultTenant)
		}
		if got := d.scalar(t, "SELECT toString(retention_class) FROM flowseer.syslog WHERE message = 'link down'"); got != "2" {
			t.Errorf("retention_class of a tenant with no class = %s, want 2", got)
		}
	})

	t.Run("a tenant's retention class reaches its rows", func(t *testing.T) {
		d.central.mu.Lock()
		hub := d.central.hub
		d.central.mu.Unlock()
		tenants, err := tenantstore.New(context.Background(), hub.JetStream(), edgebus.TenantBucket)
		if err != nil {
			t.Fatalf("open the tenant store: %v", err)
		}
		const shortTenant, standardTenant = "0192e6a0-0000-7000-8000-0000000000a5", "0192e6a0-0000-7000-8000-0000000000a6"
		for id, class := range map[string]identityv1.RetentionClass{
			shortTenant:    identityv1.RetentionClass_RETENTION_CLASS_SHORT,
			standardTenant: identityv1.RetentionClass_RETENTION_CLASS_UNSPECIFIED,
		} {
			config := identityv1.TenantConfig_builder{
				Ref:                    identityv1.TenantGlobalRef_builder{Tenant: identityv1.TenantLocalRef_builder{Id: proto.String(id)}.Build()}.Build(),
				Issuer:                 proto.String("https://issuer.example.test"),
				OrganizationClaimName:  proto.String("org_id"),
				OrganizationClaimValue: proto.String("org-" + id),
			}.Build()
			if class != identityv1.RetentionClass_RETENTION_CLASS_UNSPECIFIED {
				config.SetRetentionClass(class)
			}
			if _, err := tenants.Create(context.Background(), config); err != nil {
				t.Fatalf("create tenant %s: %v", id, err)
			}
			record := ingestv1.IngestRecord_builder{
				RecordId: proto.String(uuid.NewString()),
				Provenance: inventoryv1.Provenance_builder{
					Binding:    inventoryv1.BindingGlobalRef_builder{Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(fixtureBindingID)}.Build()}.Build(),
					Edge:       edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(d.edgeID)}.Build()}.Build(),
					ObservedAt: timestamppb.Now(),
					Log:        inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum(),
				}.Build(),
				Syslog: eventlogv1.SyslogRecord_builder{
					Device:     deviceRef(),
					ReceivedAt: timestamppb.Now(),
					Message:    []byte("retention " + id),
				}.Build(),
			}.Build()
			data, err := proto.Marshal(record)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if _, err := hub.JetStream().Publish(context.Background(), edgebus.CentralIngestSubject(id, edgebus.IngestRecordTypeSyslog, fixtureDeviceID), data); err != nil {
				t.Fatalf("publish for tenant %s: %v", id, err)
			}
		}

		waitUntil(t, "both tenants' rows", 30*time.Second, func() bool {
			return d.scalar(t, "SELECT toString(count()) FROM flowseer.syslog WHERE tenant_id IN ('"+shortTenant+"', '"+standardTenant+"')") == "2"
		})
		if got := d.scalar(t, "SELECT toString(retention_class) FROM flowseer.syslog WHERE tenant_id = '"+shortTenant+"'"); got != "1" {
			t.Errorf("retention_class of the short tenant = %s, want 1", got)
		}
		if got := d.scalar(t, "SELECT toString(retention_class) FROM flowseer.syslog WHERE tenant_id = '"+standardTenant+"'"); got != "2" {
			t.Errorf("retention_class of a tenant created without one = %s, want 2", got)
		}
	})

	t.Run("1,000 records sent during a 60 second ClickHouse pause are all stored afterwards", func(t *testing.T) {
		floorBefore, _ := d.sinkConsumer(t)
		d.server.Pause(t)
		paused := time.Now()
		messages := make([]string, 1000)
		for i := range messages {
			messages[i] = fmt.Sprintf("outage record %d", i)
		}
		d.send(t, messages...)

		waitUntil(t, "the sink to hold the 1,000 records unacknowledged", 60*time.Second, func() bool {
			_, unacked := d.sinkConsumer(t)
			return unacked >= 1000
		})
		floor, _ := d.sinkConsumer(t)
		if floor != floorBefore {
			t.Errorf("the sink acknowledged up to sequence %d during the pause, want it to stay at %d", floor, floorBefore)
		}
		during := d.ingestStream(t)
		if during.Config.Discard != jetstream.DiscardNew || during.Config.MaxAge != 0 {
			t.Errorf("typed stream discard %v and max age %v, want discard-new with no age", during.Config.Discard, during.Config.MaxAge)
		}
		if held := during.State.LastSeq - floor; during.State.FirstSeq > floor+1 || during.State.Msgs < held || held < 1000 {
			t.Errorf("stream holds sequences %d to %d (%d messages) with the sink floor at %d, want every unresolved record stored",
				during.State.FirstSeq, during.State.LastSeq, during.State.Msgs, floor)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if _, err := d.central.devices().ReadInterface(ctx, connect.NewRequest(devicev1.ReadInterfaceRequest_builder{
			Device: deviceRef(), InterfaceName: proto.String(fixtureInterface),
		}.Build())); err != nil {
			t.Errorf("ReadInterface during the pause: %v", err)
		}

		// The pause is a duration the requirement names. The wait is on the
		// clock because nothing else changes while the server is frozen.
		time.Sleep(time.Until(paused.Add(60 * time.Second)))
		d.server.Unpause(t)

		waitUntil(t, "all 1,000 records stored", 3*time.Minute, func() bool {
			return d.scalar(t, "SELECT toString(countDistinct(record_id)) FROM flowseer.syslog WHERE message LIKE 'outage record %'") == "1000"
		})

		seen := make(map[string]int, len(messages))
		for _, entry := range d.listAll(t, 100) {
			if message := string(entry.GetRecord().GetMessage()); strings.HasPrefix(message, "outage record ") {
				seen[message]++
			}
		}
		for _, message := range messages {
			if seen[message] != 1 {
				t.Errorf("%q listed %d times across pages, want once", message, seen[message])
			}
		}
		waitUntil(t, "cleanup to empty the stream", 2*time.Minute, func() bool { return d.ingestStream(t).State.Msgs == 0 })
	})
}

func waitUntil(t *testing.T, what string, within time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", within, what)
		}
		time.Sleep(250 * time.Millisecond) // polling a condition another goroutine or process brings about
	}
}
