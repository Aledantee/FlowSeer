package snmp

import (
	"testing"
)

func reportPDU(counter uint32, withInstance bool) *pdu {
	subs := []uint32{1, 3, 6, 1, 6, 3, 15, 1, 1, counter}
	if withInstance {
		subs = append(subs, 0)
	}
	return &pdu{
		typ:       pduReport,
		requestID: 1,
		varbinds: []VarBind{
			Counter32Var{Header: Header{OID: MustOID(subs...), Kind: KindCounter32}, Value: 1},
		},
	}
}

// TestReportOutcome_AllCounters maps each of the six usmStats OIDs to its
// outcome, with and without the trailing .0 instance.
func TestReportOutcome_AllCounters(t *testing.T) {
	want := map[uint32]reportOutcome{
		1: reportUnsupportedSecLevel,
		2: reportNotInTimeWindow,
		3: reportUnknownUserName,
		4: reportUnknownEngineID,
		5: reportWrongDigest,
		6: reportDecryptionError,
	}
	for counter, outcome := range want {
		for _, inst := range []bool{false, true} {
			got, isReport := classifyReport(reportPDU(counter, inst))
			if !isReport {
				t.Fatalf("counter %d inst=%v: not classified as Report", counter, inst)
			}
			if got != outcome {
				t.Fatalf("counter %d inst=%v: got %v want %v", counter, inst, got, outcome)
			}
		}
	}
}

// TestReportOutcome_UnknownOID classifies a Report whose OID is outside the
// usmStats arc as reportUnknown (still a Report, never data).
func TestReportOutcome_UnknownOID(t *testing.T) {
	p := &pdu{
		typ:       pduReport,
		requestID: 1,
		varbinds: []VarBind{
			Counter32Var{Header: Header{OID: MustOID(1, 3, 6, 1, 4, 1, 9999), Kind: KindCounter32}, Value: 1},
		},
	}
	got, isReport := classifyReport(p)
	if !isReport {
		t.Fatalf("should still be a Report")
	}
	if got != reportUnknown {
		t.Fatalf("got %v, want reportUnknown", got)
	}
}

// TestClassifyReport_NonReport confirms a non-Report PDU is not treated as a
// Report.
func TestClassifyReport_NonReport(t *testing.T) {
	p := &pdu{typ: pduGetResponse, requestID: 1}
	if _, isReport := classifyReport(p); isReport {
		t.Fatalf("GetResponse should not be a Report")
	}
}

// TestReportOutcome_String checks the low-cardinality labels.
func TestReportOutcome_String(t *testing.T) {
	cases := map[reportOutcome]string{
		reportUnknownEngineID: "unknown_engine_id",
		reportNotInTimeWindow: "not_in_time_window",
		reportWrongDigest:     "wrong_digest",
		reportUnknown:         "unknown_report",
	}
	for o, s := range cases {
		if o.String() != s {
			t.Fatalf("%d.String() = %q, want %q", o, o.String(), s)
		}
	}
}
