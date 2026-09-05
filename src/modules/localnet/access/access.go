package access

// This file is the facade doc.go describes: thin wrappers over the
// interface capability under internal/capability/interfaces, so a caller
// outside this module never imports an internal path directly.

import (
	"context"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// InterfaceShellAdapter is the seam a firmware-specific SSH adapter
// implements for the interface capability; fastiron.Adapter is the first.
type InterfaceShellAdapter = interfaces.ShellAdapter

// InterfaceProvenanceInputs are the fields a caller supplies that the
// capability cannot derive itself: which binding and edge answered, and
// the device's firmware fingerprint.
type InterfaceProvenanceInputs = interfaces.ProvenanceInputs

// InterfaceVerificationDisposition is the outcome of
// [VerifyInterfaceDescriptionChange].
type InterfaceVerificationDisposition = interfaces.VerificationDisposition

// InterfaceFreshness and InterfaceDelayedEffect are the typed values a
// route selector consumes: how old a cached observation may be before
// [ReadInterface] re-reads it, and how long a mutation may take to become
// visible before [VerifyInterfaceDescriptionChange] reports it failed
// rather than not yet applied.
type (
	InterfaceFreshness     = interfaces.Freshness
	InterfaceDelayedEffect = interfaces.DelayedEffect
)

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
