package simtest

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
)

// BridgeDiffAllowance returns no exceptions for this exact producer.
func BridgeDiffAllowance() DiffAllowance {
	return DiffAllowance{}
}

func bridgeDiffMapping() (diffMapping, error) {
	return newDiffMapping("bridge", bridge.Config{}, `{"AgingTime":"300s","MaxEntries":1024,"FloodVLANs":[10],"ProtectedPorts":["1/1/1"],"ForwardBPDU":true,"VLAN":{"Table":{"10":"ten"},"Switchports":{"1/1/1":{"PVID":10,"Tagged":[20],"Untagged":[10],"IngressFiltering":true,"Admission":"TaggedOnly","Tunnel":{"VID":30,"CustomerVIDs":[100],"TPID":34984},"PriorityTags":"IfNonzero"}}}}`, func(c bridge.Config) bridge.Config { return c.Normalize(layer.Env{}) }, bridge.Diff, []mappedEntry{
		{Path: "bridge.Config.AgingTime", Producer: "bridge", AllowancePath: "bridge.Config.AgingTime", Setup: "", Before: `"300s"`, After: `"301s"`},
		{Path: "bridge.Config.MaxEntries", Producer: "bridge", AllowancePath: "bridge.Config.MaxEntries", Setup: "", Before: `1024`, After: `1025`},
		{Path: "bridge.Config.FloodVLANs[0]", Producer: "bridge", AllowancePath: "bridge.Config.FloodVLANs[0]", Setup: "", Before: `10`, After: `11`},
		{Path: "bridge.Config.ProtectedPorts[0]", Producer: "bridge", AllowancePath: "bridge.Config.ProtectedPorts[0]", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "bridge.Config.ForwardBPDU", Producer: "bridge", AllowancePath: "bridge.Config.ForwardBPDU", Setup: "", Before: `true`, After: `false`},
		{Path: "bridge.Config.VLAN.Table[10]", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Table[10]", Setup: "", Before: `"ten"`, After: `"eleven"`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].PVID", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].PVID", Setup: "", Before: `10`, After: `11`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].Tagged[0]", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].Tagged[0]", Setup: "", Before: `20`, After: `21`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].Untagged[0]", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].Untagged[0]", Setup: "", Before: `10`, After: `11`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].IngressFiltering", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].IngressFiltering", Setup: "", Before: `true`, After: `false`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].Admission", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].Admission", Setup: "", Before: `"TaggedOnly"`, After: `"All"`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].PriorityTags", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].PriorityTags", Setup: "", Before: `"IfNonzero"`, After: `"Never"`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.VID", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.VID", Setup: "", Before: `30`, After: `31`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.CustomerVIDs[0]", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.CustomerVIDs[0]", Setup: "", Before: `100`, After: `101`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.TPID", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.TPID", Setup: "", Before: `0`, After: `34984`, Reason: "TPID zero and 34984 have the same effective value"},
		{Path: "bridge.Config.VLAN.@present", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.@present", Setup: "", Before: `{"Table":{"10":"ten"},"Switchports":{"1/1/1":{"PVID":10,"Tagged":[20],"Untagged":[10],"IngressFiltering":true,"Admission":"TaggedOnly","Tunnel":{"VID":30,"CustomerVIDs":[100],"TPID":34984},"PriorityTags":"IfNonzero"}}}`, After: `null`},
		{Path: "bridge.Config.VLAN.Table[10].@key", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Table[10].@key", Setup: "", Before: `10`, After: `11`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].@key", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].@key", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].PVID.@present", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].PVID.@present", Setup: "", Before: `10`, After: `null`},
		{Path: "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.@present", Producer: "bridge", AllowancePath: "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.@present", Setup: "", Before: `{"VID":30,"CustomerVIDs":[100],"TPID":34984}`, After: `null`},
	}, bridgeDiffExpectation)
}

func bridgeDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "bridge.Config.AgingTime":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "", "aging_time", &[2]string{"bridge.duration", "5m0s"}, &[2]string{"bridge.duration", "5m1s"}},
		}, nil
	case "bridge.Config.MaxEntries":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "", "max_entries", &[2]string{"bridge.int", "1024"}, &[2]string{"bridge.int", "1025"}},
		}, nil
	case "bridge.Config.FloodVLANs[0]":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "", "flood_vlans[\"10\"]", &[2]string{"bridge.vlan_id", "10"}, nil},
			{"relay", "bridge", "", "flood_vlans[\"11\"]", nil, &[2]string{"bridge.vlan_id", "11"}},
		}, nil
	case "bridge.Config.ProtectedPorts[0]":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "", "protected_ports[\"1/1/1\"]", &[2]string{"bridge.port", "1/1/1"}, nil},
			{"relay", "bridge", "", "protected_ports[\"1/1/2\"]", nil, &[2]string{"bridge.port", "1/1/2"}},
		}, nil
	case "bridge.Config.ForwardBPDU":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "", "forward_bpdu", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
		}, nil
	case "bridge.Config.VLAN.Table[10]":
		*hits++
		return []changeRecord{
			{"vlan", "vlan", "10", "name", &[2]string{"bridge.vlan_name", "ten"}, &[2]string{"bridge.vlan_name", "eleven"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].PVID":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "pvid", &[2]string{"bridge.pvid", "10"}, &[2]string{"bridge.pvid", "11"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].Tagged[0]":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "tagged_vlan_ids[\"20\"]", &[2]string{"bridge.vlan_id", "20"}, nil},
			{"vlan", "port", "1/1/1", "tagged_vlan_ids[\"21\"]", nil, &[2]string{"bridge.vlan_id", "21"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].Untagged[0]":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "untagged_vlan_ids[\"10\"]", &[2]string{"bridge.vlan_id", "10"}, nil},
			{"vlan", "port", "1/1/1", "untagged_vlan_ids[\"11\"]", nil, &[2]string{"bridge.vlan_id", "11"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].IngressFiltering":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "ingress_filtering", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].Admission":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "frame_admission", &[2]string{"bridge.admission", "TaggedOnly"}, &[2]string{"bridge.admission", "All"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].PriorityTags":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "priority_tags", &[2]string{"bridge.priority_tags", "IfNonzero"}, &[2]string{"bridge.priority_tags", "Never"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.VID":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "tunnel.vid", &[2]string{"bridge.vlan_id", "30"}, &[2]string{"bridge.vlan_id", "31"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.CustomerVIDs[0]":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "tunnel.customer_vids[\"100\"]", &[2]string{"bridge.vlan_id", "100"}, nil},
			{"vlan", "port", "1/1/1", "tunnel.customer_vids[\"101\"]", nil, &[2]string{"bridge.vlan_id", "101"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.TPID":
		*hits++
		return []changeRecord{}, nil
	case "bridge.Config.VLAN.@present":
		*hits++
		return []changeRecord{
			{"vlan", "bridge", "", "vlan.presence", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
			{"vlan", "vlan", "10", "", &[2]string{"bridge.vlan_name", "ten"}, nil},
			{"vlan", "port", "1/1/1", "", &[2]string{"bridge.switchport", "pvid=10;tagged=[20];untagged=[10];ingress_filtering=true;admission=\"TaggedOnly\";priority_tags=\"IfNonzero\";tunnel={vid=30;tpid=34984;customer_vids=[100]}"}, nil},
		}, nil
	case "bridge.Config.VLAN.Table[10].@key":
		*hits++
		return []changeRecord{
			{"vlan", "vlan", "10", "", &[2]string{"bridge.vlan_name", "ten"}, nil},
			{"vlan", "vlan", "11", "", nil, &[2]string{"bridge.vlan_name", "ten"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "", &[2]string{"bridge.switchport", "pvid=10;tagged=[20];untagged=[10];ingress_filtering=true;admission=\"TaggedOnly\";priority_tags=\"IfNonzero\";tunnel={vid=30;tpid=34984;customer_vids=[100]}"}, nil},
			{"vlan", "port", "1/1/2", "", nil, &[2]string{"bridge.switchport", "pvid=10;tagged=[20];untagged=[10];ingress_filtering=true;admission=\"TaggedOnly\";priority_tags=\"IfNonzero\";tunnel={vid=30;tpid=34984;customer_vids=[100]}"}},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].PVID.@present":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "pvid", &[2]string{"bridge.pvid", "10"}, nil},
		}, nil
	case "bridge.Config.VLAN.Switchports[1/1/1].Tunnel.@present":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "tunnel.presence", &[2]string{"bridge.tunnel", "vid=30;tpid=34984;customer_vids=[100]"}, nil},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
