//go:build netpen_bench

package bench

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
)

// decodeFixtures holds the raw frame bytes loaded from the layers
// testdata corpus. Loaded once in TestMain (or init) so benchmarks
// measure decode, not file I/O.
var decodeFixtures [][]byte

// TestMain loads the pcap fixtures from the layers testdata directory
// so the decode benchmark measures only the decode hot path.
func TestMain(m *testing.M) {
	fixtures, err := loadLayerFixtures()
	if err != nil {
		// If fixtures can't be loaded, benchmarks still compile
		// but will fail if run. This keeps `go vet` happy without
		// the build tag.
		os.Stderr.WriteString("bench: " + err.Error() + "\n")
		os.Exit(m.Run())
	}
	decodeFixtures = fixtures
	os.Exit(m.Run())
}

// loadLayerFixtures loads all pcap files from the layers/testdata
// directory. These are the characterization fixtures the decode
// hot path processes.
func loadLayerFixtures() ([][]byte, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	// From src/edge/netpen/bench/, the layers testdata is at ../layers/testdata.
	dir := filepath.Join(wd, "..", "layers", "testdata")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".pcap" {
			continue
		}
		pkts, err := readPcapFile(filepath.Join(dir, name))
		if err != nil {
			continue // skip unreadable pcaps
		}
		out = append(out, pkts...)
	}
	if len(out) == 0 {
		return nil, os.ErrNotExist
	}
	return out, nil
}

func readPcapFile(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r, err := pcapgo.NewReader(f)
	if err != nil {
		return nil, err
	}
	var pkts [][]byte
	for {
		data, _, err := r.ReadPacketData()
		if err != nil {
			break
		}
		pkts = append(pkts, data)
	}
	return pkts, nil
}

// BenchmarkDecodeHotPath measures the decode hot path: gopacket's full
// packet decoder over all layers testdata fixtures. This is the
// receive-side hot path — every frame the leg delivers is decoded
// through the owned layer decoders (DTP, VTP, MVRP, PAgP, LACP, EIGRP,
// HSRP, GLBP, LLMNR).
func BenchmarkDecodeHotPath(b *testing.B) {
	if len(decodeFixtures) == 0 {
		b.Skip("no decode fixtures loaded")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fix := decodeFixtures[i%len(decodeFixtures)]
		_ = gopacket.NewPacket(fix, layers.LayerTypeEthernet, gopacket.Default)
	}
}

// BenchmarkDecodeHotPathParallel measures the decode hot path under
// parallelism (the runner can decode frames from multiple behaviors
// concurrently).
func BenchmarkDecodeHotPathParallel(b *testing.B) {
	if len(decodeFixtures) == 0 {
		b.Skip("no decode fixtures loaded")
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			fix := decodeFixtures[i%len(decodeFixtures)]
			_ = gopacket.NewPacket(fix, layers.LayerTypeEthernet, gopacket.Default)
			i++
		}
	})
}

// BenchmarkFloodCraftPoolLoop measures the flood-craft hot path:
// serializing a burst of Ethernet frames through gopacket's
// SerializeBuffer pool. This is the send-side hot path — every frame a
// behavior crafts goes through SerializeLayers.
//
// The benchmark builds a minimal Ethernet + ARP frame (the simplest
// craft shape) to isolate the serialization cost from protocol-specific
// construction. Protocol-specific craft is benchmarked by the per-attack
// behavior tests; this measures the shared serialization pool.
func BenchmarkFloodCraftPoolLoop(b *testing.B) {
	eth := layers.Ethernet{
		SrcMAC:       []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		DstMAC:       []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          1, // Ethernet
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		SourceProtAddress: []byte{10, 0, 0, 1},
		DstHwAddress:      []byte{0, 0, 0, 0, 0, 0},
		DstProtAddress:    []byte{10, 0, 0, 2},
	}
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := gopacket.NewSerializeBuffer()
		_ = gopacket.SerializeLayers(buf, opts, &eth, &arp)
		_ = buf.Bytes()
	}
}

// BenchmarkFloodCraftReuseBuffer measures the flood-craft hot path with
// a reused SerializeBuffer (the pool loop a behavior actually runs: one
// buffer, many serializations).
func BenchmarkFloodCraftReuseBuffer(b *testing.B) {
	eth := layers.Ethernet{
		SrcMAC:       []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		DstMAC:       []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          1, // Ethernet
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		SourceProtAddress: []byte{10, 0, 0, 1},
		DstHwAddress:      []byte{0, 0, 0, 0, 0, 0},
		DstProtAddress:    []byte{10, 0, 0, 2},
	}
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	buf := gopacket.NewSerializeBuffer()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Clear()
		_ = gopacket.SerializeLayers(buf, opts, &eth, &arp)
		_ = buf.Bytes()
	}
}
