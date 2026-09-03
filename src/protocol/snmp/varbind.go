package snmp

import "net"

// Header is the shared metadata of every [VarBind] variant: the object
// identifier the binding refers to and the wire-level [Kind] it carries.
//
// Concrete VarBind variants embed Header so that callers can read OID and
// Kind without a type switch. The pattern mirrors miekg/dns RR_Header.
type Header struct {
	OID  OID
	Kind Kind
}

// VarBind is the sealed sum of every SNMP variable binding value FlowSeer
// recognizes: the fifteen value-bearing SMIv2 base types plus the three
// SNMPv2 exception variants ([NoSuchObjectVar], [NoSuchInstanceVar],
// [EndOfMibViewVar]).
//
// Implementations are restricted to the variants defined in this file; the
// unexported sealedVarBind method prevents third-party types from satisfying
// the interface, and the //sumtype:decl directive lets the
// alecthomas/go-check-sumtype linter enforce exhaustive type switches.
//
//sumtype:decl
type VarBind interface {
	sealedVarBind()
	// GetHeader returns the variant's embedded Header by value.
	GetHeader() Header
}

// Integer32Var carries an SMIv2 Integer32 value.
type Integer32Var struct {
	Header
	Value int32
}

func (Integer32Var) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v Integer32Var) GetHeader() Header { return v.Header }

// Uinteger32Var carries an SMIv2 Unsigned32 value.
type Uinteger32Var struct {
	Header
	Value uint32
}

func (Uinteger32Var) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v Uinteger32Var) GetHeader() Header { return v.Header }

// OctetStringVar carries an SMIv2 OCTET STRING value. A nil value is the
// zero-length string; consumers should not distinguish nil from an empty
// slice on this type.
type OctetStringVar struct {
	Header
	Value []byte
}

func (OctetStringVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v OctetStringVar) GetHeader() Header { return v.Header }

// ObjectIDVar carries an SMIv2 OBJECT IDENTIFIER value.
type ObjectIDVar struct {
	Header
	Value OID
}

func (ObjectIDVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v ObjectIDVar) GetHeader() Header { return v.Header }

// BitStringVar carries an ASN.1 BIT STRING value (Asn1BER 0x03). The SMIv2
// BITS textual convention is encoded as OCTET STRING on the wire; use
// [OctetStringVar] for that case.
type BitStringVar struct {
	Header
	Value []byte
}

func (BitStringVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v BitStringVar) GetHeader() Header { return v.Header }

// Counter32Var carries an SMIv2 Counter32 value.
type Counter32Var struct {
	Header
	Value uint32
}

func (Counter32Var) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v Counter32Var) GetHeader() Header { return v.Header }

// Gauge32Var carries an SMIv2 Gauge32 value.
type Gauge32Var struct {
	Header
	Value uint32
}

func (Gauge32Var) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v Gauge32Var) GetHeader() Header { return v.Header }

// TimeTicksVar carries an SMIv2 TimeTicks value, measured in hundredths
// of a second.
type TimeTicksVar struct {
	Header
	Value uint32
}

func (TimeTicksVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v TimeTicksVar) GetHeader() Header { return v.Header }

// Counter64Var carries an SMIv2 Counter64 value.
type Counter64Var struct {
	Header
	Value uint64
}

func (Counter64Var) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v Counter64Var) GetHeader() Header { return v.Header }

// IPAddressVar carries an SMIv2 IpAddress value. Backends are expected to
// hand back a 4-byte IPv4 slice; the [net.IP] type accommodates both forms
// so the field type is broader than the wire type.
type IPAddressVar struct {
	Header
	Value net.IP
}

func (IPAddressVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v IPAddressVar) GetHeader() Header { return v.Header }

// NsapAddressVar carries an ASN.1 NSAP Address value (Asn1BER 0x45). The
// payload is the raw NSAP wire form; FlowSeer does not interpret it.
type NsapAddressVar struct {
	Header
	Value []byte
}

func (NsapAddressVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v NsapAddressVar) GetHeader() Header { return v.Header }

// OpaqueVar carries an SMIv2 Opaque value. The payload is opaque to
// FlowSeer; the RFC 2856 wrapped float/double forms are surfaced as
// [OpaqueFloatVar] / [OpaqueDoubleVar] instead.
type OpaqueVar struct {
	Header
	Value []byte
}

func (OpaqueVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v OpaqueVar) GetHeader() Header { return v.Header }

// OpaqueFloatVar carries an RFC 2856 Opaque-wrapped IEEE-754 float
// (Asn1BER 0x78).
type OpaqueFloatVar struct {
	Header
	Value float32
}

func (OpaqueFloatVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v OpaqueFloatVar) GetHeader() Header { return v.Header }

// OpaqueDoubleVar carries an RFC 2856 Opaque-wrapped IEEE-754 double
// (Asn1BER 0x79).
type OpaqueDoubleVar struct {
	Header
	Value float64
}

func (OpaqueDoubleVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v OpaqueDoubleVar) GetHeader() Header { return v.Header }

// NullVar carries an ASN.1 NULL value. NULL appears in outbound Get-style
// PDUs as a placeholder; on receive it is rare but legal.
type NullVar struct {
	Header
}

func (NullVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v NullVar) GetHeader() Header { return v.Header }

// NoSuchObjectVar is the SNMPv2 exception variant indicating the requested
// object is not present in the agent's MIB view. It is a [VarBind] value,
// not an error.
type NoSuchObjectVar struct {
	Header
}

func (NoSuchObjectVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v NoSuchObjectVar) GetHeader() Header { return v.Header }

// NoSuchInstanceVar is the SNMPv2 exception variant indicating the requested
// object exists but no instance is available at the requested index. It is
// a [VarBind] value, not an error.
type NoSuchInstanceVar struct {
	Header
}

func (NoSuchInstanceVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v NoSuchInstanceVar) GetHeader() Header { return v.Header }

// EndOfMibViewVar is the SNMPv2 exception variant returned by GetNext or
// GetBulk after the last OID in the agent's MIB view. It is a [VarBind]
// value, not an error.
type EndOfMibViewVar struct {
	Header
}

func (EndOfMibViewVar) sealedVarBind() {}

// GetHeader implements [VarBind].
func (v EndOfMibViewVar) GetHeader() Header { return v.Header }

// IsException reports whether v is one of the three SNMPv2 exception
// variants ([NoSuchObjectVar], [NoSuchInstanceVar], [EndOfMibViewVar]).
// Decoders that expect a value-bearing variant should check this first.
func IsException(v VarBind) bool {
	// A default case keeps the go-check-sumtype linter happy: the switch
	// intentionally cares only about the three exception variants, so the
	// default branch documents "everything else is fine".
	switch v.(type) {
	case NoSuchObjectVar, NoSuchInstanceVar, EndOfMibViewVar:
		return true
	default:
		return false
	}
}
