package syslog_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

func TestExplicitClockContext(t *testing.T) {
	location, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	p, err := syslog.NewParser(syslog.ParseOptions{Year: 2026, Location: location})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		message  string
		resolved bool
	}{
		{"<13>Oct 25 02:30:00 host app: fold", false},
		{"<13>Mar 29 02:30:00 host app: gap", false},
		{"<13>Mar 29 03:30:00 host app: normal", true},
		{"<13>Feb 30 10:00:00 host app: invalid", false},
	} {
		r, err := p.Parse([]byte(tc.message), syslog.Observation{})
		if err != nil {
			t.Fatal(err)
		}
		if (r.DeviceTime.Instant != nil) != tc.resolved {
			t.Fatalf("%s: %+v", tc.message, r.DeviceTime)
		}
	}
}

func TestUnknownAndLimitedSD(t *testing.T) {
	p, err := syslog.NewParser(syslog.ParseOptions{Limits: syslog.Limits{MaxElements: 1, MaxDiagnostics: 1}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Parse([]byte(`<13>1 - h a - - [x][y] body`), syslog.Observation{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != syslog.RFC5424 || r.Status != syslog.Partial || len(r.StructuredData) != 1 || len(r.Unparsed) == 0 {
		t.Fatal(r)
	}
	r, err = p.Parse([]byte(`plain description includes %LINK-3-UPDOWN: not a prefix`), syslog.Observation{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Vendor.Family != "" || r.Format != syslog.Unknown {
		t.Fatal(r)
	}
}
