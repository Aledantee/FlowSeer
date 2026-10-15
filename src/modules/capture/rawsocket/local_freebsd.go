//go:build freebsd && (amd64 || arm64)

package rawsocket

import (
	"bytes"
	"time"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/bpfdev"
)

// errNoRecords marks a poll whose read timed out with nothing buffered. The
// receive loop treats it as retryable and delivers no frame.
var errNoRecords = errs.New().Msg("no records this poll")

// openLocalInterface opens iface through a BPF device and runs prog in
// userspace, as the Linux source does, so the device's counters keep one
// meaning on both platforms.
//
// The device sees both directions. bpfdev's zero Config selects DirectionIn
// (src/common/net/bpfdev/config.go), so the source sets DirectionInOut
// explicitly: an AF_PACKET socket bound with ETH_P_ALL on Linux sees
// outgoing frames too, and both platforms should capture both directions. It
// reads with Immediate set, so a frame is delivered when it
// arrives instead of when the buffer fills or the timeout expires.
func openLocalInterface(iface string, promiscuous bool, prog []bpf.RawInstruction) (Source, error) {
	var vm *bpf.VM
	if len(prog) > 0 {
		insts, allDecoded := bpf.Disassemble(prog)
		if !allDecoded {
			return nil, errs.New().
				Code(ErrCodeSourceOpen).
				Msg("disassemble the local interface's filter program")
		}
		v, err := bpf.NewVM(insts)
		if err != nil {
			return nil, errs.From(err).
				Code(ErrCodeSourceOpen).
				Msg("build the local interface's filter VM")
		}
		vm = v
	}

	dev, err := bpfdev.Open(iface, bpfdev.Config{
		ReadTimeout: pollTimeout,
		Immediate:   true,
		Promiscuous: promiscuous,
		Direction:   bpfdev.DirectionInOut,
	})
	if err != nil {
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			Attr("iface", iface).
			UserMsg("could not open the network interface for raw capture").
			Hint("check the interface name and that you run as root").
			Msgf("open bpf device on %q", iface)
	}

	// The engine writes LINK_TYPE_ETHERNET for every frame, so any other
	// link type would be recorded with the wrong framing.
	dlt, err := dev.LinkType()
	if err == nil && dlt != unix.DLT_EN10MB {
		err = errs.New().Attr("dlt", dlt).Msg("link type is not Ethernet")
	}
	if err != nil {
		_ = dev.Close()
		return nil, errs.From(err).
			Code(ErrCodeSourceOpen).
			Attr("iface", iface).
			UserMsg("the named interface is not an Ethernet interface").
			Msgf("check link type of %q", iface)
	}

	src := newLocalSource(&bpfFrameReader{dev: dev})
	src.vm = vm
	return src, nil
}

// bpfFrameReader hands out the records of one ReadBatch a frame at a time.
//
// pending aliases the device's buffer, which the next ReadBatch overwrites,
// so the reader calls ReadBatch only when pending is empty and copies each
// record before returning it. localSource serialises every method.
type bpfFrameReader struct {
	dev     *bpfdev.Device
	pending []bpfdev.Record
	last    counterPair
}

func (r *bpfFrameReader) readFrame(_ []byte) ([]byte, int, time.Time, bool, error) {
	if len(r.pending) == 0 {
		recs, err := r.dev.ReadBatch()
		if err != nil {
			return nil, 0, time.Time{}, false, errs.From(err).
				Code(ErrCodeSourceOpen).
				Msg("read bpf batch")
		}
		if len(recs) == 0 {
			return nil, 0, time.Time{}, true, errNoRecords
		}
		r.pending = recs
	}
	rec := r.pending[0]
	r.pending = r.pending[1:]
	return bytes.Clone(rec.Data), rec.WireLength, rec.Timestamp, false, nil
}

func (r *bpfFrameReader) stats() (uint64, uint64, error) {
	st, err := r.dev.Stats()
	if err != nil {
		return 0, 0, err
	}
	cur := counterPair{recv: st.Recv, drop: st.Drop}
	received, dropped := cur.deltaSince(r.last)
	r.last = cur
	return received, dropped, nil
}

// close drops the records of the current batch that were not read.
func (r *bpfFrameReader) close() error {
	r.pending = nil
	return r.dev.Close()
}
