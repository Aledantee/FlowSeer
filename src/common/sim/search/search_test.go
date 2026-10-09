package search

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func makeTestFabrics(t *testing.T) (cur, cand, curTwin, candTwin *fabric.Fabric, h1MAC, h2MAC netaddr.MAC) {
	t.Helper()
	h1MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	h2MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 2}

	buildFabric := func(vid2 uint16) *fabric.Fabric {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		pTable, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}

		v10 := vlan.ID(10)
		v2 := vlan.ID(vid2)
		swCfg := vswitch.Config{
			Ports: pTable,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{
						10: "VLAN10",
						20: "VLAN20",
					},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {
							PVID:     &v10,
							Untagged: []vlan.ID{10},
						},
						"1/1/2": {
							PVID:     &v2,
							Untagged: []vlan.ID{v2},
						},
					},
				},
			},
		}

		cfg := fabric.Config{
			PhyAssumption: &cablemodel.PhyAssumption{
				Medium: cablemodel.TwistedPair,
				Ethernet: phy.Ethernet{
					SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
					AutoNegotiationSupported: phy.CapabilitySupported,
					Setting:                  &phy.Setting{AutoNegotiation: true},
				},
			},
			Switches: map[string]vswitch.Config{
				"sw1": swCfg,
			},
			Hosts: map[string]fabric.Host{
				"h1": {Address: h1MAC},
				"h2": {Address: h2MAC},
			},
			Cables: []fabric.Cable{
				{
					Cable: cablemodel.Cable{LengthMeters: 5, Medium: cablemodel.TwistedPair},
					A:     fabric.Endpoint{Node: "h1"},
					B:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				},
				{
					Cable: cablemodel.Cable{LengthMeters: 5, Medium: cablemodel.TwistedPair},
					A:     fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
					B:     fabric.Endpoint{Node: "h2"},
				},
			},
		}

		fab, err := fabric.New(cfg)
		if err != nil {
			t.Fatalf("fabric.New: %v", err)
		}
		return fab
	}

	cur = buildFabric(10)
	curTwin = buildFabric(10)
	cand = buildFabric(20)
	candTwin = buildFabric(20)
	return
}

func make12TupleDomain(t *testing.T, h1MAC, h2MAC netaddr.MAC) *L2TrafficDomain {
	t.Helper()
	dom, err := NewL2TrafficDomain(L2TrafficDomainConfig{
		Sources: []fabric.Endpoint{
			{Node: "h1"},
			{Node: "h2"},
		},
		Destinations: []netaddr.MAC{
			h1MAC,
			h2MAC,
		},
		VLANs: []vlan.ID{10, 20, 30},
		Shapes: []FrameShape{
			{EtherType: ethernet.EtherTypeIPv4, Payload: []byte("pkt")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return dom
}

func TestSearchFullCoverage(t *testing.T) {
	t.Parallel()

	cur, _, curTwin, _, h1MAC, h2MAC := makeTestFabrics(t)
	dom := make12TupleDomain(t, h1MAC, h2MAC)

	res, err := Search(cur, curTwin, dom, Limits{Budget: 10})
	if err != nil {
		t.Fatal(err)
	}

	if res.Coverage.Tested != dom.Size() {
		t.Errorf("res.Coverage.Tested = %d, want %d", res.Coverage.Tested, dom.Size())
	}
	if !res.Coverage.Complete() {
		t.Errorf("res.Coverage.Complete() = false, want true")
	}
	if len(res.Remainder) != 0 {
		t.Errorf("len(res.Remainder) = %d, want 0", len(res.Remainder))
	}
	if len(res.Differences) != 0 {
		t.Errorf("len(res.Differences) = %d, want 0", len(res.Differences))
	}
	if res.EquivalentCount != dom.Size() {
		t.Errorf("res.EquivalentCount = %d, want %d", res.EquivalentCount, dom.Size())
	}
}

func TestSearchBudgetCutExactRemainder(t *testing.T) {
	t.Parallel()

	cur, _, curTwin, _, h1MAC, h2MAC := makeTestFabrics(t)
	dom := make12TupleDomain(t, h1MAC, h2MAC)

	var allTuples []Tuple
	dom.Enumerate(func(c Candidate) bool {
		allTuples = append(allTuples, c.Tuple)
		return true
	})

	lim := Limits{
		MaxCandidates: 8,
		Budget:        10,
	}
	res, err := Search(cur, curTwin, dom, lim)
	if err != nil {
		t.Fatal(err)
	}

	if res.Coverage.Tested != 8 {
		t.Fatalf("res.Coverage.Tested = %d, want 8", res.Coverage.Tested)
	}
	if res.Coverage.Total != 12 {
		t.Fatalf("res.Coverage.Total = %d, want 12", res.Coverage.Total)
	}
	if res.Coverage.Complete() {
		t.Errorf("res.Coverage.Complete() = true, want false")
	}

	expectedRemainder := allTuples[8:]
	if !slices.Equal(res.Remainder, expectedRemainder) {
		t.Fatalf("res.Remainder = %v, want %v", res.Remainder, expectedRemainder)
	}
}

func TestSearchDetectsDifferenceAndCapturesReplay(t *testing.T) {
	t.Parallel()

	cur, cand, _, _, h1MAC, h2MAC := makeTestFabrics(t)
	dom := make12TupleDomain(t, h1MAC, h2MAC)

	lim := Limits{
		MaxDifferences: 5,
		Budget:         10,
	}
	res, err := Search(cur, cand, dom, lim)
	if err != nil {
		t.Fatal(err)
	}

	if res.TotalDifferences == 0 {
		t.Fatalf("res.TotalDifferences = 0, want >= 1")
	}
	if len(res.Differences) == 0 {
		t.Fatalf("len(res.Differences) = 0, want >= 1")
	}

	diff := res.Differences[0]
	if diff.Tuple == "" {
		t.Errorf("retained difference tuple is empty")
	}
	if diff.Difference.Observable == "" {
		t.Errorf("retained difference observable is empty")
	}
	if diff.Replay[0].Contract != fabric.ReplayContract {
		t.Errorf("retained difference replay[0] contract = %q, want %q", diff.Replay[0].Contract, fabric.ReplayContract)
	}
	if diff.Replay[1].Contract != fabric.ReplayContract {
		t.Errorf("retained difference replay[1] contract = %q, want %q", diff.Replay[1].Contract, fabric.ReplayContract)
	}
}

func TestSearchRetainedDifferenceWallSummaryOverflow(t *testing.T) {
	t.Parallel()

	cur, cand, _, _, h1MAC, h2MAC := makeTestFabrics(t)
	dom := make12TupleDomain(t, h1MAC, h2MAC)

	lim := Limits{
		MaxDifferences: 1,
		Budget:         10,
	}
	res, err := Search(cur, cand, dom, lim)
	if err != nil {
		t.Fatal(err)
	}

	if res.TotalDifferences <= 1 {
		t.Fatalf("res.TotalDifferences = %d, want > 1 for overflow test", res.TotalDifferences)
	}
	if len(res.Differences) != 1 {
		t.Fatalf("len(res.Differences) = %d, want 1 (wall enforced)", len(res.Differences))
	}
	if res.DiscardedDifferences() != res.TotalDifferences-1 {
		t.Errorf("res.DiscardedDifferences() = %d, want %d", res.DiscardedDifferences(), res.TotalDifferences-1)
	}
}

func TestSearchFabricsUnchangedAfterSearch(t *testing.T) {
	t.Parallel()

	cur, cand, curTwin, candTwin, h1MAC, h2MAC := makeTestFabrics(t)
	dom := make12TupleDomain(t, h1MAC, h2MAC)

	// Run Search
	if _, err := Search(cur, cand, dom, Limits{Budget: 10}); err != nil {
		t.Fatal(err)
	}

	// Verify cur matches curTwin
	scenario := []fabric.Injection{
		{
			At:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       h2MAC,
				Src:       h1MAC,
				Tags:      []vlan.Tag{{VID: 10}},
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("verify"),
			},
		},
	}

	cmpCur := fabric.Compare(cur, curTwin, Candidate{Actions: []fabric.Action{{At: scenario[0].At, Kind: fabric.ActionInject, Inject: &scenario[0]}}}.ToScenario(10), 10)
	if cmpCur.Disposition != analysis.Equivalent {
		t.Errorf("cur diverged from curTwin after Search: %v (%v)", cmpCur.Disposition, cmpCur.Difference)
	}

	cmpCand := fabric.Compare(cand, candTwin, Candidate{Actions: []fabric.Action{{At: scenario[0].At, Kind: fabric.ActionInject, Inject: &scenario[0]}}}.ToScenario(10), 10)
	if cmpCand.Disposition != analysis.Equivalent {
		t.Errorf("cand diverged from candTwin after Search: %v (%v)", cmpCand.Disposition, cmpCand.Difference)
	}
}

func TestSearchEmptyDomain(t *testing.T) {
	t.Parallel()

	cur, cand, _, _, _, _ := makeTestFabrics(t)
	emptyDom, err := NewL2TrafficDomain(L2TrafficDomainConfig{})
	if err != nil {
		t.Fatal(err)
	}

	res, err := Search(cur, cand, emptyDom, Limits{Budget: 10})
	if err != nil {
		t.Fatal(err)
	}

	if res.Coverage.Tested != 0 || res.Coverage.Total != 0 {
		t.Errorf("res.Coverage = %v, want 0/0", res.Coverage)
	}
	if len(res.Remainder) != 0 {
		t.Errorf("len(res.Remainder) = %d, want 0", len(res.Remainder))
	}
	if len(res.Differences) != 0 {
		t.Errorf("len(res.Differences) = %d, want 0", len(res.Differences))
	}
}

func TestTimedFaultDomainBehavioralTime(t *testing.T) {
	t.Parallel()

	cur, cand, _, _, h1MAC, h2MAC := makeTestFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0
	t2 := t0.Add(50 * time.Millisecond)

	traffic := []fabric.Injection{
		{
			At:     t0.Add(10 * time.Millisecond),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       h2MAC,
				Src:       h1MAC,
				Tags:      []vlan.Tag{{VID: 10}},
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("timed-test"),
			},
		},
	}

	dom, err := NewTimedFaultDomain(TimedFaultDomainConfig{
		Faults: []fabric.FaultAction{
			{
				A:     fabric.Endpoint{Node: "h1"},
				B:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				Fault: cablemodel.Fault{Kind: cablemodel.FaultCut},
			},
		},
		Times:       []time.Time{t1, t2},
		BaseTraffic: []fabric.Action{{At: traffic[0].At, Kind: fabric.ActionInject, Inject: &traffic[0]}},
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := Search(cur, cand, dom, Limits{Budget: 20, MaxDifferences: -1})
	if err != nil {
		t.Fatal(err)
	}

	if res.Coverage.Tested != 2 {
		t.Fatalf("res.Coverage.Tested = %d, want 2", res.Coverage.Tested)
	}
	if res.EquivalentCount != 1 {
		t.Errorf("res.EquivalentCount = %d, want 1 (T1 fault before traffic causes identical drop on both)", res.EquivalentCount)
	}
	if res.TotalDifferences != 1 {
		t.Fatalf("res.TotalDifferences = %d, want 1 (T2 fault after traffic exposes VLAN divergence)", res.TotalDifferences)
	}
	if len(res.Differences) != 1 {
		t.Fatalf("len(res.Differences) = %d, want 1", len(res.Differences))
	}
	diffCand := res.Differences[0]
	if len(diffCand.Candidate.Actions) != 2 || diffCand.Candidate.Actions[1].Kind != fabric.ActionFault || !diffCand.Candidate.Actions[1].At.Equal(t2) {
		t.Errorf("divergence candidate fault at = %v, want %v", diffCand.Candidate.Actions, t2)
	}
}

func TestTimedFaultDomainUncabledPairError(t *testing.T) {
	t.Parallel()

	cur, _, curTwin, _, h1MAC, h2MAC := makeTestFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	traffic := []fabric.Injection{
		{
			At:     t0.Add(10 * time.Millisecond),
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:       h2MAC,
				Src:       h1MAC,
				Tags:      []vlan.Tag{{VID: 10}},
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("uncabled-test"),
			},
		},
	}

	dom, err := NewTimedFaultDomain(TimedFaultDomainConfig{
		Faults: []fabric.FaultAction{
			{
				A:     fabric.Endpoint{Node: "sw1", Port: "9/9/9"},
				B:     fabric.Endpoint{Node: "sw2", Port: "9/9/9"},
				Fault: cablemodel.Fault{Kind: cablemodel.FaultCut},
			},
		},
		Times:       []time.Time{t0},
		BaseTraffic: []fabric.Action{{At: traffic[0].At, Kind: fabric.ActionInject, Inject: &traffic[0]}},
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := Search(cur, curTwin, dom, Limits{Budget: 20})
	if err != nil {
		t.Fatal(err)
	}

	if res.Coverage.Tested != 1 {
		t.Fatalf("res.Coverage.Tested = %d, want 1", res.Coverage.Tested)
	}
	if res.InconclusiveCount != 0 || res.Errors != 1 || res.Err == nil {
		t.Errorf("inconclusive/errors/err = %d/%d/%v, want 0/1/error", res.InconclusiveCount, res.Errors, res.Err)
	}
	if res.EquivalentCount != 0 {
		t.Errorf("res.EquivalentCount = %d, want 0", res.EquivalentCount)
	}
	if res.TotalDifferences != 0 {
		t.Errorf("res.TotalDifferences = %d, want 0", res.TotalDifferences)
	}
}

type candidateList []Candidate

func TestSearchRejectsExecutableConfigureCandidate(t *testing.T) {
	current, _, twin, _, _, _ := makeTestFabrics(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := Candidate{Tuple: "configure", Actions: []fabric.Action{{At: at, Kind: fabric.ActionConfigure, Configure: &fabric.ConfigureAction{Node: "sw1", Config: current.Config().Switches["sw1"]}}}}
	comparison := fabric.Compare(current, twin, input.ToScenario(30), 30)
	if comparison.Err != nil || comparison.Disposition != analysis.Equivalent {
		t.Fatalf("fabric comparison = %+v, want executable equivalent configure action", comparison)
	}
	result, err := Search(current, twin, candidateList{input}, Limits{Budget: 30})
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage != (Coverage{Tested: 1, Total: 1}) || result.Errors != 1 || result.Err == nil || result.Err.Error() != `candidate "configure": candidate action 0 must be injection or fault` || result.EquivalentCount != 0 || result.InconclusiveCount != 0 || result.TotalDifferences != 0 {
		t.Fatalf("result = %+v, want one candidate action-kind error", result)
	}
}

func (d candidateList) Size() int { return len(d) }
func (d candidateList) Enumerate(yield func(Candidate) bool) {
	for _, c := range d {
		if !yield(c) {
			return
		}
	}
}

type enumerationProbe struct{ calls int }

func (p *enumerationProbe) Size() int                      { p.calls++; return 1 }
func (p *enumerationProbe) Enumerate(func(Candidate) bool) { p.calls++ }

func TestSearchRejectsLimitsBeforeEnumeration(t *testing.T) {
	for _, limits := range []Limits{{Budget: 0}, {Budget: -1}, {Budget: 1, MaxCandidates: -1}, {Budget: 1, MaxDifferences: -2}} {
		dom := &enumerationProbe{}
		result, err := Search(nil, nil, dom, limits)
		if err == nil || result.Coverage.Tested != 0 || dom.calls != 0 {
			t.Fatalf("limits %+v: result=%+v err=%v calls=%d", limits, result, err, dom.calls)
		}
	}
	if err := (Limits{Budget: 1, MaxDifferences: -1}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSearchAccountsEveryCandidateAndFirstError(t *testing.T) {
	cur, cand, _, _, src, dst := makeTestFabrics(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	injection := func(node string, port string) fabric.Action {
		return fabric.Action{At: at, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: node, Port: port}, Frame: ethernet.Frame{Src: src, Dst: dst, Payload: []byte("accounting")}}}
	}
	different := injection("h1", "")
	equivalent := injection("sw1", "1/1/1")
	equivalent.Inject.Frame.Tags = []vlan.Tag{{VID: 30}}
	invalid := injection("missing-first", "")
	incomplete := Candidate{Tuple: "partial"}
	for i := range 12 {
		action := injection("h1", "")
		action.At = at.Add(time.Duration(i) * time.Millisecond)
		incomplete.Actions = append(incomplete.Actions, action)
	}
	domain := candidateList{{Tuple: "equivalent", Actions: []fabric.Action{equivalent}}, {Tuple: "invalid-first", Actions: []fabric.Action{invalid}}, {Tuple: "different", Actions: []fabric.Action{different}}, incomplete}
	result, err := Search(cur, cand, domain, Limits{Budget: 10, MaxDifferences: -1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage != (Coverage{Tested: 4, Total: 4}) || result.EquivalentCount != 1 || result.TotalDifferences != 1 || result.InconclusiveCount != 1 || result.Errors != 1 {
		t.Fatalf("accounting=%+v", result)
	}
	if result.Coverage.Tested != result.EquivalentCount+result.TotalDifferences+result.InconclusiveCount+result.Errors {
		t.Fatal("candidate missing from accounting")
	}
	if result.Err == nil || errors.Unwrap(result.Err) == nil || !strings.Contains(result.Err.Error(), "invalid-first") || !strings.Contains(result.Err.Error(), "missing-first") {
		t.Fatalf("first wrapped error=%v", result.Err)
	}
	if len(result.Differences) != 1 || result.Differences[0].Tuple != "different" || result.Differences[0].Candidate.Actions[0].Index != 1 {
		t.Fatalf("retained differences=%v", result.Differences)
	}
	invalidSecond := injection("missing-second", "")
	domain = append(domain, Candidate{Tuple: "invalid-second", Actions: []fabric.Action{invalidSecond}})
	result, err = Search(cur, cand, domain, Limits{Budget: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Errors != 2 || result.Coverage.Tested != 5 || !strings.Contains(result.Err.Error(), "invalid-first") || strings.Contains(result.Err.Error(), "invalid-second") {
		t.Fatalf("first-error retention=%+v", result)
	}
	result, err = Search(cur, cand, domain, Limits{Budget: 10, MaxCandidates: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage.Tested != 2 || result.Errors != 1 || !slices.Equal(result.Remainder, []Tuple{"different", "partial", "invalid-second"}) || result.Coverage.Tested+len(result.Remainder) != result.Coverage.Total {
		t.Fatalf("limited accounting=%+v", result)
	}
}

func TestSearchCountsOnlyAndUnlimitedRetention(t *testing.T) {
	cur, cand, _, _, src, dst := makeTestFabrics(t)
	domain := make12TupleDomain(t, src, dst)
	for _, limit := range []int{0, -1, 1} {
		result, err := Search(cur, cand, domain, Limits{Budget: 10, MaxDifferences: limit})
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalDifferences != 12 {
			t.Fatalf("differences=%d, want 12", result.TotalDifferences)
		}
		want := limit
		if limit == -1 {
			want = 12
		}
		if len(result.Differences) != want || result.DiscardedDifferences() != 12-want {
			t.Fatalf("limit %d retained/discarded=%d/%d", limit, len(result.Differences), result.DiscardedDifferences())
		}
	}
}

func TestTimedFaultDomainInjectsBeforeFaultAtEqualTime(t *testing.T) {
	cur, _, twin, _, src, dst := makeTestFabrics(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	injection := fabric.Action{At: at, Index: 7, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Src: src, Dst: dst, Payload: []byte("tie")}}}
	domain, err := NewTimedFaultDomain(TimedFaultDomainConfig{BaseTraffic: []fabric.Action{injection}, Times: []time.Time{at}, Faults: []fabric.FaultAction{{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Fault: cablemodel.Fault{Kind: cablemodel.FaultCut}}}})
	if err != nil {
		t.Fatal(err)
	}
	var candidate Candidate
	domain.Enumerate(func(c Candidate) bool { candidate = c; return false })
	scenario := candidate.ToScenario(20)
	if len(scenario.Actions) != 2 || scenario.Actions[0].Kind != fabric.ActionInject || scenario.Actions[0].Index != 7 || scenario.Actions[1].Kind != fabric.ActionFault || scenario.Actions[1].Index != 8 {
		t.Fatalf("tied actions=%+v", scenario.Actions)
	}
	comparison := fabric.Compare(cur, twin, scenario, 20)
	if comparison.Err != nil || comparison.Disposition != analysis.Inconclusive || len(comparison.Current) != 1 {
		t.Fatalf("comparison=%+v", comparison)
	}
	// The first host injection releases while its link is still up.
	entries := comparison.Current[0].Entries
	if len(entries) != 4 || entries[1].Kind != fabric.EntryCrossing {
		t.Fatalf("entries=%+v, want host emission before fault", entries)
	}
	reversed := candidate.Clone()
	reversed.Actions[0].Index = 8
	reversed.Actions[1].Index = 7
	reverseComparison := fabric.Compare(cur, twin, reversed.ToScenario(20), 20)
	if reverseComparison.Err != nil || len(reverseComparison.Current) != 1 || len(reverseComparison.Current[0].Entries) != 2 || reverseComparison.Current[0].Entries[1].Kind != fabric.EntryDrop {
		t.Fatalf("fault-first did not drop injection: %+v", reverseComparison.Current)
	}
}
