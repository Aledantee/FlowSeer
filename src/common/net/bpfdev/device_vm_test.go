//go:build freebsd && (amd64 || arm64) && bpfdev_vm

package bpfdev

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
	// envFixtureDir names the directory the fixture test writes the read
	// buffer to, and the one test/freebsd/bpfdev-setup.sh makes tcpdump
	// write its capture to.
	envFixtureDir = "FLOWSEER_BPFDEV_FIXTURE_DIR"
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
	// The kernel rounds the timeout to clock ticks and may fire a tick
	// early, so the lower bound is half the timeout. A read that returns at
	// once is still caught.
	if elapsed < timeout/2 {
		t.Errorf("ReadBatch took %v, want at least %v", elapsed, timeout/2)
	}
}

// A timeout under one kernel clock tick converts to a bd_rtout of zero in
// the kernel, which means no timeout, so Open must raise it to one tick.
func TestDeviceReadTimeoutBelowOneTickReturns(t *testing.T) {
	d, err := Open(ifaceFromEnv(t, envB), Config{ReadTimeout: time.Microsecond, ReadFilter: nothingFilter()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	type result struct {
		recs []Record
		err  error
	}
	done := make(chan result, 1)
	spawn.Go(context.Background(), "bpfdev.test.read", func() {
		recs, err := d.ReadBatch()
		done <- result{recs, err}
	})
	// Close waits for the read, so a read that never returns would hang the
	// cleanup too. Close only after the read has returned.
	returned := false
	t.Cleanup(func() {
		if returned {
			_ = d.Close()
		}
	})

	select {
	case r := <-done:
		returned = true
		if r.err != nil {
			t.Fatalf("ReadBatch: %v", r.err)
		}
		if len(r.recs) != 0 {
			t.Errorf("got %d records on a quiet interface, want none", len(r.recs))
		}
	case <-time.After(time.Second):
		t.Fatal("ReadBatch did not return within 1s with a 1µs ReadTimeout")
	}
}

// A Write on a device must not wait for a ReadBatch blocked on the same
// device: both take the device lock shared.
func TestDeviceWriteDuringBlockedRead(t *testing.T) {
	const readTimeout = 2 * time.Second
	d := openDevice(t, ifaceFromEnv(t, envA), Config{HeaderComplete: true, ReadTimeout: readTimeout, ReadFilter: nothingFilter()})

	started := make(chan struct{})
	done := make(chan error, 1)
	spawn.Go(context.Background(), "bpfdev.test.read", func() {
		close(started)
		_, err := d.ReadBatch()
		done <- err
	})
	<-started
	// The pause lets the read take the device lock and block in the kernel.
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	if err := d.Write(testFrame()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("Write took %v behind a blocked read, want at most 200ms", elapsed)
	}
	if err := <-done; err != nil {
		t.Errorf("ReadBatch: %v", err)
	}
}

// After Close, an operation must fail with the closed-device error, not
// with the kernel's EBADF for the released descriptor.
func TestDeviceReadAfterCloseIsClosedError(t *testing.T) {
	d := openDevice(t, ifaceFromEnv(t, envB), Config{})
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := d.ReadBatch()
	if !errors.Is(err, errs.New().Code(ErrCodeIO).Msg("")) {
		t.Fatalf("ReadBatch error = %v, want code %q", err, ErrCodeIO)
	}
	if errors.Is(err, unix.EBADF) {
		t.Errorf("ReadBatch error = %v, want the closed-device error, not EBADF", err)
	}
}

func TestDeviceWriteAfterCloseIsClosedError(t *testing.T) {
	d := openDevice(t, ifaceFromEnv(t, envA), Config{HeaderComplete: true})
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err := d.Write(testFrame())
	if !errors.Is(err, errs.New().Code(ErrCodeIO).Msg("")) {
		t.Fatalf("Write error = %v, want code %q", err, ErrCodeIO)
	}
	if errors.Is(err, unix.EBADF) {
		t.Errorf("Write error = %v, want the closed-device error, not EBADF", err)
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

// TestWriteReadBufferFixture is a capture tool, not a regression test. It
// sends three frames of 60, 98, and 1514 bytes across the epair while tcpdump
// records the same frames, and writes the one read(2) buffer that holds them.
// TestParseBufferMatchesTcpdump compares the two files.
func TestWriteReadBufferFixture(t *testing.T) {
	dir := os.Getenv(envFixtureDir)
	if dir == "" {
		t.Skipf("%s is not set: this test writes the buffer fixture", envFixtureDir)
	}
	a, b := ifaceFromEnv(t, envA), ifaceFromEnv(t, envB)
	tx := openDevice(t, a, Config{HeaderComplete: true})
	// Immediate stays off, so the read returns when the timeout expires and
	// the buffer then holds all three frames.
	rx := openDevice(t, b, Config{ReadTimeout: time.Second, ReadFilter: etherTypeFilter(t, 0x88b5)})

	for i, size := range []int{60, 98, 1514} {
		frame := make([]byte, size)
		copy(frame[0:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
		copy(frame[6:12], []byte{0x02, 0, 0, 0, 0, 0x99})
		copy(frame[12:14], testEtherType)
		for j := 14; j < size; j++ {
			frame[j] = byte(i<<6 + j)
		}
		if err := tx.Write(frame); err != nil {
			t.Fatalf("Write frame %d: %v", i, err)
		}
	}

	// ReadBatch hides the byte count of the read, so this reads the
	// descriptor itself, once.
	rx.readMu.Lock()
	n, err := unix.Read(rx.fd, rx.buf)
	rx.readMu.Unlock()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	raw := rx.buf[:n]

	recs, err := parseBuffer(raw, hdrLayout64)
	if err != nil {
		t.Fatalf("parseBuffer: %v", err)
	}
	var lengths []int
	for _, r := range recs {
		lengths = append(lengths, r.CapturedLength)
	}
	if len(lengths) != 3 || lengths[0] != 60 || lengths[1] != 98 || lengths[2] != 1514 {
		t.Fatalf("one read holds frames of lengths %v, want [60 98 1514]", lengths)
	}

	path := filepath.Join(dir, "freebsd14-arm64-read.bin")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Logf("wrote %d bytes to %s", n, path)
}

// etherTypeFilter accepts frames of one EtherType: ldh [12]; jeq etherType; ret
// 0xffff; ret 0.
func etherTypeFilter(t *testing.T, etherType uint32) []bpf.RawInstruction {
	t.Helper()
	prog, err := bpf.Assemble([]bpf.Instruction{
		bpf.LoadAbsolute{Off: 12, Size: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherType, SkipFalse: 1},
		bpf.RetConstant{Val: 0xffff},
		bpf.RetConstant{Val: 0},
	})
	if err != nil {
		t.Fatalf("assemble filter: %v", err)
	}
	return prog
}

// nothingFilter is a program that rejects every packet: return 0.
func nothingFilter() []bpf.RawInstruction {
	return []bpf.RawInstruction{{Op: 0x06, K: 0}}
}
