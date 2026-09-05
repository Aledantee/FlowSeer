// Package bench compares owned RFC 5424 parsing on a shared standards subset.
package bench

import (
	"strconv"
	"testing"

	"github.com/leodido/go-syslog/v4/rfc5424"

	"go.aledante.io/FlowSeer/src/protocol/syslog"
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
	other, ok := b.(*rfc5424.SyslogMessage)
	if !ok {
		t.Fatal("unexpected comparison record")
	}
	if other.Timestamp == nil {
		t.Fatal("comparison parser reported no timestamp")
	}
	if !other.Timestamp.Equal(*a.DeviceTime.Instant) {
		t.Errorf("timestamp: got %q, want %q", other.Timestamp, a.DeviceTime.Instant)
	}
	fields := []struct {
		name string
		got  *string
		want string
	}{
		{"priority", stringPtr(other.Priority), a.Priority.Value},
		{"hostname", other.Hostname, a.Hostname.Value},
		{"appname", other.Appname, a.Application.Value},
		{"procid", other.ProcID, a.ProcessID.Value},
		{"msgid", other.MsgID, a.MessageID.Value},
		{"message", other.Message, string(a.Content)},
	}
	for _, f := range fields {
		if f.got == nil {
			t.Errorf("%s: got no value, want %q", f.name, f.want)
			continue
		}
		if *f.got != f.want {
			t.Errorf("%s: got %q, want %q", f.name, *f.got, f.want)
		}
	}
	if other.StructuredData == nil {
		t.Fatal("comparison parser reported no structured data")
	}
	got := (*other.StructuredData)["origin@32473"]["ip"]
	if want := string(a.StructuredData[0].Parameters[0].Value); got != want {
		t.Errorf("structured data origin@32473 ip: got %q, want %q", got, want)
	}
}

// stringPtr renders the comparison parser's numeric priority as the decimal
// string the owned parser records, or nil when the field is absent.
func stringPtr(p *uint8) *string {
	if p == nil {
		return nil
	}
	s := strconv.Itoa(int(*p))
	return &s
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
