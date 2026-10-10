package simtest

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
)

// RoutingDiffAllowance returns no exceptions for this exact producer.
func RoutingDiffAllowance() DiffAllowance {
	return DiffAllowance{}
}

func routingDiffMapping() (diffMapping, error) {
	return newDiffMapping("routing", routing.Config{}, `{"ClearRouterFlag":false,"VRFs":{"default":{"Interfaces":{"vlan10":{"VLAN":10,"Port":"1/1/1","MAC":"02:00:00:00:00:01","Prefixes":["10.0.10.1/24"]}},"Routes":[{"Prefix":"10.0.20.0/24","NextHop":"10.0.10.7","Interface":"vlan10","Preference":1,"Metric":10}],"Neighbors":[{"Interface":"vlan10","Addr":"10.0.10.7","MAC":"02:00:00:00:00:02"}],"NeighborPolicy":{"Mode":"disabled","ReachableTime":"30s","RetransTimer":"3s","HoldDepth":4}}}}`, func(c routing.Config) routing.Config { return c.Normalize(layer.Env{}) }, routing.Diff, []mappedEntry{
		{Path: "routing.Config.ClearRouterFlag", Producer: "routing", AllowancePath: "routing.Config.ClearRouterFlag", Setup: "", Before: `false`, After: `true`},
		{Path: "routing.Config.VRFs[default].Interfaces[vlan10].VLAN", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Interfaces[vlan10].VLAN", Setup: "", Before: `10`, After: `11`},
		{Path: "routing.Config.VRFs[default].Interfaces[vlan10].Port", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Interfaces[vlan10].Port", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "routing.Config.VRFs[default].Interfaces[vlan10].MAC", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Interfaces[vlan10].MAC", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
		{Path: "routing.Config.VRFs[default].Interfaces[vlan10].Prefixes[0]", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Interfaces[vlan10].Prefixes[0]", Setup: "", Before: `"10.0.10.1/24"`, After: `"192.0.2.0/25"`},
		{Path: "routing.Config.VRFs[default].Routes[0].Prefix", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Routes[0].Prefix", Setup: "", Before: `"10.0.20.0/24"`, After: `"192.0.2.0/25"`},
		{Path: "routing.Config.VRFs[default].Routes[0].NextHop", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Routes[0].NextHop", Setup: "", Before: `"10.0.10.7"`, After: `"10.0.10.8"`},
		{Path: "routing.Config.VRFs[default].Routes[0].Interface", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Routes[0].Interface", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
		{Path: "routing.Config.VRFs[default].Routes[0].Preference", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Routes[0].Preference", Setup: "", Before: `1`, After: `2`},
		{Path: "routing.Config.VRFs[default].Routes[0].Metric", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Routes[0].Metric", Setup: "", Before: `10`, After: `11`},
		{Path: "routing.Config.VRFs[default].Neighbors[0].Interface", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Neighbors[0].Interface", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
		{Path: "routing.Config.VRFs[default].Neighbors[0].Addr", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Neighbors[0].Addr", Setup: "", Before: `"10.0.10.7"`, After: `"10.0.10.8"`},
		{Path: "routing.Config.VRFs[default].Neighbors[0].MAC", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Neighbors[0].MAC", Setup: "", Before: `"02:00:00:00:00:02"`, After: `"02:00:00:00:00:03"`},
		{Path: "routing.Config.VRFs[default].NeighborPolicy.Mode", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].NeighborPolicy.Mode", Setup: "", Before: `"disabled"`, After: `""`},
		{Path: "routing.Config.VRFs[default].NeighborPolicy.ReachableTime", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].NeighborPolicy.ReachableTime", Setup: "", Before: `"30s"`, After: `"31s"`},
		{Path: "routing.Config.VRFs[default].NeighborPolicy.RetransTimer", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].NeighborPolicy.RetransTimer", Setup: "", Before: `"3s"`, After: `"4s"`},
		{Path: "routing.Config.VRFs[default].NeighborPolicy.HoldDepth", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].NeighborPolicy.HoldDepth", Setup: "", Before: `4`, After: `5`},
		{Path: "routing.Config.VRFs[default].@key", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].@key", Setup: "", Before: `"default"`, After: `"default2"`},
		{Path: "routing.Config.VRFs[default].Interfaces[vlan10].@key", Producer: "routing", AllowancePath: "routing.Config.VRFs[default].Interfaces[vlan10].@key", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
	}, routingDiffExpectation)
}

func routingDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "routing.Config.ClearRouterFlag":
		*hits++
		return []changeRecord{
			{"routing", "routing", "", "clear_router_flag", &[2]string{"routing.clear_router_flag", "false"}, &[2]string{"routing.clear_router_flag", "true"}},
		}, nil
	case "routing.Config.VRFs[default].Interfaces[vlan10].VLAN":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"default\"/\"vlan10\"", "vlan", &[2]string{"routing.vlan", "10"}, &[2]string{"routing.vlan", "11"}},
		}, nil
	case "routing.Config.VRFs[default].Interfaces[vlan10].Port":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"default\"/\"vlan10\"", "port", &[2]string{"routing.port", "1/1/1"}, &[2]string{"routing.port", "1/1/2"}},
		}, nil
	case "routing.Config.VRFs[default].Interfaces[vlan10].MAC":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"default\"/\"vlan10\"", "mac", &[2]string{"routing.mac", "02:00:00:00:00:01"}, &[2]string{"routing.mac", "02:00:00:00:00:02"}},
		}, nil
	case "routing.Config.VRFs[default].Interfaces[vlan10].Prefixes[0]":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"default\"/\"vlan10\"", "prefixes[\"10.0.10.1/24\"]", &[2]string{"routing.prefix", "10.0.10.1/24"}, nil},
			{"routing", "interface", "\"default\"/\"vlan10\"", "prefixes[\"192.0.2.0/25\"]", nil, &[2]string{"routing.prefix", "192.0.2.0/25"}},
		}, nil
	case "routing.Config.VRFs[default].Routes[0].Prefix":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan10\"", "", &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}, nil},
			{"routing", "route", "\"default\"/\"192.0.2.0/25\"/\"10.0.10.7\"/\"vlan10\"", "", nil, &[2]string{"routing.route", "prefix=\"192.0.2.0/25\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}},
		}, nil
	case "routing.Config.VRFs[default].Routes[0].NextHop":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan10\"", "", &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}, nil},
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.8\"/\"vlan10\"", "", nil, &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.8\",interface=\"vlan10\""}},
		}, nil
	case "routing.Config.VRFs[default].Routes[0].Interface":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan10\"", "", &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}, nil},
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan11\"", "", nil, &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan11\""}},
		}, nil
	case "routing.Config.VRFs[default].Routes[0].Preference":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan10\"", "preference", &[2]string{"routing.route.preference", "1"}, &[2]string{"routing.route.preference", "2"}},
		}, nil
	case "routing.Config.VRFs[default].Routes[0].Metric":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan10\"", "metric", &[2]string{"routing.route.metric", "10"}, &[2]string{"routing.route.metric", "11"}},
		}, nil
	case "routing.Config.VRFs[default].Neighbors[0].Interface":
		*hits++
		return []changeRecord{
			{"routing", "neighbor", "\"default\"/\"vlan10\"/\"10.0.10.7\"", "", &[2]string{"routing.neighbor", "interface=\"vlan10\",addr=\"10.0.10.7\",mac=\"02:00:00:00:00:02\""}, nil},
			{"routing", "neighbor", "\"default\"/\"vlan11\"/\"10.0.10.7\"", "", nil, &[2]string{"routing.neighbor", "interface=\"vlan11\",addr=\"10.0.10.7\",mac=\"02:00:00:00:00:02\""}},
		}, nil
	case "routing.Config.VRFs[default].Neighbors[0].Addr":
		*hits++
		return []changeRecord{
			{"routing", "neighbor", "\"default\"/\"vlan10\"/\"10.0.10.7\"", "", &[2]string{"routing.neighbor", "interface=\"vlan10\",addr=\"10.0.10.7\",mac=\"02:00:00:00:00:02\""}, nil},
			{"routing", "neighbor", "\"default\"/\"vlan10\"/\"10.0.10.8\"", "", nil, &[2]string{"routing.neighbor", "interface=\"vlan10\",addr=\"10.0.10.8\",mac=\"02:00:00:00:00:02\""}},
		}, nil
	case "routing.Config.VRFs[default].Neighbors[0].MAC":
		*hits++
		return []changeRecord{
			{"routing", "neighbor", "\"default\"/\"vlan10\"/\"10.0.10.7\"", "mac", &[2]string{"routing.mac", "02:00:00:00:00:02"}, &[2]string{"routing.mac", "02:00:00:00:00:03"}},
		}, nil
	case "routing.Config.VRFs[default].NeighborPolicy.Mode":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "default", "mode", &[2]string{"routing.neighbor_policy.mode", "disabled"}, &[2]string{"routing.neighbor_policy.mode", ""}},
		}, nil
	case "routing.Config.VRFs[default].NeighborPolicy.ReachableTime":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "default", "reachable_time", &[2]string{"routing.neighbor_policy.reachable_time", "30s"}, &[2]string{"routing.neighbor_policy.reachable_time", "31s"}},
		}, nil
	case "routing.Config.VRFs[default].NeighborPolicy.RetransTimer":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "default", "retrans_timer", &[2]string{"routing.neighbor_policy.retrans_timer", "3s"}, &[2]string{"routing.neighbor_policy.retrans_timer", "4s"}},
		}, nil
	case "routing.Config.VRFs[default].NeighborPolicy.HoldDepth":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "default", "hold_depth", &[2]string{"routing.neighbor_policy.hold_depth", "4"}, &[2]string{"routing.neighbor_policy.hold_depth", "5"}},
		}, nil
	case "routing.Config.VRFs[default].@key":
		*hits++
		return []changeRecord{
			{"routing", "vrf", "default", "", &[2]string{"routing.vrf", "interfaces={\"vlan10\":vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]};routes=[{prefix=\"10.0.20.0/24\";preference=1;metric=10;next_hop=\"10.0.10.7\";interface=\"vlan10\"}];neighbors=[{interface=\"vlan10\";addr=\"10.0.10.7\";mac=\"02:00:00:00:00:02\"}];neighbor_policy={mode=\"disabled\";reachable_time=30s;retrans_timer=3s;hold_depth=4}"}, nil},
			{"routing", "vrf", "default2", "", nil, &[2]string{"routing.vrf", "interfaces={\"vlan10\":vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]};routes=[{prefix=\"10.0.20.0/24\";preference=1;metric=10;next_hop=\"10.0.10.7\";interface=\"vlan10\"}];neighbors=[{interface=\"vlan10\";addr=\"10.0.10.7\";mac=\"02:00:00:00:00:02\"}];neighbor_policy={mode=\"disabled\";reachable_time=30s;retrans_timer=3s;hold_depth=4}"}},
		}, nil
	case "routing.Config.VRFs[default].Interfaces[vlan10].@key":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"default\"/\"vlan10\"", "", &[2]string{"routing.interface", "vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]"}, nil},
			{"routing", "interface", "\"default\"/\"vlan11\"", "", nil, &[2]string{"routing.interface", "vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
