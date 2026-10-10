package simtest

import (
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func mstModelInput(complete bool) netmodel.Input {
	id := stpv1.BridgeId_builder{
		Priority: new(uint32(32768)),
		Address:  addrv1.Eui48Address_builder{Octets: []byte{2, 0, 0, 0, 0, 1}}.Build(),
	}.Build()
	identity := stpv1.MstConfigId_builder{
		Name: new("region"), RevisionLevel: new(uint32(0)),
		Digest: []byte{0x93, 0x57, 0xeb, 0xb7, 0xa8, 0xd7, 0x4d, 0xd5, 0xfe, 0xf4, 0xf2, 0xba, 0xb5, 0x05, 0x31, 0xaa},
	}.Build()
	if !complete {
		identity.ClearDigest()
	}
	in := netmodel.Input{
		Want: []trace.Layer{stp.LayerName},
		BridgeState: stpv1.BridgeState_builder{
			NetworkInstance: new(netmodel.DefaultNetworkInstance),
			ProtocolVersion: stpv1.ProtocolVersion_PROTOCOL_VERSION_MSTP.Enum(),
			BridgeId:        id, MstConfigId: identity, MaxHops: new(uint32(20)),
			BridgeHelloTime: durationpb.New(2 * time.Second), BridgeMaxAge: durationpb.New(20 * time.Second),
			BridgeForwardDelay: durationpb.New(15 * time.Second), TxHoldCount: new(uint32(6)),
		}.Build(),
		STPPorts: []*stpv1.PortState{stpv1.PortState_builder{
			InterfaceName: new("p1"), Priority: new(uint32(64)), AdminPathCost: new(uint32(55)),
			PointToPoint: stpv1.PointToPointMode_POINT_TO_POINT_MODE_AUTO.Enum(),
		}.Build()},
	}
	for _, name := range []string{"p1", "down"} {
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		if name == "down" {
			oper = interfacev1.OperStatus_OPER_STATUS_DOWN
		}
		in.Interfaces = append(in.Interfaces, interfacev1.Interface_builder{
			Name: new(name), Mtu: new(uint32(0)), AdminStatus: interfacev1.AdminStatus_ADMIN_STATUS_UP.Enum(),
			OperStatus: oper.Enum(), Physical: interfacev1.PhysicalInterface_builder{}.Build(),
		}.Build())
	}
	for _, mstid := range []uint32{1, 2} {
		in.MSTInstances = append(in.MSTInstances, stpv1.MstInstance_builder{
			MstId: new(mstid), NetworkInstance: new(netmodel.DefaultNetworkInstance), BridgeId: id,
		}.Build())
		in.MSTVLANMaps = append(in.MSTVLANMaps, stpv1.MstVlanMap_builder{
			MstId: new(mstid), NetworkInstance: new(netmodel.DefaultNetworkInstance), VlanIds: []uint32{mstid * 10},
		}.Build())
		in.MSTPorts = append(in.MSTPorts, stpv1.MstPort_builder{
			MstId: new(mstid), InterfaceName: new("p1"), Priority: new(uint32(16)),
			AdminPathCost: new(uint32(7)), PathCost: new(uint32(99)),
		}.Build())
	}
	return in
}

func mstModelMetadata(complete bool) MetadataExpectation {
	node := analysis.NodeScope("mst-model")
	protocol := analysis.ProtocolScope("mst-model", "stp", "0")
	mstField := func(path ...string) analysis.Scope {
		return analysis.FieldScope(protocol, append([]string{"mst"}, path...)...)
	}
	var catalog analysis.EvidenceCatalog
	catalog, aging := catalog.Add(analysis.Evidence{
		Kind: netmodel.EvidenceKindDefault, Origin: "mst-model", Context: "default aging_time=300s",
	})
	expected := MetadataExpectation{
		Status: analysis.Complete, Scope: node,
		Assumptions: []AssumptionExpectation{{
			Scope: node, Statement: "default value applied for aging_time: 300s", Evidence: []trace.EvidenceRef{aging},
		}},
	}
	if !complete {
		expected.Status = analysis.Incomplete
		for _, row := range []struct {
			scope  analysis.Scope
			code   analysis.IssueCode
			detail string
		}{
			{mstField("mst_config_id", "digest"), netmodel.IssueIncompleteMSTRegion, "skipped mst_region: region digest is unreported"},
			{mstField("instances", "1"), netmodel.IssueIncompleteMSTRegion, "skipped mst_instance: MST region is unavailable"},
			{mstField("instances", "2"), netmodel.IssueIncompleteMSTRegion, "skipped mst_instance: MST region is unavailable"},
			{mstField("vlan_maps", "1"), netmodel.IssueIncompleteMSTRegion, "skipped mst_vlan_map: MST region is unavailable"},
			{mstField("vlan_maps", "2"), netmodel.IssueIncompleteMSTRegion, "skipped mst_vlan_map: MST region is unavailable"},
			{mstField("instances", "1", "ports", "p1"), netmodel.IssueIncompleteMSTRegion, "port p1 skipped mst_port: MST region is unavailable"},
			{mstField("instances", "2", "ports", "p1"), netmodel.IssueIncompleteMSTRegion, "port p1 skipped mst_port: MST region is unavailable"},
			{protocol, netmodel.IssueIncompleteMSTRegion, "skipped stp_bridge: MST region is unavailable"},
			{analysis.FieldScope(protocol, "ports", "p1"), netmodel.IssueIncompleteMSTRegion, "port p1 skipped stp_port: MST region is unavailable"},
		} {
			var ref trace.EvidenceRef
			catalog, ref = catalog.Add(analysis.Evidence{Kind: netmodel.EvidenceKindSkipped, Origin: "mst-model", Context: row.detail})
			expected.Issues = append(expected.Issues, IssueExpectation{
				Code: row.code, Status: analysis.Incomplete, Scope: row.scope, Evidence: []trace.EvidenceRef{ref},
			})
		}
	}
	expected.Evidence = catalog.Entries()
	return expected.Canonical()
}

func caseShadowingMSTModel(complete bool) Case {
	id := "topology-shadowing/mstp-model-incomplete-region"
	if complete {
		id = "topology-shadowing/mstp-model-complete-region"
	}
	metadata := mstModelMetadata(complete)
	forwardMetadata := mstModelMetadata(true)
	fact := NewFactExpectation(port.ForwardingFact("down", port.Port{
		Name: "down", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down,
	}, false, port.ReasonPortDown))
	return Case{
		ID: id, UseCase: UseCaseTopologyShadowing,
		Question:         "Does MSTP intake preserve a complete region and report an incomplete region with scoped issues?",
		FalseAnswer:      "Executing an incomplete region or rejecting a complete MSTP model",
		CurrentResult:    "Complete regions execute MSTI settings. A missing digest omits STP with evidenced dependent issues.",
		ExpectedMetadata: &metadata, ExpectedModelMetadata: &metadata, ExpectedForwardMetadata: &forwardMetadata,
		ExpectedOutcome: trace.Dropped, ExpectedReason: port.ReasonPortDown,
		ExpectedRules:    []trace.RuleID{"ingress-port-down"},
		ExpectedSubjects: []trace.Subject{{Kind: "port", Key: "down"}},
		ExpectedFacts:    []FactExpectation{fact},
		ExpectedSteps:    []StepExpectation{expectedStep("relay", trace.OpDrop, "ingress-port-down", trace.Subject{Kind: "port", Key: "down"}, nil, []FactExpectation{fact})},
		Execute: func() (ExecutionResult, error) {
			now := time.Unix(1700000000, 0)
			result, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "mst-model"}, mstModelInput(complete))
			if err != nil {
				return ExecutionResult{}, err
			}
			want := []trace.Layer{"relay"}
			if complete {
				want = []trace.Layer{"relay", "stp"}
			}
			if !slices.Equal(result.Report.Capabilities, want) {
				return ExecutionResult{}, errs.Msgf("capabilities = %v, want %v", result.Report.Capabilities, want)
			}
			sw, err := vswitch.NewWithSpec(result.Spec)
			if err != nil {
				return ExecutionResult{}, err
			}
			if complete {
				for _, vid := range []uint16{10, 20} {
					info := sw.TreeRoles()[vlan.ID(vid)]["p1"]
					if info.Tree != (stp.TreeRef{Kind: stp.TreeMSTI, ID: vid / 10}) || info.Priority != 16 || info.PathCost != 7 {
						return ExecutionResult{}, errs.Msgf("VLAN %d state = %+v, want reported MSTI priority 16 cost 7", vid, info)
					}
				}
			} else if sw.Config().STP != nil {
				return ExecutionResult{}, errs.Msg("incomplete region retained STP")
			}
			fwd := sw.Receive(now, "down", ethernet.Frame{Src: netaddr.MAC{2, 0, 0, 0, 0, 2}, Dst: netaddr.MAC{2, 0, 0, 0, 0, 3}})
			if err := validateReceiveResult(fwd.Result); err != nil {
				return ExecutionResult{}, err
			}
			return ExecutionResult{
				Outcome: fwd.Outcome, Reason: fwd.Reason, Steps: fwd.Steps,
				Metadata: result.Metadata, ModelResult: &result, Switch: sw, Forward: &fwd.Result,
			}, nil
		},
	}
}
