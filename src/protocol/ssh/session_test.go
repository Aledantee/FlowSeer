package ssh_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/secret"

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
		Password:      secret.NewString(fs.password),
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

func TestDialAcceptsBareFingerprintWithoutSHA256Prefix(t *testing.T) {
	t.Parallel()
	fs := newFakeServer(t, quietShell)
	opts := optsFor(fs)
	opts.HostKeySHA256 = strings.TrimPrefix(fs.fp, "SHA256:")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := ssh.Dial(ctx, fs.addr, opts)
	if err != nil {
		t.Fatalf("Dial() = %v, want success with a bare (unprefixed) pinned fingerprint", err)
	}
	defer func() { _ = s.Close() }()
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
		{"neither set", ssh.Options{Username: fs.username, Password: secret.NewString(fs.password)}},
		{"both set", ssh.Options{Username: fs.username, Password: secret.NewString(fs.password), HostKeySHA256: fs.fp, InsecureIgnoreHostKey: true}},
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
		Password:      secret.NewString("swordfish"),
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

// noCloseConn hides Close from the server side of a test connection, so the
// only end that can close the socket is the one under test.
type noCloseConn struct{ net.Conn }

func (noCloseConn) Close() error { return nil }

// TestDialClosesTheSocketWhenTheHandshakeFails covers the leak a refused
// host key would otherwise leave. NewClientConn does not close the
// connection it was given, and a device that caps concurrent sessions
// refuses the next login once a few have leaked — which reads as a wrong
// password rather than as exhaustion.
func TestDialClosesTheSocketWhenTheHandshakeFails(t *testing.T) {
	t.Parallel()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := xssh.NewSignerFromSigner(priv)
	if err != nil {
		t.Fatalf("signer from key: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
		cfg := &xssh.ServerConfig{
			PasswordCallback: func(xssh.ConnMetadata, []byte) (*xssh.Permissions, error) { return nil, nil },
		}
		cfg.AddHostKey(signer)
		// The server's own handshake fails once the client walks away, and
		// its end is held open through the wrapper so that an unclosed
		// client end reads as a socket still attached rather than as EOF.
		_, _, _, _ = xssh.NewServerConn(noCloseConn{conn}, cfg)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A pin for another key entirely, so the refusal happens in the
	// handshake rather than before it.
	if _, err := ssh.Dial(ctx, listener.Addr().String(), ssh.Options{
		Username:      "tester",
		Password:      secret.NewString("swordfish"),
		HostKeySHA256: "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU",
	}); err == nil {
		t.Fatal("Dial() = nil error, want a refusal on host-key mismatch")
	}

	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the listener never accepted the dial")
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 256)
	for {
		if _, err := conn.Read(buf); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatal("the refused handshake left the socket open")
			}
			return
		}
	}
}
