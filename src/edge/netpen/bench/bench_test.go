//go:build netpen_bench

package bench

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"

	// Register the owned decoders in gopacket's dispatch tables.
	_ "go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

// decodeFixtures holds the raw frame bytes loaded from the layers
// testdata corpus. Loaded once in TestMain so benchmarks
// measure decode, not file I/O.
var decodeFixtures [][]byte

// TestMain loads the pcap fixtures from the layers testdata directory
// so the decode benchmark measures only the decode hot path.
func TestMain(m *testing.M) {
	fixtures, err := loadLayerFixtures(filepath.Join("..", "layers", "testdata"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
	decodeFixtures = fixtures
	os.Exit(m.Run())
}

// loadLayerFixtures loads all pcap files from the layers/testdata
// directory. These are the characterization fixtures the decode
// hot path processes.
func loadLayerFixtures(dir string) ([][]byte, error) {
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
			return nil, fmt.Errorf("read fixture %s: %w", name, err)
		}
		out = append(out, pkts...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no packets in fixture directory %s", dir)
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
	if r.LinkType() != layers.LinkTypeEthernet {
		return nil, fmt.Errorf("capture link type is %v, want Ethernet", r.LinkType())
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
	return pkts, nil
}

// BenchmarkDecodeHotPath measures the decode hot path: gopacket's full
// packet decoder over all layers testdata fixtures. This is the
// receive-side hot path — every frame the leg delivers is decoded
// through the owned layer decoders (DTP, VTP, MVRP, PAgP, LACP, EIGRP,
// HSRP, GLBP, LLMNR).
func BenchmarkDecodeHotPath(b *testing.B) {
	if len(decodeFixtures) == 0 {
		b.Fatal("no decode fixtures loaded")
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
		b.Fatal("no decode fixtures loaded")
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

// BenchmarkFloodCraftPoolLoop measures Ethernet/ARP serialization with a fresh
// buffer for each frame, including buffer allocation. It does not send traffic.
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
		if err := gopacket.SerializeLayers(buf, opts, &eth, &arp); err != nil {
			b.Fatal(err)
		}
		_ = buf.Bytes()
	}
}

// BenchmarkFloodCraftReuseBuffer measures steady-state Ethernet/ARP
// serialization with one warmed buffer. It does not send traffic.
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
	if err := gopacket.SerializeLayers(buf, opts, &eth, &arp); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := gopacket.SerializeLayers(buf, opts, &eth, &arp); err != nil {
			b.Fatal(err)
		}
		_ = buf.Bytes()
	}
}
