package lag

import (
	"encoding/binary"
	"hash/fnv"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

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

	hdr, payload, err := ip.Decode(f.Payload)
	if err == nil {
		h.Write(hdr.Src.AsSlice())
		h.Write(hdr.Dst.AsSlice())
		h.Write([]byte{hdr.Protocol})
		if (hdr.Protocol == 6 || hdr.Protocol == 17) && len(payload) >= 4 {
			h.Write(payload[:4])
		}
	}

	return h.Sum32()
}
