package syslog_test

import (
	"bytes"
	"errors"
	"testing"

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
