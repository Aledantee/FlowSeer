package access

// This file is the facade doc.go describes: thin wrappers over the
// interface capability under internal/capability/interfaces, so a caller
// outside this module never imports an internal path directly.

import (
	"context"
	"time"

	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/fastiron"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/credential"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

// ReadCredentialSource is the seam the lane acquires a read credential
// through, for every read and for the onboarding probe.
//
// It and the two below are aliases rather than repeats: the interfaces live
// beside the adapter that implements them, and a host that could not name
// them could not fill them. [SubmissionHandle] is why this matters more than
// tidiness — [SubmissionCredentialSource]'s Open returns it, and a method
// returning a type the caller may not name cannot be written outside this
// module at all. Naming a seam and being unable to fill it is what this
// facade exists to prevent; see [NewFastIronShell] for the same argument
// about the shell adapter.
type ReadCredentialSource = credential.ReadCredentialSource

// SubmissionCredentialSource is the seam a mutation opens its one-use grant
// through.
type SubmissionCredentialSource = credential.SubmissionCredentialSource

// SubmissionHandle is what one open submission stream hands its caller: the
// grant, and the authority to check before each command.
type SubmissionHandle = credential.SubmissionHandle

// NewConnectCredentials builds both credential sources over a live
// EdgeService client: the read credential each read and the onboarding probe
// acquire, and the one-use grant a mutation submits under.
//
// One adapter satisfies both, and both are returned from one call because a
// host wiring one and forgetting the other gets a lane that reads devices and
// silently cannot mutate them — the no-op default grants immediately, stays
// AUTHORIZED and never ends, which is right for a facade with no central and
// wrong for a host that has one.
func NewConnectCredentials(client edgev1connect.EdgeServiceClient) (ReadCredentialSource, SubmissionCredentialSource) {
	adapter := &credential.ConnectAdapter{Client: client}
	return adapter, adapter
}

// Telemetry is the lane's own instrumentation scope. A nil one is tolerated
// by every method, so a host that cannot construct one gets a lane whose
// spans and metrics are silently off — which is why [NewTelemetry] is
// exported rather than left inside.
type Telemetry = telemetry.View

// TelemetryConfig is what a Telemetry is built from: the host's providers,
// not its tracer.
type TelemetryConfig = telemetry.ViewConfig

// NewTelemetry builds the lane's instrumentation scope. It names itself
// rather than reusing the host runtime's scope, per the observability
// convention, so a host passes its providers and not its tracer.
func NewTelemetry(cfg TelemetryConfig) (*Telemetry, error) { return telemetry.NewView(cfg) }

// InterfaceShellAdapter is the seam a firmware-specific SSH adapter
// implements for the interface capability; fastiron.Adapter is the first.
type InterfaceShellAdapter = interfaces.ShellAdapter

// NewFastIronShell builds the FastIron shell adapter over an already-dialed
// SSH session, and logs it in to privileged mode.
//
// It is here because this facade exists so a caller outside this module never
// imports an internal path, and the FastIron adapter is the one implementation
// of [InterfaceShellAdapter] the module ships. Without it a host can name the
// seam and cannot fill it, which leaves it either reimplementing the
// firmware's commands or reaching into internal/ — and the second is what the
// facade is for preventing.
//
// enablePassword is written, redacted, only if the device answers the enable
// command with a password prompt; empty means none is expected.
func NewFastIronShell(ctx context.Context, session *ssh.Session, enablePassword secret.Value) (InterfaceShellAdapter, error) {
	adapter := &fastiron.Adapter{Session: session, EnablePassword: enablePassword}
	if err := adapter.Login(ctx); err != nil {
		return nil, err
	}
	return adapter, nil
}

// InterfaceProvenanceInputs are the fields a caller supplies that the
// capability cannot derive itself: which binding and edge answered, and
// the device's firmware fingerprint.
type InterfaceProvenanceInputs = interfaces.ProvenanceInputs

// InterfaceVerificationDisposition is the outcome of
// [VerifyInterfaceDescriptionChange].
type InterfaceVerificationDisposition = interfaces.VerificationDisposition

// InterfaceFreshness bounds how old a cached observation may be before
// [ReadInterface] re-reads it instead of reusing it.
type InterfaceFreshness = interfaces.Freshness

// InterfaceDelayedEffect bounds how long a mutation may take to become
// visible before [VerifyInterfaceDescriptionChange] reports it failed
// rather than not yet applied.
type InterfaceDelayedEffect = interfaces.DelayedEffect

// Interface verification dispositions; see
// [interfaces.VerificationDisposition] for what each means.
const (
	InterfaceVerificationUnspecified = interfaces.VerificationUnspecified
	InterfaceVerificationVerified    = interfaces.VerificationVerified
	InterfaceVerificationNotYet      = interfaces.VerificationNotYetVerified
	InterfaceVerificationFailed      = interfaces.VerificationFailed
)

// InterfaceShellOpener opens the shell session the fallback read route
// needs. [ReadInterface] calls it only if it takes that route, so a device
// whose SNMP answers completely is never logged into over SSH.
type InterfaceShellOpener = interfaces.ShellOpener

// OpenedInterfaceShell is the opener for a shell that is already open, for a
// caller that needed one for its own work. See [interfaces.OpenedShell] for
// why that is the exception rather than the shape to reach for.
func OpenedInterfaceShell(adapter InterfaceShellAdapter) InterfaceShellOpener {
	return interfaces.OpenedShell(adapter)
}

// ReadInterface reads one interface's description, admin status, and oper
// status: a cached, still-fresh observation is reused; otherwise SNMP is
// read first and SSH (shell) only if that read is not complete, per the
// direction record's decision 1. cached may be nil, and so may openShell —
// a device with no shell has no fallback route.
func ReadInterface(
	ctx context.Context,
	sess snmp.Session,
	openShell InterfaceShellOpener,
	name string,
	prov InterfaceProvenanceInputs,
	cached *accessv1.InterfaceObservation,
	freshness InterfaceFreshness,
	now time.Time,
) (*accessv1.InterfaceObservation, error) {
	return interfaces.Read(ctx, sess, openShell, name, prov, cached, freshness, now)
}

// VerifyInterfaceDescriptionChange reads the affected interface's current
// state and reports whether it matches intent, per the direction record's
// decision 2: a mutation is verified only by an observation, never by its
// own command succeeding.
func VerifyInterfaceDescriptionChange(
	ctx context.Context,
	sess snmp.Session,
	shell InterfaceShellAdapter,
	intent *accessv1.InterfaceDescriptionChange,
	prov InterfaceProvenanceInputs,
	effect InterfaceDelayedEffect,
	since time.Time,
	now time.Time,
) (*accessv1.InterfaceObservation, InterfaceVerificationDisposition, error) {
	return interfaces.VerifyDescriptionChange(ctx, sess, shell, intent, prov, effect, since, now)
}

// SetInterfaceDescription issues intent over shell and verifies it once,
// per the direction record's decision 2: a write capability is advertised
// only together with its own verification. now is the mutation's own
// timestamp, so a mismatch on this first check is always
// InterfaceVerificationNotYet; a caller polls
// VerifyInterfaceDescriptionChange for later checks within effect's
// horizon.
func SetInterfaceDescription(
	ctx context.Context,
	sess snmp.Session,
	shell InterfaceShellAdapter,
	intent *accessv1.InterfaceDescriptionChange,
	prov InterfaceProvenanceInputs,
	effect InterfaceDelayedEffect,
	now time.Time,
) (*accessv1.InterfaceObservation, InterfaceVerificationDisposition, error) {
	return interfaces.SetDescriptionChange(ctx, sess, shell, intent, prov, effect, now)
}
