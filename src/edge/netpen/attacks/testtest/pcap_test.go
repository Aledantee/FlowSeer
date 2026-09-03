package testtest

import (
	"bytes"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
)

func TestReadPackets(t *testing.T) {
	var capture bytes.Buffer
	writer := pcapgo.NewWriter(&capture)
	if err := writer.WriteFileHeader(65535, layers.LinkTypeEthernet); err != nil {
		t.Fatal(err)
	}
	header := bytes.Clone(capture.Bytes())
	for _, data := range [][]byte{{1, 2}, {3, 4}} {
		if err := writer.WritePacket(gopacket.CaptureInfo{CaptureLength: len(data), Length: len(data)}, data); err != nil {
			t.Fatal(err)
		}
	}
	valid := capture.Bytes()
	for _, tt := range []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{name: "complete", data: valid},
		{name: "truncated second packet", data: valid[:len(valid)-1], wantErr: true},
		{name: "truncated header", data: valid[:8], wantErr: true},
		{name: "empty capture", data: header, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			packets, err := readPackets(bytes.NewReader(tt.data))
			if (err != nil) != tt.wantErr {
				t.Fatalf("got error %v, want error=%t", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if len(packets) != 2 || !bytes.Equal(packets[0], []byte{1, 2}) || !bytes.Equal(packets[1], []byte{3, 4}) {
				t.Errorf("got packets %v, want [[1 2] [3 4]]", packets)
			}
		})
	}
}
