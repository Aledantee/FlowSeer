package netmodel_test

import (
	"reflect"
	"slices"
	"testing"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
)

func TestForwardCarriesRelevantLoadConflictWithEvidence(t *testing.T) {
	sw, result := loadSwitchWithPortConflict(t)
	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Dst: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
	}
	if err := sw.Learn([]bridge.Seed{{MAC: frame.Dst, Port: "1/1/3", Static: true}}); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	forwarded := sw.Peek(trustTestTime, "1/1/1", frame)
	if forwarded.Metadata.Status() != analysis.Unstable {
		t.Fatalf("forward status = %s, want %s; issues: %+v", forwarded.Metadata.Status(), analysis.Unstable, forwarded.Metadata.Issues())
	}
	assertIssueEvidencePresent(t, forwarded.Metadata, netmodel.IssueConflictSTPPort)

	if forwarded.Metadata.Scope().Compare(result.Metadata.Scope()) != 0 {
		t.Errorf("forward scope = %s, want load scope %s", forwarded.Metadata.Scope(), result.Metadata.Scope())
	}
}

func TestLoadFabricJourneyPreservesConstructionTrust(t *testing.T) {
	_, loaded := loadSwitchWithPortConflict(t)
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	fab, err := fabric.NewWithSpec(fabric.ConstructionSpec{
		Start: trustTestTime,
		Switches: map[string]vswitch.ConstructionSpec{
			"sw1": loaded.Spec,
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}},
		},
	})
	if err != nil {
		t.Fatalf("fabric.NewWithSpec: %v", err)
	}
	frameID, err := fab.Inject(fabric.Injection{
		At:     trustTestTime,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Src: macH1,
			Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(100)

	var hop *vswitch.ForwardResult
	for _, journey := range fab.Report() {
		if journey.FrameID != frameID {
			continue
		}
		for _, entry := range journey.Entries {
			if entry.Kind == fabric.EntryHop && entry.Device == "sw1" {
				hop = entry.Result
				break
			}
		}
	}
	if hop == nil {
		t.Fatal("Load -> Fabric -> Journey produced no sw1 hop result")
	}
	if hop.Metadata.Status() != analysis.Unstable {
		t.Fatalf("journey hop status = %s, want Unstable; issues: %+v", hop.Metadata.Status(), hop.Metadata.Issues())
	}
	assertIssueEvidencePresent(t, hop.Metadata, netmodel.IssueConflictSTPPort)
	if got := fab.Spec().Switches["sw1"]; got.NodeID != "sw1" || got.Metadata.Status() != loaded.Spec.Metadata.Status() {
		t.Errorf("fabric construction spec lost load identity/trust: %+v", got)
	}
}

func TestForwardExcludesUnrelatedLoadConflict(t *testing.T) {
	sw, _ := loadSwitchWithPortConflict(t)
	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Dst: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
	}
	if err := sw.Learn([]bridge.Seed{{MAC: frame.Dst, Port: "1/1/2", Static: true}}); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	forwarded := sw.Peek(trustTestTime, "1/1/1", frame)
	if forwarded.Metadata.Status() != analysis.Complete {
		t.Fatalf("forward status = %s, want %s; issues: %+v", forwarded.Metadata.Status(), analysis.Complete, forwarded.Metadata.Issues())
	}
	if slices.ContainsFunc(forwarded.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == netmodel.IssueConflictSTPPort
	}) {
		t.Errorf("unrelated conflict reached forwarding metadata: %+v", forwarded.Metadata.Issues())
	}
	for _, entry := range forwarded.Metadata.Evidence().Entries() {
		if entry.Evidence.Kind == netmodel.EvidenceKindConflict {
			t.Errorf("unrelated conflict evidence reached forwarding metadata: %+v", entry)
		}
	}
}

func TestForwardIncludesNodeWideLoadIssue(t *testing.T) {
	portA := "1/1/1"
	portB := "1/1/2"
	vid := uint32(10)
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	static := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	mac := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x03}
	input := loadInput{
		ifaces: []*interfacev1.Interface{plainPhysicalInterface(portA), plainPhysicalInterface(portB)},
		fdb: []*switchingv1.FdbEntry{
			switchingv1.FdbEntry_builder{VlanId: &vid, InterfaceName: &portA, Mac: addrv1.Eui48Address_builder{Octets: mac}.Build(), Kind: &static, Status: &active}.Build(),
			switchingv1.FdbEntry_builder{VlanId: &vid, InterfaceName: &portB, Mac: addrv1.Eui48Address_builder{Octets: mac}.Build(), Kind: &static, Status: &active}.Build(),
		},
	}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "node-conflict"})
	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	forwarded := sw.Peek(trustTestTime, portA, ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	})
	if forwarded.Metadata.Status() != analysis.Unstable {
		t.Fatalf("forward status = %s, want %s; issues: %+v", forwarded.Metadata.Status(), analysis.Unstable, forwarded.Metadata.Issues())
	}
	assertIssueEvidencePresent(t, forwarded.Metadata, netmodel.IssueConflictFDB)
}

func TestConstructionSpecMetadataIsCloneIsolatedAndDeterministic(t *testing.T) {
	sw, loaded := loadSwitchWithPortConflict(t)
	want := sw.Spec()
	if !want.Equal(loaded.Spec) {
		t.Fatalf("switch spec differs from load spec:\n got: %+v\nwant: %+v", want, loaded.Spec)
	}

	loaded.Spec.NodeID = "changed-input"
	loaded.Spec.Metadata = analysis.Metadata{}
	loaded.Spec.Config.Bridge.AgingTime++
	if got := sw.Spec(); !got.Equal(want) {
		t.Errorf("mutating input spec changed retained spec:\n got: %+v\nwant: %+v", got, want)
	}

	returned := sw.Spec()
	returned.NodeID = "changed-output"
	returned.Metadata = analysis.Metadata{}
	returned.Config.Bridge.AgingTime++
	if got := sw.Spec(); !got.Equal(want) {
		t.Errorf("mutating returned spec changed retained spec:\n got: %+v\nwant: %+v", got, want)
	}

	different := want.Clone()
	different.NodeID = "different"
	if want.Equal(different) {
		t.Error("construction specs with different node identities compare equal")
	}
	different = want.Clone()
	different.Metadata = analysis.Metadata{}
	if want.Equal(different) {
		t.Error("construction specs with different metadata compare equal")
	}

	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Dst: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
	}
	if err := sw.Learn([]bridge.Seed{{MAC: frame.Dst, Port: "1/1/3", Static: true}}); err != nil {
		t.Fatalf("Learn: %v", err)
	}
	first := sw.Peek(trustTestTime, "1/1/1", frame).Metadata
	for range 10 {
		next := sw.Peek(trustTestTime, "1/1/1", frame).Metadata
		if first.Scope().Compare(next.Scope()) != 0 || first.Status() != next.Status() ||
			!reflect.DeepEqual(first.Issues(), next.Issues()) ||
			!slices.Equal(first.Evidence().Entries(), next.Evidence().Entries()) ||
			!reflect.DeepEqual(first.Assumptions(), next.Assumptions()) {
			t.Fatalf("forward metadata changed across repeated evaluation:\nfirst: %+v\n next: %+v", first, next)
		}
	}
}

func loadSwitchWithPortConflict(t *testing.T) (*vswitch.Switch, netmodel.Result) {
	t.Helper()
	conflictPort := "1/1/3"
	input := loadInput{
		ifaces: []*interfacev1.Interface{
			plainPhysicalInterface("1/1/1"),
			plainPhysicalInterface("1/1/2"),
			plainPhysicalInterface("1/1/3"),
		},
		bridgeState: validBridgeState(),
		stpPorts: []*stpv1.PortState{
			stpv1.PortState_builder{InterfaceName: &conflictPort, Priority: ptr(uint32(64)), AdminPathCost: ptr(uint32(10))}.Build(),
			stpv1.PortState_builder{InterfaceName: &conflictPort, Priority: ptr(uint32(128)), AdminPathCost: ptr(uint32(20))}.Build(),
		},
	}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "port-conflict"})
	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	return sw, loaded
}

func assertIssueEvidencePresent(t *testing.T, metadata analysis.Metadata, code analysis.IssueCode) {
	t.Helper()
	issues := metadata.Issues()
	index := slices.IndexFunc(issues, func(issue analysis.Issue) bool { return issue.Code == code })
	if index < 0 {
		t.Fatalf("issues = %+v, want %s", issues, code)
	}
	if len(issues[index].Evidence) == 0 {
		t.Fatalf("issue %s has no evidence references", code)
	}
	for _, ref := range issues[index].Evidence {
		if _, ok := metadata.Evidence().Lookup(ref); !ok {
			t.Errorf("issue %s evidence %s is absent from catalog", code, ref)
		}
	}
	if len(metadata.Assumptions()) == 0 {
		t.Error("forward metadata lost load-time assumptions")
	}
	for _, assumption := range metadata.Assumptions() {
		for _, ref := range assumption.Evidence {
			if _, ok := metadata.Evidence().Lookup(ref); !ok {
				t.Errorf("assumption evidence %s is absent from catalog", ref)
			}
		}
	}
	if metadata.Scope().Compare(analysis.NodeScope("sw1")) != 0 {
		t.Errorf("metadata scope = %s, want %s", metadata.Scope(), analysis.NodeScope("sw1"))
	}
}
