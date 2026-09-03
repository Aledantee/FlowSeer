package snmp

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// RowStatus is the SMIv2 RowStatus textual convention (RFC 2579).
type RowStatus int32

const (
	// RowStatusActive marks the row as available for protocol operations.
	RowStatusActive RowStatus = 1
	// RowStatusNotInService marks the row as provisioned but inactive.
	RowStatusNotInService RowStatus = 2
	// RowStatusNotReady marks the row as missing required columns.
	RowStatusNotReady RowStatus = 3
	// RowStatusCreateAndGo is the Set-only value to create-and-activate.
	RowStatusCreateAndGo RowStatus = 4
	// RowStatusCreateAndWait is the Set-only value to create-but-not-activate.
	RowStatusCreateAndWait RowStatus = 5
	// RowStatusDestroy is the Set-only value to destroy the row.
	RowStatusDestroy RowStatus = 6
)

// String returns the canonical lowercase-camel name of the row status.
func (s RowStatus) String() string {
	switch s {
	case RowStatusActive:
		return "active"
	case RowStatusNotInService:
		return "notInService"
	case RowStatusNotReady:
		return "notReady"
	case RowStatusCreateAndGo:
		return "createAndGo"
	case RowStatusCreateAndWait:
		return "createAndWait"
	case RowStatusDestroy:
		return "destroy"
	}
	return fmt.Sprintf("RowStatus(%d)", int32(s))
}

// exceptionError builds a wrapped ErrException carrying the exception
// variant's Kind for diagnostics. Exposed via the sentinel — callers
// distinguish with errors.Is(err, ErrException).
func exceptionError(vb VarBind) error {
	return errs.Wrapf(ErrException, "%s", vb.GetHeader().Kind)
}

// typeMismatchError builds a wrapped ErrTypeMismatch with the actual and
// expected wire kinds, suitable for diagnostics. Callers distinguish with
// errors.Is(err, ErrTypeMismatch).
func typeMismatchError(got VarBind, want string) error {
	return errs.Wrapf(ErrTypeMismatch, "got %T, want %s", got, want)
}

// DecodeMacAddress decodes the SMIv2 MacAddress textual convention: an
// OCTET STRING of exactly six octets. Exception variants surface as a
// wrapped [ErrException]; non-OctetString variants as a wrapped
// [ErrTypeMismatch].
func DecodeMacAddress(vb VarBind) (net.HardwareAddr, error) {
	if IsException(vb) {
		return nil, exceptionError(vb)
	}
	os, ok := vb.(OctetStringVar)
	if !ok {
		return nil, typeMismatchError(vb, "OctetString")
	}
	if len(os.Value) != 6 {
		return nil, errs.New().Attr("got", len(os.Value)).Msg("expected 6 octets")
	}
	out := make(net.HardwareAddr, 6)
	copy(out, os.Value)
	return out, nil
}

// DecodeDateAndTime decodes the SMIv2 DateAndTime textual convention
// (RFC 2579). The 8-octet short form encodes UTC; the 11-octet long form
// adds a direction-from-UTC byte and an hours/minutes offset.
//
// Wire layout (RFC 2579 §3):
//
//	octets 1-2: year   (big-endian)
//	octet  3:   month  (1-12)
//	octet  4:   day    (1-31)
//	octet  5:   hour   (0-23)
//	octet  6:   minute (0-59)
//	octet  7:   second (0-60; 60 permitted for leap-second representation)
//	octet  8:   deci-seconds (0-9)
//	octet  9:   direction-from-UTC ('+' / '-')
//	octet  10:  hours-from-UTC     (0-13)
//	octet  11:  minutes-from-UTC   (0-59)
func DecodeDateAndTime(vb VarBind) (time.Time, error) {
	if IsException(vb) {
		return time.Time{}, exceptionError(vb)
	}
	os, ok := vb.(OctetStringVar)
	if !ok {
		return time.Time{}, typeMismatchError(vb, "OctetString")
	}
	b := os.Value
	if len(b) != 8 && len(b) != 11 {
		return time.Time{}, errs.New().Attr("got", len(b)).Msg("expected 8 or 11 octets")
	}
	year := int(binary.BigEndian.Uint16(b[0:2]))
	month := int(b[2])
	day := int(b[3])
	hour := int(b[4])
	minute := int(b[5])
	second := int(b[6])
	deci := int(b[7])
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, errs.New().Attr("month", month).Attr("day", day).Msg("out-of-range month/day")
	}
	// RFC 2579 §2 field ranges. second = 60 is permitted for leap-second
	// representation; time.Date normalises it to the next minute, which
	// is the documented behavior callers expect.
	if hour > 23 {
		return time.Time{}, errs.New().Attr("hour", hour).Msg("out-of-range hour (RFC 2579: 0..23)")
	}
	if minute > 59 {
		return time.Time{}, errs.New().Attr("minute", minute).Msg("out-of-range minute (RFC 2579: 0..59)")
	}
	if second > 60 {
		return time.Time{}, errs.New().Attr("second", second).Msg("out-of-range second (RFC 2579: 0..60)")
	}
	if deci > 9 {
		return time.Time{}, errs.New().Attr("deci_second", deci).Msg("out-of-range deci-second (RFC 2579: 0..9)")
	}
	nsec := deci * int(time.Millisecond/time.Nanosecond) * 100

	loc := time.UTC
	if len(b) == 11 {
		dir := b[8]
		offHours := int(b[9])
		offMins := int(b[10])
		sign := 0
		switch dir {
		case '+':
			sign = +1
		case '-':
			sign = -1
		default:
			return time.Time{}, errs.New().Attr("direction", dir).Msg("invalid direction-from-UTC")
		}
		if offHours > 13 {
			return time.Time{}, errs.New().Attr("hours_from_utc", offHours).Msg("out-of-range hours-from-UTC (RFC 2579: 0..13)")
		}
		if offMins > 59 {
			return time.Time{}, errs.New().Attr("minutes_from_utc", offMins).Msg("out-of-range minutes-from-UTC (RFC 2579: 0..59)")
		}
		offsetSec := sign * (offHours*3600 + offMins*60)
		// FixedZone name is informational; using the offset gives stable
		// round-trip semantics for callers comparing with time.Equal.
		loc = time.FixedZone(fmt.Sprintf("UTC%+03d:%02d", sign*offHours, offMins), offsetSec)
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, nsec, loc), nil
}

// DecodeTruthValue decodes the SMIv2 TruthValue textual convention: an
// Integer32 where 1 means true and 2 means false. Any other integer is
// an error.
func DecodeTruthValue(vb VarBind) (bool, error) {
	if IsException(vb) {
		return false, exceptionError(vb)
	}
	iv, ok := vb.(Integer32Var)
	if !ok {
		return false, typeMismatchError(vb, "Integer32")
	}
	switch iv.Value {
	case 1:
		return true, nil
	case 2:
		return false, nil
	}
	return false, errs.New().Attr("got", iv.Value).Msg("expected 1 or 2")
}

// DecodeRowStatus decodes the SMIv2 RowStatus textual convention. Values
// outside the enum return an error rather than a typed unknown — RowStatus
// is closed in RFC 2579.
func DecodeRowStatus(vb VarBind) (RowStatus, error) {
	if IsException(vb) {
		return 0, exceptionError(vb)
	}
	iv, ok := vb.(Integer32Var)
	if !ok {
		return 0, typeMismatchError(vb, "Integer32")
	}
	switch RowStatus(iv.Value) {
	case RowStatusActive, RowStatusNotInService, RowStatusNotReady,
		RowStatusCreateAndGo, RowStatusCreateAndWait, RowStatusDestroy:
		return RowStatus(iv.Value), nil
	}
	return 0, errs.New().Attr("value", iv.Value).Msg("unknown value")
}

// DecodeDisplayString decodes the SMIv2 DisplayString textual convention.
//
// RFC 2579 restricts DisplayString to NVT ASCII (octets 0x20-0x7E plus
// HT/LF/CR), but real-world agents frequently emit UTF-8 in interface
// descriptions and contact strings; this decoder accepts both and returns
// the raw bytes as a Go string. Callers needing strict NVT-ASCII validation
// must check the result themselves.
func DecodeDisplayString(vb VarBind) (string, error) {
	if IsException(vb) {
		return "", exceptionError(vb)
	}
	os, ok := vb.(OctetStringVar)
	if !ok {
		return "", typeMismatchError(vb, "OctetString")
	}
	return string(os.Value), nil
}

// DecodePhysAddress decodes the SMIv2 PhysAddress textual convention: an
// OCTET STRING of any length representing a media-layer address. The
// returned slice is a defensive copy so the caller may freely mutate it
// without aliasing the VarBind's backing storage.
func DecodePhysAddress(vb VarBind) ([]byte, error) {
	if IsException(vb) {
		return nil, exceptionError(vb)
	}
	os, ok := vb.(OctetStringVar)
	if !ok {
		return nil, typeMismatchError(vb, "OctetString")
	}
	out := make([]byte, len(os.Value))
	copy(out, os.Value)
	return out, nil
}

// DecodeBITS decodes the SMIv2 BITS textual convention. The convention is
// encoded on the wire as an OCTET STRING whose bits are big-endian indexed
// from the leftmost bit of the first octet. Higher-level callers are
// responsible for mapping bit indices to MIB-defined names; this decoder
// returns the raw bytes verbatim (defensive copy).
func DecodeBITS(vb VarBind) ([]byte, error) {
	if IsException(vb) {
		return nil, exceptionError(vb)
	}
	os, ok := vb.(OctetStringVar)
	if !ok {
		return nil, typeMismatchError(vb, "OctetString")
	}
	out := make([]byte, len(os.Value))
	copy(out, os.Value)
	return out, nil
}
