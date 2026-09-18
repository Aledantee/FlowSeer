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
	// HubEdgeStreamPrefix starts the name of the hub stream that sources one
	// edge's buffer; the edge id follows. One stream per edge is what makes
	// a record's edge a fact of where it is stored rather than of the
	// subject it carries.
	HubEdgeStreamPrefix = "FLOWSEER_EDGE_"
	// AuditStream holds every DeviceOperationEvent central writes.
	AuditStream = "FLOWSEER_DEVICE_AUDIT"
	// LaneBucket is the key-value bucket the device service's lane records
	// live in, one key per device.
	LaneBucket = "device-lanes"
	// EdgeBucket is the key-value bucket the device service's edge records
	// live in, one key per edge.
	EdgeBucket = "edges"
	// CapturesBucket is the key-value bucket the device service's capture
	// session records live in, one key per session.
	CapturesBucket = "captures"
	// HubDomain is the hub's JetStream domain. An edge's leaf runs its own
	// domain; a leaf without one silently extends the hub's.
	HubDomain = "hub"
)

// OTelSignal is one of the three OpenTelemetry signals the receiver accepts
// and the forwarder ships.
type OTelSignal string

// The three OpenTelemetry signals, spelled as the subject token each takes.
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

// EdgePublishSubjects maps the logical names an edge's leaf node knows to the
// concrete subjects it publishes on. The vocabulary lives here because the
// module that builds the leaf node owns it; AttachBus hands the map on
// unchanged and chooses none of it.
func EdgePublishSubjects(tenant, edgeID string) map[string]string {
	subjects := make(map[string]string, 3)
	for _, signal := range []OTelSignal{SignalLogs, SignalMetrics, SignalTraces} {
		subjects["otel."+string(signal)] = OTelSubject(tenant, edgeID, signal)
	}
	return subjects
}

// AuditSubject is where central writes the audit record of one device.
func AuditSubject(tenant, deviceID string) string {
	return fmt.Sprintf("flowseer.%s.audit.device.%s", tenant, deviceID)
}

// HubEdgeStream names the hub stream that sources one edge's buffer.
func HubEdgeStream(edgeID string) string {
	return HubEdgeStreamPrefix + edgeID
}

// edgeOfHubStream reads the edge id back out of a hub edge stream's name;
// ok is false for any other stream.
func edgeOfHubStream(name string) (string, bool) {
	if !strings.HasPrefix(name, HubEdgeStreamPrefix) || len(name) == len(HubEdgeStreamPrefix) {
		return "", false
	}
	return strings.TrimPrefix(name, HubEdgeStreamPrefix), true
}

// belongsToEdge reports whether subject lies under the edge's own subtree.
func belongsToEdge(tenant, edgeID, subject string) bool {
	return strings.HasPrefix(subject, EdgeSubtree(tenant, edgeID)+".")
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
