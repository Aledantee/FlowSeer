package drain_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1/ingestv1connect"
	telemetryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1/telemetryv1connect"
	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	recordv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/drain"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/seglog"
)

// undecodable is a truncated length-delimited field, which no message accepts.
var undecodable = []byte{0x0a, 0x05, 'a'}

// bodies keeps the size of every request body a fake service received.
type bodies struct {
	mu    sync.Mutex
	sizes []int
}

func (b *bodies) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		b.mu.Lock()
		b.sizes = append(b.sizes, len(body))
		b.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

type ingestFake struct {
	ingestv1connect.UnimplementedIngestServiceHandler
	bodies
	mu       sync.Mutex
	requests []*ingestv1.DeliverRequest
	settled  uint32
	err      error
}

func (f *ingestFake) Deliver(_ context.Context, req *connect.Request[ingestv1.DeliverRequest]) (*connect.Response[ingestv1.DeliverResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(ingestv1.DeliverResponse_builder{Settled: proto.Uint32(f.settled)}.Build()), nil
}

func newIngestClient(t *testing.T, f *ingestFake) ingestv1connect.IngestServiceClient {
	t.Helper()
	path, handler := ingestv1connect.NewIngestServiceHandler(f)
	mux := http.NewServeMux()
	mux.Handle(path, f.middleware(handler))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return ingestv1connect.NewIngestServiceClient(srv.Client(), srv.URL)
}

type telemetryFake struct {
	telemetryv1connect.UnimplementedTelemetryServiceHandler
	bodies
	mu       sync.Mutex
	requests []*telemetryv1.DeliverRequest
	settled  uint32
	err      error
}

func (f *telemetryFake) Deliver(_ context.Context, req *connect.Request[telemetryv1.DeliverRequest]) (*connect.Response[telemetryv1.DeliverResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req.Msg)
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(telemetryv1.DeliverResponse_builder{Settled: proto.Uint32(f.settled)}.Build()), nil
}

func newTelemetryClient(t *testing.T, f *telemetryFake) telemetryv1connect.TelemetryServiceClient {
	t.Helper()
	path, handler := telemetryv1connect.NewTelemetryServiceHandler(f)
	mux := http.NewServeMux()
	mux.Handle(path, f.middleware(handler))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return telemetryv1connect.NewTelemetryServiceClient(srv.Client(), srv.URL)
}

// ingestRecord builds a record that passes protovalidate. n makes its record id
// unique, and rawBytes above zero attaches that much raw evidence.
func ingestRecord(n, rawBytes int) *recordv1.IngestRecord {
	b := recordv1.IngestRecord_builder{
		RecordId: proto.String(fmt.Sprintf("0192e6a0-0000-7000-8000-%012d", n)),
		Provenance: inventoryv1.Provenance_builder{
			Binding: inventoryv1.BindingGlobalRef_builder{
				Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-000000000002")}.Build(),
			}.Build(),
			ObservedAt: timestamppb.New(time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)),
			Log:        inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum(),
		}.Build(),
		Syslog: eventlogv1.SyslogRecord_builder{
			Device: inventoryv1.DeviceGlobalRef_builder{
				Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-000000000001")}.Build(),
			}.Build(),
			ReceivedAt: timestamppb.New(time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)),
		}.Build(),
	}
	if rawBytes > 0 {
		b.Raw = recordv1.RawEvidence_builder{
			Data:   make([]byte, rawBytes),
			Reason: recordv1.RawReason_RAW_REASON_PARSE_FAILURE.Enum(),
		}.Build()
	}
	return b.Build()
}

func telemetryExport(signal telemetryv1.TelemetrySignal, bodyBytes int) *telemetryv1.TelemetryExport {
	body := bytes.Repeat([]byte{byte(signal)}, bodyBytes)
	return telemetryv1.TelemetryExport_builder{Signal: &signal, Body: body}.Build()
}

// marshalAll validates each fixture the way the wire would, then marshals it.
func marshalAll[T proto.Message](t *testing.T, items ...T) [][]byte {
	t.Helper()
	out := make([][]byte, len(items))
	for i, item := range items {
		if err := protovalidate.Validate(item); err != nil {
			t.Fatalf("fixture %d fails protovalidate: %v", i, err)
		}
		b, err := proto.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = b
	}
	return out
}

func requireEqualItems[T proto.Message](t *testing.T, got, want []T) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("request holds %d items, want %d", len(got), len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("item %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestIngestSendsEveryRecordInOrder(t *testing.T) {
	f := &ingestFake{settled: 2}
	send := drain.Ingest(newIngestClient(t, f))
	records := []*recordv1.IngestRecord{ingestRecord(1, 0), ingestRecord(2, 10), ingestRecord(3, 0)}

	settled, err := send(context.Background(), marshalAll(t, records...))
	if err != nil {
		t.Fatal(err)
	}
	if settled != 2 {
		t.Errorf("settled = %d, want 2", settled)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.requests))
	}
	requireEqualItems(t, f.requests[0].GetRecords(), records)
}

func TestTelemetrySendsEveryExportInOrder(t *testing.T) {
	f := &telemetryFake{settled: 3}
	send := drain.Telemetry(newTelemetryClient(t, f))
	exports := []*telemetryv1.TelemetryExport{
		telemetryExport(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, 5),
		telemetryExport(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_METRICS, 6),
		telemetryExport(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_TRACES, 7),
	}

	settled, err := send(context.Background(), marshalAll(t, exports...))
	if err != nil {
		t.Fatal(err)
	}
	if settled != 3 {
		t.Errorf("settled = %d, want 3", settled)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.requests))
	}
	requireEqualItems(t, f.requests[0].GetExports(), exports)
}

func TestSendersReturnACallErrorWithNothingSettled(t *testing.T) {
	refusal := connect.NewError(connect.CodeUnavailable, io.ErrUnexpectedEOF)
	records := marshalAll(t, ingestRecord(1, 0))
	exports := marshalAll(t, telemetryExport(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, 1))

	ingest := drain.Ingest(newIngestClient(t, &ingestFake{err: refusal}))
	if settled, err := ingest(context.Background(), records); err == nil || settled != 0 {
		t.Errorf("Ingest() = %d, %v, want 0 and an error", settled, err)
	}
	telemetry := drain.Telemetry(newTelemetryClient(t, &telemetryFake{err: refusal}))
	if settled, err := telemetry(context.Background(), exports); err == nil || settled != 0 {
		t.Errorf("Telemetry() = %d, %v, want 0 and an error", settled, err)
	}
}

func TestSendersMapSettledPastAnUndecodableRecord(t *testing.T) {
	good := marshalAll(t, ingestRecord(1, 0), ingestRecord(2, 0))
	records := [][]byte{good[0], undecodable, good[1]}
	for _, tc := range []struct {
		name    string
		settled uint32
		want    int
	}{
		{"none", 0, 0},
		{"first", 1, 2},
		{"all", 2, 3},
		{"above", 9, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &ingestFake{settled: tc.settled}
			got, err := drain.Ingest(newIngestClient(t, f))(context.Background(), records)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("settled = %d, want %d", got, tc.want)
			}
			if len(f.requests) != 1 || len(f.requests[0].GetRecords()) != 2 {
				t.Errorf("request carried the undecodable record: %v", f.requests)
			}
		})
	}
}

func TestSendersMakeNoCallWhenNothingDecodes(t *testing.T) {
	fi := &ingestFake{}
	got, err := drain.Ingest(newIngestClient(t, fi))(context.Background(), [][]byte{undecodable, undecodable})
	if err != nil || got != 2 || len(fi.requests) != 0 {
		t.Errorf("Ingest() = %d, %v with %d calls, want 2, nil, 0", got, err, len(fi.requests))
	}
	ft := &telemetryFake{}
	got, err = drain.Telemetry(newTelemetryClient(t, ft))(context.Background(), [][]byte{undecodable})
	if err != nil || got != 1 || len(ft.requests) != 0 {
		t.Errorf("Telemetry() = %d, %v with %d calls, want 1, nil, 0", got, err, len(ft.requests))
	}
}

func TestDecodableAcceptsOnlyTheCallsItem(t *testing.T) {
	record := marshalAll(t, ingestRecord(1, 0))[0]
	export := marshalAll(t, telemetryExport(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, 3))[0]
	if !drain.IngestDecodable(record) || drain.IngestDecodable(undecodable) {
		t.Error("IngestDecodable must accept a record and refuse a truncated one")
	}
	if !drain.TelemetryDecodable(export) || drain.TelemetryDecodable(undecodable) {
		t.Error("TelemetryDecodable must accept an export and refuse a truncated one")
	}
}

// oneSegment appends records to a fresh log, then reopens it so the records
// come back as the one sealed segment Next returns.
func oneSegment(t *testing.T, records [][]byte) seglog.Segment {
	t.Helper()
	dir := t.TempDir()
	l := openLog(t, dir, seglog.Config{})
	for _, r := range records {
		if err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	seg, err := openLog(t, dir, seglog.Config{}).Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(seg.Records) != len(records) {
		t.Fatalf("segment holds %d records, want %d in one segment", len(seg.Records), len(records))
	}
	return seg
}

func TestOneFullSegmentOfIngestRecordsFillsOneRequestUnderOneMiB(t *testing.T) {
	const segmentBytes, headers = 512 << 10, 20
	var items []*recordv1.IngestRecord
	used := int64(headers)
	for i := 0; ; i++ {
		item := ingestRecord(i+1, 65535)
		size := int64(proto.Size(item)) + 8
		if used+size > segmentBytes {
			break
		}
		used += size
		items = append(items, item)
	}
	seg := oneSegment(t, marshalAll(t, items...))

	f := &ingestFake{settled: uint32(len(items))}
	settled, err := drain.Ingest(newIngestClient(t, f))(context.Background(), seg.Records)
	if err != nil || settled != len(items) {
		t.Fatalf("Ingest() = %d, %v, want %d, nil", settled, err, len(items))
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.requests))
	}
	requireEqualItems(t, f.requests[0].GetRecords(), items)
	if size := f.sizes[0]; size >= 1<<20 || size != proto.Size(f.requests[0]) {
		t.Errorf("request body = %d bytes, want the bare message under 1 MiB (%d)", size, proto.Size(f.requests[0]))
	}
}

func TestOneFullSegmentOfTelemetryExportsFillsOneRequestUnderOneMiB(t *testing.T) {
	// Two largest exports and a small one fill the segment to within a few bytes.
	items := []*telemetryv1.TelemetryExport{
		telemetryExport(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, 261120),
		telemetryExport(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_METRICS, 261120),
		telemetryExport(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_TRACES, 1500),
	}
	seg := oneSegment(t, marshalAll(t, items...))

	f := &telemetryFake{settled: uint32(len(items))}
	settled, err := drain.Telemetry(newTelemetryClient(t, f))(context.Background(), seg.Records)
	if err != nil || settled != len(items) {
		t.Fatalf("Telemetry() = %d, %v, want %d, nil", settled, err, len(items))
	}
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.requests))
	}
	requireEqualItems(t, f.requests[0].GetExports(), items)
	if size := f.sizes[0]; size >= 1<<20 || size != proto.Size(f.requests[0]) {
		t.Errorf("request body = %d bytes, want the bare message under 1 MiB (%d)", size, proto.Size(f.requests[0]))
	}
}

func TestTheLargestTelemetryExportFitsALogRecord(t *testing.T) {
	export := telemetryExport(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_METRICS, 261120)
	size := proto.Size(export)
	if size != 261126 {
		t.Errorf("proto.Size = %d, want 261126", size)
	}
	l := openLog(t, t.TempDir(), seglog.Config{})
	if err := l.Append(marshalAll(t, export)[0]); err != nil {
		t.Errorf("Append(%d bytes) = %v, want nil under the log's record bound", size, err)
	}
}
