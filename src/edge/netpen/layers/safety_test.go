package layers_test

import (
	"bytes"
	"fmt"
	"net"
	"testing"

	"github.com/gopacket/gopacket"

	"go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

func TestMVRPTruncatedVectorHeader(t *testing.T) {
	data := []byte{0, 1, 4, 0}
	var layer layers.MVRP
	var feedback decodeFeedback
	if err := layer.DecodeFromBytes(data, &feedback); err == nil {
		t.Fatal("DecodeFromBytes returned nil for a partial vector header")
	}
	if !feedback.truncated {
		t.Error("DecodeFromBytes did not mark the packet truncated")
	}
}

func TestVTPShortVLANRecord(t *testing.T) {
	for length := 0; length < 12; length++ {
		t.Run(fmt.Sprintf("length_%d", length), func(t *testing.T) {
			data := make([]byte, 40+max(4, length))
			data[0] = 1
			data[1] = byte(layers.VTPCodeSubset)
			data[40] = byte(length)
			var layer layers.VTP
			if err := layer.DecodeFromBytes(data, gopacket.NilDecodeFeedback); err == nil {
				t.Fatal("DecodeFromBytes returned nil for a record shorter than its fixed fields")
			}
		})
	}
}

func TestLACPSerializeReusedBuffer(t *testing.T) {
	layer := layers.LACP{Subtype: 1, Version: 1}
	buf := gopacket.NewSerializeBuffer()
	if err := layer.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(buf.Bytes())
	for i := range buf.Bytes() {
		buf.Bytes()[i] = 0xa5
	}
	if err := buf.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := layer.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("reused buffer: got %x, want %x", buf.Bytes(), want)
	}
}

func TestHSRPSerializeIPv4Representations(t *testing.T) {
	for _, tc := range []struct {
		name string
		ip   net.IP
	}{
		{name: "four_bytes", ip: net.IP{192, 0, 2, 1}},
		{name: "parsed", ip: net.ParseIP("192.0.2.1")},
		{name: "ipv4_constructor", ip: net.IPv4(192, 0, 2, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layer := layers.HSRP{VirtualIP: tc.ip}
			buf := gopacket.NewSerializeBuffer()
			if err := layer.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatal(err)
			}
			var decoded layers.HSRP
			if err := decoded.DecodeFromBytes(buf.Bytes(), gopacket.NilDecodeFeedback); err != nil {
				t.Fatal(err)
			}
			if !decoded.VirtualIP.Equal(tc.ip) {
				t.Errorf("virtual IP: got %s, want %s", decoded.VirtualIP, tc.ip)
			}
		})
	}
}

func TestHSRPSerializeInvalidVirtualIP(t *testing.T) {
	for _, tc := range []struct {
		name string
		ip   net.IP
	}{
		{name: "ipv6", ip: net.ParseIP("2001:db8::1")},
		{name: "short", ip: net.IP{192, 0, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layer := layers.HSRP{VirtualIP: tc.ip}
			buf := gopacket.NewSerializeBuffer()
			if err := layer.SerializeTo(buf, gopacket.SerializeOptions{}); err == nil {
				t.Fatal("SerializeTo returned nil for an invalid IPv4 address")
			}
			if len(buf.Bytes()) != 0 {
				t.Error("SerializeTo changed the buffer before rejecting the address")
			}
		})
	}
}

func TestHSRPSerializeNilVirtualIP(t *testing.T) {
	layer := layers.HSRP{VirtualIP: net.IPv4(192, 0, 2, 1)}
	buf := gopacket.NewSerializeBuffer()
	if err := layer.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := buf.Clear(); err != nil {
		t.Fatal(err)
	}
	layer.VirtualIP = nil
	if err := layer.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
		t.Fatal(err)
	}
	var decoded layers.HSRP
	if err := decoded.DecodeFromBytes(buf.Bytes(), gopacket.NilDecodeFeedback); err != nil {
		t.Fatal(err)
	}
	if !decoded.VirtualIP.Equal(net.IPv4zero) {
		t.Errorf("virtual IP: got %s, want %s", decoded.VirtualIP, net.IPv4zero)
	}
}

type decodeFeedback struct {
	truncated bool
}

func (d *decodeFeedback) SetTruncated() { d.truncated = true }
