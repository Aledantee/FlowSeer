package syslog_test

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

func BenchmarkReceiveTCP(b *testing.B) {
	receiver, err := syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: syslog.TCP, Address: "127.0.0.1:0"}}, syslog.ReceiverOptions{})
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := receiver.Close(); err != nil {
			b.Error(err)
		}
	}()
	conn, err := net.Dial("tcp", receiver.Addresses()[0].Address)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	payload := []byte("<134>Sep 03 10:00:00 host app: a message")
	frame := append([]byte(strconv.Itoa(len(payload))+" "), payload...)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := conn.Write(frame); err != nil {
			b.Fatal(err)
		}
		if _, err := receiver.Next(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseLargeSD(b *testing.B) {
	parser, err := syslog.NewParser(syslog.ParseOptions{CaptureRaw: true})
	if err != nil {
		b.Fatal(err)
	}
	payload := append([]byte(`<134>1 2026-09-03T10:00:00Z host app - - [x a="`), bytes.Repeat([]byte("x"), 24000)...)
	payload = append(payload, []byte(`"] body`)...)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parser.Parse(payload, syslog.Observation{}); err != nil {
			b.Fatal(err)
		}
	}
}
