package intake

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// BenchmarkDeliver reports records per second for batches of 256-byte syslog
// records delivered to an embedded hub that syncs every message to disk. It
// sets no threshold: the rate depends on the host's disk.
func BenchmarkDeliver(b *testing.B) {
	for _, size := range []int{64, 1024} {
		b.Run(fmt.Sprintf("batch=%d", size), func(b *testing.B) {
			hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
				StateDir:           b.TempDir(),
				FsyncPolicy:        service.BusFsyncPerMessage,
				MaxStoreBytes:      768 << 20,
				CentralBudgetBytes: 512 << 20,
				IngestMaxBytes:     112 << 20,
				EvidenceMaxBytes:   64 << 20,
			})
			if err != nil {
				b.Fatalf("start hub: %v", err)
			}
			b.Cleanup(hub.Close)
			handler, err := NewHandler(HandlerConfig{
				Central:    hub.JetStream(),
				EdgeID:     func(context.Context) (string, error) { return testEdge, nil },
				EdgeTenant: func(context.Context, string) (string, error) { return testTenant, nil },
			})
			if err != nil {
				b.Fatalf("new handler: %v", err)
			}

			// Build every batch before the clock starts, so the loop
			// measures delivery and not fixture construction.
			batches := make([]*connect.Request[edgeingestv1.DeliverRequest], b.N)
			for i := range batches {
				records := make([]*ingestv1.IngestRecord, size)
				for j := range records {
					records[j] = benchRecord(i*size + j)
				}
				batches[i] = connect.NewRequest(edgeingestv1.DeliverRequest_builder{Records: records}.Build())
			}

			b.ResetTimer()
			for _, req := range batches {
				resp, err := handler.Deliver(context.Background(), req)
				if err != nil {
					b.Fatalf("deliver: %v", err)
				}
				if got := int(resp.Msg.GetSettled()); got != size {
					b.Fatalf("settled = %d, want %d", got, size)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(b.N*size)/b.Elapsed().Seconds(), "records/s")
		})
	}
}

// benchRecord builds a valid syslog record with a 256-byte message and a
// record id no other call of the benchmark uses.
func benchRecord(n int) *ingestv1.IngestRecord {
	edge := edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdge)}.Build()}.Build()
	device := inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(testDevice)}.Build()}.Build()
	binding := inventoryv1.BindingGlobalRef_builder{Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build()}.Build()
	now := timestamppb.New(time.Now().UTC())
	return ingestv1.IngestRecord_builder{
		RecordId: proto.String(fmt.Sprintf("0198a3c0-0000-7000-8000-%012d", n)),
		Provenance: inventoryv1.Provenance_builder{
			Binding:    binding,
			ObservedAt: now,
			Edge:       edge,
			Log:        inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum(),
		}.Build(),
		Syslog: eventlogv1.SyslogRecord_builder{
			Device:     device,
			ReceivedAt: now,
			Message:    bytes.Repeat([]byte("x"), 256),
		}.Build(),
	}.Build()
}
