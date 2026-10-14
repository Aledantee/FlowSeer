package simtest

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func stpDiffMapping() (diffMapping, error) {
	return newDiffMapping("stp", stp.Config{}, `{"Priority":0,"PriorityPresent":true,"Address":"02:00:00:00:00:01","HelloTime":"2s","MaxAge":"20s","ForwardDelay":"15s","TxHoldCount":6,"Ports":{"1/1/1":{"Priority":0,"PriorityPresent":true,"PathCost":20000,"AdminEdge":true,"AutoEdge":true,"PointToPoint":"ForceTrue","BPDUGuard":true,"RestrictedRole":true,"RestrictedTCN":true,"LoopGuard":true}},"MST":{"Name":"region1","Revision":1,"MaxHops":20,"Instances":{"1":{"Priority":0,"PriorityPresent":true,"VLANs":[10],"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}},"PVST":{"Trees":{"10":{"Priority":8192,"PriorityPresent":true,"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}}}`, func(c stp.Config) stp.Config { return c.Normalize(layer.Env{}) }, stp.Diff, []mappedEntry{
		{Path: "stp.Config.Priority", Producer: "stp", Setup: "", Before: `0`, After: `4096`},
		{Path: "stp.Config.Address", Producer: "stp", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
		{Path: "stp.Config.HelloTime", Producer: "stp", Setup: "", Before: `"2s"`, After: `"3s"`},
		{Path: "stp.Config.MaxAge", Producer: "stp", Setup: "", Before: `"20s"`, After: `"21s"`},
		{Path: "stp.Config.ForwardDelay", Producer: "stp", Setup: "", Before: `"15s"`, After: `"16s"`},
		{Path: "stp.Config.TxHoldCount", Producer: "stp", Setup: "", Before: `6`, After: `7`},
		{Path: "stp.Config.PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.Ports[1/1/1].Priority", Producer: "stp", Setup: "", Before: `0`, After: `16`},
		{Path: "stp.Config.Ports[1/1/1].PathCost", Producer: "stp", Setup: "", Before: `20000`, After: `20001`},
		{Path: "stp.Config.Ports[1/1/1].AdminEdge", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.Ports[1/1/1].AutoEdge", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.Ports[1/1/1].PointToPoint", Producer: "stp", Setup: "", Before: `"ForceTrue"`, After: `"ForceFalse"`},
		{Path: "stp.Config.Ports[1/1/1].BPDUGuard", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.Ports[1/1/1].RestrictedRole", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.Ports[1/1/1].RestrictedTCN", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.Ports[1/1/1].LoopGuard", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.Ports[1/1/1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.MST.Name", Producer: "stp", Setup: "", Before: `"region1"`, After: `"region2"`},
		{Path: "stp.Config.MST.Revision", Producer: "stp", Setup: "", Before: `1`, After: `2`},
		{Path: "stp.Config.MST.MaxHops", Producer: "stp", Setup: "", Before: `20`, After: `21`},
		{Path: "stp.Config.MST.Instances[1].Priority", Producer: "stp", Setup: "", Before: `0`, After: `4096`},
		{Path: "stp.Config.MST.Instances[1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.MST.Instances[1].VLANs[0]", Producer: "stp", Setup: "", Before: `10`, After: `11`},
		{Path: "stp.Config.MST.Instances[1].Ports[1/1/1].Priority", Producer: "stp", Setup: "", Before: `128`, After: `129`},
		{Path: "stp.Config.MST.Instances[1].Ports[1/1/1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.MST.Instances[1].Ports[1/1/1].PathCost", Producer: "stp", Setup: "", Before: `20000`, After: `20001`},
		{Path: "stp.Config.PVST.Trees[10].Ports[1/1/1].Priority", Producer: "stp", Setup: "", Before: `128`, After: `129`},
		{Path: "stp.Config.PVST.Trees[10].Ports[1/1/1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.PVST.Trees[10].Ports[1/1/1].PathCost", Producer: "stp", Setup: "", Before: `20000`, After: `20001`},
		{Path: "stp.Config.PVST.Trees[10].Priority", Producer: "stp", Setup: "", Before: `8192`, After: `12288`},
		{Path: "stp.Config.PVST.Trees[10].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "stp.Config.Ports[1/1/1].@key", Producer: "stp", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "stp.Config.MST.@present", Producer: "stp", Setup: "", Before: `{"Name":"region1","Revision":1,"MaxHops":20,"Instances":{"1":{"Priority":0,"PriorityPresent":true,"VLANs":[10],"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}}`, After: `null`},
		{Path: "stp.Config.PVST.@present", Producer: "stp", Setup: "", Before: `{"Trees":{"10":{"Priority":8192,"PriorityPresent":true,"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}}`, After: `null`},
		{Path: "stp.Config.MST.Instances[1].@key", Producer: "stp", Setup: "", Before: `1`, After: `2`},
		{Path: "stp.Config.MST.Instances[1].Ports[1/1/1].@key", Producer: "stp", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "stp.Config.PVST.Trees[10].@key", Producer: "stp", Setup: "", Before: `10`, After: `11`},
		{Path: "stp.Config.PVST.Trees[10].Ports[1/1/1].@key", Producer: "stp", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
	}, stpDiffExpectation)
}

func stpDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "stp.Config.Priority", "stp.Config.Address", "stp.Config.HelloTime", "stp.Config.MaxAge", "stp.Config.ForwardDelay", "stp.Config.TxHoldCount", "stp.Config.PriorityPresent":
		return stpBridgeExpectation(path, hits)
	case "stp.Config.Ports[1/1/1].Priority", "stp.Config.Ports[1/1/1].PathCost", "stp.Config.Ports[1/1/1].AdminEdge", "stp.Config.Ports[1/1/1].AutoEdge", "stp.Config.Ports[1/1/1].PointToPoint", "stp.Config.Ports[1/1/1].BPDUGuard", "stp.Config.Ports[1/1/1].RestrictedRole", "stp.Config.Ports[1/1/1].RestrictedTCN", "stp.Config.Ports[1/1/1].LoopGuard", "stp.Config.Ports[1/1/1].PriorityPresent", "stp.Config.Ports[1/1/1].@key":
		return stpPortsExpectation(path, hits)
	case "stp.Config.MST.Name", "stp.Config.MST.Revision", "stp.Config.MST.MaxHops", "stp.Config.MST.Instances[1].Priority", "stp.Config.MST.Instances[1].PriorityPresent", "stp.Config.MST.Instances[1].VLANs[0]", "stp.Config.MST.Instances[1].Ports[1/1/1].Priority", "stp.Config.MST.Instances[1].Ports[1/1/1].PriorityPresent", "stp.Config.MST.Instances[1].Ports[1/1/1].PathCost", "stp.Config.MST.@present", "stp.Config.MST.Instances[1].@key", "stp.Config.MST.Instances[1].Ports[1/1/1].@key":
		return stpMSTExpectation(path, hits)
	case "stp.Config.PVST.Trees[10].Ports[1/1/1].Priority", "stp.Config.PVST.Trees[10].Ports[1/1/1].PriorityPresent", "stp.Config.PVST.Trees[10].Ports[1/1/1].PathCost", "stp.Config.PVST.Trees[10].Priority", "stp.Config.PVST.Trees[10].PriorityPresent", "stp.Config.PVST.@present", "stp.Config.PVST.Trees[10].@key", "stp.Config.PVST.Trees[10].Ports[1/1/1].@key":
		return stpPVSTExpectation(path, hits)
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func stpBridgeExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "stp.Config.Priority":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "4096"}},
			{"stp", "pvst_tree", "1", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "4096"}},
		}, nil
	case "stp.Config.Address":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "address", &[2]string{"stp.mac", "02:00:00:00:00:01"}, &[2]string{"stp.mac", "02:00:00:00:00:02"}},
		}, nil
	case "stp.Config.HelloTime":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "hello_time", &[2]string{"stp.duration", "2s"}, &[2]string{"stp.duration", "3s"}},
		}, nil
	case "stp.Config.MaxAge":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "max_age", &[2]string{"stp.duration", "20s"}, &[2]string{"stp.duration", "21s"}},
		}, nil
	case "stp.Config.ForwardDelay":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "forward_delay", &[2]string{"stp.duration", "15s"}, &[2]string{"stp.duration", "16s"}},
		}, nil
	case "stp.Config.TxHoldCount":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "tx_hold_count", &[2]string{"stp.tx_hold_count", "6"}, &[2]string{"stp.tx_hold_count", "7"}},
		}, nil
	case "stp.Config.PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "32768"}},
			{"stp", "pvst_tree", "1", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "32768"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func stpPortsExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "stp.Config.Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "priority", &[2]string{"stp.port_priority", "0"}, &[2]string{"stp.port_priority", "16"}},
		}, nil
	case "stp.Config.Ports[1/1/1].PathCost":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "admin_path_cost", &[2]string{"stp.path_cost", "20000"}, &[2]string{"stp.path_cost", "20001"}},
		}, nil
	case "stp.Config.Ports[1/1/1].AdminEdge":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "admin_edge", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "stp.Config.Ports[1/1/1].AutoEdge":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "auto_edge", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "stp.Config.Ports[1/1/1].PointToPoint":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "admin_point_to_point", &[2]string{"stp.point_to_point", "ForceTrue"}, &[2]string{"stp.point_to_point", "ForceFalse"}},
		}, nil
	case "stp.Config.Ports[1/1/1].BPDUGuard":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "bpdu_guard", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "stp.Config.Ports[1/1/1].RestrictedRole":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "restricted_role", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "stp.Config.Ports[1/1/1].RestrictedTCN":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "restricted_tcn", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "stp.Config.Ports[1/1/1].LoopGuard":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "loop_guard", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "stp.Config.Ports[1/1/1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "priority", &[2]string{"stp.port_priority", "0"}, &[2]string{"stp.port_priority", "128"}},
		}, nil
	case "stp.Config.Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "", &[2]string{"stp.port", "priority=0,path_cost=20000,admin_edge=true,auto_edge=true,point_to_point=\"ForceTrue\",bpdu_guard=true,restricted_role=true,restricted_tcn=true,loop_guard=true"}, nil},
			{"stp", "port", "1/1/2", "", nil, &[2]string{"stp.port", "priority=0,path_cost=20000,admin_edge=true,auto_edge=true,point_to_point=\"ForceTrue\",bpdu_guard=true,restricted_role=true,restricted_tcn=true,loop_guard=true"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func stpMSTExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "stp.Config.MST.Name":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "mst.name", &[2]string{"stp.mst.name", "region1"}, &[2]string{"stp.mst.name", "region2"}},
		}, nil
	case "stp.Config.MST.Revision":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "mst.revision", &[2]string{"stp.mst.revision", "1"}, &[2]string{"stp.mst.revision", "2"}},
		}, nil
	case "stp.Config.MST.MaxHops":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "mst.max_hops", &[2]string{"stp.mst.max_hops", "20"}, &[2]string{"stp.mst.max_hops", "21"}},
		}, nil
	case "stp.Config.MST.Instances[1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "1", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "4096"}},
		}, nil
	case "stp.Config.MST.Instances[1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "1", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "32768"}},
		}, nil
	case "stp.Config.MST.Instances[1].VLANs[0]":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "1", "vlans[\"10\"]", &[2]string{"stp.mst.vlan_id", "10"}, nil},
			{"stp", "mst_instance", "1", "vlans[\"11\"]", nil, &[2]string{"stp.mst.vlan_id", "11"}},
		}, nil
	case "stp.Config.MST.Instances[1].Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"1\"/\"1/1/1\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "129"}},
		}, nil
	case "stp.Config.MST.Instances[1].Ports[1/1/1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"1\"/\"1/1/1\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "0"}},
		}, nil
	case "stp.Config.MST.Instances[1].Ports[1/1/1].PathCost":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"1\"/\"1/1/1\"", "path_cost", &[2]string{"stp.path_cost", "20000"}, &[2]string{"stp.path_cost", "20001"}},
		}, nil
	case "stp.Config.MST.@present":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "mst.presence", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "stp.Config.MST.Instances[1].@key":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "1", "", &[2]string{"stp.mst.instance", "priority=0,vlans=[10],ports=[1/1/1:{priority=128,path_cost=20000}]"}, nil},
			{"stp", "mst_instance", "2", "", nil, &[2]string{"stp.mst.instance", "priority=0,vlans=[10],ports=[1/1/1:{priority=128,path_cost=20000}]"}},
		}, nil
	case "stp.Config.MST.Instances[1].Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"1\"/\"1/1/1\"", "", &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}, nil},
			{"stp", "mst_instance_port", "\"1\"/\"1/1/2\"", "", nil, &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func stpPVSTExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "stp.Config.PVST.Trees[10].Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"10\"/\"1/1/1\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "129"}},
		}, nil
	case "stp.Config.PVST.Trees[10].Ports[1/1/1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"10\"/\"1/1/1\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "0"}},
		}, nil
	case "stp.Config.PVST.Trees[10].Ports[1/1/1].PathCost":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"10\"/\"1/1/1\"", "path_cost", &[2]string{"stp.path_cost", "20000"}, &[2]string{"stp.path_cost", "20001"}},
		}, nil
	case "stp.Config.PVST.Trees[10].Priority":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree", "10", "priority", &[2]string{"stp.priority", "8192"}, &[2]string{"stp.priority", "12288"}},
		}, nil
	case "stp.Config.PVST.Trees[10].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree", "10", "priority", &[2]string{"stp.priority", "8192"}, &[2]string{"stp.priority", "0"}},
		}, nil
	case "stp.Config.PVST.@present":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "pvst.presence", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "stp.Config.PVST.Trees[10].@key":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree", "10", "", &[2]string{"stp.pvst.tree", "priority=8192,ports=[1/1/1:{priority=128,path_cost=20000}]"}, nil},
			{"stp", "pvst_tree", "11", "", nil, &[2]string{"stp.pvst.tree", "priority=8192,ports=[1/1/1:{priority=128,path_cost=20000}]"}},
		}, nil
	case "stp.Config.PVST.Trees[10].Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"10\"/\"1/1/1\"", "", &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}, nil},
			{"stp", "pvst_tree_port", "\"10\"/\"1/1/2\"", "", nil, &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
