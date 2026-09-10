package ssh

import (
	"testing"

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
