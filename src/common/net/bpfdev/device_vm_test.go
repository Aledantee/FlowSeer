//go:build freebsd && (amd64 || arm64) && bpfdev_vm

package bpfdev

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

// These tests need two epair(4) ends and run only in the FreeBSD VM. The
// bpfdev_vm tag opts in, so a missing variable fails the run.

const (
	envA = "FLOWSEER_BPFDEV_A"
	envB = "FLOWSEER_BPFDEV_B"
)

// testEtherType is IEEE 802 local experimental, so no stack answers it.
var testEtherType = []byte{0x88, 0xb5}

func ifaceFromEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Fatalf("%s is not set: the bpfdev_vm tag needs the epair ends from test/freebsd", name)
	}
	return v
}

func openDevice(t *testing.T, iface string, cfg Config) *Device {
	t.Helper()
	d, err := Open(iface, cfg)
	if err != nil {
		t.Fatalf("Open(%q): %v", iface, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// testFrame is a 60-byte frame to a broadcast address with source
// 02:00:00:00:00:99.
func testFrame() []byte {
	f := make([]byte, 60)
	copy(f[0:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	copy(f[6:12], []byte{0x02, 0, 0, 0, 0, 0x99})
	copy(f[12:14], testEtherType)
	return f
}

// readUntil reads batches from d until one holds a frame with the test
// EtherType or the deadline passes.
func readUntil(t *testing.T, d *Device, deadline time.Duration) *Record {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		recs, err := d.ReadBatch()
		if err != nil {
			t.Fatalf("ReadBatch: %v", err)
		}
		for i := range recs {
			if len(recs[i].Data) >= 14 && bytes.Equal(recs[i].Data[12:14], testEtherType) {
				return &recs[i]
			}
		}
	}
	t.Fatalf("no test frame within %v", deadline)
	return nil
}

func TestDeviceWriteReadAcrossEpair(t *testing.T) {
	a, b := ifaceFromEnv(t, envA), ifaceFromEnv(t, envB)
	tx := openDevice(t, a, Config{HeaderComplete: true})
	rx := openDevice(t, b, Config{Immediate: true})

	frame := testFrame()
	if err := tx.Write(frame); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rec := readUntil(t, rx, 2*time.Second)
	if want := []byte{0x02, 0, 0, 0, 0, 0x99}; !bytes.Equal(rec.Data[6:12], want) {
		t.Errorf("source address = %x, want %x", rec.Data[6:12], want)
	}
	if rec.CapturedLength != len(frame) || rec.WireLength != len(frame) {
		t.Errorf("lengths = %d captured, %d wire, want %d both", rec.CapturedLength, rec.WireLength, len(frame))
	}
}

func TestDeviceStatsCountUnread(t *testing.T) {
	a, b := ifaceFromEnv(t, envA), ifaceFromEnv(t, envB)
	tx := openDevice(t, a, Config{HeaderComplete: true})
	rx := openDevice(t, b, Config{})

	for range 3 {
		if err := tx.Write(testFrame()); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	// The frames cross the epair in the writing thread's context, so they
	// are counted by the time Write returns. Poll the counter anyway.
	deadline := time.Now().Add(2 * time.Second)
	var st unix.BpfStat
	for {
		var err error
		st, err = rx.Stats()
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if st.Recv >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond) // wait for the epair hand-off to reach the descriptor
	}
	if st.Recv != 3 || st.Drop != 0 {
		t.Errorf("Stats = {Recv: %d, Drop: %d}, want {Recv: 3, Drop: 0}", st.Recv, st.Drop)
	}
}

func TestDeviceReadTimeoutReturnsEmpty(t *testing.T) {
	b := ifaceFromEnv(t, envB)
	const timeout = 50 * time.Millisecond
	d := openDevice(t, b, Config{ReadTimeout: timeout, ReadFilter: nothingFilter()})

	start := time.Now()
	recs, err := d.ReadBatch()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ReadBatch: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("got %d records on a quiet interface, want none", len(recs))
	}
	if elapsed > 2*timeout {
		t.Errorf("ReadBatch took %v, want at most %v", elapsed, 2*timeout)
	}
}

func TestDeviceLinkTypeEthernet(t *testing.T) {
	d := openDevice(t, ifaceFromEnv(t, envA), Config{})

	dlt, err := d.LinkType()
	if err != nil {
		t.Fatalf("LinkType: %v", err)
	}
	if dlt != unix.DLT_EN10MB {
		t.Errorf("LinkType = %d, want %d (DLT_EN10MB)", dlt, unix.DLT_EN10MB)
	}
}

func TestDeviceOpenUnknownInterface(t *testing.T) {
	_, err := Open("nosuch0", Config{})
	if !errors.Is(err, errs.New().Code(ErrCodeOpen).Msg("")) {
		t.Fatalf("Open error = %v, want code %q", err, ErrCodeOpen)
	}
	if got := errs.Attributes(err)["iface"]; got != "nosuch0" {
		t.Errorf("iface attribute = %v, want nosuch0", got)
	}
}

func TestDeviceOpenRefusesNegativeReadTimeout(t *testing.T) {
	_, err := Open(ifaceFromEnv(t, envA), Config{ReadTimeout: -time.Second})
	if !errors.Is(err, errs.New().Code(ErrCodeOpen).Msg("")) {
		t.Errorf("Open error = %v, want code %q", err, ErrCodeOpen)
	}
}

func TestDeviceCloseDuringReadDefaultTimeout(t *testing.T) {
	d := openDevice(t, ifaceFromEnv(t, envB), Config{ReadFilter: nothingFilter()})

	started := make(chan struct{})
	done := make(chan error, 1)
	spawn.Go(context.Background(), "bpfdev.test.read", func() {
		close(started)
		_, err := d.ReadBatch()
		done <- err
	})
	<-started
	// The read takes the device lock within microseconds of starting. The
	// pause lets it do so, so Close has a blocked read to wait for.
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("Close took %v, want at most 200ms", elapsed)
	}
	if err := <-done; err != nil {
		t.Errorf("ReadBatch: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestDeviceBufferLenIsKernelValue(t *testing.T) {
	a, b := ifaceFromEnv(t, envA), ifaceFromEnv(t, envB)
	tx := openDevice(t, a, Config{HeaderComplete: true})
	// The kernel clamps BIOCSBLEN to net.bpf.maxbufsize (524288 by default)
	// and writes the clamped size back (sys/net/bpf_buffer.c,
	// bpf_buffer_ioctl_sblen), so twice that is a request it changes.
	rx := openDevice(t, b, Config{BufferLen: 2 * DefaultBufferLen, Immediate: true})

	if err := tx.Write(testFrame()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// readUntil fails on any ReadBatch error, which is where EINVAL shows up
	// when the read size is not the kernel's.
	readUntil(t, rx, 2*time.Second)
}

// nothingFilter is a program that rejects every packet: return 0.
func nothingFilter() []bpf.RawInstruction {
	return []bpf.RawInstruction{{Op: 0x06, K: 0}}
}
