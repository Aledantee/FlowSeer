package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"testing"

	"golang.org/x/crypto/ssh"

	"go.aledante.io/FlowSeer/src/common/secret"
)

func TestSSHConfigRequiresUsername(t *testing.T) {
	_, err := sshConfig(Options{Password: secret.NewString("swordfish"), HostKeySHA256: "SHA256:whatever"})
	if err == nil {
		t.Fatal("sshConfig() = nil error, want a refusal when Username is empty")
	}
}

func TestSSHConfigRequiresACredential(t *testing.T) {
	_, err := sshConfig(Options{Username: "tester", HostKeySHA256: "SHA256:whatever"})
	if err == nil {
		t.Fatal("sshConfig() = nil error, want a refusal when neither Password nor PrivateKeyPEM is set")
	}
}

func TestSSHConfigRejectsUnparseablePrivateKey(t *testing.T) {
	_, err := sshConfig(Options{
		Username:      "tester",
		PrivateKeyPEM: secret.NewString("not a real key"),
		HostKeySHA256: "SHA256:whatever",
	})
	if err == nil {
		t.Fatal("sshConfig() = nil error, want a refusal for an unparseable private key")
	}
}

func TestSSHConfigDecryptsPrivateKeyWithPassphrase(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(private, "", []byte("open sesame"))
	if err != nil {
		t.Fatalf("encrypt key: %v", err)
	}
	encrypted := pem.EncodeToMemory(block)

	if _, err := sshConfig(Options{
		Username:      "tester",
		PrivateKeyPEM: secret.New(encrypted),
		HostKeySHA256: "SHA256:whatever",
	}); err == nil {
		t.Fatal("sshConfig() = nil error, want a refusal for an encrypted key without its passphrase")
	}
	cfg, err := sshConfig(Options{
		Username:             "tester",
		PrivateKeyPEM:        secret.New(encrypted),
		PrivateKeyPassphrase: secret.NewString("open sesame"),
		HostKeySHA256:        "SHA256:whatever",
	})
	if err != nil {
		t.Fatalf("sshConfig() with the passphrase: %v", err)
	}
	if len(cfg.Auth) != 1 {
		t.Fatalf("auth methods = %d, want the one public-key method", len(cfg.Auth))
	}
}

func TestHostKeyCallbackRequiresExplicitVerification(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"neither set", Options{}},
		{"both set", Options{HostKeySHA256: "SHA256:aaaa", InsecureIgnoreHostKey: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := hostKeyCallback(tc.opts); err == nil {
				t.Fatal("hostKeyCallback() = nil error, want a refusal before any host-key check")
			}
		})
	}
}

func TestHostKeyCallbackAcceptsBareAndPrefixedFingerprints(t *testing.T) {
	const bare = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, pin := range []string{bare, "SHA256:" + bare} {
		if _, err := hostKeyCallback(Options{HostKeySHA256: pin}); err != nil {
			t.Errorf("hostKeyCallback(HostKeySHA256: %q) = %v, want a callback built without error", pin, err)
		}
	}
}

func TestHasSHA256Prefix(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"SHA256:abc", true},
		{"abc", false},
		{"", false},
		{"SHA25", false},
	}
	for _, tc := range cases {
		if got := hasSHA256Prefix(tc.in); got != tc.want {
			t.Errorf("hasSHA256Prefix(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
