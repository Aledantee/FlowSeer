package fastiron_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"

	xssh "golang.org/x/crypto/ssh"

	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/protocol/ssh"
)

// fakeServer is a minimal in-process SSH server: one TCP listener, a fixed
// host key, password authentication, and a per-connection shell handler
// the test supplies. It is the same pattern
// src/protocol/ssh/sshtest_helper_test.go uses, re-declared here because
// that file is package ssh_test and this package cannot import a _test.go
// file from another package.
type fakeServer struct {
	addr     string
	hostKey  xssh.Signer
	username string
	password string
	handle   func(t *testing.T, ch xssh.Channel)
	listener net.Listener
	fp       string
}

func newFakeServer(t *testing.T, handle func(t *testing.T, ch xssh.Channel)) *fakeServer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}

	signer, err := xssh.NewSignerFromSigner(priv)
	if err != nil {
		t.Fatalf("signer from key: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	fs := &fakeServer{
		addr:     ln.Addr().String(),
		hostKey:  signer,
		username: "tester",
		password: "swordfish",
		handle:   handle,
		listener: ln,
		fp:       xssh.FingerprintSHA256(signer.PublicKey()),
	}
	t.Cleanup(func() { _ = ln.Close() })
	go fs.serve(t)

	return fs
}

func (fs *fakeServer) serve(t *testing.T) {
	for {
		conn, err := fs.listener.Accept()
		if err != nil {
			return
		}

		go fs.handleConn(t, conn)
	}
}

func (fs *fakeServer) handleConn(t *testing.T, conn net.Conn) {
	cfg := &xssh.ServerConfig{
		PasswordCallback: func(_ xssh.ConnMetadata, password []byte) (*xssh.Permissions, error) {
			if string(password) != fs.password {
				return nil, errors.New("wrong password")
			}

			return nil, nil
		},
	}
	cfg.AddHostKey(fs.hostKey)

	sc, chans, reqs, err := xssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer func() { _ = sc.Close() }()

	go xssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(xssh.UnknownChannelType, "unsupported channel type")

			continue
		}

		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}

		go fs.handleSession(t, ch, chReqs)
	}
}

func (fs *fakeServer) handleSession(t *testing.T, ch xssh.Channel, reqs <-chan *xssh.Request) {
	defer func() { _ = ch.Close() }()

	started := false

	for req := range reqs {
		switch req.Type {
		case "pty-req", "shell":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}

			if req.Type == "shell" && !started {
				started = true

				go fs.handle(t, ch)
			}
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// dialSession connects a *ssh.Session to fs using its pinned fingerprint.
func dialSession(t *testing.T, fs *fakeServer) *ssh.Session {
	t.Helper()

	s, err := ssh.Dial(t.Context(), fs.addr, ssh.Options{
		Username:      fs.username,
		Password:      secret.NewString(fs.password),
		HostKeySHA256: fs.fp,
	})
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	t.Cleanup(func() { _ = s.Close() })

	return s
}

// readLine reads r until and including the next '\n'.
func readLine(r interface{ Read([]byte) (int, error) }) string {
	var buf []byte

	one := make([]byte, 1)

	for {
		n, err := r.Read(one)
		if n > 0 {
			buf = append(buf, one[0])
			if one[0] == '\n' {
				break
			}
		}

		if err != nil {
			break
		}
	}

	return string(buf)
}
