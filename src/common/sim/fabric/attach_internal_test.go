package fabric_test

import (
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/stream"
)

type scriptedSource struct {
	offsets []time.Duration
	n       int
	invalid bool
}

func probeFabricAttachments(t *testing.T) {
	fab := newTestFabricForFork(t)
	(*fabric.ExportFabricAttachments(fab)) = []fabric.ExportAttachedSource{fabric.MakeTestAttachedSource(fabric.ExportAttachedSourceFields{
		Attachment: fabric.StreamAttachment{Source: &scriptedSource{offsets: []time.Duration{time.Microsecond}}},
		Frame:      ethernet.Frame{Payload: []byte{1}},
		Peeked:     true,
	})}
	fork := fab.Fork()
	fabric.ExportAttachedSourceFrame(&(*fabric.ExportFabricAttachments(fork))[0]).Src[0] = 9
	if fabric.ExportAttachedSourceFrame(&(*fabric.ExportFabricAttachments(fab))[0]).Src[0] != 0 {
		t.Error("source peek frame value changed through fork")
	}
	if _, _, ok := fabric.ExportAttachedSourceAttachment(&(*fabric.ExportFabricAttachments(fork))[0]).Source.Next(); !ok {
		t.Fatal("fork source has no next frame")
	}
	if got := fabric.ExportAttachedSourceAttachment(&(*fabric.ExportFabricAttachments(fab))[0]).Source.(*scriptedSource).n; got != 0 {
		t.Errorf("source cursor = %d, want 0", got)
	}
}

func (s *scriptedSource) Next() (time.Duration, ethernet.Frame, bool) {
	if s.n >= len(s.offsets) {
		return 0, ethernet.Frame{}, false
	}
	at := s.offsets[s.n]
	s.n++
	frame := ethernet.Frame{
		Src: netaddr.MAC{2, 0, 0, 0, 0, 1}, Dst: netaddr.MAC{2, 0, 0, 0, 0, 2},
		Payload: make([]byte, 46),
	}
	if s.invalid && s.n == 2 {
		frame.Tags = []vlan.Tag{{PCP: 8}}
	}
	return at, frame, true
}

func TestStreamFaultEndsOneStream(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "backward", true: "codec"}[invalid], func(t *testing.T) {
			fab, src, dst := newTwoSwitchTopology(t, cablemodel.Fault{})
			offsets := []time.Duration{time.Millisecond, 0, 3 * time.Millisecond}
			if invalid {
				offsets[1] = 2 * time.Millisecond
			}
			bad := &scriptedSource{offsets: offsets, invalid: invalid}
			if err := fab.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Source: bad, Flow: 1}); err != nil {
				t.Fatal(err)
			}
			if err := fab.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "h1"}, Source: streamSource(t, flowFrame(src, dst), 2, 1000), Flow: 2}); err != nil {
				t.Fatal(err)
			}
			res := fab.Run(fabric.RunOptions{Budget: 1000, Window: 0})
			if res.Stop != fabric.StopQueueDrained || res.Err != nil {
				t.Fatalf("result = %+v, want drained without scheduling fault", res)
			}
			flows := fab.Flows()
			if flows[1].Offered != 1 || len(flows[1].Metadata.Issues()) != 1 || flows[2].Delivered["h2"] != 2 {
				t.Fatalf("flows = %+v, want failed flow issue and two healthy deliveries", flows)
			}
			if bad.n != 2 {
				t.Fatalf("faulted source pulled %d frames, want 2", bad.n)
			}
			if len(res.Issues) != 1 {
				t.Fatalf("run issues = %+v, want stream issue", res.Issues)
			}
		})
	}
}

func TestUnknownLinkPullsOne(t *testing.T) {
	fab, _, _ := newTwoSwitchTopology(t, cablemodel.Fault{})
	cfg := assembly.Spec(fab).Config()
	cfg.PhyAssumption = nil
	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := fab.Links()[0].A.Oper; got != port.Unknown {
		t.Fatalf("link = %s, want Unknown", got)
	}
	source := &scriptedSource{offsets: []time.Duration{0, 0, 0, 0}}
	if err := fab.AttachStream(fabric.StreamAttachment{Origin: fabric.Endpoint{Node: "h1"}, Source: source, Flow: 1}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, ok := fab.Step(); !ok {
			t.Fatalf("Step %d failed", i)
		}
		if got := fab.Flows()[1].Unresolved; got != uint64(i) {
			t.Fatalf("step %d unresolved = %d, want %d", i, got, i)
		}
		if source.n != i+1 {
			t.Fatalf("step %d source pulls = %d, want %d including next queued frame", i, source.n, i+1)
		}
	}
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

func TestStreamEventFollowsWakeAtSameTime(t *testing.T) {
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
	if got := len(attachedJourneys(fab)); got != 0 {
		t.Fatalf("journeys after wake = %d, want 0", got)
	}
	if _, ok := fab.Step(); !ok {
		t.Fatal("Step returned no stream event")
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
	left.Run(fabric.RunOptions{Budget: 1000, Window: 0})
	right.Run(fabric.RunOptions{Budget: 1000, Window: 0})
	if got, want := attachedJourneys(left), attachedJourneys(right); !reflect.DeepEqual(got, want) {
		t.Errorf("left fork stream report = %+v, want %+v", got, want)
	}
	if got := len(attachedJourneys(left)); got != 3 {
		t.Errorf("fork frames = %d, want 3", got)
	}
}
