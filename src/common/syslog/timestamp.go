package syslog

import (
	"strconv"
	"strings"
	"time"
)

func (p *Parser) deviceTime(original string) DeviceTime {
	d := DeviceTime{Original: original}
	s := original
	if s == "-" || s == "" {
		return d
	}
	if s[0] == '*' || s[0] == '.' {
		d.ClockMarker = s[:1]
		s = s[1:]
	}
	if isoShape(s) {
		instant, err := time.Parse(time.RFC3339Nano, s)
		if err == nil {
			d.fill(instant)
			d.Instant = &instant
			d.Present = YearPart | DatePart | ClockPart | OffsetPart
			d.Zone = s[19:]
			return d
		}
		// Preserve calendar components when an ISO clock lacks an offset.
		for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02t15:04:05.999999999"} {
			if t, err := time.Parse(layout, s); err == nil {
				d.fill(t)
				d.Present = YearPart | DatePart | ClockPart
				p.resolve(&d)
				return d
			}
		}
		return d
	}
	fields := strings.Fields(s)
	if len(fields) < 2 {
		if strings.ContainsAny(s, "dhw") && len(s) < 32 {
			d.Uptime = s
		}
		return d
	}
	if len(fields) < 3 && !strings.Contains(fields[0], "/") {
		return d
	}
	month := monthNumber(fields[0])
	dateIndex, clockIndex := 1, 2
	if month == 0 && strings.Contains(fields[0], "/") {
		parts := strings.Split(fields[0], "/")
		if len(parts) != 3 || p.options.NumericDateOrder == "" {
			return d
		}
		a, e1 := strconv.Atoi(parts[0])
		b, e2 := strconv.Atoi(parts[1])
		y, e3 := strconv.Atoi(parts[2])
		if e1 != nil || e2 != nil || e3 != nil {
			return d
		}
		if p.options.NumericDateOrder == "dmy" {
			a, b = b, a
		}
		d.Month = time.Month(a)
		d.Day = b
		d.Year = y
		d.Present = DatePart | YearPart
		clockIndex = 1
	} else {
		if month == 0 {
			return d
		}
		day, err := strconv.Atoi(fields[dateIndex])
		if err != nil || day < 1 || day > 31 {
			return d
		}
		d.Month = month
		d.Day = day
		d.Present = DatePart
		if len(fields) > 3 && len(fields[2]) == 4 && digits(fields[2]) {
			d.Year, _ = strconv.Atoi(fields[2])
			d.Present |= YearPart
			clockIndex = 3
		}
	}
	if clockIndex >= len(fields) {
		return d
	}
	clock := fields[clockIndex]
	if len(clock) > 8 && clock[8] == ':' {
		clock = clock[:8] + "." + clock[9:]
	}
	t, err := time.Parse("15:04:05.999999999", clock)
	if err != nil {
		return d
	}
	d.Hour = t.Hour()
	d.Minute = t.Minute()
	d.Second = t.Second()
	d.Nanosecond = t.Nanosecond()
	d.Present |= ClockPart
	for _, v := range fields[clockIndex+1:] {
		if len(v) == 4 && digits(v) {
			d.Year, _ = strconv.Atoi(v)
			d.Present |= YearPart
		} else if v != "" {
			d.Zone = v
		}
	}
	p.resolve(&d)
	return d
}

func (d *DeviceTime) fill(t time.Time) {
	d.Year = t.Year()
	d.Month = t.Month()
	d.Day = t.Day()
	d.Hour = t.Hour()
	d.Minute = t.Minute()
	d.Second = t.Second()
	d.Nanosecond = t.Nanosecond()
	_, d.OffsetSeconds = t.Zone()
}

func (p *Parser) resolve(d *DeviceTime) {
	if d.Present&YearPart == 0 && p.options.Year != 0 {
		d.Year = p.options.Year
		d.Inferred |= YearPart
	}
	if (d.Present|d.Inferred)&(YearPart|DatePart|ClockPart) != (YearPart | DatePart | ClockPart) {
		return
	}
	loc := p.options.Location
	if d.Zone != "" {
		if d.Zone == "UTC" || d.Zone == "GMT" || d.Zone == "Z" {
			loc = time.UTC
			d.Present |= OffsetPart
		} else if offset, ok := p.options.ZoneOffsets[d.Zone]; ok {
			loc = time.FixedZone(d.Zone, offset)
			d.Inferred |= OffsetPart
		} else {
			return
		}
	} else if loc != nil {
		d.Inferred |= OffsetPart
	}
	if loc == nil {
		return
	}
	candidate := time.Date(d.Year, d.Month, d.Day, d.Hour, d.Minute, d.Second, d.Nanosecond, loc)
	same := func(t time.Time) bool {
		return t.Year() == d.Year && t.Month() == d.Month && t.Day() == d.Day && t.Hour() == d.Hour && t.Minute() == d.Minute && t.Second() == d.Second
	}
	if !same(candidate) {
		return
	}
	// Check both offsets surrounding a transition. A fold has two valid instants;
	// a gap has none. Neither gets an arbitrary daylight-saving interpretation.
	_, offset := candidate.Zone()
	for _, delta := range []time.Duration{-48 * time.Hour, 48 * time.Hour} {
		_, other := candidate.Add(delta).Zone()
		if other != offset && same(candidate.Add(time.Duration(offset-other)*time.Second)) {
			return
		}
	}
	d.OffsetSeconds = offset
	d.Instant = &candidate
}

func monthNumber(s string) time.Month {
	if len(s) != 3 {
		return 0
	}
	for i, m := range []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"} {
		if strings.EqualFold(s, m) {
			return time.Month(i + 1)
		}
	}
	return 0
}

// timestampPrefix recognizes only bounded calendar shapes, leaving the remainder
// untouched. A numeric date without context is retained but never resolved.
func (p *Parser) timestampPrefix(s string) (DeviceTime, int) {
	offset := 0
	if len(s) > 0 && (s[0] == '*' || s[0] == '.') {
		offset = 1
	}
	rest := s[offset:]
	first, _ := token(rest)
	if isoShape(first) {
		return p.deviceTime(s[:offset+len(first)]), offset + len(first)
	}
	if monthNumber(first) == 0 && !strings.Contains(first, "/") {
		if strings.HasSuffix(first, ":") && strings.ContainsAny(first, "dhw") {
			end := offset + len(first) - 1
			return p.deviceTime(s[:end]), end
		}
		return DeviceTime{}, 0
	}
	// At most six tokens: month day [year] clock [zone] [year].
	end := offset
	count := 0
	clockSeen := false
	for end < len(s) && count < 6 {
		for end < len(s) && s[end] == ' ' {
			end++
		}
		start := end
		for end < len(s) && s[end] != ' ' {
			end++
		}
		v := s[start:end]
		count++
		clean := strings.TrimSuffix(v, ":")
		if count == 1 && strings.Contains(v, "/") {
			continue
		}
		if strings.Count(clean, ":") >= 2 {
			clockSeen = true
			if len(v) > 0 && v[len(v)-1] == ':' {
				end--
				break
			}
			continue
		}
		if clockSeen {
			if len(v) == 4 && digits(v) {
				continue
			}
			if v == "UTC" || v == "GMT" {
				continue
			}
			if _, ok := p.options.ZoneOffsets[v]; ok {
				continue
			}
			end = start
			for end > 0 && s[end-1] == ' ' {
				end--
			}
			break
		}
		if count > 1 && !digits(v) {
			return DeviceTime{}, 0
		}
	}
	if !clockSeen {
		return DeviceTime{}, 0
	}
	return p.deviceTime(s[:end]), end
}
