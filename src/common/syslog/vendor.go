package syslog

import "strings"

func (p *Parser) vendor(r *Record, s string) {
	body := s
	if r.Tag.Presence == Present {
		_, body, _ = strings.Cut(s, ":")
		body = strings.TrimLeft(body, " ")
	}
	if strings.HasPrefix(s, "%%") {
		p.slashVendor(r, s)
		return
	}
	if strings.HasPrefix(s, "%") {
		p.cisco(r, s)
		return
	}
	// NX-OS adds a slot label before the percent prefix.
	if strings.HasPrefix(s, "Slot ") || strings.HasPrefix(s, "slot ") {
		if i := strings.Index(s, ": %"); i > 5 && i < 32 {
			r.Vendor.Slot = Text(s[5:i])
			p.cisco(r, s[i+2:])
			return
		}
	}
	if strings.HasPrefix(body, "Event|") {
		fields := strings.SplitN(body, "|", 7)
		if len(fields) >= 6 && digits(fields[1]) {
			r.Vendor.Family = "aruba-cx"
			r.Vendor.EventID = Text(fields[1])
			r.Vendor.Severity = Text(fields[2])
			r.Vendor.Components = append([]string(nil), fields[3:5]...)
		}
		return
	}
	first, tail := token(s)
	if digits(first) && len(first) >= 4 {
		if i := strings.IndexByte(tail, ':'); i > 0 && i <= 32 && !strings.Contains(tail[:i], " ") {
			r.Vendor.Family = "numeric-subsystem"
			r.Vendor.EventID = Text(first)
			r.Vendor.Module = Text(tail[:i])
		}
		return
	}
	// FASTPATH-family prefixes share a component/thread and source(sequence) form.
	if open := strings.IndexByte(s, '['); open > 0 && open < 64 {
		closeBracket := strings.Index(s[open:], "]:")
		if closeBracket > 1 {
			after := strings.TrimLeft(s[open+closeBracket+2:], " ")
			source, remaining := token(after)
			sequence, _, found := strings.Cut(remaining, "%%")
			sequence = strings.TrimSpace(sequence)
			if found && digits(sequence) && strings.HasSuffix(source, ")") && strings.Contains(source, "(") {
				r.Vendor.Family = "fastpath"
				r.Vendor.Module = Text(strings.TrimSpace(s[:open]))
				r.Vendor.Thread = Text(s[open+1 : open+closeBracket])
				r.Vendor.Source = Text(source)
				r.Vendor.Sequence = Text(sequence)
				return
			}
		}
	}
	if strings.HasPrefix(s, "POE:") || strings.HasPrefix(s, "ZONEDEFENSE:") {
		r.Vendor.Family = "dlink-body"
		r.Vendor.Module = Text(s[:strings.IndexByte(s, ':')])
		return
	}
	if strings.HasPrefix(s, "<") {
		if i := strings.IndexByte(s, '>'); i > 1 && i < 128 {
			level, event, ok := strings.Cut(s[1:i], ":")
			if ok && strings.Contains(event, ".") {
				r.Vendor.Family = "extreme-exos"
				r.Vendor.Severity = Text(level)
				r.Vendor.Mnemonic = Text(event)
			}
		}
	}
}

func (p *Parser) slashVendor(r *Record, s string) {
	colon := strings.IndexByte(s, ':')
	if colon < 0 || colon > 160 {
		return
	}
	prefix := s[2:colon]
	fields := strings.Split(prefix, "/")
	if len(fields) != 3 || len(fields[0]) < 3 || !digits(fields[0][:2]) || len(fields[1]) != 1 || fields[1][0] < '0' || fields[1][0] > '7' {
		return
	}
	r.Vendor.Family = "huawei-comware"
	r.Vendor.Module = Text(fields[0][2:])
	r.Vendor.Severity = Text(fields[1])
	r.Vendor.Mnemonic = Text(fields[2])
	if i := strings.IndexByte(fields[2], '('); i > 0 && strings.HasSuffix(fields[2], ")") {
		r.Vendor.Mnemonic = Text(fields[2][:i])
		r.Vendor.Flags = Text(fields[2][i+1 : len(fields[2])-1])
	}
}

func (p *Parser) cisco(r *Record, s string) {
	colon := strings.IndexByte(s, ':')
	if colon < 0 || colon > 160 {
		return
	}
	fields := strings.Split(s[1:colon], "-")
	if len(fields) < 3 {
		return
	}
	// Locate severity from the right; additional components are not identities.
	sev := len(fields) - 2
	if len(fields[sev]) != 1 || fields[sev][0] < '0' || fields[sev][0] > '7' {
		return
	}
	r.Vendor.Family = "cisco"
	r.Vendor.Module = Text(fields[0])
	r.Vendor.Severity = Text(fields[sev])
	if fields[0] == "ASA" && digits(fields[len(fields)-1]) {
		r.Vendor.EventID = Text(fields[len(fields)-1])
	} else {
		r.Vendor.Mnemonic = Text(fields[len(fields)-1])
	}
	if sev > 1 {
		if sev-1 > 32 {
			r.diagnose("metadata_limit", 0, p.limits)
			return
		}
		r.Vendor.Components = append([]string(nil), fields[1:sev]...)
		if p.options.CiscoComponentCount != sev-1 {
			r.diagnose("ambiguous_vendor_components", 0, p.limits)
		}
	}
}
