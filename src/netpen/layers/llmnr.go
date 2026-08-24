// LLMNR (Link-Local Multicast Name Resolution, RFC 4795) layer. Rides UDP
// on port 5355, multicast to 224.0.0.252 (IPv4) or ff02::1:3 (IPv6). The
// wire format is standard DNS (RFC 1035) with name compression.
//
// The decoder surfaces the DNS header fields (ID, flags, counts) and
// decoded questions/answers as typed fields. Name compression is handled
// with cycle detection: compression-pointer loops are detected via a
// visited-offset set and reported as a named structured error, never
// causing an infinite loop.

package layers

import (
	"encoding/binary"
	"fmt"

	"github.com/gopacket/gopacket"
)

// LLMNR is a Link-Local Multicast Name Resolution message.
type LLMNR struct {
	BaseLayer
	ID      uint16
	Flags   uint16
	QDCount uint16
	ANCount uint16
	NSCount uint16
	ARCount uint16

	Questions []LLMNRQuestion
	Answers   []LLMNRResourceRecord
}

// LLMNRQuestion is a decoded LLMNR question section entry.
type LLMNRQuestion struct {
	Name   string
	QType  uint16
	QClass uint16
}

// LLMNRResourceRecord is a decoded LLMNR answer/authority/additional entry.
type LLMNRResourceRecord struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32
	Data  []byte
}

// LayerType returns LayerTypeLLMNR.
func (l *LLMNR) LayerType() gopacket.LayerType { return LayerTypeLLMNR }

// CanDecode returns the set of layer types this DecodingLayer can decode.
func (l *LLMNR) CanDecode() gopacket.LayerClass { return LayerTypeLLMNR }

// NextLayerType returns gopacket.LayerTypeZero; LLMNR has no sub-layers.
func (l *LLMNR) NextLayerType() gopacket.LayerType { return gopacket.LayerTypeZero }

// QR returns true if the message is a response.
func (l *LLMNR) QR() bool { return l.Flags&0x8000 != 0 }

// DNS fixed-field offsets.
const (
	dnsHeaderLen = 12
)

// Limits for name decompression to prevent adversarial loops.
const (
	maxNameLabels  = 128
	maxNamePointer = 127
)

// DecodeFromBytes decodes the LLMNR payload (the bytes after the UDP
// header). Name compression cycles are detected and reported as a
// structured error naming the protocol and offset.
func (l *LLMNR) DecodeFromBytes(data []byte, df gopacket.DecodeFeedback) error {
	if len(data) < dnsHeaderLen {
		df.SetTruncated()
		return fmt.Errorf("LLMNR: truncated at offset 0, need >=%d bytes, got %d", dnsHeaderLen, len(data))
	}

	l.BaseLayer = BaseLayer{Contents: data, Payload: nil}
	l.ID = binary.BigEndian.Uint16(data[0:2])
	l.Flags = binary.BigEndian.Uint16(data[2:4])
	l.QDCount = binary.BigEndian.Uint16(data[4:6])
	l.ANCount = binary.BigEndian.Uint16(data[6:8])
	l.NSCount = binary.BigEndian.Uint16(data[8:10])
	l.ARCount = binary.BigEndian.Uint16(data[10:12])

	l.Questions = l.Questions[:0]
	l.Answers = l.Answers[:0]

	offset := dnsHeaderLen

	// Questions.
	for i := uint16(0); i < l.QDCount; i++ {
		name, next, err := decodeDNSName(data, offset)
		if err != nil {
			return fmt.Errorf("LLMNR: %w", err)
		}
		if next+4 > len(data) {
			df.SetTruncated()
			return fmt.Errorf("LLMNR: truncated question at offset %d, need 4 bytes, got %d", next, len(data)-next)
		}
		qtype := binary.BigEndian.Uint16(data[next : next+2])
		qclass := binary.BigEndian.Uint16(data[next+2 : next+4])
		l.Questions = append(l.Questions, LLMNRQuestion{
			Name:   name,
			QType:  qtype,
			QClass: qclass,
		})
		offset = next + 4
	}

	// Answers.
	for i := uint16(0); i < l.ANCount; i++ {
		name, next, err := decodeDNSName(data, offset)
		if err != nil {
			return fmt.Errorf("LLMNR: %w", err)
		}
		if next+10 > len(data) {
			df.SetTruncated()
			return fmt.Errorf("LLMNR: truncated answer at offset %d, need 10 bytes, got %d", next, len(data)-next)
		}
		rrType := binary.BigEndian.Uint16(data[next : next+2])
		rrClass := binary.BigEndian.Uint16(data[next+2 : next+4])
		ttl := binary.BigEndian.Uint32(data[next+4 : next+8])
		rdlen := int(binary.BigEndian.Uint16(data[next+8 : next+10]))
		if next+10+rdlen > len(data) {
			df.SetTruncated()
			return fmt.Errorf("LLMNR: truncated answer rdata at offset %d, need %d bytes, got %d",
				next+10, rdlen, len(data)-next-10)
		}
		// Aliased to the packet buffer; lifetime is the same as
		// BaseLayer.Contents which also references data.
		rdata := data[next+10 : next+10+rdlen]
		l.Answers = append(l.Answers, LLMNRResourceRecord{
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

// SerializeTo writes the LLMNR layer from the typed fields.
func (l *LLMNR) SerializeTo(b gopacket.SerializeBuffer, _ gopacket.SerializeOptions) error {
	// Pre-compute the body length.
	bodyLen := dnsHeaderLen
	for _, q := range l.Questions {
		bodyLen += encodeDNSNameLen(q.Name) + 4
	}
	for _, a := range l.Answers {
		bodyLen += encodeDNSNameLen(a.Name) + 10 + len(a.Data)
	}

	buf, err := b.PrependBytes(bodyLen)
	if err != nil {
		return err
	}

	binary.BigEndian.PutUint16(buf[0:2], l.ID)
	binary.BigEndian.PutUint16(buf[2:4], l.Flags)
	binary.BigEndian.PutUint16(buf[4:6], l.QDCount)
	binary.BigEndian.PutUint16(buf[6:8], l.ANCount)
	binary.BigEndian.PutUint16(buf[8:10], l.NSCount)
	binary.BigEndian.PutUint16(buf[10:12], l.ARCount)

	offset := dnsHeaderLen
	for _, q := range l.Questions {
		offset += encodeDNSName(buf[offset:], q.Name)
		binary.BigEndian.PutUint16(buf[offset:], q.QType)
		binary.BigEndian.PutUint16(buf[offset+2:], q.QClass)
		offset += 4
	}
	for _, a := range l.Answers {
		offset += encodeDNSName(buf[offset:], a.Name)
		binary.BigEndian.PutUint16(buf[offset:], a.Type)
		binary.BigEndian.PutUint16(buf[offset+2:], a.Class)
		binary.BigEndian.PutUint32(buf[offset+4:], a.TTL)
		binary.BigEndian.PutUint16(buf[offset+8:], uint16(len(a.Data)))
		copy(buf[offset+10:], a.Data)
		offset += 10 + len(a.Data)
	}

	return nil
}

func decodeLLMNR(data []byte, p gopacket.PacketBuilder) error {
	l := &LLMNR{}
	if err := l.DecodeFromBytes(data, p); err != nil {
		return err
	}
	p.AddLayer(l)
	return nil
}

// decodeDNSName decodes a DNS wire-format name at the given offset in
// data, following compression pointers. It detects pointer cycles using a
// visited-offset set and returns a structured error if a cycle or
// excessive pointer depth is found, preventing infinite loops.
func decodeDNSName(data []byte, offset int) (string, int, error) {
	var labels []byte
	var visited map[int]struct{} // allocated on first pointer follow
	index := offset
	hops := 0
	nextOffset := -1 // set when we hit a compression pointer

	for {
		if index >= len(data) {
			return "", 0, fmt.Errorf("name decompression: offset %d out of bounds (data length %d)", index, len(data))
		}

		b := data[index]
		if b == 0 {
			// Root label — end of name.
			end := index + 1
			if nextOffset >= 0 {
				end = nextOffset
			}
			return string(labels), end, nil
		}

		if b&0xC0 == 0xC0 {
			// Compression pointer.
			if index+2 > len(data) {
				return "", 0, fmt.Errorf("name decompression: truncated pointer at offset %d", index)
			}
			ptr := int(binary.BigEndian.Uint16(data[index:index+2]) & 0x3FFF)

			// The byte after the pointer is where the caller should
			// continue reading (only save the first pointer's end).
			if nextOffset < 0 {
				nextOffset = index + 2
			}

			// Cycle detection: allocate lazily on first pointer follow;
			// names without compression pointers are the common case.
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

		// Normal label.
		labelLen := int(b)
		if labelLen > 63 {
			return "", 0, fmt.Errorf("name decompression: label length %d exceeds 63 at offset %d", labelLen, index)
		}
		if index+1+labelLen > len(data) {
			return "", 0, fmt.Errorf("name decompression: truncated label at offset %d, need %d bytes, got %d",
				index, labelLen, len(data)-index-1)
		}

		if len(labels) > 0 {
			labels = append(labels, '.')
		}
		labels = append(labels, data[index+1:index+1+labelLen]...)
		index += 1 + labelLen

		if len(labels) > 255 {
			return "", 0, fmt.Errorf("name decompression: name length exceeds 255 at offset %d", offset)
		}
	}
}

// encodeDNSNameLen returns the wire length of an encoded DNS name (without
// compression pointers — the serializer writes literal labels).
func encodeDNSNameLen(name string) int {
	if name == "" || name == "." {
		return 1 // root label
	}
	n := 0
	for i := 0; i < len(name); {
		j := i
		for j < len(name) && name[j] != '.' {
			j++
		}
		n += 1 + (j - i) // length byte + label
		i = j + 1
	}
	return n + 1 // trailing root label
}

// encodeDNSName writes a DNS name as literal labels (no compression) into
// buf starting at offset 0. Returns the number of bytes written.
func encodeDNSName(buf []byte, name string) int {
	if name == "" || name == "." {
		buf[0] = 0
		return 1
	}
	off := 0
	for i := 0; i < len(name); {
		j := i
		for j < len(name) && name[j] != '.' {
			j++
		}
		labelLen := j - i
		buf[off] = byte(labelLen)
		copy(buf[off+1:], []byte(name[i:j]))
		off += 1 + labelLen
		i = j + 1
	}
	buf[off] = 0 // root label
	return off + 1
}
