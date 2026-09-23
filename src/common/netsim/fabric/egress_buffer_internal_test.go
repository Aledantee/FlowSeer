package fabric

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// bufferAccountingFabric returns a one-port fabric whose endpoint is already
// busy, so enqueueEgress records egress depth instead of draining the queue
// through a link it does not have. A nil buffer leaves the queue unstated.
func bufferAccountingFabric(t *testing.T, buffer *uint64) (*Fabric, Endpoint) {
	t.Helper()

	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	var queues map[string]traffic.PortQueues
	if buffer != nil {
		queues = map[string]traffic.PortQueues{
			"1/1/1": {BufferOctets: map[vlan.PCP]uint64{0: *buffer}},
		}
	}

	fab, err := New(Config{Switches: map[string]vswitch.Config{
		"sw1": {Ports: ports, Traffic: &traffic.Config{Queues: queues}},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fab.initRunState()

	ep := Endpoint{Node: "sw1", Port: "1/1/1"}
	fab.busyUntil[ep] = fab.clock.Add(time.Hour)

	return fab, ep
}

func egressBufferFrame() ethernet.Frame {
	return ethernet.Frame{Src: netaddr.MAC{0x02}, Dst: netaddr.MAC{0x03}, Payload: make([]byte, 1000)}
}

// TestEgressQueueDepthAndPeak covers the octet accounting: ten 1014-octet
// frames make a depth and peak of ten frames' octets, the pop returns the
// depth to zero, and the peak survives the drain.
func TestEgressQueueDepthAndPeak(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	frame := egressBufferFrame()
	journey := &Journey{}
	for range 10 {
		fab.enqueueEgress(fab.clock, ep, ep.Port, frame, 1, 1, journey, 0, "")
	}

	q := fab.egress[ep]
	if got, want := q.depth[0], uint64(10*1014); got != want {
		t.Fatalf("depth after ten enqueues = %d, want %d", got, want)
	}
	if got, want := q.peak[0], uint64(10*1014); got != want {
		t.Fatalf("peak after ten enqueues = %d, want %d", got, want)
	}

	fab.busyUntil[ep] = time.Time{}
	fab.removeDequeue(ep)
	fab.serve(fab.clock.Add(2*time.Hour), ep)
	if got := q.depth[0]; got != 0 {
		t.Errorf("depth after the queue drained = %d, want 0", got)
	}
	if got, want := q.peak[0], uint64(10*1014); got != want {
		t.Errorf("peak after the queue drained = %d, want it unchanged at %d", got, want)
	}
}

// TestEgressStatedBufferBoundary covers the admit line: a stated buffer of
// 2028 admits a second 1014-octet frame because depth+octets equals it, while
// 2027 refuses it because depth+octets exceeds it. A wire-octet count would
// refuse at 2028, since two frames are 2076 wire octets together.
func TestEgressStatedBufferBoundary(t *testing.T) {
	for _, tc := range []struct {
		buffer uint64
		admits int
	}{
		{buffer: 2028, admits: 2},
		{buffer: 2027, admits: 1},
	} {
		fab, ep := bufferAccountingFabric(t, &tc.buffer)
		frame := egressBufferFrame()
		journey := &Journey{}
		for range 2 {
			fab.enqueueEgress(fab.clock, ep, ep.Port, frame, 1, 1, journey, 0, "")
		}

		if got, want := fab.egress[ep].depth[0], uint64(tc.admits*1014); got != want {
			t.Errorf("buffer %d: depth = %d, want %d", tc.buffer, got, want)
		}
		if got, want := len(journey.Entries), 2-tc.admits; got != want {
			t.Errorf("buffer %d: drop entries = %d, want %d", tc.buffer, got, want)
		}
		var outDiscards, queueFull uint64
		if c := fab.counters[ep]; c != nil {
			outDiscards = c.OutDiscards
			queueFull = c.Discards[traffic.ReasonQueueFull]
		}
		if got, want := outDiscards, uint64(2-tc.admits); got != want {
			t.Errorf("buffer %d: OutDiscards = %d, want %d", tc.buffer, got, want)
		}
		if got, want := queueFull, uint64(2-tc.admits); got != want {
			t.Errorf("buffer %d: Discards[queue-full] = %d, want %d", tc.buffer, got, want)
		}
	}
}

// TestEgressForkKeepsOwnPeak covers the fork: a later enqueue on the fork
// raises the fork's peak without moving the source's.
func TestEgressForkKeepsOwnPeak(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	frame := egressBufferFrame()
	fab.enqueueEgress(fab.clock, ep, ep.Port, frame, 1, 1, &Journey{}, 0, "")

	fork := fab.Fork()
	forkJourney := &Journey{}
	for range 3 {
		fork.enqueueEgress(fork.clock, ep, ep.Port, frame, 1, 1, forkJourney, 0, "")
	}

	if got := fab.egress[ep].peak[0]; got != 1014 {
		t.Errorf("source peak = %d, want 1014", got)
	}
	if got, want := fork.egress[ep].peak[0], uint64(4*1014); got != want {
		t.Errorf("fork peak = %d, want %d", got, want)
	}
}
