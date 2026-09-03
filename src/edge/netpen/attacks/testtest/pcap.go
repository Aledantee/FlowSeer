package testtest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/gopacket/gopacket/pcapgo"
)

// ReadPcap reads all packets from a pcap file and returns their raw bytes.
// The path is relative to the caller's working directory; behavior tests
// pass a path relative to the attacks/testdata tree.
// Empty or malformed captures fail the test, including a truncated packet
// after an otherwise valid prefix.
func ReadPcap(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	pkts, err := readPackets(f)
	if err != nil {
		t.Fatalf("read pcap %s: %v", path, err)
	}
	return pkts
}

func readPackets(reader io.Reader) ([][]byte, error) {
	r, err := pcapgo.NewReader(reader)
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	var pkts [][]byte
	for {
		data, _, err := r.ReadPacketData()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read packet %d: %w", len(pkts)+1, err)
		}
		pkts = append(pkts, data)
	}
	if len(pkts) == 0 {
		return nil, fmt.Errorf("capture has no packets")
	}
	return pkts, nil
}

// FixturePath resolves a fixture name relative to the attacks testdata root.
// Callers pass e.g. FixturePath(t, "l2/dtp.pcap").
func FixturePath(t *testing.T, rel string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
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
	return filepath.Join(wd, "..", "testdata", rel)
}
