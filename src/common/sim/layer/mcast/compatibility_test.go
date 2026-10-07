package mcast_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
)

func TestLeaveDoesNotStartCompatibilityTimer(t *testing.T) {
	for _, protocol := range []string{"IGMP", "MLD"} {
		t.Run(protocol, func(t *testing.T) {
			l := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {}}}, mcastPortTable(t))
			t0 := time.Unix(120000, 0)
			group := netip.MustParseAddr("239.1.1.1")
			s1, s2 := netip.MustParseAddr("10.1.1.1"), netip.MustParseAddr("10.1.1.2")
			if protocol == "MLD" {
				group = netip.MustParseAddr("ff05::1")
				s1, s2 = netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("2001:db8::2")
				l.LearnMLD(t0, vid, memberPort, netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.ReportV2, Records: []mld.AddressRecord{{Type: mld.ModeIsInclude, Group: group, Sources: []netip.Addr{s2}}}})
				l.LearnMLD(t0.Add(time.Second), vid, memberPort, netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.Done, Group: group})
				l.LearnMLD(t0.Add(2*time.Second), vid, memberPort, netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.ReportV2, Records: []mld.AddressRecord{{Type: mld.ChangeToExcludeMode, Group: group, Sources: []netip.Addr{s1}}}})
			} else {
				igmpRecord(t0, l, igmp.ModeIsInclude, group, []netip.Addr{s2})
				l.Learn(t0.Add(time.Second), vid, memberPort, s2, igmp.Message{Type: igmp.Leave, Group: group})
				igmpRecord(t0.Add(2*time.Second), l, igmp.ChangeToExcludeMode, group, []netip.Addr{s1})
			}
			e := mustEntry(t, l.Groups(vid, t0.Add(2*time.Second)))
			if e.Mode != mcast.Exclude || len(e.Sources) != 1 || e.Sources[0].Address != s1 || !e.Sources[0].Expires.IsZero() {
				t.Fatalf("state = %+v, want EXCLUDE with s1 blocked", e)
			}
			if ports, _, _ := l.Resolve(vid, group, s1, t0.Add(2*time.Second)); slices.Contains(ports, memberPort) {
				t.Errorf("Resolve = %v, want s1 excluded", ports)
			}
		})
	}
}

func TestIGMPCompatibilityTimersSurviveCopies(t *testing.T) {
	for _, copyKind := range []string{"original", "clone", "restore"} {
		t.Run(copyKind, func(t *testing.T) {
			cfg := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {MembershipInterval: 10 * time.Second, FastLeave: true}}}
			l := mustNewMcast(t, cfg, mcastPortTable(t))
			t0 := time.Unix(121000, 0)
			group, source := netip.MustParseAddr("239.1.1.1"), netip.MustParseAddr("10.1.1.1")
			l.Learn(t0, vid, memberPort, source, igmp.Message{Type: igmp.ReportV1, Group: group})
			l.Learn(t0.Add(5*time.Second), vid, memberPort, source, igmp.Message{Type: igmp.ReportV2, Group: group})
			switch copyKind {
			case "clone":
				l = l.Clone()
			case "restore":
				next := mustNewMcast(t, cfg, mcastPortTable(t))
				next.Restore(l, func(vlan.ID, string) bool { return true })
				l = next
			}
			igmpRecord(t0.Add(6*time.Second), l, igmp.ChangeToIncludeMode, group, nil)
			if e := l.Groups(vid, t0.Add(6*time.Second)); !entryExists(e) {
				t.Fatal("IGMPv1 state disappeared after TO_IN with fast leave")
			}
			l.Learn(t0.Add(7*time.Second), vid, memberPort, source, igmp.Message{Type: igmp.Leave, Group: group})
			if e := l.Groups(vid, t0.Add(7*time.Second)); !entryExists(e) {
				t.Fatal("IGMPv1 state disappeared after Leave with fast leave")
			}
			igmpRecord(t0.Add(11*time.Second), l, igmp.ChangeToExcludeMode, group, []netip.Addr{source})
			if e := mustEntry(t, l.Groups(vid, t0.Add(11*time.Second))); len(e.Sources) != 0 {
				t.Fatalf("IGMPv2 state = %+v, want TO_EX source list ignored", e)
			}
			igmpRecord(t0.Add(16*time.Second), l, igmp.ChangeToExcludeMode, group, []netip.Addr{source})
			if e := mustEntry(t, l.Groups(vid, t0.Add(16*time.Second))); len(e.Sources) != 1 {
				t.Errorf("IGMPv3 state = %+v, want source list restored after both timers expire", e)
			}
		})
	}
}

func TestRouterPortZeroHasNoNamedValues(t *testing.T) {
	var router mcast.RouterPort
	if router.Origin == mcast.Configured || router.Origin == mcast.Observed || router.Lifetime == mcast.Aging || router.Lifetime == mcast.Static {
		t.Errorf("zero RouterPort = %+v, want no named origin or lifetime", router)
	}
}

func TestLeaveDoesNotRefreshCompatibilityTimer(t *testing.T) {
	for _, protocol := range []string{"IGMP", "MLD"} {
		t.Run(protocol, func(t *testing.T) {
			l := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {MembershipInterval: 10 * time.Second}}}, mcastPortTable(t))
			t0 := time.Unix(125000, 0)
			group, source := netip.MustParseAddr("239.1.1.1"), netip.MustParseAddr("10.1.1.1")
			if protocol == "MLD" {
				group, source = netip.MustParseAddr("ff05::1"), netip.MustParseAddr("2001:db8::1")
				l.LearnMLD(t0, vid, memberPort, netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.ReportV1, Group: group})
				l.LearnMLD(t0.Add(5*time.Second), vid, memberPort, netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.Done, Group: group})
				l.LearnMLD(t0.Add(9*time.Second), vid, memberPort, netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.ReportV2, Records: []mld.AddressRecord{{Type: mld.ModeIsExclude, Group: group}}})
				l.LearnMLD(t0.Add(11*time.Second), vid, memberPort, netip.MustParseAddr("fe80::1"), mld.Message{Type: mld.ReportV2, Records: []mld.AddressRecord{{Type: mld.ChangeToExcludeMode, Group: group, Sources: []netip.Addr{source}}}})
			} else {
				l.Learn(t0, vid, memberPort, source, igmp.Message{Type: igmp.ReportV2, Group: group})
				l.Learn(t0.Add(5*time.Second), vid, memberPort, source, igmp.Message{Type: igmp.Leave, Group: group})
				igmpRecord(t0.Add(9*time.Second), l, igmp.ModeIsExclude, group, nil)
				igmpRecord(t0.Add(11*time.Second), l, igmp.ChangeToExcludeMode, group, []netip.Addr{source})
			}
			if e := mustEntry(t, l.Groups(vid, t0.Add(11*time.Second))); len(e.Sources) != 1 {
				t.Errorf("state = %+v, want source list honored after report timer expires", e)
			}
		})
	}
}

func TestRestoreDeepCopiesQueryObligations(t *testing.T) {
	cfg := mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {RouterPorts: []string{"1/1/8"}}}}
	original := mustNewMcast(t, cfg, mcastPortTable(t))
	t0 := time.Unix(126000, 0)
	group := netip.MustParseAddr("239.1.1.1")
	s1, s2 := netip.MustParseAddr("10.1.1.1"), netip.MustParseAddr("10.1.1.2")
	igmpRecord(t0, original, igmp.ModeIsInclude, group, []netip.Addr{s1, s2})
	igmpRecord(t0.Add(time.Second), original, igmp.BlockOldSources, group, []netip.Addr{s1, s2})
	next := mustNewMcast(t, cfg, mcastPortTable(t))
	next.Restore(original, func(vlan.ID, string) bool { return true })
	next.Learn(t0.Add(2*time.Second), vid, "1/1/8", s1, igmp.Message{Type: igmp.Query, Group: group, Sources: []netip.Addr{s1}})
	original.Learn(t0.Add(2*time.Second), vid, "1/1/8", s1, igmp.Message{Type: igmp.Query, Group: group, Sources: []netip.Addr{s2}})
	for _, l := range []*mcast.Layer{original, next} {
		if _, registered, pending := l.Resolve(vid, group, s1, t0.Add(3*time.Second)); !registered || !pending {
			t.Errorf("registered, pending = %v, %v, want independently retained obligation", registered, pending)
		}
	}
}

func TestSourceQueryObligationsEndIndividually(t *testing.T) {
	for _, action := range []string{"query", "allow", "delete"} {
		t.Run(action, func(t *testing.T) {
			l := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {RouterPorts: []string{"1/1/8"}}}}, mcastPortTable(t))
			t0 := time.Unix(122000, 0)
			group := netip.MustParseAddr("239.1.1.1")
			s1, s2 := netip.MustParseAddr("10.1.1.1"), netip.MustParseAddr("10.1.1.2")
			igmpRecord(t0, l, igmp.ModeIsInclude, group, []netip.Addr{s1, s2})
			igmpRecord(t0.Add(time.Second), l, igmp.BlockOldSources, group, []netip.Addr{s1, s2})
			if _, registered, pending := l.Resolve(vid, group, s2, t0.Add(4*time.Second)); !registered || !pending {
				t.Fatal("BLOCK fixture is not registered and pending")
			}
			switch action {
			case "query":
				l.Learn(t0.Add(5*time.Second), vid, "1/1/8", s1, igmp.Message{Type: igmp.Query, Group: group, Sources: []netip.Addr{s1}})
			case "allow":
				igmpRecord(t0.Add(5*time.Second), l, igmp.AllowNewSources, group, []netip.Addr{s1})
			case "delete":
				igmpRecord(t0.Add(5*time.Second), l, igmp.ModeIsExclude, group, []netip.Addr{s2})
			}
			if _, _, pending := l.Resolve(vid, group, s2, t0.Add(6*time.Second)); !pending {
				t.Fatal("remaining s2 obligation = false, want true")
			}
			l.Learn(t0.Add(6*time.Second), vid, "1/1/8", s1, igmp.Message{Type: igmp.Query, Group: group, Sources: []netip.Addr{s2}})
			if _, _, pending := l.Resolve(vid, group, s2, t0.Add(7*time.Second)); pending {
				t.Error("pending = true, want both source obligations ended")
			}
		})
	}
}

func TestGroupQueryLeavesSourceObligation(t *testing.T) {
	l := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {RouterPorts: []string{"1/1/8"}}}}, mcastPortTable(t))
	t0 := time.Unix(123000, 0)
	group, source := netip.MustParseAddr("239.1.1.1"), netip.MustParseAddr("10.1.1.1")
	igmpRecord(t0, l, igmp.ModeIsInclude, group, []netip.Addr{source})
	igmpRecord(t0.Add(time.Second), l, igmp.BlockOldSources, group, []netip.Addr{source})
	l.Learn(t0.Add(2*time.Second), vid, "1/1/8", source, igmp.Message{Type: igmp.Query, Group: group})
	if _, registered, pending := l.Resolve(vid, group, source, t0.Add(5*time.Second)); !registered || !pending {
		t.Errorf("registered, pending = %v, %v, want true, true", registered, pending)
	}
}

func TestReportEndsGroupQueryObligation(t *testing.T) {
	l := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{vid: {RouterPorts: []string{"1/1/8"}}}}, mcastPortTable(t))
	t0 := time.Unix(124000, 0)
	group, source := netip.MustParseAddr("239.1.1.1"), netip.MustParseAddr("10.1.1.1")
	l.Learn(t0, vid, memberPort, source, igmp.Message{Type: igmp.ReportV2, Group: group})
	l.Learn(t0.Add(time.Second), vid, memberPort, source, igmp.Message{Type: igmp.Leave, Group: group})
	if _, _, pending := l.Resolve(vid, group, source, t0.Add(4*time.Second)); !pending {
		t.Fatal("leave fixture is not pending")
	}
	l.Learn(t0.Add(5*time.Second), vid, memberPort, source, igmp.Message{Type: igmp.ReportV2, Group: group})
	if _, registered, pending := l.Resolve(vid, group, source, t0.Add(6*time.Second)); !registered || pending {
		t.Errorf("registered, pending = %v, %v, want true, false", registered, pending)
	}
}
