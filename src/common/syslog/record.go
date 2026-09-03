package syslog

import (
	"bytes"
	"net/netip"
	"strings"
	"time"
)

// Presence distinguishes missing evidence, wire NILVALUE, and a real value.
type Presence uint8

const (
	// Absent means no field was identified.
	Absent Presence = iota
	// NilValue means an explicit RFC 5424 dash.
	NilValue
	// Present includes valid numeric zero and empty text.
	Present
)

// Field preserves wire text, including numeric identifier leading zeros.
// Its zero value is absent. Values are immutable; containing records may be copied.
type Field struct {
	Value    string   `json:"value,omitempty"`
	Presence Presence `json:"presence"`
}

// Text constructs a present field, including for empty text.
func Text(value string) Field { return Field{Value: value, Presence: Present} }

// Format classifies the envelope, independently of vendor body syntax.
type Format string

const (
	// Unknown identifies an unrecognized envelope.
	Unknown Format = "unknown"
	// RFC3164 identifies a legacy envelope, including diagnosed extensions.
	RFC3164 Format = "rfc3164"
	// RFC5424 identifies a versioned structured envelope.
	RFC5424 Format = "rfc5424"
)

// ParseStatus describes interpretation completeness, not transport delivery.
type ParseStatus string

const (
	// Complete means all recognized envelope fields parsed without ambiguity.
	Complete ParseStatus = "complete"
	// Partial means some evidence could not be interpreted.
	Partial ParseStatus = "partial"
	// Unrecognized means the payload has no recognized envelope.
	Unrecognized ParseStatus = "unknown"
)

// Transport is an explicit socket protocol. Its zero value is invalid.
type Transport string

const (
	// UDP carries one payload per datagram.
	UDP Transport = "udp"
	// TCP carries framed messages over an unencrypted stream.
	TCP Transport = "tcp"
	// TLS carries framed messages over a verified TLS connection.
	TLS Transport = "tls"
)

// Observation describes reception, never the device's asserted identity.
// The zero value is appropriate for standalone parsing without receiver metadata.
type Observation struct {
	ReceivedAt    time.Time      `json:"received_at,omitempty"`
	Peer          netip.AddrPort `json:"peer,omitempty"`
	Local         netip.AddrPort `json:"local,omitempty"`
	Transport     Transport      `json:"transport,omitempty"`
	Authenticated bool           `json:"authenticated,omitempty"`
}

// TimeParts identifies calendar components present or supplied by parse context.
type TimeParts uint16

const (
	// YearPart indicates a calendar year.
	YearPart TimeParts = 1 << iota
	// DatePart indicates month and day.
	DatePart
	// ClockPart indicates hour, minute, and second.
	ClockPart
	// OffsetPart indicates a resolved UTC offset.
	OffsetPart
)

// DeviceTime preserves the original clock evidence even when Instant is absent.
// Inferred components were supplied by the caller, never by the receiver clock.
type DeviceTime struct {
	Original      string     `json:"original,omitempty"`
	Instant       *time.Time `json:"instant,omitempty"`
	Year          int        `json:"year,omitempty"`
	Month         time.Month `json:"month,omitempty"`
	Day           int        `json:"day,omitempty"`
	Hour          int        `json:"hour,omitempty"`
	Minute        int        `json:"minute,omitempty"`
	Second        int        `json:"second,omitempty"`
	Nanosecond    int        `json:"nanosecond,omitempty"`
	OffsetSeconds int        `json:"offset_seconds,omitempty"`
	Zone          string     `json:"zone,omitempty"`
	Present       TimeParts  `json:"present,omitempty"`
	Inferred      TimeParts  `json:"inferred,omitempty"`
	ClockMarker   string     `json:"clock_marker,omitempty"`
	Uptime        string     `json:"uptime,omitempty"`
}

// Parameter preserves a structured-data value as bytes, without UTF-8 replacement.
type Parameter struct {
	Name  string `json:"name"`
	Value []byte `json:"value"`
}

// Element preserves parameter order and duplicates. Duplicate names are diagnosed.
type Element struct {
	ID         string      `json:"id"`
	Parameters []Parameter `json:"parameters,omitempty"`
}

// Vendor contains prefix evidence; Family is a grammar hint, not device identity.
type Vendor struct {
	Family     string   `json:"family,omitempty"`
	Module     Field    `json:"module"`
	Mnemonic   Field    `json:"mnemonic"`
	EventID    Field    `json:"event_id"`
	Severity   Field    `json:"severity"`
	Sequence   Field    `json:"sequence"`
	Counter    Field    `json:"counter"`
	Thread     Field    `json:"thread"`
	Source     Field    `json:"source"`
	Slot       Field    `json:"slot"`
	Flags      Field    `json:"flags"`
	Components []string `json:"components,omitempty"`
}

// Diagnostic is a finite code and byte offset into the original payload.
// It never copies the full input into an operational error.
type Diagnostic struct {
	Code   string `json:"code"`
	Offset int    `json:"offset"`
}

// Record is the common parsed representation. Returned records own their data.
// A value copy shares slices; use Clone before independently mutating a copy.
// Concurrent reads are safe, but callers must synchronize mutation.
type Record struct {
	Observation           Observation  `json:"observation"`
	Format                Format       `json:"format"`
	Status                ParseStatus  `json:"status"`
	Priority              Field        `json:"priority"`
	Version               Field        `json:"version"`
	Hostname              Field        `json:"hostname"`
	Application           Field        `json:"application"`
	ProcessID             Field        `json:"process_id"`
	MessageID             Field        `json:"message_id"`
	Tag                   Field        `json:"tag"`
	DeviceTime            DeviceTime   `json:"device_time"`
	StructuredData        []Element    `json:"structured_data,omitempty"`
	OriginalSD            []byte       `json:"original_sd,omitempty"`
	Content               []byte       `json:"content"`
	Unparsed              []byte       `json:"unparsed,omitempty"`
	Vendor                Vendor       `json:"vendor"`
	Diagnostics           []Diagnostic `json:"diagnostics,omitempty"`
	SuppressedDiagnostics uint64       `json:"suppressed_diagnostics,omitempty"`
	Raw                   *[]byte      `json:"_raw,omitempty"`
}

// Clone makes an independent copy after checking limits. It does not mutate r.
// The source must remain stable during the call. Zero limits select defaults.
func (r Record) Clone(limits Limits) (Record, error) {
	l, err := limits.normalized()
	if err != nil {
		return Record{}, err
	}
	if err := r.checkSize(l); err != nil {
		return Record{}, err
	}
	out := r
	out.Content = bytes.Clone(r.Content)
	out.OriginalSD = bytes.Clone(r.OriginalSD)
	out.Unparsed = bytes.Clone(r.Unparsed)
	if r.Raw != nil {
		raw := append([]byte{}, (*r.Raw)...)
		out.Raw = &raw
	}
	if r.DeviceTime.Instant != nil {
		instant := *r.DeviceTime.Instant
		out.DeviceTime.Instant = &instant
	}
	out.Diagnostics = append([]Diagnostic(nil), r.Diagnostics...)
	out.Vendor.Components = append([]string(nil), r.Vendor.Components...)
	out.StructuredData = make([]Element, len(r.StructuredData))
	for i, e := range r.StructuredData {
		out.StructuredData[i] = Element{ID: e.ID, Parameters: make([]Parameter, len(e.Parameters))}
		for j, p := range e.Parameters {
			out.StructuredData[i].Parameters[j] = Parameter{Name: p.Name, Value: bytes.Clone(p.Value)}
		}
	}
	return out, nil
}

func (r *Record) diagnose(code string, offset int, l Limits) {
	if r.Status != Unrecognized {
		r.Status = Partial
	}
	if len(r.Diagnostics) < l.MaxDiagnostics {
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Code: code, Offset: offset})
	} else if r.SuppressedDiagnostics < ^uint64(0) {
		r.SuppressedDiagnostics++
	}
}

func (r Record) checkSize(l Limits) error {
	if len(r.StructuredData) > l.MaxElements || len(r.Diagnostics) > l.MaxDiagnostics || len(r.Vendor.Components) > 32 {
		return ErrLimit
	}
	n := 0
	add := func(v int) bool {
		if v > l.headroom()-n {
			return false
		}
		n += v
		return true
	}
	for _, b := range [][]byte{r.Content, r.OriginalSD, r.Unparsed} {
		if len(b) > l.MaxPayload || !add(len(b)) {
			return ErrLimit
		}
	}
	if r.Raw != nil && (len(*r.Raw) > l.MaxPayload || !add(len(*r.Raw))) {
		return ErrLimit
	}
	params := 0
	for _, e := range r.StructuredData {
		if len(e.Parameters) > l.MaxParameters-params {
			return ErrLimit
		}
		params += len(e.Parameters)
		if !add(len(e.ID)) {
			return ErrLimit
		}
		for _, p := range e.Parameters {
			if !add(len(p.Name)) || !add(len(p.Value)) {
				return ErrLimit
			}
		}
	}
	for _, f := range []Field{r.Priority, r.Version, r.Hostname, r.Application, r.ProcessID, r.MessageID, r.Tag, r.Vendor.Module, r.Vendor.Mnemonic, r.Vendor.EventID, r.Vendor.Severity, r.Vendor.Sequence, r.Vendor.Counter, r.Vendor.Thread, r.Vendor.Source, r.Vendor.Slot, r.Vendor.Flags} {
		if !add(len(f.Value)) {
			return ErrLimit
		}
	}
	for _, v := range r.Vendor.Components {
		if !add(len(v)) {
			return ErrLimit
		}
	}
	if !add(len(r.DeviceTime.Original)) || !add(len(r.DeviceTime.Zone)) || !add(len(r.DeviceTime.Uptime)) {
		return ErrLimit
	}
	return nil
}

func wireField(s string) Field {
	if s == "-" {
		return Field{Presence: NilValue}
	}
	return Text(strings.Clone(s))
}
