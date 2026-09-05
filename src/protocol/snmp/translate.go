package snmp

import (
	"go.aledante.io/FlowSeer/src/common/errs"
)

// translate.go bridges the structured PDU model (pdu.go) and the Session's
// typed surface: it validates an inbound response against the session
// configuration, maps an agent-side error-status to a typed
// [*PDUError], and provides the OID ordering used by the walk guards.
// The value-level wire↔[VarBind] coding lives in pdu.go (the single
// structured representation); this file is the Session-facing glue.

// validateResponse rejects a decoded response whose version or community
// disagrees with the session configuration. The returned errors
// never include the community string itself.
func validateResponse(m *message, wantVer Version, wantCommunity string) error {
	if m.version != wantVer {
		return errs.Wrapf(ErrVersionMismatch, "got %s, want %s", m.version, wantVer)
	}
	if m.community != wantCommunity {
		return ErrCommunityMismatch
	}
	return nil
}

// enrichRawErrorVarbinds decodes a raw response's varbind list in place
// when the PDU carries an error status, so [pduError] can name the
// offending OID. It is a no-op on a successful PDU or one that was
// already decoded eagerly. A failed re-decode is deliberately ignored:
// the raw bytes stay authoritative and the caller still reports the
// error status, just without an OID.
func enrichRawErrorVarbinds(p *pdu) {
	if p.errorStatus == NoError || p.rawVBL == nil {
		return
	}
	if vbs, err := decodeVarBindList(p.rawVBL, 1); err == nil {
		p.varbinds = vbs
	}
}

// pduError builds a [*PDUError] from a response PDU's error-status,
// or returns nil when the status is NoError. The 1-based error-index
// (RFC 3416 §4.2.1) selects the offending varbind's OID when in range; an
// out-of-range index leaves the OID empty rather than panicking.
func pduError(m *message) *PDUError {
	if m.pdu.errorStatus == NoError {
		return nil
	}
	pe := &PDUError{Status: m.pdu.errorStatus, Index: m.pdu.errorIndex}
	if i := m.pdu.errorIndex; i > 0 && i <= len(m.pdu.varbinds) {
		pe.OID = m.pdu.varbinds[i-1].GetHeader().OID
	}
	return pe
}

// nullVarbinds builds a request varbind list of NULL-valued bindings, one
// per OID, as used by Get/GetNext/GetBulk request PDUs.
func nullVarbinds(oids []OID) []VarBind {
	out := make([]VarBind, len(oids))
	for i, oid := range oids {
		out[i] = NullVar{Header: Header{OID: oid, Kind: KindNull}}
	}
	return out
}
