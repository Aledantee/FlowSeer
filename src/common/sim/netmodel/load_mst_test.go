package netmodel

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

const mstEmptyDigest = "ac36177f50283cd4b83821d8ab26de62"

func mstBridge(t *testing.T, digest string) *stpv1.BridgeState {
	t.Helper()
	b, err := hex.DecodeString(digest)
	if err != nil {
		t.Fatal(err)
	}
	return stpv1.BridgeState_builder{
		NetworkInstance: new(DefaultNetworkInstance),
		ProtocolVersion: stpv1.ProtocolVersion_PROTOCOL_VERSION_MSTP.Enum(),
		BridgeId:        mstBridgeID(new(uint32(32768))),
		MstConfigId:     stpv1.MstConfigId_builder{Name: new(""), RevisionLevel: new(uint32(0)), Digest: b}.Build(),
	}.Build()
}

func mstBridgeID(priority *uint32) *stpv1.BridgeId {
	return stpv1.BridgeId_builder{
		Priority: priority,
		Address:  addrv1.Eui48Address_builder{Octets: []byte{2, 0, 0, 0, 0, 1}}.Build(),
	}.Build()
}

func mstInstance(id uint32, priority *uint32) *stpv1.MstInstance {
	return stpv1.MstInstance_builder{NetworkInstance: new(DefaultNetworkInstance), MstId: new(id), BridgeId: mstBridgeID(priority)}.Build()
}

func mstMap(id uint32, vids ...uint32) *stpv1.MstVlanMap {
	return stpv1.MstVlanMap_builder{NetworkInstance: new(DefaultNetworkInstance), MstId: new(id), VlanIds: vids}.Build()
}

func runMST(t *testing.T, bridge *stpv1.BridgeState, instances []*stpv1.MstInstance, maps []*stpv1.MstVlanMap, mstPorts ...*stpv1.MstPort) (*stp.MST, *loadContext, []analysis.Issue) {
	t.Helper()
	ports, err := port.NewBuilder().Add(port.Port{Name: "p1"}).Build()
	if err != nil {
		t.Fatal(err)
	}
	c := newLoadContext(SourceContext{DeviceID: "sw"})
	common := map[string]stp.Port{"p1": {}}
	for _, instance := range instances {
		if instance == nil || !defaultInstance(instance) {
			continue
		}
		if !slices.ContainsFunc(mstPorts, func(row *stpv1.MstPort) bool {
			return row != nil && row.GetMstId() == instance.GetMstId() && row.GetInterfaceName() == "p1"
		}) {
			mstPorts = append(mstPorts, stpv1.MstPort_builder{
				MstId: new(instance.GetMstId()), InterfaceName: new("p1"),
				Priority: new(uint32(128)), AdminPathCost: new(uint32(0)),
			}.Build())
		}
	}
	m, issues := c.loadMST(bridge, instances, mstPorts, maps, common, ports)
	for _, issue := range issues {
		if len(issue.Evidence) == 0 || issue.Scope.Kind() != analysis.ScopeField {
			t.Errorf("issue lacks field scope or evidence: %+v", issue)
		}
	}
	if m != nil {
		env := layer.Env{NodeID: "sw", Ports: ports}
		cfg := stp.Config{Address: netaddr.MAC{2, 0, 0, 0, 0, 1}, Ports: common, MST: m}.Normalize(env)
		if err := cfg.Validate(env); err != nil {
			t.Fatalf("candidate does not construct: %v", err)
		}
	}
	return m, c, issues
}

func requireMSTIssue(t *testing.T, issues []analysis.Issue, field string, status analysis.Status) {
	t.Helper()
	for _, issue := range issues {
		if strings.Contains(issue.Scope.String(), field) && issue.Status == status {
			return
		}
	}
	t.Fatalf("issues = %+v, want %s issue at %s", issues, status, field)
}

func TestMSTRegionIdentityAndHops(t *testing.T) {
	rows := []struct {
		name, field string
		change      func(*stpv1.BridgeState)
		valid       bool
		hops        uint8
	}{
		{"empty_name_zero_revision", "", func(*stpv1.BridgeState) {}, true, 20},
		{"name32_revision65535", "", func(b *stpv1.BridgeState) {
			b.GetMstConfigId().SetName(strings.Repeat("a", 32))
			b.GetMstConfigId().SetRevisionLevel(65535)
		}, true, 20},
		{"missing_identity", "mst_config_id", func(b *stpv1.BridgeState) { b.ClearMstConfigId() }, false, 0},
		{"absent_name", "name", func(b *stpv1.BridgeState) { b.GetMstConfigId().ClearName() }, false, 0},
		{"absent_revision", "revision_level", func(b *stpv1.BridgeState) { b.GetMstConfigId().ClearRevisionLevel() }, false, 0},
		{"absent_digest", "digest", func(b *stpv1.BridgeState) { b.GetMstConfigId().ClearDigest() }, false, 0},
		{"name33", "name", func(b *stpv1.BridgeState) { b.GetMstConfigId().SetName(strings.Repeat("a", 33)) }, false, 0},
		{"name_octets", "name", func(b *stpv1.BridgeState) { b.GetMstConfigId().SetName(strings.Repeat("é", 17)) }, false, 0},
		{"name_nul", "name", func(b *stpv1.BridgeState) { b.GetMstConfigId().SetName("a\x00b") }, false, 0},
		{"revision65536", "revision_level", func(b *stpv1.BridgeState) { b.GetMstConfigId().SetRevisionLevel(65536) }, false, 0},
		{"digest15", "digest", func(b *stpv1.BridgeState) { b.GetMstConfigId().SetDigest(make([]byte, 15)) }, false, 0},
		{"digest17", "digest", func(b *stpv1.BridgeState) { b.GetMstConfigId().SetDigest(make([]byte, 17)) }, false, 0},
		{"hops0", "max_hops", func(b *stpv1.BridgeState) { b.SetMaxHops(0) }, false, 0},
		{"hops5", "max_hops", func(b *stpv1.BridgeState) { b.SetMaxHops(5) }, false, 0},
		{"hops6", "", func(b *stpv1.BridgeState) { b.SetMaxHops(6) }, true, 6},
		{"hops40", "", func(b *stpv1.BridgeState) { b.SetMaxHops(40) }, true, 40},
		{"hops41", "max_hops", func(b *stpv1.BridgeState) { b.SetMaxHops(41) }, false, 0},
		{"hops262", "max_hops", func(b *stpv1.BridgeState) { b.SetMaxHops(262) }, false, 0},
	}
	reached := 0
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			b := mstBridge(t, mstEmptyDigest)
			row.change(b)
			m, c, issues := runMST(t, b, nil, nil)
			if row.valid {
				if m == nil || len(issues) != 0 || m.MaxHops != row.hops || m.Name != b.GetMstConfigId().GetName() || m.Revision != uint16(b.GetMstConfigId().GetRevisionLevel()) {
					t.Fatalf("candidate/issues = %+v/%+v, want reported identity and hops %d", m, issues, row.hops)
				}
				if !b.HasMaxHops() && (len(c.assumptions) != 1 || len(c.assumptions[0].Evidence) == 0 || !strings.Contains(c.assumptions[0].Statement, "max_hops: 20")) {
					t.Fatalf("assumptions = %+v, want evidenced max_hops 20", c.assumptions)
				}
			} else {
				if m != nil {
					t.Fatalf("candidate = %+v, want omitted region", m)
				}
				requireMSTIssue(t, issues, row.field, analysis.Incomplete)
				wantScope := c.mstScope("mst_config_id", row.field)
				if row.field == "mst_config_id" || row.field == "max_hops" {
					wantScope = c.mstScope(row.field)
				}
				if issues[0].Scope != wantScope {
					t.Fatalf("issue scope = %s, want %s", issues[0].Scope, wantScope)
				}
				if strings.HasPrefix(row.name, "absent_") && !strings.Contains(issues[0].Message, "unreported") {
					t.Fatalf("issue = %+v, want absent field distinguished from malformed value", issues[0])
				}
				if (row.name == "digest15" || row.name == "digest17") && !strings.Contains(issues[0].Message, "16 octets") {
					t.Fatalf("issue = %+v, want malformed digest width distinguished from allocation mismatch", issues[0])
				}
			}
			reached++
		})
	}
	if reached != 18 {
		t.Fatalf("reached %d cases, want 18", reached)
	}
}

func TestMSTInstanceAdmissionAndPriorityPresence(t *testing.T) {
	rows := []struct {
		name, field string
		change      func(*stpv1.MstInstance)
		valid       bool
	}{
		{"id_absent", "mst_id", func(i *stpv1.MstInstance) { i.ClearMstId() }, false},
		{"id0", "mst_id", func(i *stpv1.MstInstance) { i.SetMstId(0) }, false},
		{"id1", "", func(*stpv1.MstInstance) {}, true},
		{"id4094", "", func(i *stpv1.MstInstance) { i.SetMstId(4094) }, true},
		{"id4095", "mst_id", func(i *stpv1.MstInstance) { i.SetMstId(4095) }, false},
		{"id65537", "mst_id", func(i *stpv1.MstInstance) { i.SetMstId(65537) }, false},
		{"bridge_absent", "bridge_id", func(i *stpv1.MstInstance) { i.ClearBridgeId() }, false},
		{"address_absent", "address", func(i *stpv1.MstInstance) { i.GetBridgeId().ClearAddress() }, false},
		{"address5", "address", func(i *stpv1.MstInstance) { i.GetBridgeId().GetAddress().SetOctets(make([]byte, 5)) }, false},
		{"address7", "address", func(i *stpv1.MstInstance) { i.GetBridgeId().GetAddress().SetOctets(make([]byte, 7)) }, false},
		{"different_bridge", "address", func(i *stpv1.MstInstance) { i.GetBridgeId().GetAddress().SetOctets([]byte{2, 0, 0, 0, 0, 2}) }, false},
		{"priority_absent", "", func(i *stpv1.MstInstance) { i.GetBridgeId().ClearPriority() }, true},
		{"priority0", "", func(i *stpv1.MstInstance) { i.GetBridgeId().SetPriority(0) }, true},
		{"priority4096", "", func(i *stpv1.MstInstance) { i.GetBridgeId().SetPriority(4096) }, true},
		{"priority61440", "", func(i *stpv1.MstInstance) { i.GetBridgeId().SetPriority(61440) }, true},
		{"priority1", "priority", func(i *stpv1.MstInstance) { i.GetBridgeId().SetPriority(1) }, false},
		{"priority65536", "priority", func(i *stpv1.MstInstance) { i.GetBridgeId().SetPriority(65536) }, false},
	}
	reached := 0
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			i := mstInstance(1, new(uint32(32768)))
			row.change(i)
			m, c, issues := runMST(t, mstBridge(t, mstEmptyDigest), []*stpv1.MstInstance{i, mstInstance(2, new(uint32(4096)))}, []*stpv1.MstVlanMap{mstMap(i.GetMstId()), mstMap(2)})
			if m == nil {
				t.Fatal("empty-allocation sibling lost with region")
			}
			if _, ok := m.Instances[2]; !ok {
				t.Fatal("valid sibling omitted")
			}
			inst, ok := m.Instances[bpdu.MSTID(i.GetMstId())]
			if ok != row.valid {
				t.Fatalf("instance retained = %t, want %t", ok, row.valid)
			}
			if row.valid {
				if inst.Priority != uint16(i.GetBridgeId().GetPriority()) || inst.PriorityPresent != i.GetBridgeId().HasPriority() {
					t.Fatalf("instance = %+v, want reported priority/presence", inst)
				}
				n := m.Normalize().Instances[bpdu.MSTID(i.GetMstId())]
				want := uint16(i.GetBridgeId().GetPriority())
				if !i.GetBridgeId().HasPriority() {
					want = 32768
					if len(c.assumptions) != 2 || len(c.assumptions[1].Evidence) == 0 || !strings.Contains(c.assumptions[1].Statement, "priority: 32768") {
						t.Fatalf("assumptions = %+v, want evidenced default priority", c.assumptions)
					}
				}
				if n.Priority != want {
					t.Fatalf("normalized priority = %d, want %d", n.Priority, want)
				}
			} else {
				found := false
				for _, issue := range issues {
					if issue.Code == IssueIncompleteMSTInstance && issue.Scope == c.mstScope("instances", fmt.Sprint(i.GetMstId()), row.field) {
						found = true
					}
				}
				if !found {
					t.Fatalf("issues = %+v, want instance refusal at %s", issues, row.field)
				}
			}
			reached++
		})
	}
	if reached != 17 {
		t.Fatalf("reached %d cases, want 17", reached)
	}
}

func TestMSTInstanceConflictsAndObservations(t *testing.T) {
	reached := 0
	for _, variant := range []string{"identical", "observations", "priority", "presence"} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%t", variant, reverse), func(t *testing.T) {
				a, b := mstInstance(1, new(uint32(0))), mstInstance(1, new(uint32(0)))
				switch variant {
				case "observations":
					b.SetRootPathCost(99)
					b.SetTopologyChanges(7)
					b.SetDesignatedRoot(mstBridgeID(new(uint32(4096))))
				case "priority":
					b.GetBridgeId().SetPriority(4096)
				case "presence":
					a.GetBridgeId().ClearPriority()
					b.GetBridgeId().SetPriority(32768)
				}
				rows := []*stpv1.MstInstance{a, b, mstInstance(2, new(uint32(4096)))}
				if reverse {
					slices.Reverse(rows)
				}
				dependent := stpv1.MstPort_builder{MstId: new(uint32(1)), InterfaceName: new("p1"), Priority: new(uint32(128)), AdminPathCost: new(uint32(0))}.Build()
				m, _, issues := runMST(t, mstBridge(t, mstEmptyDigest), rows, []*stpv1.MstVlanMap{mstMap(1), mstMap(2)}, dependent)
				if m == nil || len(m.Instances) == 0 {
					t.Fatal("empty-allocation sibling lost")
				}
				_, kept := m.Instances[1]
				conflict := variant == "priority" || variant == "presence"
				if kept == conflict {
					t.Fatalf("kept = %t, conflict = %t", kept, conflict)
				}
				if conflict {
					requireMSTIssue(t, issues, "instances", analysis.Unstable)
					requireMSTIssue(t, issues, "vlan_maps", analysis.Unstable)
					requireMSTIssue(t, issues, "ports", analysis.Unstable)
				} else if len(issues) != 0 {
					t.Fatalf("issues = %+v, want none", issues)
				}
				reached++
			})
		}
	}
	if reached != 8 {
		t.Fatalf("reached %d cases, want 8", reached)
	}
}

func TestMSTMapAdmissionAndContestedAllocations(t *testing.T) {
	rows := []struct {
		name   string
		maps   func() []*stpv1.MstVlanMap
		kept   bool
		field  string
		status analysis.Status
	}{
		{"empty", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(1)} }, true, "", analysis.Complete},
		{"cist_only", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(0, 10), mstMap(1)} }, true, "", analysis.Complete},
		{"missing_map", func() []*stpv1.MstVlanMap { return nil }, false, "vlan_maps", analysis.Incomplete},
		{"target_absent", func() []*stpv1.MstVlanMap { m := mstMap(1); m.ClearMstId(); return []*stpv1.MstVlanMap{m} }, false, "mst_id", analysis.Incomplete},
		{"target4095", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(4095)} }, false, "mst_id", analysis.Incomplete},
		{"target65537", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(65537)} }, false, "mst_id", analysis.Incomplete},
		{"dangling_target", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(3, 10), mstMap(1)} }, true, "vlan_maps", analysis.Incomplete},
		{"vid0", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(1, 0)} }, false, "vlan_ids", analysis.Incomplete},
		{"vid4095", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(1, 4095)} }, false, "vlan_ids", analysis.Incomplete},
		{"vid65546", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(1, 65546)} }, false, "vlan_ids", analysis.Incomplete},
		{"duplicate_within", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(1, 10, 10)} }, false, "vlan_ids", analysis.Unstable},
		{"duplicate_across", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(1, 10), mstMap(2, 10)} }, true, "vlan_ids", analysis.Unstable},
		{"duplicate_cist", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(0, 10), mstMap(1, 10)} }, true, "vlan_ids", analysis.Unstable},
		{"conflicting_key", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(1), mstMap(1, 10)} }, false, "vlan_maps", analysis.Unstable},
		{"identical_key", func() []*stpv1.MstVlanMap { return []*stpv1.MstVlanMap{mstMap(1), mstMap(1)} }, true, "", analysis.Complete},
	}
	reached := 0
	for _, row := range rows {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%t", row.name, reverse), func(t *testing.T) {
				maps := row.maps()
				maps = append(maps, mstMap(2))
				if row.name == "duplicate_across" {
					maps = maps[:2]
				}
				if reverse {
					slices.Reverse(maps)
				}
				m, _, issues := runMST(t, mstBridge(t, mstEmptyDigest), []*stpv1.MstInstance{mstInstance(1, new(uint32(0))), mstInstance(2, new(uint32(4096)))}, maps)
				if m == nil {
					t.Fatal("reconstructed empty allocation lost with region")
				}
				_, kept := m.Instances[1]
				if kept != row.kept {
					t.Fatalf("instance retained = %t, want %t", kept, row.kept)
				}
				if _, ok := m.Instances[2]; !ok {
					t.Fatal("sibling omitted")
				}
				if row.field != "" {
					requireMSTIssue(t, issues, row.field, row.status)
				} else if len(issues) != 0 {
					t.Fatalf("issues = %+v, want none", issues)
				}
				if len(m.Instances[1].VLANs) != 0 {
					t.Fatalf("VLANs = %v, want empty uncontested allocation", m.Instances[1].VLANs)
				}
				reached++
			})
		}
	}
	if reached != 30 {
		t.Fatalf("reached %d cases, want 30", reached)
	}
}

func TestMSTDigestPreservesCompleteMap(t *testing.T) {
	// src/common/sim/layer/stp/README.md supplies this nonzero regression vector.
	const nonzero = "9357ebb7a8d74dd5fef4f2bab50531aa"
	reached := 0
	for _, variant := range []string{"matching", "mismatching", "conflict_omission", "contested_omission", "missing_map_omission"} {
		t.Run(variant, func(t *testing.T) {
			b := mstBridge(t, nonzero)
			instances := []*stpv1.MstInstance{mstInstance(1, new(uint32(0))), mstInstance(2, new(uint32(4096)))}
			maps := []*stpv1.MstVlanMap{mstMap(1, 10), mstMap(2, 20)}
			switch variant {
			case "mismatching":
				b.GetMstConfigId().SetDigest(make([]byte, 16))
			case "conflict_omission":
				instances = append(instances, mstInstance(1, new(uint32(4096))))
			case "contested_omission":
				maps = append(maps, mstMap(0, 10))
			case "missing_map_omission":
				maps = maps[1:]
			}
			m, _, issues := runMST(t, b, instances, maps)
			if variant == "matching" {
				if m == nil || !reflect.DeepEqual(m.Instances[1].VLANs, []vlan.ID{10}) || !reflect.DeepEqual(m.Instances[2].VLANs, []vlan.ID{20}) || len(issues) != 0 {
					t.Fatalf("candidate/issues = %+v/%+v, want complete map independent of bridge VLAN database", m, issues)
				}
			} else {
				if m != nil {
					t.Fatalf("candidate = %+v, want omitted region", m)
				}
				requireMSTIssue(t, issues, "digest", analysis.Incomplete)
				requireMSTIssue(t, issues, "instances", analysis.Incomplete)
				requireMSTIssue(t, issues, "vlan_maps", analysis.Incomplete)
			}
			reached++
		})
	}
	if reached != 5 {
		t.Fatalf("reached %d cases, want 5", reached)
	}
}

func TestMSTInstanceCapacity(t *testing.T) {
	reached := 0
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var instances []*stpv1.MstInstance
			var maps []*stpv1.MstVlanMap
			for id := 1; id <= count; id++ {
				instances = append(instances, mstInstance(uint32(id), new(uint32(0))))
				maps = append(maps, mstMap(uint32(id)))
			}
			m, c, issues := runMST(t, mstBridge(t, mstEmptyDigest), instances, maps)
			if count == 64 {
				if m == nil || len(m.Instances) != 64 || len(issues) != 0 {
					t.Fatalf("candidate/issues = %+v/%+v, want 64 instances", m, issues)
				}
			} else {
				if m != nil {
					t.Fatalf("candidate = %+v, want omitted region rather than truncation", m)
				}
				requireMSTIssue(t, issues, "instances", analysis.Incomplete)
				if issues[0].Code != IssueIncompleteMSTRegion || issues[0].Scope != c.mstScope("instances") {
					t.Fatalf("region issue = %+v, want capacity refusal at region instances", issues[0])
				}
			}
			reached++
		})
	}
	if reached != 2 {
		t.Fatalf("reached %d cases, want 2", reached)
	}
}

func TestMSTDomainGatingPrecedesClaims(t *testing.T) {
	reached := 0
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			accepted, blue, red := mstInstance(1, new(uint32(0))), mstInstance(1, new(uint32(4096))), mstInstance(1, new(uint32(8192)))
			blue.SetNetworkInstance("blue")
			red.SetNetworkInstance("red")
			blueMap, redMap := mstMap(1, 10), mstMap(1, 20)
			blueMap.SetNetworkInstance("blue")
			redMap.SetNetworkInstance("red")
			rows := []*stpv1.MstInstance{accepted, blue, red}
			maps := []*stpv1.MstVlanMap{mstMap(1), blueMap, redMap}
			if reverse {
				slices.Reverse(rows)
				slices.Reverse(maps)
			}
			m, _, issues := runMST(t, mstBridge(t, mstEmptyDigest), rows, maps)
			if m == nil || len(m.Instances) != 1 || m.Instances[1].Priority != 0 || len(m.Instances[1].VLANs) != 0 {
				t.Fatalf("candidate = %+v, want default domain only", m)
			}
			if len(issues) != 4 {
				t.Fatalf("issues = %+v, want four refused domain rows", issues)
			}
			for _, issue := range issues {
				if issue.Status != analysis.Unsupported {
					t.Fatalf("status = %s, want Unsupported", issue.Status)
				}
			}
			reached++
		})
	}
	if reached != 2 {
		t.Fatalf("reached %d cases, want 2", reached)
	}
	i, allocation := mstInstance(1, new(uint32(0))), mstMap(1)
	i.ClearNetworkInstance()
	allocation.ClearNetworkInstance()
	m, c, issues := runMST(t, mstBridge(t, mstEmptyDigest), []*stpv1.MstInstance{i}, []*stpv1.MstVlanMap{allocation})
	if m == nil || len(issues) != 0 || len(c.assumptions) != 3 {
		t.Fatalf("candidate/issues/assumptions = %+v/%+v/%+v, want admitted default with three assumptions", m, issues, c.assumptions)
	}
	for _, a := range c.assumptions {
		if len(a.Evidence) == 0 {
			t.Fatalf("assumption lacks evidence: %+v", a)
		}
	}
}
