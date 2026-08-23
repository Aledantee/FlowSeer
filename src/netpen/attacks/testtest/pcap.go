// Package-level test helpers for reading pcap fixtures into raw frame bytes.
// Shared by behavior tests in attacks/l2 and (later) attacks/fh, attacks/ip6.

package testtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gopacket/gopacket/pcapgo"
)

// ReadPcap reads all packets from a pcap file and returns their raw bytes.
// The path is relative to the caller's working directory; behavior tests
// pass a path relative to the attacks/testdata tree.
func ReadPcap(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	r, err := pcapgo.NewReader(f)
	if err != nil {
		t.Fatalf("new pcap reader %s: %v", path, err)
	}

	var pkts [][]byte
	for {
		data, _, err := r.ReadPacketData()
		if err != nil {
			break
		}
		pkts = append(pkts, data)
	}
	if len(pkts) == 0 {
		t.Fatalf("no packets in %s", path)
	}
	return pkts
}

// FixturePath resolves a fixture name relative to the attacks testdata root.
// Callers pass e.g. FixturePath(t, "l2/dtp.pcap").
func FixturePath(t *testing.T, rel string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// From attacks/l2/, the testdata root is ../testdata; from attacks/testtest/
	// during self-test it is ../testdata. Resolve relative to the attacks/
	// directory by walking up to find "attacks".
	dir := wd
	for {
		if filepath.Base(dir) == "attacks" {
			return filepath.Join(dir, "testdata", rel)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Fallback: assume testdata is a sibling of the package dir.
	return filepath.Join(wd, "..", "testdata", rel)
}
