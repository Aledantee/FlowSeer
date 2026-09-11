// Package igmp encodes and decodes Internet Group Management Protocol messages.
package igmp

import (
	"encoding/binary"
	"errors"
	"math"
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ip"
)

const maxWireLength = math.MaxUint16 - ip.V4HeaderLen

// Version identifies the query wire format.
type Version uint8

const (
	// V2 selects the eight-octet IGMPv2 query format.
	V2 Version = 2
	// V3 selects the variable-length IGMPv3 query format.
	V3 Version = 3
)

// Type identifies an IGMP message format.
type Type uint8

const (
	// Query is a Membership Query.
	Query Type = 0x11
	// ReportV1 is a Version 1 Membership Report.
	ReportV1 Type = 0x12
	// ReportV2 is a Version 2 Membership Report.
	ReportV2 Type = 0x16
	// Leave is a Leave Group message.
	Leave Type = 0x17
	// ReportV3 is a Version 3 Membership Report.
	ReportV3 Type = 0x22
)

// RecordType identifies the state transition described by an IGMPv3 group record.
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

// GroupRecord is one multicast group entry in an IGMPv3 report.
type GroupRecord struct {
	Type    RecordType
	Group   netip.Addr
	Sources []netip.Addr
}

// Message is an IGMP query, report, or leave message. Version selects the query
// layout; message types whose wire type implies a version ignore it.
type Message struct {
	Version  Version
	Type     Type
	MaxResp  time.Duration
	Group    netip.Addr
	Records  []GroupRecord
	Sources  []netip.Addr
	Suppress bool
	QRV      uint8
	QQIC     uint8
}

var (
	// ErrMalformed identifies invalid fields, lengths, and checksums.
	ErrMalformed = errors.New("malformed IGMP message")
	// ErrUnsupported identifies well-formed wire formats the package does not support.
	ErrUnsupported = errors.New("unsupported IGMP message")
)

// Encode serializes m and writes its RFC 1071 checksum. It returns
// [ErrMalformed] for invalid fields and [ErrUnsupported] for unknown formats.
func Encode(m Message) ([]byte, error) {
	var (
		wire []byte
		err  error
	)

	switch m.Type {
	case Query:
		wire, err = encodeQuery(m)
	case ReportV1, ReportV2, Leave:
		wire, err = encodeLegacy(m)
	case ReportV3:
		wire, err = encodeReportV3(m)
	default:
		return nil, errs.From(ErrUnsupported).
			Attr("type", m.Type).
			Msg("unsupported IGMP message type")
	}
	if err != nil {
		return nil, err
	}

	binary.BigEndian.PutUint16(wire[2:4], checksum(wire))
	return wire, nil
}

func encodeQuery(m Message) ([]byte, error) {
	group, err := encodeQueryGroup(m.Group, len(m.Sources) > 0)
	if err != nil {
		return nil, err
	}

	switch m.Version {
	case V2:
		code, err := encodeLinearTimer(m.MaxResp, 100*time.Millisecond, math.MaxUint8)
		if err != nil {
			return nil, err
		}
		if len(m.Sources) != 0 {
			return nil, errs.From(ErrMalformed).
				Attr("field", "sources").
				Attr("count", len(m.Sources)).
				Msg("IGMPv2 query cannot carry sources")
		}

		wire := make([]byte, 8)
		wire[0] = byte(Query)
		wire[1] = byte(code)
		copy(wire[4:8], group[:])
		return wire, nil
	case V3:
		if m.QRV > 7 {
			return nil, errs.From(ErrMalformed).
				Attr("field", "qrv").
				Attr("value", m.QRV).
				Attr("max", 7).
				Msg("IGMPv3 QRV exceeds its three-bit field")
		}
		if len(m.Sources) > math.MaxUint16 {
			return nil, errs.From(ErrMalformed).
				Attr("field", "sources").
				Attr("count", len(m.Sources)).
				Attr("max", math.MaxUint16).
				Msg("IGMPv3 source count exceeds its wire field")
		}

		length := 12 + 4*len(m.Sources)
		if length > maxWireLength {
			return nil, errs.From(ErrMalformed).
				Attr("length", length).
				Attr("max", maxWireLength).
				Msg("IGMPv3 query exceeds the IPv4 payload limit")
		}
		code, err := encodeFloatingTimer(m.MaxResp)
		if err != nil {
			return nil, err
		}

		wire := make([]byte, length)
		wire[0] = byte(Query)
		wire[1] = code
		copy(wire[4:8], group[:])
		wire[8] = m.QRV
		if m.Suppress {
			wire[8] |= 0x08
		}
		wire[9] = m.QQIC
		binary.BigEndian.PutUint16(wire[10:12], uint16(len(m.Sources)))
		if err := encodeSources(wire[12:], m.Sources); err != nil {
			return nil, err
		}
		return wire, nil
	default:
		return nil, errs.From(ErrUnsupported).
			Attr("version", m.Version).
			Msg("unsupported IGMP query version")
	}
}

func encodeLegacy(m Message) ([]byte, error) {
	if !m.Group.Is4() || !m.Group.IsMulticast() {
		return nil, errs.From(ErrMalformed).
			Attr("field", "group").
			Attr("group", m.Group).
			Msg("legacy IGMP message requires an IPv4 multicast group")
	}

	wire := make([]byte, 8)
	wire[0] = byte(m.Type)
	group := m.Group.As4()
	copy(wire[4:8], group[:])
	return wire, nil
}

func encodeReportV3(m Message) ([]byte, error) {
	if len(m.Records) > math.MaxUint16 {
		return nil, errs.From(ErrMalformed).
			Attr("field", "records").
			Attr("count", len(m.Records)).
			Attr("max", math.MaxUint16).
			Msg("IGMPv3 record count exceeds its wire field")
	}

	length := 8
	for i, record := range m.Records {
		if !validRecordType(record.Type) {
			return nil, errs.From(ErrMalformed).
				Attr("field", "record_type").
				Attr("record", i).
				Attr("type", record.Type).
				Msg("IGMPv3 record has an invalid type")
		}
		if !record.Group.Is4() || !record.Group.IsMulticast() {
			return nil, errs.From(ErrMalformed).
				Attr("field", "group").
				Attr("record", i).
				Attr("group", record.Group).
				Msg("IGMPv3 record requires an IPv4 multicast group")
		}
		if len(record.Sources) > math.MaxUint16 {
			return nil, errs.From(ErrMalformed).
				Attr("field", "sources").
				Attr("record", i).
				Attr("count", len(record.Sources)).
				Attr("max", math.MaxUint16).
				Msg("IGMPv3 record source count exceeds its wire field")
		}

		recordLength := 8 + 4*len(record.Sources)
		if recordLength > maxWireLength-length {
			return nil, errs.From(ErrMalformed).
				Attr("length", length+recordLength).
				Attr("max", maxWireLength).
				Msg("IGMPv3 report exceeds the IPv4 payload limit")
		}
		length += recordLength
	}

	wire := make([]byte, length)
	wire[0] = byte(ReportV3)
	binary.BigEndian.PutUint16(wire[6:8], uint16(len(m.Records)))
	offset := 8
	for _, record := range m.Records {
		wire[offset] = byte(record.Type)
		binary.BigEndian.PutUint16(wire[offset+2:offset+4], uint16(len(record.Sources)))
		group := record.Group.As4()
		copy(wire[offset+4:offset+8], group[:])
		if err := encodeSources(wire[offset+8:], record.Sources); err != nil {
			return nil, err
		}
		offset += 8 + 4*len(record.Sources)
	}
	return wire, nil
}

func encodeQueryGroup(group netip.Addr, sources bool) ([4]byte, error) {
	if !group.IsValid() {
		if sources {
			return [4]byte{}, errs.From(ErrMalformed).
				Attr("field", "group").
				Msg("source-specific IGMP query requires a multicast group")
		}
		return [4]byte{}, nil
	}
	if !group.Is4() || (!group.IsUnspecified() && !group.IsMulticast()) {
		return [4]byte{}, errs.From(ErrMalformed).
			Attr("field", "group").
			Attr("group", group).
			Msg("IGMP query group must be unspecified or IPv4 multicast")
	}
	if sources && group.IsUnspecified() {
		return [4]byte{}, errs.From(ErrMalformed).
			Attr("field", "group").
			Msg("source-specific IGMP query requires a multicast group")
	}
	return group.As4(), nil
}

func encodeSources(dst []byte, sources []netip.Addr) error {
	for i, source := range sources {
		if !validSource(source) {
			return errs.From(ErrMalformed).
				Attr("field", "source").
				Attr("source", i).
				Attr("address", source).
				Msg("IGMP source must be an IPv4 unicast address")
		}
		address := source.As4()
		copy(dst[i*4:(i+1)*4], address[:])
	}
	return nil
}

func encodeLinearTimer(d, unit time.Duration, maxUnits uint64) (uint64, error) {
	if d < 0 || d%unit != 0 || uint64(d/unit) > maxUnits {
		return 0, errs.From(ErrMalformed).
			Attr("field", "max_resp").
			Attr("duration", d).
			Msg("IGMP response time has no exact wire representation")
	}
	return uint64(d / unit), nil
}

func encodeFloatingTimer(d time.Duration) (uint8, error) {
	units, err := encodeLinearTimer(d, 100*time.Millisecond, math.MaxUint64)
	if err != nil {
		return 0, err
	}
	if units < 128 {
		return uint8(units), nil
	}
	for exp := range 8 {
		factor := uint64(1) << (exp + 3)
		if units%factor != 0 {
			continue
		}
		mantissa := units / factor
		if mantissa >= 0x10 && mantissa <= 0x1f {
			return 0x80 | uint8(exp<<4) | uint8(mantissa-0x10), nil
		}
	}
	return 0, errs.From(ErrMalformed).
		Attr("field", "max_resp").
		Attr("duration", d).
		Msg("IGMP response time has no exact IGMPv3 code")
}

// Decode parses payload after its IPv4 header and verifies the checksum. It
// returns [ErrMalformed] for invalid wire data and [ErrUnsupported] for unknown
// message types or versions.
func Decode(payload []byte) (Message, error) {
	if len(payload) < 8 {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(payload)).
			Attr("min", 8).
			Msg("IGMP message is shorter than eight octets")
	}
	if checksum(payload) != 0 {
		return Message{}, errs.From(ErrMalformed).
			Msg("IGMP checksum does not match")
	}

	typ := Type(payload[0])
	switch typ {
	case Query:
		return decodeQuery(payload)
	case ReportV1, ReportV2, Leave:
		return decodeLegacy(typ, payload)
	case ReportV3:
		return decodeReportV3(payload)
	default:
		return Message{}, errs.From(ErrUnsupported).
			Attr("type", typ).
			Msg("unsupported IGMP message type")
	}
}

func decodeQuery(payload []byte) (Message, error) {
	group, err := decodeQueryGroup(payload[4:8])
	if err != nil {
		return Message{}, err
	}
	if len(payload) == 8 {
		return Message{
			Version: V2,
			Type:    Query,
			MaxResp: time.Duration(payload[1]) * 100 * time.Millisecond,
			Group:   group,
		}, nil
	}
	if len(payload) < 12 {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(payload)).
			Attr("min", 12).
			Msg("IGMPv3 query is shorter than twelve octets")
	}

	count := int(binary.BigEndian.Uint16(payload[10:12]))
	required := 12 + 4*count
	if len(payload) < required {
		return Message{}, errs.From(ErrMalformed).
			Attr("length", len(payload)).
			Attr("required", required).
			Attr("sources", count).
			Msg("IGMPv3 query source count exceeds its body")
	}
	sources, err := decodeSources(payload[12:required], count)
	if err != nil {
		return Message{}, err
	}
	if count > 0 && !group.IsValid() {
		return Message{}, errs.From(ErrMalformed).
			Attr("field", "group").
			Msg("source-specific IGMP query requires a multicast group")
	}

	return Message{
		Version:  V3,
		Type:     Query,
		MaxResp:  decodeFloatingTimer(payload[1]),
		Group:    group,
		Sources:  sources,
		Suppress: payload[8]&0x08 != 0,
		QRV:      payload[8] & 0x07,
		QQIC:     payload[9],
	}, nil
}

func decodeLegacy(typ Type, payload []byte) (Message, error) {
	group := netip.AddrFrom4([4]byte(payload[4:8]))
	if !group.IsMulticast() {
		return Message{}, errs.From(ErrMalformed).
			Attr("field", "group").
			Attr("group", group).
			Msg("legacy IGMP message requires a multicast group")
	}
	return Message{Type: typ, Group: group}, nil
}

func decodeReportV3(payload []byte) (Message, error) {
	count := int(binary.BigEndian.Uint16(payload[6:8]))
	records := make([]GroupRecord, 0, min(count, (len(payload)-8)/8))
	offset := 8
	for i := range count {
		if len(payload)-offset < 8 {
			return Message{}, errs.From(ErrMalformed).
				Attr("record", i).
				Attr("remaining", len(payload)-offset).
				Attr("min", 8).
				Msg("IGMPv3 group record header is truncated")
		}

		sourceCount := int(binary.BigEndian.Uint16(payload[offset+2 : offset+4]))
		auxLength := int(payload[offset+1]) * 4
		recordLength := 8 + 4*sourceCount + auxLength
		if len(payload)-offset < recordLength {
			return Message{}, errs.From(ErrMalformed).
				Attr("record", i).
				Attr("remaining", len(payload)-offset).
				Attr("required", recordLength).
				Msg("IGMPv3 group record length exceeds its body")
		}

		typ := RecordType(payload[offset])
		if validRecordType(typ) {
			group := netip.AddrFrom4([4]byte(payload[offset+4 : offset+8]))
			if !group.IsMulticast() {
				return Message{}, errs.From(ErrMalformed).
					Attr("field", "group").
					Attr("record", i).
					Attr("group", group).
					Msg("IGMPv3 record requires a multicast group")
			}
			sources, err := decodeSources(payload[offset+8:offset+8+4*sourceCount], sourceCount)
			if err != nil {
				return Message{}, err
			}
			records = append(records, GroupRecord{Type: typ, Group: group, Sources: sources})
		}
		offset += recordLength
	}
	return Message{Type: ReportV3, Records: records}, nil
}

func decodeQueryGroup(b []byte) (netip.Addr, error) {
	if b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] == 0 {
		return netip.Addr{}, nil
	}
	group := netip.AddrFrom4([4]byte(b))
	if !group.IsMulticast() {
		return netip.Addr{}, errs.From(ErrMalformed).
			Attr("field", "group").
			Attr("group", group).
			Msg("IGMP query group must be zero or multicast")
	}
	return group, nil
}

func decodeSources(b []byte, count int) ([]netip.Addr, error) {
	sources := make([]netip.Addr, count)
	for i := range count {
		source := netip.AddrFrom4([4]byte(b[i*4 : (i+1)*4]))
		if !validSource(source) {
			return nil, errs.From(ErrMalformed).
				Attr("field", "source").
				Attr("source", i).
				Attr("address", source).
				Msg("IGMP source must be a unicast address")
		}
		sources[i] = source
	}
	return sources, nil
}

func decodeFloatingTimer(code uint8) time.Duration {
	if code < 128 {
		return time.Duration(code) * 100 * time.Millisecond
	}
	exp := (code & 0x70) >> 4
	mantissa := uint32(code&0x0f) | 0x10
	units := mantissa << (exp + 3)
	return time.Duration(units) * 100 * time.Millisecond
}

func validRecordType(typ RecordType) bool {
	return typ >= ModeIsInclude && typ <= BlockOldSources
}

func validSource(source netip.Addr) bool {
	return source.Is4() && !source.IsUnspecified() && !source.IsMulticast() && source.As4() != [4]byte{255, 255, 255, 255}
}

func checksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	for sum > math.MaxUint16 {
		sum = sum&math.MaxUint16 + sum>>16
	}
	return ^uint16(sum)
}
