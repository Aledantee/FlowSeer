// Package intake validates delivered records and publishes them into central ingestion streams.
package intake

import (
	"context"
	"errors"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"google.golang.org/protobuf/proto"

	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// ErrCodeStart identifies a failure to build the delivery handler.
var ErrCodeStart = errs.NewCode("intake/start")

const (
	scopeName               = "go.aledante.io/FlowSeer/src/services/device/internal/intake"
	reasonInvalid           = "invalid"
	reasonForeignProvenance = "foreign_provenance"
	reasonStreamFull        = "stream_full"
	reasonPublishFailed     = "publish_failed"
	refusalLogInterval      = 10 * time.Second
	refusalLogKey           = "refusal"
	retryLogKey             = "retry"
)

// Publisher is the central JetStream publishing operation intake uses.
type Publisher interface {
	Publish(context.Context, string, []byte, ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// allowEvery reports whether key last logged at least refusalLogInterval ago,
// and records now as its last time when it did. last is guarded by mu.
func allowEvery(mu *sync.Mutex, last map[string]time.Time, key string) bool {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	if now.Sub(last[key]) < refusalLogInterval {
		return false
	}
	last[key] = now
	return true
}

type publication struct {
	subject    string
	data       []byte
	messageID  string
	recordType string
	evidence   bool
}

// prepareRecord validates a decoded record against its delivering edge and tenant.
// data is the original encoding retained by the evidence publication. The typed
// publication comes first so a full typed stream refuses before evidence is stored.
func prepareRecord(tenantID, edgeID string, record *ingestv1.IngestRecord, data []byte, validate func(proto.Message) error) ([]publication, string, string) {
	if err := validate(record); err != nil {
		var validationErr *protovalidate.ValidationError
		if !errors.As(err, &validationErr) {
			return nil, "", "validation"
		}
		return nil, reasonInvalid, ""
	}
	if record.GetProvenance().GetEdge().GetEdge().GetId() != edgeID {
		return nil, reasonForeignProvenance, ""
	}

	recordType, deviceID := recordTypeAndDevice(record)
	if recordType == "" || deviceID == "" {
		return nil, reasonInvalid, ""
	}
	messageID := tenantID + "." + record.GetRecordId()
	typed := data
	var evidence *publication
	if record.GetRaw() != nil {
		evidence = &publication{
			subject: edgebus.EvidenceSubject(tenantID, recordType, deviceID), data: data,
			messageID: messageID, recordType: recordType, evidence: true,
		}
		withoutRaw := proto.Clone(record).(*ingestv1.IngestRecord)
		withoutRaw.ClearRaw()
		encoded, err := proto.Marshal(withoutRaw)
		if err != nil {
			return nil, "", "marshal"
		}
		typed = encoded
	}
	publications := make([]publication, 0, 2)
	publications = append(publications, publication{
		subject: edgebus.CentralIngestSubject(tenantID, recordType, deviceID), data: typed,
		messageID: messageID, recordType: recordType,
	})
	if evidence != nil {
		publications = append(publications, *evidence)
	}
	return publications, "", ""
}

func recordTypeAndDevice(record *ingestv1.IngestRecord) (string, string) {
	if syslog := record.GetSyslog(); syslog != nil {
		return edgebus.IngestRecordTypeSyslog, syslog.GetDevice().GetDevice().GetId()
	}
	return "", ""
}

type intakeMetrics struct {
	republished    metric.Int64Counter
	duplicate      metric.Int64Counter
	evidenceStored metric.Int64Counter
	refused        metric.Int64Counter
	retried        metric.Int64Counter
	duration       metric.Float64Histogram
}

func newMetrics(provider metric.MeterProvider) (intakeMetrics, error) {
	meter := provider.Meter(scopeName, metric.WithSchemaURL(semconv.SchemaURL))
	republished, err := meter.Int64Counter("flowseer.intake.records.republished", metric.WithUnit("{record}"), metric.WithDescription("Records acknowledged after central republish"))
	if err != nil {
		return intakeMetrics{}, err
	}
	duplicate, err := meter.Int64Counter("flowseer.intake.records.duplicate", metric.WithUnit("{record}"), metric.WithDescription("Central publish acknowledgements marked duplicate"))
	if err != nil {
		return intakeMetrics{}, err
	}
	evidenceStored, err := meter.Int64Counter("flowseer.intake.evidence.stored", metric.WithUnit("{record}"), metric.WithDescription("Raw evidence publish acknowledgements"))
	if err != nil {
		return intakeMetrics{}, err
	}
	refused, err := meter.Int64Counter("flowseer.intake.records.refused", metric.WithUnit("{record}"), metric.WithDescription("Records settled after an intake refusal"))
	if err != nil {
		return intakeMetrics{}, err
	}
	retried, err := meter.Int64Counter("flowseer.intake.records.retried", metric.WithUnit("{record}"), metric.WithDescription("Records left unsettled for a later delivery call"))
	if err != nil {
		return intakeMetrics{}, err
	}
	duration, err := meter.Float64Histogram("flowseer.intake.record.duration", metric.WithUnit("s"), metric.WithDescription("Time from delivery to the final outcome of a record"))
	if err != nil {
		return intakeMetrics{}, err
	}
	return intakeMetrics{
		republished: republished, duplicate: duplicate, evidenceStored: evidenceStored,
		refused: refused, retried: retried, duration: duration,
	}, nil
}
