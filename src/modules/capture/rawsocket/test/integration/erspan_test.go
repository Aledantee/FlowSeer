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
	out, err := exec.Command("id", "-u").Output()
	if err != nil {
		t.Skipf("could not determine uid: %v", err)
	}
	if strings.TrimSpace(string(out)) != "0" {
		t.Skip("not running as root; skipping")
	}
}

// run and runOK always invoke "ip": every command this suite runs, OVS's
// own ovs-vsctl included, goes through "ip netns exec" (see nsExec) or is
// itself an "ip" subcommand.
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

func nsExec(t *testing.T, ns string, args ...string) {
	t.Helper()
	run(t, append([]string{"netns", "exec", ns}, args...)...)
}

// erspanTestEnv is a scratch namespace connected to the host by a veth
// pair, torn down on test cleanup. The namespace side (nsAddr) is where the
// erspan tunnel or OVS port lives and sends traffic; the host side (hostAddr)
// is where the mirror receiver listens, since rawsocket.OpenMirrorReceiver
// runs in this test's own (host) namespace.
type erspanTestEnv struct {
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
// local to nsAddr and remote to hostAddr, and mirrors every packet egressing
// the namespace's veth end onto it.
func (e *erspanTestEnv) mirrorViaErspan(t *testing.T, ver int) {
	t.Helper()
	dev := "erspan0"
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
	nsExec(t, e.ns, "tc", "filter", "add", "dev", e.vethNS, "egress",
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
// which crosses the veth pair; the reply the namespace's kernel sends back
// egresses vethNS and is what the tc mirror above wraps and forwards.
func generateTraffic(t *testing.T, e *erspanTestEnv) {
	t.Helper()
	// A UDP datagram to a closed port draws an ICMP port-unreachable reply
	// from the namespace, which is enough egress traffic on vethNS for the
	// mirror to catch without needing a listener inside the namespace.
	_ = exec.Command("bash", "-c", fmt.Sprintf(
		"echo -n probe > /dev/udp/%s/9999", e.nsAddr,
	)).Run()
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

func TestOVS_ErspanPort(t *testing.T) {
	requireBinary(t, "ovs-vsctl")
	e := newErspanTestEnv(t, "ovs")

	bridge := "fsbr0"
	nsExec(t, e.ns, "ovs-vsctl", "add-br", bridge)
	t.Cleanup(func() { _ = runOK("netns", "exec", e.ns, "ovs-vsctl", "del-br", bridge) })
	nsExec(t, e.ns, "ovs-vsctl", "add-port", bridge, e.vethNS)
	nsExec(t, e.ns, "ovs-vsctl", "add-port", bridge, "erspan0",
		"--", "set", "interface", "erspan0", "type=erspan",
		"options:erspan_ver=1", "options:key=100",
		"options:remote_ip="+e.hostAddr, "options:local_ip="+e.nsAddr)
	nsExec(t, e.ns, "ip", "link", "set", bridge, "up")

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
