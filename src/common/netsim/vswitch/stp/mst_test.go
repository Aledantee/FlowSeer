package stp_test

import (
	"encoding/hex"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
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
		Build()
	if err != nil {
		t.Fatalf("port.Builder.Build: %v", err)
	}

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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.mst.Validate(tbl)
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
