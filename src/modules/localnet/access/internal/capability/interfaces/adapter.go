package interfaces

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// ShellAdapter is the seam a firmware-specific shell mapping implements.
// fastiron.Adapter satisfies it structurally; this package never imports a
// firmware package (the direction record's decision 11), so ShellAdapter,
// not a concrete adapter type, is what Read and VerifyDescriptionChange
// take.
type ShellAdapter interface {
	// ReadInterface reads one interface's description, admin status, and
	// oper status. A successful call is complete: every compared field is
	// set.
	ReadInterface(ctx context.Context, name string) (description string, admin interfacev1.AdminStatus, oper interfacev1.OperStatus, err error)
	// SetPortName sets or clears an interface's description on running
	// configuration only.
	SetPortName(ctx context.Context, name, text string) error
}

// ProvenanceInputs are the fields neither ReadSNMP nor a ShellAdapter can
// supply: which binding and edge answered, and the device's firmware
// fingerprint. Edge and FirmwareFingerprint must both be set — an
// InterfaceObservation's Provenance requires them together whenever
// Provenance itself is present, since this capability is always
// edge-mediated, never a cloud-mediated integration answering centrally.
type ProvenanceInputs struct {
	Binding             *inventoryv1.BindingGlobalRef
	Edge                *edgev1.EdgeGlobalRef
	FirmwareFingerprint string
}

// provenance builds the Provenance Read attaches to whichever observation
// SelectRoute picks.
func (p ProvenanceInputs) provenance(route Route, observedAt time.Time) *inventoryv1.Provenance {
	prov := &inventoryv1.Provenance{}
	prov.SetBinding(p.Binding)
	prov.SetObservedAt(timestamppb.New(observedAt))
	prov.SetEdge(p.Edge)
	prov.SetFirmwareFingerprint(p.FirmwareFingerprint)

	switch route {
	case RouteSNMP:
		prov.SetProtocol(inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP)
	case RouteSSH:
		prov.SetProtocol(inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH)
	default:
		prov.SetProtocol(inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_UNSPECIFIED)
	}

	return prov
}

// Read builds the interface's observation for now: a cached observation
// still within freshness is returned as is; otherwise it reads over SNMP
// and, per the direction record's decision 1, falls through to shell over
// SSH only when the SNMP read is not COMPLETENESS_COMPLETE. The winning
// observation's Provenance is filled from prov and the route that
// answered.
func Read(
	ctx context.Context,
	sess snmp.Session,
	shell ShellAdapter,
	name string,
	prov ProvenanceInputs,
	cached *accessv1.InterfaceObservation,
	freshness Freshness,
	now time.Time,
) (*accessv1.InterfaceObservation, error) {
	if cached.GetCompleteness() == accessv1.Completeness_COMPLETENESS_COMPLETE && !freshness.Stale(now) {
		return cached, nil
	}

	// A failed SNMP read is treated the same as an incomplete one: SelectRoute
	// falls through to shell either way. snmpErr is kept only to join into
	// an eventual failure, since a caller needs to see it if shell also
	// fails.
	primary, snmpErr := ReadSNMP(ctx, sess, name)
	if snmpErr != nil {
		primary = nil
	}

	fallback := func() (*accessv1.InterfaceObservation, error) {
		description, admin, oper, err := shell.ReadInterface(ctx, name)
		if err != nil {
			return nil, errs.Wrap(err, "read over ssh")
		}

		obs := &accessv1.InterfaceObservation{}
		obs.SetInterfaceName(name)
		obs.SetDescription(description)
		obs.SetAdminStatus(admin)
		obs.SetOperStatus(oper)
		obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)

		return obs, nil
	}

	obs, route, err := SelectRoute(primary, fallback)
	if err != nil {
		return nil, errors.Join(snmpErr, err)
	}

	obs.SetProvenance(prov.provenance(route, now))

	return obs, nil
}

// VerificationDisposition is the outcome of one [VerifyDescriptionChange]
// call.
type VerificationDisposition int

const (
	// VerificationUnspecified means the call did not reach a disposition
	// (an error occurred).
	VerificationUnspecified VerificationDisposition = iota
	// VerificationVerified means a fresh, complete read matches the
	// intent.
	VerificationVerified
	// VerificationNotYetVerified means the read did not match, but the
	// delayed-effect horizon since the mutation has not elapsed — the
	// device may still apply it.
	VerificationNotYetVerified
	// VerificationFailed means the read did not match and the horizon has
	// elapsed.
	VerificationFailed
)

// String returns the disposition's name for logging and diagnostics.
func (d VerificationDisposition) String() string {
	switch d {
	case VerificationVerified:
		return "verified"
	case VerificationNotYetVerified:
		return "not_yet_verified"
	case VerificationFailed:
		return "failed"
	default:
		return "unspecified"
	}
}

// VerifyDescriptionChange implements the direction record's decision 2: a
// mutation is verified only by an observation of the affected state, never
// by the mutation's own command succeeding. It calls [Read] exactly once —
// this function does not retry — and reports one of three dispositions: a
// caller polling it across the delayed-effect horizon sees
// VerificationNotYetVerified become either VerificationVerified or
// VerificationFailed as later calls land, matching the direction record's
// decision 5.
//
// since is when the mutation was submitted (the read that established
// effect's horizon should begin from); now is the time of this call, so a
// test can drive the horizon comparison without a real clock.
func VerifyDescriptionChange(
	ctx context.Context,
	sess snmp.Session,
	shell ShellAdapter,
	intent *accessv1.InterfaceDescriptionChange,
	prov ProvenanceInputs,
	effect DelayedEffect,
	since time.Time,
	now time.Time,
) (*accessv1.InterfaceObservation, VerificationDisposition, error) {
	obs, err := Read(ctx, sess, shell, intent.GetInterfaceName(), prov, nil, Freshness{}, now)
	if err != nil {
		return nil, VerificationUnspecified, err
	}

	if obs.GetCompleteness() == accessv1.Completeness_COMPLETENESS_COMPLETE && DescriptionApplied(obs, intent) {
		return obs, VerificationVerified, nil
	}

	if effect.WithinHorizon(since, now) {
		return obs, VerificationNotYetVerified, nil
	}

	return obs, VerificationFailed, nil
}
