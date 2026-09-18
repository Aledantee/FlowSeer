package fabric

import (
	"bytes"
	"slices"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Record represents a captured frame or raw packet for replay in a simulated fabric.
type Record struct {
	At          time.Time
	Source      string
	Origin      Endpoint
	Frame       *ethernet.Frame
	Bytes       []byte
	CapturedLen int
	OriginalLen int
}

// Validate ensures the record's endpoints and payloads are well-formed.
// If raw Bytes are provided, they must be decodable as an Ethernet frame.
func (r Record) Validate() error {
	if r.Origin.Node == "" {
		return errs.Msg("record origin node cannot be empty")
	}
	if r.Frame == nil && len(r.Bytes) == 0 {
		return errs.Msg("record must specify either Frame or non-empty Bytes")
	}
	if len(r.Bytes) > 0 {
		if _, err := ethernet.Decode(r.Bytes); err != nil {
			return errs.Wrap(err, "decode record bytes")
		}
	}
	if r.CapturedLen < 0 {
		return errs.Msgf("record CapturedLen cannot be negative, got %d", r.CapturedLen)
	}
	if r.OriginalLen < 0 {
		return errs.Msgf("record OriginalLen cannot be negative, got %d", r.OriginalLen)
	}
	return nil
}

// Normalize ensures Frame is decoded from Bytes if omitted and default lengths are populated.
func (r Record) Normalize() (Record, error) {
	if err := r.Validate(); err != nil {
		return Record{}, err
	}
	cp := r.Clone()
	if cp.Frame == nil && len(cp.Bytes) > 0 {
		f, err := ethernet.Decode(cp.Bytes)
		if err != nil {
			return Record{}, errs.Wrap(err, "decode record bytes")
		}
		cp.Frame = &f
	}
	if cp.CapturedLen == 0 {
		if len(cp.Bytes) > 0 {
			cp.CapturedLen = len(cp.Bytes)
		} else if cp.Frame != nil {
			encoded, err := cp.Frame.Encode()
			if err == nil {
				cp.CapturedLen = len(encoded)
			}
		}
	}
	if cp.OriginalLen == 0 {
		cp.OriginalLen = cp.CapturedLen
	}
	return cp, nil
}

// Clone returns an independent deep copy of the Record.
// Bytes are cloned, while Frame payloads are shared per immutability conventions.
func (r Record) Clone() Record {
	cp := r
	if len(r.Bytes) > 0 {
		cp.Bytes = slices.Clone(r.Bytes)
	}
	return cp
}

type recordTimeFact string

func (f recordTimeFact) TypeID() string    { return "fabric.record.at" }
func (f recordTimeFact) Canonical() string { return string(f) }

type recordStringFact string

func (f recordStringFact) TypeID() string    { return "fabric.record.string" }
func (f recordStringFact) Canonical() string { return string(f) }

type recordIntFact int

func (f recordIntFact) TypeID() string    { return "fabric.record.int" }
func (f recordIntFact) Canonical() string { return strconv.Itoa(int(f)) }

type recordBytesFact string

func (f recordBytesFact) TypeID() string    { return "fabric.record.bytes" }
func (f recordBytesFact) Canonical() string { return string(f) }

// Diff returns the semantic differences between two records.
func (r Record) Diff(other Record) []trace.Change {
	var changes []trace.Change
	subject := trace.Subject{Kind: "scenario.record", Key: r.Source}
	if !r.At.Equal(other.At) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "at",
			From:    recordTimeFact(r.At.UTC().Format(time.RFC3339Nano)),
			To:      recordTimeFact(other.At.UTC().Format(time.RFC3339Nano)),
		})
	}
	if r.Source != other.Source {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "source",
			From:    recordStringFact(r.Source),
			To:      recordStringFact(other.Source),
		})
	}
	if r.Origin != other.Origin {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "origin",
			From:    r.Origin,
			To:      other.Origin,
		})
	}
	if !bytes.Equal(r.Bytes, other.Bytes) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "bytes",
			From:    recordBytesFact(string(r.Bytes)),
			To:      recordBytesFact(string(other.Bytes)),
		})
	}
	if r.CapturedLen != other.CapturedLen {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "captured_len",
			From:    recordIntFact(r.CapturedLen),
			To:      recordIntFact(other.CapturedLen),
		})
	}
	if r.OriginalLen != other.OriginalLen {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "original_len",
			From:    recordIntFact(r.OriginalLen),
			To:      recordIntFact(other.OriginalLen),
		})
	}
	encA, _ := r.encodedFrame()
	encB, _ := other.encodedFrame()
	if !bytes.Equal(encA, encB) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "frame",
			From:    recordBytesFact(string(encA)),
			To:      recordBytesFact(string(encB)),
		})
	}
	return changes
}

func (r Record) encodedFrame() ([]byte, error) {
	if r.Frame == nil {
		return nil, nil
	}
	return r.Frame.Encode()
}
