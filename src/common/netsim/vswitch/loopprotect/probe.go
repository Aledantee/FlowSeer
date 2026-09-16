package loopprotect

import (
	"encoding/binary"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// ReasonUnsupportedProbe indicates that a received frame could not be
// decoded as a loop-protection probe.
const ReasonUnsupportedProbe trace.Reason = "unsupported-loopprotect-probe"

// probeVersion is the only version Encode writes and Decode accepts.
const probeVersion uint8 = 1

// probeHeaderLength is the length of the fixed-size fields ahead of the
// variable-length port name: version (1), originating MAC (6), VID (2),
// sequence number (4), port-name length (1).
const probeHeaderLength = 14

// GroupAddress is the destination address of a loop-protection probe, a
// locally administered group MAC.
var GroupAddress = netaddr.MAC{0x03, 0x46, 0x53, 0x4c, 0x50, 0x00}

// EtherType is the loop-protection probe EtherType, IEEE Std 802 Local
// Experimental EtherType 1.
const EtherType ethernet.EtherType = 0x88b5

// Probe is a decoded loop-protection probe. A probe whose payload names this
// switch as OriginMAC and this switch's own port as Port is a loop on that port.
type Probe struct {
	OriginMAC netaddr.MAC
	VID       vlan.ID
	Sequence  uint32
	Port      string
}

// Encode serializes p into an Ethernet II frame addressed to the
// loop-protection probe group, with src as the sending switch's MAC. The
// payload puts its fixed fields ahead of the variable-length port name:
// version, originating MAC, VID, sequence number, port-name length, port
// name. The frame is not padded to the 60-octet Ethernet minimum.
func Encode(p Probe, src netaddr.MAC) ethernet.Frame {
	name := []byte(p.Port)
	payload := make([]byte, probeHeaderLength+len(name))

	payload[0] = probeVersion
	copy(payload[1:7], p.OriginMAC[:])
	binary.BigEndian.PutUint16(payload[7:9], uint16(p.VID))
	binary.BigEndian.PutUint32(payload[9:13], p.Sequence)
	payload[13] = byte(len(name))
	copy(payload[probeHeaderLength:], name)

	return ethernet.Frame{
		Dst:       GroupAddress,
		Src:       src,
		EtherType: EtherType,
		Payload:   payload,
	}
}

// Decode parses f's payload as a loop-protection probe. It refuses a wrong
// version, a payload shorter than 14 octets, a zero name length, a name
// running past the payload, and trailing octets after the name.
func Decode(f ethernet.Frame) (Probe, error) {
	if len(f.Payload) < probeHeaderLength {
		return Probe{}, errs.New().
			Attr("reason", ReasonUnsupportedProbe).
			Attr("have", len(f.Payload)).
			Attr("min", probeHeaderLength).
			Msgf("loop protection probe payload length %d is too short", len(f.Payload))
	}

	version := f.Payload[0]
	if version != probeVersion {
		return Probe{}, errs.New().
			Attr("reason", ReasonUnsupportedProbe).
			Attr("version", version).
			Msgf("unsupported loop protection probe version %d, want %d", version, probeVersion)
	}

	var origin netaddr.MAC
	copy(origin[:], f.Payload[1:7])

	vid := vlan.ID(binary.BigEndian.Uint16(f.Payload[7:9]))
	seq := binary.BigEndian.Uint32(f.Payload[9:13])

	nameLen := int(f.Payload[13])
	if nameLen == 0 {
		return Probe{}, errs.New().
			Attr("reason", ReasonUnsupportedProbe).
			Msg("loop protection probe port name length is zero")
	}

	end := probeHeaderLength + nameLen
	if end > len(f.Payload) {
		return Probe{}, errs.New().
			Attr("reason", ReasonUnsupportedProbe).
			Attr("name_len", nameLen).
			Attr("payload_len", len(f.Payload)).
			Msgf("loop protection probe port name of length %d runs past the %d-octet payload", nameLen, len(f.Payload))
	}
	if end < len(f.Payload) {
		return Probe{}, errs.New().
			Attr("reason", ReasonUnsupportedProbe).
			Attr("have", len(f.Payload)).
			Attr("want", end).
			Msgf("loop protection probe payload carries %d trailing octets after the port name", len(f.Payload)-end)
	}

	return Probe{
		OriginMAC: origin,
		VID:       vid,
		Sequence:  seq,
		Port:      string(f.Payload[probeHeaderLength:end]),
	}, nil
}
