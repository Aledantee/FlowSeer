package interfaces_test

import (
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
)

func completeObservation(description string, admin interfacev1.AdminStatus, oper interfacev1.OperStatus) *accessv1.InterfaceObservation {
	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName("ethernet 1/1/1")
	obs.SetDescription(description)
	obs.SetAdminStatus(admin)
	obs.SetOperStatus(oper)
	obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)

	return obs
}

func partialObservation() *accessv1.InterfaceObservation {
	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName("ethernet 1/1/1")
	obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_PARTIAL)

	return obs
}

func TestSelectRoute_CompletePrimaryShortCircuits(t *testing.T) {
	primary := completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP)

	called := false
	fallback := func() (*accessv1.InterfaceObservation, error) {
		called = true

		return nil, nil
	}

	got, route, err := interfaces.SelectRoute(primary, fallback)
	if err != nil {
		t.Fatalf("SelectRoute: %v", err)
	}

	if called {
		t.Error("fallback was called even though primary was complete")
	}

	if got != primary {
		t.Error("SelectRoute did not return the primary observation")
	}

	if route != interfaces.RouteSNMP {
		t.Errorf("route = %v, want RouteSNMP", route)
	}
}

func TestSelectRoute_PartialPrimaryFallsThrough(t *testing.T) {
	fallbackObs := completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP)

	called := false
	fallback := func() (*accessv1.InterfaceObservation, error) {
		called = true

		return fallbackObs, nil
	}

	got, route, err := interfaces.SelectRoute(partialObservation(), fallback)
	if err != nil {
		t.Fatalf("SelectRoute: %v", err)
	}

	if !called {
		t.Error("fallback was not called for a partial primary")
	}

	if got != fallbackObs {
		t.Error("SelectRoute did not return the fallback observation")
	}

	if route != interfaces.RouteSSH {
		t.Errorf("route = %v, want RouteSSH", route)
	}
}

func TestSelectRoute_NoFallbackErrors(t *testing.T) {
	if _, _, err := interfaces.SelectRoute(partialObservation(), nil); err == nil {
		t.Error("SelectRoute did not error with an incomplete primary and no fallback")
	}
}

func TestSelectRoute_FallbackErrorPropagates(t *testing.T) {
	wantErr := errFallback

	_, _, err := interfaces.SelectRoute(partialObservation(), func() (*accessv1.InterfaceObservation, error) {
		return nil, wantErr
	})
	if err == nil {
		t.Fatal("SelectRoute did not propagate the fallback's error")
	}
}

func TestConflictingReads(t *testing.T) {
	tests := []struct {
		name         string
		a, b         *accessv1.InterfaceObservation
		wantConflict bool
		wantField    string
	}{
		{
			name:         "agree",
			a:            completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP),
			b:            completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP),
			wantConflict: false,
		},
		{
			name:         "description disagrees",
			a:            completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP),
			b:            completeObservation("downlink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP),
			wantConflict: true,
			wantField:    "description",
		},
		{
			name:         "admin status disagrees",
			a:            completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP),
			b:            completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_DOWN, interfacev1.OperStatus_OPER_STATUS_UP),
			wantConflict: true,
			wantField:    "admin_status",
		},
		{
			name:         "oper status disagrees",
			a:            completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP),
			b:            completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_DOWN),
			wantConflict: true,
			wantField:    "oper_status",
		},
		{
			name:         "one partial is never a conflict",
			a:            completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP),
			b:            partialObservation(),
			wantConflict: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conflict, field := interfaces.ConflictingReads(tt.a, tt.b)
			if conflict != tt.wantConflict {
				t.Errorf("conflict = %v, want %v", conflict, tt.wantConflict)
			}

			if field != tt.wantField {
				t.Errorf("field = %q, want %q", field, tt.wantField)
			}
		})
	}
}

func TestDescriptionApplied(t *testing.T) {
	intent := &accessv1.InterfaceDescriptionChange{}
	intent.SetInterfaceName("ethernet 1/1/1")
	intent.SetDescription("uplink")

	matching := completeObservation("uplink", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP)
	if !interfaces.DescriptionApplied(matching, intent) {
		t.Error("DescriptionApplied = false, want true for a matching description")
	}

	mismatched := completeObservation("stale", interfacev1.AdminStatus_ADMIN_STATUS_UP, interfacev1.OperStatus_OPER_STATUS_UP)
	if interfaces.DescriptionApplied(mismatched, intent) {
		t.Error("DescriptionApplied = true, want false for a mismatched description")
	}
}

func TestFreshness_Stale(t *testing.T) {
	observedAt := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	f := interfaces.Freshness{ObservedAt: observedAt, MaxAge: 30 * time.Second}

	if f.Stale(observedAt.Add(30 * time.Second)) {
		t.Error("Stale = true at exactly MaxAge, want false (boundary is inclusive)")
	}

	if !f.Stale(observedAt.Add(30*time.Second + time.Nanosecond)) {
		t.Error("Stale = false just past MaxAge, want true")
	}

	if f.Stale(observedAt) {
		t.Error("Stale = true at ObservedAt, want false")
	}
}

func TestDelayedEffect_WithinHorizon(t *testing.T) {
	since := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	d := interfaces.DelayedEffect{Horizon: 10 * time.Second}

	if !d.WithinHorizon(since, since.Add(10*time.Second)) {
		t.Error("WithinHorizon = false at exactly Horizon, want true (boundary is inclusive)")
	}

	if d.WithinHorizon(since, since.Add(10*time.Second+time.Nanosecond)) {
		t.Error("WithinHorizon = true just past Horizon, want false")
	}
}

// errFallback is a sentinel error for TestSelectRoute_FallbackErrorPropagates.
type sentinelErr string

func (e sentinelErr) Error() string { return string(e) }

const errFallback = sentinelErr("fallback failed")
