package simtest

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
)

func mcastDiffMapping() (diffMapping, error) {
	return newDiffMapping("mcast", mcast.Config{}, `{"VLANs":{"10":{"FloodUnregistered":true,"FastLeave":true,"RouterPorts":["1/1/1"],"MembershipInterval":"260s","RouterPortInterval":"260s","LastMemberQueryInterval":"1s","LastMemberQueryCount":2}}}`, func(c mcast.Config) mcast.Config { return c.Normalize(layer.Env{}) }, mcast.Diff, []mappedEntry{
		{Path: "mcast.Config.VLANs[10].FloodUnregistered", Producer: "mcast", Setup: "", Before: `true`, After: `false`},
		{Path: "mcast.Config.VLANs[10].FastLeave", Producer: "mcast", Setup: "", Before: `true`, After: `false`},
		{Path: "mcast.Config.VLANs[10].MembershipInterval", Producer: "mcast", Setup: "", Before: `"260s"`, After: `"261s"`},
		{Path: "mcast.Config.VLANs[10].RouterPortInterval", Producer: "mcast", Setup: "", Before: `"260s"`, After: `"261s"`},
		{Path: "mcast.Config.VLANs[10].LastMemberQueryInterval", Producer: "mcast", Setup: "", Before: `"1s"`, After: `"2s"`},
		{Path: "mcast.Config.VLANs[10].LastMemberQueryCount", Producer: "mcast", Setup: "", Before: `2`, After: `3`},
		{Path: "mcast.Config.VLANs[10].RouterPorts[0]", Producer: "mcast", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "mcast.Config.VLANs[10].@key", Producer: "mcast", Setup: "", Before: `10`, After: `11`},
		{Path: "mcast.Config.VLANs[10].FloodUnregistered.@present", Producer: "mcast", Setup: "", Before: `true`, After: `null`, Reason: "Nil flood policy and true have the same effective value"},
	}, mcastDiffExpectation)
}

func mcastDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "mcast.Config.VLANs[10].FloodUnregistered":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "flood_unregistered", &[2]string{"mcast.bool", "true"}, &[2]string{"mcast.bool", "false"}},
		}, nil
	case "mcast.Config.VLANs[10].FastLeave":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "fast_leave", &[2]string{"mcast.bool", "true"}, &[2]string{"mcast.bool", "false"}},
		}, nil
	case "mcast.Config.VLANs[10].MembershipInterval":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "membership_interval", &[2]string{"mcast.duration", "4m20s"}, &[2]string{"mcast.duration", "4m21s"}},
		}, nil
	case "mcast.Config.VLANs[10].RouterPortInterval":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "router_port_interval", &[2]string{"mcast.duration", "4m20s"}, &[2]string{"mcast.duration", "4m21s"}},
		}, nil
	case "mcast.Config.VLANs[10].LastMemberQueryInterval":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "last_member_query_interval", &[2]string{"mcast.duration", "1s"}, &[2]string{"mcast.duration", "2s"}},
		}, nil
	case "mcast.Config.VLANs[10].LastMemberQueryCount":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "last_member_query_count", &[2]string{"mcast.int", "2"}, &[2]string{"mcast.int", "3"}},
		}, nil
	case "mcast.Config.VLANs[10].RouterPorts[0]":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "router_ports[\"1/1/1\"]", &[2]string{"mcast.router_port", "1/1/1"}, nil},
			{"mcast", "vlan", "10", "router_ports[\"1/1/2\"]", nil, &[2]string{"mcast.router_port", "1/1/2"}},
		}, nil
	case "mcast.Config.VLANs[10].@key":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "", &[2]string{"mcast.vlan_snooping", "flood=true,fast_leave=true,router_ports=[\"1/1/1\"],mem_int=4m20s,rtr_int=4m20s,lmqi=1s,lmqc=2"}, nil},
			{"mcast", "vlan", "11", "", nil, &[2]string{"mcast.vlan_snooping", "flood=true,fast_leave=true,router_ports=[\"1/1/1\"],mem_int=4m20s,rtr_int=4m20s,lmqi=1s,lmqc=2"}},
		}, nil
	case "mcast.Config.VLANs[10].FloodUnregistered.@present":
		*hits++
		return []changeRecord{}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
