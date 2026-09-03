package syslog_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

func FuzzEncode(f *testing.F) {
	for _, entry := range corpus(f) {
		f.Add([]byte(entry.Payload))
	}
	p, err := syslog.NewParser(syslog.ParseOptions{})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := p.Parse(b, syslog.Observation{})
		if err != nil {
			return
		}
		for _, format := range []syslog.Format{syslog.RFC5424, syslog.RFC3164} {
			out, _, err := syslog.Encode(r, syslog.EncodeOptions{Format: format, AllowLoss: ^syslog.Loss(0)})
			if err != nil {
				if len(out) != 0 {
					t.Fatal("partial output")
				}
				continue
			}
			if len(out) > 65536 {
				t.Fatal("output limit")
			}
			again, err := p.Parse(out, syslog.Observation{})
			if err != nil || again.Format != format {
				t.Fatalf("encoded envelope %q %v", out, err)
			}
		}
	})
}
