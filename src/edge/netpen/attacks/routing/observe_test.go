// observe_test.go holds the direct harness the routing tests drive the
// behaviors with inside a synctest bubble, and the observation tests: for
// each behavior, the response fixture that confirms it, a mutation of
// that fixture that must not, and the quiet leg.

package routing_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/routing"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/testtest"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	nl "go.aledante.io/FlowSeer/src/edge/netpen/layers"
)

// recordingEmitter keeps the findings a behavior emits.
type recordingEmitter struct {
	findings []json.RawMessage
}

func (e *recordingEmitter) Emit(findings.Record) bool { return true }

func (e *recordingEmitter) Finding(_ string, detail json.RawMessage) bool {
	e.findings = append(e.findings, detail)
	return true
}

func (e *recordingEmitter) Progress(_, _ string) bool { return true }

// armedStep is one step a behavior armed through the recorder.
type armedStep struct {
	name string
	step check.Step
}

// teardownRecorder is the [check.Teardown] the direct harness hands a
// behavior: it keeps the steps in arm order for the test to run.
type teardownRecorder struct {
	steps []armedStep
}

func (r *teardownRecorder) Arm(name string, fn check.Step) {
	r.steps = append(r.steps, armedStep{name: name, step: fn})
}

func (r *teardownRecorder) names() []string {
	out := make([]string, len(r.steps))
	for i, s := range r.steps {
		out[i] = s.name
	}
	return out
}

// directRun is what one direct run of a behavior produced.
type directRun struct {
	verdict  check.Verdict
	err      error
	teardown *teardownRecorder
	emitter  *recordingEmitter
}

// runDirect calls the named behavior with deps built from leg, the
// fixture target, the window, and the in-memory listener, the way the
// runner builds them. Call it inside a synctest bubble so the window is
// fake time.
func runDirect(t *testing.T, leg *testtest.Leg, name string, window time.Duration) directRun {
	t.Helper()
	return runDirectWith(t, leg, name, window, memListen)
}

// runDirectWith is runDirect with the listener seam the test chooses.
func runDirectWith(t *testing.T, leg *testtest.Leg, name string, window time.Duration, listen func(context.Context, string, string) (net.Listener, error)) directRun {
	t.Helper()
	entry, run, ok := catalog.MustNew(routing.Behaviors()...).Lookup(name, "")
	if !ok {
		t.Fatalf("catalog entry not found: %s", name)
	}
	rec := &teardownRecorder{}
	em := &recordingEmitter{}
	deps := check.Deps{
		AttackLeg: leg,
		Target:    fixtureTarget(),
		Entry:     entry,
		Observe:   window,
		Emitter:   em,
		Listen:    listen,
	}
	if entry.Restore.Kind == check.RestoreActive {
		deps.Teardown = rec
	}
	v, err := run(context.Background(), deps)
	return directRun{verdict: v, err: err, teardown: rec, emitter: em}
}

// runSteps runs the recorded teardown steps in arm order.
func runSteps(t *testing.T, rec *teardownRecorder) {
	t.Helper()
	for _, s := range rec.steps {
		if err := s.step(context.Background()); err != nil {
			t.Fatalf("step %s: %v", s.name, err)
		}
	}
}

// pusher delivers frames on a leg at later times on the bubble's clock.
// The bubble must join every timed goroutine before its body returns, so
// a test calls join once the behavior has returned.
type pusher struct {
	leg *testtest.Leg
	wg  sync.WaitGroup
}

func (p *pusher) at(delay time.Duration, frame []byte) {
	p.wg.Go(func() {
		time.Sleep(delay)
		p.leg.PushRX(frame)
	})
}

func (p *pusher) join() { p.wg.Wait() }

// wantVerdict fails the test unless the run's verdict has the kind and
// its detail contains want.
func wantVerdict(t *testing.T, r directRun, kind findings.VerdictKind, want string) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("behavior error = %v, want nil", r.err)
	}
	if r.verdict.Kind != kind || !strings.Contains(r.verdict.Detail, want) {
		t.Errorf("verdict = %s %q, want %s with %q", r.verdict.Kind, r.verdict.Detail, kind, want)
	}
}

// withByte returns a copy of frame with the byte at off replaced.
func withByte(frame []byte, off int, b byte) []byte {
	out := append([]byte(nil), frame...)
	out[off] = b
	return out
}

// withMAC returns a copy of frame with the six bytes at off replaced.
func withMAC(frame []byte, off int, mac net.HardwareAddr) []byte {
	out := append([]byte(nil), frame...)
	copy(out[off:off+6], mac)
	return out
}

// Offsets of the fields the mutations change, from the start of the
// Ethernet frame.
const (
	offEthSrc    = 6
	offEIGRPASLo = 14 + 20 + 19 // low octet of the autonomous system after the IPv4 header
)

// fetchingListener is the in-memory listener that hands the server one
// connection on which a client fetches path, then blocks until Close.
type fetchingListener struct {
	*memListener
	path string
	once sync.Once
	conn chan net.Conn
}

func newFetchingListener(path string) *fetchingListener {
	return &fetchingListener{memListener: newMemListener(), path: path, conn: make(chan net.Conn, 1)}
}

func (l *fetchingListener) Accept() (net.Conn, error) {
	l.once.Do(func() {
		server, client := net.Pipe()
		go func() {
			defer func() { _ = client.Close() }()
			req, err := http.NewRequest(http.MethodGet, "http://10.0.0.99"+l.path, http.NoBody)
			if err != nil {
				return
			}
			if err := req.Write(client); err != nil {
				return
			}
			resp, err := http.ReadResponse(bufio.NewReader(client), req)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
		}()
		l.conn <- server
	})
	select {
	case c := <-l.conn:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// fetchListen returns a [check.Deps.Listen] whose listener delivers one
// GET for path.
func fetchListen(path string) func(context.Context, string, string) (net.Listener, error) {
	return func(context.Context, string, string) (net.Listener, error) {
		return newFetchingListener(path), nil
	}
}

func TestOSPF_Observe(t *testing.T) {
	target := fixturePackets(t, "ospf_target.pcap")
	plain, listing := target[0], target[1]
	cases := []struct {
		name   string
		pre    []byte
		later  []byte
		window time.Duration
		kind   findings.VerdictKind
		want   string
		sends  int
	}{
		{"hello listing the leg confirms", plain, listing, 20 * time.Second, findings.VerdictConfirmed, "10.0.0.2 lists router ID 10.0.0.99 as a neighbor", 4},
		{"hello without the leg at the window's end resists", plain, plain, 20 * time.Second, findings.VerdictResisted, "the target's hello does not list router ID 10.0.0.99", 3},
		{"no hello after the probe resists", plain, nil, 20 * time.Second, findings.VerdictResisted, "no OSPF hello from 10.0.0.2 after the probe in 20s", 3},
		{"hello from the attack leg is not the target's", withMAC(plain, offEthSrc, fixtureMAC), nil, 20 * time.Second, findings.VerdictResisted, "no OSPF hello in 10s", 1},
		{"quiet leg resists", nil, nil, 20 * time.Second, findings.VerdictResisted, "no OSPF hello in 10s", 1},
		{"short window is pending", plain, nil, 5 * time.Second, findings.VerdictPending, "pass --observe 20s", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				if tc.pre != nil {
					leg.PushRX(tc.pre)
				}
				p := &pusher{leg: leg}
				if tc.later != nil {
					p.at(time.Second, tc.later)
					p.at(12*time.Second, tc.later)
				}
				r := runDirect(t, leg, "ospf", tc.window)
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
				if got := leg.SendCount(); got != tc.sends {
					t.Errorf("sends = %d, want %d", got, tc.sends)
				}
				if got := r.teardown.names(); strings.Join(got, ",") != "ospf-lsa-flush,ospf-goodbye" {
					t.Errorf("armed steps = %v, want [ospf-lsa-flush ospf-goodbye]", got)
				}
			})
		})
	}
}

func TestEIGRP_Observe(t *testing.T) {
	target := fixturePackets(t, "eigrp_target.pcap")
	hello, ack := target[0], target[1]
	rejects := fixturePackets(t, "eigrp_reject.pcap")
	cases := []struct {
		name  string
		pre   []byte
		later []byte
		kind  findings.VerdictKind
		want  string
		sends int
	}{
		{"acknowledgment confirms", hello, ack, findings.VerdictConfirmed, "the target acknowledged the adjacency in AS 100", 3},
		{"acknowledgment from another AS does not confirm", hello, withByte(ack, offEIGRPASLo, 0x65), findings.VerdictResisted, "the null update drew no acknowledgment in 3s", 2},
		{"peer termination after the hello resists", hello, rejects[0], findings.VerdictResisted, "adjacency refused", 2},
		{"K-values of 255 resist", rejects[1], nil, findings.VerdictResisted, "adjacency refused", 1},
		{"hello from another AS is not read", withByte(hello, offEIGRPASLo, 0x65), nil, findings.VerdictResisted, "no EIGRP hello for AS 100 in 3s", 1},
		{"quiet leg resists", nil, nil, findings.VerdictResisted, "no EIGRP hello for AS 100 in 3s", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				if tc.pre != nil {
					leg.PushRX(tc.pre)
				}
				p := &pusher{leg: leg}
				if tc.later != nil {
					p.at(time.Second, tc.later)
				}
				r := runDirect(t, leg, "eigrp", 3*time.Second)
				p.join()
				wantVerdict(t, r, tc.kind, tc.want)
				if got := leg.SendCount(); got != tc.sends {
					t.Errorf("sends = %d, want %d", got, tc.sends)
				}
			})
		})
	}
}

func TestWPAD_Observe(t *testing.T) {
	queries := fixturePackets(t, "wpad_query.pcap")
	nbns, llmnr := queries[0], queries[1]
	cases := []struct {
		name   string
		query  []byte
		listen func(context.Context, string, string) (net.Listener, error)
		kind   findings.VerdictKind
		want   string
		sends  int
	}{
		{"PAC fetch after the answer confirms", nbns, fetchListen("/wpad.dat"), findings.VerdictConfirmed, "a client fetched the PAC", 1},
		{"request for another path resists", nbns, fetchListen("/index.html"), findings.VerdictResisted, "no PAC fetch in 1s", 1},
		{"answered query without a fetch resists", llmnr, memListen, findings.VerdictResisted, "no PAC fetch in 1s", 1},
		{"query from the attack leg is not read", withMAC(nbns, offEthSrc, fixtureMAC), memListen, findings.VerdictPending, "no wpad query in 1s", 0},
		{"quiet leg is pending", nil, memListen, findings.VerdictPending, "no wpad query in 1s", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				leg := fixtureLeg(t)
				if tc.query != nil {
					leg.PushRX(tc.query)
				}
				r := runDirectWith(t, leg, "wpad", time.Second, tc.listen)
				wantVerdict(t, r, tc.kind, tc.want)
				if got := leg.SendCount(); got != tc.sends {
					t.Errorf("sends = %d, want %d", got, tc.sends)
				}
			})
		})
	}
}

// TestWPAD_AnswerCopiesTheQueryID decodes the LLMNR answer with the owned
// layer and reads the query's id, address, and port back from it.
func TestWPAD_AnswerCopiesTheQueryID(t *testing.T) {
	query := fixturePackets(t, "wpad_query.pcap")[1]
	synctest.Test(t, func(t *testing.T) {
		leg := fixtureLeg(t)
		leg.PushRX(withByte(withByte(query, 14+20+8, 0xab), 14+20+8+1, 0xcd))
		r := runDirect(t, leg, "wpad", time.Second)
		wantVerdict(t, r, findings.VerdictResisted, "no PAC fetch")
		tx := leg.TX()
		if len(tx) != 1 {
			t.Fatalf("sends = %d, want the one answer", len(tx))
		}
		pkt := gopacket.NewPacket(tx[0], layers.LayerTypeEthernet, gopacket.Default)
		answer, ok := pkt.Layer(nl.LayerTypeLLMNR).(*nl.LLMNR)
		if !ok {
			t.Fatalf("answer decodes to %v, want an LLMNR layer", pkt.Layers())
		}
		if answer.ID != 0xabcd {
			t.Errorf("answer id = %#x, want the query's 0xabcd", answer.ID)
		}
		ip, _ := pkt.Layer(layers.LayerTypeIPv4).(*layers.IPv4)
		udp, _ := pkt.Layer(layers.LayerTypeUDP).(*layers.UDP)
		if ip == nil || udp == nil || !ip.DstIP.Equal(net.IPv4(10, 0, 0, 2)) || udp.DstPort != 51000 {
			t.Errorf("answer addressed to %v:%v, want the query's 10.0.0.2:51000", ip, udp)
		}
	})
}
