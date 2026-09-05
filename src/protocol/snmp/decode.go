package snmp

import (
	"math"
	"net"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Wire-type leniency helpers
//
// The helpers in this file (DecodeInt32, DecodeUint32, DecodeUint64,
// DecodeBytes, DecodeOID, DecodeIP) implement a runtime coercion policy
// that lets generated decoders accept any [VarBind] variant whose value
// is losslessly convertible to the target Go type. Real SNMP agents
// routinely emit slightly off-spec variants — a Gauge32 column comes
// back as Integer32, a sysServices INTEGER (0..127) comes back as
// Uinteger32 — and the strict per-variant assertions previously
// generated into every decoder treat that as a hard mismatch even
// though the wire value fits cleanly.
//
// Coercion table (target Go type → accepted wire variants → lossy when):
//
//	int32   / KindInteger32       Integer32Var                                            (never — natural match)
//	                              Uinteger32Var / Gauge32Var / Counter32Var / TimeTicksVar  value > math.MaxInt32
//
//	uint32  / KindUinteger32      Uinteger32Var / Gauge32Var / Counter32Var / TimeTicksVar  (never — all 32-bit unsigned)
//	(Gauge32 / Counter32 /         Integer32Var                                              value < 0
//	 TimeTicks share this
//	 universe)
//
//	uint64  / KindCounter64       Counter64Var                                            (never — natural match)
//	                              Uinteger32Var / Gauge32Var / Counter32Var / TimeTicksVar  (never — widen)
//	                              Integer32Var                                            value < 0
//
//	[]byte  / KindOctetString     OctetStringVar, OpaqueVar                               (never — both carry raw bytes)
//
//	snmp.OID / KindObjectID       ObjectIDVar                                             (never — natural match;
//	                                                                                       no coercion across
//	                                                                                       structural types)
//
//	net.IP   / KindIPAddress      IPAddressVar                                            (never — natural match;
//	                                                                                       no coercion)
//
// Closed-policy rows. The variants BitStringVar, NsapAddressVar,
// OpaqueFloatVar, OpaqueDoubleVar, and NullVar are deliberately not
// accepted by any helper — they return [ErrTypeMismatch]. OpaqueFloat /
// OpaqueDouble in particular are reachable from vendor sensor MIBs, but
// no helper coerces them: a caller that needs one matches the variant
// directly.
//
// Error discrimination:
//
//   - [ErrException] is returned when the source variant is an SNMPv2
//     exception (NoSuchObject / NoSuchInstance / EndOfMibView). Callers
//     specifically interested in "the agent says this OID does not
//     exist" branch on this sentinel.
//   - [ErrTypeMismatch] is returned when the source variant is
//     structurally outside the helper's accept set.
//   - [ErrLossyConversion] is returned when the source variant is
//     accepted but the carried value cannot fit the target without
//     truncation or sign loss. Error messages carry both the source
//     variant (%T) and the offending value.

// DecodeInt32 returns vb's value coerced to int32. See the coercion
// table at the top of this file for accepted variants and lossy
// conditions.
func DecodeInt32(vb VarBind) (int32, error) {
	if IsException(vb) {
		return 0, errs.Wrapf(ErrException, "%s", vb.GetHeader().Kind)
	}
	switch v := vb.(type) {
	case Integer32Var:
		return v.Value, nil
	case Uinteger32Var:
		if v.Value > math.MaxInt32 {
			return 0, errs.Wrapf(ErrLossyConversion, "%T value %d exceeds math.MaxInt32", v, v.Value)
		}
		return int32(v.Value), nil
	case Gauge32Var:
		if v.Value > math.MaxInt32 {
			return 0, errs.Wrapf(ErrLossyConversion, "%T value %d exceeds math.MaxInt32", v, v.Value)
		}
		return int32(v.Value), nil
	case Counter32Var:
		if v.Value > math.MaxInt32 {
			return 0, errs.Wrapf(ErrLossyConversion, "%T value %d exceeds math.MaxInt32", v, v.Value)
		}
		return int32(v.Value), nil
	case TimeTicksVar:
		if v.Value > math.MaxInt32 {
			return 0, errs.Wrapf(ErrLossyConversion, "%T value %d exceeds math.MaxInt32", v, v.Value)
		}
		return int32(v.Value), nil
	default:
		return 0, errs.Wrapf(ErrTypeMismatch, "got %T", vb)
	}
}

// DecodeUint32 returns vb's value coerced to uint32. See the coercion
// table at the top of this file for accepted variants and lossy
// conditions.
func DecodeUint32(vb VarBind) (uint32, error) {
	if IsException(vb) {
		return 0, errs.Wrapf(ErrException, "%s", vb.GetHeader().Kind)
	}
	switch v := vb.(type) {
	case Uinteger32Var:
		return v.Value, nil
	case Gauge32Var:
		return v.Value, nil
	case Counter32Var:
		return v.Value, nil
	case TimeTicksVar:
		return v.Value, nil
	case Integer32Var:
		if v.Value < 0 {
			return 0, errs.Wrapf(ErrLossyConversion, "%T value %d is negative", v, v.Value)
		}
		return uint32(v.Value), nil
	default:
		return 0, errs.Wrapf(ErrTypeMismatch, "got %T", vb)
	}
}

// DecodeUint64 returns vb's value coerced to uint64. See the coercion
// table at the top of this file for accepted variants and lossy
// conditions. Widening from any 32-bit variant is never lossy.
func DecodeUint64(vb VarBind) (uint64, error) {
	if IsException(vb) {
		return 0, errs.Wrapf(ErrException, "%s", vb.GetHeader().Kind)
	}
	switch v := vb.(type) {
	case Counter64Var:
		return v.Value, nil
	case Uinteger32Var:
		return uint64(v.Value), nil
	case Gauge32Var:
		return uint64(v.Value), nil
	case Counter32Var:
		return uint64(v.Value), nil
	case TimeTicksVar:
		return uint64(v.Value), nil
	case Integer32Var:
		if v.Value < 0 {
			return 0, errs.Wrapf(ErrLossyConversion, "%T value %d is negative", v, v.Value)
		}
		return uint64(v.Value), nil
	default:
		return 0, errs.Wrapf(ErrTypeMismatch, "got %T", vb)
	}
}

// DecodeBytes returns vb's value as a defensive copy of the carried
// byte slice. Both OctetStringVar (the natural []byte carrier) and
// OpaqueVar (raw Opaque payload) are accepted.
//
// The returned slice never aliases the source VarBind's backing
// array — gosnmp can recycle packet buffers, so sharing storage
// across decode calls is a silent-corruption shape we explicitly
// rule out. Callers may safely mutate the returned slice.
func DecodeBytes(vb VarBind) ([]byte, error) {
	if IsException(vb) {
		return nil, errs.Wrapf(ErrException, "%s", vb.GetHeader().Kind)
	}
	switch v := vb.(type) {
	case OctetStringVar:
		out := make([]byte, len(v.Value))
		copy(out, v.Value)
		return out, nil
	case OpaqueVar:
		out := make([]byte, len(v.Value))
		copy(out, v.Value)
		return out, nil
	default:
		return nil, errs.Wrapf(ErrTypeMismatch, "got %T", vb)
	}
}

// DecodeOID returns vb's value as an [OID]. Only ObjectIDVar is
// accepted — OID is a structural type and the leniency policy does not
// coerce across structural boundaries.
func DecodeOID(vb VarBind) (OID, error) {
	if IsException(vb) {
		return OID{}, errs.Wrapf(ErrException, "%s", vb.GetHeader().Kind)
	}
	if v, ok := vb.(ObjectIDVar); ok {
		return v.Value, nil
	}
	return OID{}, errs.Wrapf(ErrTypeMismatch, "got %T", vb)
}

// DecodeIP returns vb's value as a [net.IP]. Only IPAddressVar is
// accepted — IP is a structural type and the leniency policy does not
// coerce across structural boundaries.
func DecodeIP(vb VarBind) (net.IP, error) {
	if IsException(vb) {
		return nil, errs.Wrapf(ErrException, "%s", vb.GetHeader().Kind)
	}
	if v, ok := vb.(IPAddressVar); ok {
		return v.Value, nil
	}
	return nil, errs.Wrapf(ErrTypeMismatch, "got %T", vb)
}
