package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func TestFabricConstructionSpecAndPropagation(t *testing.T) {
	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	p1, _ := b1.Build()

	vid10 := vlan.ID(10)
	cfg := fabric.Config{
		Start: time.Now(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: p1,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10},
							"1/1/2": {PVID: &vid10},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
				VLAN:    &vid10,
			},
		},
		Cables: []fabric.Cable{
			{
				A:      fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:      fabric.Endpoint{Node: "h1"},
				Medium: fabric.TwistedPair,
			},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	spec := fab.Spec()
	if spec.Switches == nil {
		t.Errorf("expected spec to contain normalized switches")
	}

	// Verify clone isolation
	specCopy := spec.Clone()
	if !spec.Equal(specCopy) {
		t.Errorf("cloned spec should equal original")
	}

	// Inject frame and run
	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}
	_, err = fab.Inject(fabric.Injection{
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("fab.Inject: %v", err)
	}

	fab.Run(10)

	report := fab.Report()
	if len(report) != 1 {
		t.Fatalf("got %d journeys, want 1", len(report))
	}
	journey := report[0]

	// Verify journey entry results are vswitch.ForwardResult envelopes with metadata
	var foundHopResult bool
	for _, entry := range journey.Entries {
		if entry.Kind == fabric.EntryHop && entry.Result != nil {
			foundHopResult = true
			if entry.Result.Metadata.Status() != analysis.Complete {
				t.Errorf("hop forward result status = %v, want Complete", entry.Result.Metadata.Status())
			}
		}
	}
	if !foundHopResult {
		t.Errorf("journey missing EntryHop with non-nil Result: %+v", journey.Entries)
	}
}

func TestFabricConstructionSpecPreservesCompleteSwitchSpecs(t *testing.T) {
	cfg := twoSwitchBaseConfig(t)
	sw1 := cfg.Switches["sw1"]
	sw1.Bridge = &bridge.Config{}
	cfg.Switches["sw1"] = sw1
	spec := constructionSpec(statedPhysical(cfg))
	seed := bridge.Seed{
		MAC:    netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66},
		Port:   "1/1/1",
		Static: true,
	}
	catalog, ref := (analysis.EvidenceCatalog{}).Add(analysis.Evidence{
		Kind:    "snapshot",
		Origin:  "switch sw1",
		Context: "incomplete interface state",
	})
	metadata := analysis.NewMetadata(
		analysis.NodeScope("sw1"),
		[]analysis.Issue{{
			Code:     "test.incomplete-interface-state",
			Status:   analysis.Incomplete,
			Scope:    analysis.PortScope("sw1", "1/1/1"),
			Message:  "interface state is incomplete",
			Evidence: []trace.EvidenceRef{ref},
		}},
		catalog,
		nil,
	)
	sw1Spec := spec.Switches["sw1"]
	sw1Spec.Seeds = []bridge.Seed{seed}
	sw1Spec.Metadata = metadata
	spec.Switches["sw1"] = sw1Spec

	mismatched := spec.Clone()
	mismatchedSpec := mismatched.Switches["sw1"]
	mismatchedSpec.NodeID = "different-node"
	mismatched.Switches["sw1"] = mismatchedSpec
	_, err := fabric.NewWithSpec(mismatched)
	if err == nil {
		t.Fatal("NewWithSpec accepted a switch NodeID that differs from its map key")
	}

	fab, err := fabric.NewWithSpec(spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	roundTrip := fab.Spec()
	gotMetadata := roundTrip.Switches["sw1"].Metadata
	if gotMetadata.Status() != analysis.Incomplete || len(gotMetadata.Issues()) != 1 {
		t.Errorf("round-trip metadata = %+v, want one incomplete issue", gotMetadata)
	}
	if _, ok := gotMetadata.Evidence().Lookup(ref); !ok {
		t.Errorf("round-trip metadata lost evidence %s", ref)
	}
	if got := roundTrip.Switches["sw1"].Seeds; len(got) != 1 || got[0] != seed {
		t.Errorf("round-trip seeds = %+v, want %+v", got, []bridge.Seed{seed})
	}
	rebuilt, err := fabric.NewWithSpec(roundTrip)
	if err != nil {
		t.Fatalf("NewWithSpec from returned spec: %v", err)
	}
	if got := rebuilt.Spec(); !got.Equal(roundTrip) {
		t.Errorf("round-trip spec = %+v, want %+v", got, roundTrip)
	}

	clone := roundTrip.Clone()
	cloneSw1 := clone.Switches["sw1"]
	cloneSw1.Seeds[0].Port = "mutated"
	cloneSw1.Config.MAC[5] ^= 0xff
	clone.Switches["sw1"] = cloneSw1
	if roundTrip.Switches["sw1"].Seeds[0].Port == "mutated" {
		t.Fatal("ConstructionSpec.Clone shares switch seeds")
	}
	if roundTrip.Switches["sw1"].Config.MAC == cloneSw1.Config.MAC {
		t.Fatal("ConstructionSpec.Clone shares switch configuration")
	}

	withoutMetadata := roundTrip.Clone()
	withoutMetadataSpec := withoutMetadata.Switches["sw1"]
	withoutMetadataSpec.Metadata = analysis.Metadata{}
	withoutMetadata.Switches["sw1"] = withoutMetadataSpec
	changes, err := fabric.DiffSpecs(roundTrip, withoutMetadata)
	if err != nil {
		t.Fatalf("DiffSpecs: %v", err)
	}
	foundConstructionInputs := false
	for _, change := range changes {
		if change.Subject == (trace.Subject{Kind: "switch", Key: "sw1"}) && change.Field == "construction_inputs" {
			foundConstructionInputs = true
		}
	}
	if !foundConstructionInputs {
		t.Errorf("DiffSpecs omitted metadata change: %+v", changes)
	}

	derived, err := fabric.Derive(fab, withoutMetadata)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	derivedSpec := derived.Spec().Switches["sw1"]
	if derivedSpec.Metadata.Status() != analysis.Complete {
		t.Errorf("derived metadata status = %v, want Complete", derivedSpec.Metadata.Status())
	}
	if got := derivedSpec.Seeds; len(got) != 1 || got[0] != seed {
		t.Errorf("derived seeds = %+v, want %+v", got, []bridge.Seed{seed})
	}
}

func TestFabricDiffSpecsIgnoresConstructionIssueMessages(t *testing.T) {
	a := constructionSpec(statedPhysical(twoSwitchBaseConfig(t)))
	catalog, firstRef := analysis.EvidenceCatalog{}.Add(analysis.Evidence{Kind: "snapshot", Origin: "first"})
	catalog, secondRef := catalog.Add(analysis.Evidence{Kind: "snapshot", Origin: "second"})
	issue := func(message string, ref trace.EvidenceRef) analysis.Issue {
		return analysis.Issue{
			Code: "test.unobserved", Status: analysis.Incomplete, Scope: analysis.PortScope("sw1", "1/1/1"),
			Message: message, Evidence: []trace.EvidenceRef{ref},
		}
	}

	aSwitch := a.Switches["sw1"]
	aSwitch.Metadata = analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{
		issue("first wording", firstRef),
		issue("second wording", secondRef),
	}, catalog, nil)
	a.Switches["sw1"] = aSwitch
	b := a.Clone()
	bSwitch := b.Switches["sw1"]
	bSwitch.Metadata = analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{
		issue("second wording", firstRef),
		issue("first wording", secondRef),
	}, catalog, nil)
	b.Switches["sw1"] = bSwitch

	if !a.Equal(b) {
		t.Fatal("ConstructionSpec.Equal distinguished human-only issue messages")
	}
	changes, err := fabric.DiffSpecs(a, b)
	if err != nil {
		t.Fatalf("DiffSpecs: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("DiffSpecs reported human-only issue message changes: %+v", changes)
	}
}

func TestFabricConfigConstructorsRejectInactiveFaultParameters(t *testing.T) {
	tests := []struct {
		name  string
		fault fabric.Fault
	}{
		{
			name:  "N on cut fault",
			fault: fabric.Fault{Kind: fabric.FaultCut, N: 2},
		},
		{
			name:  "sequence on every Nth fault",
			fault: fabric.Fault{Kind: fabric.FaultLoseEveryNth, N: 2, Sequence: []uint{1}},
		},
	}
	constructors := []struct {
		name string
		new  func(fabric.Config) error
	}{
		{
			name: "NewConstructionSpec",
			new: func(cfg fabric.Config) error {
				_, err := fabric.NewConstructionSpec(statedPhysical(cfg))
				return err
			},
		},
		{
			name: "New",
			new: func(cfg fabric.Config) error {
				_, err := fabric.New(statedPhysical(cfg))
				return err
			},
		},
	}

	for _, test := range tests {
		for _, constructor := range constructors {
			t.Run(test.name+"/"+constructor.name, func(t *testing.T) {
				cfg := twoSwitchBaseConfig(t)
				cfg.Cables[0].Fault = test.fault

				if err := constructor.new(cfg); err == nil {
					t.Fatalf("%s accepted fault with an inactive discriminated field: %+v", constructor.name, test.fault)
				}
			})
		}
	}
}

func TestFabricDiffSpecsReportsStartChange(t *testing.T) {
	t.Parallel()

	a := constructionSpec(statedPhysical(twoSwitchBaseConfig(t)))
	a.Start = time.Date(2026, 9, 12, 10, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	b := a.Clone()
	b.Start = a.Start.Add(time.Second)

	if a.Equal(b) {
		t.Fatal("construction specifications with different starts compare equal")
	}
	if _, err := a.Normalize(); err != nil {
		t.Fatalf("Normalize(a): %v", err)
	}
	if _, err := b.Normalize(); err != nil {
		t.Fatalf("Normalize(b): %v", err)
	}
	configChanges := fabric.Diff(a.Config(), b.Config())
	if len(configChanges) != 1 || configChanges[0].Field != "start" {
		t.Fatalf("Diff omitted public configuration start change: %+v", configChanges)
	}

	changes, err := fabric.DiffSpecs(a, b)
	if err != nil {
		t.Fatalf("DiffSpecs: %v", err)
	}
	if len(changes) == 0 {
		t.Fatal("DiffSpecs returned no changes for unequal valid specifications")
	}
	if len(changes) != 1 {
		t.Errorf("DiffSpecs duplicated the configuration start change: %+v", changes)
	}

	for _, change := range changes {
		if change.Subject != (trace.Subject{Kind: "fabric"}) || change.Field != "start" {
			continue
		}
		if change.From == nil || change.To == nil {
			t.Fatalf("start change facts = %v -> %v, want two typed facts", change.From, change.To)
		}
		if change.From.TypeID() != "fabric.start" || change.To.TypeID() != "fabric.start" {
			t.Errorf("start fact types = %q -> %q, want fabric.start", change.From.TypeID(), change.To.TypeID())
		}
		if change.From.Canonical() != "2026-09-12T08:00:00Z" || change.To.Canonical() != "2026-09-12T08:00:01Z" {
			t.Errorf("start facts = %q -> %q, want canonical UTC instants", change.From.Canonical(), change.To.Canonical())
		}
		return
	}

	t.Errorf("DiffSpecs omitted start change: %+v", changes)
}

func TestFabricConstructionSpecNormalizesSeedInstantsToUTC(t *testing.T) {
	a := constructionSpec(statedPhysical(twoSwitchBaseConfig(t)))
	switchSpec := a.Switches["sw1"]
	switchSpec.Config.Bridge = &bridge.Config{}
	instant := time.Date(2026, 9, 12, 10, 0, 0, 123, time.FixedZone("UTC+2", 2*60*60))
	switchSpec.Seeds = []bridge.Seed{{
		MAC:       netaddr.MAC{0, 1, 2, 3, 4, 5},
		Port:      "1/1/1",
		Static:    true,
		LearnedAt: instant,
	}}
	a.Switches["sw1"] = switchSpec
	b := a.Clone()
	bSwitch := b.Switches["sw1"]
	bSwitch.Seeds[0].LearnedAt = instant.UTC()
	b.Switches["sw1"] = bSwitch

	normalized, err := a.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got := normalized.Switches["sw1"].Seeds[0].LearnedAt; got.Location() != time.UTC {
		t.Errorf("normalized seed time location = %s, want UTC", got.Location())
	}
	if !a.Equal(b) {
		t.Error("construction specifications with the same seed instant compare unequal")
	}
	changes, err := fabric.DiffSpecs(a, b)
	if err != nil {
		t.Fatalf("DiffSpecs: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("DiffSpecs reported a location-only seed change: %+v", changes)
	}
}

func TestFabricPerHopReadinessMetadataPropagation(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Unknown, OperStatus: port.Unknown})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Down})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	vid10 := vlan.ID(10)
	cfg := fabric.Config{
		Start: time.Now(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10},
							"1/1/2": {PVID: &vid10},
							"1/1/3": {PVID: &vid10},
						},
					},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {
				Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11},
				VLAN:    &vid10,
			},
			"h2": {
				Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x22},
				VLAN:    &vid10,
			},
			"h3": {
				Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x33},
				VLAN:    &vid10,
			},
		},
		Cables: []fabric.Cable{
			{
				A:      fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:      fabric.Endpoint{Node: "h1"},
				Medium: fabric.TwistedPair,
			},
			{
				A:      fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:      fabric.Endpoint{Node: "h2"},
				Medium: fabric.TwistedPair,
			},
			{
				A:      fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
				B:      fabric.Endpoint{Node: "h3"},
				Medium: fabric.TwistedPair,
			},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}

	// 1. Ingress arriving on unknown oper status port 1/1/2
	_, err = fab.Inject(fabric.Injection{
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("Inject sw1 1/1/2: %v", err)
	}

	// 2. Ingress arriving on known down port 1/1/3
	_, err = fab.Inject(fabric.Injection{
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("Inject sw1 1/1/3: %v", err)
	}

	// 3. Ingress arriving on known up port 1/1/1
	_, err = fab.Inject(fabric.Injection{
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("Inject sw1 1/1/1: %v", err)
	}

	fab.Run(20)

	reports := fab.Report()
	if len(reports) != 3 {
		t.Fatalf("got %d journeys, want 3", len(reports))
	}

	findHopResult := func(j fabric.Journey, port string) *vswitch.ForwardResult {
		for _, e := range j.Entries {
			if e.Kind == fabric.EntryHop && e.Port == port && e.Result != nil {
				return e.Result
			}
		}
		return nil
	}

	// Journey 0 (from h2, arriving on 1/1/2 with Unknown oper status):
	// Must propagate Incomplete metadata and must not forward
	resUnknown := findHopResult(reports[0], "1/1/2")
	if resUnknown == nil {
		t.Fatalf("journey 0 missing EntryHop on 1/1/2: %+v", reports[0].Entries)
	}
	if resUnknown.Metadata.Status() != analysis.Incomplete {
		t.Errorf("journey 0 hop result status = %v, want Incomplete", resUnknown.Metadata.Status())
	}
	if issues := resUnknown.Metadata.Issues(); len(issues) == 0 {
		t.Errorf("journey 0 hop result has no issues, want at least one Incomplete issue")
	}

	// Journey 1 (from h3, arriving on 1/1/3 with Down oper status):
	// Definite domain drop with Complete analysis status
	resDown := findHopResult(reports[1], "1/1/3")
	if resDown == nil {
		t.Fatalf("journey 1 missing EntryHop on 1/1/3: %+v", reports[1].Entries)
	}
	if resDown.Outcome != trace.Dropped {
		t.Errorf("journey 1 hop result outcome = %v, want Dropped", resDown.Outcome)
	}
	if resDown.Reason != port.ReasonPortDown {
		t.Errorf("journey 1 hop result reason = %v, want %v", resDown.Reason, port.ReasonPortDown)
	}
	if resDown.Metadata.Status() != analysis.Complete {
		t.Errorf("journey 1 hop result status = %v, want Complete", resDown.Metadata.Status())
	}

	// Journey 2 (from h1, arriving on 1/1/1 with Up oper status):
	// Must remain Complete and unaffected by sibling port uncertainty
	resUp := findHopResult(reports[2], "1/1/1")
	if resUp == nil {
		t.Fatalf("journey 2 missing EntryHop on 1/1/1: %+v", reports[2].Entries)
	}
	if resUp.Metadata.Status() != analysis.Complete {
		t.Errorf("journey 2 hop result status = %v, want Complete", resUp.Metadata.Status())
	}
}
