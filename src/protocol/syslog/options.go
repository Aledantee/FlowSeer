package syslog

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Limits bounds a single receiver or parser. Zero fields select finite defaults.
// Negative values, overflow, and configurations without parser headroom are rejected.
type Limits struct {
	// MaxPayload bounds one syslog payload in bytes; zero selects 64 KiB.
	MaxPayload int
	// MaxListeners bounds bound listeners per receiver; zero selects 8.
	MaxListeners int
	// MaxConnections bounds concurrent stream connections; zero selects 64.
	MaxConnections int
	// MaxHandshakes bounds concurrent TLS handshakes; zero selects 8, and a
	// value above MaxConnections is clamped to it.
	MaxHandshakes int
	// MaxFrames bounds the receiver's queued frames; zero selects 256.
	MaxFrames int
	// MaxBytes bounds the receiver's total admitted bytes; zero selects
	// 32 MiB. It must leave headroom for one payload plus parser working
	// storage, or normalization rejects the configuration.
	MaxBytes int
	// MaxElements bounds structured-data elements per record; zero selects 64.
	MaxElements int
	// MaxParameters bounds structured-data parameters per element; zero
	// selects 256.
	MaxParameters int
	// MaxDiagnostics bounds diagnostics recorded per record; zero selects 16.
	MaxDiagnostics int
	// MetadataBytes bounds one record's header and metadata in bytes; zero
	// selects 32 KiB.
	MetadataBytes int
	// HandshakeTimeout bounds one TLS handshake; zero selects 5s.
	HandshakeTimeout time.Duration
	// FrameTimeout bounds reading one frame; zero selects 10s.
	FrameTimeout time.Duration
	// IdleTimeout closes a stream connection that sends nothing for this
	// long; zero selects 120s.
	IdleTimeout time.Duration
	// PressureTimeout bounds how long a frame waits for admission or queue
	// space before it is dropped or the connection is closed; zero selects 30s.
	PressureTimeout time.Duration
}

func (l Limits) normalized() (Limits, error) {
	ints := []struct {
		p   *int
		def int
	}{
		{&l.MaxPayload, 64 << 10},
		{&l.MaxListeners, 8},
		{&l.MaxConnections, 64},
		{&l.MaxHandshakes, 8},
		{&l.MaxFrames, 256},
		{&l.MaxBytes, 32 << 20},
		{&l.MaxElements, 64},
		{&l.MaxParameters, 256},
		{&l.MaxDiagnostics, 16},
		{&l.MetadataBytes, 32 << 10},
	}
	for _, v := range ints {
		if *v.p < 0 {
			return l, errs.Msg("negative syslog limit")
		}
		if *v.p == 0 {
			*v.p = v.def
		}
	}

	durations := []struct {
		p   *time.Duration
		def time.Duration
	}{
		{&l.HandshakeTimeout, 5 * time.Second},
		{&l.FrameTimeout, 10 * time.Second},
		{&l.IdleTimeout, 120 * time.Second},
		{&l.PressureTimeout, 30 * time.Second},
	}
	for _, v := range durations {
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
	// LegacyTimeSuffix interprets year/zone tokens after a legacy clock. Enable
	// only for a matching device configuration; otherwise ambiguous suffixes stay unparsed.
	LegacyTimeSuffix bool
	// CaptureRaw retains the verbatim payload on each record.
	CaptureRaw bool
	// Year supplies the calendar year for legacy timestamps that omit one.
	// Range 0..9999 inclusive; zero means no year context, so such
	// timestamps stay unresolved.
	Year int
	// Location interprets legacy timestamps that carry no zone. Nil means
	// no zone context.
	Location *time.Location
	// ZoneOffsets maps a zone abbreviation to its offset in seconds east of
	// UTC. At most 64 entries; each name is at most 32 bytes and each offset
	// lies strictly between -86400 and 86400. Nil means no abbreviation
	// context.
	ZoneOffsets map[string]int
	// NumericDateOrder resolves all-numeric dates: "mdy" or "dmy". Empty
	// means numeric dates stay unresolved.
	NumericDateOrder string
	// CiscoCounterOrder names the leading Cisco counter fields in wire
	// order. At most two entries, each either "sequence" or "counter", and
	// the two must differ. A record whose observed counter count differs
	// from this length is diagnosed "ambiguous_vendor_counters" and its
	// counters stay unassigned, which is what an empty value selects.
	CiscoCounterOrder []string
	// CiscoComponentCount is the number of facility components the device
	// puts between the module and the severity of a Cisco tag. Range 0..32
	// inclusive. A record carrying a different count is diagnosed
	// "ambiguous_vendor_components"; the components are still captured.
	CiscoComponentCount int
	// Limits bounds parsing; see [Limits] for the per-field defaults.
	Limits Limits
}
