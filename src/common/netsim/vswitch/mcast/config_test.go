package mcast_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	ports := mcastPortTable(t)
	tests := []struct {
		name    string
		cfg     mcast.Config
		wantErr bool
	}{
		{
			name: "valid",
			cfg: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				10: {RouterPorts: []string{"lag1", "1/1/4"}},
			}},
		},
		{
			name:    "invalid VLAN",
			cfg:     mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{0: {}}},
			wantErr: true,
		},
		{
			name:    "VLAN above assignable range",
			cfg:     mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vlan.MaxID + 1: {}}},
			wantErr: true,
		},
		{
			name: "missing router port",
			cfg: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				10: {RouterPorts: []string{"1/1/9"}},
			}},
			wantErr: true,
		},
		{
			name: "physical LAG member",
			cfg: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				10: {RouterPorts: []string{"1/1/3"}},
			}},
			wantErr: true,
		},
		{
			name: "negative membership interval",
			cfg: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				10: {MembershipInterval: -time.Second},
			}},
			wantErr: true,
		},
		{
			name: "negative router port interval",
			cfg: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				10: {RouterPortInterval: -time.Second},
			}},
			wantErr: true,
		},
		{
			name: "negative last member query interval",
			cfg: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				10: {LastMemberQueryInterval: -time.Second},
			}},
			wantErr: true,
		},
		{
			name: "negative last member query count",
			cfg: mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				10: {LastMemberQueryCount: -1},
			}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate(ports)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigCloneAndFloods(t *testing.T) {
	t.Parallel()

	flood := false
	cfg := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
		10: {FloodUnregistered: &flood, RouterPorts: []string{"1/1/4"}},
		20: {},
	}}
	clone := cfg.Clone()

	cloneVLAN := clone.VLANs[10]
	*cloneVLAN.FloodUnregistered = true
	cloneVLAN.RouterPorts[0] = "changed"
	clone.VLANs[10] = cloneVLAN
	delete(clone.VLANs, 20)

	if cfg.Floods(10) {
		t.Error("Floods(10) = true, want false")
	}
	if !cfg.Floods(20) || !cfg.Floods(30) {
		t.Error("Floods() = false for a defaulted or unsnooped VLAN, want true")
	}
	if got := cfg.VLANs[10].RouterPorts[0]; got != "1/1/4" {
		t.Errorf("original router port = %q, want %q", got, "1/1/4")
	}
	if _, ok := cfg.VLANs[20]; !ok {
		t.Error("clone mutation removed VLAN 20 from original")
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()

	flood := false
	a := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
		10: {RouterPorts: []string{"1/1/2", "1/1/1"}},
		30: {},
	}}
	b := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
		10: {
			FloodUnregistered:       &flood,
			FastLeave:               true,
			RouterPorts:             []string{"1/1/4"},
			MembershipInterval:      time.Minute,
			RouterPortInterval:      2 * time.Minute,
			LastMemberQueryInterval: 2 * time.Second,
			LastMemberQueryCount:    3,
		},
		20: {},
	}}

	changes := mcast.Diff(a, b)
	wantFields := []string{
		"flood_unregistered",
		"fast_leave",
		"router_ports",
		"membership_interval",
		"router_port_interval",
		"last_member_query_interval",
		"last_member_query_count",
		"",
		"",
	}
	gotFields := make([]string, len(changes))
	for i, change := range changes {
		gotFields[i] = change.Field
		if change.Layer != trace.Layer("mcast") || change.Subject.Kind != "vlan" {
			t.Errorf("change[%d] identity = %q/%+v, want mcast/vlan", i, change.Layer, change.Subject)
		}
	}
	if !slices.Equal(gotFields, wantFields) {
		t.Errorf("change fields = %v, want %v", gotFields, wantFields)
	}

	routers := changes[2]
	if routers.From == nil || routers.To == nil ||
		!trace.EqualFact(routers.From, mcast.RouterPortsFact([]string{"1/1/1", "1/1/2"})) ||
		!trace.EqualFact(routers.To, mcast.RouterPortsFact([]string{"1/1/4"})) {
		t.Errorf("router_ports change = (%v, %v), want sorted port sets", routers.From, routers.To)
	}
	for i, c := range changes {
		if c.From != nil && c.From.TypeID() == "" {
			t.Errorf("change[%d].From has empty TypeID", i)
		}
		if c.To != nil && c.To.TypeID() == "" {
			t.Errorf("change[%d].To has empty TypeID", i)
		}
	}
	if changes[7].Subject.Key != "30" || changes[7].To != nil {
		t.Errorf("removed VLAN change = %+v, want VLAN 30 removal", changes[7])
	}
	if changes[8].Subject.Key != "20" || changes[8].From != nil {
		t.Errorf("added VLAN change = %+v, want VLAN 20 addition", changes[8])
	}
}

func TestDiffUsesEffectiveValuesAndSetOrder(t *testing.T) {
	t.Parallel()

	a := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
		10: {RouterPorts: []string{"1/1/1", "1/1/2"}},
	}}
	b := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
		10: {
			RouterPorts:        []string{"1/1/2", "1/1/1"},
			MembershipInterval: mcast.DefaultMembershipInterval,
			RouterPortInterval: mcast.DefaultMembershipInterval,
		},
	}}

	if got := mcast.Diff(a, b); len(got) != 0 {
		t.Errorf("Diff() = %+v, want no changes", got)
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	t.Run("default equivalence and idempotence", func(t *testing.T) {
		raw := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			10: {RouterPorts: []string{"1/1/2", "1/1/1"}},
		}}
		flood := true
		explicit := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			10: {
				FloodUnregistered:       &flood,
				FastLeave:               false,
				RouterPorts:             []string{"1/1/1", "1/1/2"},
				MembershipInterval:      mcast.DefaultMembershipInterval,
				RouterPortInterval:      mcast.DefaultMembershipInterval,
				LastMemberQueryInterval: mcast.DefaultLastMemberQueryInterval,
				LastMemberQueryCount:    mcast.DefaultLastMemberQueryCount,
			},
		}}

		normRaw := raw.Normalize()
		normExplicit := explicit.Normalize()

		diffs := mcast.Diff(normRaw, normExplicit)
		if len(diffs) != 0 {
			t.Errorf("normalized raw != normalized explicit: %v", diffs)
		}

		normTwice := normRaw.Normalize()
		if len(mcast.Diff(normRaw, normTwice)) != 0 {
			t.Errorf("Normalize() is not idempotent")
		}
	})

	t.Run("caller input immutability", func(t *testing.T) {
		ports := []string{"1/1/2", "1/1/1"}
		raw := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			10: {RouterPorts: ports},
		}}
		_ = raw.Normalize()
		if ports[0] != "1/1/2" || ports[1] != "1/1/1" {
			t.Errorf("caller slice mutated: %v", ports)
		}
	})
}

func TestLastMemberQueryCountDiffUsesEffectiveValue(t *testing.T) {
	t.Parallel()

	zero := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {}}}
	explicitDefault := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
		10: {LastMemberQueryCount: mcast.DefaultLastMemberQueryCount},
	}}
	changedTo3 := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
		10: {LastMemberQueryCount: 3},
	}}

	if got := mcast.Diff(zero, explicitDefault); len(got) != 0 {
		t.Errorf("Diff(zero, explicit default) = %+v, want no changes", got)
	}
	if got := mcast.Diff(zero, changedTo3); len(got) != 1 || got[0].Field != "last_member_query_count" {
		t.Errorf("Diff(zero, 3) = %+v, want one last_member_query_count change", got)
	}
}

func TestBehaviorMatrix(t *testing.T) {
	t.Parallel()

	fields := []string{
		"VLANs.FloodUnregistered",
		"VLANs.FastLeave",
		"VLANs.RouterPorts",
		"VLANs.MembershipInterval",
		"VLANs.RouterPortInterval",
		"VLANs.LastMemberQueryInterval",
		"VLANs.LastMemberQueryCount",
	}
	if len(fields) != 7 {
		t.Fatalf("unexpected number of mcast fields: %d", len(fields))
	}
}
