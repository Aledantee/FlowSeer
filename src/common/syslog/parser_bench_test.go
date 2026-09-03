package syslog_test

import (
	"bytes"
	"testing"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

func BenchmarkParse(b *testing.B) {
	fixtures := corpus(b)
	for _, raw := range []bool{false, true} {
		name := "raw-off"
		if raw {
			name = "raw-on"
		}
		b.Run(name, func(b *testing.B) {
			p, err := syslog.NewParser(syslog.ParseOptions{CaptureRaw: raw})
			if err != nil {
				b.Fatal(err)
			}
			for _, f := range fixtures {
				b.Run(f.ID, func(b *testing.B) {
					payload := []byte(f.Payload)
					b.SetBytes(int64(len(payload)))
					b.ReportAllocs()
					for b.Loop() {
						if _, err := p.Parse(payload, syslog.Observation{}); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
			b.Run("mixed", func(b *testing.B) {
				payloads := make([][]byte, len(fixtures))
				total := 0
				for i, f := range fixtures {
					payloads[i] = []byte(f.Payload)
					total += len(f.Payload)
				}
				b.SetBytes(int64(total / len(fixtures)))
				b.ReportAllocs()
				i := 0
				for b.Loop() {
					if _, err := p.Parse(payloads[i%len(payloads)], syslog.Observation{}); err != nil {
						b.Fatal(err)
					}
					i++
				}
			})
		})
	}
}

func BenchmarkEncode(b *testing.B) {
	p, err := syslog.NewParser(syslog.ParseOptions{})
	if err != nil {
		b.Fatal(err)
	}
	payload := []byte(`<13>1 2026-09-03T10:00:00Z host app - - [x a="hello\]world"] body`)
	record, err := p.Parse(payload, syslog.Observation{})
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := syslog.Encode(record, syslog.EncodeOptions{Format: syslog.RFC5424}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseLargeVendor(b *testing.B) {
	parser, err := syslog.NewParser(syslog.ParseOptions{CaptureRaw: true})
	if err != nil {
		b.Fatal(err)
	}
	payload := append([]byte("<134>Sep 03 10:00:00 host ops-switchd[12]: Event|403|LOG_INFO|||"), bytes.Repeat([]byte("x"), 60000)...)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parser.Parse(payload, syslog.Observation{}); err != nil {
			b.Fatal(err)
		}
	}
}
