package syslog_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

func TestParserCorpus(t *testing.T) {
	for _, f := range corpus(t) {
		t.Run(f.ID, func(t *testing.T) {
			for _, raw := range []bool{false, true} {
				p, err := syslog.NewParser(syslog.ParseOptions{CaptureRaw: raw})
				if err != nil {
					t.Fatal(err)
				}
				input := []byte(f.Payload)
				r, err := p.Parse(input, syslog.Observation{})
				if err != nil {
					t.Fatal(err)
				}
				if string(r.Format) != f.Format || r.Vendor.Module.Value != f.Module || r.Vendor.EventID.Value != f.EventID || r.Vendor.Mnemonic.Value != f.Mnemonic {
					t.Fatalf("got format=%s vendor=%+v", r.Format, r.Vendor)
				}
				if (r.Raw != nil) != raw {
					t.Fatal("raw presence")
				}
				if raw && !bytes.Equal(*r.Raw, input) {
					t.Fatal("raw bytes")
				}
				if len(input) > 0 {
					input[0] = 'x'
				}
				if raw && string(*r.Raw) != f.Payload {
					t.Fatal("retained input alias")
				}
			}
		})
	}
}

func TestParserTimeAndSD(t *testing.T) {
	p, err := syslog.NewParser(syslog.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	r, err := p.Parse([]byte(`<13>1 1999-01-02T03:04:05Z host app - - [x a="one\]two" a="three"][x] body`), syslog.Observation{ReceivedAt: observed})
	if err != nil {
		t.Fatal(err)
	}
	if r.DeviceTime.Instant == nil || r.DeviceTime.Instant.Year() != 1999 || !r.Observation.ReceivedAt.Equal(observed) {
		t.Fatal("clock substitution", r)
	}
	if len(r.StructuredData) != 2 || string(r.StructuredData[0].Parameters[0].Value) != "one]two" || len(r.Diagnostics) == 0 {
		t.Fatal("SD preservation", r)
	}
	for _, s := range []string{"<13>Feb 29 10:00:00 h app: x", "<13>1 - h a - - [broken"} {
		r, err = p.Parse([]byte(s), syslog.Observation{})
		if err != nil {
			t.Fatal(err)
		}
		if r.DeviceTime.Instant != nil {
			t.Fatal("invented timestamp")
		}
	}
	_, err = p.Parse(make([]byte, 65537), syslog.Observation{})
	if !errors.Is(err, syslog.ErrLimit) {
		t.Fatal(err)
	}
}

func TestInvalidHeaderBytesRemainUnparsed(t *testing.T) {
	p, err := syslog.NewParser(syslog.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, wire := range []string{"<13>1 - bad\xffhost app - - - body", "<13>Sep 03 10:00:00 bad\xffhost app: body"} {
		r, err := p.Parse([]byte(wire), syslog.Observation{})
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != syslog.Partial || !bytes.Contains(r.Unparsed, []byte{0xff}) {
			t.Fatal("lost invalid header evidence", r)
		}
	}
}
