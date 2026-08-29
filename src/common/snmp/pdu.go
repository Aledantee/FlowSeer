package snmp

import (
	"math"
	"net"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// pdu.go is the structured PDU model: the single decoded
// representation of an SNMP v1/v2c message that feeds three consumers —
// translate.go (→ [VarBind] / [PDUError]), the instrumentation
// layer (span/metric attributes), and the differential/replay fixtures.
// Producing that one model also covers value-level decoding, so the
// tag→variant mapping (decodeValue) and its inverse (encodeVarBind) live
// here rather than being duplicated.

// pduType is the BER identifier octet of an SNMP PDU. These are
// context-specific constructed tags (class 0b10, constructed 0x20), so
// PDU n is 0x80|0x20|n = 0xA0|n.
//
// Note on the v1 Trap-PDU: RFC 1157 refers to it by its context tag
// *number* 4 ("0x04"); the identifier octet on the wire is 0xA4. The v2c
// SNMPv2-Trap-PDU is tag number 7 → 0xA7. The pinning test fixes both.
type pduType byte

const (
	pduGetRequest     pduType = 0xA0 // [0] GetRequest-PDU
	pduGetNextRequest pduType = 0xA1 // [1] GetNextRequest-PDU
	pduGetResponse    pduType = 0xA2 // [2] GetResponse-PDU (Response-PDU)
	pduSetRequest     pduType = 0xA3 // [3] SetRequest-PDU
	pduV1Trap         pduType = 0xA4 // [4] Trap-PDU (SNMPv1)
	pduGetBulkRequest pduType = 0xA5 // [5] GetBulkRequest-PDU
	pduInformRequest  pduType = 0xA6 // [6] InformRequest-PDU
	pduV2Trap         pduType = 0xA7 // [7] SNMPv2-Trap-PDU
	pduReport         pduType = 0xA8 // [8] Report-PDU
)

// SNMP version wire codes (RFC 1157 / RFC 3416). v3 (3) is handled by the
// parallel envelope in v3msg.go; decodeAnyMessage routes it there, and this
// v1/v2c codec rejects it via versionFromWire.
const (
	wireVersionV1  = 0
	wireVersionV2c = 1
	wireVersionV3  = 3
)

// PDU model sentinel errors.
var (
	// errUnsupportedVersion is returned by [decodeMessage] when the
	// version octet is neither v1 nor v2c. It is checked before community
	// or varbind decode so a v3 message (or garbage) is rejected up front
	// rather than mis-parsed under the v1/v2c structure.
	errUnsupportedVersion = errs.Msg("unsupported message version")
	// errMalformedPDU is the umbrella sentinel for a structurally invalid
	// PDU (wrong field shape, missing varbind list). Specific causes are
	// wrapped with context.
	errMalformedPDU = errs.Msg("malformed PDU")
)

// message is the decoded v1/v2c SNMP message envelope: SEQUENCE { version,
// community, pdu }. v3's distinct envelope is out of scope; [decodeMessage]
// rejects it via [errUnsupportedVersion].
type message struct {
	version   Version
	community string
	pdu       pdu

	// wantRaw marks a request whose walk wants the reply's varbind list
	// delivered raw (undecoded) on the fast path. Set only by the
	// raw walk engine; never encoded on the wire. The reactor's read
	// loop consults the registered waiter's flag and, when raw delivery
	// is possible, stores the list in pdu.rawVBL instead of decoding.
	wantRaw bool

	// warnings carries non-fatal decode diagnostics: protocol violations that
	// are tolerated (data preserved) rather than rejected, so the codec never
	// silently swallows a spec breach. Populated by decodeMessage and logged by
	// the reactor read-loop on delivery. Currently the only entry is the
	// Counter64-in-v1 violation (RFC 2576 §3 forbids Counter64 in an SNMPv1
	// message). Nil for a clean decode.
	warnings []error
}

// pdu is a decoded SNMP PDU. The same struct represents request and
// response PDUs of the standard shape; trapV1 is non-nil only for a v1
// Trap-PDU, which carries distinct fixed fields instead of the
// request-id/error-status/error-index triple.
type pdu struct {
	typ       pduType
	requestID int32

	// errorStatus / errorIndex are populated for standard non-bulk PDUs.
	errorStatus PDUErrorStatus
	errorIndex  int

	// nonRepeaters / maxRepetitions reuse the error-status/error-index
	// wire slots for a GetBulkRequest-PDU.
	nonRepeaters   int
	maxRepetitions int

	varbinds []VarBind

	// rawVBL carries the undecoded VarBindList TLV of a response
	// delivered on the raw fast path: the read loop clones the
	// region and skips decodeVarBindList entirely; the raw walk engine
	// iterates the TLVs in place. Exactly one of varbinds / rawVBL is
	// populated on a delivered response; both are nil on requests.
	// rawVBL responses have passed [validateRawVarBindList], so the
	// deferred per-varbind decode cannot fail on anything the eager
	// path would have rejected.
	rawVBL []byte

	// trapV1 carries the v1 Trap-PDU fixed fields; non-nil iff typ is
	// pduV1Trap.
	trapV1 *trapV1Fields
}

// trapV1Fields holds the fixed fields unique to an SNMPv1 Trap-PDU
// (RFC 1157 §4.1.6).
type trapV1Fields struct {
	enterprise OID
	agentAddr  net.IP
	generic    int
	specific   int
	timestamp  uint32
}

// decodeMessage decodes a complete v1/v2c SNMP message datagram into the
// structured model. The version octet is read and validated first, so a
// v3 or malformed datagram is rejected via [errUnsupportedVersion] before
// any community or PDU decode. Every length and bounds check is
// inherited from the ber.go primitives, so a malformed datagram yields a
// typed error, never a panic.
func decodeMessage(buf []byte) (*message, error) {
	m, rawVBL, err := decodeMessageHeader(buf)
	if err != nil {
		return nil, err
	}
	if m.pdu.trapV1 == nil {
		vbs, err := decodeVarBindList(rawVBL, 1)
		if err != nil {
			return nil, err
		}
		m.pdu.varbinds = vbs
	}
	m.warnings = scanCounter64Warnings(m.version, m.pdu.varbinds)
	return m, nil
}

// decodeMessageHeader decodes the message envelope and PDU fixed fields,
// leaving a standard PDU's varbind list encoded (returned as rawVBL,
// aliasing buf). It is the demux half of [decodeMessage]: the read loop
// uses it to route by request-id before deciding whether to decode the
// varbinds eagerly or deliver them raw. A v1 Trap-PDU has no
// standard varbind-list tail structure worth deferring and is decoded in
// full (rawVBL nil, pdu.trapV1 set).
func decodeMessageHeader(buf []byte) (*message, []byte, error) {
	body, _, err := parseSequence(buf, tagSequence, 0)
	if err != nil {
		return nil, nil, errs.Wrap(err, "decode message envelope")
	}

	// version INTEGER — validated before anything else.
	vTag, vContent, vUsed, err := parseTLV(body)
	if err != nil {
		return nil, nil, errs.Wrap(err, "decode version")
	}
	if vTag != tagInteger {
		return nil, nil, errs.Wrapf(errMalformedPDU, "decode version: tag 0x%02x", vTag)
	}
	verRaw, err := decodeSignedInt(vContent)
	if err != nil {
		return nil, nil, errs.Wrap(err, "decode version")
	}
	ver, err := versionFromWire(verRaw)
	if err != nil {
		return nil, nil, err
	}

	// community OCTET STRING.
	cTag, cContent, cUsed, err := parseTLV(body[vUsed:])
	if err != nil {
		return nil, nil, errs.Wrap(err, "decode community")
	}
	if cTag != tagOctetString {
		return nil, nil, errs.Wrapf(errMalformedPDU, "decode community: tag 0x%02x", cTag)
	}
	m := &message{version: ver, community: string(cContent)}

	// pdu fixed fields.
	tag, content, _, err := parseTLV(body[vUsed+cUsed:])
	if err != nil {
		return nil, nil, errs.Wrap(err, "decode PDU header")
	}
	typ := pduType(tag)
	if typ == pduV1Trap {
		p, err := decodeV1TrapPDU(content, 1)
		if err != nil {
			return nil, nil, err
		}
		m.pdu = p
		return m, nil, nil
	}
	p, rawVBL, err := decodeStandardPDUHeader(typ, content)
	if err != nil {
		return nil, nil, err
	}
	m.pdu = p
	return m, rawVBL, nil
}

// scanCounter64Warnings implements the enc-counter64-v1 tolerance
// policy: RFC 2576 §3 forbids Counter64 in an SNMPv1 message, devices
// emit it anyway, so the value is preserved and the breach is recorded
// as a non-fatal warning — never silently swallowed, never a panic.
func scanCounter64Warnings(ver Version, vbs []VarBind) []error {
	if ver != V1 {
		return nil
	}
	var warns []error
	for _, vb := range vbs {
		if vb.GetHeader().Kind == KindCounter64 {
			warns = append(warns, errs.Wrapf(warnCounter64InV1, "at %s", vb.GetHeader().OID))
		}
	}
	return warns
}

// warnCounter64InV1 is the non-fatal decode warning recorded when an SNMPv1
// message carries a Counter64 varbind (RFC 2576 §3 forbids it). It is a
// warning, not an error: the value is decoded and preserved.
var warnCounter64InV1 = errs.Msg("snmp: Counter64 in an SNMPv1 message (RFC 2576 §3)")

// versionFromWire maps the wire version code to [Version], rejecting
// v3 and unknown codes with [errUnsupportedVersion] (the value is carried
// in the wrapped message for the trap-drop path).
func versionFromWire(raw int64) (Version, error) {
	switch raw {
	case wireVersionV1:
		return V1, nil
	case wireVersionV2c:
		return V2c, nil
	default:
		return VersionUnset, errs.Wrapf(errUnsupportedVersion, "version code %d", raw)
	}
}

// decodePDU decodes the PDU TLV at the front of buf at the given nesting
// depth. The v1 Trap-PDU takes the fixed-field path; every other PDU type
// (requests, response, GetBulk, v2c trap, inform) takes the standard
// request-id/field2/field3/varbinds path.
func decodePDU(buf []byte, depth int) (pdu, error) {
	tag, content, _, err := parseTLV(buf)
	if err != nil {
		return pdu{}, errs.Wrap(err, "decode PDU header")
	}
	typ := pduType(tag)
	if typ == pduV1Trap {
		return decodeV1TrapPDU(content, depth)
	}
	return decodeStandardPDU(typ, content, depth)
}

// decodeStandardPDU decodes the standard PDU body shape: request-id,
// field2 (error-status or non-repeaters), field3 (error-index or
// max-repetitions), then the varbind list.
func decodeStandardPDU(typ pduType, content []byte, depth int) (pdu, error) {
	p, rawVBL, err := decodeStandardPDUHeader(typ, content)
	if err != nil {
		return pdu{}, err
	}
	vbs, err := decodeVarBindList(rawVBL, depth)
	if err != nil {
		return pdu{}, err
	}
	p.varbinds = vbs
	return p, nil
}

// decodeStandardPDUHeader decodes the fixed fields of a standard PDU and
// returns the still-encoded VarBindList TLV (aliasing content). It is
// the header half of [decodeStandardPDU], split out so the read loop can
// demux by request-id — and deliver the list raw to a raw-flagged waiter —
// without paying the per-varbind decode.
func decodeStandardPDUHeader(typ pduType, content []byte) (pdu, []byte, error) {
	reqID, rest, err := expectInt(content, "request-id")
	if err != nil {
		return pdu{}, nil, err
	}
	if reqID < math.MinInt32 || reqID > math.MaxInt32 {
		return pdu{}, nil, errs.Wrapf(errMalformedPDU, "request-id %d out of int32 range", reqID)
	}
	field2, rest, err := expectInt(rest, "error-status/non-repeaters")
	if err != nil {
		return pdu{}, nil, err
	}
	field3, rest, err := expectInt(rest, "error-index/max-repetitions")
	if err != nil {
		return pdu{}, nil, err
	}
	p := pdu{typ: typ, requestID: int32(reqID)}
	if typ == pduGetBulkRequest {
		p.nonRepeaters = int(field2)
		p.maxRepetitions = int(field3)
	} else {
		p.errorStatus = PDUErrorStatus(field2)
		p.errorIndex = int(field3)
	}
	return p, rest, nil
}

// decodeV1TrapPDU decodes the SNMPv1 Trap-PDU fixed fields followed by the
// varbind list (RFC 1157 §4.1.6).
func decodeV1TrapPDU(content []byte, depth int) (pdu, error) {
	// enterprise OID.
	tag, oidContent, used, err := parseTLV(content)
	if err != nil {
		return pdu{}, errs.Wrap(err, "v1 trap: enterprise")
	}
	if tag != tagOID {
		return pdu{}, errs.Wrapf(errMalformedPDU, "v1 trap enterprise tag 0x%02x", tag)
	}
	enterprise, err := decodeOID(oidContent)
	if err != nil {
		return pdu{}, errs.Wrap(err, "v1 trap: enterprise OID")
	}
	rest := content[used:]

	// agent-addr IpAddress.
	tag, addrContent, used, err := parseTLV(rest)
	if err != nil {
		return pdu{}, errs.Wrap(err, "v1 trap: agent-addr")
	}
	if tag != tagIPAddress {
		return pdu{}, errs.Wrapf(errMalformedPDU, "v1 trap agent-addr tag 0x%02x", tag)
	}
	agentAddr, err := decodeIPv4(addrContent)
	if err != nil {
		return pdu{}, errs.Wrap(err, "v1 trap: agent-addr")
	}
	rest = rest[used:]

	generic, rest, err := expectInt(rest, "v1 trap generic-trap")
	if err != nil {
		return pdu{}, err
	}
	specific, rest, err := expectInt(rest, "v1 trap specific-trap")
	if err != nil {
		return pdu{}, err
	}

	// time-stamp TimeTicks (unsigned).
	tag, tsContent, used, err := parseTLV(rest)
	if err != nil {
		return pdu{}, errs.Wrap(err, "v1 trap: time-stamp")
	}
	if tag != tagTimeTicks && tag != tagInteger {
		return pdu{}, errs.Wrapf(errMalformedPDU, "v1 trap time-stamp tag 0x%02x", tag)
	}
	ts, err := decodeUnsigned(tsContent)
	if err != nil {
		return pdu{}, errs.Wrap(err, "v1 trap: time-stamp")
	}
	rest = rest[used:]

	vbs, err := decodeVarBindList(rest, depth)
	if err != nil {
		return pdu{}, err
	}
	return pdu{
		typ:      pduV1Trap,
		varbinds: vbs,
		trapV1: &trapV1Fields{
			enterprise: enterprise,
			agentAddr:  agentAddr,
			generic:    int(generic),
			specific:   int(specific),
			timestamp:  uint32(ts),
		},
	}, nil
}

// expectInt reads an INTEGER TLV from the front of buf, returning its
// value and the remaining bytes. label names the field for diagnostics.
func expectInt(buf []byte, label string) (val int64, rest []byte, err error) {
	tag, content, used, err := parseTLV(buf)
	if err != nil {
		return 0, nil, errs.Wrapf(err, "decode %s", label)
	}
	if tag != tagInteger {
		return 0, nil, errs.Wrapf(errMalformedPDU, "decode %s: tag 0x%02x", label, tag)
	}
	v, err := decodeSignedInt(content)
	if err != nil {
		return 0, nil, errs.Wrapf(err, "decode %s", label)
	}
	return v, buf[used:], nil
}

// decodeVarBindList decodes the SEQUENCE OF VarBind at the front of buf
// into typed [VarBind] values. A zero-length OID in any entry yields
// a sentinel-OID varbind rather than aborting the list.
func decodeVarBindList(buf []byte, depth int) ([]VarBind, error) {
	listContent, _, err := parseSequence(buf, tagSequence, depth)
	if err != nil {
		return nil, errs.Wrap(err, "decode varbind list")
	}
	// Pre-size from the encoded length: a varbind is at least ~10 octets
	// (SEQUENCE header + a short OID + a value), so listContent/10 is a
	// conservative lower bound that avoids most regrow copies without
	// over-allocating on a malformed short list.
	out := make([]VarBind, 0, max(1, len(listContent)/10))
	for len(listContent) > 0 {
		vb, used, err := decodeVarBind(listContent, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, vb)
		listContent = listContent[used:]
	}
	return out, nil
}

// decodeVarBind decodes one VarBind SEQUENCE { name OID, value } into a
// typed [VarBind], returning the number of octets the whole varbind
// TLV consumed in the enclosing list.
func decodeVarBind(buf []byte, depth int) (VarBind, int, error) {
	vbContent, consumed, err := parseSequence(buf, tagSequence, depth)
	if err != nil {
		return nil, 0, errs.Wrap(err, "decode varbind")
	}
	nameTag, nameContent, nameUsed, err := parseTLV(vbContent)
	if err != nil {
		return nil, 0, errs.Wrap(err, "decode varbind name")
	}
	if nameTag != tagOID {
		return nil, 0, errs.Wrapf(errMalformedPDU, "varbind name tag 0x%02x", nameTag)
	}
	oid, err := decodeOID(nameContent)
	if err != nil {
		return nil, 0, errs.Wrap(err, "decode varbind name OID")
	}
	valTag, valContent, _, err := parseTLV(vbContent[nameUsed:])
	if err != nil {
		return nil, 0, errs.Wrap(err, "decode varbind value")
	}
	vb, err := decodeValue(oid, valTag, valContent)
	if err != nil {
		return nil, 0, err
	}
	return vb, consumed, nil
}

// validateRawVarBindList reports whether the encoded VarBindList TLV is
// eligible for raw (undecoded) delivery on the fast path. It walks
// the TLV structure and mirrors every rejection [decodeVarBindList] /
// [decodeValue] would make — so a datagram the eager path would drop is
// equally rejected here, and the deferred per-varbind decode can only
// succeed. The one extra requirement beyond eager-decodability is
// canonical name-OID arc encoding: the raw walk engine compares and
// prefix-matches name OIDs as bytes, which is order-correct only for
// minimal base-128 arcs. A response with a padded (non-canonical) arc —
// off-spec but decodable — returns an error here and the read loop
// falls back to eager decode, preserving today's semantics for that
// agent. The mirror is pinned by TestValidateRawVarBindList_Mirrors in
// pdu_test.go form.
func validateRawVarBindList(buf []byte, depth int) error {
	listContent, _, err := parseSequence(buf, tagSequence, depth)
	if err != nil {
		return errs.Wrap(err, "decode varbind list")
	}
	for len(listContent) > 0 {
		vbContent, consumed, err := parseSequence(listContent, tagSequence, depth+1)
		if err != nil {
			return errs.Wrap(err, "decode varbind")
		}
		nameTag, nameContent, nameUsed, err := parseTLV(vbContent)
		if err != nil {
			return errs.Wrap(err, "decode varbind name")
		}
		if nameTag != tagOID {
			return errs.Wrapf(errMalformedPDU, "varbind name tag 0x%02x", nameTag)
		}
		if err := validateRawNameOID(nameContent); err != nil {
			return err
		}
		valTag, valContent, _, err := parseTLV(vbContent[nameUsed:])
		if err != nil {
			return errs.Wrap(err, "decode varbind value")
		}
		if err := validateRawValue(valTag, valContent); err != nil {
			return err
		}
		listContent = listContent[consumed:]
	}
	return nil
}

// errNonCanonicalOID marks a name OID whose base-128 arcs carry leading
// zero-padding octets (0x80). decodable, but byte order then diverges
// from numeric arc order, so the raw path refuses the response and the
// read loop decodes it eagerly instead.
var errNonCanonicalOID = errs.Msg("non-canonical OID sub-identifier encoding")

// validateRawNameOID checks that a varbind name's content octets decode
// under exactly [decodeOID]'s rules (zero-length sentinel allowed, every
// arc a valid uint32, arc count within the SMIv2 cap) and additionally
// that every arc is minimally encoded (see [errNonCanonicalOID]).
func validateRawNameOID(b []byte) error {
	if len(b) == 0 {
		return nil // zero-length sentinel, accepted like the eager decode
	}
	reads := 0
	for i := 0; i < len(b); {
		if b[i] == 0x80 {
			return errNonCanonicalOID
		}
		_, adv, err := readBase128(b, i)
		if err != nil {
			return err
		}
		i += adv
		reads++
	}
	// The first base-128 value expands to two arcs, so the decoded arc
	// count is reads+1 — the same count [decodeOID] hands to validateSubs.
	if reads+1 > maxOIDComponents {
		return errs.New().Attr("count", reads+1).Attr("max", maxOIDComponents).
			Msg("sub-identifier count exceeds SMIv2 maximum")
	}
	return nil
}

// validateRawValue mirrors [decodeValue]'s accept/reject decision for a
// value TLV without materializing the variant. Any change to
// decodeValue's error surface must be mirrored here (and is pinned by
// the fuzz-style mirror test).
func validateRawValue(tag byte, content []byte) error {
	switch tag {
	case tagInteger:
		v, err := decodeSignedInt(content)
		if err != nil {
			return err
		}
		if v < math.MinInt32 || v > math.MaxInt32 {
			return errIntOverflow
		}
	case tagOctetString, tagBitString, tagNsapAddress, tagNull:
		// Content is cloned verbatim (or ignored, for NULL) — always valid.
	case tagOID:
		if _, err := decodeOID(content); err != nil {
			return err
		}
	case tagIPAddress:
		if len(content) != 4 && len(content) != 16 {
			return errUnexpectedTag
		}
	case tagCounter32, tagGauge32, tagUinteger32:
		v, err := decodeUnsigned(content)
		if err != nil {
			return err
		}
		if v > math.MaxUint32 {
			return errIntOverflow
		}
	case tagTimeTicks, tagCounter64:
		if _, err := decodeUnsigned(content); err != nil {
			return err
		}
	case tagOpaque:
		if _, _, _, err := opaqueReal(content); err != nil {
			return err
		}
	case tagNoSuchObject, tagNoSuchInstance, tagEndOfMibView:
		// Exception markers carry no content constraints.
	default:
		return errUnexpectedTag
	}
	return nil
}

// decodeValue maps a value TLV (tag + content) under name oid to the
// matching [VarBind] variant. SNMPv2 exception tags decode to the
// exception variants (values, not errors); an unknown tag is a
// typed error.
func decodeValue(oid OID, tag byte, content []byte) (VarBind, error) {
	hdr := func(k Kind) Header { return Header{OID: oid, Kind: k} }

	switch tag {
	case tagInteger:
		v, err := decodeSignedInt(content)
		if err != nil {
			return nil, errs.Wrapf(err, "Integer32 at %s", oid)
		}
		if v < math.MinInt32 || v > math.MaxInt32 {
			return nil, errs.Wrapf(errIntOverflow, "Integer32 at %s: value %d out of range", oid, v)
		}
		return Integer32Var{Header: hdr(KindInteger32), Value: int32(v)}, nil

	case tagOctetString:
		return OctetStringVar{Header: hdr(KindOctetString), Value: cloneBytes(content)}, nil

	case tagOID:
		inner, err := decodeOID(content)
		if err != nil {
			return nil, errs.Wrapf(err, "ObjectIdentifier at %s", oid)
		}
		return ObjectIDVar{Header: hdr(KindObjectID), Value: inner}, nil

	case tagBitString:
		return BitStringVar{Header: hdr(KindBitString), Value: cloneBytes(content)}, nil

	case tagNull:
		return NullVar{Header: hdr(KindNull)}, nil

	case tagIPAddress:
		ip, err := decodeIPv4(content)
		if err != nil {
			return nil, errs.Wrapf(err, "IpAddress at %s", oid)
		}
		return IPAddressVar{Header: hdr(KindIPAddress), Value: ip}, nil

	case tagCounter32:
		v, err := decodeUint32(content, oid, "Counter32")
		if err != nil {
			return nil, err
		}
		return Counter32Var{Header: hdr(KindCounter32), Value: v}, nil

	case tagGauge32:
		v, err := decodeUint32(content, oid, "Gauge32")
		if err != nil {
			return nil, err
		}
		return Gauge32Var{Header: hdr(KindGauge32), Value: v}, nil

	case tagTimeTicks:
		// RFC 2578: TimeTicks is a 32-bit value that wraps mod 2^32. Some
		// agents emit 5+ content octets (out-of-range); mask to the low 32
		// bits rather than rejecting — for a wrapping counter the low bits are
		// the meaningful value (enc-timeticks-range). Scoped to TimeTicks;
		// Counter32/Gauge32/Unsigned32 still reject overflow via decodeUint32.
		// decodeUnsigned still rejects a genuinely malformed >8-octet value.
		v, err := decodeUnsigned(content)
		if err != nil {
			return nil, errs.Wrapf(err, "TimeTicks at %s", oid)
		}
		return TimeTicksVar{Header: hdr(KindTimeTicks), Value: uint32(v)}, nil

	case tagUinteger32:
		v, err := decodeUint32(content, oid, "Unsigned32")
		if err != nil {
			return nil, err
		}
		return Uinteger32Var{Header: hdr(KindUinteger32), Value: v}, nil

	case tagCounter64:
		v, err := decodeUnsigned(content)
		if err != nil {
			return nil, errs.Wrapf(err, "Counter64 at %s", oid)
		}
		return Counter64Var{Header: hdr(KindCounter64), Value: v}, nil

	case tagNsapAddress:
		return NsapAddressVar{Header: hdr(KindNsapAddress), Value: cloneBytes(content)}, nil

	case tagOpaque:
		isDouble, ok, f, err := opaqueReal(content)
		if err != nil {
			return nil, errs.Wrapf(err, "Opaque at %s", oid)
		}
		switch {
		case ok && isDouble:
			return OpaqueDoubleVar{Header: hdr(KindOpaqueDouble), Value: f}, nil
		case ok:
			return OpaqueFloatVar{Header: hdr(KindOpaqueFloat), Value: float32(f)}, nil
		default:
			return OpaqueVar{Header: hdr(KindOpaque), Value: cloneBytes(content)}, nil
		}

	case tagNoSuchObject:
		return NoSuchObjectVar{Header: hdr(KindNoSuchObject)}, nil
	case tagNoSuchInstance:
		return NoSuchInstanceVar{Header: hdr(KindNoSuchInstance)}, nil
	case tagEndOfMibView:
		return EndOfMibViewVar{Header: hdr(KindEndOfMibView)}, nil

	default:
		return nil, errs.Wrapf(errUnexpectedTag, "unexpected value tag 0x%02x at %s", tag, oid)
	}
}

// decodeUint32 decodes an unsigned application integer and narrows it to
// uint32, erroring (rather than truncating) on an out-of-range value —
// the wire should never produce one for a 32-bit type, but the check
// turns a corrupt datagram into a typed error.
func decodeUint32(content []byte, oid OID, label string) (uint32, error) {
	v, err := decodeUnsigned(content)
	if err != nil {
		return 0, errs.Wrapf(err, "%s at %s", label, oid)
	}
	if v > math.MaxUint32 {
		return 0, errs.Wrapf(errIntOverflow, "%s at %s: value %d overflows uint32", label, oid, v)
	}
	return uint32(v), nil
}

// cloneBytes returns a non-nil copy of b so the decoded value does not
// alias the wire buffer (whose lifetime the caller owns).
func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// --- Encode -----------------------------------------------------------

// encodeMessage encodes the structured model back to a v1/v2c datagram:
// SEQUENCE { version, community, pdu }. It is used to build request PDUs
// and is the inverse of [decodeMessage] for the round-trip tests and the
// raw-bytes differential path.
func encodeMessage(m *message) ([]byte, error) {
	if fast, ok := encodeRequestFast(m); ok {
		return fast, nil
	}
	pduBytes, err := encodePDU(&m.pdu)
	if err != nil {
		return nil, err
	}
	var body []byte
	body = appendInt(body, int64(wireVersionFor(m.version)))
	body = appendOctetString(body, tagOctetString, []byte(m.community))
	body = append(body, pduBytes...)
	return appendSequence(tagSequence, body), nil
}

// berWrapTail wraps buf[mark:] in a TLV header for tag, inserting the
// header octets in place with a single overlapping copy. It is the
// build-inner-then-wrap primitive behind [encodeRequestFast]: the whole
// message assembles in one buffer instead of one intermediate slice per
// nesting level.
func berWrapTail(buf []byte, mark int, tag byte) []byte {
	n := len(buf) - mark
	var hdr [6]byte
	hdr[0] = tag
	hlen := 2
	if n < 0x80 {
		hdr[1] = byte(n)
	} else {
		var tmp [4]byte
		i, m := 4, n
		for m > 0 {
			i--
			tmp[i] = byte(m)
			m >>= 8
		}
		cnt := 4 - i
		hdr[1] = byte(0x80 | cnt)
		copy(hdr[2:], tmp[i:])
		hlen = 2 + cnt
	}
	buf = append(buf, hdr[:hlen]...)
	copy(buf[mark+hlen:], buf[mark:len(buf)-hlen])
	copy(buf[mark:mark+hlen], hdr[:hlen])
	return buf
}

// encodeRequestFast is the single-buffer encoder for the request shapes
// the Get/Walk hot paths emit: a Get/GetNext/GetBulk PDU whose varbinds
// are all [NullVar]. It produces bytes identical to the general
// [encodePDU]/[encodeMessage] chain (pinned by the equivalence test) in
// one allocation instead of one per nesting level. ok=false means the
// message has some other shape — a Set, a trap, a non-null varbind —
// and the caller must take the general encoder.
func encodeRequestFast(m *message) ([]byte, bool) {
	p := &m.pdu
	switch p.typ {
	case pduGetRequest, pduGetNextRequest, pduGetBulkRequest:
	default:
		return nil, false
	}
	sz := 32 + len(m.community)
	for _, vb := range p.varbinds {
		if _, ok := vb.(NullVar); !ok {
			return nil, false
		}
		sz += 16 + vb.GetHeader().OID.Len()*5
	}
	buf := make([]byte, 0, sz)
	buf = appendInt(buf, int64(wireVersionFor(m.version)))
	buf = appendOctetString(buf, tagOctetString, []byte(m.community))
	pduMark := len(buf)
	buf = appendInt(buf, int64(p.requestID))
	if p.typ == pduGetBulkRequest {
		buf = appendInt(buf, int64(p.nonRepeaters))
		buf = appendInt(buf, int64(p.maxRepetitions))
	} else {
		buf = appendInt(buf, int64(p.errorStatus))
		buf = appendInt(buf, int64(p.errorIndex))
	}
	listMark := len(buf)
	for _, vb := range p.varbinds {
		vbMark := len(buf)
		o := vb.GetHeader().OID
		oidMark := len(buf)
		switch n := o.Len(); n {
		case 0:
		case 1:
			buf = appendBase128(buf, uint64(o.At(0))*40)
		default:
			buf = appendBase128(buf, uint64(o.At(0))*40+uint64(o.At(1)))
			for i := 2; i < n; i++ {
				buf = appendBase128(buf, uint64(o.At(i)))
			}
		}
		buf = berWrapTail(buf, oidMark, tagOID)
		buf = append(buf, tagNull, 0x00)
		buf = berWrapTail(buf, vbMark, tagSequence)
	}
	buf = berWrapTail(buf, listMark, tagSequence)
	buf = berWrapTail(buf, pduMark, byte(p.typ))
	buf = berWrapTail(buf, 0, tagSequence)
	return buf, true
}

// wireVersionFor maps [Version] to its wire code for encoding. Only
// v1/v2c are encodable here; the caller validates version upstream.
func wireVersionFor(v Version) int {
	if v == V1 {
		return wireVersionV1
	}
	return wireVersionV2c
}

// encodePDU encodes a standard request/response PDU. The v1 Trap-PDU
// fixed-field encode is intentionally unimplemented — the native backend
// receives traps but never sends them (trap-send is out of scope).
func encodePDU(p *pdu) ([]byte, error) {
	if p.typ == pduV1Trap {
		return nil, errs.Wrap(errMalformedPDU, "encode PDU")
	}
	var body []byte
	body = appendInt(body, int64(p.requestID))
	if p.typ == pduGetBulkRequest {
		body = appendInt(body, int64(p.nonRepeaters))
		body = appendInt(body, int64(p.maxRepetitions))
	} else {
		body = appendInt(body, int64(p.errorStatus))
		body = appendInt(body, int64(p.errorIndex))
	}
	vbList, err := encodeVarBindList(p.varbinds)
	if err != nil {
		return nil, err
	}
	body = append(body, vbList...)
	return appendSequence(byte(p.typ), body), nil
}

// encodeVarBindList encodes vbs as a SEQUENCE OF VarBind.
func encodeVarBindList(vbs []VarBind) ([]byte, error) {
	var body []byte
	for _, vb := range vbs {
		enc, err := encodeVarBind(vb)
		if err != nil {
			return nil, err
		}
		body = append(body, enc...)
	}
	return appendSequence(tagSequence, body), nil
}

// encodeVarBind encodes one [VarBind] as a VarBind SEQUENCE { name,
// value }. The codec is symmetric: it encodes the three SNMPv2 exception
// variants too, since a GetResponse legitimately carries them and the
// differential-oracle and fixture paths need to reconstruct responses.
// The "do not transmit an exception in a request" policy is enforced one
// layer up, in Session.Set, not in the codec.
func encodeVarBind(vb VarBind) ([]byte, error) {
	hdr := vb.GetHeader()
	var value []byte
	switch x := vb.(type) {
	case Integer32Var:
		value = appendInt(nil, int64(x.Value))
	case Uinteger32Var:
		value = appendUint(nil, tagUinteger32, uint64(x.Value))
	case OctetStringVar:
		value = appendOctetString(nil, tagOctetString, x.Value)
	case ObjectIDVar:
		value = encodeOID(x.Value)
	case BitStringVar:
		value = appendOctetString(nil, tagBitString, x.Value)
	case Counter32Var:
		value = appendUint(nil, tagCounter32, uint64(x.Value))
	case Gauge32Var:
		value = appendUint(nil, tagGauge32, uint64(x.Value))
	case TimeTicksVar:
		value = appendUint(nil, tagTimeTicks, uint64(x.Value))
	case Counter64Var:
		value = appendUint(nil, tagCounter64, x.Value)
	case IPAddressVar:
		enc, err := appendIPv4(x.Value)
		if err != nil {
			return nil, errs.Wrapf(err, "encode IpAddress at %s", hdr.OID)
		}
		value = enc
	case NsapAddressVar:
		value = appendOctetString(nil, tagNsapAddress, x.Value)
	case OpaqueVar:
		value = appendOctetString(nil, tagOpaque, x.Value)
	case OpaqueFloatVar:
		value = appendOpaqueFloat(nil, x.Value)
	case OpaqueDoubleVar:
		value = appendOpaqueDouble(nil, x.Value)
	case NullVar:
		value = appendNull(nil)
	case NoSuchObjectVar:
		value = appendTLV(nil, tagNoSuchObject, nil)
	case NoSuchInstanceVar:
		value = appendTLV(nil, tagNoSuchInstance, nil)
	case EndOfMibViewVar:
		value = appendTLV(nil, tagEndOfMibView, nil)
	default:
		return nil, errs.Wrapf(errMalformedPDU, "unsupported VarBind variant %T", vb)
	}
	body := encodeOID(hdr.OID)
	body = append(body, value...)
	return appendSequence(tagSequence, body), nil
}
