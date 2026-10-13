package routing_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestRouteCanonicalAbsentNextHop(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		hop  netip.Addr
		want string
	}{
		{"absent", netip.Addr{}, `prefix="192.0.2.0/24",preference=1,metric=0,next_hop="absent",interface="out"`},
		{"IPv4", netip.MustParseAddr("192.0.2.1"), `prefix="192.0.2.0/24",preference=1,metric=0,next_hop="192.0.2.1",interface="out"`},
		{"IPv6", netip.MustParseAddr("2001:db8::1"), `prefix="192.0.2.0/24",preference=1,metric=0,next_hop="2001:db8::1",interface="out"`},
		{"unspecified IPv4", netip.MustParseAddr("0.0.0.0"), `prefix="192.0.2.0/24",preference=1,metric=0,next_hop="0.0.0.0",interface="out"`},
		{"unspecified IPv6", netip.MustParseAddr("::"), `prefix="192.0.2.0/24",preference=1,metric=0,next_hop="::",interface="out"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			route := routing.Route{Prefix: netip.MustParsePrefix("192.0.2.0/24"), NextHop: test.hop, Interface: "out", Preference: 1}
			if got := route.Canonical(); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestRouteDiffAbsentNextHop(t *testing.T) {
	t.Parallel()

	env := layer.Env{}
	base := routing.Config{VRFs: map[string]routing.VRF{"default": {
		Interfaces: map[string]routing.Interface{"out": {VLAN: 10}},
	}}}.Normalize(env)
	configured := base.Clone()
	vrf := configured.VRFs["default"]
	vrf.Routes = []routing.Route{{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Interface: "out"}}
	configured.VRFs["default"] = vrf
	configured = configured.Normalize(env)
	if err := configured.Validate(env); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	wantRoute := `prefix="192.0.2.0/24",preference=1,metric=0,next_hop="absent",interface="out"`
	wantSubject := trace.Subject{Kind: "route", Key: `"default"/"192.0.2.0/24"/"absent"/"out"`}
	for _, test := range []struct {
		name string
		from routing.Config
		to   routing.Config
	}{
		{"add", base, configured},
		{"remove", configured, base},
	} {
		t.Run(test.name, func(t *testing.T) {
			changes := routing.Diff(test.from, test.to)
			if len(changes) != 1 {
				t.Fatalf("got %v, want one route change", changes)
			}
			change := changes[0]
			if change.Layer != "routing" || change.Subject != wantSubject || change.Field != "" {
				t.Errorf("got %v, want route lifecycle at %v", change, wantSubject)
			}
			fact := change.To
			if test.name == "remove" {
				fact = change.From
				if change.To != nil {
					t.Errorf("removal To got %v, want nil", change.To)
				}
			} else if change.From != nil {
				t.Errorf("addition From got %v, want nil", change.From)
			}
			if fact == nil || fact.TypeID() != "routing.route" || fact.Canonical() != wantRoute {
				t.Errorf("got %v, want routing.route %s", fact, wantRoute)
			}
		})
	}

	changes := routing.Diff(routing.Config{}, configured)
	wantSnapshot := `interfaces={"out":vlan=10;port="";mac="00:00:00:00:00:00";prefixes=[]};routes=[{prefix="192.0.2.0/24";preference=1;metric=0;next_hop="absent";interface="out"}];neighbors=[];neighbor_policy={mode="";reachable_time=30s;retrans_timer=1s;hold_depth=3}`
	if len(changes) != 1 || changes[0].To == nil {
		t.Fatalf("got %v, want one VRF addition", changes)
	}
	if got := changes[0].To.Canonical(); got != wantSnapshot {
		t.Errorf("VRF snapshot got %q, want %q", got, wantSnapshot)
	}
}

func TestRouteDiffAbsentNextHopIdentityAndOrder(t *testing.T) {
	t.Parallel()

	prefix := netip.MustParsePrefix("192.0.2.0/24")
	base := routing.Config{VRFs: map[string]routing.VRF{"default": {
		Interfaces: map[string]routing.Interface{"out": {VLAN: 10}, "z": {VLAN: 20}},
	}}}
	configured := routing.Config{VRFs: map[string]routing.VRF{"default": {
		Interfaces: base.VRFs["default"].Interfaces,
		Routes: []routing.Route{
			{Prefix: prefix, NextHop: netip.MustParseAddr("192.0.2.1"), Interface: "out", Preference: 1},
			{Prefix: prefix, Interface: "z", Preference: 1},
			{Prefix: prefix, Interface: "out", Preference: 1},
		},
	}}}
	if err := configured.Validate(layer.Env{}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	wantKeys := []string{
		`"default"/"192.0.2.0/24"/"absent"/"out"`,
		`"default"/"192.0.2.0/24"/"absent"/"z"`,
		`"default"/"192.0.2.0/24"/"192.0.2.1"/"out"`,
	}
	changes := routing.Diff(base, configured)
	if len(changes) != len(wantKeys) {
		t.Fatalf("got %v, want three distinct route additions", changes)
	}
	for i, change := range changes {
		if change.Subject.Key != wantKeys[i] || change.From != nil || change.To == nil || change.Field != "" {
			t.Errorf("change %d got %v, want route addition at %s", i, change, wantKeys[i])
		}
	}

	updated := configured.Clone()
	updated.VRFs["default"].Routes[2].Preference = 2
	updated.VRFs["default"].Routes[2].Metric = 7
	changes = routing.Diff(configured, updated)
	if len(changes) != 2 {
		t.Fatalf("got %v, want two field changes for the same route", changes)
	}
	for i, want := range []struct{ field, from, to string }{{"preference", "1", "2"}, {"metric", "0", "7"}} {
		change := changes[i]
		if change.Subject.Key != wantKeys[0] || change.Field != want.field || change.From == nil || change.To == nil {
			t.Fatalf("change %d got %v, want %s on the absent-hop route", i, change, want.field)
		}
		if change.From.Canonical() != want.from || change.To.Canonical() != want.to {
			t.Errorf("change %d got %v, want %s to %s", i, change, want.from, want.to)
		}
	}
}
