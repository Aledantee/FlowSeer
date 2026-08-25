// NBT-NS (NetBIOS Name Service, RFC 1002) layer. Rides UDP on port 137,
// broadcast or directed to the subnet broadcast address. The wire format
// is DNS-like (RFC 1035) with name compression, but names are encoded as
// NetBIOS scope IDs (half-ASCII, padded to 16 bytes).
//
// The decoder surfaces the transaction ID, flags, and counts as typed
// fields. NetBIOS names are decoded from the half-ASCII encoding. Name
// compression is handled with the same cycle-detection as LLMNR: pointer
// loops are detected via a visited-offset set and reported as a named
// structured error, never causing an infinite loop.

package layers

import (
	"encoding/binary"
	"fmt"

	"github.com/gopacket/gopacket"
)

// NBNS is a NetBIOS Name Service message.
type NBNS struct {
	BaseLayer
	ID      uint16
	Flags   uint16
	QDCount uint16
	ANCount uint16
	NSCount uint16
	ARCount uint16

	Questions []NBNSQuestion
	Answers   []NBNSResourceRecord
}

// NBNSQuestion is a decoded NBT-NS question section entry.
type NBNSQuestion struct {
	Name   string
	QType  uint16
	QClass uint16
}

// NBNSResourceRecord is a decoded NBT-NS answer/authority/additional entry.
type NBNSResourceRecord struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32
	Data  []byte
}

// LayerType returns LayerTypeNBTNS.
func (n *NBNS) LayerType() gopacket.LayerType { return LayerTypeNBTNS }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (n *NBNS) CanDecode() gopacket.LayerClass { return LayerTypeNBTNS }

// NextLayerType returns gopacket.LayerTypeZero; NBT-NS has no sub-layers.
func (n *NBNS) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// DecodeFromBytes decodes the NBT-NS payload (the bytes after the UDP
// header). NetBIOS names are decoded from half-ASCII encoding. Name
// compression cycles are detected and reported as a structured error.
func (n *NBNS) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < dnsHeaderLen {
		df.SetTruncated()
		return fmt.Errorf("NBT-NS: truncated at offset 0, need >=%d bytes, got %d", dnsHeaderLen, len(data))
	}

	n.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	n.ID = binary.BigEndian.Uint16(data[0:2])
	n.Flags = binary.BigEndian.Uint16(data[2:4])
	n.QDCount = binary.BigEndian.Uint16(data[4:6])
	n.ANCount = binary.BigEndian.Uint16(data[6:8])
	n.NSCount = binary.BigEndian.Uint16(data[8:10])
	n.ARCount = binary.BigEndian.Uint16(data[10:12])

	n.Questions = n.Questions[:0]
	n.Answers = n.Answers[:0]

	offset := dnsHeaderLen

	for i := uint16(0); i < n.QDCount; i++ {
		name, next, err := decodeNetBIOSName(data, offset)
		if err != nil {
			return fmt.Errorf("NBT-NS: %w", err)
		}
		if next+4 > len(data) {
			df.SetTruncated()
			return fmt.Errorf("NBT-NS: truncated question at offset %d, need 4 bytes, got %d", next, len(data)-next)
		}
		qtype := binary.BigEndian.Uint16(data[next : next+2])
		qclass := binary.BigEndian.Uint16(data[next+2 : next+4])
		n.Questions = append(n.Questions, NBNSQuestion{
			Name:   name,
			QType:  qtype,
			QClass: qclass,
		})
		offset = next + 4
	}

	for i := uint16(0); i < n.ANCount; i++ {
		name, next, err := decodeNetBIOSName(data, offset)
		if err != nil {
			return fmt.Errorf("NBT-NS: %w", err)
		}
		if next+10 > len(data) {
			df.SetTruncated()
			return fmt.Errorf("NBT-NS: truncated answer at offset %d, need 10 bytes, got %d", next, len(data)-next)
		}
		rrType := binary.BigEndian.Uint16(data[next : next+2])
		rrClass := binary.BigEndian.Uint16(data[next+2 : next+4])
		ttl := binary.BigEndian.Uint32(data[next+4 : next+8])
		rdlen := int(binary.BigEndian.Uint16(data[next+8 : next+10]))
		if next+10+rdlen > len(data) {
			df.SetTruncated()
			return fmt.Errorf("NBT-NS: truncated answer rdata at offset %d, need %d bytes, got %d",
				next+10, rdlen, len(data)-next-10)
		}
		// Aliased to the packet buffer; lifetime is the same as
		// BaseLayer.Contents which also references data.
		rdata := data[next+10 : next+10+rdlen]
		n.Answers = append(n.Answers, NBNSResourceRecord{
			Name:  name,
			Type:  rrType,
			Class: rrClass,
			TTL:   ttl,
			Data:  rdata,
		})
		offset = next + 10 + rdlen
	}

	return nil
}

// SerializeTo writes the NBT-NS layer from the typed fields. NetBIOS names
// are encoded in the half-ASCII format (length byte + encoded name + root).
func (n *NBNS) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	bodyLen := dnsHeaderLen
	for range n.Questions {
		bodyLen += encodeNetBIOSNameLen() + 4
	}
	for _, a := range n.Answers {
		bodyLen += encodeNetBIOSNameLen() + 10 + len(a.Data)
	}

	buf, err := b.PrependBytes(bodyLen)
	if err != nil {
		return err
	}

	binary.BigEndian.PutUint16(buf[0:2], n.ID)
	binary.BigEndian.PutUint16(buf[2:4], n.Flags)
	binary.BigEndian.PutUint16(buf[4:6], n.QDCount)
	binary.BigEndian.PutUint16(buf[6:8], n.ANCount)
	binary.BigEndian.PutUint16(buf[8:10], n.NSCount)
	binary.BigEndian.PutUint16(buf[10:12], n.ARCount)

	offset := dnsHeaderLen
	for _, q := range n.Questions {
		offset += encodeNetBIOSName(buf[offset:], q.Name)
		binary.BigEndian.PutUint16(buf[offset:], q.QType)
		binary.BigEndian.PutUint16(buf[offset+2:], q.QClass)
		offset += 4
	}
	for _, a := range n.Answers {
		offset += encodeNetBIOSName(buf[offset:], a.Name)
		binary.BigEndian.PutUint16(buf[offset:], a.Type)
		binary.BigEndian.PutUint16(buf[offset+2:], a.Class)
		binary.BigEndian.PutUint32(buf[offset+4:], a.TTL)
		binary.BigEndian.PutUint16(buf[offset+8:], uint16(len(a.Data)))
		copy(buf[offset+10:], a.Data)
		offset += 10 + len(a.Data)
	}

	return nil
}

func decodeNBTNS(data []byte, p gopacket.PacketBuilder) error {
	n := &NBNS{}
	if err := n.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(n)
	return nil
}

// decodeNetBIOSName decodes a NetBIOS name from the wire format at the
// given offset. The name is encoded as a length byte, followed by
// half-ASCII encoded bytes (each byte split into two 4-bit nibbles mapped
// to 'A'+nibble), followed by an optional scope and a root label.
// Compression pointers are handled with cycle detection.
func decodeNetBIOSName(data []byte, offset int) (string, int, error) {
	var visited map[int]struct{} // allocated on first pointer follow
	index := offset
	hops := 0
	nextOffset := -1
	var result []byte
	firstLabel := true

	for {
		if index >= len(data) {
			return "", 0, fmt.Errorf("name decompression: offset %d out of bounds (data length %d)", index, len(data))
		}

		b := data[index]
		if b == 0 {
			end := index + 1
			if nextOffset >= 0 {
				end = nextOffset
			}
			return string(result), end, nil
		}

		if b&0xC0 == 0xC0 {
			if index+2 > len(data) {
				return "", 0, fmt.Errorf("name decompression: truncated pointer at offset %d", index)
			}
			ptr := int(binary.BigEndian.Uint16(data[index:index+2]) & 0x3FFF)

			if nextOffset < 0 {
				nextOffset = index + 2
			}

			if visited == nil {
				visited = make(map[int]struct{})
			}
			if _, seen := visited[index]; seen {
				return "", 0, fmt.Errorf("name decompression: compression pointer loop detected at offset %d (pointer to %d)", index, ptr)
			}
			visited[index] = struct{}{}

			hops++
			if hops > maxNamePointer {
				return "", 0, fmt.Errorf("name decompression: pointer depth %d exceeds limit %d at offset %d", hops, maxNamePointer, index)
			}

			if ptr >= len(data) {
				return "", 0, fmt.Errorf("name decompression: pointer offset %d out of bounds (data length %d)", ptr, len(data))
			}

			index = ptr
			continue
		}

		if b&0xC0 != 0 {
			return "", 0, fmt.Errorf("name decompression: invalid label type 0x%02x at offset %d", b, index)
		}

		labelLen := int(b)
		if labelLen > 63 {
			return "", 0, fmt.Errorf("name decompression: label length %d exceeds 63 at offset %d", labelLen, index)
		}
		if index+1+labelLen > len(data) {
			return "", 0, fmt.Errorf("name decompression: truncated label at offset %d, need %d bytes, got %d",
				index, labelLen, len(data)-index-1)
		}

		label := data[index+1 : index+1+labelLen]

		switch {
		case firstLabel && labelLen == 32:
			// NetBIOS half-ASCII encoded name (16 bytes → 32 bytes).
			decoded := make([]byte, 0, 16)
			for j := 0; j < 32; j += 2 {
				hi := label[j] - 'A'
				lo := label[j+1] - 'A'
				decoded = append(decoded, (hi<<4)|lo)
			}
			// Strip trailing space padding and null.
			for len(decoded) > 0 && (decoded[len(decoded)-1] == 0x20 || decoded[len(decoded)-1] == 0) {
				decoded = decoded[:len(decoded)-1]
			}
			result = decoded
			firstLabel = false
		case firstLabel:
			if len(result) > 0 {
				result = append(result, '.')
			}
			result = append(result, label...)
			firstLabel = false
		default:
			result = append(result, '.')
			result = append(result, label...)
		}

		index += 1 + labelLen
	}
}

// encodeNetBIOSNameLen returns the wire length of an encoded NetBIOS name.
// The encoding always pads to 16 bytes and half-ASCII encodes to 32 bytes,
// so the wire length is constant: length byte + 32 encoded bytes + root.
func encodeNetBIOSNameLen() int {
	return 1 + 32 + 1
}

// encodeNetBIOSName writes a NetBIOS name in half-ASCII format into buf.
// The name is padded to 15 bytes with spaces + a null byte (16 total),
// then each byte is split into two nibbles mapped to 'A'+nibble.
func encodeNetBIOSName(buf []byte, name string) int {
	// Pad to 16 bytes.
	var padded [16]byte
	for i := 0; i < 16; i++ {
		padded[i] = 0x20 // space
	}
	for i := 0; i < len(name) && i < 15; i++ {
		padded[i] = name[i]
	}
	padded[15] = 0

	// Encode: length byte (32) + 32 half-ASCII bytes + root label (0).
	buf[0] = 32
	for i := 0; i < 16; i++ {
		buf[1+i*2] = 'A' + (padded[i] >> 4)
		buf[2+i*2] = 'A' + (padded[i] & 0x0F)
	}
	buf[33] = 0 // root label
	return 34
}
