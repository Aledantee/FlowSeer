package ssh

import (
	"net"
	"time"

	"golang.org/x/crypto/ssh"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
)

// Default timeouts and buffer sizes. Every constant here except
// defaultMaxOutputBytes is applied by [Options.withDefaults];
// defaultMaxOutputBytes has no Options field of its own and is
// applied directly by [Session.Run] to Command.MaxOutput.
const (
	defaultDialTimeout       = 30 * time.Second
	defaultCommandDeadline   = 60 * time.Second
	defaultPTYTerm           = "vt100"
	defaultPTYWidth          = 200
	defaultPTYHeight         = 50
	defaultStdoutBufferBytes = 64 * 1024
	defaultStderrBufferBytes = 16 * 1024
	defaultMaxOutputBytes    = 1024 * 1024
)

// Options configures SSH authentication, host-key verification, the
// shell's PTY, and the session's buffer sizes. [Dial] requires
// credentials and explicit host-key verification. Options may be read
// concurrently but must not be changed while being passed to [Dial].
type Options struct {
	// Username authenticates the SSH transport. Required.
	Username string
	// Password enables SSH password authentication when non-empty.
	Password secret.Value
	// PrivateKeyPEM enables SSH public-key authentication when
	// non-empty. Both may be set; the transport offers both.
	PrivateKeyPEM secret.Value

	// HostKeySHA256 pins the peer's host key as the base64 SHA-256
	// fingerprint (the ssh-keygen -lf form, with or without the
	// "SHA256:" prefix). Exactly one of HostKeySHA256 or
	// InsecureIgnoreHostKey must be set: host-key verification has no
	// implicit default.
	HostKeySHA256 string
	// InsecureIgnoreHostKey disables host-key verification. An
	// explicit lab-device opt-in, never a default.
	InsecureIgnoreHostKey bool

	// DialTimeout bounds TCP connection plus SSH handshake.
	// Non-positive values mean 30s.
	DialTimeout time.Duration
	// CommandDeadline bounds a Run call when Command.Deadline is
	// zero. Non-positive values mean 60s.
	CommandDeadline time.Duration

	// PTYTerm names the terminal type requested for the shell's PTY.
	// Empty means "vt100".
	PTYTerm string
	// PTYWidth and PTYHeight size the shell's PTY in character cells.
	// Non-positive values default to 200x50, wide enough that most
	// device CLIs do not wrap prompt or pagination text mid-pattern.
	PTYWidth  int
	PTYHeight int

	// StdoutBufferBytes and StderrBufferBytes bound the drop-oldest
	// buffers the drain goroutines write into. Non-positive values
	// default to 64KiB and 16KiB.
	StdoutBufferBytes int
	StderrBufferBytes int
}

// withDefaults returns a copy with zero fields defaulted.
func (o Options) withDefaults() Options {
	if o.DialTimeout <= 0 {
		o.DialTimeout = defaultDialTimeout
	}
	if o.CommandDeadline <= 0 {
		o.CommandDeadline = defaultCommandDeadline
	}
	if o.PTYTerm == "" {
		o.PTYTerm = defaultPTYTerm
	}
	if o.PTYWidth <= 0 {
		o.PTYWidth = defaultPTYWidth
	}
	if o.PTYHeight <= 0 {
		o.PTYHeight = defaultPTYHeight
	}
	if o.StdoutBufferBytes <= 0 {
		o.StdoutBufferBytes = defaultStdoutBufferBytes
	}
	if o.StderrBufferBytes <= 0 {
		o.StderrBufferBytes = defaultStderrBufferBytes
	}
	return o
}

// sshConfig builds the SSH client configuration from the options.
func sshConfig(opts Options) (*ssh.ClientConfig, error) {
	if opts.Username == "" {
		return nil, errs.New().Code(ErrCodeTransport).Msg("options: username is required")
	}
	var auth []ssh.AuthMethod
	if !opts.PrivateKeyPEM.Empty() {
		signer, err := ssh.ParsePrivateKey(opts.PrivateKeyPEM.Reveal())
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeTransport).Msg("options: parse private key")
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if !opts.Password.Empty() {
		auth = append(auth, ssh.Password(opts.Password.RevealString()))
	}
	if len(auth) == 0 {
		return nil, errs.New().Code(ErrCodeTransport).Msg("options: a password or private key is required")
	}

	hostKey, err := hostKeyCallback(opts)
	if err != nil {
		return nil, err
	}

	return &ssh.ClientConfig{
		User:            opts.Username,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         opts.DialTimeout,
	}, nil
}

// hostKeyCallback builds the host-key verification callback. Exactly
// one of HostKeySHA256 or InsecureIgnoreHostKey must be set.
func hostKeyCallback(opts Options) (ssh.HostKeyCallback, error) {
	switch {
	case opts.InsecureIgnoreHostKey && opts.HostKeySHA256 != "":
		return nil, errs.New().Code(ErrCodeTransport).Msg("options: host-key pin and insecure opt-in are mutually exclusive")
	case opts.InsecureIgnoreHostKey:
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec // documented explicit lab opt-in, never a default
	case opts.HostKeySHA256 != "":
		want := opts.HostKeySHA256
		if !hasSHA256Prefix(want) {
			want = "SHA256:" + want
		}
		return pinnedHostKey(want), nil
	default:
		return nil, errs.New().Code(ErrCodeTransport).Msg("options: set HostKeySHA256 or the explicit InsecureIgnoreHostKey opt-in")
	}
}

// pinnedHostKey verifies the peer key against a SHA256:base64
// fingerprint pin. The observed key is never interpolated: a mismatch
// names only the host.
func pinnedHostKey(pin string) ssh.HostKeyCallback {
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		if got := ssh.FingerprintSHA256(key); got != pin {
			return errs.New().
				Code(ErrCodeTransport).
				Msgf("host key of %s does not match the pinned fingerprint", hostname)
		}
		return nil
	}
}

// hasSHA256Prefix reports whether s already carries the fingerprint
// scheme prefix.
func hasSHA256Prefix(s string) bool {
	return len(s) > 7 && s[:7] == "SHA256:"
}
