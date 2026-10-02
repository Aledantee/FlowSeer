package stream

import (
	"bytes"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/pcap"
)

type capturedFrame struct {
	at    time.Duration
	frame ethernet.Frame
}

type captureSource struct {
	frames []capturedFrame
	next   int
}

// NewCaptureSource validates and snapshots captured Ethernet records before
// returning a source. It maps the first timestamp to offset zero and preserves
// file order for equal timestamps. The source owns its frames; callers may
// change their records afterward. It rejects incomplete frames, declared FCS,
// non-Ethernet links, decreasing timestamps, and offsets beyond time.Duration.
// An empty input returns an exhausted source.
func NewCaptureSource(records []pcap.Record) (Source, error) {
	source := &captureSource{frames: make([]capturedFrame, 0, len(records))}
	if len(records) == 0 {
		return source, nil
	}
	start := records[0].At.UTC()
	previous := start
	for i, record := range records {
		if record.LinkType != 1 {
			return nil, errs.Msgf("capture record %d: link type %d is not Ethernet", i, record.LinkType)
		}
		if uint64(len(record.Data)) != uint64(record.OrigLen) {
			return nil, errs.Msgf("capture record %d: original length %d differs from captured length %d", i, record.OrigLen, len(record.Data))
		}
		if record.HasFCS {
			return nil, errs.Msgf("capture record %d: declared FCS cannot be replayed", i)
		}
		frame, err := ethernet.Decode(bytes.Clone(record.Data))
		if err != nil {
			return nil, errs.Wrapf(err, "capture record %d: Ethernet frame", i)
		}
		at := record.At.UTC()
		if at.Before(previous) {
			return nil, errs.Msgf("capture record %d: timestamp decreases", i)
		}
		offset := at.Sub(start)
		if !start.Add(offset).Equal(at) {
			return nil, errs.Msgf("capture record %d: offset exceeds time.Duration", i)
		}
		source.frames = append(source.frames, capturedFrame{at: offset, frame: frame})
		previous = at
	}
	return source, nil
}

func (s *captureSource) Next() (time.Duration, ethernet.Frame, bool) {
	if s.next >= len(s.frames) {
		return 0, ethernet.Frame{}, false
	}
	captured := s.frames[s.next]
	s.next++
	frame := captured.frame
	frame.Tags = slices.Clone(frame.Tags)
	frame.Payload = bytes.Clone(frame.Payload)
	return captured.at, frame, true
}

func (s *captureSource) Clone() Source {
	clone := *s
	return &clone
}
