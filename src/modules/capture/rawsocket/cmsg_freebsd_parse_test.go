package rawsocket

import (
	"net/netip"
	"testing"
)

// The byte slices below are little-endian, the order of both supported
// FreeBSD ports, so the tests assume a little-endian host.

func TestParseRecvDstAddr(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    netip.Addr
		wantErr bool
	}{
		{name: "in_addr", data: []byte{192, 0, 2, 7}, want: netip.MustParseAddr("192.0.2.7")},
		{name: "extra bytes ignored", data: []byte{10, 1, 2, 3, 0xff, 0xff}, want: netip.MustParseAddr("10.1.2.3")},
		{name: "short", data: []byte{192, 0, 2}, wantErr: true},
		{name: "empty", data: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRecvDstAddr(tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseRecvIf(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    uint16
		wantErr bool
	}{
		// sdl_len, sdl_family, then sdl_index 0x0102 little-endian.
		{name: "index at offset 2", data: []byte{20, 18, 0x02, 0x01, 6, 0, 0, 0}, want: 0x0102},
		{name: "truncated after index", data: []byte{20, 18, 0x05, 0x00}, want: 5},
		{name: "ends inside index", data: []byte{20, 18, 0x05}, wantErr: true},
		{name: "empty", data: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRecvIf(tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseIn6Pktinfo(t *testing.T) {
	full := make([]byte, 20)
	copy(full, []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01})
	// ipi6_ifindex 0x01020304 little-endian at offset 16.
	copy(full[16:], []byte{0x04, 0x03, 0x02, 0x01})

	tests := []struct {
		name      string
		data      []byte
		wantAddr  netip.Addr
		wantIndex uint32
		wantErr   bool
	}{
		{name: "pktinfo", data: full, wantAddr: netip.MustParseAddr("2001:db8::1"), wantIndex: 0x01020304},
		{name: "one byte short", data: full[:19], wantErr: true},
		{name: "address only", data: full[:16], wantErr: true},
		{name: "empty", data: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, idx, err := parseIn6Pktinfo(tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if addr != tt.wantAddr || idx != tt.wantIndex {
				t.Errorf("got (%v, %#x), want (%v, %#x)", addr, idx, tt.wantAddr, tt.wantIndex)
			}
		})
	}
}
