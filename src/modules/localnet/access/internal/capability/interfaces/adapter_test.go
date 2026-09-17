package interfaces_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
)

// mustValid holds a message to its own schema rules — the capability may
// not hand a caller a message the CEL rules in
// spec/proto/flowseer/device/access/v1/interface.proto would reject.
func mustValid(t *testing.T, m proto.Message) {
	t.Helper()

	if err := protovalidate.Validate(m); err != nil {
		t.Fatalf("message is invalid: %v", err)
	}
}

func testProvenanceInputs() interfaces.ProvenanceInputs {
	binding := &inventoryv1.BindingGlobalRef{}
	binding.SetBinding((&inventoryv1.BindingLocalRef{}))
	binding.GetBinding().SetId("11111111-1111-1111-1111-111111111111")

	edge := &edgev1.EdgeGlobalRef{}
	edge.SetEdge(&edgev1.EdgeLocalRef{})
	edge.GetEdge().SetId("22222222-2222-2222-2222-222222222222")

	return interfaces.ProvenanceInputs{Binding: binding, Edge: edge, FirmwareFingerprint: "SPS10010g"}
}

// fakeShellAdapter is a scripted ShellAdapter, independent of any real
// firmware adapter.
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

func (f *fakeShellAdapter) SetPortName(_ context.Context, _, text string) error {
	if f.setErr != nil {
		return f.setErr
	}

	f.description = text

	return nil
}

// A complete SNMP read is answered without the shell being opened at all.
//
// Opened, not called: the assertion is on the login, because that is the
// cost. A read that opens an SSH session and then does not use it has still
// logged in — on a switch that caps concurrent sessions, once per read —
// and it has still failed the read if the login failed, which is how a
// device whose SNMP answered every OID came to have no readable interfaces.
// Asserting only that ReadInterface went uncalled passes for exactly that
// implementation, which is what this assertion used to say.
func TestRead_SNMPCompleteNeverOpensTheShell(t *testing.T) {
	vbs := append(ifRow(1, "ethernet 1/1/1"), stringVar(ifXEntry, 18, 1, []byte("uplink to core")))

	opened := false

	obs, err := interfaces.Read(
		context.Background(), &fakeSession{vbs: vbs}, openerSpy(&fakeShellAdapter{}, &opened),
		"ethernet 1/1/1", testProvenanceInputs(), nil, interfaces.Freshness{}, time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if opened {
		t.Error("a shell session was opened even though SNMP answered completely")
	}

	if got := obs.GetCompleteness(); got != accessv1.Completeness_COMPLETENESS_COMPLETE {
		t.Errorf("completeness = %v, want COMPLETE", got)
	}

	if got := obs.GetProvenance().GetProtocol(); got != inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP {
		t.Errorf("provenance protocol = %v, want SNMP", got)
	}

	mustValid(t, obs)
}

func TestRead_SNMPPartialFallsThroughToSSH(t *testing.T) {
	shell := &fakeShellAdapter{
		description: "uplink to core",
		admin:       interfacev1.AdminStatus_ADMIN_STATUS_UP,
		oper:        interfacev1.OperStatus_OPER_STATUS_UP,
	}

	obs, err := interfaces.Read(
		context.Background(), &fakeSession{vbs: ifRow(1, "ethernet 1/1/1")}, interfaces.OpenedShell(shell),
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

	mustValid(t, obs)
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
		context.Background(), &fakeSession{}, interfaces.OpenedShell(&fakeShellAdapter{readErr: errBoom}),
		"ethernet 1/1/1", testProvenanceInputs(), cached, freshness, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got != cached {
		t.Error("Read did not return the cached observation")
	}
}

func TestSetDescriptionChange(t *testing.T) {
	intent := &accessv1.InterfaceDescriptionChange{}
	intent.SetInterfaceName("ethernet 1/1/1")
	intent.SetDescription("uplink to core")

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	effect := interfaces.DelayedEffect{Horizon: 10 * time.Second}

	t.Run("applies immediately", func(t *testing.T) {
		shell := &fakeShellAdapter{
			description: "old description",
			admin:       interfacev1.AdminStatus_ADMIN_STATUS_UP,
			oper:        interfacev1.OperStatus_OPER_STATUS_UP,
		}

		obs, disposition, err := interfaces.SetDescriptionChange(
			context.Background(), &fakeSession{}, shell, intent, testProvenanceInputs(), effect, now)
		if err != nil {
			t.Fatalf("SetDescriptionChange: %v", err)
		}

		if disposition != interfaces.VerificationVerified {
			t.Errorf("disposition = %v, want VerificationVerified", disposition)
		}

		mustValid(t, obs)
	})

	t.Run("set fails before any verification", func(t *testing.T) {
		shell := &fakeShellAdapter{setErr: errBoom}

		if _, _, err := interfaces.SetDescriptionChange(
			context.Background(), &fakeSession{}, shell, intent, testProvenanceInputs(), effect, now); err == nil {
			t.Fatal("SetDescriptionChange did not error when SetPortName failed")
		}
	})
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

// A shell that cannot be opened does not cost a read the SNMP route already
// answered.
//
// This is the same property as the test above seen from the failure side,
// and it is kept separate because it is the one that was live: on the lab
// ICX7150 every interface read failed with a device-session error while SNMP
// was returning a complete observation for the interface asked about.
func TestRead_SNMPCompleteSurvivesAShellThatCannotBeOpened(t *testing.T) {
	vbs := append(ifRow(1, "ethernet 1/1/1"), stringVar(ifXEntry, 18, 1, []byte("uplink to core")))

	obs, err := interfaces.Read(
		context.Background(), &fakeSession{vbs: vbs}, failingOpener(),
		"ethernet 1/1/1", testProvenanceInputs(), nil, interfaces.Freshness{}, time.Now())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got := obs.GetCompleteness(); got != accessv1.Completeness_COMPLETENESS_COMPLETE {
		t.Errorf("completeness = %v, want COMPLETE", got)
	}
}

// A shell that cannot be opened does fail a read that needed it, and the
// failure says which half went wrong.
//
// The point of the change above is not that a failed login stops mattering.
// It is that it stops mattering to reads that were never going to use it —
// so a read that had no other route must still fail, or the fix would have
// turned a device with no working route into one that silently returns
// partial observations.
func TestRead_SNMPIncompleteFailsWhenTheShellCannotBeOpened(t *testing.T) {
	_, err := interfaces.Read(
		context.Background(), &fakeSession{vbs: ifRow(1, "ethernet 1/1/1")}, failingOpener(),
		"ethernet 1/1/1", testProvenanceInputs(), nil, interfaces.Freshness{}, time.Now())
	if err == nil {
		t.Fatal("Read succeeded with an incomplete SNMP observation and no shell to fall back to")
	}

	if !strings.Contains(err.Error(), "open shell session") {
		t.Errorf("the failure does not name the shell open: %v", err)
	}
}

// openerSpy is an opener over inner that records having been called.
func openerSpy(inner interfaces.ShellAdapter, opened *bool) interfaces.ShellOpener {
	return func(context.Context) (interfaces.ShellAdapter, func(), error) {
		*opened = true

		return inner, func() {}, nil
	}
}

// failingOpener is a device whose shell cannot be opened: the lab switch's
// read credential is SNMP material, so the login it is offered to is refused
// every time.
func failingOpener() interfaces.ShellOpener {
	return func(context.Context) (interfaces.ShellAdapter, func(), error) {
		return nil, nil, errBoom
	}
}

// errBoom is a sentinel error for tests that must not reach the shell
// route.
const errBoom = sentinelErr("shell adapter should not have been called")
