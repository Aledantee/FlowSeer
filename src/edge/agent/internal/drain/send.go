package drain

import (
	"context"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1/ingestv1connect"
	telemetryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1/telemetryv1connect"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Ingest returns a [Send] that unmarshals each record into an IngestRecord
// and delivers them in one IngestService.Deliver call. Central stores a
// record delivered twice once, so a resend after a short settle is safe.
//
// A record that does not unmarshal is left out of the request and counts as
// settled with the records before the first unsettled one. When no record
// unmarshals, Ingest makes no call and reports all of them settled, since the
// request requires at least one record.
func Ingest(client ingestv1connect.IngestServiceClient) Send {
	return func(ctx context.Context, records [][]byte) (int, error) {
		return deliver(records, func() *ingestv1.IngestRecord { return new(ingestv1.IngestRecord) },
			func(items []*ingestv1.IngestRecord) (uint32, error) {
				resp, err := client.Deliver(ctx, connect.NewRequest(edgeingestv1.DeliverRequest_builder{Records: items}.Build()))
				if err != nil {
					return 0, errs.From(err).Msg("deliver ingest records")
				}
				return resp.Msg.GetSettled(), nil
			})
	}
}

// Telemetry returns a [Send] that unmarshals each record into a
// TelemetryExport and delivers them in one TelemetryService.Deliver call.
// Central does not deduplicate, so a resend can post a body to the collector
// twice. An undecodable record is handled as [Ingest] handles one.
func Telemetry(client telemetryv1connect.TelemetryServiceClient) Send {
	return func(ctx context.Context, records [][]byte) (int, error) {
		return deliver(records, func() *telemetryv1.TelemetryExport { return new(telemetryv1.TelemetryExport) },
			func(items []*telemetryv1.TelemetryExport) (uint32, error) {
				resp, err := client.Deliver(ctx, connect.NewRequest(telemetryv1.DeliverRequest_builder{Exports: items}.Build()))
				if err != nil {
					return 0, errs.From(err).Msg("deliver telemetry exports")
				}
				return resp.Msg.GetSettled(), nil
			})
	}
}

// IngestDecodable reports whether record unmarshals into an IngestRecord. It
// is the [Config.Decodable] of an ingest drain.
func IngestDecodable(record []byte) bool {
	return proto.Unmarshal(record, new(ingestv1.IngestRecord)) == nil
}

// TelemetryDecodable reports whether record unmarshals into a TelemetryExport.
// It is the [Config.Decodable] of a telemetry drain.
func TelemetryDecodable(record []byte) bool {
	return proto.Unmarshal(record, new(telemetryv1.TelemetryExport)) == nil
}

// deliver unmarshals records, calls once with the items that decode, and maps
// the settled count back to a count of records. A count above the items sent
// means all of them.
func deliver[T proto.Message](records [][]byte, newItem func() T, call func([]T) (uint32, error)) (int, error) {
	items := make([]T, 0, len(records))
	at := make([]int, 0, len(records))
	for i, record := range records {
		item := newItem()
		if proto.Unmarshal(record, item) != nil {
			continue
		}
		items = append(items, item)
		at = append(at, i)
	}
	if len(items) == 0 {
		return len(records), nil
	}
	settled, err := call(items)
	if err != nil {
		return 0, err
	}
	if uint64(settled) >= uint64(len(items)) {
		return len(records), nil
	}
	return at[settled], nil
}
