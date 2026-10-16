package edgebus

import (
	"fmt"
	"strings"

	"go.aledante.io/FlowSeer/src/common/tenant"
)

const (
	// DefaultTenant is the default tenant identifier used during development
	// and testing when no ambient tenant is configured.
	DefaultTenant = tenant.DefaultTenant

	// AuditStream holds every DeviceOperationEvent central writes.
	AuditStream = "FLOWSEER_DEVICE_AUDIT"
	// OperatorActionStream holds every OperatorActionEvent central writes for
	// an operator change.
	OperatorActionStream = "FLOWSEER_OPERATOR_ACTIONS"
	// OperatorReadStream holds every OperatorActionEvent central writes for an
	// operator view. Views have their own byte limit so that a flood of them
	// cannot push a change out of OperatorActionStream.
	OperatorReadStream = "FLOWSEER_OPERATOR_READS"
	// EvidenceStream holds raw evidence moved out of ingested records.
	EvidenceStream = "FLOWSEER_INGEST_EVIDENCE"
	// IngestRecordTypeSyslog identifies records produced by the syslog source.
	IngestRecordTypeSyslog = "syslog"
	// LaneBucket is the key-value bucket the device service's lane records
	// live in, one key per device.
	LaneBucket = "device-lanes"
	// EdgeBucket is the key-value bucket the device service's edge records
	// live in, one key per edge.
	EdgeBucket = "edges"
	// CapturesBucket is the key-value bucket the device service's capture
	// session records live in, one key per session.
	CapturesBucket = "captures"
	// RawWindowsBucket is the key-value bucket the device service's raw window
	// records live in, one key per tenant and edge: <tenant>.<edge-id>.
	RawWindowsBucket = "raw-windows"
	// TenantBucket is the key-value bucket central keeps tenant records in.
	TenantBucket = "tenants"
	// AccessBucket is the key-value bucket central keeps member, role, and
	// partner records in. Every key starts with its tenant id.
	AccessBucket = "access"
	// HubDomain is the hub's JetStream domain.
	HubDomain = "hub"
)

// OTelSignal is one of the three OpenTelemetry signals the collector posts.
type OTelSignal string

// The three OpenTelemetry signals, spelled as the subject token each takes.
const (
	SignalLogs    OTelSignal = "logs"
	SignalMetrics OTelSignal = "metrics"
	SignalTraces  OTelSignal = "traces"
)

// IngestRecordTypes returns the record types central stores.
func IngestRecordTypes() []string {
	return []string{IngestRecordTypeSyslog}
}

// CentralIngestSubject is where central stores one typed ingested record.
func CentralIngestSubject(tenant, recordType, deviceID string) string {
	return fmt.Sprintf("flowseer.%s.ingest.%s.%s", tenant, recordType, deviceID)
}

// EvidenceSubject is where central stores raw evidence for one ingested record.
func EvidenceSubject(tenant, recordType, deviceID string) string {
	return fmt.Sprintf("flowseer.%s.evidence.%s.%s", tenant, recordType, deviceID)
}

// IngestStream names the central stream for one ingested record type.
func IngestStream(recordType string) string {
	return "FLOWSEER_INGEST_" + strings.ToUpper(recordType)
}

// AuditSubject is where central writes the audit record of one device.
func AuditSubject(tenant, deviceID string) string {
	return fmt.Sprintf("flowseer.%s.audit.device.%s", tenant, deviceID)
}

// OperatorActionSubject is where central writes the audit record of one operator
// change. A call with no admitted tenant passes the token platform, which no
// tenant id can equal.
func OperatorActionSubject(tenant, action string) string {
	return fmt.Sprintf("flowseer.%s.operator.action.%s", tenant, action)
}

// OperatorReadSubject is where central writes the audit record of one operator view.
func OperatorReadSubject(tenant, action string) string {
	return fmt.Sprintf("flowseer.%s.operator.read.%s", tenant, action)
}
