package bpfdev

import (
	"encoding/binary"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeBuffer is the wire identity for a read buffer the parser refuses.
var ErrCodeBuffer = errs.NewCode("bpfdev/buffer")

// Record is one captured packet. Data aliases the buffer passed to the parser
// and is valid only until that buffer is reused.
type Record struct {
	Data []byte
	// CapturedLength is the number of bytes the kernel kept (bh_caplen).
	CapturedLength int
	// WireLength is the packet's length on the wire (bh_datalen).
	WireLength int
	Timestamp  time.Time
}

// hdrLayout describes where the fields of the kernel's bpf_hdr sit.
type hdrLayout struct {
	secOff, usecOff, caplenOff, datalenOff, hdrlenOff int
	// wordSize is the BPF_ALIGNMENT that rounds each record's offset.
	wordSize int
}

// hdrLayout64 is the bpf_hdr of a 64-bit FreeBSD kernel: an 8-byte seconds
// field, 8-byte microseconds, 4-byte caplen and datalen, and a 2-byte hdrlen.
var hdrLayout64 = hdrLayout{
	secOff:     hdr64SecOff,
	usecOff:    hdr64UsecOff,
	caplenOff:  hdr64CaplenOff,
	datalenOff: hdr64DatalenOff,
	hdrlenOff:  hdr64HdrlenOff,
	wordSize:   hdr64WordSize,
}

// The offsets of hdrLayout64 are constants so the FreeBSD-only layout file
// can check them against unix.BpfHdr at compile time.
const (
	hdr64SecOff     = 0
	hdr64UsecOff    = 8
	hdr64CaplenOff  = 16
	hdr64DatalenOff = 20
	hdr64HdrlenOff  = 24
	hdr64WordSize   = 8
)

// minHdrLen is the smallest bh_hdrlen the parser accepts: the end of the
// hdrlen field.
func (l hdrLayout) minHdrLen() int { return l.hdrlenOff + 2 }

func (l hdrLayout) wordAlign(n int) int {
	return (n + l.wordSize - 1) &^ (l.wordSize - 1)
}

// parseBuffer splits a buffer returned by read(2) on a BPF device into
// records. Header fields are read little-endian: both GOARCHes FreeBSD
// supports here, amd64 and arm64, are little-endian, which the build
// constraint of the FreeBSD-only files pins. An empty buffer yields
// no records and no error. A truncated record, or a bh_hdrlen too short to
// hold the header, fails with [ErrCodeBuffer] and no records.
func parseBuffer(buf []byte, layout hdrLayout) ([]Record, error) {
	var recs []Record
	minHdr := layout.minHdrLen()
	for off := 0; off < len(buf); {
		if len(buf)-off < minHdr {
			return nil, bufferError(off, len(buf)).Msg("buffer ends inside a record header")
		}
		hdr := buf[off:]
		hdrlen := int(binary.LittleEndian.Uint16(hdr[layout.hdrlenOff:]))
		if hdrlen < minHdr {
			return nil, bufferError(off, len(buf)).Attr("hdrlen", hdrlen).Msg("header length is shorter than the header")
		}
		caplen := int(binary.LittleEndian.Uint32(hdr[layout.caplenOff:]))
		if caplen > len(buf)-off-hdrlen {
			return nil, bufferError(off, len(buf)).Attr("hdrlen", hdrlen).Attr("caplen", caplen).Msg("captured length runs past the buffer")
		}
		sec := int64(binary.LittleEndian.Uint64(hdr[layout.secOff:]))
		usec := int64(binary.LittleEndian.Uint64(hdr[layout.usecOff:]))
		recs = append(recs, Record{
			Data:           buf[off+hdrlen : off+hdrlen+caplen],
			CapturedLength: caplen,
			WireLength:     int(binary.LittleEndian.Uint32(hdr[layout.datalenOff:])),
			Timestamp:      time.Unix(sec, usec*1000),
		})
		off += layout.wordAlign(hdrlen + caplen)
	}
	return recs, nil
}

func bufferError(off, length int) errs.Builder {
	return errs.New().Code(ErrCodeBuffer).Attr("offset", off).Attr("length", length)
}
