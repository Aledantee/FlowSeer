package edgebus

import (
	"fmt"
	"strings"
)

// Subject names the fabric uses. Every subject an edge publishes on sits
// under its own subtree, flowseer.<tenant>.edge.<edge-id>.>, so the broker
// confines the edge by prefix; the OpenTelemetry signals take the otel
// branch and each ingestion source will take ingest.<source>.
const (
	// DefaultTenant is the one account this slice runs; the tenant token
	// carries no broker enforcement until a second account exists.
	DefaultTenant = "default"

	// EdgeBufferStream is the file-backed stream on the edge's own JetStream
	// domain holding everything the edge has published and not yet shipped.
	EdgeBufferStream = "EDGE_BUFFER"
	// HubBufferStream is the hub's aggregate of every edge's buffer, sourced
	// across the leaf links.
	HubBufferStream = "FLOWSEER_EDGE_BUFFER"
	// AuditStream holds every DeviceOperationEvent central writes.
	AuditStream = "FLOWSEER_DEVICE_AUDIT"
	// LaneBucket is the key-value bucket the device service's lane records
	// live in, one key per device.
	LaneBucket = "device-lanes"
	// EdgeBucket is the key-value bucket the device service's edge records
	// live in, one key per edge.
	EdgeBucket = "edges"
	// HubDomain is the hub's JetStream domain. An edge's leaf runs its own
	// domain; a leaf without one silently extends the hub's.
	HubDomain = "hub"
)

// OTelSignal is one of the three OpenTelemetry signals the receiver accepts
// and the forwarder ships.
type OTelSignal string

const (
	SignalLogs    OTelSignal = "logs"
	SignalMetrics OTelSignal = "metrics"
	SignalTraces  OTelSignal = "traces"
)

// EdgeSubtree is the prefix every subject an edge may publish on shares.
func EdgeSubtree(tenant, edgeID string) string {
	return fmt.Sprintf("flowseer.%s.edge.%s", tenant, edgeID)
}

// OTelSubject is where an edge publishes one signal's OTLP bodies.
func OTelSubject(tenant, edgeID string, signal OTelSignal) string {
	return EdgeSubtree(tenant, edgeID) + ".otel." + string(signal)
}

// AuditSubject is where central writes the audit record of one device.
func AuditSubject(tenant, deviceID string) string {
	return fmt.Sprintf("flowseer.%s.audit.device.%s", tenant, deviceID)
}

// EdgeDomain is the JetStream domain an edge's leaf node runs.
func EdgeDomain(edgeID string) string {
	return "edge-" + edgeID
}

// signalOf reads the signal back out of an otel subject; ok is false for a
// subject that is not one.
func signalOf(subject string) (OTelSignal, bool) {
	parts := strings.Split(subject, ".")
	if len(parts) < 2 || parts[len(parts)-2] != "otel" {
		return "", false
	}
	switch signal := OTelSignal(parts[len(parts)-1]); signal {
	case SignalLogs, SignalMetrics, SignalTraces:
		return signal, true
	default:
		return "", false
	}
}
