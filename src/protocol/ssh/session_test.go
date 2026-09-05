package ssh_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"

	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

func quietShell(_ *testing.T, _ xssh.Channel) {
	// Deliberately silent: nothing to prove for these tests beyond
	// a successful dial and clean close.
}

func optsFor(fs *fakeServer) ssh.Options {
	return ssh.Options{
		Username:      fs.username,
		Password:      fs.password,
		HostKeySHA256: fs.fp,
	}
}

func TestDialPinnedHostKeySucceeds(t *testing.T) {
	t.Parallel()
	fsA := newFakeServer(t, quietShell)
	fsB := newFakeServer(t, quietShell)

	for name, fs := range map[string]*fakeServer{"serverA": fsA, "serverB": fsB} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s, err := ssh.Dial(ctx, fs.addr, optsFor(fs))
			if err != nil {
				t.Fatalf("Dial() = %v, want success against its own pinned key", err)
			}
			defer func() { _ = s.Close() }()
		})
	}
}

func TestDialHostKeyMismatchRefused(t *testing.T) {
	t.Parallel()
	fsA := newFakeServer(t, quietShell)
	fsB := newFakeServer(t, quietShell)

	opts := optsFor(fsA)
	opts.HostKeySHA256 = fsB.fp // wrong pin for fsA

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ssh.Dial(ctx, fsA.addr, opts)
	if err == nil {
		t.Fatal("Dial() = nil error, want refusal on host-key mismatch")
	}
	if strings.Contains(err.Error(), fsA.fp) {
		t.Fatalf("Dial() error interpolates the observed fingerprint: %v", err)
	}
}

func TestDialOptionsRequireExplicitHostKeyVerification(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, quietShell)

	cases := []struct {
		name string
		opts ssh.Options
	}{
		{"neither set", ssh.Options{Username: fs.username, Password: fs.password}},
		{"both set", ssh.Options{Username: fs.username, Password: fs.password, HostKeySHA256: fs.fp, InsecureIgnoreHostKey: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := ssh.Dial(ctx, fs.addr, tc.opts)
			if err == nil {
				t.Fatal("Dial() = nil error, want a refusal before any host-key check")
			}
		})
	}
}

func TestDialTimeoutOnStalledHandshake(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Accept but never write the SSH version line: the
		// handshake can never complete.
		<-stop
	}()

	opts := ssh.Options{
		Username:      "tester",
		Password:      "swordfish",
		HostKeySHA256: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		DialTimeout:   500 * time.Millisecond,
	}
	ctx := context.Background()
	start := time.Now()
	_, err = ssh.Dial(ctx, ln.Addr().String(), opts)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Dial() = nil error, want a timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Dial() took %s, want it bounded by DialTimeout", elapsed)
	}
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, quietShell)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := ssh.Dial(ctx, fs.addr, optsFor(fs))
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first Close() = %v, want nil", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close() = %v, want nil (idempotent)", err)
	}
}
