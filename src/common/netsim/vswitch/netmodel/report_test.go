package netmodel_test

import (
	"slices"
	"testing"
	"time"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func makeTestInterface(name string, operUp bool) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP

	var oper interfacev1.OperStatus
	if operUp {
		oper = interfacev1.OperStatus_OPER_STATUS_UP
	} else {
		oper = interfacev1.OperStatus_OPER_STATUS_DOWN
	}

	frameAdmAll := switchingv1.FrameAdmission_FRAME_ADMISSION_ALL
	ingressFiltFalse := false
	vid10 := uint32(10)

	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				TaggedVlanIds:    []uint32{vid10},
				FrameAdmission:   &frameAdmAll,
				IngressFiltering: &ingressFiltFalse,
			}.Build(),
		}.Build(),
	}.Build()
}

func TestLoad_CompleteModel(t *testing.T) {
	p1 := makeTestInterface("1/1/1", true)
	p2 := makeTestInterface("1/1/2", true)
	ifaces := []*interfacev1.Interface{p1, p2}

	vid10 := uint32(10)
	vlanName := "vlan10"
	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{
			Id:   &vid10,
			Name: &vlanName,
		}.Build(),
	}

	macBytes := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	fdbStatic := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	fdbActive := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	p1Name := "1/1/1"
	fdb := []*switchingv1.FdbEntry{
		switchingv1.FdbEntry_builder{
			VlanId:        &vid10,
			InterfaceName: &p1Name,
			Mac:           addrv1.Eui48Address_builder{Octets: macBytes}.Build(),
			Kind:          &fdbStatic,
			Status:        &fdbActive,
		}.Build(),
	}

	src := netmodel.SourceContext{
		DeviceID: "sw1",
		Origin:   "device-snapshot",
		Context:  "test-complete",
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	res, err := netmodel.Load(now, src, ifaces, vlans, fdb, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load complete model failed: %v", err)
	}

	// Complete model must have Complete readiness and no issues
	if got := res.Readiness(); got != analysis.Complete {
		t.Errorf("res.Readiness() = %v, want Complete", got)
	}
	if issues := res.Metadata.Issues(); len(issues) != 0 {
		t.Errorf("res.Metadata.Issues() = %+v, want empty", issues)
	}

	// Must be directly constructible with vswitch.NewWithSpec
	sw, err := vswitch.NewWithSpec(res.Spec)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec failed: %v", err)
	}

	// Static seed retention
	if len(res.Spec.Seeds) != 1 {
		t.Fatalf("len(res.Spec.Seeds) = %d, want 1", len(res.Spec.Seeds))
	}
	seed := res.Spec.Seeds[0]
	if !seed.Static || seed.Port != "1/1/1" || seed.FID != 10 {
		t.Errorf("retained seed = %+v, want static seed on 1/1/1 fid 10", seed)
	}

	// Switch built from spec retains seeds
	swSpec := sw.Spec()
	if len(swSpec.Seeds) != 1 || !swSpec.Seeds[0].Static {
		t.Errorf("sw.Spec().Seeds = %+v, want 1 static seed", swSpec.Seeds)
	}

	// Forwarding succeeds on both up ports
	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		Dst: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
	}
	fwdRes := sw.Forward(now, "1/1/2", frame)
	if fwdRes.Metadata.Status() != analysis.Complete {
		t.Errorf("Forward status = %v, want Complete", fwdRes.Metadata.Status())
	}
}

func TestLoad_PartialModel_MissingOperStatus(t *testing.T) {
	// 1/1/1 has no oper status (missing)
	name1 := "1/1/1"
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	p1 := interfacev1.Interface_builder{
		Name:        &name1,
		AdminStatus: &adminUp,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()

	// 1/1/2 has explicit oper down
	p2 := makeTestInterface("1/1/2", false)

	// 1/1/3 is fully up
	p3 := makeTestInterface("1/1/3", true)

	src := netmodel.SourceContext{
		DeviceID: "sw1",
		Origin:   "device-snapshot",
		Context:  "test-partial",
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	res, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p2, p3}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load partial model failed: %v", err)
	}

	// Overall readiness must be Incomplete
	if got := res.Readiness(); got != analysis.Incomplete {
		t.Errorf("res.Readiness() = %v, want Incomplete", got)
	}

	// Port-scoped issue on 1/1/1
	portScope1 := analysis.PortScope("sw1", "1/1/1")
	issues1 := res.Metadata.IssuesFor(portScope1)
	if len(issues1) == 0 {
		t.Fatalf("expected issue for port 1/1/1, got 0")
	}
	if issues1[0].Status != analysis.Incomplete {
		t.Errorf("issue status = %v, want Incomplete", issues1[0].Status)
	}
	if len(issues1[0].Evidence) == 0 {
		t.Errorf("issue missing evidence reference")
	}

	// Port 1/1/2 (explicit down) must NOT produce an incomplete issue
	portScope2 := analysis.PortScope("sw1", "1/1/2")
	if len(res.Metadata.IssuesFor(portScope2)) != 0 {
		t.Errorf("port 1/1/2 (explicit down) has unexpected issues: %+v", res.Metadata.IssuesFor(portScope2))
	}

	// Construct switch directly from partial spec
	sw, err := vswitch.NewWithSpec(res.Spec)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec from partial spec failed: %v", err)
	}

	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x01, 0x02, 0x03, 0x04, 0x05},
		Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}

	// 1/1/1 (unknown oper status) never forwards and returns Incomplete forwarding result
	fwd1 := sw.Forward(now, "1/1/1", frame)
	if fwd1.Outcome == trace.Forwarded || fwd1.Outcome == trace.Flooded {
		t.Errorf("unknown port 1/1/1 forwarded frame, outcome = %v", fwd1.Outcome)
	}
	if fwd1.Metadata.Status() != analysis.Incomplete {
		t.Errorf("unknown port 1/1/1 Forward status = %v, want Incomplete", fwd1.Metadata.Status())
	}

	// 1/1/2 (explicit down) returns definite port-down drop with Complete status
	fwd2 := sw.Forward(now, "1/1/2", frame)
	if fwd2.Outcome != trace.Dropped || fwd2.Reason != port.ReasonPortDown {
		t.Errorf("down port 1/1/2 outcome=%v reason=%v, want Dropped port-down", fwd2.Outcome, fwd2.Reason)
	}
	if fwd2.Metadata.Status() != analysis.Complete {
		t.Errorf("down port 1/1/2 Forward status = %v, want Complete", fwd2.Metadata.Status())
	}

	// 1/1/3 (known up) forwards with Complete status; sibling uncertainty does NOT taint it
	fwd3 := sw.Forward(now, "1/1/3", frame)
	if fwd3.Metadata.Status() != analysis.Complete {
		t.Errorf("known up port 1/1/3 Forward status = %v, want Complete", fwd3.Metadata.Status())
	}
}

func TestLoad_PartialModel_SkipsAndDefaults(t *testing.T) {
	p1 := makeTestInterface("1/1/1", true)
	p2 := makeTestInterface("1/1/2", true)

	src := netmodel.SourceContext{
		DeviceID: "sw1",
		Origin:   "test-source",
		Context:  "skips-defaults",
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	// Explicitly request only relay layer, skipping switchport/vlan facets
	res, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p2}, nil, nil, nil, nil, nil, nil, nil, nil, nil, []port.Layer{port.LayerRelay})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Report must contain skipped facets with exact scope, reason, and evidence
	if len(res.Report.Skipped) == 0 {
		t.Fatalf("expected skipped entries, got 0")
	}
	for _, s := range res.Report.Skipped {
		if s.Scope.Kind() == analysis.ScopeWhole && s.Port == "" {
			t.Errorf("skipped entry %+v missing specific scope or port", s)
		}
		if len(s.Evidence) == 0 {
			t.Errorf("skipped entry %+v missing evidence", s)
		}
	}

	// Report must contain defaults with exact scope and evidence
	if len(res.Report.Defaults) == 0 {
		t.Fatalf("expected defaults, got 0")
	}
	for _, d := range res.Report.Defaults {
		if len(d.Evidence) == 0 {
			t.Errorf("default entry %+v missing evidence", d)
		}
	}

	// Metadata must contain explicit assumptions for defaults
	assumptions := res.Metadata.Assumptions()
	if len(assumptions) == 0 {
		t.Fatalf("expected metadata assumptions, got 0")
	}
	for _, a := range assumptions {
		if len(a.Evidence) == 0 {
			t.Errorf("assumption %+v missing evidence", a)
		}
	}

	// Spec is still constructible
	_, err = vswitch.NewWithSpec(res.Spec)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec failed: %v", err)
	}
}

func TestLoad_ConflictingRows(t *testing.T) {
	p1 := makeTestInterface("1/1/1", true)
	p2 := makeTestInterface("1/1/2", true)

	vid10 := uint32(10)
	vlanName := "vlan10"
	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: &vid10, Name: &vlanName}.Build(),
	}

	// Two conflicting FDB entries for the same MAC and VLAN on different ports
	macBytes := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	fdbStatic := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	fdbActive := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	p1Name := "1/1/1"
	p2Name := "1/1/2"

	fdb1 := switchingv1.FdbEntry_builder{
		VlanId:        &vid10,
		InterfaceName: &p1Name,
		Mac:           addrv1.Eui48Address_builder{Octets: macBytes}.Build(),
		Kind:          &fdbStatic,
		Status:        &fdbActive,
	}.Build()

	fdb2 := switchingv1.FdbEntry_builder{
		VlanId:        &vid10,
		InterfaceName: &p2Name,
		Mac:           addrv1.Eui48Address_builder{Octets: macBytes}.Build(),
		Kind:          &fdbStatic,
		Status:        &fdbActive,
	}.Build()

	// Conflicting STP port state rows (duplicate port 1/1/1)
	ps1 := stpv1.PortState_builder{
		InterfaceName: &p1Name,
	}.Build()
	ps2 := stpv1.PortState_builder{
		InterfaceName: &p1Name,
	}.Build()

	src := netmodel.SourceContext{
		DeviceID: "sw1",
		Origin:   "test-source",
		Context:  "conflicts",
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	res, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p2}, vlans, []*switchingv1.FdbEntry{fdb1, fdb2}, nil, nil, []*stpv1.PortState{ps1, ps2}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load with conflicting rows failed: %v", err)
	}

	// Conflicts must be recorded in Report.Conflicts
	if len(res.Report.Conflicts) == 0 {
		t.Fatalf("expected conflicts in report, got 0")
	}

	// Readiness must be Unstable according to shared status precedence
	if got := res.Readiness(); got != analysis.Unstable {
		t.Errorf("res.Readiness() = %v, want Unstable", got)
	}

	// Switch remains constructible directly from spec
	sw, err := vswitch.NewWithSpec(res.Spec)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec with conflicting rows failed: %v", err)
	}
	if sw == nil {
		t.Fatal("expected non-nil switch")
	}
}

func TestLoad_ErrorVersusResultSeparation(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	src := netmodel.SourceContext{DeviceID: "sw1"}

	// 1. Empty interface list is an impossible construction error
	if _, err := netmodel.Load(now, src, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Errorf("Load(empty ifaces) succeeded, want error")
	}

	// 2. Duplicate interface name is an impossible construction error
	p1 := makeTestInterface("1/1/1", true)
	p1Dup := makeTestInterface("1/1/1", true)
	if _, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p1Dup}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Errorf("Load(duplicate port name) succeeded, want error")
	}

	// 3. LAG parent referring to non-existent interface is an impossible construction error
	parentBogus := "nonexistent"
	pLagMember := interfacev1.Interface_builder{
		Name: &parentBogus,
		Physical: interfacev1.PhysicalInterface_builder{
			LagParent: &parentBogus,
		}.Build(),
	}.Build()
	if _, err := netmodel.Load(now, src, []*interfacev1.Interface{pLagMember}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Errorf("Load(invalid LAG parent) succeeded, want error")
	}
}

func TestLoad_CopyIsolationAndDeterministicOrdering(t *testing.T) {
	p1 := makeTestInterface("1/1/1", true)
	p2 := makeTestInterface("1/1/2", true)

	src := netmodel.SourceContext{
		DeviceID: "sw1",
		Origin:   "test-source",
		Context:  "isolation",
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	res1, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p2}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	res2, err := netmodel.Load(now, src, []*interfacev1.Interface{p2, p1}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load shuffled failed: %v", err)
	}

	// Deterministic report capabilities
	if !slices.Equal(res1.Report.Capabilities, res2.Report.Capabilities) {
		t.Errorf("res1 caps %+v != res2 caps %+v", res1.Report.Capabilities, res2.Report.Capabilities)
	}

	// Clone isolation
	cloned := res1.Clone()
	if len(cloned.Report.Defaults) > 0 {
		cloned.Report.Defaults[0].Field = "mutated"
		if res1.Report.Defaults[0].Field == "mutated" {
			t.Errorf("mutating cloned report affected original result")
		}
	}
}

func TestLoad_SpecConfigNormalizedDirectly(t *testing.T) {
	p1 := makeTestInterface("1/1/1", true)
	p2 := makeTestInterface("1/1/2", true)

	src := netmodel.SourceContext{
		DeviceID: "sw1",
		Origin:   "test-source",
		Context:  "normalize-spec",
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	res, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p2}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// 1. Spec.Config must be structurally and semantically normalized directly on load.
	if res.Spec.Config.MAC == (netaddr.MAC{}) {
		t.Errorf("res.Spec.Config.MAC is zero, want normalized base MAC")
	}
	if !res.Spec.Config.Equal(res.Spec.Config.Normalize()) {
		t.Errorf("res.Spec.Config is not equal to its own Normalize() output")
	}

	// 2. NewWithSpec must construct from Spec without changing the configuration.
	sw, err := vswitch.NewWithSpec(res.Spec)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec failed: %v", err)
	}
	if !sw.Spec().Config.Equal(res.Spec.Config) {
		t.Errorf("sw.Spec().Config differs from res.Spec.Config; NewWithSpec mutated config")
	}

	// 3. Explicit vs defaulted observable equivalence:
	// A model with an explicit MAC normalizes to the same MAC format.
	explicitMAC := res.Spec.Config.MAC
	if explicitMAC == (netaddr.MAC{}) {
		t.Errorf("expected non-zero explicit MAC")
	}
}

func TestLoad_ShuffledFdbRowsYieldEqualSpec(t *testing.T) {
	p1 := makeTestInterface("1/1/1", true)
	p2 := makeTestInterface("1/1/2", true)

	vid10 := uint32(10)
	vid20 := uint32(20)
	vlanName10 := "vlan10"
	vlanName20 := "vlan20"
	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: &vid10, Name: &vlanName10}.Build(),
		switchingv1.Vlan_builder{Id: &vid20, Name: &vlanName20}.Build(),
	}

	mac1 := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	mac2 := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	mac3 := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x03}
	fdbStatic := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	fdbActive := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	p1Name := "1/1/1"
	p2Name := "1/1/2"

	fdb1 := switchingv1.FdbEntry_builder{
		VlanId:        &vid10,
		InterfaceName: &p1Name,
		Mac:           addrv1.Eui48Address_builder{Octets: mac1}.Build(),
		Kind:          &fdbStatic,
		Status:        &fdbActive,
	}.Build()

	fdb2 := switchingv1.FdbEntry_builder{
		VlanId:        &vid20,
		InterfaceName: &p2Name,
		Mac:           addrv1.Eui48Address_builder{Octets: mac2}.Build(),
		Kind:          &fdbStatic,
		Status:        &fdbActive,
	}.Build()

	fdb3 := switchingv1.FdbEntry_builder{
		VlanId:        &vid10,
		InterfaceName: &p1Name,
		Mac:           addrv1.Eui48Address_builder{Octets: mac3}.Build(),
		Kind:          &fdbStatic,
		Status:        &fdbActive,
	}.Build()

	src := netmodel.SourceContext{
		DeviceID: "sw1",
		Origin:   "test-source",
		Context:  "shuffled-fdb",
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	// Shuffled orders of valid static FDB entries
	res1, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p2}, vlans, []*switchingv1.FdbEntry{fdb1, fdb2, fdb3}, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load order 1 failed: %v", err)
	}

	res2, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p2}, vlans, []*switchingv1.FdbEntry{fdb3, fdb2, fdb1}, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load order 2 failed: %v", err)
	}

	res3, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p2}, vlans, []*switchingv1.FdbEntry{fdb2, fdb1, fdb3}, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load order 3 failed: %v", err)
	}

	// Shuffled inputs must produce equal ConstructionSpecs and identical seed order
	if !res1.Spec.Equal(res2.Spec) {
		t.Errorf("res1.Spec != res2.Spec for shuffled FDB rows")
	}
	if !res1.Spec.Equal(res3.Spec) {
		t.Errorf("res1.Spec != res3.Spec for shuffled FDB rows")
	}
	if !slices.Equal(res1.Spec.Seeds, res2.Spec.Seeds) {
		t.Errorf("res1.Spec.Seeds %+v != res2.Spec.Seeds %+v", res1.Spec.Seeds, res2.Spec.Seeds)
	}

	// Verify canonical sorting: FID 10 (mac1, then mac3), then FID 20 (mac2)
	if len(res1.Spec.Seeds) != 3 {
		t.Fatalf("len(res1.Spec.Seeds) = %d, want 3", len(res1.Spec.Seeds))
	}
	if res1.Spec.Seeds[0].FID != 10 || res1.Spec.Seeds[0].MAC.String() != "00:11:22:33:44:01" {
		t.Errorf("seed 0 = %+v, want FID 10 mac 01", res1.Spec.Seeds[0])
	}
	if res1.Spec.Seeds[1].FID != 10 || res1.Spec.Seeds[1].MAC.String() != "00:11:22:33:44:03" {
		t.Errorf("seed 1 = %+v, want FID 10 mac 03", res1.Spec.Seeds[1])
	}
	if res1.Spec.Seeds[2].FID != 20 || res1.Spec.Seeds[2].MAC.String() != "00:11:22:33:44:02" {
		t.Errorf("seed 2 = %+v, want FID 20 mac 02", res1.Spec.Seeds[2])
	}
}

func TestLoad_InvalidNumericAdminOperEnum(t *testing.T) {
	adminInvalid := interfacev1.AdminStatus(99)
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	p1Name := "1/1/1"
	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminInvalid,
		OperStatus:  &operUp,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()

	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operInvalid := interfacev1.OperStatus(99)
	p2Name := "1/1/2"
	p2 := interfacev1.Interface_builder{
		Name:        &p2Name,
		AdminStatus: &adminUp,
		OperStatus:  &operInvalid,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()

	// Known non-forwarding statuses
	adminDown := interfacev1.AdminStatus_ADMIN_STATUS_DOWN
	p3Name := "1/1/3"
	p3 := interfacev1.Interface_builder{
		Name:        &p3Name,
		AdminStatus: &adminDown,
		OperStatus:  &operUp,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()

	operTesting := interfacev1.OperStatus_OPER_STATUS_TESTING
	p4Name := "1/1/4"
	p4 := interfacev1.Interface_builder{
		Name:        &p4Name,
		AdminStatus: &adminUp,
		OperStatus:  &operTesting,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()

	p5 := makeTestInterface("1/1/5", true)

	src := netmodel.SourceContext{
		DeviceID: "sw1",
		Origin:   "test-source",
		Context:  "invalid-enums",
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

	res, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p2, p3, p4, p5}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// 1. Invalid numeric admin status enum must produce Unknown, not silent Down
	port1, ok := res.Spec.Config.Ports.Port("1/1/1")
	if !ok || port1.AdminStatus != port.Unknown {
		t.Errorf("1/1/1 AdminStatus = %v, want Unknown", port1.AdminStatus)
	}
	issues1 := res.Metadata.IssuesFor(analysis.PortScope("sw1", "1/1/1"))
	if len(issues1) == 0 || issues1[0].Code != netmodel.IssueInvalidAdminStatus || issues1[0].Status != analysis.Incomplete {
		t.Errorf("1/1/1 issues = %+v, want IssueInvalidAdminStatus with Incomplete status", issues1)
	}

	// 2. Invalid numeric oper status enum must produce Unknown, not silent Down
	port2, ok := res.Spec.Config.Ports.Port("1/1/2")
	if !ok || port2.OperStatus != port.Unknown {
		t.Errorf("1/1/2 OperStatus = %v, want Unknown", port2.OperStatus)
	}
	issues2 := res.Metadata.IssuesFor(analysis.PortScope("sw1", "1/1/2"))
	if len(issues2) == 0 || issues2[0].Code != netmodel.IssueInvalidOperStatus || issues2[0].Status != analysis.Incomplete {
		t.Errorf("1/1/2 issues = %+v, want IssueInvalidOperStatus with Incomplete status", issues2)
	}

	// 3. Known non-forwarding statuses map deliberately to Down without invalid enum issues
	port3, ok := res.Spec.Config.Ports.Port("1/1/3")
	if !ok || port3.AdminStatus != port.Down {
		t.Errorf("1/1/3 AdminStatus = %v, want Down", port3.AdminStatus)
	}
	if issues := res.Metadata.IssuesFor(analysis.PortScope("sw1", "1/1/3")); len(issues) != 0 {
		t.Errorf("1/1/3 unexpected issues: %+v", issues)
	}

	port4, ok := res.Spec.Config.Ports.Port("1/1/4")
	if !ok || port4.OperStatus != port.Down {
		t.Errorf("1/1/4 OperStatus = %v, want Down", port4.OperStatus)
	}
	if issues := res.Metadata.IssuesFor(analysis.PortScope("sw1", "1/1/4")); len(issues) != 0 {
		t.Errorf("1/1/4 unexpected issues: %+v", issues)
	}

	// 4. Construct switch and verify forwarding behavior
	sw, err := vswitch.NewWithSpec(res.Spec)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec failed: %v", err)
	}

	frame := ethernet.Frame{
		Src: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}

	// 1/1/1 (invalid admin enum -> Unknown) drops with unknown-operational-status and Incomplete status
	fwd1 := sw.Forward(now, "1/1/1", frame)
	if fwd1.Metadata.Status() != analysis.Incomplete {
		t.Errorf("1/1/1 Forward status = %v, want Incomplete", fwd1.Metadata.Status())
	}

	// 1/1/2 (invalid oper enum -> Unknown) drops with unknown-operational-status and Incomplete status
	fwd2 := sw.Forward(now, "1/1/2", frame)
	if fwd2.Metadata.Status() != analysis.Incomplete {
		t.Errorf("1/1/2 Forward status = %v, want Incomplete", fwd2.Metadata.Status())
	}

	// 1/1/3 (known admin down) drops with port-down and Complete status
	fwd3 := sw.Forward(now, "1/1/3", frame)
	if fwd3.Outcome != trace.Dropped || fwd3.Reason != port.ReasonPortDown || fwd3.Metadata.Status() != analysis.Complete {
		t.Errorf("1/1/3 Forward = %+v, want Dropped port-down with Complete status", fwd3)
	}

	// 1/1/4 (known oper testing -> down) drops with port-down and Complete status
	fwd4 := sw.Forward(now, "1/1/4", frame)
	if fwd4.Outcome != trace.Dropped || fwd4.Reason != port.ReasonPortDown || fwd4.Metadata.Status() != analysis.Complete {
		t.Errorf("1/1/4 Forward = %+v, want Dropped port-down with Complete status", fwd4)
	}
}
