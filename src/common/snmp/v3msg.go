package snmp

import (
	"math"

	"go.aledante.io/ae"
)

// v3msg.go is the SNMPv3 message envelope codec (RFC 3412 §6). It is a
// *parallel* envelope to the v1/v2c [message]: the inner pdu / value codec
// (pdu.go, ber.go) is reused unchanged for the scoped PDU, but the v3
// header, the USM security parameters (a double-BER OCTET-STRING-wrapped
// SEQUENCE), and the scoped-PDU wrapper are v3-specific.
//
// The crypto attaches in the USM layer; this file is codec-only. The two
// facts the crypto needs from the wire — the byte range of
// msgAuthenticationParameters (to
// zero it before the HMAC check, without re-encoding a possibly
// non-canonical sender's bytes) and the raw scoped-PDU data (ciphertext
// under authPriv) — are returned by [decodeV3Message] alongside the decoded
// struct.

// v3 msgFlags bits (RFC 3412 §6.4).
const (
	v3FlagAuth       = 0x01
	v3FlagPriv       = 0x02
	v3FlagReportable = 0x04
)

// securityModelUSM is the msgSecurityModel value for USM (RFC 3414).
const securityModelUSM = 3

// v3MaxMessageSize is the msgMaxSize this backend advertises — the largest
// UDP datagram it will accept, within the RFC 3412 range.
const v3MaxMessageSize = maxUDPPayload

// v3 codec sentinels.
var (
	// errV3Malformed is the umbrella sentinel for a structurally invalid v3
	// message; specific causes are wrapped with context.
	errV3Malformed = ae.Msg("malformed SNMPv3 message")
	// errV3FlagsInvalid rejects an RFC-invalid msgFlags combination (priv
	// set without auth).
	errV3FlagsInvalid = ae.Msg("priv flag set without auth flag")
)

// msgFlags is the decoded msgFlags octet.
type msgFlags struct {
	reportable bool
	auth       bool
	priv       bool
}

// byteValue encodes the flags back to the single wire octet.
func (f msgFlags) byteValue() byte {
	var b byte
	if f.auth {
		b |= v3FlagAuth
	}
	if f.priv {
		b |= v3FlagPriv
	}
	if f.reportable {
		b |= v3FlagReportable
	}
	return b
}

// level returns the security level the flags imply.
func (f msgFlags) level() SecurityLevel {
	switch {
	case f.auth && f.priv:
		return SecurityLevelAuthPriv
	case f.auth:
		return SecurityLevelAuthNoPriv
	default:
		return SecurityLevelNoAuthNoPriv
	}
}

// usmSecurityParameters is the decoded USMSecurityParameters SEQUENCE.
type usmSecurityParameters struct {
	engineID    []byte
	engineBoots int32
	engineTime  int32
	userName    string
	authParams  []byte
	privParams  []byte
}

// scopedPDU is the decoded ScopedPDU SEQUENCE { contextEngineID,
// contextName, data }.
type scopedPDU struct {
	contextEngineID []byte
	contextName     []byte
	pdu             pdu
}

// v3Message is the decoded SNMPv3 message. Exactly one of scoped (plaintext
// msgData) or ciphertext (encrypted msgData) is populated, per the priv
// flag.
type v3Message struct {
	msgID         int32
	msgMaxSize    int32
	flags         msgFlags
	securityModel int
	sec           usmSecurityParameters

	scoped     *scopedPDU
	ciphertext []byte
}

// --- Encode -----------------------------------------------------------

// encodeScopedPDU encodes ScopedPDU { contextEngineID, contextName, data }.
func encodeScopedPDU(sp *scopedPDU) ([]byte, error) {
	pduBytes, err := encodePDU(&sp.pdu)
	if err != nil {
		return nil, err
	}
	var body []byte
	body = appendOctetString(body, tagOctetString, sp.contextEngineID)
	body = appendOctetString(body, tagOctetString, sp.contextName)
	body = append(body, pduBytes...)
	return appendSequence(tagSequence, body), nil
}

// encodeUSMSecParams encodes the USMSecurityParameters SEQUENCE and wraps it
// in the OCTET STRING the envelope carries (the double-BER layer).
func encodeUSMSecParams(p *usmSecurityParameters) []byte {
	var body []byte
	body = appendOctetString(body, tagOctetString, p.engineID)
	body = appendInt(body, int64(p.engineBoots))
	body = appendInt(body, int64(p.engineTime))
	body = appendOctetString(body, tagOctetString, []byte(p.userName))
	body = appendOctetString(body, tagOctetString, p.authParams)
	body = appendOctetString(body, tagOctetString, p.privParams)
	seq := appendSequence(tagSequence, body)
	return appendOctetString(nil, tagOctetString, seq)
}

// encodeV3Message serializes a complete SNMPv3 datagram. The
// msgAuthenticationParameters are emitted verbatim from m.sec.authParams, so
// the auth layer produces the digest by encoding once with the field
// zeroed (to the protocol's truncated length), computing the HMAC over those
// bytes, then encoding again with the field set to the digest — the two
// encodings differ only in that field's content, never in length or layout.
func encodeV3Message(m *v3Message) ([]byte, error) {
	var msgData []byte
	switch {
	case m.ciphertext != nil:
		msgData = appendOctetString(nil, tagOctetString, m.ciphertext)
	case m.scoped != nil:
		sp, err := encodeScopedPDU(m.scoped)
		if err != nil {
			return nil, err
		}
		msgData = sp
	default:
		return nil, ae.Wrap("encode v3 message", errV3Malformed)
	}

	// HeaderData SEQUENCE { msgID, msgMaxSize, msgFlags, msgSecurityModel }.
	var hdr []byte
	hdr = appendInt(hdr, int64(m.msgID))
	hdr = appendInt(hdr, int64(m.msgMaxSize))
	hdr = appendOctetString(hdr, tagOctetString, []byte{m.flags.byteValue()})
	hdr = appendInt(hdr, int64(m.securityModel))
	globalData := appendSequence(tagSequence, hdr)

	secParams := encodeUSMSecParams(&m.sec)

	var body []byte
	body = appendInt(body, int64(wireVersionV3))
	body = append(body, globalData...)
	body = append(body, secParams...)
	body = append(body, msgData...)
	return appendSequence(tagSequence, body), nil
}

// --- Decode -----------------------------------------------------------

// v3Decoded carries the decoded message plus the two byte-level facts the
// crypto needs from the wire.
type v3Decoded struct {
	msg *v3Message
	// wholeMsg is the raw received datagram, and [authStart:authEnd) is the
	// byte range of the msgAuthenticationParameters content within it. The
	// HMAC verify streams over wholeMsg with that window treated as zeros
	// (authMACOverZeroed), reproducing the canonical auth-zeroed input
	// (RFC 3414 §6.3.2) WITHOUT copying the datagram.
	//
	// wholeMsg ALIASES the caller's receive buffer; it is not an owned copy.
	// The decode→verify→deliver chain runs synchronously on the read-loop
	// (reactor.handleInboundV3) and trap-listener (listener.handlePacket)
	// goroutines before either reuses its buffer, and the verify reads these
	// bytes but never lets them escape into the delivered result (the scoped
	// PDU is cloned during decode). A consumer that defers verification past
	// buffer reuse must clone wholeMsg first.
	wholeMsg           []byte
	authStart, authEnd int
	// scopedRaw is the raw msgData payload: the ciphertext when encrypted,
	// or the scoped-PDU SEQUENCE bytes when plaintext.
	scopedRaw []byte
	encrypted bool
}

// decodeV3Message decodes a complete SNMPv3 datagram. It records the absolute
// byte range of msgAuthenticationParameters so the HMAC check can zero-treat
// it while streaming over the received bytes (no datagram copy).
func decodeV3Message(datagram []byte) (*v3Decoded, error) {
	_, bodyStart, err := sequenceContent(datagram, 0, 0)
	if err != nil {
		return nil, ae.Wrap("decode v3 envelope", err)
	}
	pos := bodyStart

	// msgVersion INTEGER (must be 3 — caller dispatched on it, re-checked).
	ver, next, err := decodeIntAt(datagram, pos, "msgVersion")
	if err != nil {
		return nil, err
	}
	if ver != wireVersionV3 {
		return nil, ae.Wrapf("msgVersion %d", errV3Malformed, ver)
	}
	pos = next

	m := &v3Message{}

	// msgGlobalData SEQUENCE.
	gd, gdStart, err := sequenceContent(datagram, pos, 1)
	if err != nil {
		return nil, ae.Wrap("decode msgGlobalData", err)
	}
	if err := decodeGlobalData(m, gd); err != nil {
		return nil, err
	}
	pos = gdStart + len(gd)

	// msgSecurityParameters OCTET STRING (wraps the USM SEQUENCE).
	secContent, secStart, secEnd, err := octetStringAt(datagram, pos, "msgSecurityParameters")
	if err != nil {
		return nil, err
	}
	authStart, authEnd, err := decodeUSMSecParams(m, datagram, secStart, secContent)
	if err != nil {
		return nil, err
	}
	pos = secEnd

	// msgData: ScopedPDU (plaintext SEQUENCE) or encryptedPDU (OCTET STRING).
	tag, _, _, err := parseTLV(datagram[pos:])
	if err != nil {
		return nil, ae.Wrap("decode msgData", err)
	}
	dec := &v3Decoded{msg: m}
	switch tag {
	case tagOctetString:
		ct, _, _, err := octetStringAt(datagram, pos, "encryptedPDU")
		if err != nil {
			return nil, err
		}
		m.ciphertext = cloneBytes(ct)
		dec.scopedRaw = m.ciphertext
		dec.encrypted = true
	case tagSequence:
		spContent, spStart, err := sequenceContent(datagram, pos, 1)
		if err != nil {
			return nil, ae.Wrap("decode scopedPDU", err)
		}
		sp, err := decodeScopedPDUContent(spContent)
		if err != nil {
			return nil, err
		}
		m.scoped = sp
		// The whole scoped-PDU SEQUENCE TLV: from its tag at pos to the end
		// of its content.
		dec.scopedRaw = cloneBytes(datagram[pos : spStart+len(spContent)])
	default:
		return nil, ae.Wrapf("msgData tag 0x%02x", errV3Malformed, tag)
	}

	// Reject the RFC-invalid priv-without-auth combination.
	if m.flags.priv && !m.flags.auth {
		return nil, errV3FlagsInvalid
	}

	// Record the datagram and the auth-param window for a streaming HMAC
	// verify (authMACOverZeroed), which zero-treats [authStart:authEnd)
	// without copying the datagram. wholeMsg aliases the caller's buffer; see
	// the v3Decoded field doc for the synchronous-consume contract.
	dec.wholeMsg = datagram
	dec.authStart = authStart
	dec.authEnd = authEnd
	return dec, nil
}

// decodeGlobalData fills the header fields from the msgGlobalData content.
func decodeGlobalData(m *v3Message, gd []byte) error {
	id, rest, err := expectInt(gd, "msgID")
	if err != nil {
		return err
	}
	if id < 0 || id > math.MaxInt32 {
		return ae.Wrapf("msgID %d out of range", errV3Malformed, id)
	}
	m.msgID = int32(id)

	maxSize, rest, err := expectInt(rest, "msgMaxSize")
	if err != nil {
		return err
	}
	if maxSize < 0 || maxSize > math.MaxInt32 {
		return ae.Wrapf("msgMaxSize %d out of range", errV3Malformed, maxSize)
	}
	m.msgMaxSize = int32(maxSize)

	fTag, fContent, fUsed, err := parseTLV(rest)
	if err != nil {
		return ae.Wrap("decode msgFlags", err)
	}
	if fTag != tagOctetString || len(fContent) != 1 {
		return ae.Wrapf("msgFlags tag 0x%02x len %d", errV3Malformed, fTag, len(fContent))
	}
	fb := fContent[0]
	m.flags = msgFlags{
		reportable: fb&v3FlagReportable != 0,
		auth:       fb&v3FlagAuth != 0,
		priv:       fb&v3FlagPriv != 0,
	}
	rest = rest[fUsed:]

	sm, _, err := expectInt(rest, "msgSecurityModel")
	if err != nil {
		return err
	}
	m.securityModel = int(sm)
	return nil
}

// decodeUSMSecParams decodes the USM security parameters from the inner
// SEQUENCE (itself the content of the msgSecurityParameters OCTET STRING).
// secContentStart is the absolute offset of secContent within the datagram,
// used to report the absolute byte range of msgAuthenticationParameters.
func decodeUSMSecParams(m *v3Message, datagram []byte, secContentStart int, _ []byte) (authStart, authEnd int, err error) {
	seq, seqStart, err := sequenceContent(datagram, secContentStart, 1)
	if err != nil {
		return 0, 0, ae.Wrap("decode USM security params", err)
	}
	pos := seqStart

	eid, next, err := octetAt(datagram, pos, "msgAuthoritativeEngineID")
	if err != nil {
		return 0, 0, err
	}
	m.sec.engineID = cloneBytes(eid)
	pos = next

	boots, next, err := decodeIntAt(datagram, pos, "msgAuthoritativeEngineBoots")
	if err != nil {
		return 0, 0, err
	}
	m.sec.engineBoots = int32(boots)
	pos = next

	etime, next, err := decodeIntAt(datagram, pos, "msgAuthoritativeEngineTime")
	if err != nil {
		return 0, 0, err
	}
	m.sec.engineTime = int32(etime)
	pos = next

	user, next, err := octetAt(datagram, pos, "msgUserName")
	if err != nil {
		return 0, 0, err
	}
	m.sec.userName = string(user)
	pos = next

	auth, authContentStart, next, err := octetStringAt(datagram, pos, "msgAuthenticationParameters")
	if err != nil {
		return 0, 0, err
	}
	m.sec.authParams = cloneBytes(auth)
	authStart, authEnd = authContentStart, authContentStart+len(auth)
	pos = next

	priv, _, err := octetAt(datagram, pos, "msgPrivacyParameters")
	if err != nil {
		return 0, 0, err
	}
	m.sec.privParams = cloneBytes(priv)

	_ = seq // bounds already validated by the field walk
	return authStart, authEnd, nil
}

// decodeScopedPDUContent decodes the ScopedPDU SEQUENCE content.
func decodeScopedPDUContent(content []byte) (*scopedPDU, error) {
	eid, rest, err := expectOctet(content, "contextEngineID")
	if err != nil {
		return nil, err
	}
	name, rest, err := expectOctet(rest, "contextName")
	if err != nil {
		return nil, err
	}
	p, err := decodePDU(rest, 1)
	if err != nil {
		return nil, err
	}
	return &scopedPDU{
		contextEngineID: cloneBytes(eid),
		contextName:     cloneBytes(name),
		pdu:             p,
	}, nil
}

// decodeScopedPDU decodes a standalone scoped-PDU SEQUENCE datagram (the
// decrypted plaintext under authPriv). It is the entry point the privacy
// layer calls after decrypting the ciphertext.
func decodeScopedPDU(buf []byte) (*scopedPDU, error) {
	content, _, err := sequenceContent(buf, 0, 0)
	if err != nil {
		return nil, ae.Wrap("decode scopedPDU", err)
	}
	return decodeScopedPDUContent(content)
}

// --- Version dispatch -------------------------------------------------

// decodeAnyMessage reads the version INTEGER of a datagram and routes to the
// v1/v2c decoder or the v3 decoder. It is the single entry point the
// reactor read-loop and the trap listener use so a v3 datagram is never fed
// to the v1/v2c-only [decodeMessage] (which still rejects v3 at
// versionFromWire, so each surface stays independently targetable by the
// differential/fuzz harness, M1).
func decodeAnyMessage(datagram []byte) (v1v2 *message, v3 *v3Decoded, err error) {
	_, start, err := sequenceContent(datagram, 0, 0)
	if err != nil {
		return nil, nil, ae.Wrap("decode message envelope", err)
	}
	ver, _, err := decodeIntAt(datagram, start, "version")
	if err != nil {
		return nil, nil, err
	}
	if ver == wireVersionV3 {
		dec, err := decodeV3Message(datagram)
		return nil, dec, err
	}
	m, err := decodeMessage(datagram)
	return m, nil, err
}

// --- Offset-tracking BER helpers --------------------------------------
//
// These mirror the pdu.go field readers but thread an absolute position so
// decodeV3Message can report the msgAuthenticationParameters byte range.

// sequenceContent parses the SEQUENCE TLV at datagram[pos:] and returns its
// content plus the absolute offset at which the content begins.
func sequenceContent(datagram []byte, pos, depth int) (content []byte, contentStart int, err error) {
	c, consumed, err := parseSequence(datagram[pos:], tagSequence, depth)
	if err != nil {
		return nil, 0, err
	}
	return c, pos + (consumed - len(c)), nil
}

// decodeIntAt reads an INTEGER TLV at datagram[pos:] and returns its value
// and the absolute position just past it.
func decodeIntAt(datagram []byte, pos int, label string) (val int64, next int, err error) {
	tag, content, used, err := parseTLV(datagram[pos:])
	if err != nil {
		return 0, 0, ae.Wrapf("decode %s", err, label)
	}
	if tag != tagInteger {
		return 0, 0, ae.Wrapf("decode %s: tag 0x%02x", errV3Malformed, label, tag)
	}
	v, err := decodeSignedInt(content)
	if err != nil {
		return 0, 0, ae.Wrapf("decode %s", err, label)
	}
	return v, pos + used, nil
}

// octetStringAt parses an OCTET STRING TLV at datagram[pos:] and returns its
// content, the absolute content start, and the absolute position past the
// whole TLV.
func octetStringAt(datagram []byte, pos int, label string) (content []byte, contentStart, next int, err error) {
	tag, c, used, err := parseTLV(datagram[pos:])
	if err != nil {
		return nil, 0, 0, ae.Wrapf("decode %s", err, label)
	}
	if tag != tagOctetString {
		return nil, 0, 0, ae.Wrapf("decode %s: tag 0x%02x", errV3Malformed, label, tag)
	}
	return c, pos + (used - len(c)), pos + used, nil
}

// octetAt is octetStringAt discarding the content-start offset.
func octetAt(datagram []byte, pos int, label string) (content []byte, next int, err error) {
	c, _, n, err := octetStringAt(datagram, pos, label)
	return c, n, err
}

// expectOctet reads an OCTET STRING TLV from the front of buf, returning its
// content and the remaining bytes (the non-offset-tracking variant, mirrors
// expectInt).
func expectOctet(buf []byte, label string) (content, rest []byte, err error) {
	tag, c, used, err := parseTLV(buf)
	if err != nil {
		return nil, nil, ae.Wrapf("decode %s", err, label)
	}
	if tag != tagOctetString {
		return nil, nil, ae.Wrapf("decode %s: tag 0x%02x", errV3Malformed, label, tag)
	}
	return c, buf[used:], nil
}
