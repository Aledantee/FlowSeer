package stream_test

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/pcap"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
)

func captureFixture(t *testing.T) []pcap.Record {
	t.Helper()
	wire, err := os.ReadFile("../../net/pcap/testdata/classic_micro_le.pcap")
	if err != nil {
		t.Fatal(err)
	}
	r, err := pcap.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	var records []pcap.Record
	for {
		record, err := r.Next()
		if err == io.EOF {
			return records
		}
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
}

func TestCaptureSourceOffsetsAndCopies(t *testing.T) {
	records := captureFixture(t)
	source, err := stream.NewCaptureSource(records)
	if err != nil {
		t.Fatal(err)
	}
	records[0].Data[14] = 0xff

	at, first, ok := source.Next()
	if !ok || at != 0 {
		t.Fatalf("first offset = %s, ok %t, want zero and true", at, ok)
	}
	if first.Src != [6]byte{2, 0, 0, 0, 0, 1} || first.Dst != [6]byte{2, 0, 0, 0, 0, 2} {
		t.Errorf("first MACs = %s -> %s, want captured source and destination", first.Src, first.Dst)
	}
	if first.Payload[0] != 0 {
		t.Errorf("first payload[0] = %d, want snapshot byte 0", first.Payload[0])
	}
	first.Payload[0] = 0xee

	at, second, ok := source.Next()
	if !ok || at != 250*time.Microsecond {
		t.Fatalf("second offset = %s, ok %t, want 250us and true", at, ok)
	}
	if second.Payload[0] != 0 || second.Src != first.Src || second.Dst != first.Dst {
		t.Errorf("second frame = %+v, want independent bytes and preserved MACs", second)
	}
}

func TestCaptureSourceCloneAndExhaustion(t *testing.T) {
	source, err := stream.NewCaptureSource(captureFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := source.Next(); !ok {
		t.Fatal("first frame missing")
	}
	clone := source.Clone()
	for _, current := range []stream.Source{source, clone} {
		at, frame, ok := current.Next()
		if !ok || at != 250*time.Microsecond || frame.Src != [6]byte{2, 0, 0, 0, 0, 1} {
			t.Errorf("clone cursor = %s, ok %t, frame %+v, want second frame", at, ok, frame)
		}
		if frame.Payload[0] != 0 {
			t.Errorf("clone payload[0] = %d, want independent byte 0", frame.Payload[0])
		}
		frame.Payload[0] = 0xee
		for range 2 {
			if _, _, ok := current.Next(); ok {
				t.Error("source returned a frame after exhaustion")
			}
		}
	}
}

func TestCaptureSourceEqualTimestampsKeepOrder(t *testing.T) {
	records := captureFixture(t)
	records[1].At = records[0].At
	records[1].Data[14] = 1
	source, err := stream.NewCaptureSource(records)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		at, frame, ok := source.Next()
		if !ok {
			t.Fatalf("frame %d missing", i)
		}
		if at != 0 || frame.Payload[0] != byte(i) {
			t.Errorf("frame %d = offset %s, payload[0] %d; want offset zero and byte %d", i, at, frame.Payload[0], i)
		}
	}
}

func TestCaptureSourceEmpty(t *testing.T) {
	source, err := stream.NewCaptureSource(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range []stream.Source{source, source.Clone()} {
		if _, _, ok := current.Next(); ok {
			t.Error("empty source returned a frame")
		}
	}
}

func TestCaptureSourceRefusals(t *testing.T) {
	records := captureFixture(t)
	complete := records[0]
	truncated := complete
	truncated.OrigLen = 64
	withFCS := complete
	withFCS.HasFCS = true
	badHeader := complete
	badHeader.Data = complete.Data[:13]
	badHeader.OrigLen = 13
	decreasing := records[1]
	decreasing.At = complete.At.Add(-time.Nanosecond)
	overflow := records[1]
	overflow.At = complete.At.Add(time.Duration(1<<63 - 1)).Add(time.Nanosecond)
	for _, tc := range []struct {
		name    string
		records []pcap.Record
		part    string
	}{
		{"non-Ethernet link", []pcap.Record{{At: complete.At, Data: complete.Data, OrigLen: 60, LinkType: 276}}, "276"},
		{"truncated capture", []pcap.Record{truncated}, "original length"},
		{"declared FCS", []pcap.Record{withFCS}, "FCS"},
		{"bad Ethernet header", []pcap.Record{badHeader}, "Ethernet frame"},
		{"decreasing timestamps", []pcap.Record{complete, decreasing}, "decreases"},
		{"duration overflow", []pcap.Record{complete, overflow}, "time.Duration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := stream.NewCaptureSource(tc.records)
			if err == nil {
				t.Fatal("NewCaptureSource succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.part) {
				t.Errorf("error = %q, want %q", err, tc.part)
			}
		})
	}
}
