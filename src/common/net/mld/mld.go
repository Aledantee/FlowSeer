// Package mld encodes and decodes Multicast Listener Discovery messages.
package mld

import (
	"encoding/binary"
	"errors"
	"math"
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ip"
)

const (
	icmpv6Protocol              = 58
	minimumHopByHopHeaderLength = 8
	maxWireLength               = math.MaxUint16 - minimumHopByHopHeaderLength
)

// Version identifies the query wire format.
type Version uint8

const (
	// V1 selects the fixed-length MLDv1 query format.
	V1 Version = 1
	// V2 selects the variable-length MLDv2 query format.
	V2 Version = 2
)

// Type identifies an MLD message format.
type Type uint8

const (
	// Query is a Multicast Listener Query.
	Query Type = 130
	// ReportV1 is a Version 1 Multicast Listener Report.
	ReportV1 Type = 131
	// Done is a Multicast Listener Done message.
	Done Type = 132
	// ReportV2 is a Version 2 Multicast Listener Report.
	ReportV2 Type = 143
)

// RecordType identifies the state transition described by an MLDv2 address record.
type RecordType uint8

const (
	// ModeIsInclude reports the current INCLUDE filter state.
	ModeIsInclude RecordType = 1
	// ModeIsExclude reports the current EXCLUDE filter state.
	ModeIsExclude RecordType = 2
	// ChangeToIncludeMode reports a transition to INCLUDE filter mode.
	ChangeToIncludeMode RecordType = 3
	// ChangeToExcludeMode reports a transition to EXCLUDE filter mode.
	ChangeToExcludeMode RecordType = 4
	// AllowNewSources reports newly accepted sources.
	AllowNewSources RecordType = 5
	// BlockOldSources reports newly blocked sources.
	BlockOldSources RecordType = 6
)

// AddressRecord is one multicast address entry in an MLDv2 report.
type AddressRecord struct {
	Type    RecordType
	Group   netip.Addr
	Sources []netip.Addr
}

// Message is an MLD query, report, or done message. Version selects the query
// layout; message types whose wire type implies a version ignore it.
type Message struct {
	Version  Version
	Type     Type
	MaxResp  time.Duration
	Group    netip.Addr
	Records  []AddressRecord
	Sources  []netip.Addr
	Suppress bool
	QRV      uint8
	QQIC     uint8
}

var (
	// ErrMalformed identifies invalid fields, lengths, extension headers, and checksums.
	ErrMalformed = errors.New("malformed MLD message")
	// ErrUnsupported identifies well-formed wire formats the package does not support.
	ErrUnsupported = errors.New("unsupported MLD message")
)

// Encode serializes m as an ICMPv6 message and writes its checksum using hdr's
// IPv6 addresses. The result does not include a Hop-by-Hop header and is limited
// so an eight-octet minimum Hop-by-Hop header fits in the IPv6 payload. Encode
// returns [ErrMalformed] for invalid fields and [ErrUnsupported] for unknown formats.
func Encode(hdr ip.Header, m Message) ([]byte, error) {
	if err := validateIPv6Header(hdr); err != nil {
		return nil, err
	}

	var (
		wire []byte
		err  error
	)
	switch m.Type {
	case Query:
		wire, err = encodeQuery(m)
	case ReportV1, Done:
		wire, err = encodeLegacy(m)
	case ReportV2:
		wire, err = encodeReportV2(m)
	default:
		return nil, errs.From(ErrUnsupported).
			Attr("type", m.Type).
			Msg("unsupported MLD message type")
	}
	if err != nil {
		return nil, err
	}

	binary.BigEndian.PutUint16(wire[2:4], checksum(hdr, wire))
	return wire, nil
}

func encodeQuery(m Message) ([]byte, error) {
	group, err := encodeQueryGroup(m.Group, len(m.Sources) > 0)
	if err != nil {
		return nil, err
	}

	switch m.Version {
	case V1:
		code, err := encodeLinearTimer(m.MaxResp, time.Millisecond, math.MaxUint16)
		if err != nil {
			return nil, err
		}
		if len(m.Sources) != 0 {
			return nil, errs.From(ErrMalformed).
				Attr("field", "sources").
				Attr("count", len(m.Sources)).
				Msg("MLDv1 query cannot carry sources")
		}

		wire := make([]byte, 24)
		wire[0] = byte(Query)
		binary.BigEndian.PutUint16(wire[4:6], uint16(code))
		copy(wire[8:24], group[:])
		return wire, nil
	case V2:
		if m.QRV > 7 {
			return nil, errs.From(ErrMalformed).
				Attr("field", "qrv").
				Attr("value", m.QRV).
				Attr("max", 7).
				Msg("MLDv2 QRV exceeds its three-bit field")
		}
		if len(m.Sources) > math.MaxUint16 {
			return nil, errs.From(ErrMalformed).
				Attr("field", "sources").
				Attr("count", len(m.Sources)).
				Attr("max", math.MaxUint16).
				Msg("MLDv2 source count exceeds its wire field")
		}

		length := 28 + 16*len(m.Sources)
		if length > maxWireLength {
			return nil, errs.From(ErrMalformed).
				Attr("length", length).
				Attr("max", maxWireLength).
				Msg("MLDv2 query exceeds the IPv6 payload limit")
		}
		code, err := encodeFloatingTimer(m.MaxResp)
		if err != nil {
			return nil, err
		}

		wire := make([]byte, length)
		wire[0] = byte(Query)
		binary.BigEndian.PutUint16(wire[4:6], code)
		copy(wire[8:24], group[:])
		wire[24] = m.QRV
		if m.Suppress {
			wire[24] |= 0x08
		}
		wire[25] = m.QQIC
		binary.BigEndian.PutUint16(wire[26:28], uint16(len(m.Sources)))
		if err := encodeSources(wire[28:], m.Sources); err != nil {
			return nil, err
		}
		return wire, nil
	default:
		return nil, errs.From(ErrUnsupported).
			Attr("version", m.Version).
			Msg("unsupported MLD query version")
	}
}

func encodeLegacy(m Message) ([]byte, error) {
	if !validGroup(m.Group) {
		return nil, errs.From(ErrMalformed).
			Attr("field", "group").
			Attr("group", m.Group).
			Msg("legacy MLD message requires an IPv6 multicast group")
	}

	wire := make([]byte, 24)
	wire[0] = byte(m.Type)
	group := m.Group.As16()
	copy(wire[8:24], group[:])
	return wire, nil
}

func encodeReportV2(m Message) ([]byte, error) {
	if len(m.Records) > math.MaxUint16 {
		return nil, errs.From(ErrMalformed).
			Attr("field", "records").
			Attr("count", len(m.Records)).
			Attr("max", math.MaxUint16).
			Msg("MLDv2 record count exceeds its wire field")
	}

	length := 8
	for i, record := range m.Records {
		if !validRecordType(record.Type) {
			return nil, errs.From(ErrMalformed).
				Attr("field", "record_type").
				Attr("record", i).
				Attr("type", record.Type).
				Msg("MLDv2 record has an invalid type")
		}
		if !validGroup(record.Group) {
			return nil, errs.From(ErrMalformed).
				Attr("field", "group").
				Attr("record", i).
				Attr("group", record.Group).
				Msg("MLDv2 record requires an IPv6 multicast group")
		}
		if len(record.Sources) > math.MaxUint16 {
			return nil, errs.From(ErrMalformed).
				Attr("field", "sources").
				Attr("record", i).
				Attr("count", len(record.Sources)).
				Attr("max", math.MaxUint16).
				Msg("MLDv2 record source count exceeds its wire field")
		}

		recordLength := 20 + 16*len(record.Sources)
		if recordLength > maxWireLength-length {
			return nil, errs.From(ErrMalformed).
				Attr("length", length+recordLength).
				Attr("max", maxWireLength).
				Msg("MLDv2 report exceeds the IPv6 payload limit")
		}
		length += recordLength
	}

	wire := make([]byte, length)
	wire[0] = byte(ReportV2)
	binary.BigEndian.PutUint16(wire[6:8], uint16(len(m.Records)))
	offset := 8
	for _, record := range m.Records {
		wire[offset] = byte(record.Type)
		binary.BigEndian.PutUint16(wire[offset+2:offset+4], uint16(len(record.Sources)))
		group := record.Group.As16()
		copy(wire[offset+4:offset+20], group[:])
		if err := encodeSources(wire[offset+20:], record.Sources); err != nil {
			return nil, err
		}
		offset += 20 + 16*len(record.Sources)
	}
	return wire, nil
}

func encodeQueryGroup(group netip.Addr, sources bool) ([16]byte, error) {
	if !group.IsValid() {
		if sources {
			return [16]byte{}, errs.From(ErrMalformed).
				Attr("field", "group").
				Msg("source-specific MLD query requires a multicast group")
		}
		return [16]byte{}, nil
	}
	if !pureIPv6(group) || (!group.IsUnspecified() && !group.IsMulticast()) {
		return [16]byte{}, errs.From(ErrMalformed).
			Attr("field", "group").
			Attr("group", group).
			Msg("MLD query group must be unspecified or IPv6 multicast")
	}
	if sources && group.IsUnspecified() {
		return [16]byte{}, errs.From(ErrMalformed).
			Attr("field", "group").
			Msg("source-specific MLD query requires a multicast group")
	}
	return group.As16(), nil
}

func encodeSources(dst []byte, sources []netip.Addr) error {
	for i, source := range sources {
		if !validSource(source) {
			return errs.From(ErrMalformed).
				Attr("field", "source").
				Attr("source", i).
				Attr("address", source).
				Msg("MLD source must be an IPv6 unicast address")
		}
		address := source.As16()
		copy(dst[i*16:(i+1)*16], address[:])
	}
	return nil
}

func encodeLinearTimer(d, unit time.Duration, maxUnits uint64) (uint64, error) {
	if d < 0 || d%unit != 0 || uint64(d/unit) > maxUnits {
		return 0, errs.From(ErrMalformed).
			Attr("field", "max_resp").
			Attr("duration", d).
			Msg("MLD response time has no exact wire representation")
	}
	return uint64(d / unit), nil
}

func encodeFloatingTimer(d time.Duration) (uint16, error) {
	units, err := encodeLinearTimer(d, time.Millisecond, math.MaxUint64)
	if err != nil {
		return 0, err
	}
	if units < 32768 {
		return uint16(units), nil
	}
	for exp := range 8 {
		factor := uint64(1) << (exp + 3)
		if units%factor != 0 {
			continue
		}
		mantissa := units / factor
		if mantissa >= 0x1000 && mantissa <= 0x1fff {
			return 0x8000 | uint16(exp<<12) | uint16(mantissa-0x1000), nil
		}
	}
	return 0, errs.From(ErrMalformed).
		Attr("field", "max_resp").
		Attr("duration", d).
		Msg("MLD response time has no exact MLDv2 code")
}

// Decode parses an MLD message from payload and verifies its ICMPv6 checksum.
// When hdr.Protocol is zero, payload starts with one Hop-by-Hop header whose
// encoded length locates the ICMPv6 suffix. Decode returns [ErrMalformed] for
// invalid wire data and [ErrUnsupported] for unknown message types or versions.
func Decode(hdr ip.Header, payload []byte) (Message, error) {
	if err := validateIPv6Header(hdr); err != nil {
		return Message{}, err
	}
	suffix, err := icmpv6Suffix(hdr.Protocol, payload)
	if err != nil {
		return Message{}, err
	}
	if len(suffix) < 4 {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(suffix)).
			Attr("min", 4).
			Msg("MLD message is shorter than an ICMPv6 header")
	}
	if checksum(hdr, suffix) != 0 {
		return Message{}, errs.From(ErrMalformed).
			Msg("MLD checksum does not match")
	}

	typ := Type(suffix[0])
	switch typ {
	case Query:
		return decodeQuery(suffix)
	case ReportV1, Done:
		return decodeLegacy(typ, suffix)
	case ReportV2:
		return decodeReportV2(suffix)
	default:
		return Message{}, errs.From(ErrUnsupported).
			Attr("type", typ).
			Msg("unsupported MLD message type")
	}
}

func validateIPv6Header(hdr ip.Header) error {
	if hdr.V6 == nil || hdr.V4 != nil || !pureIPv6(hdr.Src) || !pureIPv6(hdr.Dst) {
		return errs.From(ErrMalformed).
			Attr("field", "header").
			Attr("source", hdr.Src).
			Attr("destination", hdr.Dst).
			Msg("MLD checksum requires an IPv6 header and addresses")
	}
	return nil
}

func icmpv6Suffix(protocol uint8, payload []byte) ([]byte, error) {
	switch protocol {
	case icmpv6Protocol:
		return payload, nil
	case 0:
		if len(payload) < 2 {
			return nil, errs.From(ErrMalformed).
				Attr("length", len(payload)).
				Attr("min", 2).
				Msg("Hop-by-Hop header is truncated")
		}
		length := (int(payload[1]) + 1) * 8
		if len(payload) < length {
			return nil, errs.From(ErrMalformed).
				Attr("length", len(payload)).
				Attr("header_length", length).
				Msg("Hop-by-Hop length exceeds the IPv6 payload")
		}
		if payload[0] != icmpv6Protocol {
			return nil, errs.From(ErrMalformed).
				Attr("next_header", payload[0]).
				Attr("want", icmpv6Protocol).
				Msg("Hop-by-Hop header does not terminate at ICMPv6")
		}
		return payload[length:], nil
	default:
		return nil, errs.From(ErrMalformed).
			Attr("next_header", protocol).
			Attr("want", icmpv6Protocol).
			Msg("IPv6 header does not identify an MLD message")
	}
}

func decodeQuery(payload []byte) (Message, error) {
	if len(payload) < 24 {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(payload)).
			Attr("min", 24).
			Msg("MLD query is shorter than twenty-four octets")
	}
	group, err := decodeQueryGroup(payload[8:24])
	if err != nil {
		return Message{}, err
	}
	if len(payload) == 24 {
		return Message{
			Version: V1,
			Type:    Query,
			MaxResp: time.Duration(binary.BigEndian.Uint16(payload[4:6])) * time.Millisecond,
			Group:   group,
		}, nil
	}
	if len(payload) < 28 {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(payload)).
			Attr("min", 28).
			Msg("MLDv2 query is shorter than twenty-eight octets")
	}

	count := int(binary.BigEndian.Uint16(payload[26:28]))
	required := 28 + 16*count
	if len(payload) < required {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(payload)).
			Attr("required", required).
			Attr("sources", count).
			Msg("MLDv2 query source count exceeds its body")
	}
	sources, err := decodeSources(payload[28:required], count)
	if err != nil {
		return Message{}, err
	}
	if count > 0 && !group.IsValid() {
		return Message{}, errs.From(ErrMalformed).
			Attr("field", "group").
			Msg("source-specific MLD query requires a multicast group")
	}

	return Message{
		Version:  V2,
		Type:     Query,
		MaxResp:  decodeFloatingTimer(binary.BigEndian.Uint16(payload[4:6])),
		Group:    group,
		Sources:  sources,
		Suppress: payload[24]&0x08 != 0,
		QRV:      payload[24] & 0x07,
		QQIC:     payload[25],
	}, nil
}

func decodeLegacy(typ Type, payload []byte) (Message, error) {
	if len(payload) < 24 {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(payload)).
			Attr("min", 24).
			Msg("legacy MLD message is shorter than twenty-four octets")
	}
	group := netip.AddrFrom16([16]byte(payload[8:24]))
	if !validGroup(group) {
		return Message{}, errs.From(ErrMalformed).
			Attr("field", "group").
			Attr("group", group).
			Msg("legacy MLD message requires a multicast group")
	}
	return Message{Type: typ, Group: group}, nil
}

func decodeReportV2(payload []byte) (Message, error) {
	if len(payload) < 8 {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(payload)).
			Attr("min", 8).
			Msg("MLDv2 report is shorter than eight octets")
	}

	count := int(binary.BigEndian.Uint16(payload[6:8]))
	records := make([]AddressRecord, 0, min(count, (len(payload)-8)/20))
	offset := 8
	for i := range count {
		if len(payload)-offset < 20 {
			return Message{}, errs.From(ErrMalformed).
				Attr("record", i).
				Attr("remaining", len(payload)-offset).
				Attr("min", 20).
				Msg("MLDv2 address record header is truncated")
		}

		sourceCount := int(binary.BigEndian.Uint16(payload[offset+2 : offset+4]))
		auxLength := int(payload[offset+1]) * 4
		recordLength := 20 + 16*sourceCount + auxLength
		if len(payload)-offset < recordLength {
			return Message{}, errs.From(ErrMalformed).
				Attr("record", i).
				Attr("remaining", len(payload)-offset).
				Attr("required", recordLength).
				Msg("MLDv2 address record length exceeds its body")
		}

		typ := RecordType(payload[offset])
		if validRecordType(typ) {
			group := netip.AddrFrom16([16]byte(payload[offset+4 : offset+20]))
			if !validGroup(group) {
				return Message{}, errs.From(ErrMalformed).
					Attr("field", "group").
					Attr("record", i).
					Attr("group", group).
					Msg("MLDv2 record requires a multicast group")
			}
			sources, err := decodeSources(payload[offset+20:offset+20+16*sourceCount], sourceCount)
			if err != nil {
				return Message{}, err
			}
			records = append(records, AddressRecord{Type: typ, Group: group, Sources: sources})
		}
		offset += recordLength
	}
	return Message{Type: ReportV2, Records: records}, nil
}

func decodeQueryGroup(b []byte) (netip.Addr, error) {
	if [16]byte(b) == [16]byte{} {
		return netip.Addr{}, nil
	}
	group := netip.AddrFrom16([16]byte(b))
	if !group.IsMulticast() {
		return netip.Addr{}, errs.From(ErrMalformed).
			Attr("field", "group").
			Attr("group", group).
			Msg("MLD query group must be zero or multicast")
	}
	return group, nil
}

func decodeSources(b []byte, count int) ([]netip.Addr, error) {
	sources := make([]netip.Addr, count)
	for i := range count {
		source := netip.AddrFrom16([16]byte(b[i*16 : (i+1)*16]))
		if !validSource(source) {
			return nil, errs.From(ErrMalformed).
				Attr("field", "source").
				Attr("source", i).
				Attr("address", source).
				Msg("MLD source must be a unicast address")
		}
		sources[i] = source
	}
	return sources, nil
}

func decodeFloatingTimer(code uint16) time.Duration {
	if code < 32768 {
		return time.Duration(code) * time.Millisecond
	}
	exp := (code & 0x7000) >> 12
	mantissa := uint32(code&0x0fff) | 0x1000
	units := mantissa << (exp + 3)
	return time.Duration(units) * time.Millisecond
}

func validRecordType(typ RecordType) bool {
	return typ >= ModeIsInclude && typ <= BlockOldSources
}

func validGroup(group netip.Addr) bool {
	return pureIPv6(group) && group.IsMulticast()
}

func validSource(source netip.Addr) bool {
	return pureIPv6(source) && !source.IsUnspecified() && !source.IsMulticast()
}

func pureIPv6(address netip.Addr) bool {
	return address.Is6() && !address.Is4In6()
}

func checksum(hdr ip.Header, payload []byte) uint16 {
	var pseudo [40]byte
	source := hdr.Src.As16()
	destination := hdr.Dst.As16()
	copy(pseudo[0:16], source[:])
	copy(pseudo[16:32], destination[:])
	binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(payload)))
	pseudo[39] = icmpv6Protocol

	sum := checksumSum(0, pseudo[:])
	sum = checksumSum(sum, payload)
	for sum > math.MaxUint16 {
		sum = sum&math.MaxUint16 + sum>>16
	}
	return ^uint16(sum)
}

func checksumSum(sum uint64, b []byte) uint64 {
	for len(b) >= 2 {
		sum += uint64(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint64(b[0]) << 8
	}
	return sum
}
