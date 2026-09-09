package mirror

import (
	"encoding/binary"
	"fmt"
	"net"
	"slices"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
)

// GRE payload protocol types the ERSPAN draft and RFC 1701 assign.
// draft-foschiano-erspan-03 gives 0x88BE to both ERSPAN Type I and Type II;
// the two are told apart by the GRE sequence-number bit below, not by this
// value. ethPTypeTEB (Transparent Ethernet Bridging, RFC 1701) marks a plain
// GRE payload as an Ethernet frame; any other value is not.
const (
	greProtoERSPANTypeIOrII = 0x88be
	greProtoERSPANTypeIII   = 0x22eb
	ethPTypeTEB             = 0x6558
)

// greHeader is the base GRE header (RFC 2784) plus the optional words RFC
// 2890 adds, parsed but not the checksum RFC 2784 already has the kernel
// verify on receipt.
type greHeader struct {
	keyPresent     bool
	seqPresent     bool
	protocolType   uint16
	key            uint32
	sequenceNumber uint32
	rest           []byte
}

func parseGRE(b []byte) (*greHeader, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("GRE header truncated: %d bytes", len(b))
	}
	flags := b[0]
	if flags&0x40 != 0 { // R (routing) bit
		return nil, fmt.Errorf("GRE routing (R bit) is not supported")
	}
	h := &greHeader{
		keyPresent:   flags&0x20 != 0,
		seqPresent:   flags&0x10 != 0,
		protocolType: binary.BigEndian.Uint16(b[2:4]),
	}

	rest := b[4:]
	if flags&0x80 != 0 { // C (checksum) bit: checksum + reserved1 word
		if len(rest) < 4 {
			return nil, fmt.Errorf("GRE checksum word truncated")
		}
		rest = rest[4:]
	}
	if h.keyPresent {
		if len(rest) < 4 {
			return nil, fmt.Errorf("GRE key word truncated")
		}
		h.key = binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
	}
	if h.seqPresent {
		if len(rest) < 4 {
			return nil, fmt.Errorf("GRE sequence number word truncated")
		}
		h.sequenceNumber = binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
	}
	h.rest = rest
	return h, nil
}

func toIPAddress(ip net.IP) *addrv1.IpAddress {
	a := &addrv1.IpAddress{}
	if v4 := ip.To4(); v4 != nil {
		addr := &addrv1.Ipv4Address{}
		addr.SetOctets(append([]byte{}, v4...))
		a.SetV4(addr)
		return a
	}
	addr := &addrv1.Ipv6Address{}
	addr.SetOctets(append([]byte{}, ip.To16()...))
	a.SetV6(addr)
	return a
}

// Decode parses a GRE/ERSPAN-family packet: payload starts at the GRE
// header, and srcIP/dstIP are the outer IP addresses the caller has already
// resolved (Decode never assumes an IP-header-prefixed buffer, since an
// AF_INET6 SOCK_RAW socket does not deliver one). It returns the decoded
// envelope and the inner Ethernet frame, or a nil inner frame and no error
// when the envelope carries none: an ERSPAN Type III marker, or a plain
// GRE/ERSPAN Type I payload that is not Ethernet.
func Decode(payload []byte, srcIP, dstIP net.IP) (*capturev1.MirrorEnvelope, []byte, error) {
	gre, err := parseGRE(payload)
	if err != nil {
		return nil, nil, err
	}

	env := &capturev1.MirrorEnvelope{}
	env.SetSource(toIPAddress(srcIP))
	env.SetDestination(toIPAddress(dstIP))

	switch gre.protocolType {
	case greProtoERSPANTypeIOrII:
		if gre.seqPresent {
			fields, rest, err := ParseErspanTypeII(gre.rest)
			if err != nil {
				return nil, nil, err
			}
			env.SetErspanTypeIi(fields)
			return env, rest, nil
		}
		// No GRE sequence number: draft-foschiano-erspan-03 describes Type I
		// as GRE with no ERSPAN header at all, so the GRE payload is the
		// mirrored frame directly.
		env.SetErspanTypeI(&capturev1.ErspanTypeIFields{})
		return env, gre.rest, nil

	case greProtoERSPANTypeIII:
		fields, rest, err := ParseErspanTypeIII(gre.rest)
		if err != nil {
			return nil, nil, err
		}
		env.SetErspanTypeIii(fields)
		if !fields.GetEthernetFrame() {
			// A marker, or a mirrored frame that is not Ethernet: counted,
			// no record.
			return env, nil, nil
		}
		return env, rest, nil

	default:
		fields := &capturev1.GreFields{}
		fields.SetProtocolType(uint32(gre.protocolType))
		if gre.keyPresent {
			fields.SetKey(gre.key)
		}
		if gre.seqPresent {
			fields.SetSequenceNumber(gre.sequenceNumber)
		}
		env.SetGre(fields)
		if gre.protocolType != ethPTypeTEB {
			return env, nil, nil
		}
		return env, gre.rest, nil
	}
}

// ParseErspanTypeII parses an ERSPAN Type II header
// (draft-foschiano-erspan-03) from the start of b and returns the decoded
// fields plus whatever follows the header.
func ParseErspanTypeII(b []byte) (*capturev1.ErspanTypeIiFields, []byte, error) {
	if len(b) < 8 {
		return nil, nil, fmt.Errorf("ERSPAN Type II header truncated: %d bytes", len(b))
	}
	w0 := binary.BigEndian.Uint32(b[0:4])
	w1 := binary.BigEndian.Uint32(b[4:8])

	if ver := (w0 >> 28) & 0xF; ver != 1 {
		return nil, nil, fmt.Errorf("ERSPAN Type II header has Ver %d, want 1", ver)
	}

	f := &capturev1.ErspanTypeIiFields{}
	f.SetVlan((w0 >> 16) & 0xFFF)
	f.SetCos((w0 >> 13) & 0x7)
	f.SetEncapsulationType(capturev1.ErspanEncapsulationType((w0 >> 11) & 0x3))
	f.SetTruncated((w0>>10)&0x1 != 0)
	f.SetSessionId(w0 & 0x3FF)
	f.SetIndex(w1 & 0xFFFFF)

	return f, b[8:], nil
}

// ParseErspanTypeIII parses an ERSPAN Type III header
// (draft-foschiano-erspan-03) from the start of b and returns the decoded
// fields plus whatever follows the header (past the optional platform
// subheader, which is dropped: its layout varies by Platf ID and no
// receiver here interprets it).
func ParseErspanTypeIII(b []byte) (*capturev1.ErspanTypeIiiFields, []byte, error) {
	if len(b) < 12 {
		return nil, nil, fmt.Errorf("ERSPAN Type III header truncated: %d bytes", len(b))
	}
	w0 := binary.BigEndian.Uint32(b[0:4])
	w1 := binary.BigEndian.Uint32(b[4:8])
	w2 := binary.BigEndian.Uint32(b[8:12])

	if ver := (w0 >> 28) & 0xF; ver != 2 {
		return nil, nil, fmt.Errorf("ERSPAN Type III header has Ver %d, want 2", ver)
	}

	f := &capturev1.ErspanTypeIiiFields{}
	f.SetVlan((w0 >> 16) & 0xFFF)
	f.SetCos((w0 >> 13) & 0x7)
	f.SetBadFrame(capturev1.ErspanBadFrame((w0 >> 11) & 0x3))
	f.SetTruncated((w0>>10)&0x1 != 0)
	f.SetSessionId(w0 & 0x3FF)
	f.SetTimestamp(w1)
	f.SetSecurityGroupTag((w2 >> 16) & 0xFFFF)
	f.SetEthernetFrame((w2>>15)&0x1 != 0)
	f.SetFrameType((w2 >> 10) & 0x1F)
	f.SetHardwareId((w2 >> 4) & 0x3F)
	f.SetDirection((w2>>3)&0x1 != 0)
	f.SetTimestampGranularity(capturev1.ErspanTimestampGranularity((w2 >> 1) & 0x3))

	rest := b[12:]
	if w2&0x1 != 0 { // O bit: optional platform subheader present
		if len(rest) < 8 {
			return nil, nil, fmt.Errorf("ERSPAN Type III platform subheader truncated")
		}
		rest = rest[8:]
	}
	return f, rest, nil
}

func decodeVXLAN(b []byte) (*capturev1.VxlanFields, []byte, error) {
	if len(b) < 8 {
		return nil, nil, fmt.Errorf("VXLAN header truncated: %d bytes", len(b))
	}
	if b[0]&0x08 == 0 { // I flag: VNI valid
		return nil, nil, fmt.Errorf("VXLAN header I flag is not set: no VNI")
	}
	vni := uint32(b[4])<<16 | uint32(b[5])<<8 | uint32(b[6])
	f := &capturev1.VxlanFields{}
	f.SetVni(vni)
	return f, b[8:], nil
}

func decodeTZSP(b []byte) (*capturev1.TzspFields, []byte, error) {
	if len(b) < 4 {
		return nil, nil, fmt.Errorf("TZSP header truncated: %d bytes", len(b))
	}
	encapsulatedProtocol := binary.BigEndian.Uint16(b[2:4])

	rest := b[4:]
	for {
		if len(rest) < 1 {
			return nil, nil, fmt.Errorf("TZSP tag list truncated")
		}
		tag := rest[0]
		switch tag {
		case 1: // TAG_END: no length, no value, terminates the tag list.
			rest = rest[1:]
			f := &capturev1.TzspFields{}
			f.SetEncapsulatedProtocol(uint32(encapsulatedProtocol))
			return f, rest, nil
		case 0: // TAG_PADDING: no length, no value.
			rest = rest[1:]
		default:
			if len(rest) < 2 {
				return nil, nil, fmt.Errorf("TZSP tag %d truncated", tag)
			}
			length := int(rest[1])
			if len(rest) < 2+length {
				return nil, nil, fmt.Errorf("TZSP tag %d value truncated", tag)
			}
			rest = rest[2+length:]
		}
	}
}

// DecodeUDP parses a UDP-delivered mirror payload against candidates, the
// UDP-family encapsulations (VXLAN, TZSP) the receiver accepts. When more
// than one is configured on the same port, VXLAN's header is tried before
// TZSP's, and the first one that validates structurally is accepted,
// regardless of candidates' own order: ordinary configuration names one
// UDP-family encapsulation per receiver, and this rule exists only for the
// case where it does not. It returns a nil inner frame and no error for a
// decoded envelope that carries none.
func DecodeUDP(payload []byte, srcIP, dstIP net.IP, candidates []capturev1.MirrorEncapsulation) (*capturev1.MirrorEnvelope, []byte, error) {
	has := func(want capturev1.MirrorEncapsulation) bool {
		return slices.Contains(candidates, want)
	}

	env := &capturev1.MirrorEnvelope{}
	env.SetSource(toIPAddress(srcIP))
	env.SetDestination(toIPAddress(dstIP))

	if has(capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_VXLAN) {
		if f, rest, err := decodeVXLAN(payload); err == nil {
			env.SetVxlan(f)
			return env, rest, nil
		}
	}
	if has(capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_TZSP) {
		if f, rest, err := decodeTZSP(payload); err == nil {
			env.SetTzsp(f)
			return env, rest, nil
		}
	}
	return nil, nil, fmt.Errorf("no candidate encapsulation matched the UDP payload")
}
