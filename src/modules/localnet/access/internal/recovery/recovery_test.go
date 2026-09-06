package recovery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/audit"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/freeze"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/mutation"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/recovery"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

type noopDeliverer struct{}

func (noopDeliverer) Emit(context.Context, *eventv1.DeviceOperationEvent) error { return nil }

// failsOnLaneBlockedDeliverer fails only for a LaneBlocked event whose
// reason is RECOVERY_HOLD, so PhaseTransitioned, EnterRecovering's own
// INDETERMINATE LaneBlocked, and every other event this test's setup
// needs deliver normally, and only Abandon's own trailing block() call
// sees the simulated outage.
type failsOnLaneBlockedDeliverer struct{}

func (failsOnLaneBlockedDeliverer) Emit(_ context.Context, event *eventv1.DeviceOperationEvent) error {
	if event.GetLaneBlocked().GetReason() == accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD {
		return errors.New("audit delivery unavailable")
	}
	return nil
}

func observation(description string) *accessv1.InterfaceObservation {
	return observationOf("ethernet 1/1/1", description)
}

func observationOf(interfaceName, description string) *accessv1.InterfaceObservation {
	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName(interfaceName)
	obs.SetDescription(description)
	obs.SetAdminStatus(interfacev1.AdminStatus_ADMIN_STATUS_UP)
	obs.SetOperStatus(interfacev1.OperStatus_OPER_STATUS_UP)
	obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)
	return obs
}

func recoveringMachine(t *testing.T, reads ...*accessv1.InterfaceObservation) *mutation.Machine {
	t.Helper()
	return recoveringMachineWithDeliverer(t, noopDeliverer{}, reads...)
}

func recoveringMachineWithDeliverer(t *testing.T, deliverer audit.Deliverer, reads ...*accessv1.InterfaceObservation) *mutation.Machine {
	t.Helper()

	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}

	i := 0
	deps := mutation.Deps{
		CurrentFingerprint: "fw-A",
		DeviceID:           "0192e6a0-0000-7000-8000-0000000000ed",
		Freeze:             freeze.New(view),
		Audit:              deliverer,
		Telemetry:          view,
		Clock:              time.Now,
		Read: func(context.Context) (*accessv1.InterfaceObservation, error) {
			obs := reads[i]
			if i < len(reads)-1 {
				i++
			}
			return obs, nil
		},
	}

	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName("ethernet 1/1/1")
	change.SetDescription("uplink to core")
	intent := &accessv1.MutationIntent{}
	intent.SetExpectedFirmwareFingerprint("fw-A")
	intent.SetInterfaceDescription(change)
	req := &integrationv1.ExecuteRequest{}
	req.SetSequence(1)
	req.SetMutation(intent)

	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(1)
	if _, err := m.Checkpoint(context.Background(), checkpointReq); err != nil {
		t.Fatalf("Checkpoint() error: %v", err)
	}
	if err := m.EnterRecovering(context.Background()); err != nil {
		t.Fatalf("EnterRecovering() error: %v", err)
	}
	return m
}

func TestObserveAlwaysPrecedesARetryDecision(t *testing.T) {
	var observed bool
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	deps := mutation.Deps{
		CurrentFingerprint: "fw-A",
		DeviceID:           "device",
		Freeze:             freeze.New(view),
		Audit:              noopDeliverer{},
		Telemetry:          view,
		Clock:              time.Now,
		Read: func(context.Context) (*accessv1.InterfaceObservation, error) {
			observed = true
			return observation("pre"), nil
		},
	}
	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName("ethernet 1/1/1")
	change.SetDescription("post")
	intent := &accessv1.MutationIntent{}
	intent.SetExpectedFirmwareFingerprint("fw-A")
	intent.SetInterfaceDescription(change)
	req := &integrationv1.ExecuteRequest{}
	req.SetSequence(1)
	req.SetMutation(intent)
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(1)
	if _, err := m.Checkpoint(context.Background(), checkpointReq); err != nil {
		t.Fatalf("Checkpoint() error: %v", err)
	}
	if err := m.EnterRecovering(context.Background()); err != nil {
		t.Fatalf("EnterRecovering() error: %v", err)
	}

	fenced := func(context.Context) (bool, error) { return true, nil }
	runner := recovery.New(m, fenced, interfaces.DelayedEffect{Horizon: time.Minute}, time.Second, time.Now, nil)

	outcome, _, err := runner.Attempt(context.Background(), time.Now(), observation("pre"))
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if !observed {
		t.Fatal("Attempt authorized a retry without calling Observe first")
	}
	if outcome != recovery.OutcomeRetry {
		t.Errorf("Attempt() = %v, want OutcomeRetry (fenced)", outcome)
	}
}

func TestFenceAuthorizesImmediateRetry(t *testing.T) {
	m := recoveringMachine(t, observation("pre"))
	fenced := func(context.Context) (bool, error) { return true, nil }
	runner := recovery.New(m, fenced, interfaces.DelayedEffect{Horizon: time.Minute}, time.Second, time.Now, nil)

	outcome, _, err := runner.Attempt(context.Background(), time.Now(), observation("pre"))
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeRetry {
		t.Fatalf("Attempt() = %v, want OutcomeRetry", outcome)
	}
}

func TestTwoCorroboratingObservationsAcrossTheHorizonPermitRetry(t *testing.T) {
	pre := observation("pre")
	m := recoveringMachine(t, pre, pre)

	now := time.Now()
	clock := func() time.Time { return now }
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Minute}, 5*time.Second, clock, nil)
	since := now

	outcome, _, err := runner.Attempt(context.Background(), since, pre)
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeContinueObserving {
		t.Fatalf("first Attempt() = %v, want OutcomeContinueObserving (only one corroboration so far)", outcome)
	}

	now = now.Add(10 * time.Second)
	outcome, _, err = runner.Attempt(context.Background(), since, pre)
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeRetry {
		t.Fatalf("second Attempt() = %v, want OutcomeRetry (two corroborating observations)", outcome)
	}

	// The corroboration count must reset once it has authorized a retry:
	// a third Attempt at the same unchanged state, before two fresh
	// corroborating observations have accumulated again, must not
	// authorize another retry — "two corroborating observations" permits
	// exactly one retry, never an unbounded stream of them for the rest
	// of the horizon.
	now = now.Add(10 * time.Second)
	outcome, _, err = runner.Attempt(context.Background(), since, pre)
	if err != nil {
		t.Fatalf("third Attempt() error: %v", err)
	}
	if outcome == recovery.OutcomeRetry {
		t.Fatal("third Attempt() = OutcomeRetry, want the corroboration count to have reset after authorizing the first retry")
	}
}

// TestPersistentFenceStillAbandonsOnceTheHorizonElapses proves the horizon
// is checked before the fence: a Fenced that stays true forever must not
// be able to authorize retries past the horizon with no bound at all.
func TestPersistentFenceStillAbandonsOnceTheHorizonElapses(t *testing.T) {
	m := recoveringMachine(t, observation("pre"))
	fenced := func(context.Context) (bool, error) { return true, nil }

	now := time.Now()
	clock := func() time.Time { return now }
	runner := recovery.New(m, fenced, interfaces.DelayedEffect{Horizon: time.Minute}, time.Second, clock, nil)
	since := now

	outcome, _, err := runner.Attempt(context.Background(), since, observation("pre"))
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeRetry {
		t.Fatalf("Attempt() before the horizon elapses = %v, want OutcomeRetry", outcome)
	}

	now = now.Add(2 * time.Minute)
	outcome, _, err = runner.Attempt(context.Background(), since, observation("pre"))
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeAbandoned {
		t.Fatalf("Attempt() after the horizon elapses = %v, want OutcomeAbandoned even though Fenced still returns true", outcome)
	}
}

func TestOneStaleObservationDoesNotPermitRetry(t *testing.T) {
	pre := observation("pre")
	changed := observation("something else entirely")
	m := recoveringMachine(t, pre, changed)

	now := time.Now()
	clock := func() time.Time { return now }
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Minute}, 5*time.Second, clock, nil)
	since := now

	if _, _, err := runner.Attempt(context.Background(), since, pre); err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}

	now = now.Add(10 * time.Second)
	outcome, _, err := runner.Attempt(context.Background(), since, pre)
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome == recovery.OutcomeRetry {
		t.Fatal("Attempt() authorized a retry after only one corroborating observation (the second disagreed)")
	}
}

func TestHorizonElapsedWithNoCorroborationAbandons(t *testing.T) {
	changed := observation("something else entirely")
	m := recoveringMachine(t, changed)

	now := time.Now()
	clock := func() time.Time { return now }
	hold := &recovery.Hold{}
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Minute}, 5*time.Second, clock, hold)
	since := now
	now = now.Add(2 * time.Minute)

	outcome, _, err := runner.Attempt(context.Background(), since, observation("pre"))
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeAbandoned {
		t.Fatalf("Attempt() = %v, want OutcomeAbandoned", outcome)
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
		t.Errorf("Phase() = %v, want ABANDONED", got)
	}
	// Abandonment with no engaged Hold leaves the device's lane free to
	// admit the next mutation over an effect recovery could not establish
	// — the Runner must engage the caller's Hold itself, since nothing
	// else in this package's own return value does.
	if !hold.Active() {
		t.Error("Hold was not engaged after OutcomeAbandoned")
	}
}

// TestAbandonEngagesHoldEvenWhenTheBlockedAuditDeliveryFails proves the
// Hold is engaged from the machine's own durable phase, not from Abandon's
// return value: Abandon's trailing block() call can fail purely on its own
// LaneBlocked audit delivery after the phase and block reason are already
// durably ABANDONED, and the Hold must still be engaged in that case, or a
// degraded audit link would leave an abandoned mutation with no Hold and
// Submit admitting the next mutation over its unresolved effect.
func TestAbandonEngagesHoldEvenWhenTheBlockedAuditDeliveryFails(t *testing.T) {
	changed := observation("something else entirely")
	m := recoveringMachineWithDeliverer(t, failsOnLaneBlockedDeliverer{}, changed)

	now := time.Now()
	clock := func() time.Time { return now }
	hold := &recovery.Hold{}
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Minute}, 5*time.Second, clock, hold)
	since := now
	now = now.Add(2 * time.Minute)

	outcome, _, err := runner.Attempt(context.Background(), since, observation("pre"))
	if err == nil {
		t.Fatal("Attempt() error = nil, want the LaneBlocked delivery failure surfaced")
	}
	if outcome == recovery.OutcomeAbandoned {
		t.Error("Attempt() outcome = OutcomeAbandoned, want the zero value alongside the error")
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
		t.Fatalf("Phase() = %v, want ABANDONED despite the audit delivery failure", got)
	}
	if !hold.Active() {
		t.Error("Hold was not engaged despite the machine reaching ABANDONED")
	}
}

// TestRecoveryTerminatesWhenTheDeviceStaysUnreachable proves the exact case
// recovery exists for — a device that never answers a read again — still
// reaches ABANDONED once the horizon elapses, rather than wedging at
// OBSERVING because a failed read left the phase somewhere a later Observe
// call can no longer be made from.
func TestRecoveryTerminatesWhenTheDeviceStaysUnreachable(t *testing.T) {
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	deps := mutation.Deps{
		CurrentFingerprint: "fw-A",
		DeviceID:           "device",
		Freeze:             freeze.New(view),
		Audit:              noopDeliverer{},
		Telemetry:          view,
		Clock:              time.Now,
		Read: func(context.Context) (*accessv1.InterfaceObservation, error) {
			return nil, errors.New("device unreachable")
		},
	}
	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName("ethernet 1/1/1")
	change.SetDescription("uplink to core")
	intent := &accessv1.MutationIntent{}
	intent.SetExpectedFirmwareFingerprint("fw-A")
	intent.SetInterfaceDescription(change)
	req := &integrationv1.ExecuteRequest{}
	req.SetSequence(1)
	req.SetMutation(intent)

	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(1)
	if _, err := m.Checkpoint(context.Background(), checkpointReq); err != nil {
		t.Fatalf("Checkpoint() error: %v", err)
	}
	if err := m.EnterRecovering(context.Background()); err != nil {
		t.Fatalf("EnterRecovering() error: %v", err)
	}

	now := time.Now()
	clock := func() time.Time { return now }
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Minute}, 5*time.Second, clock, nil)
	since := now

	// First attempt: the read fails. This is the ordinary case recovery
	// polls through, not a fatal error the caller must handle specially.
	outcome, _, err := runner.Attempt(context.Background(), since, observation("pre"))
	if err != nil {
		t.Fatalf("Attempt() error: %v, want nil — a failed read is not this call's own failure", err)
	}
	if outcome != recovery.OutcomeContinueObserving {
		t.Fatalf("Attempt() = %v, want OutcomeContinueObserving", outcome)
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_RECOVERING {
		t.Fatalf("Phase() after a failed read = %v, want RECOVERING (able to Observe again)", got)
	}

	// The horizon elapses with the device still unreachable: this must
	// reach ABANDONED, not another ErrCodeOutOfOrder from a wedged phase.
	now = now.Add(2 * time.Minute)
	outcome, _, err = runner.Attempt(context.Background(), since, observation("pre"))
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome != recovery.OutcomeAbandoned {
		t.Fatalf("Attempt() = %v, want OutcomeAbandoned", outcome)
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
		t.Errorf("Phase() = %v, want ABANDONED", got)
	}
}

// TestObservationsOfDifferentInterfacesNeverCorroborate proves a
// preMutation baseline for a different interface than the one actually
// observed cannot count as corroboration:
// interfaces.ConflictingReads deliberately reports "no conflict" for two
// observations naming different interfaces (they have nothing to conflict
// about), which recovery must not read as "unchanged."
func TestObservationsOfDifferentInterfacesNeverCorroborate(t *testing.T) {
	other := observationOf("ethernet 1/1/2", "pre")
	m := recoveringMachine(t, other, other)

	now := time.Now()
	clock := func() time.Time { return now }
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Minute}, 5*time.Second, clock, nil)
	since := now

	if _, _, err := runner.Attempt(context.Background(), since, observation("pre")); err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}

	now = now.Add(10 * time.Second)
	outcome, _, err := runner.Attempt(context.Background(), since, observation("pre"))
	if err != nil {
		t.Fatalf("Attempt() error: %v", err)
	}
	if outcome == recovery.OutcomeRetry {
		t.Fatal("Attempt() = OutcomeRetry, want observations of a different interface to never corroborate")
	}
}

func TestHoldBlocksUntilResolved(t *testing.T) {
	var hold recovery.Hold

	if hold.Active() {
		t.Fatal("a fresh Hold should not be active")
	}

	hold.Engage()
	if !hold.Active() {
		t.Fatal("Engage() did not activate the hold")
	}

	hold.Resolve()
	if hold.Active() {
		t.Fatal("Resolve() did not clear the hold")
	}
}
