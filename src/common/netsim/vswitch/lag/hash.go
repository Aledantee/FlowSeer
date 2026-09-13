package lag

import (
	"encoding/binary"
	"hash/fnv"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

type tcpHashInput struct {
	ipDecoded    bool
	ipSrc        netip.Addr
	ipDst        netip.Addr
	ipProtocol   uint8
	transport4   [4]byte
	hasTransport bool
}

func hashSLB(basis uint32, src netaddr.MAC, vid vlan.ID) uint32 {
	h := fnv.New32a()

	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], basis)
	h.Write(buf[:])
	h.Write(src[:])

	binary.BigEndian.PutUint16(buf[:2], uint16(vid))
	h.Write(buf[:2])

	return h.Sum32()
}

func hashTCP(basis uint32, f ethernet.Frame) uint32 {
	h := fnv.New32a()

	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], basis)
	h.Write(buf[:])
	h.Write(f.Src[:])
	h.Write(f.Dst[:])

	binary.BigEndian.PutUint16(buf[:2], uint16(f.EtherType))
	h.Write(buf[:2])

	input := inspectTCPHashInput(f)
	if input.ipDecoded {
		h.Write(input.ipSrc.AsSlice())
		h.Write(input.ipDst.AsSlice())
		h.Write([]byte{input.ipProtocol})
		if input.hasTransport {
			h.Write(input.transport4[:])
		}
	}

	return h.Sum32()
}

func inspectTCPHashInput(f ethernet.Frame) tcpHashInput {
	hdr, payload, err := ip.Decode(f.Payload)
	if err != nil {
		return tcpHashInput{}
	}

	input := tcpHashInput{
		ipDecoded:  true,
		ipSrc:      hdr.Src,
		ipDst:      hdr.Dst,
		ipProtocol: hdr.Protocol,
	}
	if (hdr.Protocol == 6 || hdr.Protocol == 17) && len(payload) >= len(input.transport4) {
		copy(input.transport4[:], payload[:len(input.transport4)])
		input.hasTransport = true
	}

	return input
}
