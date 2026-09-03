package syslog

import "strings"

func (p *Parser) legacy(r *Record, s string, owned []byte, pos int) {
	rest := s[pos:]
	for range 2 {
		i := strings.Index(rest, ": ")
		if i <= 0 || i > 20 || !digits(rest[:i]) {
			break
		}
		r.Vendor.Counters = append(r.Vendor.Counters, rest[:i])
		pos += i + 2
		rest = s[pos:]
	}
	if len(r.Vendor.Counters) > 0 {
		if len(p.options.CiscoCounterOrder) != len(r.Vendor.Counters) {
			r.diagnose("ambiguous_vendor_counters", 0, p.limits)
		} else {
			for i, role := range p.options.CiscoCounterOrder {
				if role == "sequence" {
					r.Vendor.Sequence = Text(r.Vendor.Counters[i])
				} else {
					r.Vendor.Counter = Text(r.Vendor.Counters[i])
				}
			}
		}
	}
	d, n := p.timestampPrefix(rest)
	recognized := n > 0 || r.Priority.Presence == Present
	if !recognized {
		r.Unparsed = owned[pos:]
		r.diagnose("unknown_envelope", pos, p.limits)
		return
	}
	r.Format = RFC3164
	r.Status = Complete
	r.DeviceTime = d
	pos += n
	for pos < len(s) && (s[pos] == ' ' || s[pos] == ':') {
		pos++
	}
	if n == 0 {
		r.diagnose("missing_timestamp", pos, p.limits)
	} else if d.Present&(DatePart|ClockPart) != (DatePart|ClockPart) || strings.Contains(d.Original, "/") && p.options.NumericDateOrder == "" {
		r.diagnose("unresolved_timestamp", pos-n, p.limits)
		r.Unparsed = owned[:pos]
	}
	rest = s[pos:]
	first, tail := token(rest)
	// A recognizable prefix or tag at this position means origin was omitted.
	if first != "" && !strings.HasPrefix(first, "%") && !strings.HasSuffix(first, ":") && !strings.Contains(first, "[") && !numericEvent(rest) {
		r.Hostname = Text(first)
		pos += len(first)
		if tail != "" {
			pos++
		}
	}
	if r.Hostname.Presence == Present && !headerText(r.Hostname.Value, 255) {
		r.diagnose("invalid_hostname", pos-len(r.Hostname.Value), p.limits)
		r.Unparsed = owned[:pos]
	}
	r.Content = owned[pos:]
	// Retain the complete post-envelope content even when a tag is extracted.
	body := s[pos:]
	if colon := strings.IndexByte(body, ':'); colon > 0 && colon <= 128 {
		tag := body[:colon]
		if !strings.ContainsAny(tag, " %|") && headerText(tag, 128) {
			r.Tag = Text(tag)
			r.Application = Text(tag)
			if bracket := strings.IndexByte(tag, '['); bracket > 0 && strings.HasSuffix(tag, "]") {
				r.Application = Text(tag[:bracket])
				r.ProcessID = Text(tag[bracket+1 : len(tag)-1])
			}
		}
	}
}

func numericEvent(s string) bool {
	a, b := token(s)
	return len(a) >= 4 && digits(a) && strings.Contains(b, ":")
}
