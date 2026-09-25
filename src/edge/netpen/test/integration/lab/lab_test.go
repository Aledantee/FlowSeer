//go:build netpen_t2

package lab

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"go.aledante.io/FlowSeer/src/common/secret"
)

func TestConfigFromEnv(t *testing.T) {
	values := map[string]string{
		envInjectorHost:          "172.16.0.21",
		envInjectorUser:          "aledante",
		envInjectorPrivateKey:    "/tmp/lab_ed25519",
		envInjectorHostKeySHA256: "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU",
		envInjectorSudoPassword:  "injector-secret",
		envInjectionInterface:    "eth0",
		envTargetHost:            "172.16.0.42",
		envTargetUser:            "lab",
		envTargetPassword:        "labIt123",
		envTargetHostKeySHA256:   "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		envTargetPlatform:        "iosxe",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv() = %v", err)
	}
	if cfg.InjectorHost != values[envInjectorHost] || cfg.InjectionInterface != values[envInjectionInterface] {
		t.Errorf("ConfigFromEnv() injector = %#v, want host %q and interface %q", cfg, values[envInjectorHost], values[envInjectionInterface])
	}
	if got := cfg.InjectorSudoPassword.RevealString(); got != values[envInjectorSudoPassword] {
		t.Errorf("ConfigFromEnv() injector sudo password = %q, want configured value", got)
	}
	if got := cfg.TargetPassword.RevealString(); got != values[envTargetPassword] {
		t.Errorf("ConfigFromEnv() target password = %q, want configured value", got)
	}

	for name := range values {
		t.Run("missing "+name, func(t *testing.T) {
			t.Setenv(name, "")
			_, err := ConfigFromEnv()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("ConfigFromEnv() error = %v, want error naming %s", err, name)
			}
		})
	}
}

func TestInjectCommand(t *testing.T) {
	cfg := Config{InjectionInterface: "eth0"}
	got := cfg.InjectCommand("ospf", 2*time.Second, 20*time.Second)
	want := []string{"sudo", "-S", "-p", "", "netpen", "ospf", "-i", "eth0", "--json=true", "--duration", "2s", "--timeout", "20s"}
	if !slices.Equal(got, want) {
		t.Errorf("InjectCommand() = %q, want %q", got, want)
	}
}

func TestRunInjectorSuppliesSudoPasswordOnStdin(t *testing.T) {
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientKeyBlock, err := ssh.MarshalPrivateKey(clientKey, "test")
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}
	clientKeyPath := t.TempDir() + "/id_ed25519"
	if err := os.WriteFile(clientKeyPath, pem.EncodeToMemory(clientKeyBlock), 0o600); err != nil {
		t.Fatalf("write client key: %v", err)
	}

	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromSigner(hostKey)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	type observation struct {
		command string
		stdin   string
	}
	observed := make(chan observation, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.Close() }()

		serverConfig := &ssh.ServerConfig{
			PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
				return nil, nil
			},
		}
		serverConfig.AddHostKey(hostSigner)
		serverConn, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
		if err != nil {
			serverErr <- fmt.Errorf("accept SSH connection: %w", err)
			return
		}
		defer func() { _ = serverConn.Close() }()
		go ssh.DiscardRequests(requests)

		newChannel, ok := <-channels
		if !ok {
			serverErr <- fmt.Errorf("SSH client opened no channel")
			return
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			serverErr <- fmt.Errorf("accept session channel: %w", err)
			return
		}
		defer func() { _ = channel.Close() }()

		request, ok := <-channelRequests
		if !ok || request.Type != "exec" {
			serverErr <- fmt.Errorf("first channel request = %v, want exec", request)
			return
		}
		var execRequest struct{ Command string }
		if err := ssh.Unmarshal(request.Payload, &execRequest); err != nil {
			serverErr <- fmt.Errorf("decode exec request: %w", err)
			return
		}
		if err := request.Reply(true, nil); err != nil {
			serverErr <- fmt.Errorf("reply to exec request: %w", err)
			return
		}
		stdin, err := io.ReadAll(channel)
		if err != nil {
			serverErr <- fmt.Errorf("read command stdin: %w", err)
			return
		}
		observed <- observation{command: execRequest.Command, stdin: string(stdin)}
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{}))
	}()

	cfg := Config{
		InjectorHost:           listener.Addr().String(),
		InjectorUser:           "tester",
		InjectorPrivateKeyPath: clientKeyPath,
		InjectorHostKeySHA256:  ssh.FingerprintSHA256(hostSigner.PublicKey()),
		InjectorSudoPassword:   secret.NewString("injector-secret"),
		InjectionInterface:     "eth0",
	}
	argv := cfg.InjectCommand("ospf", 2*time.Second, 20*time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := RunInjector(ctx, cfg, argv)
	if err != nil {
		t.Fatalf("RunInjector() = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("RunInjector() exit code = %d, want 0", result.ExitCode)
	}

	select {
	case got := <-observed:
		if want := "'sudo' '-S' '-p' '' 'netpen' 'ospf' '-i' 'eth0' '--json=true' '--duration' '2s' '--timeout' '20s'"; got.command != want {
			t.Errorf("injector command = %q, want %q", got.command, want)
		}
		if got.stdin != "injector-secret\n" {
			t.Errorf("injector stdin = %q, want sudo password and newline", got.stdin)
		}
	case err := <-serverErr:
		t.Fatalf("SSH server: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestValidateHostKeyPin(t *testing.T) {
	const valid = "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU"
	if err := validateHostKeyPin(valid); err != nil {
		t.Errorf("validateHostKeyPin(valid) = %v", err)
	}
	if err := validateHostKeyPin("SHA256:not-a-fingerprint"); err == nil {
		t.Fatal("validateHostKeyPin(malformed) = nil error, want refusal")
	}
}
