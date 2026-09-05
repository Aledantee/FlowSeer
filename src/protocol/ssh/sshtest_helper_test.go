package ssh_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

// fakeServer is a minimal in-process SSH server: one TCP listener, a
// fixed host key, password authentication, and a per-connection shell
// handler the test supplies. It exists so the package's tests exercise
// Dial and Run against a real golang.org/x/crypto/ssh server rather
// than a mocked transport.
type fakeServer struct {
	addr     string
	hostKey  ssh.Signer
	username string
	password string
	handle   func(t *testing.T, ch ssh.Channel)
	listener net.Listener
	fp       string // SHA256 fingerprint of hostKey, "SHA256:..." form
}

// newFakeServer starts a listener with a freshly generated ed25519
// host key and the given shell handler, returning its address and
// pinned fingerprint. It stops when the test ends.
func newFakeServer(t *testing.T, handle func(t *testing.T, ch ssh.Channel)) *fakeServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromSigner(priv)
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
		fp:       ssh.FingerprintSHA256(signer.PublicKey()),
	}
	t.Cleanup(func() { _ = ln.Close() })
	go fs.serve(t)
	return fs
}

// serve accepts connections until the listener closes.
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
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) != fs.password {
				return nil, errors.New("wrong password")
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(fs.hostKey)

	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer func() { _ = sc.Close() }()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go fs.handleSession(t, ch, chReqs)
	}
}

// handleSession replies affirmatively to pty-req and shell requests
// (as most network device CLIs do), discards everything else, and
// invokes the test's handler exactly once, when "shell" arrives.
func (fs *fakeServer) handleSession(t *testing.T, ch ssh.Channel, reqs <-chan *ssh.Request) {
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
