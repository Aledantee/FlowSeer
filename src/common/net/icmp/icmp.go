// Package icmp decodes the common ICMPv4 and ICMPv6 message header. The
// first four octets have the same layout in both protocols (RFC 792, RFC
// 4443 section 2.1), so one decoder serves both. It decodes only: nothing
// in this tree originates an ICMP message.
package icmp

import (
	"encoding/binary"
	"errors"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Header is the four-octet header common to ICMPv4 and ICMPv6 messages.
// Checksum is the transmitted value as read; Decode does not verify it.
type Header struct {
	Type     uint8
	Code     uint8
	Checksum uint16
}

// ErrMalformed identifies a message too short to hold the common header.
var ErrMalformed = errors.New("malformed ICMP message")

// Decode parses the common header from b and returns it with the payload
// that follows. It returns [ErrMalformed] for a message shorter than 4
// octets. Decode does not verify the checksum.
func Decode(b []byte) (Header, []byte, error) {
	if len(b) < 4 {
		return Header{}, nil, errs.From(ErrMalformed).
			Attr("length", len(b)).
			Attr("min", 4).
			Msg("ICMP message is shorter than the common header")
	}

	h := Header{
		Type:     b[0],
		Code:     b[1],
		Checksum: binary.BigEndian.Uint16(b[2:4]),
	}
	return h, b[4:], nil
}
