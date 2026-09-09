// erspan_test.go proves the mirror receiver against independent senders it
// does not control: the Linux kernel's own erspan tunnel device and an Open
// vSwitch erspan port. See README.md for prerequisites; every test here
// skips cleanly when a prerequisite is missing rather than failing the
// suite.
//
//go:build linux && capture_mirror_integration

package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

// requireBinary skips the test when name is not on PATH.
func requireBinary(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not found on PATH; skipping", name)
	}
}

// requireRoot skips the test when it is not running as root: every
// operation here (netns, veth, tc, erspan tunnels) needs CAP_NET_ADMIN.
func requireRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("not running as root; skipping")
	}
}

// run and runOK always invoke "ip": most commands this suite runs go through
// "ip netns exec" (see nsExec) or are themselves an "ip" subcommand. runOVS
// and runOVSOK invoke ovs-vsctl directly instead: unlike every erspan-tunnel
// device this suite creates, ovs-vswitchd's kernel datapath is a single
// resource the daemon owns from whatever namespace it was started in
// (normally root), so ovs-vsctl's own commands are never routed through
// nsExec.
func run(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("ip", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ip %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func runOK(args ...string) error {
	cmd := exec.Command("ip", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

func runOVS(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("ovs-vsctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ovs-vsctl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func runOVSOK(args ...string) error {
	cmd := exec.Command("ovs-vsctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ovs-vsctl %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

func nsExec(t *testing.T, ns string, args ...string) {
	t.Helper()
	run(t, append([]string{"netns", "exec", ns}, args...)...)
}

// erspanTestEnv is a scratch namespace connected to the host by a veth
// pair, torn down on test cleanup. The namespace side (nsAddr) is where the
// kernel's own erspan tunnel device lives and sends traffic (an OVS erspan
// port lives in the host namespace instead — see TestOVS_ErspanPort); the
// host side (hostAddr) is where the mirror receiver listens, since
// rawsocket.OpenMirrorReceiver runs in this test's own (host) namespace.
type erspanTestEnv struct {
	suffix   string
	ns       string
	vethHost string
	vethNS   string
	hostAddr string
	nsAddr   string
}

func newErspanTestEnv(t *testing.T, suffix string) *erspanTestEnv {
	t.Helper()
	requireRoot(t)
	requireBinary(t, "ip")

	e := &erspanTestEnv{
		suffix:   suffix,
		ns:       "flowseer-erspan-" + suffix,
		vethHost: "fs-h-" + suffix,
		vethNS:   "fs-n-" + suffix,
		hostAddr: "10.242.0.1",
		nsAddr:   "10.242.0.2",
	}

	run(t, "netns", "add", e.ns)
	t.Cleanup(func() { _ = runOK("netns", "del", e.ns) })

	run(t, "link", "add", e.vethHost, "type", "veth", "peer", "name", e.vethNS)
	run(t, "link", "set", e.vethNS, "netns", e.ns)

	run(t, "addr", "add", e.hostAddr+"/24", "dev", e.vethHost)
	run(t, "link", "set", e.vethHost, "up")

	nsExec(t, e.ns, "ip", "addr", "add", e.nsAddr+"/24", "dev", e.vethNS)
	nsExec(t, e.ns, "ip", "link", "set", e.vethNS, "up")
	nsExec(t, e.ns, "ip", "link", "set", "lo", "up")

	return e
}

// mirrorViaErspan creates an erspan tunnel device inside e's namespace,
// local to nsAddr and remote to hostAddr, and mirrors every packet ingressing
// the namespace's veth end (the UDP probe generateTraffic sends) onto it.
//
// The device is named per-suffix rather than "erspan0": the ip_gre module
// auto-creates its own fallback device named exactly "erspan0" the first
// time an erspan-type link is created in a namespace (the same pattern as
// gre0/gretap0), and a test-created device of that same name collides with
// it. Mirroring ingress rather than egress traffic on vethNS avoids a
// different problem: the tunnel's own encapsulated output is itself routed
// back out through vethNS (the only route out of the namespace), so an
// egress-side mirror would catch its own tunnel's re-encapsulated traffic
// and mirror that too, recursively.
func (e *erspanTestEnv) mirrorViaErspan(t *testing.T, ver int) {
	t.Helper()
	dev := "fserspan-" + e.suffix
	args := []string{
		"ip", "link", "add", "dev", dev, "type", "erspan",
		"local", e.nsAddr, "remote", e.hostAddr,
		"erspan_ver", strconv.Itoa(ver),
	}
	if ver != 0 {
		args = append(args, "key", "100")
	}
	if ver == 2 {
		args = append(args, "erspan_dir", "egress", "erspan_hwid", "7")
	}
	nsExec(t, e.ns, args...)
	nsExec(t, e.ns, "ip", "link", "set", dev, "up")

	nsExec(t, e.ns, "tc", "qdisc", "add", "dev", e.vethNS, "clsact")
	nsExec(t, e.ns, "tc", "filter", "add", "dev", e.vethNS, "ingress",
		"matchall", "action", "mirred", "egress", "mirror", "dev", dev)
}

// probeErspanVer0 reports whether the running kernel supports erspan_ver 0
// (ERSPAN Type I, added to ip_gre after the initial version 1/2 support).
func probeErspanVer0(t *testing.T, e *erspanTestEnv) bool {
	t.Helper()
	err := runOK("netns", "exec", e.ns, "ip", "link", "add", "dev", "erspan-probe",
		"type", "erspan", "local", e.nsAddr, "remote", e.hostAddr, "erspan_ver", "0")
	if err != nil {
		return false
	}
	_ = runOK("netns", "exec", e.ns, "ip", "link", "del", "erspan-probe")
	return true
}

// generateTraffic sends one UDP datagram from the host to the namespace,
// which crosses the veth pair and ingresses vethNS: the traffic
// mirrorViaErspan's tc filter mirrors. The datagram's own delivery outcome
// does not matter (nothing inside the namespace need be listening); the
// ingress crossing itself is what the mirror catches.
func generateTraffic(t *testing.T, e *erspanTestEnv) {
	t.Helper()
	conn, err := net.DialTimeout("udp4", net.JoinHostPort(e.nsAddr, "9999"), time.Second)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("probe"))
}

func TestErspanTunnel_TypeII(t *testing.T) {
	e := newErspanTestEnv(t, "t2")
	e.mirrorViaErspan(t, 1) // erspan_ver 1: Type II
	assertMirroredFrame(t, e, []capturev1.MirrorEncapsulation{
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_ERSPAN_TYPE_II,
	}, func(env *capturev1.MirrorEnvelope) bool { return env.HasErspanTypeIi() })
}

func TestErspanTunnel_TypeIII(t *testing.T) {
	e := newErspanTestEnv(t, "t3")
	e.mirrorViaErspan(t, 2) // erspan_ver 2: Type III
	assertMirroredFrame(t, e, []capturev1.MirrorEncapsulation{
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_ERSPAN_TYPE_III,
	}, func(env *capturev1.MirrorEnvelope) bool { return env.HasErspanTypeIii() })
}

func TestErspanTunnel_TypeI(t *testing.T) {
	e := newErspanTestEnv(t, "t1")
	if !probeErspanVer0(t, e) {
		t.Skip("running kernel does not support erspan_ver 0 (needs Linux >= 4.18); skipping ERSPAN Type I")
	}
	e.mirrorViaErspan(t, 0) // erspan_ver 0: Type I
	assertMirroredFrame(t, e, []capturev1.MirrorEncapsulation{
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_ERSPAN_TYPE_I,
	}, func(env *capturev1.MirrorEnvelope) bool { return env.HasErspanTypeI() })
}

// TestOVS_ErspanPort builds the bridge and erspan port in the root
// namespace, not e.ns: ovs-vswitchd's kernel datapath is a single global
// resource the daemon owns from wherever it was started (normally the root
// namespace), so "ip netns exec e.ns ovs-vsctl ..." runs the ovs-vsctl
// client inside e.ns but still talks to that same root-namespace daemon and
// datapath — a bridge "created" that way is bookkept by ovsdb without ever
// correctly attaching to e.vethNS, which really lives inside e.ns. Enslaving
// e.vethHost (already in the root namespace) into the bridge gives it real
// traffic to mirror instead.
//
// A bridge port alone forwards nothing to it; OVS needs an explicit Mirror
// record naming the port to copy traffic onto, set on the bridge's own
// mirrors column.
func TestOVS_ErspanPort(t *testing.T) {
	requireBinary(t, "ovs-vsctl")
	e := newErspanTestEnv(t, "ovs")

	bridge := "fsbr-" + e.suffix
	erspanPort := "fserspan-" + e.suffix

	runOVS(t, "add-br", bridge)
	t.Cleanup(func() { _ = runOVSOK("del-br", bridge) })

	// vethHost joins the bridge as an L2 port: its own IP address moves to
	// the bridge device, the same as attaching any interface to a Linux
	// bridge.
	run(t, "addr", "del", e.hostAddr+"/24", "dev", e.vethHost)
	runOVS(t, "add-port", bridge, e.vethHost)
	run(t, "addr", "add", e.hostAddr+"/24", "dev", bridge)
	run(t, "link", "set", bridge, "up")

	// local_ip and remote_ip are both hostAddr: the bridge, the erspan port,
	// and the mirror receiver (assertMirroredFrame's OpenMirrorReceiver) all
	// run in this same root namespace, so the encapsulated GRE packet only
	// needs to be delivered locally, not routed anywhere.
	runOVS(t, "add-port", bridge, erspanPort,
		"--", "set", "interface", erspanPort, "type=erspan",
		"options:erspan_ver=1", "options:key=100",
		"options:remote_ip="+e.hostAddr, "options:local_ip="+e.hostAddr)

	runOVS(t,
		"--", "--id=@p", "get", "port", erspanPort,
		"--", "--id=@m", "create", "Mirror", "name=fs-mirror-"+e.suffix, "select-all=true", "output-port=@p",
		"--", "set", "bridge", bridge, "mirrors=@m")

	assertMirroredFrame(t, e, []capturev1.MirrorEncapsulation{
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_ERSPAN_TYPE_II,
	}, func(env *capturev1.MirrorEnvelope) bool { return env.HasErspanTypeIi() })
}

func assertMirroredFrame(t *testing.T, e *erspanTestEnv, encapsulations []capturev1.MirrorEncapsulation, wrapperOK func(*capturev1.MirrorEnvelope) bool) {
	t.Helper()

	src, err := rawsocket.OpenMirrorReceiver(encapsulations, 0, "", nil)
	if err != nil {
		t.Fatalf("OpenMirrorReceiver: %v", err)
	}
	defer func() { _ = src.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := src.Receive(ctx)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		generateTraffic(t, e)

		select {
		case f := <-frames:
			if f.Err != nil {
				continue
			}
			if !wrapperOK(f.Envelope) {
				t.Fatalf("mirrored frame's envelope did not carry the expected wrapper: %v", f.Envelope)
			}
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("no mirrored frame carrying the expected wrapper arrived within the timeout")
}
