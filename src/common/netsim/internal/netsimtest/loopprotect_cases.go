package netsimtest

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// RegisterLoopProtectCases populates registry with the case covering loop
// protection breaking a physical loop that spanning tree never sees because
// nothing runs it.
func RegisterLoopProtectCases(registry *Registry) {
	registry.MustRegister(CaseTroubleshootingLoopProtectContainsAccessLoop())
}

var (
	loopProtectCaseStart = time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)
	loopProtectCaseH1    = netaddr.MAC{0x02, 0, 0, 0, 3, 0x01}
	loopProtectCaseH2    = netaddr.MAC{0x02, 0, 0, 0, 3, 0x02}
	loopProtectCaseSrc   = netaddr.MAC{0x02, 0, 0, 0, 3, 0x30}
	loopProtectCaseDst   = netaddr.MAC{0x02, 0, 0, 0, 3, 0x31}
)

// loopProtectCaseGigabit is the physical profile every port in the fixture
// states explicitly, so the journey's readiness turns on the loop-protection
// question under test rather than on unresolved physical facts.
func loopProtectCaseGigabit() phy.Ethernet {
	return phy.Ethernet{
		SupportedSpeedsBPS:       []uint64{1_000_000_000},
		AutoNegotiationSupported: phy.CapabilitySupported,
		Setting:                  &phy.Setting{AutoNegotiation: true},
	}
}

// loopProtectCaseSpec builds the fixture's fabric: two switches joined by two
// cables and no spanning tree on either, so the two cables form a real loop
// unless something else breaks it. sw1's two looped ports run loop
// protection with Block; sw2 is a plain bridge. Each switch also carries one
// host, so a broadcast either host sends has somewhere to be delivered.
func loopProtectCaseSpec() (fabric.ConstructionSpec, error) {
	gigabit := loopProtectCaseGigabit()

	newPorts := func() (port.Table, error) {
		return port.NewBuilder().
			Range("1/1/%d", 1, 3, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
	}

	sw1Ports, err := newPorts()
	if err != nil {
		return fabric.ConstructionSpec{}, err
	}
	sw2Ports, err := newPorts()
	if err != nil {
		return fabric.ConstructionSpec{}, err
	}

	ethFacts := map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": gigabit, "1/1/3": gigabit}

	sw1Cfg := vswitch.Config{
		Ports:  sw1Ports,
		Bridge: &bridge.Config{},
		Phy:    &phy.Config{Ethernet: ethFacts},
		LoopProtect: &loopprotect.Config{
			Interval: 5 * time.Second,
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block},
				"1/1/2": {Action: loopprotect.Block},
			},
		},
	}
	sw2Cfg := vswitch.Config{
		Ports:  sw2Ports,
		Bridge: &bridge.Config{},
		Phy:    &phy.Config{Ethernet: ethFacts},
	}

	return fabric.ConstructionSpec{
		Start: loopProtectCaseStart,
		Switches: map[string]vswitch.ConstructionSpec{
			"sw1": {NodeID: "sw1", Config: sw1Cfg},
			"sw2": {NodeID: "sw2", Config: sw2Cfg},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: loopProtectCaseH1, Ethernet: gigabit},
			"h2": {Address: loopProtectCaseH2, Ethernet: gigabit},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/3"}, Medium: fabric.TwistedPair},
		},
	}, nil
}

// loopProtectCaseIngressBlocked reports whether injecting a frame directly on
// the named port of sw1 is dropped at that port's own ingress with
// bridge.ReasonPortBlocked, mirroring the fabric package's own
// loopProtectBlockedPort check: an ingress-level drop on the injected port
// itself is what proves that port's gate denies it, as distinct from an
// egress-level drop recorded while flooding out of a still-admitted port.
func loopProtectCaseIngressBlocked(fab *fabric.Fabric, portName string) (bool, error) {
	fid, err := fab.Inject(fabric.Injection{
		At:     fab.Snapshot().Clock.Add(time.Millisecond),
		Origin: fabric.Endpoint{Node: "sw1", Port: portName},
		Frame:  ethernet.Frame{Dst: loopProtectCaseDst, Src: loopProtectCaseSrc, EtherType: ethernet.EtherTypeIPv4, Payload: []byte{9, 9, 9}},
	})
	if err != nil {
		return false, err
	}
	fab.Run(50)

	for _, j := range fab.Report() {
		if j.FrameID != fid {
			continue
		}
		for _, e := range j.Entries {
			if e.Kind == fabric.EntryDrop && e.Port == portName && e.Reason == bridge.ReasonPortBlocked {
				return true, nil
			}
		}
	}

	return false, nil
}

var (
	loopProtectCaseFrame = expectedFact("bridge.frame",
		`src="02:00:00:00:03:30";dst="02:00:00:00:03:31";ether_type=2048;tags=[];payload_len=3`)
	loopProtectCaseGate = expectedFact("loopprotect.forwarding_decision",
		`port="1/1/1";vid=0;action="Block";inter_vlan=false;recurrences=0;learns=false;forwards=false`)
)

// CaseTroubleshootingLoopProtectContainsAccessLoop returns the case
// evaluating that loop protection, on its own and with no spanning tree
// running anywhere in the fabric, stops a physical loop between two switches
// from circulating a broadcast forever.
func CaseTroubleshootingLoopProtectContainsAccessLoop() Case {
	steps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{loopProtectCaseFrame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpDrop, "port-blocked", trace.Subject{Kind: "port", Key: "1/1/1"},
			[]FactExpectation{loopProtectCaseGate},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/1";member="";fid=0;eligible=false;reason="port-blocked"`)}),
	}

	return Case{
		ID:      "troubleshooting/loop-protect-contains-access-loop",
		UseCase: UseCaseTroubleshooting,
		Question: "When two switches are joined by two cables and neither runs spanning tree, " +
			"does loop protection on one switch's looped ports stop a broadcast from circulating " +
			"forever?",
		FalseAnswer: "The fabric floods the broadcast forever, since nothing runs to break the " +
			"two-cable loop between the switches",
		CurrentResult: "One probe interval after startup, loop protection's Block action has denied " +
			"both learning and forwarding on exactly one of sw1's two looped ports (1/1/1): the " +
			"other port's returning probe is still recognized there as this switch's own probe, " +
			"but the gated ingress refuses it VLAN classification, so it never reaches detection " +
			"and only one port carries the action. A " +
			"broadcast h1 sends afterward reaches h2 exactly once instead of circulating. The " +
			"decisive trace injects directly on the blocked port: the frame drops Dropped and " +
			"port-blocked at that port's own ingress, with the loop-protection gate fact naming " +
			"action=Block",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Dropped,
		ExpectedReason:   bridge.ReasonPortBlocked,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("default-vlan"),
			trace.RuleID("port-blocked"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "0"},
			{Kind: "port", Key: "1/1/1"},
		},
		ExpectedFacts: []FactExpectation{loopProtectCaseFrame, loopProtectCaseGate},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			spec, err := loopProtectCaseSpec()
			if err != nil {
				return ExecutionResult{}, err
			}

			fab, err := fabric.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}
			// Let one probe interval pass so loop protection sends its
			// probes, sees them return through the other switch, and acts.
			fab.Run(200)

			blocked, err := loopProtectCaseIngressBlocked(fab, "1/1/1")
			if err != nil {
				return ExecutionResult{}, err
			}
			if !blocked {
				return ExecutionResult{}, fmt.Errorf("loopprotect corpus fixture: port 1/1/1 was not blocked after one probe interval")
			}
			clean, err := loopProtectCaseIngressBlocked(fab, "1/1/2")
			if err != nil {
				return ExecutionResult{}, err
			}
			if clean {
				return ExecutionResult{}, fmt.Errorf("loopprotect corpus fixture: port 1/1/2 was also blocked, want exactly one of the two looped ports acted on")
			}

			broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
			bfid, err := fab.Inject(fabric.Injection{
				At:     fab.Snapshot().Clock.Add(time.Second),
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  ethernet.Frame{Dst: broadcast, Src: loopProtectCaseH1, EtherType: ethernet.EtherTypeIPv4, Payload: []byte{1, 2, 3}},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(200)

			deliveries := 0
			for _, j := range fab.Report() {
				if j.FrameID != bfid {
					continue
				}
				for _, d := range j.Deliveries {
					if d.Host == "h2" {
						deliveries++
					}
				}
			}
			if deliveries != 1 {
				return ExecutionResult{}, fmt.Errorf("loopprotect corpus fixture: h2 received %d copies of the broadcast, want exactly 1", deliveries)
			}

			// The decisive journey: a frame injected directly on the blocked
			// port, dropped at that port's own ingress.
			fid, err := fab.Inject(fabric.Injection{
				At:     fab.Snapshot().Clock.Add(time.Millisecond),
				Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				Frame:  ethernet.Frame{Dst: loopProtectCaseDst, Src: loopProtectCaseSrc, EtherType: ethernet.EtherTypeIPv4, Payload: []byte{9, 9, 9}},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(50)

			journey := fab.Report()[fid-1]
			fabricMetadata := fab.Metadata()

			return ExecutionResult{
				Outcome:        journeyHopOutcome(journey),
				Reason:         journeyHopReason(journey),
				Steps:          journeySteps(journey),
				Metadata:       journey.Metadata,
				Switch:         fab.Switch("sw1"),
				Journey:        &journey,
				FabricMetadata: &fabricMetadata,
			}, nil
		},
	}
}
