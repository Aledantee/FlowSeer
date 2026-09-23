//go:build netpen_t2

package lab

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	values := map[string]string{
		envInjectorHost:          "172.16.0.21",
		envInjectorUser:          "aledante",
		envInjectorPrivateKey:    "/tmp/lab_ed25519",
		envInjectorHostKeySHA256: "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU",
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
	want := []string{"netpen", "ospf", "-i", "eth0", "--json=true", "--duration", "2s", "--timeout", "20s"}
	if !slices.Equal(got, want) {
		t.Errorf("InjectCommand() = %q, want %q", got, want)
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
