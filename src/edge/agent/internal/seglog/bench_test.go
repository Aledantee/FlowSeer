package seglog

import (
	"context"
	"testing"
	"time"
)

func BenchmarkAppend(b *testing.B) {
	l, err := Open(Config{Dir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := l.Close(); err != nil {
			b.Fatal(err)
		}
	})
	record := make([]byte, 256)
	b.SetBytes(256)
	b.ReportAllocs()
	for b.Loop() {
		if err := l.Append(record); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "records/s")
}

func BenchmarkAppendNextAck(b *testing.B) {
	l, err := Open(Config{Dir: b.TempDir(), SegmentBytes: 284, MaxRecordBytes: 256, Linger: time.Nanosecond})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := l.Close(); err != nil {
			b.Fatal(err)
		}
	})
	record := make([]byte, 256)
	b.SetBytes(256)
	b.ReportAllocs()
	for b.Loop() {
		if err := l.Append(record); err != nil {
			b.Fatal(err)
		}
		s, err := l.Next(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		if err := l.Ack(s.Sequence); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "records/s")
}
