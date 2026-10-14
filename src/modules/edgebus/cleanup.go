package edgebus

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeCleanup identifies a cleanup failure. A membership or configuration
// refusal leaves every stored record untouched.
var ErrCodeCleanup = errs.NewCode("edgebus/cleanup")

// IngestConsumers returns the closed set of durable consumers that must resolve
// every record before cleanup. Unknown record types have no members.
func IngestConsumers(recordType string) []string {
	if recordType == IngestRecordTypeSyslog {
		return []string{"history_sink"}
	}
	return nil
}

// IngestConsumerConfig declares an unfiltered, unlimited-delivery member.
// Code must create members with this config and never repair them in place.
func IngestConsumerConfig(name string) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable: name, DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy: jetstream.AckExplicitPolicy, MaxDeliver: -1,
		AckWait: 2 * time.Minute, MaxAckPending: 10000,
	}
}

// EnsureIngestConsumer creates a named member or returns its existing matching
// incarnation. An unknown name or differing config returns [ErrCodeCleanup]
// without changing the consumer. It is safe for concurrent use.
func EnsureIngestConsumer(ctx context.Context, js jetstream.JetStream, recordType, name string) (jetstream.Consumer, error) {
	if !slices.Contains(IngestConsumers(recordType), name) {
		return nil, errs.New().Code(ErrCodeCleanup).Msg("unknown ingest consumer")
	}
	stream, err := js.Stream(ctx, IngestStream(recordType))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeCleanup).Msg("load ingest stream")
	}
	consumer, err := stream.CreateConsumer(ctx, IngestConsumerConfig(name))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeCleanup).Msg("create ingest member")
	}
	return consumer, nil
}

// CleanReport describes the state read before cleanup. Pending sums undelivered
// and unacknowledged records across members, so multiple members can count a
// record more than once. Purged means a prefix request succeeded, not a loss count.
type CleanReport struct {
	Floor         uint64
	FirstSequence uint64
	LastSequence  uint64
	Bytes         uint64
	MaxBytes      int64
	FirstTime     time.Time
	Pending       uint64
	Purged        bool
}

// CleanIngest removes only the prefix every named member resolved. Missing,
// differing, or stray consumers return [ErrCodeCleanup] without purging.
// Concurrent passes are safe because each purges below a resolved floor.
func CleanIngest(ctx context.Context, js jetstream.JetStream, recordType string) (CleanReport, error) {
	stream, err := js.Stream(ctx, IngestStream(recordType))
	if err != nil {
		return CleanReport{}, errs.From(err).Code(ErrCodeCleanup).Msg("load ingest stream")
	}
	return cleanIngest(ctx, stream, IngestConsumers(recordType), nil)
}

func cleanIngest(ctx context.Context, stream jetstream.Stream, members []string, beforePurge func()) (CleanReport, error) {
	report := CleanReport{Floor: math.MaxUint64}
	seen := make(map[string]bool, len(members))
	listing := stream.ListConsumers(ctx)
	valid := len(members) > 0
	for info := range listing.Info() {
		cfg := info.Config
		want := IngestConsumerConfig(info.Name)
		if !slices.Contains(members, info.Name) || seen[info.Name] ||
			cfg.Durable != want.Durable || cfg.FilterSubject != "" || len(cfg.FilterSubjects) != 0 ||
			cfg.DeliverPolicy != want.DeliverPolicy || cfg.AckPolicy != want.AckPolicy || cfg.MaxDeliver != want.MaxDeliver {
			valid = false
		}
		seen[info.Name] = true
		report.Floor = min(report.Floor, info.AckFloor.Stream)
		report.Pending += info.NumPending + uint64(info.NumAckPending)
	}
	if err := listing.Err(); err != nil {
		return CleanReport{}, errs.From(err).Code(ErrCodeCleanup).Msg("list ingest consumers")
	}
	if !valid || len(seen) != len(members) {
		return CleanReport{}, errs.New().Code(ErrCodeCleanup).Msg("ingest consumer membership or config differs")
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return CleanReport{}, errs.From(err).Code(ErrCodeCleanup).Msg("read ingest state")
	}
	report.FirstSequence, report.LastSequence = info.State.FirstSeq, info.State.LastSeq
	report.Bytes, report.MaxBytes, report.FirstTime = info.State.Bytes, info.Config.MaxBytes, info.State.FirstTime
	// Sequence zero means purge everything to the client. A zero floor must
	// never be converted to a whole-stream request.
	if report.Floor == 0 || report.Floor < report.FirstSequence {
		return report, nil
	}
	if beforePurge != nil {
		beforePurge()
	}
	if err := stream.Purge(ctx, jetstream.WithPurgeSequence(report.Floor+1)); err != nil {
		return CleanReport{}, errs.From(err).Code(ErrCodeCleanup).Msg("purge resolved ingest prefix")
	}
	report.Purged = true
	return report, nil
}

// IsStreamFull distinguishes the byte-capacity refusal from other JetStream
// store failures, including wrapped errors.
func IsStreamFull(err error) bool {
	var api *jetstream.APIError
	return errors.As(err, &api) && api.ErrorCode == 10077 && api.Description == "maximum bytes exceeded"
}
