package runner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// hasSysctlCode reports whether err carries [ErrCodeSysctl].
func hasSysctlCode(err error) bool {
	code, ok := errs.CodeOf(err)
	return ok && code == ErrCodeSysctl
}

// TestProcSysctlMapsDottedKeysUnderRoot pins the path rule: a dotted key
// is a file under the root with dots as separators, Get trims the newline
// the kernel appends, Set writes the value, and a key that is not dotted
// segments or names a missing file is a coded error.
func TestProcSysctlMapsDottedKeysUnderRoot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "net", "ipv4")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "ip_forward")
	if err := os.WriteFile(file, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := procSysctl{root: root}

	got, err := s.Get("net.ipv4.ip_forward")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "0" {
		t.Errorf("Get = %q, want %q", got, "0")
	}
	if err := s.Set("net.ipv4.ip_forward", "1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1" {
		t.Errorf("file holds %q after Set, want %q", data, "1")
	}

	for _, key := range []string{"net.ipv4.missing", "net..ip_forward", "../etc/passwd", "net/ipv4/ip_forward"} {
		if _, err := s.Get(key); !hasSysctlCode(err) {
			t.Errorf("Get(%q) error = %v, want one carrying ErrCodeSysctl", key, err)
		}
		if err := s.Set(key, "1"); !hasSysctlCode(err) {
			t.Errorf("Set(%q) error = %v, want one carrying ErrCodeSysctl", key, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Set on a missing key created the file, want none")
	}
}

// TestMapSysctlRoundTrip pins the map seam: a seeded key reads back, a
// Set replaces it, and an absent key is a coded error.
func TestMapSysctlRoundTrip(t *testing.T) {
	m := NewMapSysctl(map[string]string{"net.ipv4.ip_forward": "0"})
	got, err := m.Get("net.ipv4.ip_forward")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "0" {
		t.Errorf("Get = %q, want %q", got, "0")
	}
	if err := m.Set("net.ipv4.ip_forward", "1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, _ := m.Get("net.ipv4.ip_forward"); got != "1" {
		t.Errorf("Get after Set = %q, want %q", got, "1")
	}
	if _, err := m.Get("net.ipv6.conf.all.forwarding"); !hasSysctlCode(err) {
		t.Errorf("Get of an absent key error = %v, want one carrying ErrCodeSysctl", err)
	}
}
