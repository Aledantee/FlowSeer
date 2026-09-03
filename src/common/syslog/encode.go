package syslog

import (
	"bytes"
	"strconv"
	"time"
	"unicode/utf8"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Loss identifies information a selected wire representation cannot carry.
type Loss uint32

const (
	// LossYear is the year discarded by legacy timestamps.
	LossYear Loss = 1 << iota
	// LossZone is the UTC offset or zone discarded by legacy timestamps.
	LossZone
	// LossPrecision is subsecond precision beyond the target format.
	LossPrecision
	// LossStructuredData is structured data absent from the target format.
	LossStructuredData
	// LossUnparsed is unresolved evidence that cannot be encoded faithfully.
	LossUnparsed
	// LossHeader is a field such as version, process ID, or message ID not represented.
	LossHeader
	// LossVendor is vendor prefix evidence outside the preserved message content.
	LossVendor
	// LossTime is incomplete or invalid device time replaced by NILVALUE.
	LossTime
	// OmitObservation is local reception metadata, outside the syslog wire model.
	OmitObservation
	// OmitRaw is the optional captured original payload.
	OmitRaw
	// OmitDiagnostics is local parser diagnostic information.
	OmitDiagnostics
)

// EncodeReport separates lossy projection from expected local-only omissions.
// Losses are present even when the caller explicitly permits them.
type EncodeReport struct {
	Losses  Loss
	Omitted Loss
}

// EncodeOptions selects a wire format and explicitly allowed losses.
// LegacyMaxPayload defaults to RFC 3164's 1024 bytes; increasing it is an extension.
// Overrides are used only for this call; the source record remains unchanged.
type EncodeOptions struct {
	Format           Format
	AllowLoss        Loss
	Hostname         *string
	DeviceTime       *DeviceTime
	LegacyMaxPayload int
	Limits           Limits
}

// Encode validates and measures before allocating output. Errors return no bytes.
// The caller must keep the record and options stable during the call.
func Encode(record Record, options EncodeOptions) ([]byte, EncodeReport, error) {
	var report EncodeReport
	l, err := options.Limits.normalized()
	if err != nil {
		return nil, report, err
	}
	if err := record.checkSize(l); err != nil {
		return nil, report, err
	}
	r := record
	if options.Hostname != nil {
		r.Hostname = Text(*options.Hostname)
	}
	if options.DeviceTime != nil {
		r.DeviceTime = *options.DeviceTime
	}
	if !r.Observation.ReceivedAt.IsZero() || r.Observation.Peer.IsValid() {
		report.Omitted |= OmitObservation
	}
	if r.Raw != nil {
		report.Omitted |= OmitRaw
	}
	if len(r.Diagnostics) > 0 || r.SuppressedDiagnostics > 0 {
		report.Omitted |= OmitDiagnostics
	}
	if len(r.Unparsed) > 0 || r.Format == Unknown {
		report.Losses |= LossUnparsed
	}
	pri, err := strconv.Atoi(r.Priority.Value)
	if r.Priority.Presence != Present || !digits(r.Priority.Value) || err != nil || pri > 191 {
		return nil, report, ErrUnrepresentable
	}
	if len(r.Vendor.Counters) > 0 {
		report.Losses |= LossVendor
	}
	if normalized := strconv.Itoa(pri); normalized != r.Priority.Value {
		report.Losses |= LossHeader
		r.Priority = Text(normalized)
	}
	if r.Version.Presence == Present && r.Version.Value != "1" {
		report.Losses |= LossHeader
	}

	if options.Format == RFC5424 {
		for i, element := range r.StructuredData {
			for _, previous := range r.StructuredData[:i] {
				if previous.ID == element.ID {
					return nil, report, ErrUnrepresentable
				}
			}
			for j, parameter := range element.Parameters {
				for _, previous := range element.Parameters[:j] {
					if previous.Name == parameter.Name {
						return nil, report, ErrUnrepresentable
					}
				}
			}
		}
	}
	limit := l.MaxPayload
	timestamp := "-"
	switch options.Format {
	case RFC5424:
		if r.DeviceTime.Instant != nil {
			t := *r.DeviceTime.Instant
			if t.Year() < 0 || t.Year() > 9999 {
				return nil, report, ErrUnrepresentable
			}
			if t.Nanosecond()%1000 != 0 {
				report.Losses |= LossPrecision
				t = t.Truncate(time.Microsecond)
			}
			_, offset := t.Zone()
			if offset <= -86400 || offset >= 86400 || offset%60 != 0 {
				return nil, report, ErrUnrepresentable
			}
			timestamp = t.Format(time.RFC3339Nano)
		} else if r.DeviceTime.Present|r.DeviceTime.Inferred != 0 || r.DeviceTime.Original != "" && r.DeviceTime.Original != "-" {
			report.Losses |= LossTime
		}
	case RFC3164:
		limit = options.LegacyMaxPayload
		if limit == 0 {
			limit = 1024
		}
		if limit < 0 || limit > l.MaxPayload {
			return nil, report, ErrLimit
		}
		d := r.DeviceTime
		if d.Instant != nil {
			d.fill(*d.Instant)
			d.Present |= YearPart | DatePart | ClockPart | OffsetPart
		}
		if d.Present&(DatePart|ClockPart) != (DatePart|ClockPart) || r.Hostname.Presence != Present {
			return nil, report, ErrUnrepresentable
		}
		t := time.Date(2000, d.Month, d.Day, d.Hour, d.Minute, d.Second, 0, time.UTC)
		if t.Month() != d.Month || t.Day() != d.Day || t.Hour() != d.Hour || t.Minute() != d.Minute || t.Second() != d.Second {
			return nil, report, ErrUnrepresentable
		}
		timestamp = t.Format("Jan _2 15:04:05")
		if (d.Present|d.Inferred)&YearPart != 0 {
			report.Losses |= LossYear
		}
		if (d.Present|d.Inferred)&OffsetPart != 0 || d.Zone != "" {
			report.Losses |= LossZone
		}
		if d.Nanosecond != 0 {
			report.Losses |= LossPrecision
		}
		if len(r.StructuredData) > 0 || len(r.OriginalSD) > 1 {
			report.Losses |= LossStructuredData
		}
		tag, app, process := legacyTag(string(r.Content))
		if r.MessageID.Presence == Present ||
			r.Application.Presence == Present && r.Application != app ||
			r.ProcessID.Presence == Present && r.ProcessID != process ||
			r.Tag.Presence == Present && r.Tag != tag {
			report.Losses |= LossHeader
		}
	default:
		return nil, report, errs.Msg("invalid syslog output format")
	}
	if r.DeviceTime.ClockMarker != "" || r.DeviceTime.Uptime != "" {
		report.Losses |= LossTime
	}
	if report.Losses & ^options.AllowLoss != 0 {
		return nil, report, ErrUnrepresentable
	}
	if bytes.HasPrefix(r.Content, []byte{0xef, 0xbb, 0xbf}) && !utf8.Valid(r.Content[3:]) {
		return nil, report, ErrUnrepresentable
	}
	measure := wireWriter{limit: limit}
	if err := encodeInto(&measure, r, options.Format, timestamp); err != nil {
		return nil, report, err
	}
	out := wireWriter{limit: limit, out: make([]byte, 0, measure.size)}
	if err := encodeInto(&out, r, options.Format, timestamp); err != nil {
		return nil, report, err
	}
	return out.out, report, nil
}

type wireWriter struct {
	out         []byte
	size, limit int
}

func (w *wireWriter) text(s string) error {
	if len(s) > w.limit-w.size {
		return ErrLimit
	}
	w.size += len(s)
	if w.out != nil {
		w.out = append(w.out, s...)
	}
	return nil
}

func (w *wireWriter) data(b []byte) error {
	if len(b) > w.limit-w.size {
		return ErrLimit
	}
	w.size += len(b)
	if w.out != nil {
		w.out = append(w.out, b...)
	}
	return nil
}

func encodeInto(w *wireWriter, r Record, format Format, timestamp string) error {
	if err := w.text("<" + r.Priority.Value + ">"); err != nil {
		return err
	}
	if format == RFC5424 {
		if err := w.text("1 "); err != nil {
			return err
		}
	}
	if err := w.text(timestamp + " "); err != nil {
		return err
	}
	fields := []Field{r.Hostname}
	sizes := []int{255}
	if format == RFC5424 {
		fields = append(fields, r.Application, r.ProcessID, r.MessageID)
		sizes = append(sizes, 48, 128, 32)
	}
	for i, f := range fields {
		value := "-"
		if f.Presence == Present {
			value = f.Value
			if value == "-" {
				return ErrUnrepresentable
			}
		}
		if !headerText(value, sizes[i]) {
			return ErrUnrepresentable
		}
		if err := w.text(value); err != nil {
			return err
		}
		if err := w.text(" "); err != nil {
			return err
		}
	}
	if format == RFC5424 {
		if len(r.StructuredData) == 0 {
			if err := w.text("-"); err != nil {
				return err
			}
		} else {
			for _, e := range r.StructuredData {
				if !validName(e.ID) {
					return ErrUnrepresentable
				}
				if err := w.text("[" + e.ID); err != nil {
					return err
				}
				for _, p := range e.Parameters {
					if !validName(p.Name) || !utf8.Valid(p.Value) {
						return ErrUnrepresentable
					}
					if err := w.text(" " + p.Name + "=\""); err != nil {
						return err
					}
					for _, b := range p.Value {
						if b == '"' || b == '\\' || b == ']' {
							if err := w.text("\\"); err != nil {
								return err
							}
						}
						if err := w.data([]byte{b}); err != nil {
							return err
						}
					}
					if err := w.text("\""); err != nil {
						return err
					}
				}
				if err := w.text("]"); err != nil {
					return err
				}
			}
		}
		if len(r.Content) == 0 {
			return nil
		}
		if err := w.text(" "); err != nil {
			return err
		}
	}
	return w.data(r.Content)
}
