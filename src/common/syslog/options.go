package syslog

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Limits bounds a single receiver or parser. Zero fields select finite defaults.
// Negative values, overflow, and configurations without parser headroom are rejected.
type Limits struct {
	MaxPayload       int
	MaxListeners     int
	MaxConnections   int
	MaxHandshakes    int
	MaxFrames        int
	MaxBytes         int
	MaxElements      int
	MaxParameters    int
	MaxDiagnostics   int
	MetadataBytes    int
	HandshakeTimeout time.Duration
	FrameTimeout     time.Duration
	IdleTimeout      time.Duration
	PressureTimeout  time.Duration
}

func (l Limits) normalized() (Limits, error) {
	ints := []struct {
		p   *int
		def int
	}{
		{&l.MaxPayload, 64 << 10}, {&l.MaxListeners, 8}, {&l.MaxConnections, 64}, {&l.MaxHandshakes, 8}, {&l.MaxFrames, 256}, {&l.MaxBytes, 32 << 20}, {&l.MaxElements, 64}, {&l.MaxParameters, 256}, {&l.MaxDiagnostics, 16}, {&l.MetadataBytes, 32 << 10},
	}
	for _, v := range ints {
		if *v.p < 0 {
			return l, errs.Msg("negative syslog limit")
		}
		if *v.p == 0 {
			*v.p = v.def
		}
	}
	for _, v := range []struct {
		p   *time.Duration
		def time.Duration
	}{{&l.HandshakeTimeout, 5 * time.Second}, {&l.FrameTimeout, 10 * time.Second}, {&l.IdleTimeout, 120 * time.Second}, {&l.PressureTimeout, 30 * time.Second}} {
		if *v.p < 0 {
			return l, errs.Msg("negative syslog timeout")
		}
		if *v.p == 0 {
			*v.p = v.def
		}
	}
	// Arithmetic uses small factors below. Reject before computing any allocation size.
	maxInt := int(^uint(0) >> 1)
	for _, v := range ints {
		if v.p != &l.MaxBytes && *v.p > maxInt/1024 {
			return l, ErrLimit
		}
	}
	if l.headroom()+l.MaxPayload > l.MaxBytes {
		return l, errs.Wrap(ErrLimit, "syslog budget lacks parser headroom")
	}
	if l.MaxHandshakes > l.MaxConnections {
		l.MaxHandshakes = l.MaxConnections
	}
	return l, nil
}

func (l Limits) headroom() int {
	return 3*l.MaxPayload + 3*l.MetadataBytes + 32*l.MaxDiagnostics + 8192
}

// ParseOptions is immutable after NewParser. Raw capture is disabled by default.
// Year, Location, and zone offsets are explicit interpretation context.
type ParseOptions struct {
	CaptureRaw          bool
	Year                int
	Location            *time.Location
	ZoneOffsets         map[string]int
	NumericDateOrder    string
	CiscoCounterOrder   []string
	CiscoComponentCount int
	Limits              Limits
}
