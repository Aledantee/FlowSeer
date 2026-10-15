//go:build freebsd && rawsocket_vm

package rawsocket

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"golang.org/x/net/bpf"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/bpfdev"
)

// These tests need the epair(4) ends and the jail that test/freebsd sets up
// and run only in the FreeBSD VM. The rawsocket_vm tag opts in, so a missing
// variable fails the run.

const (
	envLocalA = "FLOWSEER_RAWSOCKET_LOCAL_A"
	envLocalB = "FLOWSEER_RAWSOCKET_LOCAL_B"
)

// testEtherType is IEEE 802 local experimental, so no stack answers it.
var testEtherType = []byte{0x88, 0xb5}

var vlan1000Tag = []byte{0x81, 0x00, 0x03, 0xe8}

func ifaceFromEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("%s is not set: the rawsocket_vm tag needs the epair ends from test/freebsd", name)
	}
	return v
}

// vlan1000Filter accepts a frame whose 802.1Q tag at offset 12 carries VLAN
// 1000 and rejects every other frame, tagged or not.
func vlan1000Filter(t *testing.T) []bpf.RawInstruction {
	t.Helper()
	prog, err := bpf.Assemble([]bpf.Instruction{
		bpf.LoadAbsolute{Off: 12, Size: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: 0x8100, SkipFalse: 4},
		bpf.LoadAbsolute{Off: 14, Size: 2},
		bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: 0x0fff},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: 1000, SkipFalse: 1},
		bpf.RetConstant{Val: 65535},
		bpf.RetConstant{Val: 0},
	})
	if err != nil {
		t.Fatalf("assemble VLAN filter: %v", err)
	}
	return prog
}

// sender opens a device on iface that writes frames as given.
func sender(t *testing.T, iface string) *bpfdev.Device {
	t.Helper()
	d, err := bpfdev.Open(iface, bpfdev.Config{HeaderComplete: true})
	if err != nil {
		t.Fatalf("bpfdev.Open(%q): %v", iface, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// testFrame is a 64-byte frame to a broadcast address from
// 02:00:00:00:00:99, with the 802.1Q tag when tag is not nil.
func testFrame(tag []byte) []byte {
	f := make([]byte, 0, 64)
	f = append(f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	f = append(f, 0x02, 0, 0, 0, 0, 0x99)
	f = append(f, tag...)
	f = append(f, testEtherType...)
	return append(f, make([]byte, 64-len(f))...)
}

func openLocal(t *testing.T, iface string, prog []bpf.RawInstruction) Source {
	t.Helper()
	src, err := OpenLocalInterface(iface, false, prog)
	if err != nil {
		t.Fatalf("OpenLocalInterface(%q): %v", iface, err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src
}

func nextFrame(t *testing.T, frames <-chan Frame) Frame {
	t.Helper()
	select {
	case f, ok := <-frames:
		if !ok {
			t.Fatal("frame channel closed before a frame arrived")
		}
		if f.Err != nil {
			t.Fatalf("receive: %v", f.Err)
		}
		return f
	case <-time.After(3 * time.Second):
		t.Fatal("no frame within 3s")
	}
	return Frame{}
}

func TestLocalSourceDeliversTaggedFrame(t *testing.T) {
	tx := sender(t, ifaceFromEnv(t, envLocalA))
	src := openLocal(t, ifaceFromEnv(t, envLocalB), vlan1000Filter(t))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := src.Receive(ctx)

	// The untagged frame goes first, so a delivered frame that is not the
	// tagged one means the filter let the untagged one through.
	for _, f := range [][]byte{testFrame(nil), testFrame(vlan1000Tag)} {
		if err := tx.Write(f); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	got := nextFrame(t, frames)
	if !bytes.Equal(got.Data[12:16], vlan1000Tag) {
		t.Errorf("Data[12:16] = % x, want % x", got.Data[12:16], vlan1000Tag)
	}
	if want := uint32(len(testFrame(vlan1000Tag))); got.OriginalLength != want {
		t.Errorf("OriginalLength = %d, want %d", got.OriginalLength, want)
	}
	if got.CapturedAt.IsZero() {
		t.Error("CapturedAt is zero, want the record's timestamp")
	}
}

func TestLocalSourceRefusesNonEthernet(t *testing.T) {
	src, err := OpenLocalInterface("lo0", false, nil)
	if err == nil {
		_ = src.Close()
		t.Fatal("OpenLocalInterface(lo0) succeeded, want an error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeSourceOpen {
		t.Errorf("error code = %v (found %t), want %v: %v", code, ok, ErrCodeSourceOpen, err)
	}
}

// The filter rejects two of the three frames in userspace, so received
// counting all three shows the counters sit below that filter.
func TestLocalSourceStatsDeltas(t *testing.T) {
	tx := sender(t, ifaceFromEnv(t, envLocalA))
	src := openLocal(t, ifaceFromEnv(t, envLocalB), vlan1000Filter(t))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := src.Receive(ctx)

	for _, f := range [][]byte{testFrame(nil), testFrame(vlan1000Tag), testFrame(nil)} {
		if err := tx.Write(f); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	nextFrame(t, frames)

	received, dropped, err := src.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if received != 3 || dropped != 0 {
		t.Errorf("first Stats = (received %d, dropped %d), want (3, 0)", received, dropped)
	}
	received, dropped, err = src.Stats()
	if err != nil {
		t.Fatalf("second Stats: %v", err)
	}
	if received != 0 || dropped != 0 {
		t.Errorf("second Stats = (received %d, dropped %d), want (0, 0)", received, dropped)
	}
}
