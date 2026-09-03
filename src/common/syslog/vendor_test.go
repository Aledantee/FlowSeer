package syslog_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

func TestVendorEvidence(t *testing.T) {
	p, err := syslog.NewParser(syslog.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range corpus(t) {
		r, err := p.Parse([]byte(f.Payload), syslog.Observation{})
		if err != nil {
			t.Fatal(err)
		}
		switch f.ID {
		case "cisco-ios":
			if r.Vendor.Sequence.Presence != syslog.Absent || len(r.Vendor.Counters) != 1 || r.Vendor.Counters[0] != "00123" || r.DeviceTime.ClockMarker != "*" || r.DeviceTime.Instant != nil {
				t.Fatal(r)
			}
		case "cisco-uptime":
			if r.DeviceTime.Uptime != "2d03h" {
				t.Fatal(r)
			}
		case "netgear":
			if r.Vendor.Thread.Value != "2110" || r.Vendor.Source.Value != "mstp_api.c(318)" || r.Vendor.Sequence.Value != "237" {
				t.Fatal(r)
			}
		case "dlink-dws":
			if r.Vendor.Thread.Value != "0x800023" {
				t.Fatal(r)
			}
		case "huawei":
			if r.Vendor.Flags.Value != "l" || r.DeviceTime.Year != 2026 {
				t.Fatal(r)
			}
		case "comware":
			if r.DeviceTime.Year != 2026 || r.DeviceTime.Nanosecond != 123000000 {
				t.Fatal(r)
			}
		case "aoscx":
			if len(r.Vendor.Components) != 2 || r.Vendor.Components[0] != "" || r.Vendor.Components[1] != "" {
				t.Fatal(r)
			}
		}
	}
}

func TestCiscoAmbiguousCounters(t *testing.T) {
	payload := []byte("<189>001: 002: *Mar 1 00:00:00: %SYS-5-RESTART: restarted")
	p, err := syslog.NewParser(syslog.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Parse(payload, syslog.Observation{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Vendor.Sequence.Presence != syslog.Absent || len(r.Vendor.Counters) != 2 || r.Vendor.Mnemonic.Value != "RESTART" || r.Status != syslog.Partial {
		t.Fatal(r)
	}
	p, err = syslog.NewParser(syslog.ParseOptions{CiscoCounterOrder: []string{"sequence", "counter"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err = p.Parse(payload, syslog.Observation{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Vendor.Sequence.Value != "001" || r.Vendor.Counter.Value != "002" {
		t.Fatal(r)
	}
}
