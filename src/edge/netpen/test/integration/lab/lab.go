//go:build netpen_t2

// Package lab supplies live-lab configuration and remote injection for the
// netpen vendor-validation tier.
package lab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

const (
	envInjectorHost          = "NETPEN_LAB_INJECTOR_HOST"
	envInjectorUser          = "NETPEN_LAB_INJECTOR_USER"
	envInjectorPrivateKey    = "NETPEN_LAB_INJECTOR_PRIVATE_KEY"
	envInjectorHostKeySHA256 = "NETPEN_LAB_INJECTOR_HOST_KEY_SHA256"
	envInjectionInterface    = "NETPEN_LAB_INJECTION_INTERFACE"
	envTargetHost            = "NETPEN_LAB_TARGET_HOST"
	envTargetUser            = "NETPEN_LAB_TARGET_USER"
	envTargetPassword        = "NETPEN_LAB_TARGET_PASSWORD"
	envTargetHostKeySHA256   = "NETPEN_LAB_TARGET_HOST_KEY_SHA256"
	envTargetPlatform        = "NETPEN_LAB_TARGET_PLATFORM"
)

// Config names the injector and target endpoints for one live-lab run. Callers
// must not mutate a Config while another goroutine is using it.
type Config struct {
	// InjectorHost is the injector's SSH host, with an optional port.
	InjectorHost string
	// InjectorUser is the injector's SSH account.
	InjectorUser string
	// InjectorPrivateKeyPath is the local path to the injector SSH private key.
	InjectorPrivateKeyPath string
	// InjectorHostKeySHA256 pins the injector SSH host key.
	InjectorHostKeySHA256 string
	// InjectionInterface is the injector's data-plane interface.
	InjectionInterface string

	// TargetHost is the target's management SSH host, with an optional port.
	TargetHost string
	// TargetUser is the target's SSH account.
	TargetUser string
	// TargetPassword is the target's SSH password.
	TargetPassword secret.Value
	// TargetHostKeySHA256 pins the target SSH host key.
	TargetHostKeySHA256 string
	// TargetPlatform selects the target observable parser.
	TargetPlatform string
}

// InjectorResult contains the remote process streams and exit status. A
// nonzero exit status is a completed remote command, not a transport error.
type InjectorResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ConfigFromEnv reads and validates the live-lab environment. Its error names
// the first missing or malformed variable.
func ConfigFromEnv() (Config, error) {
	var cfg Config
	values := []struct {
		name string
		set  func(string)
	}{
		{envInjectorHost, func(value string) { cfg.InjectorHost = value }},
		{envInjectorUser, func(value string) { cfg.InjectorUser = value }},
		{envInjectorPrivateKey, func(value string) { cfg.InjectorPrivateKeyPath = value }},
		{envInjectorHostKeySHA256, func(value string) { cfg.InjectorHostKeySHA256 = value }},
		{envInjectionInterface, func(value string) { cfg.InjectionInterface = value }},
		{envTargetHost, func(value string) { cfg.TargetHost = value }},
		{envTargetUser, func(value string) { cfg.TargetUser = value }},
		{envTargetPassword, func(value string) { cfg.TargetPassword = secret.NewString(value) }},
		{envTargetHostKeySHA256, func(value string) { cfg.TargetHostKeySHA256 = value }},
		{envTargetPlatform, func(value string) { cfg.TargetPlatform = value }},
	}
	for _, variable := range values {
		value := os.Getenv(variable.name)
		if value == "" {
			return Config{}, fmt.Errorf("%s is required", variable.name)
		}
		variable.set(value)
	}
	if err := validateHostKeyPin(cfg.InjectorHostKeySHA256); err != nil {
		return Config{}, fmt.Errorf("%s: %w", envInjectorHostKeySHA256, err)
	}
	if err := validateHostKeyPin(cfg.TargetHostKeySHA256); err != nil {
		return Config{}, fmt.Errorf("%s: %w", envTargetHostKeySHA256, err)
	}

	return cfg, nil
}

// InjectCommand builds a deterministic netpen command for the configured data
// interface.
func (c Config) InjectCommand(attack string, duration, timeout time.Duration) []string {
	return []string{
		"netpen", attack, "-i", c.InjectionInterface, "--json=true",
		"--duration", duration.String(), "--timeout", timeout.String(),
	}
}

// RunInjector executes argv on the configured injector. It preserves stdout
// and stderr separately and returns context cancellation without wrapping it.
func RunInjector(ctx context.Context, cfg Config, argv []string) (InjectorResult, error) {
	if len(argv) == 0 {
		return InjectorResult{}, fmt.Errorf("injector command is empty")
	}

	privateKey, err := os.ReadFile(cfg.InjectorPrivateKeyPath)
	if err != nil {
		return InjectorResult{}, fmt.Errorf("read injector private key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return InjectorResult{}, fmt.Errorf("parse injector private key: %w", err)
	}
	if err := validateHostKeyPin(cfg.InjectorHostKeySHA256); err != nil {
		return InjectorResult{}, fmt.Errorf("injector host-key pin: %w", err)
	}
	address := sshAddress(cfg.InjectorHost)
	transport, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return InjectorResult{}, fmt.Errorf("dial injector: %w", err)
	}

	connection, channels, requests, err := ssh.NewClientConn(transport, address, &ssh.ClientConfig{
		User:            cfg.InjectorUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: pinnedHostKey(cfg.InjectorHostKeySHA256),
	})
	if err != nil {
		_ = transport.Close()
		return InjectorResult{}, fmt.Errorf("open injector SSH connection: %w", err)
	}
	client := ssh.NewClient(connection, channels, requests)
	defer func() { _ = client.Close() }()

	session, err := client.NewSession()
	if err != nil {
		return InjectorResult{}, fmt.Errorf("open injector SSH session: %w", err)
	}
	defer func() { _ = session.Close() }()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Start(shellCommand(argv)); err != nil {
		return InjectorResult{}, fmt.Errorf("start injector command: %w", err)
	}

	wait := make(chan error, 1)
	spawn.Go(ctx, "wait for netpen injector", func() { wait <- session.Wait() })
	select {
	case <-ctx.Done():
		_ = session.Close()
		return InjectorResult{}, ctx.Err()
	case err := <-wait:
		result := InjectorResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
		if err == nil {
			return result, nil
		}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitStatus()
			return result, nil
		}
		return InjectorResult{}, fmt.Errorf("wait for injector command: %w", err)
	}
}

func validateHostKeyPin(pin string) error {
	const prefix = "SHA256:"
	if !strings.HasPrefix(pin, prefix) {
		return fmt.Errorf("fingerprint must start with %s", prefix)
	}
	digest, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(pin, prefix))
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("fingerprint is not a SHA-256 digest")
	}
	return nil
}

func pinnedHostKey(pin string) ssh.HostKeyCallback {
	return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
		if ssh.FingerprintSHA256(key) != pin {
			return fmt.Errorf("host key of %s does not match the pinned fingerprint", hostname)
		}
		return nil
	}
}

func sshAddress(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "22")
}

func shellCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " ")
}
