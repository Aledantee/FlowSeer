package syslog

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type shortWriter struct {
	bytes.Buffer
	fail bool
}

func (w *shortWriter) Write(b []byte) (int, error) {
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	if len(b) > 2 {
		b = b[:2]
	}
	return w.Buffer.Write(b)
}

func TestShortWrites(t *testing.T) {
	w := &shortWriter{}
	n, err := writeAll(w, []byte("abcdef"), false)
	if n != 6 || err != nil || w.String() != "abcdef" {
		t.Fatal(n, err, w.String())
	}
	w.fail = true
	n, err = writeAll(w, []byte("x"), false)
	if n != 0 || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(n, err)
	}
	w.fail = false
	n, err = writeAll(w, []byte("abcdef"), true)
	if n != 2 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(n, err)
	}
}

func TestSenderCancellationAndBusy(t *testing.T) {
	s, err := NewSender("127.0.0.1:1", TCP, SenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	left, right := net.Pipe()
	defer func() { _ = right.Close() }()
	s.conn = left
	s.raw = left
	record := Record{Priority: Text("13"), Content: []byte("hello")}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := s.Send(ctx, record, EncodeOptions{Format: RFC5424}); result <- err }()
	deadline := time.Now().Add(time.Second)
	for !s.busy.Load() {
		if time.Now().After(deadline) {
			t.Fatal("send start")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := s.Send(context.Background(), record, EncodeOptions{Format: RFC5424}); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("send cancellation")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
