package snmp

// Kind identifies the wire type carried by a [VarBind]. Each Kind value
// corresponds one-to-one with a concrete VarBind variant; the mapping is
// documented on each variant.
//
// The zero value [KindUnknown] is reserved so that an uninitialized
// [Header] is distinguishable from a header that intentionally carries a
// known type. It is not a valid SNMP wire type.
type Kind int

const (
	// KindUnknown is the zero value, used to detect uninitialized headers.
	KindUnknown Kind = iota
	// KindInteger32 is the SMIv2 Integer32 type.
	KindInteger32
	// KindUinteger32 is the SMIv2 Unsigned32 type (Asn1BER 0x47).
	KindUinteger32
	// KindOctetString is the SMIv2 OCTET STRING type.
	KindOctetString
	// KindObjectID is the SMIv2 OBJECT IDENTIFIER type.
	KindObjectID
	// KindBitString is the ASN.1 BIT STRING type (Asn1BER 0x03). Note that
	// SMIv2 BITS textual convention is encoded on the wire as OCTET STRING;
	// this kind represents the rarely-seen BER BIT STRING form.
	KindBitString
	// KindCounter32 is the SMIv2 Counter32 type.
	KindCounter32
	// KindGauge32 is the SMIv2 Gauge32 type.
	KindGauge32
	// KindTimeTicks is the SMIv2 TimeTicks type, in hundredths of a second.
	KindTimeTicks
	// KindCounter64 is the SMIv2 Counter64 type.
	KindCounter64
	// KindIPAddress is the SMIv2 IpAddress type.
	KindIPAddress
	// KindNsapAddress is the ASN.1 NSAP Address type (Asn1BER 0x45).
	KindNsapAddress
	// KindOpaque is the SMIv2 Opaque type.
	KindOpaque
	// KindOpaqueFloat is the RFC 2856 Opaque-wrapped IEEE-754 float
	// (Asn1BER 0x78).
	KindOpaqueFloat
	// KindOpaqueDouble is the RFC 2856 Opaque-wrapped IEEE-754 double
	// (Asn1BER 0x79).
	KindOpaqueDouble
	// KindNull is the ASN.1 NULL type.
	KindNull
	// KindNoSuchObject is the SNMPv2 exception indicating the requested
	// object does not exist in the agent's MIB view.
	KindNoSuchObject
	// KindNoSuchInstance is the SNMPv2 exception indicating the object
	// exists but no instance is available at the requested index.
	KindNoSuchInstance
	// KindEndOfMibView is the SNMPv2 exception indicating GetNext/GetBulk
	// has walked past the end of the agent's MIB view.
	KindEndOfMibView
)

// String returns the Kind's identifier in a form suitable for diagnostics.
func (k Kind) String() string {
	switch k {
	case KindUnknown:
		return "Unknown"
	case KindInteger32:
		return "Integer32"
	case KindUinteger32:
		return "Unsigned32"
	case KindOctetString:
		return "OctetString"
	case KindObjectID:
		return "ObjectIdentifier"
	case KindBitString:
		return "BitString"
	case KindCounter32:
		return "Counter32"
	case KindGauge32:
		return "Gauge32"
	case KindTimeTicks:
		return "TimeTicks"
	case KindCounter64:
		return "Counter64"
	case KindIPAddress:
		return "IpAddress"
	case KindNsapAddress:
		return "NsapAddress"
	case KindOpaque:
		return "Opaque"
	case KindOpaqueFloat:
		return "OpaqueFloat"
	case KindOpaqueDouble:
		return "OpaqueDouble"
	case KindNull:
		return "Null"
	case KindNoSuchObject:
		return "NoSuchObject"
	case KindNoSuchInstance:
		return "NoSuchInstance"
	case KindEndOfMibView:
		return "EndOfMibView"
	}
	return "Kind(?)"
}
