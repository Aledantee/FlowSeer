//go:build linux

package packetio

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestLinuxSenderUsesSendOnlyProtocol(t *testing.T) {
	if sendOnlyProtocol != 0 {
		t.Fatalf("AF_PACKET protocol = %d, want zero so the sender receives nothing", sendOnlyProtocol)
	}
}

type fakeWriteSocket struct {
	writes    [][]byte
	writeN    int
	writeErr  error
	closeErr  error
	closeCall int
}

func (s *fakeWriteSocket) write(p []byte) (int, error) {
	s.writes = append(s.writes, append([]byte(nil), p...))
	return s.writeN, s.writeErr
}

func (s *fakeWriteSocket) close() error {
	s.closeCall++
	return s.closeErr
}

func TestLinuxSenderWritesCompleteFrame(t *testing.T) {
	sock := &fakeWriteSocket{writeN: 4}
	sender := newLinuxSender(sock)

	if err := sender.Send(context.Background(), []byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := sock.writes; len(got) != 1 || string(got[0]) != "\x01\x02\x03\x04" {
		t.Fatalf("writes = %#v, want one complete frame", got)
	}
}

func TestLinuxSenderRejectsCancellationBeforeWrite(t *testing.T) {
	sock := &fakeWriteSocket{writeN: 1}
	sender := newLinuxSender(sock)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sender.Send(ctx, []byte{1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send error = %v, want context.Canceled", err)
	}
	if len(sock.writes) != 0 {
		t.Fatal("canceled send reached the socket")
	}
}

func TestLinuxSenderReportsShortAndSyscallWrites(t *testing.T) {
	sock := &fakeWriteSocket{writeN: 1}
	sender := newLinuxSender(sock)
	if err := sender.Send(context.Background(), []byte{1, 2}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short Send error = %v, want io.ErrShortWrite", err)
	}

	syscallErr := errors.New("sendto failed")
	sock = &fakeWriteSocket{writeN: 0, writeErr: syscallErr}
	sender = newLinuxSender(sock)
	if err := sender.Send(context.Background(), []byte{1}); !errors.Is(err, syscallErr) {
		t.Fatalf("syscall Send error = %v, want %v", err, syscallErr)
	}
}

func TestLinuxSenderCloseIsIdempotent(t *testing.T) {
	sock := &fakeWriteSocket{closeErr: errors.New("close failed")}
	sender := newLinuxSender(sock)
	if err := sender.Close(); err == nil {
		t.Fatal("first Close returned nil, want close error")
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if sock.closeCall != 1 {
		t.Fatalf("close calls = %d, want 1", sock.closeCall)
	}
}

func TestLinuxSenderRejectsSendAfterClose(t *testing.T) {
	sock := &fakeWriteSocket{writeN: 1}
	sender := newLinuxSender(sock)
	if err := sender.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !errors.Is(sender.Send(context.Background(), []byte{1}), ErrClosed) {
		t.Fatal("Send after Close did not return ErrClosed")
	}
}
