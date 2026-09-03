package syslog_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/protocol/syslog"
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

func TestUnknownZoneEvidence(t *testing.T) {
	p, err := syslog.NewParser(syslog.ParseOptions{Year: 2026, LegacyTimeSuffix: true})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Parse([]byte("<13>Sep 03 10:00:00 PDT host app: text"), syslog.Observation{})
	if err != nil {
		t.Fatal(err)
	}
	if r.DeviceTime.Zone != "PDT" || r.DeviceTime.Instant != nil || r.Hostname.Value != "host" {
		t.Fatal(r)
	}
}

func TestCiscoTimestampTerminators(t *testing.T) {
	for _, tc := range []struct {
		name, timestamp, zone string
		year                  int
	}{
		{"utc", "*Mar 1 00:00:01.123 UTC:", "UTC", 0},
		{"pdt", ".Mar 1 00:00:01.123 PDT:", "PDT", 0},
		{"year_before_clock", "Oct 13 2004 22:45:31 UTC:", "UTC", 2004},
		{"year_after_zone", "Oct 13 22:45:31 UTC 2004:", "UTC", 2004},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := syslog.NewParser(syslog.ParseOptions{CiscoCounterOrder: []string{"sequence"}})
			if err != nil {
				t.Fatal(err)
			}
			body := "%LINK-3-UPDOWN: Interface down"
			r, err := p.Parse([]byte("<189>00123: "+tc.timestamp+" "+body), syslog.Observation{})
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != syslog.Complete || r.DeviceTime.Zone != tc.zone || r.DeviceTime.Year != tc.year || r.DeviceTime.Original != tc.timestamp[:len(tc.timestamp)-1] || r.Vendor.Sequence.Value != "00123" || r.Vendor.Module.Value != "LINK" || r.Vendor.Severity.Value != "3" || r.Vendor.Mnemonic.Value != "UPDOWN" || string(r.Content) != body {
				t.Fatalf("got %+v", r)
			}
		})
	}
}

func TestLegacyTimestampAmbiguity(t *testing.T) {
	for _, suffix := range []string{"UTC", "1000", "PDT host"} {
		t.Run(suffix, func(t *testing.T) {
			p, err := syslog.NewParser(syslog.ParseOptions{Year: 2026, Location: time.UTC})
			if err != nil {
				t.Fatal(err)
			}
			rest := suffix + " app: text"
			r, err := p.Parse([]byte("<13>Sep 3 10:00:00 "+rest), syslog.Observation{})
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != syslog.Partial || r.DeviceTime.Instant != nil || r.Hostname.Presence != syslog.Absent || string(r.Unparsed) != rest || string(r.Content) != rest || len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "ambiguous_timestamp_suffix" {
				t.Fatalf("got %+v", r)
			}
		})
	}
	p, err := syslog.NewParser(syslog.ParseOptions{Year: 2026, LegacyTimeSuffix: true})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Parse([]byte("<13>Sep 3 10:00:00 UTC host app: text"), syslog.Observation{})
	if err != nil || r.Status != syslog.Complete || r.Hostname.Value != "host" || r.DeviceTime.Instant == nil || r.DeviceTime.Zone != "UTC" {
		t.Fatalf("got %+v, %v", r, err)
	}
}
