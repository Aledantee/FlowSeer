//go:build netpen_bench

package bench

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
)

func TestOwnedDecodersRegistered(t *testing.T) {
	cases := []struct {
		file     string
		typeName string
	}{
		{file: "dtp.pcap", typeName: "DTP"},
		{file: "vtp.pcap", typeName: "VTP"},
		{file: "lacp.pcap", typeName: "LACP"},
		{file: "eigrp.pcap", typeName: "EIGRP"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			packets, err := readPcapFile(filepath.Join("..", "layers", "testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if len(packets) == 0 {
				t.Fatal("fixture contains no packets")
			}
			packet := gopacket.NewPacket(packets[0], layers.LayerTypeEthernet, gopacket.Default)
			for _, layer := range packet.Layers() {
				typ := reflect.TypeOf(layer)
				if typ.Kind() == reflect.Pointer {
					typ = typ.Elem()
				}
				if typ.PkgPath() == "go.aledante.io/FlowSeer/src/edge/netpen/layers" && typ.Name() == tc.typeName {
					return
				}
			}
			t.Errorf("owned %s decoder absent from packet: %v", tc.typeName, packet.Layers())
		})
	}
}

func TestReadPcapFileRejectsTruncation(t *testing.T) {
	var capture bytes.Buffer
	w := pcapgo.NewWriter(&capture)
	if err := w.WriteFileHeader(65535, layers.LinkTypeEthernet); err != nil {
		t.Fatal(err)
	}
	payload := []byte{1, 2, 3, 4}
	if err := w.WritePacket(gopacket.CaptureInfo{CaptureLength: len(payload), Length: len(payload)}, payload); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{name: "complete", data: capture.Bytes()},
		{name: "truncated_packet", data: capture.Bytes()[:capture.Len()-1], wantErr: true},
		{name: "partial_record_after_packet", data: append(bytes.Clone(capture.Bytes()), 1), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "fixture.pcap")
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			packets, err := readPcapFile(path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("got error %v with %d packets, want error %t", err, len(packets), tc.wantErr)
			}
			if !tc.wantErr && (len(packets) != 1 || !bytes.Equal(packets[0], payload)) {
				t.Errorf("got packets %v, want [%v]", packets, payload)
			}
			if err := os.WriteFile(filepath.Join(dir, "valid.pcap"), capture.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadLayerFixtures(dir); (err != nil) != tc.wantErr {
				t.Errorf("load directory error = %v, want error %t", err, tc.wantErr)
			}
		})
	}
}
