package stp_test

import (
	"encoding/hex"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func TestMSTConfigIDDigest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		instances  map[stp.MSTID]stp.Instance
		wantDigest string
	}{
		{
			name:       "all-zero table",
			instances:  nil,
			wantDigest: "ac36177f50283cd4b83821d8ab26de62",
		},
		{
			name: "VID 10 on MSTID 1 and VID 20 on MSTID 2",
			instances: map[stp.MSTID]stp.Instance{
				1: {VLANs: []vlan.ID{10}},
				2: {VLANs: []vlan.ID{20}},
			},
			wantDigest: "9357ebb7a8d74dd5fef4f2bab50531aa",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := stp.MST{Instances: tc.instances}
			id := m.ConfigID()

			want, err := hex.DecodeString(tc.wantDigest)
			if err != nil {
				t.Fatalf("hex.DecodeString(%q): %v", tc.wantDigest, err)
			}
			if got := id.Digest[:]; hex.EncodeToString(got) != hex.EncodeToString(want) {
				t.Errorf("ConfigID().Digest = %x, want %x", got, want)
			}
		})
	}
}

func TestMSTConfigIDCarriesSelectorNameAndRevision(t *testing.T) {
	t.Parallel()

	m := stp.MST{Name: "region-1", Revision: 7}
	id := m.ConfigID()

	if id.Selector != 0 {
		t.Errorf("Selector = %d, want 0", id.Selector)
	}
	if id.Name != "region-1" {
		t.Errorf("Name = %q, want %q", id.Name, "region-1")
	}
	if id.Revision != 7 {
		t.Errorf("Revision = %d, want 7", id.Revision)
	}
}

func TestMSTValidate(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}
	// "1/1/2" is in the port table but never added as an STP port, so an
	// instance may reference it in the port table check but not the
	// STP-port-set check.
	stpPorts := map[string]stp.Port{"1/1/1": {}}

	tests := []struct {
		name      string
		mst       stp.MST
		wantErr   bool
		wantField string
	}{
		{
			name: "valid region",
			mst: stp.MST{
				Name:    "region-1",
				MaxHops: 20,
				Instances: map[stp.MSTID]stp.Instance{
					1: {Priority: 4096, VLANs: []vlan.ID{10}, Ports: map[string]stp.InstancePort{"1/1/1": {}}},
				},
			},
			wantErr: false,
		},
		{
			name: "MSTID zero rejected",
			mst: stp.MST{
				Instances: map[stp.MSTID]stp.Instance{0: {}},
			},
			wantErr:   true,
			wantField: "mst.instances.0",
		},
		{
			name: "MSTID above 4094 rejected",
			mst: stp.MST{
				Instances: map[stp.MSTID]stp.Instance{4095: {}},
			},
			wantErr:   true,
			wantField: "mst.instances.4095",
		},
		{
			name:      "max hops below six rejected",
			mst:       stp.MST{MaxHops: 5},
			wantErr:   true,
			wantField: "mst.max_hops",
		},
		{
			name:      "max hops above forty rejected",
			mst:       stp.MST{MaxHops: 41},
			wantErr:   true,
			wantField: "mst.max_hops",
		},
		{
			name: "instance priority not a multiple of 4096 rejected",
			mst: stp.MST{
				Instances: map[stp.MSTID]stp.Instance{1: {Priority: 100}},
			},
			wantErr:   true,
			wantField: "mst.instances.1.priority",
		},
		{
			name:      "name over 32 octets rejected",
			mst:       stp.MST{Name: "this configuration name is far too long"},
			wantErr:   true,
			wantField: "mst.name",
		},
		{
			name: "VID claimed twice rejected",
			mst: stp.MST{
				Instances: map[stp.MSTID]stp.Instance{
					1: {Priority: 4096, VLANs: []vlan.ID{10}},
					2: {Priority: 4096, VLANs: []vlan.ID{10}},
				},
			},
			wantErr:   true,
			wantField: "mst.instances.2.vlans",
		},
		{
			name: "unknown instance port rejected",
			mst: stp.MST{
				Instances: map[stp.MSTID]stp.Instance{
					1: {Priority: 4096, Ports: map[string]stp.InstancePort{"1/1/99": {}}},
				},
			},
			wantErr:   true,
			wantField: "mst.instances.1.ports.1/1/99",
		},
		{
			name: "instance port absent from the STP port set rejected",
			mst: stp.MST{
				Instances: map[stp.MSTID]stp.Instance{
					1: {Priority: 4096, Ports: map[string]stp.InstancePort{"1/1/2": {}}},
				},
			},
			wantErr:   true,
			wantField: "mst.instances.1.ports.1/1/2",
		},
		{
			name: "VID zero rejected",
			mst: stp.MST{
				Instances: map[stp.MSTID]stp.Instance{
					1: {Priority: 4096, VLANs: []vlan.ID{0}},
				},
			},
			wantErr:   true,
			wantField: "mst.instances.1.vlans",
		},
		{
			name: "VID 4095 rejected",
			mst: stp.MST{
				Instances: map[stp.MSTID]stp.Instance{
					1: {Priority: 4096, VLANs: []vlan.ID{4095}},
				},
			},
			wantErr:   true,
			wantField: "mst.instances.1.vlans",
		},
		{
			name: "VID 5000 rejected",
			mst: stp.MST{
				Instances: map[stp.MSTID]stp.Instance{
					1: {Priority: 4096, VLANs: []vlan.ID{5000}},
				},
			},
			wantErr:   true,
			wantField: "mst.instances.1.vlans",
		},
		{
			name: "region name containing NUL rejected",
			mst: stp.MST{
				Name: "region-a\x00",
			},
			wantErr:   true,
			wantField: "mst.name",
		},
		{
			name: "instance port path cost over maximum rejected",
			mst: stp.MST{
				Instances: map[stp.MSTID]stp.Instance{
					1: {
						Priority: 4096,
						Ports:    map[string]stp.InstancePort{"1/1/1": {PathCost: stp.MaxPathCost + 1}},
					},
				},
			},
			wantErr:   true,
			wantField: "mst.instances.1.ports.1/1/1.path_cost",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.mst.Validate(tbl, stpPorts)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, tc.wantErr)
			}
			if tc.wantErr {
				if got := errs.Attributes(err)["field"]; got != tc.wantField {
					t.Errorf("field attribute = %v, want %q", got, tc.wantField)
				}
			}
		})
	}
}

func TestMSTNormalizeIdempotent(t *testing.T) {
	t.Parallel()

	m := stp.MST{
		Name: "region-1",
		Instances: map[stp.MSTID]stp.Instance{
			1: {
				VLANs: []vlan.ID{30, 10, 20},
				Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 100}},
			},
		},
	}

	once := m.Normalize()
	twice := once.Normalize()

	if once.MaxHops != twice.MaxHops {
		t.Errorf("MaxHops: once %d, twice %d", once.MaxHops, twice.MaxHops)
	}
	onceInst, twiceInst := once.Instances[1], twice.Instances[1]
	if onceInst.Priority != twiceInst.Priority {
		t.Errorf("instance priority: once %d, twice %d", onceInst.Priority, twiceInst.Priority)
	}
	if len(onceInst.VLANs) != len(twiceInst.VLANs) {
		t.Fatalf("VLANs length: once %d, twice %d", len(onceInst.VLANs), len(twiceInst.VLANs))
	}
	for i := range onceInst.VLANs {
		if onceInst.VLANs[i] != twiceInst.VLANs[i] {
			t.Errorf("VLANs[%d]: once %d, twice %d", i, onceInst.VLANs[i], twiceInst.VLANs[i])
		}
	}
	oncePort, twicePort := onceInst.Ports["1/1/1"], twiceInst.Ports["1/1/1"]
	if oncePort != twicePort {
		t.Errorf("instance port: once %+v, twice %+v", oncePort, twicePort)
	}
}

func TestMSTNormalizeSortsVLANs(t *testing.T) {
	t.Parallel()

	m := stp.MST{
		Instances: map[stp.MSTID]stp.Instance{
			1: {VLANs: []vlan.ID{30, 10, 20}},
		},
	}

	norm := m.Normalize()
	want := []vlan.ID{10, 20, 30}
	got := norm.Instances[1].VLANs
	if len(got) != len(want) {
		t.Fatalf("VLANs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("VLANs[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestMSTClone(t *testing.T) {
	t.Parallel()

	m := stp.MST{
		Instances: map[stp.MSTID]stp.Instance{
			1: {
				VLANs: []vlan.ID{10},
				Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 100}},
			},
		},
	}

	cloned := m.Clone()
	cloned.Instances[1].VLANs[0] = 20
	clonedInst := cloned.Instances[1]
	clonedInst.Ports["1/1/1"] = stp.InstancePort{PathCost: 200}
	cloned.Instances[1] = clonedInst

	if m.Instances[1].VLANs[0] != 10 {
		t.Errorf("original VLAN modified: got %d, want 10", m.Instances[1].VLANs[0])
	}
	if m.Instances[1].Ports["1/1/1"].PathCost != 100 {
		t.Errorf("original instance port modified: got %d, want 100", m.Instances[1].Ports["1/1/1"].PathCost)
	}
}

// TestMSTValidateRejectsTheDigestCollisionVLAN reproduces the boundary
// classification failure: two regions sharing a name and revision, where one
// instance's VLAN list holds an out-of-range VID, are supposed to collide on
// the digest instead of being caught by validation. Both configurations must
// fail to validate so the digest never sees the out-of-range VID.
func TestMSTValidateRejectsTheDigestCollisionVLAN(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	a := stp.MST{
		Name: "region-1",
		Instances: map[stp.MSTID]stp.Instance{
			1: {Priority: 4096, VLANs: []vlan.ID{10}},
		},
	}
	b := stp.MST{
		Name: "region-1",
		Instances: map[stp.MSTID]stp.Instance{
			1: {Priority: 4096, VLANs: []vlan.ID{10, 5000}},
		},
	}

	if err := a.Validate(tbl, nil); err != nil {
		t.Errorf("a.Validate() = %v, want acceptance", err)
	}
	if err := b.Validate(tbl, nil); err == nil {
		t.Error("b.Validate() = nil, want rejection of VID 5000")
	}
}

// TestMSTValidateRefusesMoreInstancesThanOneBPDUCarries guards the seam
// between the region configuration and the wire: Encode refuses to build an
// MST BPDU whose version 3 length would not fit 16 bits, so a region that
// validates must not be able to reach that count.
func TestMSTValidateRefusesMoreInstancesThanOneBPDUCarries(t *testing.T) {
	t.Parallel()

	tbl, err := port.NewBuilder().Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

	// 4091 records is the most the version 3 length field can name; the MSTID
	// space runs to 4094, so a region can ask for more than the wire allows.
	instances := make(map[stp.MSTID]stp.Instance, 4092)
	for id := stp.MSTID(1); id <= 4092; id++ {
		instances[id] = stp.Instance{Priority: 4096}
	}

	m := stp.MST{Name: "region-1", Instances: instances}
	err = m.Validate(tbl, nil)
	if err == nil {
		t.Fatal("Validate() = nil, want rejection of a region no BPDU can carry")
	}

	if got := errs.Attributes(err)["field"]; got != "mst.instances" {
		t.Errorf("field = %v, want mst.instances", got)
	}
}

// TestMSTNormalizeDoesNotOverrideUnsetInstancePortPriority guards the
// instance-port priority override signal. Normalize must leave an instance
// port that never set a priority with PriorityPresent false, so the layer's
// "does this instance override the CIST port priority" check still has a
// real answer to read.
func TestMSTNormalizeDoesNotOverrideUnsetInstancePortPriority(t *testing.T) {
	t.Parallel()

	m := stp.MST{
		Instances: map[stp.MSTID]stp.Instance{
			1: {
				Priority: 4096,
				Ports:    map[string]stp.InstancePort{"1/1/1": {PathCost: 200_000}},
			},
		},
	}

	norm := m.Normalize()
	got := norm.Instances[1].Ports["1/1/1"]
	if got.PriorityPresent {
		t.Errorf("PriorityPresent = true, want false: normalization must not manufacture an override")
	}
	if got.Priority != 0 {
		t.Errorf("Priority = %d, want 0 (unfilled)", got.Priority)
	}
}

// TestMSTNormalizePreservesExplicitZeroInstancePriority guards the instance
// priority override signal the same way bridge and port priority already
// are: an instance explicitly configured with priority 0 (a legal multiple
// of 4096, and the value that makes this bridge the root for the instance)
// must survive normalization as 0, not fall back to the default.
func TestMSTNormalizePreservesExplicitZeroInstancePriority(t *testing.T) {
	t.Parallel()

	m := stp.MST{
		Instances: map[stp.MSTID]stp.Instance{
			1: {Priority: 0, PriorityPresent: true},
		},
	}

	norm := m.Normalize()
	if got := norm.Instances[1].Priority; got != 0 {
		t.Errorf("Priority = %d, want explicit zero", got)
	}
	if !norm.Instances[1].PriorityPresent {
		t.Error("PriorityPresent = false, want true after normalization")
	}
}

// TestMSTConfigIDDeterministicAcrossCalls guards ConfigID against map
// iteration order: two calls on the same configuration, with several
// instances, must always produce the same digest.
func TestMSTConfigIDDeterministicAcrossCalls(t *testing.T) {
	t.Parallel()

	m := stp.MST{
		Instances: map[stp.MSTID]stp.Instance{
			1: {VLANs: []vlan.ID{10, 11, 12}},
			2: {VLANs: []vlan.ID{20, 21}},
			3: {VLANs: []vlan.ID{30}},
			4: {VLANs: []vlan.ID{40, 41, 42, 43}},
		},
	}

	want := m.ConfigID().Digest
	for i := 0; i < 20; i++ {
		if got := m.ConfigID().Digest; got != want {
			t.Fatalf("ConfigID().Digest on call %d = %x, want %x", i, got, want)
		}
	}
}

// TestInstanceCanonicalIncludesPorts guards the canonical fact against
// dropping per-port settings: two instances differing only in a port's path
// cost must canonicalize differently, or the diff-then-derive step would
// read them as the same fact.
func TestInstanceCanonicalIncludesPorts(t *testing.T) {
	t.Parallel()

	a := stp.Instance{Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 100}}}
	b := stp.Instance{Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 200}}}

	if trace.EqualFact(a, b) {
		t.Errorf("instances differing only in port path cost canonicalize alike: %q", a.Canonical())
	}
}

// TestInstanceCanonicalTreatsRawAndEffectivePriorityAlike guards against a
// raw-config instance and a constructed one reading as different facts when
// they mean the same thing: an unset priority and the explicit default must
// canonicalize the same way.
func TestInstanceCanonicalTreatsRawAndEffectivePriorityAlike(t *testing.T) {
	t.Parallel()

	raw := stp.Instance{}
	effective := stp.Instance{Priority: stp.DefaultBridgePriority}

	if !trace.EqualFact(raw, effective) {
		t.Errorf("raw fact = %q, effective fact = %q, want alike", raw.Canonical(), effective.Canonical())
	}
}

// TestMSTCanonicalIncludesInstances guards the region-level canonical fact:
// two regions differing only in one instance's ports must canonicalize
// differently.
func TestMSTCanonicalIncludesInstances(t *testing.T) {
	t.Parallel()

	a := stp.MST{
		Instances: map[stp.MSTID]stp.Instance{
			1: {Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 100}}},
		},
	}
	b := stp.MST{
		Instances: map[stp.MSTID]stp.Instance{
			1: {Ports: map[string]stp.InstancePort{"1/1/1": {PathCost: 200}}},
		},
	}

	if trace.EqualFact(a, b) {
		t.Errorf("regions differing only in an instance port canonicalize alike: %q", a.Canonical())
	}
}
