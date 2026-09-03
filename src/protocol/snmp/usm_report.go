package snmp

// usm_report.go classifies an SNMPv3 Report-PDU (0xA8). A Report carries a
// single varbind whose OID is one of the usmStats counters (RFC 3414 §5);
// the value itself is never surfaced as data. The classification
// drives session-side discovery and time-window resync and the listener's
// inform/discovery responder.

// usmStatsBase is the OID arc 1.3.6.1.6.3.15.1.1 under which the six USM
// statistics counters live; the next sub-identifier selects the counter.
var usmStatsBase = MustOID(1, 3, 6, 1, 6, 3, 15, 1, 1)

// reportOutcome is the typed classification of a Report-PDU.
type reportOutcome int

const (
	// reportUnknown is a Report whose varbind OID is not a recognized
	// usmStats counter (or a PDU that is not a Report). It is never treated
	// as data.
	reportUnknown reportOutcome = iota
	// reportUnsupportedSecLevel ← usmStatsUnsupportedSecLevels (.1).
	reportUnsupportedSecLevel
	// reportNotInTimeWindow ← usmStatsNotInTimeWindows (.2).
	reportNotInTimeWindow
	// reportUnknownUserName ← usmStatsUnknownUserNames (.3).
	reportUnknownUserName
	// reportUnknownEngineID ← usmStatsUnknownEngineIDs (.4): the discovery
	// signal.
	reportUnknownEngineID
	// reportWrongDigest ← usmStatsWrongDigests (.5).
	reportWrongDigest
	// reportDecryptionError ← usmStatsDecryptionErrors (.6).
	reportDecryptionError
)

// String returns a short identifier for the outcome (low-cardinality, safe
// for span/metric attributes).
func (o reportOutcome) String() string {
	switch o {
	case reportUnsupportedSecLevel:
		return "unsupported_sec_level"
	case reportNotInTimeWindow:
		return "not_in_time_window"
	case reportUnknownUserName:
		return "unknown_user_name"
	case reportUnknownEngineID:
		return "unknown_engine_id"
	case reportWrongDigest:
		return "wrong_digest"
	case reportDecryptionError:
		return "decryption_error"
	default:
		return "unknown_report"
	}
}

// reportOutcomeForOID maps a usmStats counter OID to its outcome. The match
// is on the usmStatsBase prefix plus the selector sub-identifier, so a
// counter sent with or without a trailing .0 instance suffix classifies the
// same. An OID outside the arc is reportUnknown.
func reportOutcomeForOID(oid OID) reportOutcome {
	if !oid.HasPrefix(usmStatsBase) || oid.Len() <= usmStatsBase.Len() {
		return reportUnknown
	}
	switch oid.At(usmStatsBase.Len()) {
	case 1:
		return reportUnsupportedSecLevel
	case 2:
		return reportNotInTimeWindow
	case 3:
		return reportUnknownUserName
	case 4:
		return reportUnknownEngineID
	case 5:
		return reportWrongDigest
	case 6:
		return reportDecryptionError
	default:
		return reportUnknown
	}
}

// classifyReport reports whether p is a Report-PDU and, if so, its outcome.
// A Report is never surfaced to a caller as a VarBind or PDUError; the
// caller branches on isReport before any data handling. The
// authoritative engineID/boots/time the caller needs for discovery/resync
// live in the message security parameters, not the PDU.
func classifyReport(p *pdu) (outcome reportOutcome, isReport bool) {
	if p.typ != pduReport {
		return reportUnknown, false
	}
	for _, vb := range p.varbinds {
		if o := reportOutcomeForOID(vb.GetHeader().OID); o != reportUnknown {
			return o, true
		}
	}
	return reportUnknown, true
}
