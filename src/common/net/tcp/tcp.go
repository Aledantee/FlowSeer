// Package tcp decodes Transmission Control Protocol segments (RFC 9293).
// It decodes only: nothing in this tree originates a TCP segment.
package tcp

import (
	"encoding/binary"
	"errors"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Flags is the set of TCP control bits, all eight of which share the
// segment's fourteenth octet (RFC 9293 section 3.1).
type Flags uint16

const (
	// FIN indicates no more data from the sender.
	FIN Flags = 1 << 0
	// SYN synchronizes sequence numbers.
	SYN Flags = 1 << 1
	// RST resets the connection.
	RST Flags = 1 << 2
	// PSH pushes buffered data to the receiving application.
	PSH Flags = 1 << 3
	// ACK indicates the acknowledgment field is significant.
	ACK Flags = 1 << 4
	// URG indicates the urgent pointer field is significant.
	URG Flags = 1 << 5
	// ECE indicates ECN-Echo (RFC 3168).
	ECE Flags = 1 << 6
	// CWR indicates Congestion Window Reduced (RFC 3168).
	CWR Flags = 1 << 7
)

// Has reports whether every bit set in want is also set in f.
func (f Flags) Has(want Flags) bool {
	return f&want == want
}

// Header is a decoded TCP header (RFC 9293 section 3.1). DataOffset is the
// header length in 32-bit words, as transmitted.
type Header struct {
	SrcPort    uint16
	DstPort    uint16
	Seq        uint32
	Ack        uint32
	DataOffset uint8
	Flags      Flags
	Window     uint16
}

// ErrMalformed identifies a segment too short to hold a header, a data
// offset below the minimum header size, or a data offset past the buffer.
var ErrMalformed = errors.New("malformed TCP segment")

// Decode parses a TCP header from b and returns it with the payload that
// follows the header, as delimited by the header's data offset. It returns
// [ErrMalformed] for a segment shorter than 20 octets, a data offset under
// 5, or a data offset past the end of b. Decode does not verify the
// checksum.
func Decode(b []byte) (Header, []byte, error) {
	if len(b) < 20 {
		return Header{}, nil, errs.From(ErrMalformed).
			Attr("length", len(b)).
			Attr("min", 20).
			Msg("TCP segment is shorter than the fixed header")
	}

	dataOffset := b[12] >> 4
	if dataOffset < 5 {
		return Header{}, nil, errs.From(ErrMalformed).
			Attr("field", "data_offset").
			Attr("value", dataOffset).
			Attr("min", 5).
			Msg("TCP data offset is below the minimum header size")
	}

	offset := int(dataOffset) * 4
	if offset > len(b) {
		return Header{}, nil, errs.From(ErrMalformed).
			Attr("field", "data_offset").
			Attr("offset", offset).
			Attr("length", len(b)).
			Msg("TCP data offset extends past the buffer")
	}

	h := Header{
		SrcPort:    binary.BigEndian.Uint16(b[0:2]),
		DstPort:    binary.BigEndian.Uint16(b[2:4]),
		Seq:        binary.BigEndian.Uint32(b[4:8]),
		Ack:        binary.BigEndian.Uint32(b[8:12]),
		DataOffset: dataOffset,
		Flags:      Flags(b[13]),
		Window:     binary.BigEndian.Uint16(b[14:16]),
	}
	return h, b[offset:], nil
}
