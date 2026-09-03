package syslog_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

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
	timestamp := regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,6})?(Z|[+-](0[0-9]|1[0-9]|2[0-3]):[0-5][0-9])$`)
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
			if format == syslog.RFC5424 {
				fields := strings.SplitN(string(out), " ", 8)
				if len(fields) < 7 {
					t.Fatalf("incomplete RFC5424 header: %q", out)
				}
				if fields[1] != "-" {
					if !timestamp.MatchString(fields[1]) {
						t.Fatalf("invalid RFC5424 timestamp: %q", fields[1])
					}
					if _, err := time.Parse(time.RFC3339Nano, fields[1]); err != nil {
						t.Fatal(err)
					}
				}
			}
			again, err := p.Parse(out, syslog.Observation{})
			if err != nil || again.Format != format {
				t.Fatalf("encoded envelope %q %v", out, err)
			}
		}
	})
}
