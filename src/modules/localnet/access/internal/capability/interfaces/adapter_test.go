package interfaces_test

import (
	"context"
	"testing"
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
)

func testProvenanceInputs() interfaces.ProvenanceInputs {
	binding := &inventoryv1.BindingGlobalRef{}
	binding.SetBinding((&inventoryv1.BindingLocalRef{}))
	binding.GetBinding().SetId("11111111-1111-1111-1111-111111111111")

	edge := &edgev1.EdgeGlobalRef{}
	edge.SetEdge(&edgev1.EdgeLocalRef{})
	edge.GetEdge().SetId("22222222-2222-2222-2222-222222222222")

	return interfaces.ProvenanceInputs{Binding: binding, Edge: edge, FirmwareFingerprint: "SPS10010g"}
}

// fakeShellAdapter is a scripted interfaces.ShellAdapter for U6's tests,
// independent of any real firmware adapter.
type fakeShellAdapter struct {
	description string
	admin       interfacev1.AdminStatus
	oper        interfacev1.OperStatus
	readErr     error
	setErr      error
}

func (f *fakeShellAdapter) ReadInterface(context.Context, string) (string, interfacev1.AdminStatus, interfacev1.OperStatus, error) {
	return f.description, f.admin, f.oper, f.readErr
}

func (f *fakeShellAdapter) SetPortName(context.Context, string, string) error { return f.setErr }

func TestRead_SNMPCompleteNeverCallsShell(t *testing.T) {
	vbs := append(ifRow(1, "ethernet 1/1/1"), stringVar(ifXEntry, 18, 1, []byte("uplink to core")))

	called := false
	shell := &fakeShellAdapter{}

	obs, err := interfaces.Read(
		context.Background(), &fakeSession{vbs: vbs}, shellSpy(shell, &called),
		"ethernet 1/1/1", testProvenanceInputs(), nil, interfaces.Freshness{}, time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if called {
		t.Error("shell adapter was called even though SNMP was complete")
	}

	if got := obs.GetCompleteness(); got != accessv1.Completeness_COMPLETENESS_COMPLETE {
		t.Errorf("completeness = %v, want COMPLETE", got)
	}

	if got := obs.GetProvenance().GetProtocol(); got != inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP {
		t.Errorf("provenance protocol = %v, want SNMP", got)
	}
}

func TestRead_SNMPPartialFallsThroughToSSH(t *testing.T) {
	shell := &fakeShellAdapter{
		description: "uplink to core",
		admin:       interfacev1.AdminStatus_ADMIN_STATUS_UP,
		oper:        interfacev1.OperStatus_OPER_STATUS_UP,
	}

	obs, err := interfaces.Read(
		context.Background(), &fakeSession{vbs: ifRow(1, "ethernet 1/1/1")}, shell,
		"ethernet 1/1/1", testProvenanceInputs(), nil, interfaces.Freshness{}, time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got := obs.GetCompleteness(); got != accessv1.Completeness_COMPLETENESS_COMPLETE {
		t.Errorf("completeness = %v, want COMPLETE", got)
	}

	if got := obs.GetProvenance().GetProtocol(); got != inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH {
		t.Errorf("provenance protocol = %v, want SSH", got)
	}
}

func TestRead_CachedFreshObservationSkipsBothRoutes(t *testing.T) {
	cached := &accessv1.InterfaceObservation{}
	cached.SetInterfaceName("ethernet 1/1/1")
	cached.SetDescription("uplink to core")
	cached.SetAdminStatus(interfacev1.AdminStatus_ADMIN_STATUS_UP)
	cached.SetOperStatus(interfacev1.OperStatus_OPER_STATUS_UP)
	cached.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	freshness := interfaces.Freshness{ObservedAt: now, MaxAge: time.Minute}

	// No SNMP session or shell fixture is supplied: a fake session with no
	// vbs errors on every walk, so if Read touched either route this
	// would fail rather than return the cached value.
	got, err := interfaces.Read(
		context.Background(), &fakeSession{}, &fakeShellAdapter{readErr: errBoom},
		"ethernet 1/1/1", testProvenanceInputs(), cached, freshness, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got != cached {
		t.Error("Read did not return the cached observation")
	}
}

func TestVerifyDescriptionChange(t *testing.T) {
	intent := &accessv1.InterfaceDescriptionChange{}
	intent.SetInterfaceName("ethernet 1/1/1")
	intent.SetDescription("uplink to core")

	since := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	effect := interfaces.DelayedEffect{Horizon: 10 * time.Second}

	t.Run("matches", func(t *testing.T) {
		shell := &fakeShellAdapter{
			description: "uplink to core",
			admin:       interfacev1.AdminStatus_ADMIN_STATUS_UP,
			oper:        interfacev1.OperStatus_OPER_STATUS_UP,
		}

		_, disposition, err := interfaces.VerifyDescriptionChange(
			context.Background(), &fakeSession{}, shell, intent, testProvenanceInputs(), effect, since, since.Add(2*time.Second))
		if err != nil {
			t.Fatalf("VerifyDescriptionChange: %v", err)
		}

		if disposition != interfaces.VerificationVerified {
			t.Errorf("disposition = %v, want VerificationVerified", disposition)
		}
	})

	t.Run("not yet verified within horizon", func(t *testing.T) {
		shell := &fakeShellAdapter{
			description: "old description",
			admin:       interfacev1.AdminStatus_ADMIN_STATUS_UP,
			oper:        interfacev1.OperStatus_OPER_STATUS_UP,
		}

		_, disposition, err := interfaces.VerifyDescriptionChange(
			context.Background(), &fakeSession{}, shell, intent, testProvenanceInputs(), effect, since, since.Add(5*time.Second))
		if err != nil {
			t.Fatalf("VerifyDescriptionChange: %v", err)
		}

		if disposition != interfaces.VerificationNotYetVerified {
			t.Errorf("disposition = %v, want VerificationNotYetVerified", disposition)
		}
	})

	t.Run("failed past horizon", func(t *testing.T) {
		shell := &fakeShellAdapter{
			description: "old description",
			admin:       interfacev1.AdminStatus_ADMIN_STATUS_UP,
			oper:        interfacev1.OperStatus_OPER_STATUS_UP,
		}

		_, disposition, err := interfaces.VerifyDescriptionChange(
			context.Background(), &fakeSession{}, shell, intent, testProvenanceInputs(), effect, since, since.Add(20*time.Second))
		if err != nil {
			t.Fatalf("VerifyDescriptionChange: %v", err)
		}

		if disposition != interfaces.VerificationFailed {
			t.Errorf("disposition = %v, want VerificationFailed", disposition)
		}
	})
}

// shellSpy wraps a ShellAdapter and records whether either of its methods
// was invoked.
type shellSpyAdapter struct {
	interfaces.ShellAdapter
	called *bool
}

func (s shellSpyAdapter) ReadInterface(ctx context.Context, name string) (string, interfacev1.AdminStatus, interfacev1.OperStatus, error) {
	*s.called = true

	return s.ShellAdapter.ReadInterface(ctx, name)
}

func shellSpy(inner interfaces.ShellAdapter, called *bool) interfaces.ShellAdapter {
	return shellSpyAdapter{ShellAdapter: inner, called: called}
}

// errBoom is a sentinel error for tests that must not reach the shell
// route.
var errBoom = boomErr("shell adapter should not have been called")

type boomErr string

func (e boomErr) Error() string { return string(e) }
