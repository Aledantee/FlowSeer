package integration_test

import (
	"context"
	"errors"
	"net"
	"runtime"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

func TestLifecycleAndAtomicBind(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer closeChecked(t, occupied)
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := available.Addr().String()
	closeChecked(t, available)
	_, err = syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: syslog.TCP, Address: address}, {Transport: syslog.TCP, Address: occupied.Addr().String()}}, syslog.ReceiverOptions{})
	if err == nil {
		t.Fatal("conflicting bind succeeded")
	}
	available, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal("failed bind leaked listener", err)
	}
	closeChecked(t, available)
	baseline := runtime.NumGoroutine()
	for range 100 {
		r, err := syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: syslog.TCP, Address: "127.0.0.1:0"}}, syslog.ReceiverOptions{Limits: syslog.Limits{MaxConnections: 2, MaxFrames: 2}})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := net.Dial("tcp", r.Addresses()[0].Address)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte("100 partial")); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := r.Next(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		closeChecked(t, r)
		closeChecked(t, conn)
		if r.Stats().ReservedBytes != 0 {
			t.Fatal("reservation leak", r.Stats())
		}
	}
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > baseline+2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if runtime.NumGoroutine() > baseline+2 {
		t.Fatalf("goroutine leak before=%d after=%d", baseline, runtime.NumGoroutine())
	}
}

func TestPartialFrameDeadlineAndConnectionCap(t *testing.T) {
	r, err := syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: syslog.TCP, Address: "127.0.0.1:0"}}, syslog.ReceiverOptions{Limits: syslog.Limits{MaxConnections: 1, FrameTimeout: 500 * time.Millisecond, IdleTimeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeChecked(t, r)
	conn, err := net.Dial("tcp", r.Addresses()[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	defer closeChecked(t, conn)
	if _, err := conn.Write([]byte("100 x")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for r.Stats().ActiveConnections != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	second, err := net.Dial("tcp", r.Addresses()[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	defer closeChecked(t, second)
	deadline = time.Now().Add(2 * time.Second)
	for (r.Stats().ActiveConnections != 0 || r.Stats().ConnectionRejected == 0 || r.Stats().FramingErrors == 0) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.Stats().ActiveConnections != 0 || r.Stats().ConnectionRejected == 0 || r.Stats().FramingErrors == 0 {
		t.Fatal(r.Stats())
	}
}
