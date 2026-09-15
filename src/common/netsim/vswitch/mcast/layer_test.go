package mcast_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func mcastPortTable(t *testing.T) port.Table {
	t.Helper()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/8", Kind: port.Physical}).
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return ports
}

func mustNewMcast(t *testing.T, cfg mcast.Config, ports port.Table) *mcast.Layer {
	t.Helper()
	m, err := mcast.New(cfg, ports)
	if err != nil {
		t.Fatalf("mcast.New: %v", err)
	}
	return m
}

func mustEntry(t *testing.T, entries []mcast.Entry, portName string) mcast.Entry {
	t.Helper()
	for _, e := range entries {
		if e.Port == portName {
			return e
		}
	}
	t.Fatalf("no entry for port %q in %+v", portName, entries)
	return mcast.Entry{}
}

func entryExists(entries []mcast.Entry, portName string) bool {
	for _, e := range entries {
		if e.Port == portName {
			return true
		}
	}
	return false
}

func sourceExpiry(t *testing.T, e mcast.Entry, addr netip.Addr) (time.Time, bool) {
	t.Helper()
	for _, s := range e.Sources {
		if s.Address == addr {
			return s.Expires, true
		}
	}
	return time.Time{}, false
}

func igmpRecord(now time.Time, layer *mcast.Layer, vid vlan.ID, portName string, kind igmp.RecordType, group netip.Addr, sources []netip.Addr) {
	layer.Learn(now, vid, portName, netip.MustParseAddr("10.0.0.1"), igmp.Message{
		Type:    igmp.ReportV3,
		Records: []igmp.GroupRecord{{Type: kind, Group: group, Sources: sources}},
	})
}

func TestResolveReturnsMembersAndRouterPorts(t *testing.T) {
	t.Parallel()

	ports := mcastPortTable(t)

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	source := netip.MustParseAddr("10.1.1.1")
	layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, ports)
	now := time.Unix(1_000, 0)
	layer.Learn(now, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{
		Type:  igmp.ReportV2,
		Group: group,
	})
	layer.Learn(now, vid, "1/1/4", netip.MustParseAddr("10.0.0.254"), igmp.Message{Type: igmp.Query})

	got, registered, pending := layer.Resolve(vid, group, source, now)
	want := []string{"1/1/1", "1/1/4"}
	if !registered || pending || !slices.Equal(got, want) {
		t.Errorf("Resolve() = (%v, %v, %v), want (%v, true, false)", got, registered, pending, want)
	}

	entries := layer.Groups(vid)
	if len(entries) != 1 || entries[0].Group != group || entries[0].Port != "1/1/1" ||
		entries[0].Mode != mcast.Exclude || !entries[0].GroupExpires.Equal(now.Add(mcast.DefaultMembershipInterval)) {
		t.Errorf("Groups() = %+v, want EXCLUDE({},{}) with default group timer", entries)
	}
}

func TestReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "unregistered", got: string(mcast.ReasonUnregistered), want: "unregistered"},
		{name: "no router port", got: string(mcast.ReasonNoRouterPort), want: "no-router-port"},
		{name: "bad control", got: string(mcast.ReasonBadControl), want: "bad-control"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("reason = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestRouterPortLearningRequiresProtocolSource(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, mcastPortTable(t))
	now := time.Unix(2_000, 0)

	layer.Learn(now, vid, "1/1/1", netip.IPv4Unspecified(), igmp.Message{Type: igmp.Query})
	layer.LearnMLD(now, vid, "1/1/2", netip.MustParseAddr("2001:db8::1"), mld.Message{Type: mld.Query})
	if got := layer.RouterPorts(vid); len(got) != 0 {
		t.Fatalf("router ports after ineligible sources = %+v, want none", got)
	}

	layer.Learn(now, vid, "1/1/4", netip.MustParseAddr("10.0.0.254"), igmp.Message{Type: igmp.Query})
	layer.LearnMLD(now, vid, "1/1/2", netip.MustParseAddr("fe80::2"), mld.Message{Type: mld.Query})
	routers := layer.RouterPorts(vid)
	if len(routers) != 2 || routers[0].Port != "1/1/2" || routers[1].Port != "1/1/4" {
		t.Errorf("RouterPorts() = %+v, want 1/1/2 and 1/1/4", routers)
	}
	for _, router := range routers {
		if router.Static || !router.Expires.Equal(now.Add(mcast.DefaultMembershipInterval)) {
			t.Errorf("learned router port = %+v, want dynamic default expiry", router)
		}
	}

	resolved, registered, pending := layer.Resolve(vid, netip.MustParseAddr("239.9.9.9"), netip.MustParseAddr("10.5.5.5"), now)
	if registered || pending || !slices.Equal(resolved, []string{"1/1/2", "1/1/4"}) {
		t.Errorf("router-only Resolve() = (%v, %v, %v), want router ports and false", resolved, registered, pending)
	}
}

func TestStaticRouterPortsNeverExpire(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
		vid: {RouterPorts: []string{"1/1/4"}},
	}}, mcastPortTable(t))
	layer.Age(time.Unix(1_000_000, 0))

	routers := layer.RouterPorts(vid)
	if len(routers) != 1 || routers[0].Port != "1/1/4" || !routers[0].Static || !routers[0].Expires.IsZero() {
		t.Errorf("RouterPorts() = %+v, want one non-expiring static port", routers)
	}
}

func TestLearningIgnoresUnsnoopedVLANsAndPhysicalLAGMembers(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, mcastPortTable(t))
	now := time.Unix(3_000, 0)

	layer.Learn(now, 20, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
	layer.Learn(now, vid, "1/1/3", netip.MustParseAddr("10.0.0.3"), igmp.Message{Type: igmp.ReportV2, Group: group})
	layer.Learn(now, vid, "missing", netip.MustParseAddr("10.0.0.9"), igmp.Message{Type: igmp.ReportV2, Group: group})
	layer.Learn(now, vid, "1/1/3", netip.MustParseAddr("10.0.0.3"), igmp.Message{Type: igmp.Query})

	if layer.Snooped(20) || !layer.Snooped(vid) {
		t.Errorf("Snooped() reported wrong configured VLANs")
	}
	if got := layer.Groups(vid); len(got) != 0 {
		t.Errorf("Groups() = %+v, want none", got)
	}
	if got := layer.RouterPorts(vid); len(got) != 0 {
		t.Errorf("RouterPorts() = %+v, want none", got)
	}
}

func TestLegacyReportsLearnMembership(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	ports := mcastPortTable(t)
	layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, ports)
	now := time.Unix(4_000, 0)

	layer.Learn(now, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{
		Type: igmp.ReportV1, Group: netip.MustParseAddr("239.1.1.1"),
	})
	layer.Learn(now, vid, "1/1/2", netip.MustParseAddr("10.0.0.2"), igmp.Message{
		Type: igmp.ReportV2, Group: netip.MustParseAddr("239.1.1.2"),
	})
	layer.LearnMLD(now, vid, "1/1/4", netip.MustParseAddr("fe80::4"), mld.Message{
		Type: mld.ReportV1, Group: netip.MustParseAddr("ff05::1"),
	})

	groups := layer.Groups(vid)
	if len(groups) != 3 {
		t.Fatalf("Groups() = %+v, want three legacy memberships", groups)
	}
	if groups[0].Group.String() != "239.1.1.1" || groups[1].Group.String() != "239.1.1.2" || groups[2].Group.String() != "ff05::1" {
		t.Errorf("Groups() order = %+v, want address order", groups)
	}
	for _, g := range groups {
		if g.Mode != mcast.Exclude || len(g.Sources) != 0 || !g.GroupExpires.Equal(now.Add(mcast.DefaultMembershipInterval)) {
			t.Errorf("legacy join state = %+v, want EXCLUDE({},{}) with default group timer", g)
		}
	}
}

func TestLeaveAndDoneRespectFastLeave(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fast bool
		mld  bool
	}{
		{name: "IGMP leave waits", fast: false},
		{name: "IGMP fast leave removes", fast: true},
		{name: "MLD done waits", fast: false, mld: true},
		{name: "MLD fast done removes", fast: true, mld: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const vid vlan.ID = 10
			group := netip.MustParseAddr("239.1.1.1")
			if tt.mld {
				group = netip.MustParseAddr("ff05::1")
			}
			layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				vid: {FastLeave: tt.fast},
			}}, mcastPortTable(t))
			now := time.Unix(7_000, 0)
			if tt.mld {
				layer.LearnMLD(now, vid, "1/1/1", netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.ReportV1, Group: group})
				layer.LearnMLD(now.Add(time.Second), vid, "1/1/1", netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.Done, Group: group})
			} else {
				layer.Learn(now, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
				layer.Learn(now.Add(time.Second), vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.Leave, Group: group})
			}

			got := len(layer.Groups(vid))
			want := 1
			if tt.fast {
				want = 0
			}
			if got != want {
				t.Errorf("membership count = %d, want %d", got, want)
			}
		})
	}
}

func TestRetainDropsDepartedPortsAndPreservesExpiries(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	t0 := time.Unix(10_000, 0)
	ports := mcastPortTable(t)
	layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, ports)
	for _, name := range []string{"1/1/1", "1/1/2"} {
		layer.Learn(t0, vid, name, netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
	}
	layer.Learn(t0, vid, "1/1/4", netip.MustParseAddr("10.0.0.254"), igmp.Message{Type: igmp.Query})

	wantExpiry := t0.Add(mcast.DefaultMembershipInterval)
	layer.Retain(ports, func(_ vlan.ID, name string) bool { return name != "1/1/2" })
	groups := layer.Groups(vid)
	if len(groups) != 1 || groups[0].Port != "1/1/1" || !groups[0].GroupExpires.Equal(wantExpiry) {
		t.Errorf("Groups() after Retain = %+v, want retained 1/1/1 with original group timer", groups)
	}
	if routers := layer.RouterPorts(vid); len(routers) != 1 || routers[0].Port != "1/1/4" {
		t.Errorf("RouterPorts() after Retain = %+v, want retained router", routers)
	}
}

func TestLayerCloneIsIndependent(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	other := netip.MustParseAddr("10.9.9.9")
	t0 := time.Unix(11_000, 0)
	ports := mcastPortTable(t)
	original := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, ports)
	original.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
	original.Learn(t0, vid, "1/1/4", netip.MustParseAddr("10.0.0.254"), igmp.Message{Type: igmp.Query})

	clone := original.Clone()
	clone.Retain(ports, func(_ vlan.ID, name string) bool { return name != "1/1/1" })
	original.Learn(t0, vid, "1/1/2", netip.MustParseAddr("10.0.0.2"), igmp.Message{Type: igmp.ReportV2, Group: group})

	if got, registered, _ := original.Resolve(vid, group, other, t0); !registered || !slices.Equal(got, []string{"1/1/1", "1/1/2", "1/1/4"}) {
		t.Errorf("original Resolve() = (%v, %v), want independent original state", got, registered)
	}
	if got, registered, _ := clone.Resolve(vid, group, other, t0); registered || !slices.Equal(got, []string{"1/1/4"}) {
		t.Errorf("clone Resolve() = (%v, %v), want independent clone state", got, registered)
	}
}

func TestLayerCloneDeepCopiesSourceRecords(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	s1 := netip.MustParseAddr("10.1.1.1")
	t0 := time.Unix(11_500, 0)
	layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, mcastPortTable(t))
	igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})

	clone := layer.Clone()
	clone.Learn(t0.Add(time.Second), vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{
		Type: igmp.ReportV3,
		Records: []igmp.GroupRecord{{
			Type: igmp.AllowNewSources, Group: group, Sources: []netip.Addr{netip.MustParseAddr("10.1.1.2")},
		}},
	})

	if got := mustEntry(t, layer.Groups(vid), "1/1/1"); len(got.Sources) != 1 {
		t.Errorf("original Sources = %+v, want unaffected by clone mutation", got.Sources)
	}
	if got := mustEntry(t, clone.Groups(vid), "1/1/1"); len(got.Sources) != 2 {
		t.Errorf("clone Sources = %+v, want the new source added", got.Sources)
	}
}

// --- §6.4.1: current-state records ---

func TestCurrentStateRecordRules(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	s1 := netip.MustParseAddr("10.1.1.1")
	s2 := netip.MustParseAddr("10.1.1.2")
	s3 := netip.MustParseAddr("10.1.1.3")
	gmi := 100 * time.Second

	newLayer := func(t *testing.T) *mcast.Layer {
		t.Helper()
		return mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			vid: {MembershipInterval: gmi},
		}}, mcastPortTable(t))
	}

	t.Run("INCLUDE(A) + IS_IN(B) -> INCLUDE(A+B), (B)=GMI", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(20_000, 0)
		t1 := t0.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s2})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Include {
			t.Fatalf("mode = %v, want INCLUDE", e.Mode)
		}
		if exp, ok := sourceExpiry(t, e, s1); !ok || !exp.Equal(t0.Add(gmi)) {
			t.Errorf("s1 expiry = (%v, %v), want unchanged at t0+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.Equal(t1.Add(gmi)) {
			t.Errorf("s2 expiry = (%v, %v), want t1+GMI", exp, ok)
		}
	})

	t.Run("INCLUDE(A) + IS_EX(B) -> EXCLUDE(A*B, B-A)", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(21_000, 0)
		t1 := t0.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1, s2})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s2, s3})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Exclude || !e.GroupExpires.Equal(t1.Add(gmi)) {
			t.Fatalf("state = %+v, want EXCLUDE with group timer t1+GMI", e)
		}
		if _, ok := sourceExpiry(t, e, s1); ok {
			t.Errorf("s1 (A-B) present, want deleted")
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.Equal(t0.Add(gmi)) {
			t.Errorf("s2 (A*B) expiry = (%v, %v), want retained at t0+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s3); !ok || !exp.IsZero() {
			t.Errorf("s3 (B-A) expiry = (%v, %v), want zero", exp, ok)
		}
	})

	t.Run("EXCLUDE(X,Y) + IS_IN(A) -> EXCLUDE(X+A, Y-A)", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(22_000, 0)
		t1 := t0.Add(time.Second)
		t2 := t1.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1}) // X seed
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s1, s2})
		// state: EXCLUDE(X={s1: t0+GMI}, Y={s2: 0}), group timer t1+GMI
		igmpRecord(t2, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s2, s3})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Exclude || !e.GroupExpires.Equal(t1.Add(gmi)) {
			t.Fatalf("state = %+v, want EXCLUDE with group timer unchanged at t1+GMI", e)
		}
		if exp, ok := sourceExpiry(t, e, s1); !ok || !exp.Equal(t0.Add(gmi)) {
			t.Errorf("s1 (untouched X) expiry = (%v, %v), want t0+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.Equal(t2.Add(gmi)) {
			t.Errorf("s2 (moved Y->X) expiry = (%v, %v), want t2+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s3); !ok || !exp.Equal(t2.Add(gmi)) {
			t.Errorf("s3 (new) expiry = (%v, %v), want t2+GMI", exp, ok)
		}
	})

	t.Run("EXCLUDE(X,Y) + IS_EX(A) -> EXCLUDE(A-Y, Y*A)", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(23_000, 0)
		t1 := t0.Add(time.Second)
		t2 := t1.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s1, s2})
		// state: EXCLUDE(X={s1: t0+GMI}, Y={s2: 0}), group timer t1+GMI
		igmpRecord(t2, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s2, s3})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Exclude || !e.GroupExpires.Equal(t2.Add(gmi)) {
			t.Fatalf("state = %+v, want EXCLUDE with group timer refreshed to t2+GMI", e)
		}
		if _, ok := sourceExpiry(t, e, s1); ok {
			t.Errorf("s1 (X-A) present, want deleted")
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.IsZero() {
			t.Errorf("s2 (Y*A) expiry = (%v, %v), want zero (unchanged)", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s3); !ok || !exp.Equal(t2.Add(gmi)) {
			t.Errorf("s3 (A-X-Y) expiry = (%v, %v), want t2+GMI", exp, ok)
		}
	})
}

// --- §6.4.2: filter-mode-change and source-list-change records ---

func TestFilterModeAndSourceListChangeRecordRules(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	s1 := netip.MustParseAddr("10.1.1.1")
	s2 := netip.MustParseAddr("10.1.1.2")
	s3 := netip.MustParseAddr("10.1.1.3")
	gmi := 100 * time.Second
	lmqt := 2 * time.Second

	newLayer := func(t *testing.T) *mcast.Layer {
		t.Helper()
		return mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			vid: {
				MembershipInterval:      gmi,
				RouterPorts:             []string{"1/1/8"},
				LastMemberQueryInterval: time.Second,
				LastMemberQueryCount:    2,
			},
		}}, mcastPortTable(t))
	}

	t.Run("INCLUDE(A) + ALLOW(B) -> INCLUDE(A+B), (B)=GMI", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(30_000, 0)
		t1 := t0.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.AllowNewSources, group, []netip.Addr{s2})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Include {
			t.Fatalf("mode = %v, want INCLUDE", e.Mode)
		}
		if exp, ok := sourceExpiry(t, e, s1); !ok || !exp.Equal(t0.Add(gmi)) {
			t.Errorf("s1 expiry = (%v, %v), want unchanged t0+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.Equal(t1.Add(gmi)) {
			t.Errorf("s2 expiry = (%v, %v), want t1+GMI", exp, ok)
		}
		if _, _, pending := layer.Resolve(vid, group, s1, t1.Add(lmqt)); pending {
			t.Errorf("pending = true, want ALLOW to never query")
		}
	})

	t.Run("INCLUDE(A) + BLOCK(B) -> INCLUDE(A), Send Q(G, A*B)", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(31_000, 0)
		t1 := t0.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1, s2})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.BlockOldSources, group, []netip.Addr{s2, s3})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Include {
			t.Fatalf("mode = %v, want INCLUDE unchanged", e.Mode)
		}
		if exp, ok := sourceExpiry(t, e, s1); !ok || !exp.Equal(t0.Add(gmi)) {
			t.Errorf("s1 expiry = (%v, %v), want unchanged", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.Equal(t0.Add(gmi)) {
			t.Errorf("s2 expiry = (%v, %v), want unchanged", exp, ok)
		}
		if _, _, pending := layer.Resolve(vid, group, s1, t1.Add(lmqt)); !pending {
			t.Errorf("pending = false at t1+LMQT, want true from Send Q(G, A*B)")
		}
	})

	t.Run("INCLUDE(A) + TO_EX(B) -> EXCLUDE(A*B, B-A), Send Q(G, A*B)", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(32_000, 0)
		t1 := t0.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1, s2})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ChangeToExcludeMode, group, []netip.Addr{s2, s3})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Exclude || !e.GroupExpires.Equal(t1.Add(gmi)) {
			t.Fatalf("state = %+v, want EXCLUDE with group timer t1+GMI", e)
		}
		if _, ok := sourceExpiry(t, e, s1); ok {
			t.Errorf("s1 (A-B) present, want deleted")
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.Equal(t0.Add(gmi)) {
			t.Errorf("s2 (A*B) expiry = (%v, %v), want retained t0+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s3); !ok || !exp.IsZero() {
			t.Errorf("s3 (B-A) expiry = (%v, %v), want zero", exp, ok)
		}
		if _, _, pending := layer.Resolve(vid, group, s1, t1.Add(lmqt)); !pending {
			t.Errorf("pending = false at t1+LMQT, want true from Send Q(G, A*B)")
		}
	})

	t.Run("INCLUDE(A) + TO_IN(B) -> INCLUDE(A+B), Send Q(G, A-B)", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(33_000, 0)
		t1 := t0.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1, s2})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ChangeToIncludeMode, group, []netip.Addr{s2, s3})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Include {
			t.Fatalf("mode = %v, want INCLUDE", e.Mode)
		}
		if exp, ok := sourceExpiry(t, e, s1); !ok || !exp.Equal(t0.Add(gmi)) {
			t.Errorf("s1 (unchanged) expiry = (%v, %v), want t0+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.Equal(t1.Add(gmi)) {
			t.Errorf("s2 (refreshed) expiry = (%v, %v), want t1+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s3); !ok || !exp.Equal(t1.Add(gmi)) {
			t.Errorf("s3 (new) expiry = (%v, %v), want t1+GMI", exp, ok)
		}
		if _, _, pending := layer.Resolve(vid, group, s1, t1.Add(lmqt)); !pending {
			t.Errorf("pending = false at t1+LMQT, want true from Send Q(G, A-B)")
		}
	})

	t.Run("EXCLUDE(X,Y) + ALLOW(A) -> EXCLUDE(X+A, Y-A)", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(34_000, 0)
		t1 := t0.Add(time.Second)
		t2 := t1.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s1, s2})
		igmpRecord(t2, layer, vid, "1/1/1", igmp.AllowNewSources, group, []netip.Addr{s2, s3})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Exclude || !e.GroupExpires.Equal(t1.Add(gmi)) {
			t.Fatalf("state = %+v, want EXCLUDE, group timer unchanged at t1+GMI", e)
		}
		if exp, ok := sourceExpiry(t, e, s1); !ok || !exp.Equal(t0.Add(gmi)) {
			t.Errorf("s1 expiry = (%v, %v), want unchanged t0+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.Equal(t2.Add(gmi)) {
			t.Errorf("s2 (Y->X) expiry = (%v, %v), want t2+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s3); !ok || !exp.Equal(t2.Add(gmi)) {
			t.Errorf("s3 (new) expiry = (%v, %v), want t2+GMI", exp, ok)
		}
	})

	t.Run("EXCLUDE(X,Y) + BLOCK(A) -> EXCLUDE(X+(A-Y), Y)", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(35_000, 0)
		t1 := t0.Add(time.Second)
		t2 := t1.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s1, s2})
		// state: EXCLUDE(X={s1}, Y={s2}), group timer t1+GMI
		igmpRecord(t2, layer, vid, "1/1/1", igmp.BlockOldSources, group, []netip.Addr{s2, s3})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Exclude || !e.GroupExpires.Equal(t1.Add(gmi)) {
			t.Fatalf("state = %+v, want EXCLUDE, group timer untouched at t1+GMI", e)
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.IsZero() {
			t.Errorf("s2 (Y, untouched) expiry = (%v, %v), want zero", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s3); !ok || !exp.Equal(t1.Add(gmi)) {
			t.Errorf("s3 (A-X-Y, new) expiry = (%v, %v), want current group timer t1+GMI", exp, ok)
		}
		if _, _, pending := layer.Resolve(vid, group, s1, t2.Add(lmqt)); !pending {
			t.Errorf("pending = false at t2+LMQT, want true from Send Q(G, A-Y)")
		}
	})

	t.Run("EXCLUDE(X,Y) + TO_EX(A) -> EXCLUDE(A-Y, Y*A)", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(36_000, 0)
		t1 := t0.Add(time.Second)
		t2 := t1.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s1, s2})
		// state: EXCLUDE(X={s1}, Y={s2}), group timer t1+GMI
		igmpRecord(t2, layer, vid, "1/1/1", igmp.ChangeToExcludeMode, group, []netip.Addr{s2, s3})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Exclude || !e.GroupExpires.Equal(t2.Add(gmi)) {
			t.Fatalf("state = %+v, want EXCLUDE, group timer refreshed to t2+GMI", e)
		}
		if _, ok := sourceExpiry(t, e, s1); ok {
			t.Errorf("s1 (X-A) present, want deleted")
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.IsZero() {
			t.Errorf("s2 (Y*A) expiry = (%v, %v), want zero (unchanged)", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s3); !ok || !exp.Equal(t1.Add(gmi)) {
			t.Errorf("s3 (A-X-Y, new) expiry = (%v, %v), want pre-update group timer t1+GMI", exp, ok)
		}
		if _, _, pending := layer.Resolve(vid, group, s1, t2.Add(lmqt)); !pending {
			t.Errorf("pending = false at t2+LMQT, want true from Send Q(G, A-Y)")
		}
	})

	t.Run("EXCLUDE(X,Y) + TO_IN(A) -> EXCLUDE(X+A, Y-A)", func(t *testing.T) {
		t.Parallel()
		layer := newLayer(t)
		t0 := time.Unix(37_000, 0)
		t1 := t0.Add(time.Second)
		t2 := t1.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s1, s2})
		// state: EXCLUDE(X={s1}, Y={s2}), group timer t1+GMI
		igmpRecord(t2, layer, vid, "1/1/1", igmp.ChangeToIncludeMode, group, []netip.Addr{s2, s3})

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Exclude || !e.GroupExpires.Equal(t1.Add(gmi)) {
			t.Fatalf("state = %+v, want EXCLUDE, group timer untouched at t1+GMI", e)
		}
		if exp, ok := sourceExpiry(t, e, s1); !ok || !exp.Equal(t0.Add(gmi)) {
			t.Errorf("s1 (X-A, untouched) expiry = (%v, %v), want t0+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s2); !ok || !exp.Equal(t2.Add(gmi)) {
			t.Errorf("s2 (Y->X) expiry = (%v, %v), want t2+GMI", exp, ok)
		}
		if exp, ok := sourceExpiry(t, e, s3); !ok || !exp.Equal(t2.Add(gmi)) {
			t.Errorf("s3 (new) expiry = (%v, %v), want t2+GMI", exp, ok)
		}
		if _, _, pending := layer.Resolve(vid, group, s1, t2.Add(lmqt)); !pending {
			t.Errorf("pending at t2+LMQT = false, want true from Send Q(G, X-A) or Send Q(G)")
		}
	})
}

// --- §6.5: timer expiry, applied by Age ---

func TestGroupTimerExpiryMovesExcludeToInclude(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	s1 := netip.MustParseAddr("10.1.1.1")
	s2 := netip.MustParseAddr("10.1.1.2")
	gmi := 10 * time.Second

	t.Run("running sources kept, zero-timer sources dropped", func(t *testing.T) {
		t.Parallel()
		layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			vid: {MembershipInterval: gmi},
		}}, mcastPortTable(t))
		t0 := time.Unix(40_000, 0)
		t1 := t0.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s2})
		// EXCLUDE(X={}, Y={s2: 0}), group timer t0+gmi
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
		// EXCLUDE(X={s1: t1+gmi}, Y={s2: 0}), group timer still t0+gmi (t1+gmi outlives it)

		layer.Age(t0.Add(gmi))

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Include {
			t.Fatalf("mode = %v, want INCLUDE after group timer expiry", e.Mode)
		}
		if _, ok := sourceExpiry(t, e, s1); !ok {
			t.Errorf("s1 (running) deleted, want kept")
		}
		if _, ok := sourceExpiry(t, e, s2); ok {
			t.Errorf("s2 (zero timer) kept, want deleted")
		}
	})

	t.Run("nothing left deletes the port's group state", func(t *testing.T) {
		t.Parallel()
		layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			vid: {MembershipInterval: gmi},
		}}, mcastPortTable(t))
		t0 := time.Unix(41_000, 0)
		layer.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
		// EXCLUDE({},{}), group timer t0+gmi

		layer.Age(t0.Add(gmi))

		if entries := layer.Groups(vid); entryExists(entries, "1/1/1") {
			t.Errorf("Groups() = %+v, want 1/1/1 removed", entries)
		}
	})
}

func TestSourceTimerExpiry(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	s1 := netip.MustParseAddr("10.1.1.1")
	gmi := 10 * time.Second

	t.Run("INCLUDE deletes the record and removes the port when it was the last one", func(t *testing.T) {
		t.Parallel()
		layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			vid: {MembershipInterval: gmi},
		}}, mcastPortTable(t))
		t0 := time.Unix(42_000, 0)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})

		layer.Age(t0.Add(gmi))

		if entries := layer.Groups(vid); entryExists(entries, "1/1/1") {
			t.Errorf("Groups() = %+v, want 1/1/1 removed", entries)
		}
	})

	t.Run("EXCLUDE moves the source from running to zero without removing the port", func(t *testing.T) {
		t.Parallel()
		s2 := netip.MustParseAddr("10.1.1.2")
		layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			vid: {MembershipInterval: 1000 * time.Second},
		}}, mcastPortTable(t))
		t0 := time.Unix(43_000, 0)
		t1 := t0.Add(time.Second)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
		igmpRecord(t1, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s1, s2})
		// EXCLUDE(X={s1: t0+1000s}, Y={s2: 0}), group timer far in the future

		layer.Age(t0.Add(1000 * time.Second))

		e := mustEntry(t, layer.Groups(vid), "1/1/1")
		if e.Mode != mcast.Exclude {
			t.Fatalf("mode = %v, want EXCLUDE unchanged", e.Mode)
		}
		exp, ok := sourceExpiry(t, e, s1)
		if !ok || !exp.IsZero() {
			t.Errorf("s1 expiry = (%v, %v), want zero after its timer expired", exp, ok)
		}
	})
}

// --- §6.3: forwarding, applied by Resolve ---

func TestForwardingTable(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	s1 := netip.MustParseAddr("10.1.1.1")
	s2 := netip.MustParseAddr("10.1.1.2")
	gmi := 100 * time.Second

	tests := []struct {
		name    string
		setup   func(layer *mcast.Layer, t0 time.Time)
		query   netip.Addr
		forward bool
	}{
		{
			name: "INCLUDE, source timer running, forward",
			setup: func(layer *mcast.Layer, t0 time.Time) {
				igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
			},
			query:   s1,
			forward: true,
		},
		{
			name: "INCLUDE, no record, do not forward",
			setup: func(layer *mcast.Layer, t0 time.Time) {
				igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
			},
			query:   s2,
			forward: false,
		},
		{
			name: "EXCLUDE, source timer running, forward",
			setup: func(layer *mcast.Layer, t0 time.Time) {
				igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
				igmpRecord(t0.Add(time.Second), layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s1})
			},
			query:   s1,
			forward: true,
		},
		{
			name: "EXCLUDE, source timer zero, do not forward",
			setup: func(layer *mcast.Layer, t0 time.Time) {
				igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s2})
			},
			query:   s2,
			forward: false,
		},
		{
			name: "EXCLUDE, no record, forward",
			setup: func(layer *mcast.Layer, t0 time.Time) {
				igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsExclude, group, nil)
			},
			query:   s1,
			forward: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				vid: {MembershipInterval: gmi},
			}}, mcastPortTable(t))
			t0 := time.Unix(50_000, 0)
			tt.setup(layer, t0)

			ports, _, _ := layer.Resolve(vid, group, tt.query, t0.Add(2*time.Second))
			got := slices.Contains(ports, "1/1/1")
			if got != tt.forward {
				t.Errorf("admitted = %v, want %v", got, tt.forward)
			}
		})
	}
}

// --- version compatibility ---

func TestLegacyReportAndLeaveMapToCompatibilityRecords(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	gmi := 100 * time.Second
	lmqt := 2 * time.Second

	tests := []struct {
		name string
		mld  bool
	}{
		{name: "IGMPv2"},
		{name: "MLDv1", mld: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				vid: {MembershipInterval: gmi, RouterPorts: []string{"1/1/8"}, LastMemberQueryInterval: time.Second, LastMemberQueryCount: 2},
			}}, mcastPortTable(t))
			t0 := time.Unix(60_000, 0)
			t1 := t0.Add(time.Second)

			g := group
			if tt.mld {
				g = netip.MustParseAddr("ff05::1")
				layer.LearnMLD(t0, vid, "1/1/1", netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.ReportV1, Group: g})
			} else {
				layer.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: g})
			}

			e := mustEntry(t, layer.Groups(vid), "1/1/1")
			if e.Mode != mcast.Exclude || len(e.Sources) != 0 || !e.GroupExpires.Equal(t0.Add(gmi)) {
				t.Fatalf("join state = %+v, want EXCLUDE({},{}) per IS_EX({})", e)
			}

			if tt.mld {
				layer.LearnMLD(t1, vid, "1/1/1", netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.Done, Group: g})
			} else {
				layer.Learn(t1, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.Leave, Group: g})
			}

			e = mustEntry(t, layer.Groups(vid), "1/1/1")
			if e.Mode != mcast.Exclude || len(e.Sources) != 0 {
				t.Fatalf("leave state = %+v, want EXCLUDE({},{}) per TO_IN({})", e)
			}
			if _, _, pending := layer.Resolve(vid, g, netip.MustParseAddr("10.5.5.5"), t1.Add(lmqt)); !pending {
				t.Errorf("pending at t1+LMQT = false, want true from the leave's Send Q(G)")
			}
		})
	}
}

func TestOlderVersionHostBlockIgnoredUntilTimerExpires(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	s1 := netip.MustParseAddr("10.1.1.1")
	gmi := 10 * time.Second
	layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
		vid: {MembershipInterval: gmi},
	}}, mcastPortTable(t))
	t0 := time.Unix(70_000, 0)

	// IGMPv2 join starts the older-version-host timer until t0+gmi.
	layer.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})

	igmpRecord(t0, layer, vid, "1/1/1", igmp.BlockOldSources, group, []netip.Addr{s1})
	if e := mustEntry(t, layer.Groups(vid), "1/1/1"); len(e.Sources) != 0 {
		t.Errorf("sources after BLOCK during older-host timer = %+v, want ignored", e.Sources)
	}

	after := t0.Add(gmi).Add(time.Second)
	igmpRecord(after, layer, vid, "1/1/1", igmp.BlockOldSources, group, []netip.Addr{s1})
	e := mustEntry(t, layer.Groups(vid), "1/1/1")
	if exp, ok := sourceExpiry(t, e, s1); !ok || !exp.Equal(t0.Add(gmi)) {
		t.Errorf("s1 expiry after the timer expired = (%v, %v), want the current group timer t0+GMI", exp, ok)
	}
}

// --- source filtering ---

func TestSourceFiltering(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	s1 := netip.MustParseAddr("10.1.1.1")
	s2 := netip.MustParseAddr("10.1.1.2")
	layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, mcastPortTable(t))
	t0 := time.Unix(80_000, 0)

	igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})

	if ports, _, _ := layer.Resolve(vid, group, s1, t0); !slices.Contains(ports, "1/1/1") {
		t.Errorf("Resolve(s1) does not admit 1/1/1, want admitted")
	}
	if ports, _, _ := layer.Resolve(vid, group, s2, t0); slices.Contains(ports, "1/1/1") {
		t.Errorf("Resolve(s2) admits 1/1/1, want not admitted")
	}

	igmpRecord(t0.Add(time.Second), layer, vid, "1/1/1", igmp.ModeIsExclude, group, []netip.Addr{s2})

	if ports, _, _ := layer.Resolve(vid, group, s1, t0.Add(2*time.Second)); !slices.Contains(ports, "1/1/1") {
		t.Errorf("Resolve(s1) after IS_EX({s2}) does not admit 1/1/1, want admitted (no record in EXCLUDE forwards)")
	}
	if ports, _, _ := layer.Resolve(vid, group, s2, t0.Add(2*time.Second)); slices.Contains(ports, "1/1/1") {
		t.Errorf("Resolve(s2) after IS_EX({s2}) admits 1/1/1, want not admitted (zero timer)")
	}
}

// --- leave timing ---

func TestLeaveTimingFollowsObservedQueries(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")

	build := func(t *testing.T, withRouter bool) *mcast.Layer {
		t.Helper()
		snooping := mcast.VLANSnooping{LastMemberQueryInterval: time.Second, LastMemberQueryCount: 2}
		if withRouter {
			snooping.RouterPorts = []string{"1/1/8"}
		}
		return mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: snooping}}, mcastPortTable(t))
	}

	t.Run("observed query shortens the leave to LMQT", func(t *testing.T) {
		t.Parallel()
		layer := build(t, true)
		t0 := time.Unix(90_000, 0)
		leaveAt := t0.Add(10 * time.Second)

		layer.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
		layer.Learn(leaveAt, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.Leave, Group: group})
		layer.Learn(leaveAt, vid, "1/1/8", netip.MustParseAddr("10.0.0.254"), igmp.Message{Type: igmp.Query, Group: group})

		other := netip.MustParseAddr("10.9.9.9")
		if ports, _, _ := layer.Resolve(vid, group, other, leaveAt.Add(time.Second)); !slices.Contains(ports, "1/1/1") {
			t.Errorf("admitted at t+1s = false, want true")
		}
		if ports, _, _ := layer.Resolve(vid, group, other, leaveAt.Add(2*time.Second)); slices.Contains(ports, "1/1/1") {
			t.Errorf("admitted at t+2s = true, want false")
		}
	})

	t.Run("without a query the leave runs the full interval and flags the gap", func(t *testing.T) {
		t.Parallel()
		layer := build(t, true)
		t0 := time.Unix(91_000, 0)
		leaveAt := t0.Add(10 * time.Second)

		layer.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
		layer.Learn(leaveAt, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.Leave, Group: group})

		other := netip.MustParseAddr("10.9.9.9")
		ports, _, pending := layer.Resolve(vid, group, other, leaveAt.Add(3*time.Second))
		if !slices.Contains(ports, "1/1/1") {
			t.Errorf("admitted at t+3s = false, want true")
		}
		if !pending {
			t.Errorf("pending at t+3s = false, want true")
		}
	})

	t.Run("with no router port nothing is flagged", func(t *testing.T) {
		t.Parallel()
		layer := build(t, false)
		t0 := time.Unix(92_000, 0)
		leaveAt := t0.Add(10 * time.Second)

		layer.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
		layer.Learn(leaveAt, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.Leave, Group: group})

		other := netip.MustParseAddr("10.9.9.9")
		ports, _, pending := layer.Resolve(vid, group, other, leaveAt.Add(3*time.Second))
		if !slices.Contains(ports, "1/1/1") {
			t.Errorf("admitted at t+3s = false, want true")
		}
		if pending {
			t.Errorf("pending at t+3s = true, want false with no router port")
		}
	})
}

// --- fast leave ---

func TestFastLeave(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	s1 := netip.MustParseAddr("10.1.1.1")

	t.Run("legacy leave deletes the EXCLUDE state outright", func(t *testing.T) {
		t.Parallel()
		layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			vid: {FastLeave: true},
		}}, mcastPortTable(t))
		t0 := time.Unix(100_000, 0)
		layer.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
		layer.Learn(t0.Add(time.Second), vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.Leave, Group: group})

		if entries := layer.Groups(vid); entryExists(entries, "1/1/1") {
			t.Errorf("Groups() = %+v, want 1/1/1 removed", entries)
		}
	})

	t.Run("a v3 record shaped like a leave deletes the INCLUDE state outright", func(t *testing.T) {
		t.Parallel()
		layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			vid: {FastLeave: true},
		}}, mcastPortTable(t))
		t0 := time.Unix(101_000, 0)
		igmpRecord(t0, layer, vid, "1/1/1", igmp.ModeIsInclude, group, []netip.Addr{s1})
		igmpRecord(t0.Add(time.Second), layer, vid, "1/1/1", igmp.ModeIsInclude, group, nil)

		if entries := layer.Groups(vid); entryExists(entries, "1/1/1") {
			t.Errorf("Groups() = %+v, want 1/1/1 removed", entries)
		}
	})
}
