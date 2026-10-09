package fabric_test

import (
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/stream"
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
	(*fabric.ExportFabricAttachments(fab)) = []fabric.ExportAttachedSource{fabric.MakeTestAttachedSource(fabric.ExportAttachedSourceFields{
		Attachment: fabric.StreamAttachment{Source: &scriptedSource{offsets: []time.Duration{time.Microsecond}}},
		Frame:      ethernet.Frame{Payload: []byte{1}},
		Peeked:     true,
	})}
	fork := fab.Fork()
	fabric.ExportAttachedSourceFrame(&(*fabric.ExportFabricAttachments(fork))[0]).Payload[0] = 2
	if got := fabric.ExportAttachedSourceFrame(&(*fabric.ExportFabricAttachments(fab))[0]).Payload[0]; got != 1 {
		t.Errorf("source peek payload = %d, want 1", got)
	}
	if _, _, ok := fabric.ExportAttachedSourceAttachment(&(*fabric.ExportFabricAttachments(fork))[0]).Source.Next(); !ok {
		t.Fatal("fork source has no next frame")
	}
	if got := fabric.ExportAttachedSourceAttachment(&(*fabric.ExportFabricAttachments(fab))[0]).Source.(*scriptedSource).n; got != 0 {
		t.Errorf("source cursor = %d, want 0", got)
	}
}

func probeFabricPendingHosts(t *testing.T) {
	fab := newTestFabricForFork(t)
	(*fabric.ExportFabricPendingHosts(fab)) = []fabric.ExportPendingHostInjection{fabric.MakeTestPendingHostInjection(fabric.ExportPendingHostInjectionFields{Frame: ethernet.Frame{Payload: []byte{1}}})}
	fork := fab.Fork()
	fabric.ExportPendingHostInjectionFrame(&(*fabric.ExportFabricPendingHosts(fork))[0]).Payload[0] = 2
	if got := fabric.ExportPendingHostInjectionFrame(&(*fabric.ExportFabricPendingHosts(fab))[0]).Payload[0]; got != 1 {
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

func attachScripted(t *testing.T, fab *fabric.Fabric, offsets ...time.Duration) {
	t.Helper()
	if err := fab.AttachStream(fabric.StreamAttachment{
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Source: &scriptedSource{offsets: offsets}, Flow: 1, Retention: fabric.RetainJourney,
	}); err != nil {
		t.Fatalf("AttachStream: %v", err)
	}
}

func attachedJourneys(fab *fabric.Fabric) []fabric.Journey {
	var out []fabric.Journey
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
	(*fabric.ExportFabricQueue(fab)) = nil
	(*fabric.ExportFabricWakeItems(fab)) = nil
	(*fabric.ExportFabricWakes(fab)) = nil
	fabric.ExportFabricEnqueue(fab, fabric.MakeTestArrival(fabric.ExportArrivalFields{At: t0.Add(10 * time.Microsecond), Kind: fabric.ArrivalWake, Device: "unused"}))
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
	(*fabric.ExportFabricQueue(fab)) = nil
	(*fabric.ExportFabricWakeItems(fab)) = nil
	(*fabric.ExportFabricWakes(fab)) = nil
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

	result := fabric.ExportFabricRun(fab, 20, 3)
	if result.Stop != fabric.StopBudget {
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
