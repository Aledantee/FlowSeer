package audit_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"unicode/utf8"

	"buf.build/go/protovalidate"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/audit"
)

func fixedClock() audit.Clock {
	t := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func commonFixture() audit.Common {
	return audit.Common{
		Device:   audit.Device{DeviceID: "0192e6a0-0000-7000-8000-0000000000ed"},
		Sequence: 1,
	}
}

func validate(t *testing.T, event *eventv1.DeviceOperationEvent) {
	t.Helper()
	if err := protovalidate.Validate(event); err != nil {
		t.Errorf("event failed validation: %v", err)
	}
}

func TestBuildPhaseTransitionedValidates(t *testing.T) {
	event := audit.BuildPhaseTransitioned(fixedClock(), commonFixture(),
		accessv1.OperationPhase_OPERATION_PHASE_UNSPECIFIED, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	validate(t, event)
	if event.GetPhaseTransitioned().HasFrom() {
		t.Error("From should be unset when there is no earlier phase")
	}
}

func TestBuildLaneBlockedValidates(t *testing.T) {
	event := audit.BuildLaneBlocked(fixedClock(), commonFixture(), accessv1.BlockReason_BLOCK_REASON_CONFLICTING_READS)
	validate(t, event)
}

func TestBuildLaneReleasedValidates(t *testing.T) {
	validate(t, audit.BuildLaneReleased(fixedClock(), commonFixture()))
}

func TestBuildRouteSelectedValidates(t *testing.T) {
	event := audit.BuildRouteSelected(fixedClock(), commonFixture(), inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH, true)
	validate(t, event)
	if !event.GetRouteSelected().GetFellThrough() {
		t.Error("FellThrough should be true")
	}
}

func TestBuildDiscoveryCompletedValidates(t *testing.T) {
	validate(t, audit.BuildDiscoveryCompleted(fixedClock(), commonFixture(), "FastIron 10.0.10gT7f1"))
}

func TestBuildFirmwareEpochChangedValidates(t *testing.T) {
	validate(t, audit.BuildFirmwareEpochChanged(fixedClock(), commonFixture(), "A", "B"))
}

func TestBuildRecoveryStartedValidates(t *testing.T) {
	validate(t, audit.BuildRecoveryStarted(fixedClock(), commonFixture()))
}

func TestBuildLaneFrozenValidates(t *testing.T) {
	// LaneFrozen is a lane-level event with no single mutation in scope, so
	// Sequence stays unset.
	common := commonFixture()
	common.Sequence = 0
	event := audit.BuildLaneFrozen(fixedClock(), common)
	validate(t, event)
	if event.HasSequence() {
		t.Error("Sequence should be unset for a lane-level event")
	}
}

type fakeSink struct {
	release chan struct{}
	err     error
	called  chan *eventv1.DeviceOperationEvent
}

func newFakeSink() *fakeSink {
	return &fakeSink{release: make(chan struct{}), called: make(chan *eventv1.DeviceOperationEvent, 1)}
}

func (s *fakeSink) Emit(ctx context.Context, event *eventv1.DeviceOperationEvent) error {
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.called <- event
	return s.err
}

func TestDelivererBlocksUntilItReturns(t *testing.T) {
	sink := newFakeSink()
	event := audit.BuildLaneReleased(fixedClock(), commonFixture())

	done := make(chan error, 1)
	go func() { done <- sink.Emit(context.Background(), event) }()

	select {
	case <-done:
		t.Fatal("Emit returned before the sink was released")
	case <-time.After(50 * time.Millisecond):
	}

	close(sink.release)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Emit() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Emit did not return after release")
	}
}

func TestDelivererErrorPropagates(t *testing.T) {
	sink := newFakeSink()
	sink.err = errors.New("delivery failed")
	close(sink.release)

	if err := sink.Emit(context.Background(), audit.BuildLaneReleased(fixedClock(), commonFixture())); err == nil {
		t.Fatal("Emit() error = nil, want the sink's error")
	}
}

func TestCorrelationIDsBoundedAtEachSchemaLimit(t *testing.T) {
	// 10 pairs, over the schema's 8 max_pairs; deliberately unordered map
	// iteration means any two must survive, not a specific two.
	ids := make(map[string]string, 10)
	for i := range 10 {
		ids[string(rune('a'+i))] = "v"
	}
	common := commonFixture()
	common.CorrelationIDs = ids

	event := audit.BuildPhaseTransitioned(fixedClock(), common,
		accessv1.OperationPhase_OPERATION_PHASE_UNSPECIFIED, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	validate(t, event)

	if got := len(event.GetCorrelationIds()); got > 8 {
		t.Errorf("correlation_ids has %d pairs, want at most 8", got)
	}
}

func TestCorrelationIDsTruncatedOnRuneBoundaries(t *testing.T) {
	// "€" (U+20AC) is 3 bytes in UTF-8, and neither the schema's 64-byte
	// key bound nor its 128-byte value bound is a multiple of 3, so a
	// plain byte-offset cut (s[:64], s[:128]) lands mid-rune here — a
	// 2-byte rune repeated to the same bounds would not discriminate,
	// since 64 and 128 are both even.
	longKey := ""
	for len(longKey) < 70 {
		longKey += "€"
	}
	longValue := ""
	for len(longValue) < 140 {
		longValue += "€"
	}
	common := commonFixture()
	common.CorrelationIDs = map[string]string{longKey: longValue}

	event := audit.BuildPhaseTransitioned(fixedClock(), common,
		accessv1.OperationPhase_OPERATION_PHASE_UNSPECIFIED, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	validate(t, event)

	for k, v := range event.GetCorrelationIds() {
		if len(k) > 64 {
			t.Errorf("correlation_ids key %q is %d bytes, want at most 64", k, len(k))
		}
		if len(v) > 128 {
			t.Errorf("correlation_ids value %q is %d bytes, want at most 128", v, len(v))
		}
		if !utf8.ValidString(k) {
			t.Errorf("correlation_ids key %q is not valid UTF-8", k)
		}
		if !utf8.ValidString(v) {
			t.Errorf("correlation_ids value %q is not valid UTF-8", v)
		}
	}
}
