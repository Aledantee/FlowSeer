package simtest

import (
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// casePlanningLAGMemberFaultKeepsSurvivingFlows returns the case evaluating
// OVS-style bucket persistence: a member fault reassigns only the buckets
// that were assigned to the faulted member, and every other bucket keeps its
// member.
func casePlanningLAGMemberFaultKeepsSurvivingFlows() Case {
	frame := expectedFact("bridge.frame", `src="02:00:00:00:00:52";dst="02:00:00:00:00:d2";ether_type=2048;tags=[];payload_len=5`)
	fdbLearned := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:00:52";present=true;port="in";static=false`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:00:d2";present=true;port="lag1";static=true`)
	selection := expectedFact("lag.selection", `lag="lag1";present=true;mode="BalanceSLB";hash_basis=0;src="02:00:00:00:00:52";vid=0;enabled=["member-b"];member="member-b";selected=true;bucket=253;prior="member-b";cause="kept";rebalance_unmodeled=false;`)
	egressDecision := expectedFact("bridge.egress_decision", `port="lag1";member="member-b";fid=0;eligible=true;reason=""`)

	expectedSteps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="in";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:00:52"},
			[]FactExpectation{fdbLearned}, []FactExpectation{fdbLearned}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:00:d2"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "lag1"},
			[]FactExpectation{frame, selection}, []FactExpectation{egressDecision}),
	}

	return Case{
		ID:            "planning/lag-member-fault-keeps-surviving-flows",
		UseCase:       UseCasePlanning,
		Question:      "When one member of a BalanceSLB bond fails, do buckets already assigned to a surviving member keep that member, or does the whole bond reassign?",
		FalseAnswer:   "All flows remap when one member fails",
		CurrentResult: "Two flows land on distinct members (bucket 28 on member-a, bucket 253 on member-b) before member-a is disabled; the bucket 253 flow's next selection keeps member-b with cause \"kept\", the same bucket it held before the fault",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("default-vlan"),
			trace.RuleID("learn"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("transmit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "0"},
			{Kind: "mac", Key: "02:00:00:00:00:52"},
			{Kind: "mac", Key: "02:00:00:00:00:d2"},
			{Kind: "port", Key: "lag1"},
		},
		ExpectedFacts: []FactExpectation{
			selection,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "member-a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "member-b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			dst1 := netaddr.MAC{0x02, 0, 0, 0, 0, 0xd1}
			dst2 := netaddr.MAC{0x02, 0, 0, 0, 0, 0xd2}

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports:  ports,
					Bridge: &bridge.Config{},
					LAG: &lag.Config{LAGs: map[string]lag.LAG{
						"lag1": {
							Mode:    lag.BalanceSLB,
							Members: map[string]lag.Member{"member-a": {}, "member-b": {}},
						},
					}},
				},
				Seeds: []bridge.Seed{
					{MAC: dst1, Port: "lag1", Lifetime: bridge.Static},
					{MAC: dst2, Port: "lag1", Lifetime: bridge.Static},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			flow1 := ethernet.Frame{Dst: dst1, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 0x51}, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("flow1")}
			flow2 := ethernet.Frame{Dst: dst2, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 0x52}, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("flow2")}

			now := time.Unix(1700000000, 0)
			// Both flows land on distinct members: flow1 takes the front of
			// the enabled list (member-a, bucket 28), and flow2 takes the
			// new front after the rotation (member-b, bucket 253).
			if err := validateReceiveResult(sw.Receive(now, "in", flow1).Result); err != nil {
				return ExecutionResult{}, err
			}
			if err := validateReceiveResult(sw.Receive(now, "in", flow2).Result); err != nil {
				return ExecutionResult{}, err
			}

			// member-a faults. Only the bucket it held (flow1's) needs
			// reassignment; flow2's bucket was never on member-a.
			if _, err := sw.LinkChange(now.Add(time.Second), "member-a", device.Link{State: port.Down, Duplex: device.DuplexFull, PeerRelays: true, SpeedBPS: 1_000_000_000}); err != nil {
				return ExecutionResult{}, err
			}
			if err := validateReceiveResult(sw.Receive(now.Add(2*time.Second), "in", flow1).Result); err != nil {
				return ExecutionResult{}, err
			}

			fwd := sw.Receive(now.Add(2*time.Second), "in", flow2)
			if err := validateReceiveResult(fwd.Result); err != nil {
				return ExecutionResult{}, err
			}

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd.Result,
			}, nil
		},
	}
}

// caseTroubleshootingActiveBackupNoFailback returns the case evaluating that
// active-backup keeps carrying traffic on the member it last chose once a
// higher-named member recovers, rather than returning to it.
func caseTroubleshootingActiveBackupNoFailback() Case {
	frame := expectedFact("bridge.frame", `src="02:00:00:00:00:01";dst="02:00:00:00:00:d1";ether_type=2048;tags=[];payload_len=1`)
	fdbLearned := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:00:01";present=true;port="in";static=false`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:00:d1";present=true;port="lag1";static=true`)
	selection := expectedFact("lag.selection", `lag="lag1";present=true;mode="";hash_basis=0;primary="";enabled=["b","a"];member="b";selected=true;bucket=0;prior="b";cause="last-active";rebalance_unmodeled=false;`)
	egressDecision := expectedFact("bridge.egress_decision", `port="lag1";member="b";fid=0;eligible=true;reason=""`)

	expectedSteps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="in";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:00:01"},
			[]FactExpectation{fdbLearned}, []FactExpectation{fdbLearned}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:00:d1"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "lag1"},
			[]FactExpectation{frame, selection}, []FactExpectation{egressDecision}),
	}

	return Case{
		ID:            "troubleshooting/active-backup-no-failback",
		UseCase:       UseCaseTroubleshooting,
		Question:      "After an active-backup member without a configured Primary fails and its traffic moves to the other member, does traffic return to the first member once it recovers?",
		FalseAnswer:   "Traffic returns to the lowest-named member once it recovers",
		CurrentResult: "With no Primary configured, member \"a\" carries traffic initially, moves to \"b\" when \"a\" goes down, and stays on \"b\" with cause \"last-active\" once \"a\" comes back up",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("default-vlan"),
			trace.RuleID("learn"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("transmit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "0"},
			{Kind: "mac", Key: "02:00:00:00:00:01"},
			{Kind: "mac", Key: "02:00:00:00:00:d1"},
			{Kind: "port", Key: "lag1"},
		},
		ExpectedFacts: []FactExpectation{
			selection,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			dst := netaddr.MAC{0x02, 0, 0, 0, 0, 0xd1}
			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports:  ports,
					Bridge: &bridge.Config{},
					LAG: &lag.Config{LAGs: map[string]lag.LAG{
						"lag1": {
							Mode:    lag.ActiveBackup,
							Members: map[string]lag.Member{"a": {}, "b": {}},
						},
					}},
				},
				Seeds: []bridge.Seed{{MAC: dst, Port: "lag1", Lifetime: bridge.Static}},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			frame := ethernet.Frame{Dst: dst, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 1}, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("x")}

			now := time.Unix(1700000000, 0)
			if err := validateReceiveResult(sw.Receive(now, "in", frame).Result); err != nil {
				return ExecutionResult{}, err
			}

			if _, err := sw.LinkChange(now.Add(time.Second), "a", device.Link{State: port.Down, Duplex: device.DuplexFull, PeerRelays: true, SpeedBPS: 1_000_000_000}); err != nil {
				return ExecutionResult{}, err
			}
			if err := validateReceiveResult(sw.Receive(now.Add(2*time.Second), "in", frame).Result); err != nil {
				return ExecutionResult{}, err
			}

			if _, err := sw.LinkChange(now.Add(3*time.Second), "a", device.Link{State: port.Up, Duplex: device.DuplexFull, PeerRelays: true, SpeedBPS: 1_000_000_000}); err != nil {
				return ExecutionResult{}, err
			}
			fwd := sw.Receive(now.Add(4*time.Second), "in", frame)
			if err := validateReceiveResult(fwd.Result); err != nil {
				return ExecutionResult{}, err
			}

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd.Result,
			}, nil
		},
	}
}

// caseTroubleshootingSSMRejectsUnjoinedSource returns the case evaluating
// that per-source multicast admission rejects a source no port has joined,
// rather than admitting every source to a group any port has joined.
func caseTroubleshootingSSMRejectsUnjoinedSource() Case {
	frame := expectedFact("bridge.frame", `src="00:11:22:33:44:fe";dst="01:00:5e:01:01:01";ether_type=2048;tags=[];payload_len=24`)
	fdbLearned := expectedFact("bridge.fdb_decision", `fid=10;mac="00:11:22:33:44:fe";present=true;port="p9";static=false`)
	groupDestination := expectedFact("bridge.egress_decision", `port="";member="";fid=10;eligible=true;reason="group-destination"`)
	membership := expectedFact("mcast.membership_decision", `fid=10;group="232.1.1.1";source="10.0.0.2";registered=true;decided=true;ports=[]`)
	dropDecision := expectedFact("bridge.egress_decision", `port="";member="";fid=10;eligible=false;reason="unregistered"`)

	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="p9";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "00:11:22:33:44:fe"},
			[]FactExpectation{fdbLearned}, []FactExpectation{fdbLearned}),
		expectedStep("relay", trace.OpLookup, "group-destination", trace.Subject{Kind: "mac", Key: "01:00:5e:01:01:01"},
			[]FactExpectation{frame}, []FactExpectation{groupDestination}),
		expectedStep("relay", trace.OpDrop, trace.RuleID(mcast.ReasonUnregistered), trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{membership}, []FactExpectation{dropDecision}),
	}

	return Case{
		ID:            "troubleshooting/ssm-rejects-unjoined-source",
		UseCase:       UseCaseTroubleshooting,
		Question:      "After a port joins a group with a source-specific (S,G) report, is a frame from a second, unjoined source to that same group admitted or rejected?",
		FalseAnswer:   "The group-only model admits S2",
		CurrentResult: "p1 joins group 232.1.1.1 with an IS_IN report naming source S1; a frame from S2 to the same group resolves zero admitted ports and drops with reason \"unregistered\"",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Dropped,
		ExpectedReason:  mcast.ReasonUnregistered,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("learn"),
			trace.RuleID("group-destination"),
			trace.RuleID(mcast.ReasonUnregistered),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "mac", Key: "00:11:22:33:44:fe"},
			{Kind: "mac", Key: "01:00:5e:01:01:01"},
		},
		ExpectedFacts: []FactExpectation{
			membership,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			pvid := vlan.ID(10)
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "p9", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			group := netip.MustParseAddr("232.1.1.1")
			groupMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x01}
			hostMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
			routerMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0xfe}
			s1 := netip.MustParseAddr("10.0.0.1")
			s2 := netip.MustParseAddr("10.0.0.2")

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports: ports,
					Bridge: &bridge.Config{VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "ten"},
						Switchports: map[string]bridge.Switchport{
							"p1": {PVID: &pvid, Untagged: []vlan.ID{10}},
							"p9": {PVID: &pvid, Untagged: []vlan.ID{10}},
						},
					}},
					Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {}}},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			now := time.Unix(1700000000, 0)

			joinPayload, err := igmp.Encode(igmp.Message{
				Type:    igmp.ReportV3,
				Records: []igmp.GroupRecord{{Type: igmp.ChangeToIncludeMode, Group: group, Sources: []netip.Addr{s1}}},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			joinHdr := ip.Header{Src: netip.MustParseAddr("10.0.0.9"), Dst: group, HopLimit: 1, Protocol: 2, V4: &ip.V4{}}
			joinPacket, err := joinHdr.Encode(joinPayload)
			if err != nil {
				return ExecutionResult{}, err
			}
			joinFrame := ethernet.Frame{Dst: groupMAC, Src: hostMAC, EtherType: ethernet.EtherTypeIPv4, Payload: joinPacket}
			if err := validateReceiveResult(sw.Receive(now, "p1", joinFrame).Result); err != nil {
				return ExecutionResult{}, err
			}

			dataFrame := func(src netip.Addr) (ethernet.Frame, error) {
				hdr := ip.Header{Src: src, Dst: group, HopLimit: 32, Protocol: 17, V4: &ip.V4{}}
				pkt, err := hdr.Encode([]byte("data"))
				if err != nil {
					return ethernet.Frame{}, err
				}
				return ethernet.Frame{Dst: groupMAC, Src: routerMAC, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}, nil
			}

			frameS1, err := dataFrame(s1)
			if err != nil {
				return ExecutionResult{}, err
			}
			if err := validateReceiveResult(sw.Receive(now.Add(time.Second), "p9", frameS1).Result); err != nil {
				return ExecutionResult{}, err
			}

			frameS2, err := dataFrame(s2)
			if err != nil {
				return ExecutionResult{}, err
			}
			fwd := sw.Receive(now.Add(time.Second), "p9", frameS2)
			if err := validateReceiveResult(fwd.Result); err != nil {
				return ExecutionResult{}, err
			}

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd.Result,
			}, nil
		},
	}
}

// caseTroubleshootingLeaveLastMemberQuery returns the case evaluating that a
// non-fast leave with an observed group-specific query stops forwarding
// after the last member query time, rather than after the full membership
// interval.
func caseTroubleshootingLeaveLastMemberQuery() Case {
	frame := expectedFact("bridge.frame", `src="00:11:22:33:44:fe";dst="01:00:5e:06:06:06";ether_type=2048;tags=[];payload_len=24`)
	fdbLearned := expectedFact("bridge.fdb_decision", `fid=10;mac="00:11:22:33:44:fe";present=true;port="p9";static=false`)
	groupDestination := expectedFact("bridge.egress_decision", `port="";member="";fid=10;eligible=true;reason="group-destination"`)
	membership := expectedFact("mcast.membership_decision", `fid=10;group="239.6.6.6";source="10.9.9.9";registered=false;decided=true;ports=["p9"]`)
	dropDecision := expectedFact("bridge.egress_decision", `port="";member="";fid=10;eligible=false;reason="no-egress"`)

	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="p9";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "00:11:22:33:44:fe"},
			[]FactExpectation{fdbLearned}, []FactExpectation{fdbLearned}),
		expectedStep("relay", trace.OpLookup, "group-destination", trace.Subject{Kind: "mac", Key: "01:00:5e:06:06:06"},
			[]FactExpectation{frame}, []FactExpectation{groupDestination}),
		expectedStep("relay", trace.OpDrop, trace.RuleID(bridge.ReasonNoEgress), trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{membership}, []FactExpectation{dropDecision}),
	}

	return Case{
		ID:            "troubleshooting/leave-last-member-query",
		UseCase:       UseCaseTroubleshooting,
		Question:      "After the sole member of a group leaves and the router's group-specific query is observed, does forwarding to that member stop at the last member query time or run for the full membership interval?",
		FalseAnswer:   "Forwarding continues for the full membership interval after a queried leave",
		CurrentResult: "p1 joins group 239.6.6.6, leaves, and an observed group-specific query from router port p9 arrives at the leave time; two last member query intervals later (2s, well short of the 260s membership interval) the group is unregistered and resolves only ingress router port p9; with unregistered flooding disabled, the frame drops with reason \"no-egress\"",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Dropped,
		ExpectedReason:  bridge.ReasonNoEgress,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("learn"),
			trace.RuleID("group-destination"),
			trace.RuleID(bridge.ReasonNoEgress),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "mac", Key: "00:11:22:33:44:fe"},
			{Kind: "mac", Key: "01:00:5e:06:06:06"},
		},
		ExpectedFacts: []FactExpectation{
			membership,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			pvid := vlan.ID(10)
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "p9", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			group := netip.MustParseAddr("239.6.6.6")
			groupMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x06, 0x06, 0x06}
			allRoutersMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x02}
			hostMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
			routerMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0xfe}

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports: ports,
					Bridge: &bridge.Config{VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "ten"},
						Switchports: map[string]bridge.Switchport{
							"p1": {PVID: &pvid, Untagged: []vlan.ID{10}},
							"p9": {PVID: &pvid, Untagged: []vlan.ID{10}},
						},
					}},
					Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {
						LastMemberQueryInterval: time.Second,
						LastMemberQueryCount:    2,
						RouterPorts:             []string{"p9"},
						FloodUnregistered:       new(false),
					}}},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			now := time.Unix(1700000000, 0)
			leaveAt := now.Add(10 * time.Second)

			joinPayload, err := igmp.Encode(igmp.Message{Type: igmp.ReportV2, Group: group})
			if err != nil {
				return ExecutionResult{}, err
			}
			joinHdr := ip.Header{Src: netip.MustParseAddr("10.0.0.1"), Dst: group, HopLimit: 1, Protocol: 2, V4: &ip.V4{}}
			joinPacket, err := joinHdr.Encode(joinPayload)
			if err != nil {
				return ExecutionResult{}, err
			}
			if err := validateReceiveResult(sw.Receive(now, "p1", ethernet.Frame{Dst: groupMAC, Src: hostMAC, EtherType: ethernet.EtherTypeIPv4, Payload: joinPacket}).Result); err != nil {
				return ExecutionResult{}, err
			}

			leavePayload, err := igmp.Encode(igmp.Message{Type: igmp.Leave, Group: group})
			if err != nil {
				return ExecutionResult{}, err
			}
			leaveHdr := ip.Header{Src: netip.MustParseAddr("10.0.0.1"), Dst: netip.MustParseAddr("224.0.0.2"), HopLimit: 1, Protocol: 2, V4: &ip.V4{}}
			leavePacket, err := leaveHdr.Encode(leavePayload)
			if err != nil {
				return ExecutionResult{}, err
			}
			if err := validateReceiveResult(sw.Receive(leaveAt, "p1", ethernet.Frame{Dst: allRoutersMAC, Src: hostMAC, EtherType: ethernet.EtherTypeIPv4, Payload: leavePacket}).Result); err != nil {
				return ExecutionResult{}, err
			}

			queryPayload, err := igmp.Encode(igmp.Message{Type: igmp.Query, Version: igmp.V2, Group: group})
			if err != nil {
				return ExecutionResult{}, err
			}
			queryHdr := ip.Header{Src: netip.MustParseAddr("10.0.0.254"), Dst: group, HopLimit: 1, Protocol: 2, V4: &ip.V4{}}
			queryPacket, err := queryHdr.Encode(queryPayload)
			if err != nil {
				return ExecutionResult{}, err
			}
			if err := validateReceiveResult(sw.Receive(leaveAt, "p9", ethernet.Frame{Dst: groupMAC, Src: routerMAC, EtherType: ethernet.EtherTypeIPv4, Payload: queryPacket}).Result); err != nil {
				return ExecutionResult{}, err
			}

			dataHdr := ip.Header{Src: netip.MustParseAddr("10.9.9.9"), Dst: group, HopLimit: 32, Protocol: 17, V4: &ip.V4{}}
			dataPacket, err := dataHdr.Encode([]byte("data"))
			if err != nil {
				return ExecutionResult{}, err
			}
			dataFrame := ethernet.Frame{Dst: groupMAC, Src: routerMAC, EtherType: ethernet.EtherTypeIPv4, Payload: dataPacket}

			if err := validateReceiveResult(sw.Receive(leaveAt.Add(time.Second), "p9", dataFrame).Result); err != nil {
				return ExecutionResult{}, err
			}
			fwd := sw.Receive(leaveAt.Add(2*time.Second), "p9", dataFrame)
			if err := validateReceiveResult(fwd.Result); err != nil {
				return ExecutionResult{}, err
			}

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd.Result,
			}, nil
		},
	}
}
