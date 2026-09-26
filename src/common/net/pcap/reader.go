package pcap

import (
	"bytes"
	"encoding/binary"
	"io"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

const maxCapturedLength = 1 << 20

// Record is one captured packet. Data belongs to this record and is not
// changed by later calls to [Reader.Next]. HasFCS reports explicit capture
// metadata declaring an FCS; false does not prove that the bytes lack one.
type Record struct {
	At       time.Time
	Data     []byte
	OrigLen  uint32
	LinkType uint16
	HasFCS   bool
}

// Reader walks one classic pcap or pcapng file. It is not safe for concurrent use.
type Reader struct {
	r          io.Reader
	order      binary.ByteOrder
	ng         bool
	nanos      bool
	snapLen    uint32
	linkType   uint16
	hasFCS     bool
	interfaces []ngInterface
}

// NewReader accepts a classic pcap or pcapng stream and consumes its file
// header. It returns an error for a nil input, unknown magic, unsupported
// version, or truncated header. The caller retains ownership of input and must
// keep it open until done.
func NewReader(input io.Reader) (*Reader, error) {
	if input == nil {
		return nil, errs.Msg("pcap: nil input")
	}

	r := &Reader{r: input}
	var magic [4]byte
	if _, err := io.ReadFull(input, magic[:]); err != nil {
		return nil, errs.Wrap(structuredEOF(err), "pcap: file header")
	}
	if bytes.Equal(magic[:], []byte{0x0a, 0x0d, 0x0d, 0x0a}) {
		r.ng = true
		var length [4]byte
		if _, err := io.ReadFull(input, length[:]); err != nil {
			return nil, errs.Wrap(structuredEOF(err), "pcapng: section header")
		}
		if err := r.readSection(length); err != nil {
			return nil, err
		}
		return r, nil
	}
	if err := r.readClassicHeader(magic); err != nil {
		return nil, err
	}
	return r, nil
}

// Next returns the next packet. It returns [io.EOF] only when the file ends
// between records or blocks; truncated structured data returns another error.
func (r *Reader) Next() (Record, error) {
	if r.ng {
		return r.nextNG()
	}
	return r.nextClassic()
}

func structuredEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}
