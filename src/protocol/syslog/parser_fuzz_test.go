package syslog_test

import (
	"bytes"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

func FuzzParse(f *testing.F) {
	for _, entry := range corpus(f) {
		f.Add([]byte(entry.Payload))
	}
	p, err := syslog.NewParser(syslog.ParseOptions{CaptureRaw: true})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		before := bytes.Clone(b)
		r, err := p.Parse(b, syslog.Observation{})
		if len(b) > 65536 {
			if err == nil {
				t.Fatal("size limit")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if r.Raw == nil || !bytes.Equal(*r.Raw, b) || len(r.Diagnostics) > 16 || len(r.StructuredData) > 64 {
			t.Fatal("record invariant")
		}
		for i := range b {
			b[i] ^= 0xff
		}
		if !bytes.Equal(*r.Raw, before) {
			t.Fatal("input alias")
		}
	})
}
