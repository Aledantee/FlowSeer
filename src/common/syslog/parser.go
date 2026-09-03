package syslog

import (
	"bytes"
	"maps"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Parser interprets complete payloads. It is immutable and safe for concurrent use.
// Parse never reads the current clock or retains caller-owned input.
type Parser struct {
	options ParseOptions
	limits  Limits
}

// NewParser validates options and snapshots mutable configuration maps and slices.
func NewParser(options ParseOptions) (*Parser, error) {
	l, err := options.Limits.normalized()
	if err != nil {
		return nil, err
	}
	if options.Year < 0 || options.Year > 9999 {
		return nil, errs.Msg("invalid syslog year")
	}
	if options.NumericDateOrder != "" && options.NumericDateOrder != "mdy" && options.NumericDateOrder != "dmy" {
		return nil, errs.Msg("invalid syslog numeric date order")
	}
	if len(options.ZoneOffsets) > 64 || len(options.CiscoComponents) > 32 {
		return nil, ErrLimit
	}
	for name, offset := range options.ZoneOffsets {
		if len(name) > 32 || offset <= -86400 || offset >= 86400 {
			return nil, errs.Msg("invalid syslog zone context")
		}
	}
	if len(options.CiscoCounterOrder) > 2 {
		return nil, ErrLimit
	}
	for _, role := range options.CiscoCounterOrder {
		if role != "sequence" && role != "counter" {
			return nil, errs.Msg("invalid Cisco counter role")
		}
	}
	if len(options.CiscoCounterOrder) == 2 && options.CiscoCounterOrder[0] == options.CiscoCounterOrder[1] {
		return nil, errs.Msg("duplicate Cisco counter role")
	}
	options.CiscoCounterOrder = append([]string(nil), options.CiscoCounterOrder...)
	options.ZoneOffsets = maps.Clone(options.ZoneOffsets)
	options.CiscoComponents = append([]string(nil), options.CiscoComponents...)
	return &Parser{options: options, limits: l}, nil
}

// Parse returns an owned record, including for malformed or unknown payloads.
// Only configuration/resource failures are errors. Observation is copied verbatim.
// Byte fields within one record can share storage; Clone separates records.
func (p *Parser) Parse(payload []byte, observation Observation) (Record, error) {
	if len(payload) > p.limits.MaxPayload {
		return Record{}, ErrLimit
	}
	owned := bytes.Clone(payload)
	if owned == nil {
		owned = []byte{}
	}
	r := Record{Observation: observation, Format: Unknown, Status: Unrecognized, Content: owned}
	if p.options.CaptureRaw {
		r.Raw = &owned
	}
	s := string(owned)
	pos := 0
	if strings.HasPrefix(s, "<") {
		end := strings.IndexByte(s, '>')
		if end >= 2 && end <= 4 && digits(s[1:end]) {
			n, _ := strconv.Atoi(s[1:end])
			if n <= 191 {
				r.Priority = Text(s[1:end])
				pos = end + 1
			} else {
				r.diagnose("invalid_priority", 0, p.limits)
			}
		} else {
			r.diagnose("invalid_priority", 0, p.limits)
		}
	}
	start := pos
	for pos < len(s) && s[pos] == ' ' {
		pos++
	}
	if pos != start {
		r.diagnose("header_whitespace", start, p.limits)
	}
	// The version plus timestamp shape commits the structured envelope. Damaged
	// structured data must not fall through to an unrelated legacy interpretation.
	version, rest := token(s[pos:])
	timestamp, _ := token(rest)
	if r.Priority.Presence == Present && len(version) <= 3 && digits(version) && version[0] != '0' && (timestamp == "-" || isoShape(timestamp)) {
		r.Format = RFC5424
		r.Status = Complete
		r.Version = Text(version)
		p.structured(&r, s, owned, pos+len(version)+1)
	} else {
		p.legacy(&r, s, owned, pos)
	}
	p.vendor(&r)
	if len(r.Diagnostics) > 0 && r.Status == Complete {
		r.Status = Partial
	}
	return r, nil
}

func token(s string) (string, string) {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isoShape(s string) bool {
	return len(s) >= 19 && s[4] == '-' && s[7] == '-' && (s[10] == 'T' || s[10] == 't')
}

func validName(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for i := range len(s) {
		if s[i] < 33 || s[i] > 126 || s[i] == '=' || s[i] == ']' || s[i] == '"' {
			return false
		}
	}
	return true
}
