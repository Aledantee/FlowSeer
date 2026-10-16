package syslogsource_test

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/src/edge/agent/internal/seglog"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/syslogsource"
	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

func TestSyslogMaximumPayloadFitsLogRecord(t *testing.T) {
	const header = "<34>1 - - - - - [x"
	payload := header + strings.Repeat(` a=""`, (65535-len(header)-1)/5) + "]"
	payload += strings.Repeat(" ", 65535-len(payload))
	if len(payload) != 65535 {
		t.Fatalf("payload size = %d, want 65535", len(payload))
	}
	opts := syslogsource.ReceiverOptions()
	opts.Parse.Limits = opts.Limits
	// Exercise the envelope bound with every parameter retained, even though
	// the receiver normally stops at its smaller metadata budget.
	opts.Parse.Limits.MaxParameters = 65535
	opts.Parse.Limits.MetadataBytes = 4 << 20
	parser, err := syslog.NewParser(opts.Parse)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := parser.Parse([]byte(payload), syslog.Observation{
		ReceivedAt: time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC),
		Peer:       netip.MustParseAddrPort("192.0.2.1:514"), Transport: syslog.UDP,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.StructuredData) != 1 || len(rec.StructuredData[0].Parameters) != (65535-len(header)-1)/5 {
		t.Fatalf("structured data = %d elements, want every empty parameter retained", len(rec.StructuredData))
	}
	mapped, provenance, _ := syslogsource.MapRecord(rec, testDeviceEntry(), testEdgeRef())
	if got := len(mapped.GetStructuredData()); got != 1 {
		t.Fatalf("mapped structured data = %d elements, want 1", got)
	}
	if got, want := len(mapped.GetStructuredData()[0].GetParams()), (65535-len(header)-1)/5; got != want {
		t.Fatalf("mapped parameters = %d, want %d", got, want)
	}
	ids := make([]string, 16)
	for i := range ids {
		ids[i] = fmt.Sprintf("0192e6a0-0000-7000-8000-%012d", i+1)
	}
	env := syslogsource.BuildEnvelope(testDeviceID, provenance, mapped, syslogsource.WindowEvidence([]byte(payload), ids))
	if len(env.GetRaw().GetData()) != 65535 || len(env.GetRaw().GetWindows()) != 16 {
		t.Fatalf("raw evidence = %d bytes, %d windows, want 65535 and 16", len(env.GetRaw().GetData()), len(env.GetRaw().GetWindows()))
	}
	if err := protovalidate.Validate(env); err != nil {
		t.Fatal(err)
	}
	if got := proto.Size(env); got > 262144 {
		t.Fatalf("proto.Size = %d, want at most 262144", got)
	} else {
		t.Logf("proto.Size = %d", got)
	}
	data, err := proto.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	log, err := seglog.Open(seglog.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	if err := log.Append(data); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	segment, err := log.Next(ctx)
	if err != nil || len(segment.Records) != 1 {
		t.Fatalf("Next = %v, %v", segment, err)
	}
}
