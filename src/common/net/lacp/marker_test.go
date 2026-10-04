package lacp_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

var markerRequestFixture = []byte{
	0x01, 0x80, 0xc2, 0x00, 0x00, 0x02, 0xde, 0xad, 0xbe, 0xef, 0x00, 0x01, 0x88, 0x09, 0x02, 0x02,
	0x01, 0x10, 0x00, 0x12, 0x00, 0x04, 0x96, 0x1f, 0x50, 0x6a, 0x01, 0x02, 0x03, 0x04, 0xab, 0xcd,
	0x00, 0x00, 0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x8b, 0x8c, 0x8d,
	0x8e, 0x8f, 0x90, 0x91, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0x9b, 0x9c, 0x9d,
	0x9e, 0x9f, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad,
	0xae, 0xaf, 0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd,
	0xbe, 0xbf, 0xc0, 0xc1, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xcb, 0xcc, 0xcd,
	0xce, 0xcf, 0xd0, 0xd1, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9,
}

func TestMarkerResponsePreservesWire(t *testing.T) {
	request := decodeFixture(t, markerRequestFixture)
	request.Dst = netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x03}
	request.Tags = []vlan.Tag{{TPID: 0x8100, PCP: 7}}
	src := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}

	response, err := lacp.MarkerResponse(request, src)
	if err != nil {
		t.Fatalf("MarkerResponse() failed: %v", err)
	}

	wantPayload := bytes.Clone(request.Payload)
	wantPayload[2] = 0x02
	if !bytes.Equal(response.Payload, wantPayload) {
		t.Fatalf("response payload = %x, want %x", response.Payload, wantPayload)
	}
	if response.Dst != lacp.GroupAddress {
		t.Errorf("response destination = %s, want %s", response.Dst, lacp.GroupAddress)
	}
	if response.Src != src {
		t.Errorf("response source = %s, want %s", response.Src, src)
	}
	if response.EtherType != request.EtherType {
		t.Errorf("response EtherType = %s, want %s", response.EtherType, request.EtherType)
	}
	if !reflect.DeepEqual(response.Tags, []vlan.Tag{{TPID: 0x8100, PCP: 7}}) {
		t.Errorf("response tags = %+v, want %+v", response.Tags, []vlan.Tag{{TPID: 0x8100, PCP: 7}})
	}
	response.Tags[0].PCP = 0
	if !reflect.DeepEqual(request.Tags, []vlan.Tag{{TPID: 0x8100, PCP: 7}}) {
		t.Errorf("request tags changed through response = %+v, want %+v", request.Tags, []vlan.Tag{{TPID: 0x8100, PCP: 7}})
	}

	response.Payload[20] ^= 0xff
	if response.Payload[20] == request.Payload[20] {
		t.Error("response payload aliases request payload")
	}
}

func TestMarkerResponseRefusals(t *testing.T) {
	validFrame := decodeFixture(t, markerRequestFixture)

	tests := []struct {
		name   string
		modify func(f ethernet.Frame) ethernet.Frame
	}{
		{
			name: "EtherType 0x0800",
			modify: func(f ethernet.Frame) ethernet.Frame {
				f.EtherType = ethernet.EtherTypeIPv4
				return f
			},
		},
		{
			name: "109-octet payload",
			modify: func(f ethernet.Frame) ethernet.Frame {
				f.Payload = f.Payload[:109]
				return f
			},
		},
		{
			name: "empty payload",
			modify: func(f ethernet.Frame) ethernet.Frame {
				f.Payload = nil
				return f
			},
		},
		{
			name: "subtype 0x01",
			modify: func(f ethernet.Frame) ethernet.Frame {
				f.Payload = bytes.Clone(f.Payload)
				f.Payload[0] = lacp.SubtypeLACP
				return f
			},
		},
		{
			name: "Marker TLV type 0x02",
			modify: func(f ethernet.Frame) ethernet.Frame {
				f.Payload = bytes.Clone(f.Payload)
				f.Payload[2] = 0x02
				return f
			},
		},
		{
			name: "Marker TLV type 0x03",
			modify: func(f ethernet.Frame) ethernet.Frame {
				f.Payload = bytes.Clone(f.Payload)
				f.Payload[2] = 0x03
				return f
			},
		},
		{
			name: "Marker TLV length 15",
			modify: func(f ethernet.Frame) ethernet.Frame {
				f.Payload = bytes.Clone(f.Payload)
				f.Payload[3] = 15
				return f
			},
		},
		{
			name: "Marker TLV length 17",
			modify: func(f ethernet.Frame) ethernet.Frame {
				f.Payload = bytes.Clone(f.Payload)
				f.Payload[3] = 17
				return f
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := lacp.MarkerResponse(tc.modify(validFrame), netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02})
			if err == nil {
				t.Fatal("MarkerResponse() succeeded, want error wrapping ErrUnsupported")
			}
			if !errors.Is(err, lacp.ErrUnsupported) {
				t.Errorf("MarkerResponse() error = %v, want errors.Is(err, ErrUnsupported)", err)
			}
		})
	}
}

func TestMarkerResponseRetainsFinalOctet(t *testing.T) {
	t.Parallel()

	request := decodeFixture(t, markerRequestFixture)
	request.Payload = append(bytes.Clone(request.Payload), 0xef)
	if len(request.Payload) != 111 {
		t.Fatalf("request payload length = %d, want 111", len(request.Payload))
	}
	src := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}

	response, err := lacp.MarkerResponse(request, src)
	if err != nil {
		t.Fatalf("MarkerResponse() failed: %v", err)
	}
	if len(response.Payload) != 111 {
		t.Fatalf("response payload len = %d, want 111", len(response.Payload))
	}
	if response.Payload[110] != 0xef {
		t.Fatalf("response payload final octet = 0x%02x, want 0xef", response.Payload[110])
	}
}
