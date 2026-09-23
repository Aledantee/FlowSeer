package stream

import (
	"fmt"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/udp"
)

// Variation transforms one frame at index n. Validate checks settings that do
// not depend on the frame; [Spec.Validate] checks template-dependent settings.
// Implementations must leave their inputs unchanged and be safe to share
// between sources. Draws use the source's seeded generator.
type Variation interface {
	// Validate reports invalid settings before a source starts.
	Validate() error
	// Apply returns a frame with the selected field changed.
	Apply(n int, frame ethernet.Frame, rng *SplitMix64) ethernet.Frame
}

// MACField identifies which Ethernet address a [MACVariation] changes.
type MACField string

// MACDestination selects the destination address.
const MACDestination MACField = "destination"

// MACSource selects the source address.
const MACSource MACField = "source"

// MACVariation adds a signed step to a 48-bit Ethernet address. Count is the
// number of steps before the offset repeats. Draw uses a seeded offset in
// [0, Count) instead of the frame index and ignores Step.
type MACVariation struct {
	Field MACField
	Step  int64
	Count int
	Draw  bool
}

// Validate requires a selected address and a positive Count.
func (v MACVariation) Validate() error {
	if v.Field != MACDestination && v.Field != MACSource {
		return fmt.Errorf("MAC variation field must be destination or source")
	}
	if v.Count <= 0 {
		return fmt.Errorf("MAC variation count must be positive")
	}
	return nil
}

// Apply steps or draws the selected address, wrapping at 48 bits.
func (v MACVariation) Apply(n int, frame ethernet.Frame, rng *SplitMix64) ethernet.Frame {
	var mac *netaddr.MAC
	if v.Field == MACDestination {
		mac = &frame.Dst
	} else {
		mac = &frame.Src
	}
	var delta uint64
	if v.Draw {
		delta = rng.Next() % uint64(v.Count)
	} else {
		delta = uint64(n%v.Count) * uint64(v.Step)
	}
	value := uint64(mac[0])<<40 | uint64(mac[1])<<32 | uint64(mac[2])<<24 |
		uint64(mac[3])<<16 | uint64(mac[4])<<8 | uint64(mac[5])
	value = (value + delta) & 0xffffffffffff
	for i := len(mac) - 1; i >= 0; i-- {
		mac[i] = byte(value)
		value >>= 8
	}
	return frame
}

// SizeVariation cycles through frame sizes in octets, including the four-octet
// FCS. It preserves the template payload prefix and pads with zeroes.
type SizeVariation struct{ Sizes []int }

// Validate requires at least one size large enough for an untagged header and
// FCS. [Spec.Validate] also checks the template's VLAN tags.
func (v SizeVariation) Validate() error {
	if len(v.Sizes) == 0 {
		return fmt.Errorf("size variation requires at least one size")
	}
	for _, size := range v.Sizes {
		if size < 18 {
			return fmt.Errorf("size variation frame size %d is smaller than Ethernet header and FCS", size)
		}
	}
	return nil
}

// Apply sizes the payload so [ethernet.Frame.Encode] plus FCS has the selected
// size. Each result owns its payload buffer.
func (v SizeVariation) Apply(n int, frame ethernet.Frame, _ *SplitMix64) ethernet.Frame {
	length := v.Sizes[n%len(v.Sizes)] - 18 - 4*len(frame.Tags)
	payload := make([]byte, length)
	copy(payload, frame.Payload)
	frame.Payload = payload
	return frame
}

// UDPPortVariation steps one UDP port through Count offsets. Dst selects the
// destination port; false selects the source. Draw uses a seeded offset in
// [0, Count) instead of the frame index and ignores Step.
type UDPPortVariation struct {
	Dst   bool
	Step  int64
	Count int
	Draw  bool
}

// Validate requires a positive Count. [Spec.Validate] checks the IP/UDP
// template before the source is constructed.
func (v UDPPortVariation) Validate() error {
	if v.Count <= 0 {
		return fmt.Errorf("UDP port variation count must be positive")
	}
	return nil
}

// Apply decodes and re-encodes UDP and IP so their checksums track the changed
// port. A frame that cannot be decoded as IP/UDP is returned unchanged.
func (v UDPPortVariation) Apply(n int, frame ethernet.Frame, rng *SplitMix64) ethernet.Frame {
	ipHeader, udpHeader, payload, err := decodeUDPFrame(frame)
	if err != nil {
		return frame
	}
	var delta uint16
	if v.Draw {
		delta = uint16(rng.Next() % uint64(v.Count))
	} else {
		delta = uint16(uint64(n%v.Count) * uint64(v.Step))
	}
	if v.Dst {
		udpHeader.DstPort += delta
	} else {
		udpHeader.SrcPort += delta
	}
	datagram, err := udp.Encode(udpHeader, payload, ipHeader.Src, ipHeader.Dst)
	if err != nil {
		return frame
	}
	encoded, err := ipHeader.Encode(datagram)
	if err != nil {
		return frame
	}
	frame.Payload = encoded
	return frame
}

func decodeUDPFrame(frame ethernet.Frame) (ip.Header, udp.Header, []byte, error) {
	if frame.EtherType != ethernet.EtherTypeIPv4 && frame.EtherType != ethernet.EtherTypeIPv6 {
		return ip.Header{}, udp.Header{}, nil, fmt.Errorf("UDP variation requires IPv4 or IPv6 EtherType")
	}
	ipHeader, datagram, err := ip.Decode(frame.Payload)
	if err != nil {
		return ip.Header{}, udp.Header{}, nil, fmt.Errorf("decode IP for UDP variation: %w", err)
	}
	if ipHeader.Protocol != 17 || (frame.EtherType == ethernet.EtherTypeIPv4) != (ipHeader.V4 != nil) {
		return ip.Header{}, udp.Header{}, nil, fmt.Errorf("UDP variation requires matching IP/UDP payload")
	}
	udpHeader, payload, err := udp.Decode(datagram)
	if err != nil {
		return ip.Header{}, udp.Header{}, nil, fmt.Errorf("decode UDP for variation: %w", err)
	}
	return ipHeader, udpHeader, payload, nil
}
