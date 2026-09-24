package fabric

import (
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
)

type scriptedSource struct {
	offsets []time.Duration
	n       int
}

func init() {
	fabricFieldClasses["attachments"] = classDeepCopied
	fabricFieldClasses["pendingHosts"] = classDeepCopied
	fabricDeepCopiedProbes["attachments"] = probeFabricAttachments
	fabricDeepCopiedProbes["pendingHosts"] = probeFabricPendingHosts
}

func probeFabricAttachments(t *testing.T) {
	fab := newTestFabricForFork(t)
	fab.attachments = []attachedSource{{
		attachment: StreamAttachment{Source: &scriptedSource{offsets: []time.Duration{time.Microsecond}}},
		frame:      ethernet.Frame{Payload: []byte{1}},
		peeked:     true,
	}}
	fork := fab.Fork()
	fork.attachments[0].frame.Payload[0] = 2
	if got := fab.attachments[0].frame.Payload[0]; got != 1 {
		t.Errorf("source peek payload = %d, want 1", got)
	}
	if _, _, ok := fork.attachments[0].attachment.Source.Next(); !ok {
		t.Fatal("fork source has no next frame")
	}
	if got := fab.attachments[0].attachment.Source.(*scriptedSource).n; got != 0 {
		t.Errorf("source cursor = %d, want 0", got)
	}
}

func probeFabricPendingHosts(t *testing.T) {
	fab := newTestFabricForFork(t)
	fab.pendingHosts = []pendingHostInjection{{frame: ethernet.Frame{Payload: []byte{1}}}}
	fork := fab.Fork()
	fork.pendingHosts[0].frame.Payload[0] = 2
	if got := fab.pendingHosts[0].frame.Payload[0]; got != 1 {
		t.Errorf("source pending frame payload = %d, want 1", got)
	}
}

func (s *scriptedSource) Next() (time.Duration, ethernet.Frame, bool) {
	if s.n >= len(s.offsets) {
		return 0, ethernet.Frame{}, false
	}
	at := s.offsets[s.n]
	s.n++
	return at, ethernet.Frame{
		Src: netaddr.MAC{2, 0, 0, 0, 0, 1}, Dst: netaddr.MAC{2, 0, 0, 0, 0, 2},
		Payload: make([]byte, 46),
	}, true
}

func (s *scriptedSource) Clone() stream.Source {
	clone := *s
	return &clone
}

func attachScripted(t *testing.T, fab *Fabric, offsets ...time.Duration) {
	t.Helper()
	if err := fab.AttachStream(StreamAttachment{
		Origin: Endpoint{Node: "sw1", Port: "1/1/1"},
		Source: &scriptedSource{offsets: offsets}, Flow: 1, Retention: RetainJourney,
	}); err != nil {
		t.Fatalf("AttachStream: %v", err)
	}
}

func attachedJourneys(fab *Fabric) []Journey {
	var out []Journey
	for _, journey := range fab.Report() {
		if journey.Injection.Flow == 1 {
			out = append(out, journey)
		}
	}
	return out
}

func TestPullSourcesUsesEarliestArrivalAsInclusiveBoundary(t *testing.T) {
	t0 := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	fab := newTestSTPFabric(t, t0, 4096, 8192)
	fab.queue = nil
	fab.wakeItems = nil
	fab.wakes = nil
	fab.enqueue(Arrival{At: t0.Add(10 * time.Microsecond), Kind: ArrivalWake, Device: "unused"})
	attachScripted(t, fab, 10*time.Microsecond, 11*time.Microsecond)

	if _, ok := fab.Step(); !ok {
		t.Fatal("Step returned no arrival")
	}
	got := attachedJourneys(fab)
	if len(got) != 1 || !got[0].Injection.At.Equal(t0.Add(10*time.Microsecond)) {
		t.Errorf("injections after boundary step = %d, want one at boundary", len(got))
	}
}

func TestStepPullsFromEmptyQueue(t *testing.T) {
	t0 := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	fab := newTestSTPFabric(t, t0, 4096, 8192)
	fab.queue = nil
	fab.wakeItems = nil
	fab.wakes = nil
	attachScripted(t, fab, 5*time.Microsecond)

	if _, ok := fab.Step(); !ok {
		t.Fatal("Step returned no arrival for attached source")
	}
	if got := attachedJourneys(fab); len(got) != 1 || !got[0].Injection.At.Equal(t0.Add(5*time.Microsecond)) {
		t.Errorf("injections = %d, want one at t0 + 5µs", len(got))
	}
}

func TestConvergenceWaitsForAttachedSource(t *testing.T) {
	t0 := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	fab := newTestSTPFabric(t, t0, 4096, 8192)
	attachScripted(t, fab, 100*time.Second)

	result := fab.run(20, 3)
	if result.Stop != StopBudget {
		t.Errorf("Run stop = %s, want budget while source remains", result.Stop)
	}
	if got := len(attachedJourneys(fab)); got != 0 {
		t.Errorf("reported frames = %d, want source still pending", got)
	}
}

func TestForkClonesHalfPulledSource(t *testing.T) {
	t0 := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	fab := newTestSTPFabric(t, t0, 4096, 8192)
	attachScripted(t, fab, 0, 100*time.Microsecond, 200*time.Microsecond)
	if _, ok := fab.Step(); !ok {
		t.Fatal("Step returned no arrival")
	}
	left, right := fab.Fork(), fab.Fork()
	left.Run(1000)
	right.Run(1000)
	if got, want := attachedJourneys(left), attachedJourneys(right); !reflect.DeepEqual(got, want) {
		t.Errorf("left fork stream report = %+v, want %+v", got, want)
	}
	if got := len(attachedJourneys(left)); got != 3 {
		t.Errorf("fork frames = %d, want 3", got)
	}
}
