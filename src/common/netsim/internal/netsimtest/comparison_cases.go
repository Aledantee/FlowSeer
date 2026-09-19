package netsimtest

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// ComparisonResult captures the unified outcome of evaluating either a vswitch or fabric comparison.
type ComparisonResult struct {
	Disposition analysis.Disposition
	Observable  string
	Current     string
	Expected    string
}

// ComparisonCase defines a test case for comparing network simulation components (switch or fabric),
// verifying order-independence, determinism under randomized map and slice insertion order, and the
// non-consuming execution guarantee.
type ComparisonCase struct {
	Name                string
	ExpectedDisposition analysis.Disposition
	ExpectedObservable  string
	Execute             func(candidateFirst bool, rng *rand.Rand) (ComparisonResult, error)
	VerifyNonConsuming  func() error
}

func shuffleSlice[T any](rng *rand.Rand, items []T) []T {
	if rng == nil || len(items) <= 1 {
		return slices.Clone(items)
	}
	cp := slices.Clone(items)
	rng.Shuffle(len(cp), func(i, j int) { cp[i], cp[j] = cp[j], cp[i] })
	return cp
}

func shuffleMap[K comparable, V any](rng *rand.Rand, src map[K]V) map[K]V {
	if src == nil {
		return nil
	}
	out := make(map[K]V, len(src))
	keys := make([]K, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	if rng != nil && len(keys) > 1 {
		rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	}
	for _, k := range keys {
		out[k] = src[k]
	}
	return out
}

func buildPortTable(portList []port.Port, rng *rand.Rand) (port.Table, error) {
	b := port.NewBuilder()
	for _, p := range shuffleSlice(rng, portList) {
		b.Add(p)
	}
	return b.Build()
}

// ComparisonCorpus returns the suite of comparison cases asserting invariant properties across
// vswitch.Compare and fabric.Compare: determinism under randomized map and slice insertion order,
// current-first versus candidate-first execution invariance, and the non-consuming simulation guarantee.
func ComparisonCorpus() []ComparisonCase {
	now := time.Unix(1700000000, 0)
	mac1 := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x01, 0x01}
	mac2 := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x01, 0x02}
	vid10 := vlan.ID(10)

	testFrame := ethernet.Frame{
		Dst:       mac2,
		Src:       mac1,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test-payload"),
	}

	buildSwitchPair := func(
		portsA, portsB []port.Port,
		seedsA, seedsB []bridge.Seed,
		vlanCfgA, vlanCfgB *bridge.VLAN,
		rng *rand.Rand,
	) (*vswitch.Switch, *vswitch.Switch, error) {
		tblA, err := buildPortTable(portsA, rng)
		if err != nil {
			return nil, nil, err
		}
		tblB, err := buildPortTable(portsB, rng)
		if err != nil {
			return nil, nil, err
		}

		makeBridgeCfg := func(vc *bridge.VLAN) *bridge.Config {
			if vc == nil {
				return &bridge.Config{}
			}
			cloneVC := &bridge.VLAN{
				Table:       shuffleMap(rng, vc.Table),
				Switchports: shuffleMap(rng, vc.Switchports),
			}
			return &bridge.Config{VLAN: cloneVC}
		}

		swA, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
			Config: vswitch.Config{Ports: tblA, Bridge: makeBridgeCfg(vlanCfgA)},
			Seeds:  shuffleSlice(rng, seedsA),
		})
		if err != nil {
			return nil, nil, err
		}

		swB, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
			Config: vswitch.Config{Ports: tblB, Bridge: makeBridgeCfg(vlanCfgB)},
			Seeds:  shuffleSlice(rng, seedsB),
		})
		if err != nil {
			return nil, nil, err
		}

		return swA, swB, nil
	}

	// -------------------------------------------------------------------------
	// Case 1: vswitch/equivalent-complete
	// -------------------------------------------------------------------------
	c1Ports := []port.Port{
		{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
		{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
	}
	c1Seeds := []bridge.Seed{{FID: 10, MAC: mac2, Port: "1/1/2", Lifetime: bridge.Static}}
	c1VLAN := &bridge.VLAN{
		Table: map[vlan.ID]string{10: "prod"},
		Switchports: map[string]bridge.Switchport{
			"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
			"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
		},
	}
	c1SwA, c1SwB, err := buildSwitchPair(c1Ports, c1Ports, c1Seeds, c1Seeds, c1VLAN, c1VLAN, nil)
	mustNil(err)
	c1InitSpecA, c1InitSpecB := c1SwA.Spec(), c1SwB.Spec()
	c1InitEntriesA, c1InitEntriesB := c1SwA.Entries(), c1SwB.Entries()

	case1 := ComparisonCase{
		Name:                "vswitch/equivalent-complete",
		ExpectedDisposition: analysis.Equivalent,
		ExpectedObservable:  "",
		Execute: func(candidateFirst bool, rng *rand.Rand) (ComparisonResult, error) {
			swA, swB := c1SwA, c1SwB
			if rng != nil {
				var err error
				swA, swB, err = buildSwitchPair(c1Ports, c1Ports, c1Seeds, c1Seeds, c1VLAN, c1VLAN, rng)
				if err != nil {
					return ComparisonResult{}, err
				}
			}
			var cmp vswitch.Comparison
			if candidateFirst {
				cmp = vswitch.Compare(swB, swA, now, "1/1/1", testFrame)
			} else {
				cmp = vswitch.Compare(swA, swB, now, "1/1/1", testFrame)
			}
			return ComparisonResult{
				Disposition: cmp.Disposition,
				Observable:  cmp.Difference.Observable,
				Current:     cmp.Difference.Current,
				Expected:    cmp.Difference.Expected,
			}, nil
		},
		VerifyNonConsuming: func() error {
			if !reflect.DeepEqual(c1SwA.Spec(), c1InitSpecA) {
				return fmt.Errorf("c1SwA spec was mutated")
			}
			if !reflect.DeepEqual(c1SwB.Spec(), c1InitSpecB) {
				return fmt.Errorf("c1SwB spec was mutated")
			}
			if !reflect.DeepEqual(c1SwA.Entries(), c1InitEntriesA) {
				return fmt.Errorf("c1SwA entries were mutated")
			}
			if !reflect.DeepEqual(c1SwB.Entries(), c1InitEntriesB) {
				return fmt.Errorf("c1SwB entries were mutated")
			}
			return nil
		},
	}

	// -------------------------------------------------------------------------
	// Case 2: vswitch/port-down-different
	// -------------------------------------------------------------------------
	c2PortsA := []port.Port{
		{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
		{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
	}
	c2PortsB := []port.Port{
		{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
		{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Down},
	}
	c2SwA, c2SwB, err := buildSwitchPair(c2PortsA, c2PortsB, c1Seeds, c1Seeds, c1VLAN, c1VLAN, nil)
	mustNil(err)
	c2InitSpecA, c2InitSpecB := c2SwA.Spec(), c2SwB.Spec()
	c2InitEntriesA, c2InitEntriesB := c2SwA.Entries(), c2SwB.Entries()

	case2 := ComparisonCase{
		Name:                "vswitch/port-down-different",
		ExpectedDisposition: analysis.Different,
		ExpectedObservable:  "outcome",
		Execute: func(candidateFirst bool, rng *rand.Rand) (ComparisonResult, error) {
			swA, swB := c2SwA, c2SwB
			if rng != nil {
				var err error
				swA, swB, err = buildSwitchPair(c2PortsA, c2PortsB, c1Seeds, c1Seeds, c1VLAN, c1VLAN, rng)
				if err != nil {
					return ComparisonResult{}, err
				}
			}
			var cmp vswitch.Comparison
			if candidateFirst {
				cmp = vswitch.Compare(swB, swA, now, "1/1/1", testFrame)
			} else {
				cmp = vswitch.Compare(swA, swB, now, "1/1/1", testFrame)
			}
			return ComparisonResult{
				Disposition: cmp.Disposition,
				Observable:  cmp.Difference.Observable,
				Current:     cmp.Difference.Current,
				Expected:    cmp.Difference.Expected,
			}, nil
		},
		VerifyNonConsuming: func() error {
			if !reflect.DeepEqual(c2SwA.Spec(), c2InitSpecA) {
				return fmt.Errorf("c2SwA spec was mutated")
			}
			if !reflect.DeepEqual(c2SwB.Spec(), c2InitSpecB) {
				return fmt.Errorf("c2SwB spec was mutated")
			}
			if !reflect.DeepEqual(c2SwA.Entries(), c2InitEntriesA) {
				return fmt.Errorf("c2SwA entries were mutated")
			}
			if !reflect.DeepEqual(c2SwB.Entries(), c2InitEntriesB) {
				return fmt.Errorf("c2SwB entries were mutated")
			}
			return nil
		},
	}

	// -------------------------------------------------------------------------
	// Case 3: vswitch/inconclusive-unknown-status
	// -------------------------------------------------------------------------
	c3Ports := []port.Port{
		{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
		{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown},
	}
	c3SwA, c3SwB, err := buildSwitchPair(c3Ports, c3Ports, c1Seeds, c1Seeds, c1VLAN, c1VLAN, nil)
	mustNil(err)
	c3InitSpecA, c3InitSpecB := c3SwA.Spec(), c3SwB.Spec()
	c3InitEntriesA, c3InitEntriesB := c3SwA.Entries(), c3SwB.Entries()

	case3 := ComparisonCase{
		Name:                "vswitch/inconclusive-unknown-status",
		ExpectedDisposition: analysis.Inconclusive,
		ExpectedObservable:  "",
		Execute: func(candidateFirst bool, rng *rand.Rand) (ComparisonResult, error) {
			swA, swB := c3SwA, c3SwB
			if rng != nil {
				var err error
				swA, swB, err = buildSwitchPair(c3Ports, c3Ports, c1Seeds, c1Seeds, c1VLAN, c1VLAN, rng)
				if err != nil {
					return ComparisonResult{}, err
				}
			}
			var cmp vswitch.Comparison
			if candidateFirst {
				cmp = vswitch.Compare(swB, swA, now, "1/1/1", testFrame)
			} else {
				cmp = vswitch.Compare(swA, swB, now, "1/1/1", testFrame)
			}
			return ComparisonResult{
				Disposition: cmp.Disposition,
				Observable:  cmp.Difference.Observable,
				Current:     cmp.Difference.Current,
				Expected:    cmp.Difference.Expected,
			}, nil
		},
		VerifyNonConsuming: func() error {
			if !reflect.DeepEqual(c3SwA.Spec(), c3InitSpecA) {
				return fmt.Errorf("c3SwA spec was mutated")
			}
			if !reflect.DeepEqual(c3SwB.Spec(), c3InitSpecB) {
				return fmt.Errorf("c3SwB spec was mutated")
			}
			if !reflect.DeepEqual(c3SwA.Entries(), c3InitEntriesA) {
				return fmt.Errorf("c3SwA entries were mutated")
			}
			if !reflect.DeepEqual(c3SwB.Entries(), c3InitEntriesB) {
				return fmt.Errorf("c3SwB entries were mutated")
			}
			return nil
		},
	}

	// -------------------------------------------------------------------------
	// Fabric helpers
	// -------------------------------------------------------------------------
	gigabit := gigabitAuto()
	fabScenario := []fabric.Injection{
		{
			At:     now,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame:  testFrame,
		},
	}

	buildFabricPair := func(
		mutateCablesB func([]fabric.Cable) []fabric.Cable,
		rng *rand.Rand,
	) (*fabric.Fabric, *fabric.Fabric, error) {
		makeFab := func(mutateCables func([]fabric.Cable) []fabric.Cable) (*fabric.Fabric, error) {
			baseCables := []fabric.Cable{
				{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Medium: fabric.TwistedPair},
				{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, Medium: fabric.TwistedPair},
			}
			if mutateCables != nil {
				baseCables = mutateCables(baseCables)
			}

			portList := []port.Port{
				{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
				{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			}
			for _, c := range baseCables {
				if c.Fault.Kind == fabric.FaultCut {
					for pi := range portList {
						if (c.A.Node == "sw1" && c.A.Port == portList[pi].Name) ||
							(c.B.Node == "sw1" && c.B.Port == portList[pi].Name) {
							portList[pi].OperStatus = port.Down
						}
					}
				}
			}

			tbl, err := buildPortTable(portList, rng)
			if err != nil {
				return nil, err
			}

			switches := map[string]vswitch.ConstructionSpec{
				"sw1": {
					NodeID: "sw1",
					Config: vswitch.Config{
						Ports:  tbl,
						Bridge: &bridge.Config{},
						Phy: &phy.Config{
							Ethernet: shuffleMap(rng, map[string]phy.Ethernet{
								"1/1/1": gigabit,
								"1/1/2": gigabit,
							}),
						},
					},
					Seeds: shuffleSlice(rng, []bridge.Seed{{MAC: mac2, Port: "1/1/2", Lifetime: bridge.Static}}),
				},
			}

			hosts := map[string]fabric.Host{
				"h1": {Address: mac1, Ethernet: gigabit},
				"h2": {Address: mac2, Ethernet: gigabit},
			}

			cables := shuffleSlice(rng, baseCables)

			spec := fabric.ConstructionSpec{
				Switches: shuffleMap(rng, switches),
				Hosts:    shuffleMap(rng, hosts),
				Cables:   cables,
			}
			return fabric.NewWithSpec(spec)
		}

		fabA, err := makeFab(nil)
		if err != nil {
			return nil, nil, err
		}
		fabB, err := makeFab(mutateCablesB)
		if err != nil {
			return nil, nil, err
		}
		return fabA, fabB, nil
	}

	// -------------------------------------------------------------------------
	// Case 4: fabric/equivalent-complete
	// -------------------------------------------------------------------------
	c4FabA, c4FabB, err := buildFabricPair(nil, nil)
	mustNil(err)
	c4ClockA, c4ClockB := c4FabA.Snapshot().Clock, c4FabB.Snapshot().Clock
	c4ReportA, c4ReportB := c4FabA.Report(), c4FabB.Report()
	c4LinksA, c4LinksB := c4FabA.Links(), c4FabB.Links()
	c4SpecA, c4SpecB := c4FabA.Spec(), c4FabB.Spec()

	case4 := ComparisonCase{
		Name:                "fabric/equivalent-complete",
		ExpectedDisposition: analysis.Equivalent,
		ExpectedObservable:  "",
		Execute: func(candidateFirst bool, rng *rand.Rand) (ComparisonResult, error) {
			fabA, fabB := c4FabA, c4FabB
			if rng != nil {
				var err error
				fabA, fabB, err = buildFabricPair(nil, rng)
				if err != nil {
					return ComparisonResult{}, err
				}
			}
			var cmp fabric.Comparison
			if candidateFirst {
				cmp = fabric.Compare(fabB, fabA, fabScenario, 50)
			} else {
				cmp = fabric.Compare(fabA, fabB, fabScenario, 50)
			}
			if cmp.Err != nil {
				return ComparisonResult{}, cmp.Err
			}
			return ComparisonResult{
				Disposition: cmp.Disposition,
				Observable:  cmp.Difference.Observable,
				Current:     cmp.Difference.Current,
				Expected:    cmp.Difference.Expected,
			}, nil
		},
		VerifyNonConsuming: func() error {
			if !c4FabA.Snapshot().Clock.Equal(c4ClockA) {
				return fmt.Errorf("c4FabA clock advanced from %v to %v", c4ClockA, c4FabA.Snapshot().Clock)
			}
			if !c4FabB.Snapshot().Clock.Equal(c4ClockB) {
				return fmt.Errorf("c4FabB clock advanced from %v to %v", c4ClockB, c4FabB.Snapshot().Clock)
			}
			if !reflect.DeepEqual(c4FabA.Report(), c4ReportA) {
				return fmt.Errorf("c4FabA report mutated")
			}
			if !reflect.DeepEqual(c4FabB.Report(), c4ReportB) {
				return fmt.Errorf("c4FabB report mutated")
			}
			if !reflect.DeepEqual(c4FabA.Links(), c4LinksA) {
				return fmt.Errorf("c4FabA links mutated")
			}
			if !reflect.DeepEqual(c4FabB.Links(), c4LinksB) {
				return fmt.Errorf("c4FabB links mutated")
			}
			if !reflect.DeepEqual(c4FabA.Spec(), c4SpecA) {
				return fmt.Errorf("c4FabA spec mutated")
			}
			if !reflect.DeepEqual(c4FabB.Spec(), c4SpecB) {
				return fmt.Errorf("c4FabB spec mutated")
			}
			return nil
		},
	}

	// -------------------------------------------------------------------------
	// Case 5: fabric/cable-cut-different
	// -------------------------------------------------------------------------
	cutCableMutation := func(cables []fabric.Cable) []fabric.Cable {
		res := make([]fabric.Cable, len(cables))
		copy(res, cables)
		for i := range res {
			if res[i].A.Node == "h2" || res[i].B.Node == "h2" {
				res[i].Fault = fabric.Fault{Kind: fabric.FaultCut}
			}
		}
		return res
	}
	c5FabA, c5FabB, err := buildFabricPair(cutCableMutation, nil)
	mustNil(err)
	c5ClockA, c5ClockB := c5FabA.Snapshot().Clock, c5FabB.Snapshot().Clock
	c5ReportA, c5ReportB := c5FabA.Report(), c5FabB.Report()
	c5LinksA, c5LinksB := c5FabA.Links(), c5FabB.Links()
	c5SpecA, c5SpecB := c5FabA.Spec(), c5FabB.Spec()

	case5 := ComparisonCase{
		Name:                "fabric/cable-cut-different",
		ExpectedDisposition: analysis.Different,
		ExpectedObservable:  "journey terminal",
		Execute: func(candidateFirst bool, rng *rand.Rand) (ComparisonResult, error) {
			fabA, fabB := c5FabA, c5FabB
			if rng != nil {
				var err error
				fabA, fabB, err = buildFabricPair(cutCableMutation, rng)
				if err != nil {
					return ComparisonResult{}, err
				}
			}
			var cmp fabric.Comparison
			if candidateFirst {
				cmp = fabric.Compare(fabB, fabA, fabScenario, 50)
			} else {
				cmp = fabric.Compare(fabA, fabB, fabScenario, 50)
			}
			if cmp.Err != nil {
				return ComparisonResult{}, cmp.Err
			}
			return ComparisonResult{
				Disposition: cmp.Disposition,
				Observable:  cmp.Difference.Observable,
				Current:     cmp.Difference.Current,
				Expected:    cmp.Difference.Expected,
			}, nil
		},
		VerifyNonConsuming: func() error {
			if !c5FabA.Snapshot().Clock.Equal(c5ClockA) {
				return fmt.Errorf("c5FabA clock advanced")
			}
			if !c5FabB.Snapshot().Clock.Equal(c5ClockB) {
				return fmt.Errorf("c5FabB clock advanced")
			}
			if !reflect.DeepEqual(c5FabA.Report(), c5ReportA) {
				return fmt.Errorf("c5FabA report mutated")
			}
			if !reflect.DeepEqual(c5FabB.Report(), c5ReportB) {
				return fmt.Errorf("c5FabB report mutated")
			}
			if !reflect.DeepEqual(c5FabA.Links(), c5LinksA) {
				return fmt.Errorf("c5FabA links mutated")
			}
			if !reflect.DeepEqual(c5FabB.Links(), c5LinksB) {
				return fmt.Errorf("c5FabB links mutated")
			}
			if !reflect.DeepEqual(c5FabA.Spec(), c5SpecA) {
				return fmt.Errorf("c5FabA spec mutated")
			}
			if !reflect.DeepEqual(c5FabB.Spec(), c5SpecB) {
				return fmt.Errorf("c5FabB spec mutated")
			}
			return nil
		},
	}

	// -------------------------------------------------------------------------
	// Case 6: fabric/medium-path-different
	// -------------------------------------------------------------------------
	mediumMutation := func(cables []fabric.Cable) []fabric.Cable {
		res := make([]fabric.Cable, len(cables))
		copy(res, cables)
		for i := range res {
			if res[i].A.Node == "h2" || res[i].B.Node == "h2" {
				res[i].Medium = fabric.SinglemodeFiber
			}
		}
		return res
	}
	c6FabA, c6FabB, err := buildFabricPair(mediumMutation, nil)
	mustNil(err)
	c6ClockA, c6ClockB := c6FabA.Snapshot().Clock, c6FabB.Snapshot().Clock
	c6ReportA, c6ReportB := c6FabA.Report(), c6FabB.Report()
	c6LinksA, c6LinksB := c6FabA.Links(), c6FabB.Links()
	c6SpecA, c6SpecB := c6FabA.Spec(), c6FabB.Spec()

	case6 := ComparisonCase{
		Name:                "fabric/medium-path-different",
		ExpectedDisposition: analysis.Different,
		ExpectedObservable:  "path",
		Execute: func(candidateFirst bool, rng *rand.Rand) (ComparisonResult, error) {
			fabA, fabB := c6FabA, c6FabB
			if rng != nil {
				var err error
				fabA, fabB, err = buildFabricPair(mediumMutation, rng)
				if err != nil {
					return ComparisonResult{}, err
				}
			}
			var cmp fabric.Comparison
			if candidateFirst {
				cmp = fabric.Compare(fabB, fabA, fabScenario, 50)
			} else {
				cmp = fabric.Compare(fabA, fabB, fabScenario, 50)
			}
			if cmp.Err != nil {
				return ComparisonResult{}, cmp.Err
			}
			return ComparisonResult{
				Disposition: cmp.Disposition,
				Observable:  cmp.Difference.Observable,
				Current:     cmp.Difference.Current,
				Expected:    cmp.Difference.Expected,
			}, nil
		},
		VerifyNonConsuming: func() error {
			if !c6FabA.Snapshot().Clock.Equal(c6ClockA) {
				return fmt.Errorf("c6FabA clock advanced")
			}
			if !c6FabB.Snapshot().Clock.Equal(c6ClockB) {
				return fmt.Errorf("c6FabB clock advanced")
			}
			if !reflect.DeepEqual(c6FabA.Report(), c6ReportA) {
				return fmt.Errorf("c6FabA report mutated")
			}
			if !reflect.DeepEqual(c6FabB.Report(), c6ReportB) {
				return fmt.Errorf("c6FabB report mutated")
			}
			if !reflect.DeepEqual(c6FabA.Links(), c6LinksA) {
				return fmt.Errorf("c6FabA links mutated")
			}
			if !reflect.DeepEqual(c6FabB.Links(), c6LinksB) {
				return fmt.Errorf("c6FabB links mutated")
			}
			if !reflect.DeepEqual(c6FabA.Spec(), c6SpecA) {
				return fmt.Errorf("c6FabA spec mutated")
			}
			if !reflect.DeepEqual(c6FabB.Spec(), c6SpecB) {
				return fmt.Errorf("c6FabB spec mutated")
			}
			return nil
		},
	}

	// -------------------------------------------------------------------------
	// Case 7: fabric/inconclusive-budget-exhausted
	// -------------------------------------------------------------------------
	c7FabA, c7FabB, err := buildFabricPair(nil, nil)
	mustNil(err)
	c7ClockA, c7ClockB := c7FabA.Snapshot().Clock, c7FabB.Snapshot().Clock
	c7ReportA, c7ReportB := c7FabA.Report(), c7FabB.Report()
	c7LinksA, c7LinksB := c7FabA.Links(), c7FabB.Links()
	c7SpecA, c7SpecB := c7FabA.Spec(), c7FabB.Spec()

	case7 := ComparisonCase{
		Name:                "fabric/inconclusive-budget-exhausted",
		ExpectedDisposition: analysis.Inconclusive,
		ExpectedObservable:  "",
		Execute: func(candidateFirst bool, rng *rand.Rand) (ComparisonResult, error) {
			fabA, fabB := c7FabA, c7FabB
			if rng != nil {
				var err error
				fabA, fabB, err = buildFabricPair(nil, rng)
				if err != nil {
					return ComparisonResult{}, err
				}
			}
			var cmp fabric.Comparison
			if candidateFirst {
				cmp = fabric.Compare(fabB, fabA, fabScenario, 1)
			} else {
				cmp = fabric.Compare(fabA, fabB, fabScenario, 1)
			}
			if cmp.Err != nil {
				return ComparisonResult{}, cmp.Err
			}
			return ComparisonResult{
				Disposition: cmp.Disposition,
				Observable:  cmp.Difference.Observable,
				Current:     cmp.Difference.Current,
				Expected:    cmp.Difference.Expected,
			}, nil
		},
		VerifyNonConsuming: func() error {
			if !c7FabA.Snapshot().Clock.Equal(c7ClockA) {
				return fmt.Errorf("c7FabA clock advanced")
			}
			if !c7FabB.Snapshot().Clock.Equal(c7ClockB) {
				return fmt.Errorf("c7FabB clock advanced")
			}
			if !reflect.DeepEqual(c7FabA.Report(), c7ReportA) {
				return fmt.Errorf("c7FabA report mutated")
			}
			if !reflect.DeepEqual(c7FabB.Report(), c7ReportB) {
				return fmt.Errorf("c7FabB report mutated")
			}
			if !reflect.DeepEqual(c7FabA.Links(), c7LinksA) {
				return fmt.Errorf("c7FabA links mutated")
			}
			if !reflect.DeepEqual(c7FabB.Links(), c7LinksB) {
				return fmt.Errorf("c7FabB links mutated")
			}
			if !reflect.DeepEqual(c7FabA.Spec(), c7SpecA) {
				return fmt.Errorf("c7FabA spec mutated")
			}
			if !reflect.DeepEqual(c7FabB.Spec(), c7SpecB) {
				return fmt.Errorf("c7FabB spec mutated")
			}
			return nil
		},
	}

	return []ComparisonCase{
		case1,
		case2,
		case3,
		case4,
		case5,
		case6,
		case7,
	}
}
