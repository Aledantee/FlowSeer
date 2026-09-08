package access

// This file is the facade doc.go describes: thin wrappers over the
// interface capability under internal/capability/interfaces, so a caller
// outside this module never imports an internal path directly.

import (
	"context"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/fastiron"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

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
func NewFastIronShell(ctx context.Context, session *ssh.Session, enablePassword string) (InterfaceShellAdapter, error) {
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

// ReadInterface reads one interface's description, admin status, and oper
// status: a cached, still-fresh observation is reused; otherwise SNMP is
// read first and SSH (shell) only if that read is not complete, per the
// direction record's decision 1. cached may be nil.
func ReadInterface(
	ctx context.Context,
	sess snmp.Session,
	shell InterfaceShellAdapter,
	name string,
	prov InterfaceProvenanceInputs,
	cached *accessv1.InterfaceObservation,
	freshness InterfaceFreshness,
	now time.Time,
) (*accessv1.InterfaceObservation, error) {
	return interfaces.Read(ctx, sess, shell, name, prov, cached, freshness, now)
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
