package pcap

import (
	"encoding/binary"
	"io"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func (r *Reader) readClassicHeader(magic [4]byte) error {
	switch magic {
	case [4]byte{0xd4, 0xc3, 0xb2, 0xa1}:
		r.order = binary.LittleEndian
	case [4]byte{0xa1, 0xb2, 0xc3, 0xd4}:
		r.order = binary.BigEndian
	case [4]byte{0x4d, 0x3c, 0xb2, 0xa1}:
		r.order, r.nanos = binary.LittleEndian, true
	case [4]byte{0xa1, 0xb2, 0x3c, 0x4d}:
		r.order, r.nanos = binary.BigEndian, true
	default:
		return errs.Msgf("pcap: unknown magic % x", magic)
	}

	var header [20]byte
	if _, err := io.ReadFull(r.r, header[:]); err != nil {
		return errs.Wrap(structuredEOF(err), "pcap: file header")
	}
	if major, minor := r.order.Uint16(header[:2]), r.order.Uint16(header[2:4]); major != 2 || minor != 4 {
		return errs.Msgf("pcap: unsupported version %d.%d", major, minor)
	}
	r.snapLen = r.order.Uint32(header[12:16])
	linkWord := r.order.Uint32(header[16:20])
	r.linkType = uint16(linkWord)
	r.hasFCS = linkWord&(1<<26) != 0 && linkWord>>28 != 0
	return nil
}

func (r *Reader) nextClassic() (Record, error) {
	var header [16]byte
	if _, err := io.ReadFull(r.r, header[:]); err != nil {
		if err == io.EOF {
			return Record{}, io.EOF
		}
		return Record{}, errs.Wrap(err, "pcap: record header")
	}
	seconds := r.order.Uint32(header[:4])
	fraction := r.order.Uint32(header[4:8])
	unit := uint32(1_000_000)
	toNanos := int64(1_000)
	if r.nanos {
		unit, toNanos = 1_000_000_000, 1
	}
	if fraction >= unit {
		return Record{}, errs.Msgf("pcap: timestamp fraction %d is outside one second", fraction)
	}
	captured := r.order.Uint32(header[8:12])
	if err := checkCapturedLength(captured, r.snapLen); err != nil {
		return Record{}, err
	}
	data := make([]byte, captured)
	if _, err := io.ReadFull(r.r, data); err != nil {
		return Record{}, errs.Wrap(structuredEOF(err), "pcap: packet data")
	}
	return Record{
		At:       time.Unix(int64(seconds), int64(fraction)*toNanos).UTC(),
		Data:     data,
		OrigLen:  r.order.Uint32(header[12:16]),
		LinkType: r.linkType,
		HasFCS:   r.hasFCS,
	}, nil
}

func checkCapturedLength(captured, snapLen uint32) error {
	if captured > maxCapturedLength {
		return errs.Msgf("pcap: captured length %d exceeds %d", captured, maxCapturedLength)
	}
	if snapLen != 0 && captured > snapLen {
		return errs.Msgf("pcap: captured length %d exceeds snapshot length %d", captured, snapLen)
	}
	return nil
}
