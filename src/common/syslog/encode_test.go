package syslog_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

func TestEncodeProjection(t *testing.T) {
	p, err := syslog.NewParser(syslog.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wire := []byte(`<13>1 1999-01-02T03:04:05.123456Z host app - ID [x a="one\]two"] body`)
	r, err := p.Parse(wire, syslog.Observation{})
	if err != nil {
		t.Fatal(err)
	}
	out, report, err := syslog.Encode(r, syslog.EncodeOptions{Format: syslog.RFC5424})
	if err != nil || report.Losses != 0 || !bytes.Equal(wire, out) {
		t.Fatalf("%q %+v %v", out, report, err)
	}
	out, report, err = syslog.Encode(r, syslog.EncodeOptions{Format: syslog.RFC3164})
	if !errors.Is(err, syslog.ErrUnrepresentable) || len(out) != 0 || report.Losses&syslog.LossStructuredData == 0 {
		t.Fatal(report, err)
	}
	out, _, err = syslog.Encode(r, syslog.EncodeOptions{Format: syslog.RFC3164, AllowLoss: report.Losses})
	if err != nil || string(out) != "<13>Jan  2 03:04:05 host body" {
		t.Fatalf("%q %v", out, err)
	}
	r.Hostname = syslog.Text("injected\nheader")
	if _, _, err = syslog.Encode(r, syslog.EncodeOptions{Format: syslog.RFC5424}); err == nil {
		t.Fatal("header injection")
	}
}

func TestEncodeRejectsDuplicateSDAndInvalidPresence(t *testing.T) {
	r := syslog.Record{Priority: syslog.Text("13"), StructuredData: []syslog.Element{{ID: "same"}, {ID: "same"}}}
	if _, _, err := syslog.Encode(r, syslog.EncodeOptions{Format: syslog.RFC5424}); err == nil {
		t.Fatal("duplicate SD cannot be standard wire output")
	}
	r.StructuredData = nil
	r.Hostname = syslog.Field{Presence: 99}
	if _, _, err := syslog.Encode(r, syslog.EncodeOptions{Format: syslog.RFC5424}); err == nil {
		t.Fatal("invalid presence")
	}
}

func TestEncodeCallerRecordLosses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record syslog.Record
		format syslog.Format
		loss   syslog.Loss
	}{
		{"legacy_headers", syslog.Record{Application: syslog.Text("app"), ProcessID: syslog.Text("42"), MessageID: syslog.Text("ID"), DeviceTime: syslog.DeviceTime{Month: 9, Day: 3, Hour: 10, Present: syslog.DatePart | syslog.ClockPart}}, syslog.RFC3164, syslog.LossHeader},
		{"partial_date", syslog.Record{DeviceTime: syslog.DeviceTime{Year: 2026, Month: 9, Day: 3, Present: syslog.YearPart | syslog.DatePart}}, syslog.RFC5424, syslog.LossTime},
		{"inferred_date", syslog.Record{DeviceTime: syslog.DeviceTime{Year: 2026, Inferred: syslog.YearPart}}, syslog.RFC5424, syslog.LossTime},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.record
			r.Priority = syslog.Text("13")
			r.Hostname = syslog.Text("host")
			r.Content = []byte("body")
			out, report, err := syslog.Encode(r, syslog.EncodeOptions{Format: tc.format})
			if !errors.Is(err, syslog.ErrUnrepresentable) || len(out) != 0 || report.Losses != tc.loss {
				t.Fatalf("got %q, %+v, %v", out, report, err)
			}
			out, report, err = syslog.Encode(r, syslog.EncodeOptions{Format: tc.format, AllowLoss: tc.loss})
			if err != nil || len(out) == 0 || report.Losses != tc.loss {
				t.Fatalf("got %q, %+v, %v", out, report, err)
			}
		})
	}
	p, err := syslog.NewParser(syslog.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Parse([]byte("<13>Sep 3 10:00:00 host app[42]: body"), syslog.Observation{})
	if err != nil {
		t.Fatal(err)
	}
	out, report, err := syslog.Encode(r, syslog.EncodeOptions{Format: syslog.RFC3164})
	if err != nil || report.Losses != 0 || !bytes.HasSuffix(out, []byte("app[42]: body")) {
		t.Fatalf("got %q, %+v, %v", out, report, err)
	}
}

func TestEncodeOffsetValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset int
		valid  bool
	}{
		{"seconds", 45, false}, {"too_large", 25 * 3600, false}, {"negative_seconds", -45, false}, {"maximum", 23*3600 + 59*60, true}, {"negative_maximum", -23*3600 - 59*60, true}, {"utc", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instant := time.Date(2026, 9, 3, 10, 0, 0, 0, time.FixedZone("device", tc.offset))
			r := syslog.Record{Priority: syslog.Text("13"), DeviceTime: syslog.DeviceTime{Instant: &instant}}
			out, _, err := syslog.Encode(r, syslog.EncodeOptions{Format: syslog.RFC5424, AllowLoss: ^syslog.Loss(0)})
			if !tc.valid {
				if !errors.Is(err, syslog.ErrUnrepresentable) || len(out) != 0 {
					t.Fatalf("got %q, %v", out, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			fields := bytes.Fields(out)
			got, err := time.Parse(time.RFC3339Nano, string(fields[1]))
			if err != nil || !got.Equal(instant) {
				t.Fatalf("got %q, %v, want %v", out, err, instant)
			}
		})
	}
}
