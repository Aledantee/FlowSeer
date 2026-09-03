// Package bench compares owned RFC 5424 parsing on a shared standards subset.
package bench

import (
	"testing"

	"github.com/leodido/go-syslog/v4/rfc5424"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

var standard = []byte(`<134>1 2026-09-03T10:00:00.123456Z switch app 42 ID [origin@32473 ip="192.0.2.1"] interface up`)

func TestCommonSubset(t *testing.T) {
	own, err := syslog.NewParser(syslog.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := own.Parse(standard, syslog.Observation{})
	if err != nil || a.Status != syslog.Complete {
		t.Fatal(a, err)
	}
	b, err := rfc5424.NewParser().Parse(standard)
	if err != nil || b == nil {
		t.Fatal(err)
	}
}

func BenchmarkCommonSubset(b *testing.B) {
	b.Run("flowseer", func(b *testing.B) {
		p, err := syslog.NewParser(syslog.ParseOptions{})
		if err != nil {
			b.Fatal(err)
		}
		b.SetBytes(int64(len(standard)))
		b.ReportAllocs()
		for b.Loop() {
			if _, err := p.Parse(standard, syslog.Observation{}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("leodido", func(b *testing.B) {
		p := rfc5424.NewParser()
		b.SetBytes(int64(len(standard)))
		b.ReportAllocs()
		for b.Loop() {
			if _, err := p.Parse(standard); err != nil {
				b.Fatal(err)
			}
		}
	})
}
