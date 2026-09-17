package interfaces

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// ErrCodeNotSubmitted marks a failure an adapter can prove changed nothing on
// the device: the command that would have changed it was never sent.
//
// It is part of the ShellAdapter contract rather than any one adapter's
// business, because the caller acts on it. A mutation whose submit error
// carries this is disposed rejected — provably nothing happened — while one
// carrying anything else is treated as an effect nobody can establish, which
// is what a command that may have been delivered deserves.
//
// The conservative reading is the default and must stay that way. An adapter
// that grows a new refusal path and does not mark it lands on "unknown",
// which is wrong in the safe direction; an adapter that marks a path it
// cannot actually prove reports a device as untouched when it may not be,
// which is wrong in the direction that reaches a switch. Mark only what the
// code's own structure makes certain.
var ErrCodeNotSubmitted = errs.NewCode("interfaces/not-submitted")

// ShellAdapter is the seam a firmware-specific shell mapping implements.
// fastiron.Adapter satisfies it structurally; this package never imports a
// firmware package (the firmware-family split), so ShellAdapter,
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

// ShellOpener opens the shell session the fallback read route needs, and
// returns the way to close it.
//
// [Read] takes an opener rather than an open adapter because the fallback is
// the route it may not take. A device whose SNMP answers completely is read
// without ever logging in over SSH, which against a switch that caps
// concurrent sessions is the difference between a read and a refused login;
// and a device whose shell cannot be opened at all is still read, because
// nothing tried to open it. Read calls the opener at most once, and only on
// the route that uses it.
//
// release is called when the read is done with the adapter, and is never nil
// when err is nil, so a caller need not check it.
type ShellOpener func(ctx context.Context) (adapter ShellAdapter, release func(), err error)

// OpenedShell is the opener for a shell somebody else has already opened: it
// hands out the adapter it was given and closes nothing, since whoever opened
// it owns it. A nil adapter yields a nil opener, which is how a device with
// no shell says it has no fallback route.
//
// This is for a caller that needs the adapter for its own work anyway — a
// mutation sends its command over one — and not the shape to reach for
// otherwise: it gives up exactly the laziness [ShellOpener] exists for.
func OpenedShell(adapter ShellAdapter) ShellOpener {
	if adapter == nil {
		return nil
	}

	return func(context.Context) (ShellAdapter, func(), error) {
		return adapter, func() {}, nil
	}
}

// ProvenanceInputs are the fields neither ReadSNMP nor a ShellAdapter can
// supply: which binding and edge answered, and the device's firmware
// fingerprint. Edge and FirmwareFingerprint must both be set — an
// InterfaceObservation's Provenance requires them together whenever
// Provenance itself is present, since this capability is always
// edge-mediated, never a cloud-mediated integration answering centrally.
//
// FirmwareFingerprint is the one field with two kinds of caller.
// access.Lane overwrites whatever it is given with the fingerprint its own
// identity probe returned, because an observation's provenance must name
// the epoch the lane actually observed under rather than one a host
// supplied and may not have refreshed. Callers of the package-level facade
// functions in access.go have no probe behind them and set it themselves;
// what they put here is what the observation carries.
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
// and, per the route-fallback rule, falls through to shell over
// SSH only when the SNMP read is not COMPLETENESS_COMPLETE. The winning
// observation's Provenance is filled from prov and the route that
// answered.
//
// openShell is called only if that fall-through happens, and may be nil for
// a device with no shell — an incomplete SNMP read then fails rather than
// being answered another way. Opening the shell is this function's decision
// to make and not its caller's: see [ShellOpener].
func Read(
	ctx context.Context,
	sess snmp.Session,
	openShell ShellOpener,
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

	var fallback func() (*accessv1.InterfaceObservation, error)
	if openShell != nil {
		fallback = func() (*accessv1.InterfaceObservation, error) {
			shell, closeShell, err := openShell(ctx)
			if err != nil {
				return nil, errs.Wrap(err, "open shell session")
			}

			defer closeShell()

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

// VerifyDescriptionChange implements the verification rule: a
// mutation is verified only by an observation of the affected state, never
// by the mutation's own command succeeding. It calls [Read] exactly once —
// this function does not retry — and reports one of three dispositions: a
// caller polling it across the delayed-effect horizon sees
// VerificationNotYetVerified become either VerificationVerified or
// VerificationFailed as later calls land — which is how ambiguity stays
// indeterminate until an observation resolves it.
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
	obs, err := Read(ctx, sess, OpenedShell(shell), intent.GetInterfaceName(), prov, nil, Freshness{}, now)
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

// SetDescriptionChange issues intent's description over shell and verifies
// it once: the verification rule requires a write capability
// to carry its own verification, so this is the only way this package
// mutates a description. now is submitted (the mutation's own timestamp)
// and is passed to VerifyDescriptionChange as both since and now, so the
// first check always reports VerificationNotYetVerified on a mismatch —
// the caller polls VerifyDescriptionChange itself for later checks within
// the delayed-effect horizon.
func SetDescriptionChange(
	ctx context.Context,
	sess snmp.Session,
	shell ShellAdapter,
	intent *accessv1.InterfaceDescriptionChange,
	prov ProvenanceInputs,
	effect DelayedEffect,
	now time.Time,
) (*accessv1.InterfaceObservation, VerificationDisposition, error) {
	if err := shell.SetPortName(ctx, intent.GetInterfaceName(), intent.GetDescription()); err != nil {
		return nil, VerificationUnspecified, errs.Wrap(err, "set port name")
	}

	return VerifyDescriptionChange(ctx, sess, shell, intent, prov, effect, now, now)
}
