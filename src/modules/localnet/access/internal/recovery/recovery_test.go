package recovery_test

import (
	"context"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/freeze"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/mutation"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/recovery"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

type noopDeliverer struct{}

func (noopDeliverer) Emit(context.Context, *eventv1.DeviceOperationEvent) error { return nil }

func observation(description string) *accessv1.InterfaceObservation {
	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName("ethernet 1/1/1")
	obs.SetDescription(description)
	obs.SetAdminStatus(interfacev1.AdminStatus_ADMIN_STATUS_UP)
	obs.SetOperStatus(interfacev1.OperStatus_OPER_STATUS_UP)
	obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)
	return obs
}

func recoveringMachine(t *testing.T, reads ...*accessv1.InterfaceObservation) *mutation.Machine {
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
		Audit:              noopDeliverer{},
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
	runner := recovery.New(m, fenced, interfaces.DelayedEffect{Horizon: time.Minute}, time.Second, time.Now)

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
	runner := recovery.New(m, fenced, interfaces.DelayedEffect{Horizon: time.Minute}, time.Second, time.Now)

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
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Minute}, 5*time.Second, clock)
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
}

func TestOneStaleObservationDoesNotPermitRetry(t *testing.T) {
	pre := observation("pre")
	changed := observation("something else entirely")
	m := recoveringMachine(t, pre, changed)

	now := time.Now()
	clock := func() time.Time { return now }
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Minute}, 5*time.Second, clock)
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
	runner := recovery.New(m, nil, interfaces.DelayedEffect{Horizon: time.Minute}, 5*time.Second, clock)
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
