package snmp

import (
	"fmt"

	"go.aledante.io/ae"
)

// PDUErrorStatus is the SNMP agent-side error-status code carried in a
// response PDU. Values match the SMI-standard codes; this type does not
// import any backend package so that callers can compare against named
// constants without pulling in gosnmp.
type PDUErrorStatus int

const (
	// NoError indicates a successful response (status 0).
	NoError PDUErrorStatus = iota
	// TooBig indicates the response would be too large to transport.
	TooBig
	// NoSuchName is the SNMPv1-only "no such name" code.
	NoSuchName
	// BadValue indicates a Set request supplied an unacceptable value.
	BadValue
	// ReadOnly indicates a Set request targeted a read-only object.
	ReadOnly
	// GenErr is a generic unspecified error from the agent.
	GenErr
	// NoAccess indicates the requesting principal lacks access to the object.
	NoAccess
	// WrongType indicates the supplied value has the wrong SMI type.
	WrongType
	// WrongLength indicates the supplied value's length is out of range.
	WrongLength
	// WrongEncoding indicates the supplied value's encoding is malformed.
	WrongEncoding
	// WrongValue indicates the supplied value is otherwise unacceptable.
	WrongValue
	// NoCreation indicates the target row cannot be created.
	NoCreation
	// InconsistentValue indicates the value is inconsistent with other state.
	InconsistentValue
	// ResourceUnavailable indicates the agent lacks resources to satisfy the request.
	ResourceUnavailable
	// CommitFailed indicates a Set-phase commit step failed.
	CommitFailed
	// UndoFailed indicates a Set-phase undo step failed.
	UndoFailed
	// AuthorizationError indicates the request was not authorized.
	AuthorizationError
	// NotWritable indicates the target object is not writable.
	NotWritable
	// InconsistentName indicates the target name is inconsistent.
	InconsistentName
)

// String returns the canonical SMI-style name of the status code.
func (s PDUErrorStatus) String() string {
	switch s {
	case NoError:
		return "noError"
	case TooBig:
		return "tooBig"
	case NoSuchName:
		return "noSuchName"
	case BadValue:
		return "badValue"
	case ReadOnly:
		return "readOnly"
	case GenErr:
		return "genErr"
	case NoAccess:
		return "noAccess"
	case WrongType:
		return "wrongType"
	case WrongLength:
		return "wrongLength"
	case WrongEncoding:
		return "wrongEncoding"
	case WrongValue:
		return "wrongValue"
	case NoCreation:
		return "noCreation"
	case InconsistentValue:
		return "inconsistentValue"
	case ResourceUnavailable:
		return "resourceUnavailable"
	case CommitFailed:
		return "commitFailed"
	case UndoFailed:
		return "undoFailed"
	case AuthorizationError:
		return "authorizationError"
	case NotWritable:
		return "notWritable"
	case InconsistentName:
		return "inconsistentName"
	}
	return fmt.Sprintf("PDUErrorStatus(%d)", int(s))
}

// PDUError reports a per-PDU agent-side failure. Status carries the SMI
// error-status code, Index identifies the offending VarBind position in
// the request (1-based as on the wire; 0 if not applicable), and OID
// records the OID associated with that index when the Session translator
// can recover it.
//
// Multiple PDUErrors from a multi-OID request are aggregated via
// [errors.Join]; callers extract individual entries with
// [errors.AsType] or [errors.As].
type PDUError struct {
	Status PDUErrorStatus
	Index  int
	OID    OID
}

// Error returns a short diagnostic of the PDU failure.
func (e *PDUError) Error() string {
	if e.OID.Len() > 0 {
		return fmt.Sprintf("PDU error %s at index %d (oid %s)", e.Status, e.Index, e.OID)
	}
	return fmt.Sprintf("PDU error %s at index %d", e.Status, e.Index)
}

// Unwrap returns nil; PDUError is a leaf error and does not wrap a cause.
// Declaring the method explicitly is informative to readers and keeps
// errors.Is/As traversal predictable.
func (e *PDUError) Unwrap() error { return nil }

// ErrException is the sentinel returned by typed decoders (the well-known
// TC decoders in tc.go) when invoked on one of the three SNMPv2 exception
// VarBind variants. The exceptions are values on the wire; this
// sentinel exists to surface them when a caller specifically asked for a
// typed Go value and got an exception instead.
var ErrException = ae.Msg("VarBind is an SNMPv2 exception variant")

// ErrTypeMismatch is the sentinel returned by typed decoders when invoked
// on a VarBind variant whose wire type does not match the decoder's
// expectation (for example DecodeTruthValue on an OctetString) — i.e.
// the source variant is structurally outside the decoder's accept set.
//
// Callers branching on this sentinel as a general decode-failure gate
// should also branch on [ErrLossyConversion] for complete coverage:
// the leniency helpers distinguish "agent emitted the wrong variant"
// (this sentinel) from "agent emitted a value outside the target Go
// type's representable range" (ErrLossyConversion).
var ErrTypeMismatch = ae.Msg("VarBind variant does not match decoder")

// ErrLossyConversion is the sentinel returned by the leniency helpers in
// decode.go when the source VarBind's variant is within the decoder's
// accept set but the carried value cannot be represented in the target
// Go type without information loss — for example DecodeInt32 invoked on
// a Uinteger32Var carrying math.MaxInt32+1, or DecodeUint32 invoked on a
// negative Integer32Var. Distinct from [ErrTypeMismatch] (which signals
// a fundamentally wrong variant) so callers branching on the failure
// class can tell "agent emitted the wrong type" apart from "agent
// emitted a value that would truncate or change sign".
//
// Callers that previously branched only on [ErrTypeMismatch] should
// also branch on this sentinel — the leniency helpers now surface
// out-of-range values as ErrLossyConversion rather than rejecting them
// as ErrTypeMismatch, so the pre-leniency catch-all branch is
// incomplete on its own.
//
// The error message carries both the source variant (%T) and the
// offending value so operators can see exactly what came off the wire.
var ErrLossyConversion = ae.Msg("VarBind value would lose information when converted to target Go type")
