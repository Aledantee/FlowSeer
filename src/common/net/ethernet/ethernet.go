// Package ethernet provides an Ethernet II frame codec, tag stack support, and EtherType constants.
package ethernet

import (
	"encoding/binary"
	"fmt"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// EtherType represents a 16-bit IEEE EtherType identifier in network byte order.
// The zero value represents an unspecified EtherType.
type EtherType uint16

const (
	// EtherTypeUnspecified indicates no EtherType was reported. Zero is not a valid EtherType on the wire.
	EtherTypeUnspecified EtherType = 0

	// EtherTypeIPv4 is Internet Protocol version 4 (0x0800).
	EtherTypeIPv4 EtherType = 0x0800

	// EtherTypeARP is Address Resolution Protocol (0x0806).
	EtherTypeARP EtherType = 0x0806

	// EtherTypeDot1Q is IEEE Std 802.1Q customer VLAN tag, the C-Tag (0x8100).
	EtherTypeDot1Q EtherType = 0x8100

	// EtherTypeIPv6 is Internet Protocol version 6 (0x86DD).
	EtherTypeIPv6 EtherType = 0x86DD

	// EtherTypeSlowProtocols is Slow Protocols (0x8809), IEEE 802.3 clause 57, carrying LACP.
	EtherTypeSlowProtocols EtherType = 0x8809

	// EtherTypeMPLSUnicast is MPLS with a downstream-assigned label (0x8847).
	EtherTypeMPLSUnicast EtherType = 0x8847

	// EtherTypeMPLSMulticast is MPLS with an upstream-assigned label (0x8848).
	EtherTypeMPLSMulticast EtherType = 0x8848

	// EtherTypeProviderBridging is IEEE Std 802.1Q service VLAN tag, the S-Tag (0x88A8).
	EtherTypeProviderBridging EtherType = 0x88A8

	// EtherTypeLLDP is Link Layer Discovery Protocol (0x88CC).
	EtherTypeLLDP EtherType = 0x88CC

	// EtherTypeMACsec is IEEE Std 802.1AE MAC Security (0x88E5).
	EtherTypeMACsec EtherType = 0x88E5
)

// String returns the hexadecimal representation of e or its known standard name.
func (e EtherType) String() string {
	switch e {
	case EtherTypeIPv4:
		return "IPv4 (0x0800)"
	case EtherTypeARP:
		return "ARP (0x0806)"
	case EtherTypeDot1Q:
		return "Dot1Q (0x8100)"
	case EtherTypeIPv6:
		return "IPv6 (0x86dd)"
	case EtherTypeSlowProtocols:
		return "SlowProtocols (0x8809)"
	case EtherTypeMPLSUnicast:
		return "MPLS Unicast (0x8847)"
	case EtherTypeMPLSMulticast:
		return "MPLS Multicast (0x8848)"
	case EtherTypeProviderBridging:
		return "Provider Bridging (0x88a8)"
	case EtherTypeLLDP:
		return "LLDP (0x88cc)"
	case EtherTypeMACsec:
		return "MACsec (0x88e5)"
	default:
		return fmt.Sprintf("EtherType(0x%04x)", uint16(e))
	}
}

// Frame represents an Ethernet II frame carrying an optional IEEE 802.1Q tag stack.
// Tags are ordered outermost first. EtherType is the first non-tag EtherType identifying the payload.
// The zero value is a usable empty frame.
type Frame struct {
	Dst       netaddr.MAC
	Src       netaddr.MAC
	Tags      []vlan.Tag
	EtherType EtherType
	Payload   []byte
}

// Encode serializes f into its Ethernet II byte representation with its tag stack.
// A tag with a zero TPID is written as a C-Tag (0x8100). A tag whose TPID is neither
// 0x8100 nor 0x88A8 is rejected, because [Decode] would not peel it and the pair would
// stop being inverses.
func (f Frame) Encode() ([]byte, error) {
	for i, tag := range f.Tags {
		if tag.TPID != 0 && tag.TPID != uint16(EtherTypeDot1Q) && tag.TPID != uint16(EtherTypeProviderBridging) {
			return nil, errs.New().
				Attr("index", i).
				Attr("tpid", tag.TPID).
				Msg("tag protocol identifier is not a VLAN tag")
		}
		if tag.VID > 0x0FFF {
			return nil, errs.New().
				Attr("index", i).
				Attr("vid", tag.VID).
				Msg("vlan identifier exceeds 12 bits")
		}
		if tag.PCP > 7 {
			return nil, errs.New().
				Attr("index", i).
				Attr("pcp", tag.PCP).
				Msg("priority code point exceeds 3 bits")
		}
	}

	totalLen := 12 + len(f.Tags)*4 + 2 + len(f.Payload)
	out := make([]byte, totalLen)

	copy(out[0:6], f.Dst[:])
	copy(out[6:12], f.Src[:])

	offset := 12
	for _, tag := range f.Tags {
		tpid := tag.TPID
		if tpid == 0 {
			tpid = uint16(EtherTypeDot1Q)
		}
		binary.BigEndian.PutUint16(out[offset:offset+2], tpid)
		offset += 2

		tci := (uint16(tag.PCP&0x07) << 13) | (uint16(tag.VID) & 0x0FFF)
		if tag.DEI {
			tci |= 0x1000
		}
		binary.BigEndian.PutUint16(out[offset:offset+2], tci)
		offset += 2
	}

	binary.BigEndian.PutUint16(out[offset:offset+2], uint16(f.EtherType))
	offset += 2

	copy(out[offset:], f.Payload)

	return out, nil
}

// Decode decodes an Ethernet II frame from wire bytes, peeling 802.1Q tags while the
// EtherType is 0x8100 (C-Tag) or 0x88A8 (S-Tag). It returns an error if the frame is
// shorter than the minimum Ethernet header (14 bytes) or if a tag is truncated.
// The returned Payload aliases b: a caller that reuses or rewrites b afterwards
// changes the frame, and one that keeps the frame copies the payload first.
func Decode(b []byte) (Frame, error) {
	if len(b) < 14 {
		return Frame{}, errs.New().
			Attr("have", len(b)).
			Attr("min", 14).
			Msg("frame length too short")
	}

	var f Frame
	copy(f.Dst[:], b[0:6])
	copy(f.Src[:], b[6:12])

	currentEtherType := binary.BigEndian.Uint16(b[12:14])
	offset := 14

	for currentEtherType == uint16(EtherTypeDot1Q) || currentEtherType == uint16(EtherTypeProviderBridging) {
		if len(b)-offset < 4 {
			return Frame{}, errs.New().
				Attr("offset", offset).
				Attr("remaining", len(b)-offset).
				Msg("truncated 802.1Q tag")
		}

		tci := binary.BigEndian.Uint16(b[offset : offset+2])
		offset += 2

		tag := vlan.Tag{
			TPID: currentEtherType,
			PCP:  vlan.PCP((tci >> 13) & 0x07),
			DEI:  (tci & 0x1000) != 0,
			VID:  vlan.ID(tci & 0x0FFF),
		}
		f.Tags = append(f.Tags, tag)

		currentEtherType = binary.BigEndian.Uint16(b[offset : offset+2])
		offset += 2
	}

	f.EtherType = EtherType(currentEtherType)
	f.Payload = b[offset:]

	return f, nil
}

// Priority returns the PCP and DEI carried by f's outer 802.1Q tag. An untagged frame
// returns (0, false). A tag whose TPID is neither zero (the repository's convention for
// an unspecified TPID, treated as 802.1Q) nor [EtherTypeDot1Q] — a provider S-Tag
// (0x88A8) among others — also returns (0, false); only an outer C-Tag is consulted.
func (f Frame) Priority() (vlan.PCP, bool) {
	if len(f.Tags) == 0 {
		return 0, false
	}
	outer := f.Tags[0]
	if outer.TPID != 0 && outer.TPID != uint16(EtherTypeDot1Q) {
		return 0, false
	}

	return outer.PCP, outer.DEI
}

// IsReserved reports whether mac is in the IEEE standard reserved bridge address
// range (01:80:c2:00:00:00 through 01:80:c2:00:00:0f inclusive).
// Frames matching this range are neither forwarded nor learned by compliant bridges.
func IsReserved(mac netaddr.MAC) bool {
	return mac[0] == 0x01 &&
		mac[1] == 0x80 &&
		mac[2] == 0xc2 &&
		mac[3] == 0x00 &&
		mac[4] == 0x00 &&
		mac[5] <= 0x0f
}
