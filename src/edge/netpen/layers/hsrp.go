// HSRP (Hot Standby Router Protocol, RFC 2281) layer. Rides UDP on port
// 1985, multicast to 224.0.0.2. The wire format is a 20-byte fixed header:
// version, opcode, state, hellotime, holdtime, priority, group, reserved,
// 8-byte auth string, and 4-byte virtual IP.
//
// The decoder surfaces the opcode, state, priority, group, auth, and
// virtual IP as typed fields a behavior can gate on — the baseline's
// hsrp_cmd crafts a coup (opcode=1) with priority=255 to steal the active
// router role.

package layers

import (
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
)

// HSRPOpcode is an HSRP message opcode.
type HSRPOpcode uint8

// HSRP opcodes per RFC 2281.
const (
	HSRPOpcodeHello  HSRPOpcode = 0
	HSRPOpcodeCoup   HSRPOpcode = 1
	HSRPOpcodeResign HSRPOpcode = 2
)

// HSRPState is an HSRP router state.
type HSRPState uint8

// HSRP states per RFC 2281.
const (
	HSRPStateInitial HSRPState = 0
	HSRPStateLearn   HSRPState = 1
	HSRPStateListen  HSRPState = 2
	HSRPStateSpeak   HSRPState = 4
	HSRPStateStandby HSRPState = 8
	HSRPStateActive  HSRPState = 16
)

// HSRP is a Hot Standby Router Protocol message.
type HSRP struct {
	BaseLayer
	Version   uint8
	Opcode    HSRPOpcode
	State     HSRPState
	Hellotime uint8
	Holdtime  uint8
	Priority  uint8
	Group     uint8
	Reserved  uint8
	Auth      [8]byte
	VirtualIP net.IP
}

// LayerType returns LayerTypeHSRP.
func (h *HSRP) LayerType() gopacket.LayerType { return LayerTypeHSRP }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (h *HSRP) CanDecode() gopacket.LayerClass { return LayerTypeHSRP }

// NextLayerType returns gopacket.LayerTypeZero; HSRP has no sub-layers.
func (h *HSRP) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// HSRP fixed-field offsets within the PDU (after UDP header).
const (
	hsrpMinLen  = 20
	hsrpAuthOff = 8
	hsrpVIPOff  = 16
)

// DecodeFromBytes decodes the HSRP payload (the bytes after the UDP header).
func (h *HSRP) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < hsrpMinLen {
		df.SetTruncated()
		return fmt.Errorf("HSRP: truncated at offset 0, need >=%d bytes, got %d", hsrpMinLen, len(data))
	}

	h.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	h.Version = data[0]
	h.Opcode = HSRPOpcode(data[1])
	h.State = HSRPState(data[2])
	h.Hellotime = data[3]
	h.Holdtime = data[4]
	h.Priority = data[5]
	h.Group = data[6]
	h.Reserved = data[7]
	copy(h.Auth[:], data[hsrpAuthOff:hsrpAuthOff+8])
	h.VirtualIP = net.IP(append([]byte(nil), data[hsrpVIPOff:hsrpVIPOff+4]...))

	return nil
}

// SerializeTo writes the HSRP layer from the typed fields.
func (h *HSRP) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	buf, err := b.PrependBytes(hsrpMinLen)
	if err != nil {
		return err
	}

	buf[0] = h.Version
	buf[1] = byte(h.Opcode)
	buf[2] = byte(h.State)
	buf[3] = h.Hellotime
	buf[4] = h.Holdtime
	buf[5] = h.Priority
	buf[6] = h.Group
	buf[7] = h.Reserved
	copy(buf[hsrpAuthOff:], h.Auth[:])
	vip := h.VirtualIP
	if len(vip) < 4 {
		vip = make(net.IP, 4)
	}
	copy(buf[hsrpVIPOff:], vip[:4])

	return nil
}

func decodeHSRP(data []byte, p gopacket.PacketBuilder) error {
	h := &HSRP{}
	if err := h.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(h)
	return nil
}
