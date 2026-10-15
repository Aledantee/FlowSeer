package conformance

import (
	"bytes"
	"testing"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	telemetryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
)

func TestIngestDeliverRequestRules(t *testing.T) {
	invalidRecord := ingestv1.IngestRecord_builder{}.Build()

	runValidationCases(t, []validationCase{
		{name: "delivery carries one record", message: edgeingestv1.DeliverRequest_builder{Records: []*ingestv1.IngestRecord{baseIngestRecord().Build()}}.Build(), wantValid: true},
		{name: "delivery without records is rejected", message: edgeingestv1.DeliverRequest_builder{}.Build()},
		{
			name:      "an invalid record does not refuse the batch",
			message:   edgeingestv1.DeliverRequest_builder{Records: []*ingestv1.IngestRecord{baseIngestRecord().Build(), invalidRecord}}.Build(),
			wantValid: true,
		},
	})
}

func TestTelemetryDeliverRequestRules(t *testing.T) {
	export := func(signal telemetryv1.TelemetrySignal, body []byte) *telemetryv1.TelemetryExport {
		return telemetryv1.TelemetryExport_builder{Signal: signal.Enum(), Body: body}.Build()
	}
	logs := telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS

	runValidationCases(t, []validationCase{
		{name: "delivery carries one export", message: telemetryv1.DeliverRequest_builder{Exports: []*telemetryv1.TelemetryExport{export(logs, []byte("x"))}}.Build(), wantValid: true},
		{name: "delivery without exports is rejected", message: telemetryv1.DeliverRequest_builder{}.Build()},
		{
			name:      "an invalid export does not refuse the batch",
			message:   telemetryv1.DeliverRequest_builder{Exports: []*telemetryv1.TelemetryExport{export(logs, []byte("x")), export(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_UNSPECIFIED, nil)}}.Build(),
			wantValid: true,
		},
		{name: "an export with the largest body is valid", message: export(logs, bytes.Repeat([]byte("a"), 261120)), wantValid: true},
		{name: "an export over the body bound is rejected", message: export(logs, bytes.Repeat([]byte("a"), 261121))},
		{name: "an export without a signal is rejected", message: export(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_UNSPECIFIED, []byte("x"))},
		{name: "an export with an undefined signal is rejected", message: export(telemetryv1.TelemetrySignal(99), []byte("x"))},
		{name: "an export without a body is rejected", message: export(logs, nil)},
	})
}
