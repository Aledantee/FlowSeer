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
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return ports
}

func TestResolveReturnsMembersAndRouterPorts(t *testing.T) {
	t.Parallel()

	ports := mcastPortTable(t)

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, ports)
	now := time.Unix(1_000, 0)
	layer.Learn(now, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{
		Type:  igmp.ReportV2,
		Group: group,
	})
	layer.Learn(now, vid, "1/1/4", netip.MustParseAddr("10.0.0.254"), igmp.Message{Type: igmp.Query})

	got, registered := layer.Resolve(vid, group)
	want := []string{"1/1/1", "1/1/4"}
	if !registered || !slices.Equal(got, want) {
		t.Errorf("Resolve() = (%v, %v), want (%v, true)", got, registered, want)
	}

	entries := layer.Groups(vid)
	if len(entries) != 1 || entries[0].Group != group || entries[0].Port != "1/1/1" ||
		!entries[0].Expires.Equal(now.Add(mcast.DefaultMembershipInterval)) {
		t.Errorf("Groups() = %+v, want learned membership with default expiry", entries)
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
	layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, mcastPortTable(t))
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

	resolved, registered := layer.Resolve(vid, netip.MustParseAddr("239.9.9.9"))
	if registered || !slices.Equal(resolved, []string{"1/1/2", "1/1/4"}) {
		t.Errorf("router-only Resolve() = (%v, %v), want router ports and false", resolved, registered)
	}
}

func TestStaticRouterPortsNeverExpire(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
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
	layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, mcastPortTable(t))
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
	layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, ports)
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
}

func TestIGMPv3RecordRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		recordType igmp.RecordType
		withSource bool
		want       string
	}{
		{name: "mode is include with source", recordType: igmp.ModeIsInclude, withSource: true, want: "refresh"},
		{name: "mode is include empty", recordType: igmp.ModeIsInclude, want: "remove"},
		{name: "mode is exclude with source", recordType: igmp.ModeIsExclude, withSource: true, want: "refresh"},
		{name: "mode is exclude empty", recordType: igmp.ModeIsExclude, want: "refresh"},
		{name: "change to include with source", recordType: igmp.ChangeToIncludeMode, withSource: true, want: "refresh"},
		{name: "change to include empty", recordType: igmp.ChangeToIncludeMode, want: "remove"},
		{name: "change to exclude with source", recordType: igmp.ChangeToExcludeMode, withSource: true, want: "refresh"},
		{name: "change to exclude empty", recordType: igmp.ChangeToExcludeMode, want: "refresh"},
		{name: "allow new sources with source", recordType: igmp.AllowNewSources, withSource: true, want: "refresh"},
		{name: "allow new sources empty", recordType: igmp.AllowNewSources, want: "unchanged"},
		{name: "block old sources with source", recordType: igmp.BlockOldSources, withSource: true, want: "unchanged"},
		{name: "block old sources empty", recordType: igmp.BlockOldSources, want: "unchanged"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const vid vlan.ID = 10
			group := netip.MustParseAddr("239.1.1.1")
			t0 := time.Unix(5_000, 0)
			layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				vid: {FastLeave: true},
			}}, mcastPortTable(t))
			layer.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})

			var sources []netip.Addr
			if tt.withSource {
				sources = []netip.Addr{netip.MustParseAddr("10.1.1.1")}
			}
			t1 := t0.Add(time.Second)
			layer.Learn(t1, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{
				Type: igmp.ReportV3,
				Records: []igmp.GroupRecord{{
					Type: tt.recordType, Group: group, Sources: sources,
				}},
			})

			assertRecordResult(t, layer.Groups(vid), tt.want, t0, t1)
		})
	}
}

func TestMLDv2RecordRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		recordType mld.RecordType
		withSource bool
		want       string
	}{
		{name: "mode is include with source", recordType: mld.ModeIsInclude, withSource: true, want: "refresh"},
		{name: "mode is include empty", recordType: mld.ModeIsInclude, want: "remove"},
		{name: "mode is exclude with source", recordType: mld.ModeIsExclude, withSource: true, want: "refresh"},
		{name: "mode is exclude empty", recordType: mld.ModeIsExclude, want: "refresh"},
		{name: "change to include with source", recordType: mld.ChangeToIncludeMode, withSource: true, want: "refresh"},
		{name: "change to include empty", recordType: mld.ChangeToIncludeMode, want: "remove"},
		{name: "change to exclude with source", recordType: mld.ChangeToExcludeMode, withSource: true, want: "refresh"},
		{name: "change to exclude empty", recordType: mld.ChangeToExcludeMode, want: "refresh"},
		{name: "allow new sources with source", recordType: mld.AllowNewSources, withSource: true, want: "refresh"},
		{name: "allow new sources empty", recordType: mld.AllowNewSources, want: "unchanged"},
		{name: "block old sources with source", recordType: mld.BlockOldSources, withSource: true, want: "unchanged"},
		{name: "block old sources empty", recordType: mld.BlockOldSources, want: "unchanged"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const vid vlan.ID = 10
			group := netip.MustParseAddr("ff05::1")
			t0 := time.Unix(6_000, 0)
			layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				vid: {FastLeave: true},
			}}, mcastPortTable(t))
			layer.LearnMLD(t0, vid, "1/1/1", netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.ReportV1, Group: group})

			var sources []netip.Addr
			if tt.withSource {
				sources = []netip.Addr{netip.MustParseAddr("2001:db8::1")}
			}
			t1 := t0.Add(time.Second)
			layer.LearnMLD(t1, vid, "1/1/1", netip.MustParseAddr("fe80::1"), mld.Message{
				Type: mld.ReportV2,
				Records: []mld.AddressRecord{{
					Type: tt.recordType, Group: group, Sources: sources,
				}},
			})

			assertRecordResult(t, layer.Groups(vid), tt.want, t0, t1)
		})
	}
}

func assertRecordResult(t *testing.T, entries []mcast.Entry, want string, t0, t1 time.Time) {
	t.Helper()

	if want == "remove" {
		if len(entries) != 0 {
			t.Errorf("Groups() = %+v, want membership removed", entries)
		}

		return
	}
	if len(entries) != 1 {
		t.Fatalf("Groups() = %+v, want one membership", entries)
	}

	wantExpiry := t0.Add(mcast.DefaultMembershipInterval)
	if want == "refresh" {
		wantExpiry = t1.Add(mcast.DefaultMembershipInterval)
	}
	if !entries[0].Expires.Equal(wantExpiry) {
		t.Errorf("expiry = %v, want %v for %s", entries[0].Expires, wantExpiry, want)
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
			layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
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

func TestAgeDefaultAndConfiguredIntervals(t *testing.T) {
	t.Parallel()

	t.Run("default", func(t *testing.T) {
		t.Parallel()

		const vid vlan.ID = 10
		group := netip.MustParseAddr("239.1.1.1")
		t0 := time.Unix(8_000, 0)
		layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, mcastPortTable(t))
		layer.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
		layer.Learn(t0, vid, "1/1/4", netip.MustParseAddr("10.0.0.254"), igmp.Message{Type: igmp.Query})

		layer.Age(t0.Add(259 * time.Second))
		if got, registered := layer.Resolve(vid, group); !registered || !slices.Equal(got, []string{"1/1/1", "1/1/4"}) {
			t.Errorf("Resolve() at 259s = (%v, %v), want both ports and true", got, registered)
		}

		layer.Age(t0.Add(261 * time.Second))
		if got, registered := layer.Resolve(vid, group); registered || len(got) != 0 {
			t.Errorf("Resolve() at 261s = (%v, %v), want empty and false", got, registered)
		}
	})

	t.Run("configured", func(t *testing.T) {
		t.Parallel()

		const vid vlan.ID = 10
		group := netip.MustParseAddr("239.1.1.1")
		t0 := time.Unix(9_000, 0)
		layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			vid: {MembershipInterval: 10 * time.Second, RouterPortInterval: 20 * time.Second},
		}}, mcastPortTable(t))
		layer.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
		layer.Learn(t0, vid, "1/1/4", netip.MustParseAddr("10.0.0.254"), igmp.Message{Type: igmp.Query})

		layer.Age(t0.Add(10 * time.Second))
		if got, registered := layer.Resolve(vid, group); registered || !slices.Equal(got, []string{"1/1/4"}) {
			t.Errorf("Resolve() at membership expiry = (%v, %v), want router only and false", got, registered)
		}

		layer.Age(t0.Add(20 * time.Second))
		if got := layer.RouterPorts(vid); len(got) != 0 {
			t.Errorf("RouterPorts() at configured expiry = %+v, want none", got)
		}
	})
}

func TestRetainDropsDepartedPortsAndPreservesExpiries(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	t0 := time.Unix(10_000, 0)
	ports := mcastPortTable(t)
	layer := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, ports)
	for _, name := range []string{"1/1/1", "1/1/2"} {
		layer.Learn(t0, vid, name, netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
	}
	layer.Learn(t0, vid, "1/1/4", netip.MustParseAddr("10.0.0.254"), igmp.Message{Type: igmp.Query})

	wantExpiry := t0.Add(mcast.DefaultMembershipInterval)
	layer.Retain(ports, func(_ vlan.ID, name string) bool { return name != "1/1/2" })
	groups := layer.Groups(vid)
	if len(groups) != 1 || groups[0].Port != "1/1/1" || !groups[0].Expires.Equal(wantExpiry) {
		t.Errorf("Groups() after Retain = %+v, want retained 1/1/1 with original expiry", groups)
	}
	if routers := layer.RouterPorts(vid); len(routers) != 1 || routers[0].Port != "1/1/4" {
		t.Errorf("RouterPorts() after Retain = %+v, want retained router", routers)
	}
}

func TestLayerCloneIsIndependent(t *testing.T) {
	t.Parallel()

	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	t0 := time.Unix(11_000, 0)
	ports := mcastPortTable(t)
	original := mcast.New(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, ports)
	original.Learn(t0, vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{Type: igmp.ReportV2, Group: group})
	original.Learn(t0, vid, "1/1/4", netip.MustParseAddr("10.0.0.254"), igmp.Message{Type: igmp.Query})

	clone := original.Clone()
	clone.Retain(ports, func(_ vlan.ID, name string) bool { return name != "1/1/1" })
	original.Learn(t0, vid, "1/1/2", netip.MustParseAddr("10.0.0.2"), igmp.Message{Type: igmp.ReportV2, Group: group})

	if got, registered := original.Resolve(vid, group); !registered || !slices.Equal(got, []string{"1/1/1", "1/1/2", "1/1/4"}) {
		t.Errorf("original Resolve() = (%v, %v), want independent original state", got, registered)
	}
	if got, registered := clone.Resolve(vid, group); registered || !slices.Equal(got, []string{"1/1/4"}) {
		t.Errorf("clone Resolve() = (%v, %v), want independent clone state", got, registered)
	}
}
