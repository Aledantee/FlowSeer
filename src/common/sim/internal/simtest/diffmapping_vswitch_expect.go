package simtest

import "go.aledante.io/FlowSeer/src/common/errs"

func vswitchDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.Bridge.AgingTime", "vswitch.Config.Bridge.MaxEntries", "vswitch.Config.Bridge.FloodVLANs[0]", "vswitch.Config.Bridge.ProtectedPorts[0]", "vswitch.Config.Bridge.ForwardBPDU":
		return vswitchBridgeRelayExpectation(path, hits)
	case "vswitch.Config.Bridge.VLAN.Table[10]", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].PVID", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tagged[0]", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Untagged[0]", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].IngressFiltering", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Admission", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].PriorityTags", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.VID", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.CustomerVIDs[0]", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.TPID", "vswitch.Config.Bridge.VLAN.@present", "vswitch.Config.Bridge.VLAN.Table[10].@key", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].@key", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].PVID.@present", "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.@present":
		return vswitchBridgeVLANExpectation(path, hits)
	case "vswitch.Config.Phy.Ethernet[1/1/1].Setting.SpeedBPS", "vswitch.Config.Phy.Ethernet[1/1/1].Setting.Duplex", "vswitch.Config.Phy.Ethernet[1/1/1].Setting.AutoNegotiation", "vswitch.Config.Phy.Ethernet[1/1/1].AutoNegotiationSupported", "vswitch.Config.Phy.Ethernet[1/1/1].Observed.SpeedBPS", "vswitch.Config.Phy.Ethernet[1/1/1].Observed.Duplex", "vswitch.Config.Phy.Ethernet[1/1/1].SupportedSpeedsBPS[0]", "vswitch.Config.Phy.Ethernet[1/1/1].@key", "vswitch.Config.Phy.Ethernet[1/1/1].Setting.@present", "vswitch.Config.Phy.Ethernet[1/1/1].Observed.@present":
		return vswitchPhyEthernetExpectation(path, hits)
	case "vswitch.Config.Phy.PoE.Groups[g1].PowerNanowatts", "vswitch.Config.Phy.PoE.Ports[1/1/1].Group", "vswitch.Config.Phy.PoE.Ports[1/1/1].MaxClass", "vswitch.Config.Phy.PoE.Ports[1/1/1].Enabled", "vswitch.Config.Phy.PoE.Ports[1/1/1].Limit", "vswitch.Config.Phy.PoE.Ports[1/1/1].Priority", "vswitch.Config.Phy.PoE.Ports[1/1/1].PD", "vswitch.Config.Phy.PoE.Ports[1/1/1].PDClass", "vswitch.Config.Phy.PoE.@present", "vswitch.Config.Phy.PoE.Groups[g1].@key", "vswitch.Config.Phy.PoE.Ports[1/1/1].@key", "vswitch.Config.Phy.PoE.Ports[1/1/1].Limit.@present", "vswitch.Config.Phy.PoE.Ports[1/1/1].PDClass.@present":
		return vswitchPhyPoEExpectation(path, hits)
	case "vswitch.Config.LoopProtect.Interval", "vswitch.Config.LoopProtect.Ports[1/1/1].Action", "vswitch.Config.LoopProtect.Ports[1/1/1].Recovery.Mode", "vswitch.Config.LoopProtect.Ports[1/1/1].Recovery.Duration", "vswitch.Config.LoopProtect.Ports[1/1/1].VLANs[0]", "vswitch.Config.LoopProtect.Ports[1/1/1].@key":
		return vswitchLoopProtectExpectation(path, hits)
	case "vswitch.Config.Mcast.VLANs[10].FloodUnregistered", "vswitch.Config.Mcast.VLANs[10].FastLeave", "vswitch.Config.Mcast.VLANs[10].MembershipInterval", "vswitch.Config.Mcast.VLANs[10].RouterPortInterval", "vswitch.Config.Mcast.VLANs[10].LastMemberQueryInterval", "vswitch.Config.Mcast.VLANs[10].LastMemberQueryCount", "vswitch.Config.Mcast.VLANs[10].RouterPorts[0]", "vswitch.Config.Mcast.VLANs[10].@key", "vswitch.Config.Mcast.VLANs[10].FloodUnregistered.@present":
		return vswitchMcastExpectation(path, hits)
	case "vswitch.Config.LAG.LAGs[lag1].Mode", "vswitch.Config.LAG.LAGs[lag1].Primary", "vswitch.Config.LAG.LAGs[lag1].UpDelay", "vswitch.Config.LAG.LAGs[lag1].DownDelay", "vswitch.Config.LAG.LAGs[lag1].HashBasis", "vswitch.Config.LAG.LAGs[lag1].MinLinks", "vswitch.Config.LAG.LAGs[lag1].RebalanceInterval", "vswitch.Config.LAG.LAGs[lag1].LACP.Mode", "vswitch.Config.LAG.LAGs[lag1].LACP.Fast", "vswitch.Config.LAG.LAGs[lag1].LACP.SystemPriority", "vswitch.Config.LAG.LAGs[lag1].LACP.SystemID", "vswitch.Config.LAG.LAGs[lag1].LACP.Key", "vswitch.Config.LAG.LAGs[lag1].LACP.Fallback", "vswitch.Config.LAG.LAGs[lag1].Members[1/1/1].Priority", "vswitch.Config.LAG.LAGs[lag1].Members[1/1/1].Key", "vswitch.Config.LAG.LAGs[lag1].@key", "vswitch.Config.LAG.LAGs[lag1].RebalanceInterval.@present", "vswitch.Config.LAG.LAGs[lag1].Members[1/1/1].@key":
		return vswitchLAGExpectation(path, hits)
	case "vswitch.Config.STP.Priority", "vswitch.Config.STP.Address", "vswitch.Config.STP.HelloTime", "vswitch.Config.STP.MaxAge", "vswitch.Config.STP.ForwardDelay", "vswitch.Config.STP.TxHoldCount", "vswitch.Config.STP.PriorityPresent":
		return vswitchSTPBridgeExpectation(path, hits)
	case "vswitch.Config.STP.Ports[1/1/1].Priority", "vswitch.Config.STP.Ports[1/1/1].PathCost", "vswitch.Config.STP.Ports[1/1/1].AdminEdge", "vswitch.Config.STP.Ports[1/1/1].AutoEdge", "vswitch.Config.STP.Ports[1/1/1].PointToPoint", "vswitch.Config.STP.Ports[1/1/1].BPDUGuard", "vswitch.Config.STP.Ports[1/1/1].RestrictedRole", "vswitch.Config.STP.Ports[1/1/1].RestrictedTCN", "vswitch.Config.STP.Ports[1/1/1].LoopGuard", "vswitch.Config.STP.Ports[1/1/1].PriorityPresent", "vswitch.Config.STP.Ports[1/1/1].@key":
		return vswitchSTPPortsExpectation(path, hits)
	case "vswitch.Config.STP.MST.Name", "vswitch.Config.STP.MST.Revision", "vswitch.Config.STP.MST.MaxHops", "vswitch.Config.STP.MST.Instances[1].Priority", "vswitch.Config.STP.MST.Instances[1].PriorityPresent", "vswitch.Config.STP.MST.Instances[1].VLANs[0]", "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].Priority", "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].PriorityPresent", "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].PathCost", "vswitch.Config.STP.MST.@present", "vswitch.Config.STP.MST.Instances[1].@key", "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].@key":
		return vswitchSTPMSTExpectation(path, hits)
	case "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].Priority", "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].PriorityPresent", "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].PathCost", "vswitch.Config.STP.PVST.Trees[10].Priority", "vswitch.Config.STP.PVST.Trees[10].PriorityPresent", "vswitch.Config.STP.PVST.@present", "vswitch.Config.STP.PVST.Trees[10].@key", "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].@key":
		return vswitchSTPPVSTExpectation(path, hits)
	case "vswitch.Config.Filter.Sets[ingress].Stateful", "vswitch.Config.Filter.Sets[ingress].Default", "vswitch.Config.Filter.Sets[ingress].Rules[0].Name", "vswitch.Config.Filter.Sets[ingress].Rules[0].Action", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Protocol", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Src[0]", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Dst[0]", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.SrcPorts[0].Start", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.SrcPorts[0].End", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.DstPorts[0].Start", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.DstPorts[0].End", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.Type", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.Code", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.Mask", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.Value", "vswitch.Config.Filter.Sets[ingress].@key", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Protocol.@present", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.@present", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.Code.@present", "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.@present":
		return vswitchFilterSetsExpectation(path, hits)
	case "vswitch.Config.Filter.Bindings[0].Interface", "vswitch.Config.Filter.Bindings[0].Direction", "vswitch.Config.Filter.Bindings[0].Set":
		return vswitchFilterBindingsExpectation(path, hits)
	case "vswitch.Config.Routing.ClearRouterFlag", "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].VLAN", "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].Port", "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].MAC", "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].Prefixes[0]", "vswitch.Config.Routing.VRFs[default].Routes[0].Prefix", "vswitch.Config.Routing.VRFs[default].Routes[0].NextHop", "vswitch.Config.Routing.VRFs[default].Routes[0].Interface", "vswitch.Config.Routing.VRFs[default].Routes[0].Preference", "vswitch.Config.Routing.VRFs[default].Routes[0].Metric", "vswitch.Config.Routing.VRFs[default].Neighbors[0].Interface", "vswitch.Config.Routing.VRFs[default].Neighbors[0].Addr", "vswitch.Config.Routing.VRFs[default].Neighbors[0].MAC", "vswitch.Config.Routing.VRFs[default].NeighborPolicy.Mode", "vswitch.Config.Routing.VRFs[default].NeighborPolicy.ReachableTime", "vswitch.Config.Routing.VRFs[default].NeighborPolicy.RetransTimer", "vswitch.Config.Routing.VRFs[default].NeighborPolicy.HoldDepth", "vswitch.Config.Routing.VRFs[default].@key", "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].@key":
		return vswitchRoutingExpectation(path, hits)
	case "vswitch.Config.Traffic.Mirrors[0].Name", "vswitch.Config.Traffic.Mirrors[0].SelectAll", "vswitch.Config.Traffic.Mirrors[0].OutputPort", "vswitch.Config.Traffic.Mirrors[0].OutputVLAN", "vswitch.Config.Traffic.Mirrors[0].SnapLen", "vswitch.Config.Traffic.Mirrors[0].SelectSrcPorts[0]", "vswitch.Config.Traffic.Mirrors[0].SelectDstPorts[0]", "vswitch.Config.Traffic.Mirrors[0].SelectVLANs[0]", "vswitch.Config.Traffic.Policers[1/1/1].RateBPS", "vswitch.Config.Traffic.Policers[1/1/1].BurstOctets", "vswitch.Config.Traffic.Queues[1/1/1].MaxRateBPS[0]", "vswitch.Config.Traffic.Queues[1/1/1].BufferOctets[0]", "vswitch.Config.Traffic.Mirrors[0].OutputVLAN.@present", "vswitch.Config.Traffic.Policers[1/1/1].@key", "vswitch.Config.Traffic.Queues[1/1/1].@key", "vswitch.Config.Traffic.Queues[1/1/1].MaxRateBPS[0].@key", "vswitch.Config.Traffic.Queues[1/1/1].BufferOctets[0].@key":
		return vswitchTrafficExpectation(path, hits)
	case "vswitch.Config.Ports.Ports()[1/1/1].Name", "vswitch.Config.Ports.Ports()[1/1/1].IfIndex", "vswitch.Config.Ports.Ports()[1/1/1].Kind", "vswitch.Config.Ports.Ports()[1/1/1].AdminStatus", "vswitch.Config.Ports.Ports()[1/1/1].OperStatus", "vswitch.Config.Ports.Ports()[1/1/1].MTU", "vswitch.Config.Ports.Ports()[1/1/1].LagParent":
		return vswitchPortsExpectation(path, hits)
	case "vswitch.Config.MAC", "vswitch.Config.Phy.@present", "vswitch.Config.Bridge.@present", "vswitch.Config.LAG.@present", "vswitch.Config.STP.@present", "vswitch.Config.LoopProtect.@present", "vswitch.Config.Mcast.@present", "vswitch.Config.Routing.@present", "vswitch.Config.Traffic.@present", "vswitch.Config.Filter.@present":
		return vswitchPresenceExpectation(path, hits)
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchBridgeRelayExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.Bridge.AgingTime":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "", "aging_time", &[2]string{"bridge.duration", "5m0s"}, &[2]string{"bridge.duration", "5m1s"}},
		}, nil
	case "vswitch.Config.Bridge.MaxEntries":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "", "max_entries", &[2]string{"bridge.int", "1024"}, &[2]string{"bridge.int", "1025"}},
		}, nil
	case "vswitch.Config.Bridge.FloodVLANs[0]":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "", "flood_vlans[\"10\"]", &[2]string{"bridge.vlan_id", "10"}, nil},
			{"relay", "bridge", "", "flood_vlans[\"11\"]", nil, &[2]string{"bridge.vlan_id", "11"}},
		}, nil
	case "vswitch.Config.Bridge.ProtectedPorts[0]":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "", "protected_ports[\"1/1/1\"]", &[2]string{"bridge.port", "1/1/1"}, nil},
			{"relay", "bridge", "", "protected_ports[\"1/1/2\"]", nil, &[2]string{"bridge.port", "1/1/2"}},
		}, nil
	case "vswitch.Config.Bridge.ForwardBPDU":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "", "forward_bpdu", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchBridgeVLANExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.Bridge.VLAN.Table[10]":
		*hits++
		return []changeRecord{
			{"vlan", "vlan", "10", "name", &[2]string{"bridge.vlan_name", "ten"}, &[2]string{"bridge.vlan_name", "eleven"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].PVID":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "pvid", &[2]string{"bridge.pvid", "10"}, &[2]string{"bridge.pvid", "11"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tagged[0]":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "tagged_vlan_ids[\"20\"]", &[2]string{"bridge.vlan_id", "20"}, nil},
			{"vlan", "port", "1/1/1", "tagged_vlan_ids[\"21\"]", nil, &[2]string{"bridge.vlan_id", "21"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Untagged[0]":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "untagged_vlan_ids[\"10\"]", &[2]string{"bridge.vlan_id", "10"}, nil},
			{"vlan", "port", "1/1/1", "untagged_vlan_ids[\"11\"]", nil, &[2]string{"bridge.vlan_id", "11"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].IngressFiltering":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "ingress_filtering", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Admission":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "frame_admission", &[2]string{"bridge.admission", "TaggedOnly"}, &[2]string{"bridge.admission", "All"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].PriorityTags":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "priority_tags", &[2]string{"bridge.priority_tags", "IfNonzero"}, &[2]string{"bridge.priority_tags", "Never"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.VID":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "tunnel.vid", &[2]string{"bridge.vlan_id", "30"}, &[2]string{"bridge.vlan_id", "31"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.CustomerVIDs[0]":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "tunnel.customer_vids[\"100\"]", &[2]string{"bridge.vlan_id", "100"}, nil},
			{"vlan", "port", "1/1/1", "tunnel.customer_vids[\"101\"]", nil, &[2]string{"bridge.vlan_id", "101"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.TPID":
		*hits++
		return []changeRecord{}, nil
	case "vswitch.Config.Bridge.VLAN.@present":
		*hits++
		return []changeRecord{
			{"vlan", "capability", "vlan", "presence", &[2]string{"vswitch.layer", "vlan"}, nil},
			{"vlan", "bridge", "", "vlan.presence", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
			{"vlan", "vlan", "10", "", &[2]string{"bridge.vlan_name", "ten"}, nil},
			{"vlan", "port", "1/1/1", "", &[2]string{"bridge.switchport", "pvid=10;tagged=[20];untagged=[10];ingress_filtering=true;admission=\"TaggedOnly\";priority_tags=\"IfNonzero\";tunnel={vid=30;tpid=34984;customer_vids=[100]}"}, nil},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Table[10].@key":
		*hits++
		return []changeRecord{
			{"vlan", "vlan", "10", "", &[2]string{"bridge.vlan_name", "ten"}, nil},
			{"vlan", "vlan", "11", "", nil, &[2]string{"bridge.vlan_name", "ten"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "", &[2]string{"bridge.switchport", "pvid=10;tagged=[20];untagged=[10];ingress_filtering=true;admission=\"TaggedOnly\";priority_tags=\"IfNonzero\";tunnel={vid=30;tpid=34984;customer_vids=[100]}"}, nil},
			{"vlan", "port", "1/1/2", "", nil, &[2]string{"bridge.switchport", "pvid=10;tagged=[20];untagged=[10];ingress_filtering=true;admission=\"TaggedOnly\";priority_tags=\"IfNonzero\";tunnel={vid=30;tpid=34984;customer_vids=[100]}"}},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].PVID.@present":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "pvid", &[2]string{"bridge.pvid", "10"}, nil},
		}, nil
	case "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.@present":
		*hits++
		return []changeRecord{
			{"vlan", "port", "1/1/1", "tunnel.presence", &[2]string{"bridge.tunnel", "vid=30;tpid=34984;customer_vids=[100]"}, nil},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchPhyEthernetExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.Phy.Ethernet[1/1/1].Setting.SpeedBPS":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "vswitch.Config.Phy.Ethernet[1/1/1].Setting.Duplex":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "vswitch.Config.Phy.Ethernet[1/1/1].Setting.AutoNegotiation":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "vswitch.Config.Phy.Ethernet[1/1/1].AutoNegotiationSupported":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "auto_negotiation_supported", &[2]string{"phy.capability", "Supported"}, &[2]string{"phy.capability", "Unsupported"}},
		}, nil
	case "vswitch.Config.Phy.Ethernet[1/1/1].Observed.SpeedBPS":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "vswitch.Config.Phy.Ethernet[1/1/1].Observed.Duplex":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "vswitch.Config.Phy.Ethernet[1/1/1].SupportedSpeedsBPS[0]":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "supported_speeds_bps[\"1000000000\"]", &[2]string{"phy.speed_bps", "1000000000"}, nil},
			{"ethernet", "port", "1/1/1", "supported_speeds_bps[\"10000000000\"]", nil, &[2]string{"phy.speed_bps", "10000000000"}},
		}, nil
	case "vswitch.Config.Phy.Ethernet[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "", &[2]string{"phy.ethernet", "speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\"Full\",autoneg=true},observed={speed=1000000000,duplex=\"Full\"}"}, nil},
			{"ethernet", "port", "1/1/2", "", nil, &[2]string{"phy.ethernet", "speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\"Full\",autoneg=true},observed={speed=1000000000,duplex=\"Full\"}"}},
		}, nil
	case "vswitch.Config.Phy.Ethernet[1/1/1].Setting.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"ethernet", "port", "1/1/1", "duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
			{"ethernet", "port", "1/1/1", "auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "vswitch.Config.Phy.Ethernet[1/1/1].Observed.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"ethernet", "port", "1/1/1", "observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchPhyPoEExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.Phy.PoE.Groups[g1].PowerNanowatts":
		*hits++
		return []changeRecord{
			{"poe", "pse_group", "g1", "power_nanowatts", &[2]string{"phy.power_nw", "370000000000"}, &[2]string{"phy.power_nw", "370000000001"}},
		}, nil
	case "vswitch.Config.Phy.PoE.Ports[1/1/1].Group":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "group", &[2]string{"phy.string", "g1"}, &[2]string{"phy.string", "g2"}},
		}, nil
	case "vswitch.Config.Phy.PoE.Ports[1/1/1].MaxClass":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "max_class", &[2]string{"phy.class", "4"}, &[2]string{"phy.class", "5"}},
		}, nil
	case "vswitch.Config.Phy.PoE.Ports[1/1/1].Enabled":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "vswitch.Config.Phy.PoE.Ports[1/1/1].Limit":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "power_limit_nanowatts", &[2]string{"phy.power_nw", "5000000000"}, &[2]string{"phy.power_nw", "5000000001"}},
		}, nil
	case "vswitch.Config.Phy.PoE.Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "priority", &[2]string{"phy.priority", "High"}, &[2]string{"phy.priority", "Low"}},
		}, nil
	case "vswitch.Config.Phy.PoE.Ports[1/1/1].PD":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "pd", &[2]string{"phy.pd_state", "Attached"}, &[2]string{"phy.pd_state", "Absent"}},
		}, nil
	case "vswitch.Config.Phy.PoE.Ports[1/1/1].PDClass":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "pd_class", &[2]string{"phy.class", "4"}, &[2]string{"phy.class", "5"}},
		}, nil
	case "vswitch.Config.Phy.PoE.@present":
		*hits++
		return []changeRecord{
			{"poe", "capability", "poe", "presence", &[2]string{"vswitch.layer", "poe"}, nil},
			{"poe", "pse_group", "g1", "", &[2]string{"phy.pse_group", "370000000000"}, nil},
			{"poe", "port", "1/1/1", "", &[2]string{"phy.pse_port", "group=\"g1\",max_class=4,enabled=true,limit=5000000000,priority=\"High\",pd=Attached,pd_class=4"}, nil},
		}, nil
	case "vswitch.Config.Phy.PoE.Groups[g1].@key":
		*hits++
		return []changeRecord{
			{"poe", "pse_group", "g1", "", &[2]string{"phy.pse_group", "370000000000"}, nil},
			{"poe", "pse_group", "g2", "", nil, &[2]string{"phy.pse_group", "370000000000"}},
		}, nil
	case "vswitch.Config.Phy.PoE.Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "", &[2]string{"phy.pse_port", "group=\"g1\",max_class=4,enabled=true,limit=5000000000,priority=\"High\",pd=Attached,pd_class=4"}, nil},
			{"poe", "port", "1/1/2", "", nil, &[2]string{"phy.pse_port", "group=\"g1\",max_class=4,enabled=true,limit=5000000000,priority=\"High\",pd=Attached,pd_class=4"}},
		}, nil
	case "vswitch.Config.Phy.PoE.Ports[1/1/1].Limit.@present":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "power_limit_nanowatts", &[2]string{"phy.power_nw", "5000000000"}, nil},
		}, nil
	case "vswitch.Config.Phy.PoE.Ports[1/1/1].PDClass.@present":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "pd_class", &[2]string{"phy.class", "4"}, nil},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchLoopProtectExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.LoopProtect.Interval":
		*hits++
		return []changeRecord{
			{"loopprotect", "bridge", "", "interval", &[2]string{"loopprotect.duration", "5s"}, &[2]string{"loopprotect.duration", "6s"}},
		}, nil
	case "vswitch.Config.LoopProtect.Ports[1/1/1].Action":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "1/1/1", "action", &[2]string{"loopprotect.action", "Block"}, &[2]string{"loopprotect.action", "Disable"}},
		}, nil
	case "vswitch.Config.LoopProtect.Ports[1/1/1].Recovery.Mode":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "1/1/1", "recovery.mode", &[2]string{"loopprotect.recovery_mode", "Timer"}, &[2]string{"loopprotect.recovery_mode", "Manual"}},
		}, nil
	case "vswitch.Config.LoopProtect.Ports[1/1/1].Recovery.Duration":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "1/1/1", "recovery.duration", &[2]string{"loopprotect.duration", "30s"}, &[2]string{"loopprotect.duration", "31s"}},
		}, nil
	case "vswitch.Config.LoopProtect.Ports[1/1/1].VLANs[0]":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "1/1/1", "vlans[\"10\"]", &[2]string{"loopprotect.vlan_id", "10"}, nil},
			{"loopprotect", "port", "1/1/1", "vlans[\"11\"]", nil, &[2]string{"loopprotect.vlan_id", "11"}},
		}, nil
	case "vswitch.Config.LoopProtect.Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "1/1/1", "", &[2]string{"loopprotect.port", "action=Block,recovery_mode=Timer,recovery_duration=30s,vlans=10"}, nil},
			{"loopprotect", "port", "1/1/2", "", nil, &[2]string{"loopprotect.port", "action=Block,recovery_mode=Timer,recovery_duration=30s,vlans=10"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchMcastExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.Mcast.VLANs[10].FloodUnregistered":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "flood_unregistered", &[2]string{"mcast.bool", "true"}, &[2]string{"mcast.bool", "false"}},
		}, nil
	case "vswitch.Config.Mcast.VLANs[10].FastLeave":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "fast_leave", &[2]string{"mcast.bool", "true"}, &[2]string{"mcast.bool", "false"}},
		}, nil
	case "vswitch.Config.Mcast.VLANs[10].MembershipInterval":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "membership_interval", &[2]string{"mcast.duration", "4m20s"}, &[2]string{"mcast.duration", "4m21s"}},
		}, nil
	case "vswitch.Config.Mcast.VLANs[10].RouterPortInterval":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "router_port_interval", &[2]string{"mcast.duration", "4m20s"}, &[2]string{"mcast.duration", "4m21s"}},
		}, nil
	case "vswitch.Config.Mcast.VLANs[10].LastMemberQueryInterval":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "last_member_query_interval", &[2]string{"mcast.duration", "1s"}, &[2]string{"mcast.duration", "2s"}},
		}, nil
	case "vswitch.Config.Mcast.VLANs[10].LastMemberQueryCount":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "last_member_query_count", &[2]string{"mcast.int", "2"}, &[2]string{"mcast.int", "3"}},
		}, nil
	case "vswitch.Config.Mcast.VLANs[10].RouterPorts[0]":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "router_ports[\"1/1/1\"]", &[2]string{"mcast.router_port", "1/1/1"}, nil},
			{"mcast", "vlan", "10", "router_ports[\"1/1/2\"]", nil, &[2]string{"mcast.router_port", "1/1/2"}},
		}, nil
	case "vswitch.Config.Mcast.VLANs[10].@key":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "10", "", &[2]string{"mcast.vlan_snooping", "flood=true,fast_leave=true,router_ports=[\"1/1/1\"],mem_int=4m20s,rtr_int=4m20s,lmqi=1s,lmqc=2"}, nil},
			{"mcast", "vlan", "11", "", nil, &[2]string{"mcast.vlan_snooping", "flood=true,fast_leave=true,router_ports=[\"1/1/1\"],mem_int=4m20s,rtr_int=4m20s,lmqi=1s,lmqc=2"}},
		}, nil
	case "vswitch.Config.Mcast.VLANs[10].FloodUnregistered.@present":
		*hits++
		return []changeRecord{}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchSTPBridgeExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.STP.Priority":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "4096"}},
			{"stp", "pvst_tree", "1", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "4096"}},
		}, nil
	case "vswitch.Config.STP.Address":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "address", &[2]string{"stp.mac", "02:00:00:00:00:01"}, &[2]string{"stp.mac", "02:00:00:00:00:02"}},
		}, nil
	case "vswitch.Config.STP.HelloTime":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "hello_time", &[2]string{"stp.duration", "2s"}, &[2]string{"stp.duration", "3s"}},
		}, nil
	case "vswitch.Config.STP.MaxAge":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "max_age", &[2]string{"stp.duration", "20s"}, &[2]string{"stp.duration", "21s"}},
		}, nil
	case "vswitch.Config.STP.ForwardDelay":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "forward_delay", &[2]string{"stp.duration", "15s"}, &[2]string{"stp.duration", "16s"}},
		}, nil
	case "vswitch.Config.STP.TxHoldCount":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "tx_hold_count", &[2]string{"stp.tx_hold_count", "6"}, &[2]string{"stp.tx_hold_count", "7"}},
		}, nil
	case "vswitch.Config.STP.PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "32768"}},
			{"stp", "pvst_tree", "1", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "32768"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchSTPPortsExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.STP.Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "priority", &[2]string{"stp.port_priority", "0"}, &[2]string{"stp.port_priority", "16"}},
		}, nil
	case "vswitch.Config.STP.Ports[1/1/1].PathCost":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "admin_path_cost", &[2]string{"stp.path_cost", "20000"}, &[2]string{"stp.path_cost", "20001"}},
		}, nil
	case "vswitch.Config.STP.Ports[1/1/1].AdminEdge":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "admin_edge", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "vswitch.Config.STP.Ports[1/1/1].AutoEdge":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "auto_edge", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "vswitch.Config.STP.Ports[1/1/1].PointToPoint":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "admin_point_to_point", &[2]string{"stp.point_to_point", "ForceTrue"}, &[2]string{"stp.point_to_point", "ForceFalse"}},
		}, nil
	case "vswitch.Config.STP.Ports[1/1/1].BPDUGuard":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "bpdu_guard", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "vswitch.Config.STP.Ports[1/1/1].RestrictedRole":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "restricted_role", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "vswitch.Config.STP.Ports[1/1/1].RestrictedTCN":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "restricted_tcn", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "vswitch.Config.STP.Ports[1/1/1].LoopGuard":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "loop_guard", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "vswitch.Config.STP.Ports[1/1/1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "priority", &[2]string{"stp.port_priority", "0"}, &[2]string{"stp.port_priority", "128"}},
		}, nil
	case "vswitch.Config.STP.Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "", &[2]string{"stp.port", "priority=0,path_cost=20000,admin_edge=true,auto_edge=true,point_to_point=\"ForceTrue\",bpdu_guard=true,restricted_role=true,restricted_tcn=true,loop_guard=true"}, nil},
			{"stp", "port", "1/1/2", "", nil, &[2]string{"stp.port", "priority=0,path_cost=20000,admin_edge=true,auto_edge=true,point_to_point=\"ForceTrue\",bpdu_guard=true,restricted_role=true,restricted_tcn=true,loop_guard=true"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchSTPPVSTExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"10\"/\"1/1/1\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "129"}},
		}, nil
	case "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"10\"/\"1/1/1\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "0"}},
		}, nil
	case "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].PathCost":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"10\"/\"1/1/1\"", "path_cost", &[2]string{"stp.path_cost", "20000"}, &[2]string{"stp.path_cost", "20001"}},
		}, nil
	case "vswitch.Config.STP.PVST.Trees[10].Priority":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree", "10", "priority", &[2]string{"stp.priority", "8192"}, &[2]string{"stp.priority", "12288"}},
		}, nil
	case "vswitch.Config.STP.PVST.Trees[10].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree", "10", "priority", &[2]string{"stp.priority", "8192"}, &[2]string{"stp.priority", "0"}},
		}, nil
	case "vswitch.Config.STP.PVST.@present":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "pvst.presence", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "vswitch.Config.STP.PVST.Trees[10].@key":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree", "10", "", &[2]string{"stp.pvst.tree", "priority=8192,ports=[1/1/1:{priority=128,path_cost=20000}]"}, nil},
			{"stp", "pvst_tree", "11", "", nil, &[2]string{"stp.pvst.tree", "priority=8192,ports=[1/1/1:{priority=128,path_cost=20000}]"}},
		}, nil
	case "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"10\"/\"1/1/1\"", "", &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}, nil},
			{"stp", "pvst_tree_port", "\"10\"/\"1/1/2\"", "", nil, &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchFilterBindingsExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.Filter.Bindings[0].Interface":
		*hits++
		return []changeRecord{
			{"filter", "binding", "\"vlan10\"/\"in\"", "", &[2]string{"filter.binding", "set=\"ingress\""}, nil},
			{"filter", "binding", "\"vlan11\"/\"in\"", "", nil, &[2]string{"filter.binding", "set=\"ingress\""}},
		}, nil
	case "vswitch.Config.Filter.Bindings[0].Direction":
		*hits++
		return []changeRecord{
			{"filter", "binding", "\"vlan10\"/\"in\"", "", &[2]string{"filter.binding", "set=\"ingress\""}, nil},
			{"filter", "binding", "\"vlan10\"/\"out\"", "", nil, &[2]string{"filter.binding", "set=\"ingress\""}},
		}, nil
	case "vswitch.Config.Filter.Bindings[0].Set":
		*hits++
		return []changeRecord{
			{"filter", "binding", "\"vlan10\"/\"in\"", "set", &[2]string{"filter.binding", "set=\"ingress\""}, &[2]string{"filter.binding", "set=\"ingress2\""}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchRoutingExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.Routing.ClearRouterFlag":
		*hits++
		return []changeRecord{
			{"routing", "routing", "", "clear_router_flag", &[2]string{"routing.clear_router_flag", "false"}, &[2]string{"routing.clear_router_flag", "true"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].VLAN":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"default\"/\"vlan10\"", "vlan", &[2]string{"routing.vlan", "10"}, &[2]string{"routing.vlan", "11"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].Port":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"default\"/\"vlan10\"", "port", &[2]string{"routing.port", "1/1/1"}, &[2]string{"routing.port", "1/1/2"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].MAC":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"default\"/\"vlan10\"", "mac", &[2]string{"routing.mac", "02:00:00:00:00:01"}, &[2]string{"routing.mac", "02:00:00:00:00:02"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].Prefixes[0]":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"default\"/\"vlan10\"", "prefixes[\"10.0.10.1/24\"]", &[2]string{"routing.prefix", "10.0.10.1/24"}, nil},
			{"routing", "interface", "\"default\"/\"vlan10\"", "prefixes[\"192.0.2.0/25\"]", nil, &[2]string{"routing.prefix", "192.0.2.0/25"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Routes[0].Prefix":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan10\"", "", &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}, nil},
			{"routing", "route", "\"default\"/\"192.0.2.0/25\"/\"10.0.10.7\"/\"vlan10\"", "", nil, &[2]string{"routing.route", "prefix=\"192.0.2.0/25\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Routes[0].NextHop":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan10\"", "", &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}, nil},
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.8\"/\"vlan10\"", "", nil, &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.8\",interface=\"vlan10\""}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Routes[0].Interface":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan10\"", "", &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}, nil},
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan11\"", "", nil, &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan11\""}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Routes[0].Preference":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan10\"", "preference", &[2]string{"routing.route.preference", "1"}, &[2]string{"routing.route.preference", "2"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Routes[0].Metric":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"default\"/\"10.0.20.0/24\"/\"10.0.10.7\"/\"vlan10\"", "metric", &[2]string{"routing.route.metric", "10"}, &[2]string{"routing.route.metric", "11"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Neighbors[0].Interface":
		*hits++
		return []changeRecord{
			{"routing", "neighbor", "\"default\"/\"vlan10\"/\"10.0.10.7\"", "", &[2]string{"routing.neighbor", "interface=\"vlan10\",addr=\"10.0.10.7\",mac=\"02:00:00:00:00:02\""}, nil},
			{"routing", "neighbor", "\"default\"/\"vlan11\"/\"10.0.10.7\"", "", nil, &[2]string{"routing.neighbor", "interface=\"vlan11\",addr=\"10.0.10.7\",mac=\"02:00:00:00:00:02\""}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Neighbors[0].Addr":
		*hits++
		return []changeRecord{
			{"routing", "neighbor", "\"default\"/\"vlan10\"/\"10.0.10.7\"", "", &[2]string{"routing.neighbor", "interface=\"vlan10\",addr=\"10.0.10.7\",mac=\"02:00:00:00:00:02\""}, nil},
			{"routing", "neighbor", "\"default\"/\"vlan10\"/\"10.0.10.8\"", "", nil, &[2]string{"routing.neighbor", "interface=\"vlan10\",addr=\"10.0.10.8\",mac=\"02:00:00:00:00:02\""}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Neighbors[0].MAC":
		*hits++
		return []changeRecord{
			{"routing", "neighbor", "\"default\"/\"vlan10\"/\"10.0.10.7\"", "mac", &[2]string{"routing.mac", "02:00:00:00:00:02"}, &[2]string{"routing.mac", "02:00:00:00:00:03"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].NeighborPolicy.Mode":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "default", "mode", &[2]string{"routing.neighbor_policy.mode", "disabled"}, &[2]string{"routing.neighbor_policy.mode", ""}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].NeighborPolicy.ReachableTime":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "default", "reachable_time", &[2]string{"routing.neighbor_policy.reachable_time", "30s"}, &[2]string{"routing.neighbor_policy.reachable_time", "31s"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].NeighborPolicy.RetransTimer":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "default", "retrans_timer", &[2]string{"routing.neighbor_policy.retrans_timer", "3s"}, &[2]string{"routing.neighbor_policy.retrans_timer", "4s"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].NeighborPolicy.HoldDepth":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "default", "hold_depth", &[2]string{"routing.neighbor_policy.hold_depth", "4"}, &[2]string{"routing.neighbor_policy.hold_depth", "5"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].@key":
		*hits++
		return []changeRecord{
			{"routing", "vrf", "default", "", &[2]string{"routing.vrf", "interfaces={\"vlan10\":vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]};routes=[{prefix=\"10.0.20.0/24\";preference=1;metric=10;next_hop=\"10.0.10.7\";interface=\"vlan10\"}];neighbors=[{interface=\"vlan10\";addr=\"10.0.10.7\";mac=\"02:00:00:00:00:02\"}];neighbor_policy={mode=\"disabled\";reachable_time=30s;retrans_timer=3s;hold_depth=4}"}, nil},
			{"routing", "vrf", "default2", "", nil, &[2]string{"routing.vrf", "interfaces={\"vlan10\":vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]};routes=[{prefix=\"10.0.20.0/24\";preference=1;metric=10;next_hop=\"10.0.10.7\";interface=\"vlan10\"}];neighbors=[{interface=\"vlan10\";addr=\"10.0.10.7\";mac=\"02:00:00:00:00:02\"}];neighbor_policy={mode=\"disabled\";reachable_time=30s;retrans_timer=3s;hold_depth=4}"}},
		}, nil
	case "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].@key":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"default\"/\"vlan10\"", "", &[2]string{"routing.interface", "vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]"}, nil},
			{"routing", "interface", "\"default\"/\"vlan11\"", "", nil, &[2]string{"routing.interface", "vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchPortsExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.Ports.Ports()[1/1/1].Name":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "", &[2]string{"port.port", "name=\"1/1/1\",kind=\"Other\",ifindex=1,admin=\"Up\",oper=\"Up\",mtu=1500,lag=\"lag1\""}, nil},
			{"port", "port", "1/1/2", "", nil, &[2]string{"port.port", "name=\"1/1/2\",kind=\"Other\",ifindex=1,admin=\"Up\",oper=\"Up\",mtu=1500,lag=\"lag1\""}},
		}, nil
	case "vswitch.Config.Ports.Ports()[1/1/1].IfIndex":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "ifindex", &[2]string{"port.ifindex", "1"}, &[2]string{"port.ifindex", "2"}},
		}, nil
	case "vswitch.Config.Ports.Ports()[1/1/1].Kind":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "kind", &[2]string{"port.kind", "Other"}, &[2]string{"port.kind", "Physical"}},
		}, nil
	case "vswitch.Config.Ports.Ports()[1/1/1].AdminStatus":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "admin_status", &[2]string{"port.link_state", "Up"}, &[2]string{"port.link_state", "Down"}},
		}, nil
	case "vswitch.Config.Ports.Ports()[1/1/1].OperStatus":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "oper_status", &[2]string{"port.link_state", "Up"}, &[2]string{"port.link_state", "Down"}},
		}, nil
	case "vswitch.Config.Ports.Ports()[1/1/1].MTU":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "mtu", &[2]string{"port.mtu", "1500"}, &[2]string{"port.mtu", "1501"}},
		}, nil
	case "vswitch.Config.Ports.Ports()[1/1/1].LagParent":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "lag_parent", &[2]string{"port.lag_parent", "lag1"}, &[2]string{"port.lag_parent", ""}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func vswitchPresenceExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "vswitch.Config.MAC":
		*hits++
		return []changeRecord{
			{"port", "device", "", "mac", &[2]string{"vswitch.mac", "02:00:00:00:00:01"}, &[2]string{"vswitch.mac", "02:00:00:00:00:02"}},
		}, nil
	case "vswitch.Config.Phy.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "capability", "ethernet", "presence", &[2]string{"vswitch.layer", "ethernet"}, nil},
			{"poe", "capability", "poe", "presence", &[2]string{"vswitch.layer", "poe"}, nil},
		}, nil
	case "vswitch.Config.Bridge.@present":
		*hits++
		return []changeRecord{
			{"relay", "capability", "relay", "presence", &[2]string{"vswitch.layer", "relay"}, nil},
			{"vlan", "capability", "vlan", "presence", &[2]string{"vswitch.layer", "vlan"}, nil},
			{"vlan", "bridge", "", "vlan.presence", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
		}, nil
	case "vswitch.Config.LAG.@present":
		*hits++
		return []changeRecord{
			{"lag", "capability", "lag", "presence", &[2]string{"vswitch.layer", "lag"}, nil},
		}, nil
	case "vswitch.Config.STP.@present":
		*hits++
		return []changeRecord{
			{"stp", "capability", "stp", "presence", &[2]string{"vswitch.layer", "stp"}, nil},
			{"stp", "bridge", "", "address", &[2]string{"stp.mac", "02:00:00:00:00:01"}, &[2]string{"stp.mac", "00:00:00:00:00:00"}},
		}, nil
	case "vswitch.Config.LoopProtect.@present":
		*hits++
		return []changeRecord{
			{"loopprotect", "capability", "loopprotect", "presence", &[2]string{"vswitch.layer", "loopprotect"}, nil},
		}, nil
	case "vswitch.Config.Mcast.@present":
		*hits++
		return []changeRecord{
			{"mcast", "capability", "mcast", "presence", &[2]string{"vswitch.layer", "mcast"}, nil},
		}, nil
	case "vswitch.Config.Routing.@present":
		*hits++
		return []changeRecord{
			{"routing", "capability", "routing", "presence", &[2]string{"vswitch.layer", "routing"}, nil},
		}, nil
	case "vswitch.Config.Traffic.@present":
		*hits++
		return []changeRecord{
			{"traffic", "capability", "traffic", "presence", &[2]string{"vswitch.layer", "traffic"}, nil},
		}, nil
	case "vswitch.Config.Filter.@present":
		*hits++
		return []changeRecord{
			{"filter", "capability", "filter", "presence", &[2]string{"vswitch.layer", "filter"}, nil},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
