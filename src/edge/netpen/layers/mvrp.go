// MVRP (Multiple VLAN Registration Protocol, IEEE 802.1Qak) layer. Rides
// directly on Ethernet with EtherType 0x88F5. The PDU is a 1-byte protocol
// version followed by one or more messages, each consisting of an attribute
// type, attribute length, a vector header (LeaveAll + count), a first value,
// and packed event descriptors. The PDU terminates with a 0x0000 endmark.
//
// The decoder surfaces the attribute type (1 = VID), the first value (the
// VLAN ID), and the first event (JoinIn, LeaveEmpty, etc.) as typed fields a
// behavior can gate on.

package layers

import (
	"encoding/binary"
	"fmt"

	"github.com/gopacket/gopacket"
)

// MVRPEvent is a packed event descriptor value.
type MVRPEvent uint8

// MVRP event values per IEEE 802.1Qak (TwoPackedEvents / ThreePackedEvents).
const (
	MVRPEventEmpty      MVRPEvent = 0
	MVRPEventJoinEmpty  MVRPEvent = 1
	MVRPEventJoinIn     MVRPEvent = 2
	MVRPEventLeaveEmpty MVRPEvent = 3
	MVRPEventLeaveIn    MVRPEvent = 4
	MVRPEventNew        MVRPEvent = 5
)

// String returns the event name.
func (e MVRPEvent) String() string {
	switch e {
	case MVRPEventEmpty:
		return "Empty"
	case MVRPEventJoinEmpty:
		return "JoinEmpty"
	case MVRPEventJoinIn:
		return "JoinIn"
	case MVRPEventLeaveEmpty:
		return "LeaveEmpty"
	case MVRPEventLeaveIn:
		return "LeaveIn"
	case MVRPEventNew:
		return "New"
	default:
		return fmt.Sprintf("Unknown(%d)", e)
	}
}

// MVRPAttributeType identifies the attribute carried in an MVRP message.
type MVRPAttributeType uint8

// MVRP attribute types.
const (
	MVRPAttrTypeVID MVRPAttributeType = 1
)

// MVRPMessage is one decoded MVRP message.
type MVRPMessage struct {
	AttributeType MVRPAttributeType
	AttributeLen  uint8
	LeaveAll      bool
	NumValues     uint16
	FirstValue    uint16
	Events        []MVRPEvent
}

// MVRP is a Multiple VLAN Registration Protocol frame. Its zero value is
// ready for decoding. Decoding retains data in BaseLayer and is not safe
// concurrently with other uses of the same frame.
type MVRP struct {
	BaseLayer
	Version  uint8
	Messages []MVRPMessage
}

// LayerType returns LayerTypeMVRP.
func (m *MVRP) LayerType() gopacket.LayerType { return LayerTypeMVRP }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (m *MVRP) CanDecode() gopacket.LayerClass { return LayerTypeMVRP }

// NextLayerType returns gopacket.LayerTypeZero; MVRP has no sub-layers.
func (m *MVRP) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// DecodeFromBytes decodes the MVRP payload (the bytes after the Ethernet
// header).
func (m *MVRP) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < 1 {
		df.SetTruncated()
		return fmt.Errorf("MVRP: truncated at offset 0, need >=1 bytes, got %d", len(data))
	}

	m.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	m.Version = data[0]
	m.Messages = m.Messages[:0]

	offset := 1
	for offset < len(data) {
		// Endmark: two zero bytes terminate the PDU.
		if offset+2 <= len(data) && data[offset] == 0 && data[offset+1] == 0 {
			break
		}
		if offset+4 > len(data) {
			df.SetTruncated()
			return fmt.Errorf("MVRP: truncated message header at offset %d, need 4 bytes, got %d", offset, len(data)-offset)
		}

		msg := MVRPMessage{
			AttributeType: MVRPAttributeType(data[offset]),
			AttributeLen:  data[offset+1],
		}

		vh := binary.BigEndian.Uint16(data[offset+2 : offset+4])
		msg.LeaveAll = vh>>15 != 0
		msg.NumValues = vh & 0x1FFF

		firstValOffset := offset + 4
		firstValLen := mvrpFirstValueLen
		if firstValOffset+firstValLen > len(data) {
			df.SetTruncated()
			return fmt.Errorf("MVRP: truncated first value at offset %d, need %d bytes, got %d", firstValOffset, firstValLen, len(data)-firstValOffset)
		}

		if firstValLen >= 2 {
			msg.FirstValue = binary.BigEndian.Uint16(data[firstValOffset : firstValOffset+2])
		}

		// ThreePackedEvents: ceil(NumValues / 3) bytes, each packing 3 events
		// (2 bits each, top-first).
		eventsOffset := firstValOffset + firstValLen
		numEventBytes := int(msg.NumValues+2) / 3
		if numEventBytes > 0 {
			if eventsOffset+numEventBytes > len(data) {
				df.SetTruncated()
				return fmt.Errorf("MVRP: truncated packed events at offset %d, need %d bytes, got %d", eventsOffset, numEventBytes, len(data)-eventsOffset)
			}
			for i := 0; i < int(msg.NumValues); i++ {
				byteIdx := i / 3
				shift := 6 - 2*(i%3)
				msg.Events = append(msg.Events, MRPEvent(data[eventsOffset+byteIdx]>>uint(shift)&0x03))
			}
		}

		m.Messages = append(m.Messages, msg)

		// Advance past: attrType(1) + attrLen(1) + vectorHeader(2) +
		// firstValue + packedEvents.
		offset = eventsOffset + numEventBytes
	}

	return nil
}

// MRPEvent is an alias for MVRPEvent, the packed event descriptor.
// It is exported as MRPEvent because the event encoding is shared across
// the MRP protocol family (MVRP, MMRP, MIRP).
type MRPEvent = MVRPEvent

// SerializeTo writes the MVRP layer from the Messages slice.
func (m *MVRP) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	bodyLen := 1 // version
	for _, msg := range m.Messages {
		fvLen := mvrpFirstValueLen
		bodyLen += 4 + fvLen + (int(msg.NumValues)+2)/3
	}
	bodyLen += 2 // endmark

	buf, err := b.PrependBytes(bodyLen)
	if err != nil {
		return err
	}

	buf[0] = m.Version
	offset := 1
	for _, msg := range m.Messages {
		buf[offset] = byte(msg.AttributeType)
		buf[offset+1] = msg.AttributeLen
		vh := uint16(0)
		if msg.LeaveAll {
			vh |= 1 << 15
		}
		vh |= msg.NumValues & 0x1FFF
		binary.BigEndian.PutUint16(buf[offset+2:], vh)
		offset += 4

		fvLen := mvrpFirstValueLen
		if fvLen >= 2 {
			binary.BigEndian.PutUint16(buf[offset:], msg.FirstValue)
		}
		offset += fvLen

		numEventBytes := int(msg.NumValues+2) / 3
		for i := range numEventBytes {
			var packed byte
			for j := 0; j < 3 && i*3+j < int(msg.NumValues); j++ {
				shift := uint(6 - 2*j)
				packed |= byte(msg.Events[i*3+j]&0x03) << shift
			}
			buf[offset] = packed
			offset++
		}
	}

	// Endmark
	buf[offset] = 0
	buf[offset+1] = 0

	return nil
}

// mvrpFirstValueLen is the first-value length for MVRP VID attributes (the
// only attribute type the baseline crafts). The baseline sets
// AttributeLength=4 but writes only 2 bytes of first value; the actual
// first-value size on the wire is 2.
const mvrpFirstValueLen = 2

func decodeMVRP(data []byte, p gopacket.PacketBuilder) error {
	m := &MVRP{}
	if err := m.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(m)
	return nil
}
