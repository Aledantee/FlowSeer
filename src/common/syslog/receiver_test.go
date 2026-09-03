package syslog_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

func TestReceiverUDPDropNewAndOwnership(t *testing.T) {
	r, err := syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: syslog.UDP, Address: "127.0.0.1:0"}}, syslog.ReceiverOptions{Parse: syslog.ParseOptions{CaptureRaw: true}, Limits: syslog.Limits{MaxFrames: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	}()
	conn, err := net.Dial("udp", r.Addresses()[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	first := []byte("<13>Sep 03 10:00:00 host app: first\ninside\x00")
	if _, err := conn.Write(first); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return r.Stats().Queued == 1 })
	if _, err := conn.Write([]byte("new arrival")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return r.Stats().UDPDropped == 1 })
	before := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	record, err := r.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(*record.Raw) != string(first) || record.Observation.ReceivedAt.After(before) {
		t.Fatal("payload or receive timestamp changed")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if r.Stats().ReservedBytes != 0 || string(*record.Raw) != string(first) {
		t.Fatal("retention or reservation")
	}
	if _, err := r.Next(ctx); !errors.Is(err, syslog.ErrClosed) {
		t.Fatal(err)
	}
}

func TestReceiverTCPFraming(t *testing.T) {
	r, err := syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: syslog.TCP, Address: "127.0.0.1:0"}}, syslog.ReceiverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	conn, err := net.Dial("tcp", r.Addresses()[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("3 bad<13>Sep 03 10:00:00 h a: good\n")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	one, err := r.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	two, err := r.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if one.Format != syslog.Unknown || two.Format != syslog.RFC3164 || !one.Observation.ReceivedAt.Equal(two.Observation.ReceivedAt) {
		t.Fatal(one, two)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition timeout")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestUnusableStreamBudgetRejected(t *testing.T) {
	r, err := syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: syslog.TCP, Address: "127.0.0.1:0"}}, syslog.ReceiverOptions{Limits: syslog.Limits{MaxPayload: 4096, MaxBytes: 28000, MaxFrames: 1, MaxConnections: 1, MetadataBytes: 256}})
	if r != nil {
		_ = r.Close()
	}
	if !errors.Is(err, syslog.ErrLimit) {
		t.Fatal("budget cannot admit a connection but startup succeeded", err)
	}
}

func TestUDPOversizeAndCanceledNext(t *testing.T) {
	r, err := syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: syslog.UDP, Address: "127.0.0.1:0"}}, syslog.ReceiverOptions{Parse: syslog.ParseOptions{CaptureRaw: true}, Limits: syslog.Limits{MaxPayload: 64, MaxFrames: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	conn, err := net.Dial("udp", r.Addresses()[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(make([]byte, 65)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return r.Stats().Oversized == 1 })
	if _, err := conn.Write([]byte("retained")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return r.Stats().Queued == 1 })
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Next(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	next, cancelNext := context.WithTimeout(context.Background(), time.Second)
	defer cancelNext()
	record, err := r.Next(next)
	if err != nil || string(*record.Raw) != "retained" {
		t.Fatal(record, err)
	}
}
