package mcast_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
)

func TestControlMessageFactsAreImmutableAndNormalizeSetOrder(t *testing.T) {
	for _, test := range []struct {
		name string
		fact func([]netip.Addr) trace.Fact
	}{
		{
			name: "IGMP",
			fact: func(sources []netip.Addr) trace.Fact {
				return mcast.IGMPControlMessageFact(netip.MustParseAddr("10.0.0.1"), igmp.Message{
					Type: igmp.ReportV3,
					Records: []igmp.GroupRecord{{
						Type: igmp.ChangeToExcludeMode, Group: netip.MustParseAddr("239.1.1.1"), Sources: sources,
					}},
				})
			},
		},
		{
			name: "MLD",
			fact: func(sources []netip.Addr) trace.Fact {
				return mcast.MLDControlMessageFact(netip.MustParseAddr("fe80::1"), mld.Message{
					Type: mld.ReportV2,
					Records: []mld.AddressRecord{{
						Type: mld.ChangeToExcludeMode, Group: netip.MustParseAddr("ff05::1"), Sources: sources,
					}},
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			firstSource := netip.MustParseAddr("2001:db8::1")
			secondSource := netip.MustParseAddr("2001:db8::2")
			if test.name == "IGMP" {
				firstSource = netip.MustParseAddr("10.0.0.1")
				secondSource = netip.MustParseAddr("10.0.0.2")
			}

			sources := []netip.Addr{secondSource, firstSource}
			fact := test.fact(sources)
			before := fact.Canonical()
			sources[0] = netip.Addr{}
			if got := fact.Canonical(); got != before {
				t.Errorf("fact changed after source mutation: got %q, want %q", got, before)
			}

			normalized := test.fact([]netip.Addr{firstSource, secondSource})
			if !trace.EqualFact(fact, normalized) {
				t.Errorf("source set order changed fact:\nfirst:  %s\nsecond: %s", fact, normalized)
			}
		})
	}
}

func TestControlMessageFactsDistinguishTypeAndGroupTransition(t *testing.T) {
	groupOne := netip.MustParseAddr("239.1.1.1")
	groupTwo := netip.MustParseAddr("239.1.1.2")
	fact := func(messageType igmp.Type, group netip.Addr) trace.Fact {
		return mcast.IGMPControlMessageFact(netip.MustParseAddr("10.0.0.1"), igmp.Message{
			Type: messageType,
			Records: []igmp.GroupRecord{{
				Type: igmp.ChangeToExcludeMode, Group: group,
			}},
		})
	}

	baseline := fact(igmp.ReportV3, groupOne)
	if trace.EqualFact(baseline, fact(igmp.ReportV2, groupOne)) {
		t.Fatal("different decoded message types compare equal")
	}
	if trace.EqualFact(baseline, fact(igmp.ReportV3, groupTwo)) {
		t.Fatal("different group transitions compare equal")
	}
}

func TestControlMessageFactsPreserveBehaviorSignificantRecordOrder(t *testing.T) {
	const vid vlan.ID = 10
	group := netip.MustParseAddr("239.1.1.1")
	joinRecord := igmp.GroupRecord{Type: igmp.ModeIsExclude, Group: group}
	leaveRecord := igmp.GroupRecord{Type: igmp.ChangeToIncludeMode, Group: group}
	fact := func(records []igmp.GroupRecord) trace.Fact {
		return mcast.IGMPControlMessageFact(netip.MustParseAddr("10.0.0.1"), igmp.Message{
			Type:    igmp.ReportV3,
			Records: records,
		})
	}
	learn := func(records []igmp.GroupRecord) []mcast.Entry {
		layer := mustNewMcast(t, mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			vid: {FastLeave: true},
		}}, mcastPortTable(t))
		layer.Learn(time.Unix(5_000, 0), vid, "1/1/1", netip.MustParseAddr("10.0.0.1"), igmp.Message{
			Type:    igmp.ReportV3,
			Records: records,
		})
		return layer.Groups(vid)
	}

	joinedThenLeft := []igmp.GroupRecord{joinRecord, leaveRecord}
	leftThenJoined := []igmp.GroupRecord{leaveRecord, joinRecord}
	if got := learn(joinedThenLeft); len(got) != 0 {
		t.Fatalf("join then leave groups = %+v, want none", got)
	}
	if got := learn(leftThenJoined); len(got) != 1 {
		t.Fatalf("leave then join groups = %+v, want one", got)
	}
	if trace.EqualFact(fact(joinedThenLeft), fact(leftThenJoined)) {
		t.Fatal("behaviorally distinct record orders produced equal control facts")
	}
}
