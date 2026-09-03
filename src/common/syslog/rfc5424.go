package syslog

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

func (p *Parser) structured(r *Record, s string, owned []byte, pos int) {
	fields := [5]string{}
	for i := range 5 {
		if pos >= len(s) {
			r.diagnose("incomplete_header", pos, p.limits)
			r.Unparsed = owned[pos:]
			return
		}
		end := strings.IndexByte(s[pos:], ' ')
		if end < 0 {
			r.diagnose("incomplete_header", pos, p.limits)
			r.Unparsed = owned[pos:]
			return
		}
		fields[i] = s[pos : pos+end]
		pos += end + 1
	}
	r.DeviceTime = p.deviceTime(fields[0])
	if i := strings.IndexByte(fields[0], '.'); i >= 0 {
		end := i + 1
		for end < len(fields[0]) && fields[0][end] >= '0' && fields[0][end] <= '9' {
			end++
		}
		if end-i-1 > 6 {
			r.diagnose("timestamp_precision", 0, p.limits)
		}
	}
	if fields[0] != "-" && r.DeviceTime.Instant == nil {
		r.diagnose("invalid_timestamp", 0, p.limits)
	}
	r.Hostname = wireField(fields[1])
	r.Application = wireField(fields[2])
	r.ProcessID = wireField(fields[3])
	r.MessageID = wireField(fields[4])
	for i, max := range []int{255, 48, 128, 32} {
		v := fields[i+1]
		if !headerText(v, max) {
			r.diagnose("invalid_header_field", 0, p.limits)
			r.Unparsed = owned[:pos]
		}
	}
	if r.Version.Value != "1" {
		r.diagnose("unsupported_version", 0, p.limits)
	}
	start := pos
	switch {
	case pos < len(s) && s[pos] == '-':
		pos++
	case pos < len(s) && s[pos] == '[':
		var ok bool
		pos, ok = p.structuredData(r, s, pos)
		if !ok {
			r.OriginalSD = owned[start:]
			r.Unparsed = owned[start:]
			r.Content = nil
			return
		}
	default:
		r.diagnose("invalid_sd", pos, p.limits)
		r.Unparsed = owned[pos:]
		r.Content = nil
		return
	}
	r.OriginalSD = owned[start:pos]
	if pos == len(s) {
		r.Content = owned[pos:]
		return
	}
	if s[pos] != ' ' {
		r.diagnose("invalid_sd_separator", pos, p.limits)
		r.Unparsed = owned[pos:]
		r.Content = nil
		return
	}
	r.Content = owned[pos+1:]
	if bytes.HasPrefix(r.Content, []byte{0xef, 0xbb, 0xbf}) && !utf8.Valid(r.Content[3:]) {
		r.diagnose("invalid_utf8_bom", pos+1, p.limits)
	}
}

func (p *Parser) structuredData(r *Record, s string, pos int) (int, bool) {
	params, metadata := 0, 0
	fail := func(code string) (int, bool) { r.diagnose(code, pos, p.limits); return pos, false }
	for pos < len(s) && s[pos] == '[' {
		if len(r.StructuredData) >= p.limits.MaxElements {
			return fail("metadata_limit")
		}
		pos++
		start := pos
		for pos < len(s) && s[pos] != ' ' && s[pos] != ']' {
			pos++
		}
		id := s[start:pos]
		if !validName(id) {
			return fail("invalid_sd_name")
		}
		metadata += len(id) + 64
		if metadata > p.limits.MetadataBytes {
			return fail("metadata_limit")
		}
		for _, prior := range r.StructuredData {
			if prior.ID == id {
				r.diagnose("duplicate_sd_id", start, p.limits)
				break
			}
		}
		// Keep already parsed parameters even when a later parameter is malformed.
		r.StructuredData = append(r.StructuredData, Element{ID: id})
		current := &r.StructuredData[len(r.StructuredData)-1]
		for pos < len(s) && s[pos] == ' ' {
			pos++
			start = pos
			for pos < len(s) && s[pos] != '=' && s[pos] != ']' && s[pos] != ' ' {
				pos++
			}
			name := s[start:pos]
			if !validName(name) || pos+1 >= len(s) || s[pos] != '=' || s[pos+1] != '"' {
				return fail("invalid_sd_parameter")
			}
			params++
			metadata += len(name) + 48
			if params > p.limits.MaxParameters || metadata > p.limits.MetadataBytes {
				return fail("metadata_limit")
			}
			pos += 2
			start = pos
			var value []byte
			for pos < len(s) && s[pos] != '"' {
				c := s[pos]
				pos++
				switch c {
				case '\\':
					if pos >= len(s) {
						return fail("invalid_sd_escape")
					}
					c = s[pos]
					pos++
					if c != '\\' && c != '"' && c != ']' {
						r.diagnose("invalid_sd_escape", pos-2, p.limits)
						value = append(value, '\\')
						metadata++
					}
				case ']':
					r.diagnose("unescaped_sd_bracket", pos-1, p.limits)
				}
				metadata++
				if metadata > p.limits.MetadataBytes {
					return fail("metadata_limit")
				}
				value = append(value, c)
			}
			if pos >= len(s) {
				return fail("unterminated_sd")
			}
			pos++
			for _, prior := range current.Parameters {
				if prior.Name == name {
					r.diagnose("duplicate_sd_parameter", start, p.limits)
					break
				}
			}
			current.Parameters = append(current.Parameters, Parameter{Name: name, Value: value})
		}
		if pos >= len(s) || s[pos] != ']' {
			return fail("unterminated_sd")
		}
		pos++
	}
	return pos, true
}

func headerText(s string, maximum int) bool {
	if len(s) == 0 || len(s) > maximum {
		return false
	}
	for i := range len(s) {
		if s[i] < 33 || s[i] > 126 {
			return false
		}
	}
	return true
}
