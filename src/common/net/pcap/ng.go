package pcap

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
	"time"
)

type ngInterface struct {
	linkType uint16
	snapLen  uint32
	tsresol  byte
	tsoffset int64
	hasFCS   bool
}

func (r *Reader) readSection(length [4]byte) error {
	var magic [4]byte
	if _, err := io.ReadFull(r.r, magic[:]); err != nil {
		return fmt.Errorf("pcapng: section byte order: %w", structuredEOF(err))
	}
	switch magic {
	case [4]byte{0x4d, 0x3c, 0x2b, 0x1a}:
		r.order = binary.LittleEndian
	case [4]byte{0x1a, 0x2b, 0x3c, 0x4d}:
		r.order = binary.BigEndian
	default:
		return fmt.Errorf("pcapng: invalid section byte-order magic % x", magic)
	}
	total := r.order.Uint32(length[:])
	if total < 28 || total%4 != 0 {
		return fmt.Errorf("pcapng: invalid section length %d", total)
	}
	body := &io.LimitedReader{R: r.r, N: int64(total) - 16}
	var fixed [12]byte
	if _, err := io.ReadFull(body, fixed[:]); err != nil {
		return fmt.Errorf("pcapng: section fields: %w", structuredEOF(err))
	}
	if major, minor := r.order.Uint16(fixed[:2]), r.order.Uint16(fixed[2:4]); major != 1 || minor != 0 {
		return fmt.Errorf("pcapng: unsupported version %d.%d", major, minor)
	}
	if err := readOptions(body, r.order, nil); err != nil {
		return fmt.Errorf("pcapng: section options: %w", err)
	}
	if err := r.readTrailer(total); err != nil {
		return err
	}
	r.interfaces = nil
	return nil
}

func (r *Reader) nextNG() (Record, error) {
	for {
		var header [8]byte
		if _, err := io.ReadFull(r.r, header[:]); err != nil {
			if err == io.EOF {
				return Record{}, io.EOF
			}
			return Record{}, fmt.Errorf("pcapng: block header: %w", err)
		}
		if header[0] == 0x0a && header[1] == 0x0d && header[2] == 0x0d && header[3] == 0x0a {
			var length [4]byte
			copy(length[:], header[4:])
			if err := r.readSection(length); err != nil {
				return Record{}, err
			}
			continue
		}

		kind := r.order.Uint32(header[:4])
		total := r.order.Uint32(header[4:8])
		if total < 12 || total%4 != 0 {
			return Record{}, fmt.Errorf("pcapng: invalid block length %d", total)
		}
		body := &io.LimitedReader{R: r.r, N: int64(total) - 12}
		var rec Record
		var err error
		switch kind {
		case 1:
			err = r.readIDB(body)
		case 6:
			rec, err = r.readEPB(body)
		case 2, 3:
			return Record{}, fmt.Errorf("pcapng: packet block type %d is unsupported", kind)
		default:
			_, err = io.CopyN(io.Discard, body, body.N)
		}
		if err != nil {
			return Record{}, fmt.Errorf("pcapng: block type %d: %w", kind, structuredEOF(err))
		}
		if body.N != 0 {
			return Record{}, fmt.Errorf("pcapng: block type %d has %d unparsed bytes", kind, body.N)
		}
		if err := r.readTrailer(total); err != nil {
			return Record{}, err
		}
		if kind == 6 {
			return rec, nil
		}
	}
}

func (r *Reader) readTrailer(total uint32) error {
	var trailer [4]byte
	if _, err := io.ReadFull(r.r, trailer[:]); err != nil {
		return fmt.Errorf("pcapng: block trailer: %w", structuredEOF(err))
	}
	if got := r.order.Uint32(trailer[:]); got != total {
		return fmt.Errorf("pcapng: block trailer length %d differs from header %d", got, total)
	}
	return nil
}

func (r *Reader) readIDB(body *io.LimitedReader) error {
	var fixed [8]byte
	if _, err := io.ReadFull(body, fixed[:]); err != nil {
		return fmt.Errorf("interface fields: %w", structuredEOF(err))
	}
	iface := ngInterface{
		linkType: r.order.Uint16(fixed[:2]),
		snapLen:  r.order.Uint32(fixed[4:8]),
		tsresol:  6,
	}
	if err := readOptions(body, r.order, func(code uint16, value []byte) error {
		switch code {
		case 9:
			if len(value) != 1 {
				return fmt.Errorf("if_tsresol length %d, want 1", len(value))
			}
			iface.tsresol = value[0]
		case 13:
			if len(value) != 1 {
				return fmt.Errorf("if_fcslen length %d, want 1", len(value))
			}
			iface.hasFCS = value[0] != 0
		case 14:
			if len(value) != 8 {
				return fmt.Errorf("if_tsoffset length %d, want 8", len(value))
			}
			iface.tsoffset = int64(r.order.Uint64(value))
		}
		return nil
	}); err != nil {
		return fmt.Errorf("interface options: %w", err)
	}
	r.interfaces = append(r.interfaces, iface)
	return nil
}

func (r *Reader) readEPB(body *io.LimitedReader) (Record, error) {
	var fixed [20]byte
	if _, err := io.ReadFull(body, fixed[:]); err != nil {
		return Record{}, fmt.Errorf("packet fields: %w", structuredEOF(err))
	}
	id := r.order.Uint32(fixed[:4])
	if uint64(id) >= uint64(len(r.interfaces)) {
		return Record{}, fmt.Errorf("invalid interface ID %d", id)
	}
	iface := r.interfaces[id]
	captured := r.order.Uint32(fixed[12:16])
	if err := checkCapturedLength(captured, iface.snapLen); err != nil {
		return Record{}, err
	}
	padding := (4 - captured%4) % 4
	if body.N < int64(captured)+int64(padding) {
		return Record{}, io.ErrUnexpectedEOF
	}
	data := make([]byte, captured)
	if _, err := io.ReadFull(body, data); err != nil {
		return Record{}, fmt.Errorf("packet data: %w", structuredEOF(err))
	}
	var pad [3]byte
	if _, err := io.ReadFull(body, pad[:padding]); err != nil {
		return Record{}, fmt.Errorf("packet padding: %w", structuredEOF(err))
	}
	for _, b := range pad[:padding] {
		if b != 0 {
			return Record{}, fmt.Errorf("nonzero packet padding")
		}
	}
	hasFCS := iface.hasFCS
	if err := readOptions(body, r.order, func(code uint16, value []byte) error {
		if code != 2 {
			return nil
		}
		if len(value) != 4 {
			return fmt.Errorf("epb_flags length %d, want 4", len(value))
		}
		hasFCS = hasFCS || r.order.Uint32(value)&(0xf<<5) != 0
		return nil
	}); err != nil {
		return Record{}, fmt.Errorf("packet options: %w", err)
	}
	ticks := uint64(r.order.Uint32(fixed[4:8]))<<32 | uint64(r.order.Uint32(fixed[8:12]))
	at, err := ngTimestamp(ticks, iface.tsresol, iface.tsoffset)
	if err != nil {
		return Record{}, err
	}
	return Record{At: at, Data: data, OrigLen: r.order.Uint32(fixed[16:20]), LinkType: iface.linkType, HasFCS: hasFCS}, nil
}

func readOptions(body *io.LimitedReader, order binary.ByteOrder, onOption func(uint16, []byte) error) error {
	for body.N > 0 {
		var header [4]byte
		if _, err := io.ReadFull(body, header[:]); err != nil {
			return structuredEOF(err)
		}
		code, length := order.Uint16(header[:2]), order.Uint16(header[2:4])
		if code == 0 {
			if length != 0 || body.N != 0 {
				return fmt.Errorf("invalid end-of-options marker")
			}
			return nil
		}
		padding := (4 - int(length)%4) % 4
		if body.N < int64(length)+int64(padding) {
			return io.ErrUnexpectedEOF
		}
		value := make([]byte, length)
		if _, err := io.ReadFull(body, value); err != nil {
			return structuredEOF(err)
		}
		var pad [3]byte
		if _, err := io.ReadFull(body, pad[:padding]); err != nil {
			return structuredEOF(err)
		}
		for _, b := range pad[:padding] {
			if b != 0 {
				return fmt.Errorf("nonzero option padding")
			}
		}
		if onOption != nil {
			if err := onOption(code, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func ngTimestamp(ticks uint64, resolution byte, offset int64) (time.Time, error) {
	var seconds, nanos uint64
	exponent := resolution & 0x7f
	switch {
	case resolution&0x80 == 0:
		powers := [...]uint64{
			1, 10, 100, 1_000, 10_000, 100_000, 1_000_000, 10_000_000, 100_000_000, 1_000_000_000,
			10_000_000_000, 100_000_000_000, 1_000_000_000_000, 10_000_000_000_000, 100_000_000_000_000,
			1_000_000_000_000_000, 10_000_000_000_000_000, 100_000_000_000_000_000,
			1_000_000_000_000_000_000, 10_000_000_000_000_000_000,
		}
		switch {
		case exponent <= 9:
			divisor := powers[exponent]
			seconds = ticks / divisor
			nanos = ticks % divisor * (1_000_000_000 / divisor)
		case exponent <= 28:
			totalNanos := ticks / powers[exponent-9]
			seconds, nanos = totalNanos/1_000_000_000, totalNanos%1_000_000_000
		}
	case exponent < 64:
		divisor := uint64(1) << exponent
		seconds = ticks / divisor
		hi, lo := bits.Mul64(ticks%divisor, 1_000_000_000)
		nanos, _ = bits.Div64(hi, lo, divisor)
	case exponent < 94:
		hi, _ := bits.Mul64(ticks, 1_000_000_000)
		nanos = hi >> (exponent - 64)
	}

	const maxSeconds = int64(^uint64(0) >> 1)
	if seconds > uint64(maxSeconds) || offset > 0 && int64(seconds) > maxSeconds-offset {
		return time.Time{}, fmt.Errorf("pcapng: timestamp overflows time.Time")
	}
	unixSeconds := int64(seconds) + offset
	at := time.Unix(unixSeconds, int64(nanos)).UTC()
	if at.Unix() != unixSeconds || at.Nanosecond() != int(nanos) {
		return time.Time{}, fmt.Errorf("pcapng: timestamp overflows time.Time")
	}
	return at, nil
}
