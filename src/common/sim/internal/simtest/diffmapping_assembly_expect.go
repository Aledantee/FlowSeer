package simtest

import "go.aledante.io/FlowSeer/src/common/errs"

func assemblyDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "assembly.Config.Switches[sw1].Bridge.AgingTime":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "\"sw1\"/\"\"", "aging_time", &[2]string{"bridge.duration", "5m0s"}, &[2]string{"bridge.duration", "5m1s"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.MaxEntries":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "\"sw1\"/\"\"", "max_entries", &[2]string{"bridge.int", "1024"}, &[2]string{"bridge.int", "1025"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.FloodVLANs[0]":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "\"sw1\"/\"\"", "flood_vlans[\"10\"]", &[2]string{"bridge.vlan_id", "10"}, nil},
			{"relay", "bridge", "\"sw1\"/\"\"", "flood_vlans[\"11\"]", nil, &[2]string{"bridge.vlan_id", "11"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.ProtectedPorts[0]":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "\"sw1\"/\"\"", "protected_ports[\"1/1/1\"]", &[2]string{"bridge.port", "1/1/1"}, nil},
			{"relay", "bridge", "\"sw1\"/\"\"", "protected_ports[\"1/1/2\"]", nil, &[2]string{"bridge.port", "1/1/2"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.ForwardBPDU":
		*hits++
		return []changeRecord{
			{"relay", "bridge", "\"sw1\"/\"\"", "forward_bpdu", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Table[10]":
		*hits++
		return []changeRecord{
			{"vlan", "vlan", "\"sw1\"/\"10\"", "name", &[2]string{"bridge.vlan_name", "ten"}, &[2]string{"bridge.vlan_name", "eleven"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].PVID":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "pvid", &[2]string{"bridge.pvid", "10"}, &[2]string{"bridge.pvid", "11"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tagged[0]":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "tagged_vlan_ids[\"20\"]", &[2]string{"bridge.vlan_id", "20"}, nil},
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "tagged_vlan_ids[\"21\"]", nil, &[2]string{"bridge.vlan_id", "21"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Untagged[0]":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "untagged_vlan_ids[\"10\"]", &[2]string{"bridge.vlan_id", "10"}, nil},
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "untagged_vlan_ids[\"11\"]", nil, &[2]string{"bridge.vlan_id", "11"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].IngressFiltering":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "ingress_filtering", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Admission":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "frame_admission", &[2]string{"bridge.admission", "TaggedOnly"}, &[2]string{"bridge.admission", "All"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].PriorityTags":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "priority_tags", &[2]string{"bridge.priority_tags", "IfNonzero"}, &[2]string{"bridge.priority_tags", "Never"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tunnel.VID":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "tunnel.vid", &[2]string{"bridge.vlan_id", "30"}, &[2]string{"bridge.vlan_id", "31"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tunnel.CustomerVIDs[0]":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "tunnel.customer_vids[\"100\"]", &[2]string{"bridge.vlan_id", "100"}, nil},
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "tunnel.customer_vids[\"101\"]", nil, &[2]string{"bridge.vlan_id", "101"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tunnel.TPID":
		*hits++
		return []changeRecord{}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.@present":
		*hits++
		return []changeRecord{
			{"vlan", "capability", "\"sw1\"/\"vlan\"", "presence", &[2]string{"vswitch.layer", "vlan"}, nil},
			{"vlan", "bridge", "\"sw1\"/\"\"", "vlan.presence", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
			{"vlan", "vlan", "\"sw1\"/\"10\"", "", &[2]string{"bridge.vlan_name", "ten"}, nil},
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "", &[2]string{"bridge.switchport", "pvid=10;tagged=[20];untagged=[10];ingress_filtering=true;admission=\"TaggedOnly\";priority_tags=\"IfNonzero\";tunnel={vid=30;tpid=34984;customer_vids=[100]}"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Table[10].@key":
		*hits++
		return []changeRecord{
			{"vlan", "vlan", "\"sw1\"/\"10\"", "", &[2]string{"bridge.vlan_name", "ten"}, nil},
			{"vlan", "vlan", "\"sw1\"/\"11\"", "", nil, &[2]string{"bridge.vlan_name", "ten"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "", &[2]string{"bridge.switchport", "pvid=10;tagged=[20];untagged=[10];ingress_filtering=true;admission=\"TaggedOnly\";priority_tags=\"IfNonzero\";tunnel={vid=30;tpid=34984;customer_vids=[100]}"}, nil},
			{"vlan", "port", "\"sw1\"/\"1/1/2\"", "", nil, &[2]string{"bridge.switchport", "pvid=10;tagged=[20];untagged=[10];ingress_filtering=true;admission=\"TaggedOnly\";priority_tags=\"IfNonzero\";tunnel={vid=30;tpid=34984;customer_vids=[100]}"}},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].PVID.@present":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "pvid", &[2]string{"bridge.pvid", "10"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tunnel.@present":
		*hits++
		return []changeRecord{
			{"vlan", "port", "\"sw1\"/\"1/1/1\"", "tunnel.presence", &[2]string{"bridge.tunnel", "vid=30;tpid=34984;customer_vids=[100]"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Setting.SpeedBPS":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Setting.Duplex":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Setting.AutoNegotiation":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].AutoNegotiationSupported":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "auto_negotiation_supported", &[2]string{"phy.capability", "Supported"}, &[2]string{"phy.capability", "Unsupported"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Observed.SpeedBPS":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Observed.Duplex":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].SupportedSpeedsBPS[0]":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "supported_speeds_bps[\"1000000000\"]", &[2]string{"phy.speed_bps", "1000000000"}, nil},
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "supported_speeds_bps[\"10000000000\"]", nil, &[2]string{"phy.speed_bps", "10000000000"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Groups[g1].PowerNanowatts":
		*hits++
		return []changeRecord{
			{"poe", "pse_group", "\"sw1\"/\"g1\"", "power_nanowatts", &[2]string{"phy.power_nw", "370000000000"}, &[2]string{"phy.power_nw", "370000000001"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].Group":
		*hits++
		return []changeRecord{
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "group", &[2]string{"phy.string", "g1"}, &[2]string{"phy.string", "g2"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].MaxClass":
		*hits++
		return []changeRecord{
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "max_class", &[2]string{"phy.class", "4"}, &[2]string{"phy.class", "5"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].Enabled":
		*hits++
		return []changeRecord{
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].Limit":
		*hits++
		return []changeRecord{
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "power_limit_nanowatts", &[2]string{"phy.power_nw", "5000000000"}, &[2]string{"phy.power_nw", "5000000001"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "priority", &[2]string{"phy.priority", "High"}, &[2]string{"phy.priority", "Low"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].PD":
		*hits++
		return []changeRecord{
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "pd", &[2]string{"phy.pd_state", "Attached"}, &[2]string{"phy.pd_state", "Absent"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].PDClass":
		*hits++
		return []changeRecord{
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "pd_class", &[2]string{"phy.class", "4"}, &[2]string{"phy.class", "5"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "", &[2]string{"phy.ethernet", "speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\"Full\",autoneg=true},observed={speed=1000000000,duplex=\"Full\"}"}, nil},
			{"ethernet", "port", "\"sw1\"/\"1/1/2\"", "", nil, &[2]string{"phy.ethernet", "speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\"Full\",autoneg=true},observed={speed=1000000000,duplex=\"Full\"}"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Setting.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Observed.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"ethernet", "port", "\"sw1\"/\"1/1/1\"", "observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.@present":
		*hits++
		return []changeRecord{
			{"poe", "capability", "\"sw1\"/\"poe\"", "presence", &[2]string{"vswitch.layer", "poe"}, nil},
			{"poe", "pse_group", "\"sw1\"/\"g1\"", "", &[2]string{"phy.pse_group", "370000000000"}, nil},
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "", &[2]string{"phy.pse_port", "group=\"g1\",max_class=4,enabled=true,limit=5000000000,priority=\"High\",pd=Attached,pd_class=4"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Groups[g1].@key":
		*hits++
		return []changeRecord{
			{"poe", "pse_group", "\"sw1\"/\"g1\"", "", &[2]string{"phy.pse_group", "370000000000"}, nil},
			{"poe", "pse_group", "\"sw1\"/\"g2\"", "", nil, &[2]string{"phy.pse_group", "370000000000"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "", &[2]string{"phy.pse_port", "group=\"g1\",max_class=4,enabled=true,limit=5000000000,priority=\"High\",pd=Attached,pd_class=4"}, nil},
			{"poe", "port", "\"sw1\"/\"1/1/2\"", "", nil, &[2]string{"phy.pse_port", "group=\"g1\",max_class=4,enabled=true,limit=5000000000,priority=\"High\",pd=Attached,pd_class=4"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].Limit.@present":
		*hits++
		return []changeRecord{
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "power_limit_nanowatts", &[2]string{"phy.power_nw", "5000000000"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].PDClass.@present":
		*hits++
		return []changeRecord{
			{"poe", "port", "\"sw1\"/\"1/1/1\"", "pd_class", &[2]string{"phy.class", "4"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].LoopProtect.Interval":
		*hits++
		return []changeRecord{
			{"loopprotect", "bridge", "\"sw1\"/\"\"", "interval", &[2]string{"loopprotect.duration", "5s"}, &[2]string{"loopprotect.duration", "6s"}},
		}, nil
	case "assembly.Config.Switches[sw1].LoopProtect.Ports[1/1/1].Action":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "\"sw1\"/\"1/1/1\"", "action", &[2]string{"loopprotect.action", "Block"}, &[2]string{"loopprotect.action", "Disable"}},
		}, nil
	case "assembly.Config.Switches[sw1].LoopProtect.Ports[1/1/1].Recovery.Mode":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "\"sw1\"/\"1/1/1\"", "recovery.mode", &[2]string{"loopprotect.recovery_mode", "Timer"}, &[2]string{"loopprotect.recovery_mode", "Manual"}},
		}, nil
	case "assembly.Config.Switches[sw1].LoopProtect.Ports[1/1/1].Recovery.Duration":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "\"sw1\"/\"1/1/1\"", "recovery.duration", &[2]string{"loopprotect.duration", "30s"}, &[2]string{"loopprotect.duration", "31s"}},
		}, nil
	case "assembly.Config.Switches[sw1].LoopProtect.Ports[1/1/1].VLANs[0]":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "\"sw1\"/\"1/1/1\"", "vlans[\"10\"]", &[2]string{"loopprotect.vlan_id", "10"}, nil},
			{"loopprotect", "port", "\"sw1\"/\"1/1/1\"", "vlans[\"11\"]", nil, &[2]string{"loopprotect.vlan_id", "11"}},
		}, nil
	case "assembly.Config.Switches[sw1].LoopProtect.Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "\"sw1\"/\"1/1/1\"", "", &[2]string{"loopprotect.port", "action=Block,recovery_mode=Timer,recovery_duration=30s,vlans=10"}, nil},
			{"loopprotect", "port", "\"sw1\"/\"1/1/2\"", "", nil, &[2]string{"loopprotect.port", "action=Block,recovery_mode=Timer,recovery_duration=30s,vlans=10"}},
		}, nil
	case "assembly.Config.Switches[sw1].Mcast.VLANs[10].FloodUnregistered":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "\"sw1\"/\"10\"", "flood_unregistered", &[2]string{"mcast.bool", "true"}, &[2]string{"mcast.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].Mcast.VLANs[10].FastLeave":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "\"sw1\"/\"10\"", "fast_leave", &[2]string{"mcast.bool", "true"}, &[2]string{"mcast.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].Mcast.VLANs[10].MembershipInterval":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "\"sw1\"/\"10\"", "membership_interval", &[2]string{"mcast.duration", "4m20s"}, &[2]string{"mcast.duration", "4m21s"}},
		}, nil
	case "assembly.Config.Switches[sw1].Mcast.VLANs[10].RouterPortInterval":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "\"sw1\"/\"10\"", "router_port_interval", &[2]string{"mcast.duration", "4m20s"}, &[2]string{"mcast.duration", "4m21s"}},
		}, nil
	case "assembly.Config.Switches[sw1].Mcast.VLANs[10].LastMemberQueryInterval":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "\"sw1\"/\"10\"", "last_member_query_interval", &[2]string{"mcast.duration", "1s"}, &[2]string{"mcast.duration", "2s"}},
		}, nil
	case "assembly.Config.Switches[sw1].Mcast.VLANs[10].LastMemberQueryCount":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "\"sw1\"/\"10\"", "last_member_query_count", &[2]string{"mcast.int", "2"}, &[2]string{"mcast.int", "3"}},
		}, nil
	case "assembly.Config.Switches[sw1].Mcast.VLANs[10].RouterPorts[0]":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "\"sw1\"/\"10\"", "router_ports[\"1/1/1\"]", &[2]string{"mcast.router_port", "1/1/1"}, nil},
			{"mcast", "vlan", "\"sw1\"/\"10\"", "router_ports[\"1/1/2\"]", nil, &[2]string{"mcast.router_port", "1/1/2"}},
		}, nil
	case "assembly.Config.Switches[sw1].Mcast.VLANs[10].@key":
		*hits++
		return []changeRecord{
			{"mcast", "vlan", "\"sw1\"/\"10\"", "", &[2]string{"mcast.vlan_snooping", "flood=true,fast_leave=true,router_ports=[\"1/1/1\"],mem_int=4m20s,rtr_int=4m20s,lmqi=1s,lmqc=2"}, nil},
			{"mcast", "vlan", "\"sw1\"/\"11\"", "", nil, &[2]string{"mcast.vlan_snooping", "flood=true,fast_leave=true,router_ports=[\"1/1/1\"],mem_int=4m20s,rtr_int=4m20s,lmqi=1s,lmqc=2"}},
		}, nil
	case "assembly.Config.Switches[sw1].Mcast.VLANs[10].FloodUnregistered.@present":
		*hits++
		return []changeRecord{}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].Mode":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "mode", &[2]string{"lag.bond_mode", "BalanceSLB"}, &[2]string{"lag.bond_mode", "BalanceTCP"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].Primary":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "primary", &[2]string{"lag.primary", "1/1/1"}, &[2]string{"lag.primary", "1/1/2"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].UpDelay":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "up_delay", &[2]string{"lag.duration", "2s"}, &[2]string{"lag.duration", "3s"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].DownDelay":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "down_delay", &[2]string{"lag.duration", "1s"}, &[2]string{"lag.duration", "2s"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].HashBasis":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "hash_basis", &[2]string{"lag.hash_basis", "7"}, &[2]string{"lag.hash_basis", "8"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].MinLinks":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "min_links", &[2]string{"lag.min_links", "1"}, &[2]string{"lag.min_links", "2"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].RebalanceInterval":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "rebalance_interval", &[2]string{"lag.rebalance_interval", "45s"}, &[2]string{"lag.rebalance_interval", "46s"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.Mode":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "lacp_mode", &[2]string{"lag.lacp_mode", "Active"}, &[2]string{"lag.lacp_mode", "Passive"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.Fast":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "lacp_fast", &[2]string{"lag.bool", "true"}, &[2]string{"lag.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.SystemPriority":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "lacp_system_priority", &[2]string{"lag.uint16", "32768"}, &[2]string{"lag.uint16", "32769"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.SystemID":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "lacp_system_id", &[2]string{"lag.mac", "02:00:00:00:00:01"}, &[2]string{"lag.mac", "02:00:00:00:00:02"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.Key":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "lacp_key", &[2]string{"lag.uint16", "1"}, &[2]string{"lag.uint16", "2"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.Fallback":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "lacp_fallback", &[2]string{"lag.bool", "true"}, &[2]string{"lag.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].Members[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"lag", "lag_member", "\"sw1\"/\"\\\"lag1\\\"/\\\"1/1/1\\\"\"", "priority", &[2]string{"lag.port_priority", "128"}, &[2]string{"lag.port_priority", "129"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].Members[1/1/1].Key":
		*hits++
		return []changeRecord{
			{"lag", "lag_member", "\"sw1\"/\"\\\"lag1\\\"/\\\"1/1/1\\\"\"", "key", &[2]string{"lag.uint16", "1"}, &[2]string{"lag.uint16", "2"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].@key":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "", &[2]string{"lag.lag", "mode=\"BalanceSLB\";primary=\"1/1/1\";up_delay_ns=2000000000;down_delay_ns=1000000000;hash_basis=7;min_links=1;rebalance_interval=45s;lacp={mode=\"Active\";fast=true;system_priority=32768;system_id=\"02:00:00:00:00:01\";key=1;fallback=true};members={\"1/1/1\":{priority=128;key=1}}"}, nil},
			{"lag", "lag", "\"sw1\"/\"lag2\"", "", nil, &[2]string{"lag.lag", "mode=\"BalanceSLB\";primary=\"1/1/1\";up_delay_ns=2000000000;down_delay_ns=1000000000;hash_basis=7;min_links=1;rebalance_interval=45s;lacp={mode=\"Active\";fast=true;system_priority=32768;system_id=\"02:00:00:00:00:01\";key=1;fallback=true};members={\"1/1/1\":{priority=128;key=1}}"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].RebalanceInterval.@present":
		*hits++
		return []changeRecord{
			{"lag", "lag", "\"sw1\"/\"lag1\"", "rebalance_interval", &[2]string{"lag.rebalance_interval", "45s"}, &[2]string{"lag.rebalance_interval", "10s"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.LAGs[lag1].Members[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"lag", "lag_member", "\"sw1\"/\"\\\"lag1\\\"/\\\"1/1/1\\\"\"", "", &[2]string{"lag.member", "priority=128,key=1"}, nil},
			{"lag", "lag_member", "\"sw1\"/\"\\\"lag1\\\"/\\\"1/1/2\\\"\"", "", nil, &[2]string{"lag.member", "priority=128,key=1"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Priority":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "4096"}},
			{"stp", "pvst_tree", "\"sw1\"/\"1\"", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "4096"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Address":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "address", &[2]string{"stp.mac", "02:00:00:00:00:01"}, &[2]string{"stp.mac", "02:00:00:00:00:02"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.HelloTime":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "hello_time", &[2]string{"stp.duration", "2s"}, &[2]string{"stp.duration", "3s"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MaxAge":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "max_age", &[2]string{"stp.duration", "20s"}, &[2]string{"stp.duration", "21s"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.ForwardDelay":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "forward_delay", &[2]string{"stp.duration", "15s"}, &[2]string{"stp.duration", "16s"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.TxHoldCount":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "tx_hold_count", &[2]string{"stp.tx_hold_count", "6"}, &[2]string{"stp.tx_hold_count", "7"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "32768"}},
			{"stp", "pvst_tree", "\"sw1\"/\"1\"", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "32768"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "priority", &[2]string{"stp.port_priority", "0"}, &[2]string{"stp.port_priority", "16"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].PathCost":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "admin_path_cost", &[2]string{"stp.path_cost", "20000"}, &[2]string{"stp.path_cost", "20001"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].AdminEdge":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "admin_edge", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].AutoEdge":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "auto_edge", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].PointToPoint":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "admin_point_to_point", &[2]string{"stp.point_to_point", "ForceTrue"}, &[2]string{"stp.point_to_point", "ForceFalse"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].BPDUGuard":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "bpdu_guard", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].RestrictedRole":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "restricted_role", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].RestrictedTCN":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "restricted_tcn", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].LoopGuard":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "loop_guard", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "priority", &[2]string{"stp.port_priority", "0"}, &[2]string{"stp.port_priority", "128"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.Name":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "mst.name", &[2]string{"stp.mst.name", "region1"}, &[2]string{"stp.mst.name", "region2"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.Revision":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "mst.revision", &[2]string{"stp.mst.revision", "1"}, &[2]string{"stp.mst.revision", "2"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.MaxHops":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "mst.max_hops", &[2]string{"stp.mst.max_hops", "20"}, &[2]string{"stp.mst.max_hops", "21"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.Instances[1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "\"sw1\"/\"1\"", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "4096"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.Instances[1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "\"sw1\"/\"1\"", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "32768"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.Instances[1].VLANs[0]":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "\"sw1\"/\"1\"", "vlans[\"10\"]", &[2]string{"stp.mst.vlan_id", "10"}, nil},
			{"stp", "mst_instance", "\"sw1\"/\"1\"", "vlans[\"11\"]", nil, &[2]string{"stp.mst.vlan_id", "11"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.Instances[1].Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"sw1\"/\"\\\"1\\\"/\\\"1/1/1\\\"\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "129"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.Instances[1].Ports[1/1/1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"sw1\"/\"\\\"1\\\"/\\\"1/1/1\\\"\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "0"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.Instances[1].Ports[1/1/1].PathCost":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"sw1\"/\"\\\"1\\\"/\\\"1/1/1\\\"\"", "path_cost", &[2]string{"stp.path_cost", "20000"}, &[2]string{"stp.path_cost", "20001"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.PVST.Trees[10].Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"sw1\"/\"\\\"10\\\"/\\\"1/1/1\\\"\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "129"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.PVST.Trees[10].Ports[1/1/1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"sw1\"/\"\\\"10\\\"/\\\"1/1/1\\\"\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "0"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.PVST.Trees[10].Ports[1/1/1].PathCost":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"sw1\"/\"\\\"10\\\"/\\\"1/1/1\\\"\"", "path_cost", &[2]string{"stp.path_cost", "20000"}, &[2]string{"stp.path_cost", "20001"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.PVST.Trees[10].Priority":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree", "\"sw1\"/\"10\"", "priority", &[2]string{"stp.priority", "8192"}, &[2]string{"stp.priority", "12288"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.PVST.Trees[10].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree", "\"sw1\"/\"10\"", "priority", &[2]string{"stp.priority", "8192"}, &[2]string{"stp.priority", "0"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"stp", "port", "\"sw1\"/\"1/1/1\"", "", &[2]string{"stp.port", "priority=0,path_cost=20000,admin_edge=true,auto_edge=true,point_to_point=\"ForceTrue\",bpdu_guard=true,restricted_role=true,restricted_tcn=true,loop_guard=true"}, nil},
			{"stp", "port", "\"sw1\"/\"1/1/2\"", "", nil, &[2]string{"stp.port", "priority=0,path_cost=20000,admin_edge=true,auto_edge=true,point_to_point=\"ForceTrue\",bpdu_guard=true,restricted_role=true,restricted_tcn=true,loop_guard=true"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.@present":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "mst.presence", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.PVST.@present":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "pvst.presence", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.Instances[1].@key":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "\"sw1\"/\"1\"", "", &[2]string{"stp.mst.instance", "priority=0,vlans=[10],ports=[1/1/1:{priority=128,path_cost=20000}]"}, nil},
			{"stp", "mst_instance", "\"sw1\"/\"2\"", "", nil, &[2]string{"stp.mst.instance", "priority=0,vlans=[10],ports=[1/1/1:{priority=128,path_cost=20000}]"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.MST.Instances[1].Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"sw1\"/\"\\\"1\\\"/\\\"1/1/1\\\"\"", "", &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}, nil},
			{"stp", "mst_instance_port", "\"sw1\"/\"\\\"1\\\"/\\\"1/1/2\\\"\"", "", nil, &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.PVST.Trees[10].@key":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree", "\"sw1\"/\"10\"", "", &[2]string{"stp.pvst.tree", "priority=8192,ports=[1/1/1:{priority=128,path_cost=20000}]"}, nil},
			{"stp", "pvst_tree", "\"sw1\"/\"11\"", "", nil, &[2]string{"stp.pvst.tree", "priority=8192,ports=[1/1/1:{priority=128,path_cost=20000}]"}},
		}, nil
	case "assembly.Config.Switches[sw1].STP.PVST.Trees[10].Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"stp", "pvst_tree_port", "\"sw1\"/\"\\\"10\\\"/\\\"1/1/1\\\"\"", "", &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}, nil},
			{"stp", "pvst_tree_port", "\"sw1\"/\"\\\"10\\\"/\\\"1/1/2\\\"\"", "", nil, &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Stateful":
		*hits++
		return []changeRecord{
			{"filter", "set", "\"sw1\"/\"ingress\"", "stateful", &[2]string{"filter.bool", "true"}, &[2]string{"filter.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Default":
		*hits++
		return []changeRecord{
			{"filter", "set", "\"sw1\"/\"ingress\"", "default", &[2]string{"filter.action", "drop"}, &[2]string{"filter.action", "accept"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Name":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "", &[2]string{"filter.rule", "name=\"rule1\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}, nil},
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule2\\\"\"", "", nil, &[2]string{"filter.rule", "name=\"rule2\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Action":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "action", &[2]string{"filter.action", "accept"}, &[2]string{"filter.action", "drop"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.Protocol":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.protocol", &[2]string{"filter.protocol", "6"}, &[2]string{"filter.protocol", "17"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.Src[0]":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.src[\"10.0.1.0/24\"]", &[2]string{"filter.prefix", "10.0.1.0/24"}, nil},
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.src[\"192.0.2.0/25\"]", nil, &[2]string{"filter.prefix", "192.0.2.0/25"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.Dst[0]":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.dst[\"10.0.2.0/24\"]", &[2]string{"filter.prefix", "10.0.2.0/24"}, nil},
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.dst[\"192.0.2.0/25\"]", nil, &[2]string{"filter.prefix", "192.0.2.0/25"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.SrcPorts[0].Start":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.src_ports[0].start", &[2]string{"filter.port", "1000"}, &[2]string{"filter.port", "1001"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.SrcPorts[0].End":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.src_ports[0].end", &[2]string{"filter.port", "2000"}, &[2]string{"filter.port", "2001"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.DstPorts[0].Start":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.dst_ports[0].start", &[2]string{"filter.port", "80"}, &[2]string{"filter.port", "81"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.DstPorts[0].End":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.dst_ports[0].end", &[2]string{"filter.port", "80"}, &[2]string{"filter.port", "81"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.ICMP.Type":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.type", &[2]string{"filter.icmp_type", "8"}, &[2]string{"filter.icmp_type", "9"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.ICMP.Code":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, &[2]string{"filter.icmp_code", "1"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.TCPFlags.Mask":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.tcp_flags.mask", &[2]string{"filter.tcp_flags", "2"}, &[2]string{"filter.tcp_flags", "18"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.TCPFlags.Value":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.tcp_flags.value", &[2]string{"filter.tcp_flags", "2"}, &[2]string{"filter.tcp_flags", "0"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Bindings[0].Interface":
		*hits++
		return []changeRecord{
			{"filter", "binding", "\"sw1\"/\"\\\"vlan10\\\"/\\\"in\\\"\"", "", &[2]string{"filter.binding", "set=\"ingress\""}, nil},
			{"filter", "binding", "\"sw1\"/\"\\\"vlan11\\\"/\\\"in\\\"\"", "", nil, &[2]string{"filter.binding", "set=\"ingress\""}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Bindings[0].Direction":
		*hits++
		return []changeRecord{
			{"filter", "binding", "\"sw1\"/\"\\\"vlan10\\\"/\\\"in\\\"\"", "", &[2]string{"filter.binding", "set=\"ingress\""}, nil},
			{"filter", "binding", "\"sw1\"/\"\\\"vlan10\\\"/\\\"out\\\"\"", "", nil, &[2]string{"filter.binding", "set=\"ingress\""}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Bindings[0].Set":
		*hits++
		return []changeRecord{
			{"filter", "binding", "\"sw1\"/\"\\\"vlan10\\\"/\\\"in\\\"\"", "set", &[2]string{"filter.binding", "set=\"ingress\""}, &[2]string{"filter.binding", "set=\"ingress2\""}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].@key":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "", &[2]string{"filter.rule", "name=\"rule1\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}, nil},
			{"filter", "rule", "\"sw1\"/\"\\\"ingress2\\\"/\\\"rule1\\\"\"", "", nil, &[2]string{"filter.rule", "name=\"rule1\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}},
			{"filter", "set", "\"sw1\"/\"ingress\"", "", &[2]string{"filter.set", "stateful=true;default=\"drop\";rule_count=1"}, nil},
			{"filter", "set", "\"sw1\"/\"ingress2\"", "", nil, &[2]string{"filter.set", "stateful=true;default=\"drop\";rule_count=1"}},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.Protocol.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.protocol", &[2]string{"filter.protocol", "6"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.ICMP.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.type", &[2]string{"filter.icmp_type", "8"}, nil},
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.ICMP.Code.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.TCPFlags.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.tcp_flags.mask", &[2]string{"filter.tcp_flags", "2"}, nil},
			{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.tcp_flags.value", &[2]string{"filter.tcp_flags", "2"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.ClearRouterFlag":
		*hits++
		return []changeRecord{
			{"routing", "routing", "\"sw1\"/\"\"", "clear_router_flag", &[2]string{"routing.clear_router_flag", "false"}, &[2]string{"routing.clear_router_flag", "true"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Interfaces[vlan10].VLAN":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"sw1\"/\"\\\"default\\\"/\\\"vlan10\\\"\"", "vlan", &[2]string{"routing.vlan", "10"}, &[2]string{"routing.vlan", "11"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Interfaces[vlan10].Port":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"sw1\"/\"\\\"default\\\"/\\\"vlan10\\\"\"", "port", &[2]string{"routing.port", "1/1/1"}, &[2]string{"routing.port", "1/1/2"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Interfaces[vlan10].MAC":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"sw1\"/\"\\\"default\\\"/\\\"vlan10\\\"\"", "mac", &[2]string{"routing.mac", "02:00:00:00:00:01"}, &[2]string{"routing.mac", "02:00:00:00:00:02"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Interfaces[vlan10].Prefixes[0]":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"sw1\"/\"\\\"default\\\"/\\\"vlan10\\\"\"", "prefixes[\"10.0.10.1/24\"]", &[2]string{"routing.prefix", "10.0.10.1/24"}, nil},
			{"routing", "interface", "\"sw1\"/\"\\\"default\\\"/\\\"vlan10\\\"\"", "prefixes[\"192.0.2.0/25\"]", nil, &[2]string{"routing.prefix", "192.0.2.0/25"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Routes[0].Prefix":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"sw1\"/\"\\\"default\\\"/\\\"10.0.20.0/24\\\"/\\\"10.0.10.7\\\"/\\\"vlan10\\\"\"", "", &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}, nil},
			{"routing", "route", "\"sw1\"/\"\\\"default\\\"/\\\"192.0.2.0/25\\\"/\\\"10.0.10.7\\\"/\\\"vlan10\\\"\"", "", nil, &[2]string{"routing.route", "prefix=\"192.0.2.0/25\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Routes[0].NextHop":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"sw1\"/\"\\\"default\\\"/\\\"10.0.20.0/24\\\"/\\\"10.0.10.7\\\"/\\\"vlan10\\\"\"", "", &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}, nil},
			{"routing", "route", "\"sw1\"/\"\\\"default\\\"/\\\"10.0.20.0/24\\\"/\\\"10.0.10.8\\\"/\\\"vlan10\\\"\"", "", nil, &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.8\",interface=\"vlan10\""}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Routes[0].Interface":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"sw1\"/\"\\\"default\\\"/\\\"10.0.20.0/24\\\"/\\\"10.0.10.7\\\"/\\\"vlan10\\\"\"", "", &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan10\""}, nil},
			{"routing", "route", "\"sw1\"/\"\\\"default\\\"/\\\"10.0.20.0/24\\\"/\\\"10.0.10.7\\\"/\\\"vlan11\\\"\"", "", nil, &[2]string{"routing.route", "prefix=\"10.0.20.0/24\",preference=1,metric=10,next_hop=\"10.0.10.7\",interface=\"vlan11\""}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Routes[0].Preference":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"sw1\"/\"\\\"default\\\"/\\\"10.0.20.0/24\\\"/\\\"10.0.10.7\\\"/\\\"vlan10\\\"\"", "preference", &[2]string{"routing.route.preference", "1"}, &[2]string{"routing.route.preference", "2"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Routes[0].Metric":
		*hits++
		return []changeRecord{
			{"routing", "route", "\"sw1\"/\"\\\"default\\\"/\\\"10.0.20.0/24\\\"/\\\"10.0.10.7\\\"/\\\"vlan10\\\"\"", "metric", &[2]string{"routing.route.metric", "10"}, &[2]string{"routing.route.metric", "11"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Neighbors[0].Interface":
		*hits++
		return []changeRecord{
			{"routing", "neighbor", "\"sw1\"/\"\\\"default\\\"/\\\"vlan10\\\"/\\\"10.0.10.7\\\"\"", "", &[2]string{"routing.neighbor", "interface=\"vlan10\",addr=\"10.0.10.7\",mac=\"02:00:00:00:00:02\""}, nil},
			{"routing", "neighbor", "\"sw1\"/\"\\\"default\\\"/\\\"vlan11\\\"/\\\"10.0.10.7\\\"\"", "", nil, &[2]string{"routing.neighbor", "interface=\"vlan11\",addr=\"10.0.10.7\",mac=\"02:00:00:00:00:02\""}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Neighbors[0].Addr":
		*hits++
		return []changeRecord{
			{"routing", "neighbor", "\"sw1\"/\"\\\"default\\\"/\\\"vlan10\\\"/\\\"10.0.10.7\\\"\"", "", &[2]string{"routing.neighbor", "interface=\"vlan10\",addr=\"10.0.10.7\",mac=\"02:00:00:00:00:02\""}, nil},
			{"routing", "neighbor", "\"sw1\"/\"\\\"default\\\"/\\\"vlan10\\\"/\\\"10.0.10.8\\\"\"", "", nil, &[2]string{"routing.neighbor", "interface=\"vlan10\",addr=\"10.0.10.8\",mac=\"02:00:00:00:00:02\""}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Neighbors[0].MAC":
		*hits++
		return []changeRecord{
			{"routing", "neighbor", "\"sw1\"/\"\\\"default\\\"/\\\"vlan10\\\"/\\\"10.0.10.7\\\"\"", "mac", &[2]string{"routing.mac", "02:00:00:00:00:02"}, &[2]string{"routing.mac", "02:00:00:00:00:03"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].NeighborPolicy.Mode":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "\"sw1\"/\"default\"", "mode", &[2]string{"routing.neighbor_policy.mode", "disabled"}, &[2]string{"routing.neighbor_policy.mode", ""}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].NeighborPolicy.ReachableTime":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "\"sw1\"/\"default\"", "reachable_time", &[2]string{"routing.neighbor_policy.reachable_time", "30s"}, &[2]string{"routing.neighbor_policy.reachable_time", "31s"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].NeighborPolicy.RetransTimer":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "\"sw1\"/\"default\"", "retrans_timer", &[2]string{"routing.neighbor_policy.retrans_timer", "3s"}, &[2]string{"routing.neighbor_policy.retrans_timer", "4s"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].NeighborPolicy.HoldDepth":
		*hits++
		return []changeRecord{
			{"routing", "neighbor-policy", "\"sw1\"/\"default\"", "hold_depth", &[2]string{"routing.neighbor_policy.hold_depth", "4"}, &[2]string{"routing.neighbor_policy.hold_depth", "5"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].@key":
		*hits++
		return []changeRecord{
			{"routing", "vrf", "\"sw1\"/\"default\"", "", &[2]string{"routing.vrf", "interfaces={\"vlan10\":vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]};routes=[{prefix=\"10.0.20.0/24\";preference=1;metric=10;next_hop=\"10.0.10.7\";interface=\"vlan10\"}];neighbors=[{interface=\"vlan10\";addr=\"10.0.10.7\";mac=\"02:00:00:00:00:02\"}];neighbor_policy={mode=\"disabled\";reachable_time=30s;retrans_timer=3s;hold_depth=4}"}, nil},
			{"routing", "vrf", "\"sw1\"/\"default2\"", "", nil, &[2]string{"routing.vrf", "interfaces={\"vlan10\":vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]};routes=[{prefix=\"10.0.20.0/24\";preference=1;metric=10;next_hop=\"10.0.10.7\";interface=\"vlan10\"}];neighbors=[{interface=\"vlan10\";addr=\"10.0.10.7\";mac=\"02:00:00:00:00:02\"}];neighbor_policy={mode=\"disabled\";reachable_time=30s;retrans_timer=3s;hold_depth=4}"}},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.VRFs[default].Interfaces[vlan10].@key":
		*hits++
		return []changeRecord{
			{"routing", "interface", "\"sw1\"/\"\\\"default\\\"/\\\"vlan10\\\"\"", "", &[2]string{"routing.interface", "vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]"}, nil},
			{"routing", "interface", "\"sw1\"/\"\\\"default\\\"/\\\"vlan11\\\"\"", "", nil, &[2]string{"routing.interface", "vlan=10;port=\"1/1/1\";mac=\"02:00:00:00:00:01\";prefixes=[\"10.0.10.1/24\"]"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Mirrors[0].Name":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "", &[2]string{"traffic.mirror", "name=\"m1\";select_all=true;select_src_ports=[\"1/1/1\"];select_dst_ports=[\"1/1/2\"];select_vlans=[10];output_port=\"1/1/3\";output_vlan=20;snap_len=128"}, nil},
			{"traffic", "mirror", "\"sw1\"/\"m2\"", "", nil, &[2]string{"traffic.mirror", "name=\"m2\";select_all=true;select_src_ports=[\"1/1/1\"];select_dst_ports=[\"1/1/2\"];select_vlans=[10];output_port=\"1/1/3\";output_vlan=20;snap_len=128"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Mirrors[0].SelectAll":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "select_all", &[2]string{"traffic.select_all", "true"}, &[2]string{"traffic.select_all", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Mirrors[0].OutputPort":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "output_port", &[2]string{"traffic.output_port", "1/1/3"}, &[2]string{"traffic.output_port", "1/1/4"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Mirrors[0].OutputVLAN":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "output_vlan", &[2]string{"traffic.output_vlan", "20"}, &[2]string{"traffic.output_vlan", "21"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Mirrors[0].SnapLen":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "snap_len", &[2]string{"traffic.snap_len", "128"}, &[2]string{"traffic.snap_len", "129"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Mirrors[0].SelectSrcPorts[0]":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "select_src_ports[\"1/1/1\"]", &[2]string{"traffic.port", "1/1/1"}, nil},
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "select_src_ports[\"1/1/2\"]", nil, &[2]string{"traffic.port", "1/1/2"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Mirrors[0].SelectDstPorts[0]":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "select_dst_ports[\"1/1/2\"]", &[2]string{"traffic.port", "1/1/2"}, nil},
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "select_dst_ports[\"1/1/3\"]", nil, &[2]string{"traffic.port", "1/1/3"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Mirrors[0].SelectVLANs[0]":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "select_vlans[\"10\"]", &[2]string{"traffic.output_vlan", "10"}, nil},
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "select_vlans[\"11\"]", nil, &[2]string{"traffic.output_vlan", "11"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Policers[1/1/1].RateBPS":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"sw1\"/\"1/1/1\"", "rate", &[2]string{"traffic.rate_bps", "1000000"}, &[2]string{"traffic.rate_bps", "1000001"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Policers[1/1/1].BurstOctets":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"sw1\"/\"1/1/1\"", "burst", &[2]string{"traffic.burst_octets", "1500"}, &[2]string{"traffic.burst_octets", "1501"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Queues[1/1/1].MaxRateBPS[0]":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"sw1\"/\"\\\"1/1/1\\\"/\\\"0\\\"\"", "max_rate", &[2]string{"traffic.max_rate_bps", "1000000"}, &[2]string{"traffic.max_rate_bps", "1000001"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Queues[1/1/1].BufferOctets[0]":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"sw1\"/\"\\\"1/1/1\\\"/\\\"0\\\"\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "4096"}, &[2]string{"traffic.queue_buffer_octets", "4097"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Mirrors[0].OutputVLAN.@present":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "\"sw1\"/\"m1\"", "output_vlan", &[2]string{"traffic.output_vlan", "20"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Policers[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"sw1\"/\"1/1/1\"", "rate", &[2]string{"traffic.rate_bps", "1000000"}, &[2]string{"traffic.rate_bps", "0"}},
			{"traffic", "port", "\"sw1\"/\"1/1/1\"", "burst", &[2]string{"traffic.burst_octets", "1500"}, &[2]string{"traffic.burst_octets", "0"}},
			{"traffic", "port", "\"sw1\"/\"1/1/2\"", "rate", &[2]string{"traffic.rate_bps", "0"}, &[2]string{"traffic.rate_bps", "1000000"}},
			{"traffic", "port", "\"sw1\"/\"1/1/2\"", "burst", &[2]string{"traffic.burst_octets", "0"}, &[2]string{"traffic.burst_octets", "1500"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Queues[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"sw1\"/\"\\\"1/1/1\\\"/\\\"0\\\"\"", "max_rate", &[2]string{"traffic.max_rate_bps", "1000000"}, &[2]string{"traffic.max_rate_bps", "0"}},
			{"traffic", "port", "\"sw1\"/\"\\\"1/1/1\\\"/\\\"0\\\"\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "4096"}, &[2]string{"traffic.queue_buffer_octets", "0"}},
			{"traffic", "port", "\"sw1\"/\"\\\"1/1/2\\\"/\\\"0\\\"\"", "max_rate", &[2]string{"traffic.max_rate_bps", "0"}, &[2]string{"traffic.max_rate_bps", "1000000"}},
			{"traffic", "port", "\"sw1\"/\"\\\"1/1/2\\\"/\\\"0\\\"\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "0"}, &[2]string{"traffic.queue_buffer_octets", "4096"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Queues[1/1/1].MaxRateBPS[0].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"sw1\"/\"\\\"1/1/1\\\"/\\\"0\\\"\"", "max_rate", &[2]string{"traffic.max_rate_bps", "1000000"}, &[2]string{"traffic.max_rate_bps", "0"}},
			{"traffic", "port", "\"sw1\"/\"\\\"1/1/1\\\"/\\\"1\\\"\"", "max_rate", &[2]string{"traffic.max_rate_bps", "0"}, &[2]string{"traffic.max_rate_bps", "1000000"}},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.Queues[1/1/1].BufferOctets[0].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"sw1\"/\"\\\"1/1/1\\\"/\\\"0\\\"\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "4096"}, &[2]string{"traffic.queue_buffer_octets", "0"}},
			{"traffic", "port", "\"sw1\"/\"\\\"1/1/1\\\"/\\\"1\\\"\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "0"}, &[2]string{"traffic.queue_buffer_octets", "4096"}},
		}, nil
	case "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].Name":
		*hits++
		return []changeRecord{
			{"port", "port", "\"sw1\"/\"1/1/1\"", "", &[2]string{"port.port", "name=\"1/1/1\",kind=\"Other\",ifindex=1,admin=\"Up\",oper=\"Up\",mtu=1500,lag=\"lag1\""}, nil},
			{"port", "port", "\"sw1\"/\"1/1/2\"", "", nil, &[2]string{"port.port", "name=\"1/1/2\",kind=\"Other\",ifindex=1,admin=\"Up\",oper=\"Up\",mtu=1500,lag=\"lag1\""}},
		}, nil
	case "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].IfIndex":
		*hits++
		return []changeRecord{
			{"port", "port", "\"sw1\"/\"1/1/1\"", "ifindex", &[2]string{"port.ifindex", "1"}, &[2]string{"port.ifindex", "2"}},
		}, nil
	case "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].Kind":
		*hits++
		return []changeRecord{
			{"port", "port", "\"sw1\"/\"1/1/1\"", "kind", &[2]string{"port.kind", "Other"}, &[2]string{"port.kind", "Physical"}},
		}, nil
	case "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].AdminStatus":
		*hits++
		return []changeRecord{
			{"port", "port", "\"sw1\"/\"1/1/1\"", "admin_status", &[2]string{"port.link_state", "Up"}, &[2]string{"port.link_state", "Down"}},
		}, nil
	case "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].OperStatus":
		*hits++
		return []changeRecord{
			{"port", "port", "\"sw1\"/\"1/1/1\"", "oper_status", &[2]string{"port.link_state", "Up"}, &[2]string{"port.link_state", "Down"}},
		}, nil
	case "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].MTU":
		*hits++
		return []changeRecord{
			{"port", "port", "\"sw1\"/\"1/1/1\"", "mtu", &[2]string{"port.mtu", "1500"}, &[2]string{"port.mtu", "1501"}},
		}, nil
	case "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].LagParent":
		*hits++
		return []changeRecord{
			{"port", "port", "\"sw1\"/\"1/1/1\"", "lag_parent", &[2]string{"port.lag_parent", "lag1"}, &[2]string{"port.lag_parent", ""}},
		}, nil
	case "assembly.Config.Switches[sw1].MAC":
		*hits++
		return []changeRecord{
			{"port", "device", "\"sw1\"/\"\"", "mac", &[2]string{"vswitch.mac", "02:00:00:00:00:01"}, &[2]string{"vswitch.mac", "02:00:00:00:00:02"}},
		}, nil
	case "assembly.Config.Switches[sw1].Phy.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "capability", "\"sw1\"/\"ethernet\"", "presence", &[2]string{"vswitch.layer", "ethernet"}, nil},
			{"poe", "capability", "\"sw1\"/\"poe\"", "presence", &[2]string{"vswitch.layer", "poe"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Bridge.@present":
		*hits++
		return []changeRecord{
			{"relay", "capability", "\"sw1\"/\"relay\"", "presence", &[2]string{"vswitch.layer", "relay"}, nil},
			{"vlan", "capability", "\"sw1\"/\"vlan\"", "presence", &[2]string{"vswitch.layer", "vlan"}, nil},
			{"vlan", "bridge", "\"sw1\"/\"\"", "vlan.presence", &[2]string{"bridge.bool", "true"}, &[2]string{"bridge.bool", "false"}},
		}, nil
	case "assembly.Config.Switches[sw1].LAG.@present":
		*hits++
		return []changeRecord{
			{"lag", "capability", "\"sw1\"/\"lag\"", "presence", &[2]string{"vswitch.layer", "lag"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].STP.@present":
		*hits++
		return []changeRecord{
			{"stp", "capability", "\"sw1\"/\"stp\"", "presence", &[2]string{"vswitch.layer", "stp"}, nil},
			{"stp", "bridge", "\"sw1\"/\"\"", "address", &[2]string{"stp.mac", "02:00:00:00:00:01"}, &[2]string{"stp.mac", "00:00:00:00:00:00"}},
		}, nil
	case "assembly.Config.Switches[sw1].LoopProtect.@present":
		*hits++
		return []changeRecord{
			{"loopprotect", "capability", "\"sw1\"/\"loopprotect\"", "presence", &[2]string{"vswitch.layer", "loopprotect"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Mcast.@present":
		*hits++
		return []changeRecord{
			{"mcast", "capability", "\"sw1\"/\"mcast\"", "presence", &[2]string{"vswitch.layer", "mcast"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Routing.@present":
		*hits++
		return []changeRecord{
			{"routing", "capability", "\"sw1\"/\"routing\"", "presence", &[2]string{"vswitch.layer", "routing"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Traffic.@present":
		*hits++
		return []changeRecord{
			{"traffic", "capability", "\"sw1\"/\"traffic\"", "presence", &[2]string{"vswitch.layer", "traffic"}, nil},
		}, nil
	case "assembly.Config.Switches[sw1].Filter.@present":
		*hits++
		return []changeRecord{
			{"filter", "capability", "\"sw1\"/\"filter\"", "presence", &[2]string{"vswitch.layer", "filter"}, nil},
		}, nil
	case "assembly.Config.Start":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "start", &[2]string{"fabric.start", "2026-09-10T12:00:00Z"}, &[2]string{"fabric.start", "2026-09-10T12:00:01Z"}},
		}, nil
	case "assembly.Config.Hosts[h1].Address":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "address", &[2]string{"fabric.mac", "02:00:00:00:00:02"}, &[2]string{"fabric.mac", "02:00:00:00:00:05"}},
		}, nil
	case "assembly.Config.Hosts[h1].VLAN":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "vlan", &[2]string{"fabric.vlan", "10"}, &[2]string{"fabric.vlan", "11"}},
		}, nil
	case "assembly.Config.Hosts[h1].VLAN.@present":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "vlan", &[2]string{"fabric.vlan", "10"}, nil},
		}, nil
	case "assembly.Config.Hosts[h1].IP.Addresses[0]":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "addresses[\"10.0.10.7/24\"]", &[2]string{"fabric.prefix", "10.0.10.7/24"}, nil},
			{"fabric", "host", "h1", "addresses[\"192.0.2.7/24\"]", nil, &[2]string{"fabric.prefix", "192.0.2.7/24"}},
		}, nil
	case "assembly.Config.Hosts[h1].IP.Gateway":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "gateway", &[2]string{"fabric.gateway", "10.0.10.1"}, &[2]string{"fabric.gateway", "10.0.10.2"}},
		}, nil
	case "assembly.Config.Hosts[h1].IP.Neighbors[10.0.10.1]":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "neighbors[\"10.0.10.1\"]", &[2]string{"fabric.mac", "02:00:00:00:00:03"}, &[2]string{"fabric.mac", "02:00:00:00:00:05"}},
		}, nil
	case "assembly.Config.Hosts[h1].IP.Neighbors[10.0.10.1].@key":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "neighbors[\"10.0.10.1\"]", &[2]string{"fabric.mac", "02:00:00:00:00:03"}, nil},
			{"fabric", "host", "h1", "neighbors[\"10.0.10.2\"]", nil, &[2]string{"fabric.mac", "02:00:00:00:00:03"}},
		}, nil
	case "assembly.Config.Hosts[h1].IP.@present":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "addresses[\"10.0.10.7/24\"]", &[2]string{"fabric.prefix", "10.0.10.7/24"}, nil},
			{"fabric", "host", "h1", "gateway", &[2]string{"fabric.gateway", "10.0.10.1"}, nil},
			{"fabric", "host", "h1", "neighbors[\"10.0.10.1\"]", &[2]string{"fabric.mac", "02:00:00:00:00:03"}, nil},
		}, nil
	case "assembly.Config.Hosts[h1].Accept.Promiscuous":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "accept.promiscuous", &[2]string{"fabric.bool", "true"}, &[2]string{"fabric.bool", "false"}},
		}, nil
	case "assembly.Config.Hosts[h1].Accept.AllMulticast":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "accept.all_multicast", &[2]string{"fabric.bool", "true"}, &[2]string{"fabric.bool", "false"}},
		}, nil
	case "assembly.Config.Hosts[h1].Accept.Multicast[0]":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "accept.multicast[\"01:00:5e:00:00:01\"]", &[2]string{"fabric.mac", "01:00:5e:00:00:01"}, nil},
			{"fabric", "host", "h1", "accept.multicast[\"01:00:5e:00:00:02\"]", nil, &[2]string{"fabric.mac", "01:00:5e:00:00:02"}},
		}, nil
	case "assembly.Config.Hosts[h1].Ethernet.Setting.SpeedBPS":
		*hits++
		return []changeRecord{
			{"ethernet", "host", "h1", "ethernet.speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "assembly.Config.Hosts[h1].Ethernet.Setting.Duplex":
		*hits++
		return []changeRecord{
			{"ethernet", "host", "h1", "ethernet.duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "assembly.Config.Hosts[h1].Ethernet.Setting.AutoNegotiation":
		*hits++
		return []changeRecord{
			{"ethernet", "host", "h1", "ethernet.auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "assembly.Config.Hosts[h1].Ethernet.AutoNegotiationSupported":
		*hits++
		return []changeRecord{
			{"ethernet", "host", "h1", "ethernet.auto_negotiation_supported", &[2]string{"phy.capability", "Supported"}, &[2]string{"phy.capability", "Unsupported"}},
		}, nil
	case "assembly.Config.Hosts[h1].Ethernet.Observed.SpeedBPS":
		*hits++
		return []changeRecord{
			{"ethernet", "host", "h1", "ethernet.observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "assembly.Config.Hosts[h1].Ethernet.Observed.Duplex":
		*hits++
		return []changeRecord{
			{"ethernet", "host", "h1", "ethernet.observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "assembly.Config.Hosts[h1].Ethernet.SupportedSpeedsBPS[0]":
		*hits++
		return []changeRecord{
			{"ethernet", "host", "h1", "ethernet.supported_speeds_bps[\"1000000000\"]", &[2]string{"phy.speed_bps", "1000000000"}, nil},
			{"ethernet", "host", "h1", "ethernet.supported_speeds_bps[\"10000000000\"]", nil, &[2]string{"phy.speed_bps", "10000000000"}},
		}, nil
	case "assembly.Config.Hosts[h1].Ethernet.Setting.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "host", "h1", "ethernet.speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"ethernet", "host", "h1", "ethernet.duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
			{"ethernet", "host", "h1", "ethernet.auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "assembly.Config.Hosts[h1].Ethernet.Observed.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "host", "h1", "ethernet.observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"ethernet", "host", "h1", "ethernet.observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
		}, nil
	case "assembly.Config.Reflectors[r1].Ports[p1].Setting.SpeedBPS":
		*hits++
		return []changeRecord{
			{"ethernet", "reflector", "r1", "ports[\"p1\"].speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Ports[p1].Setting.Duplex":
		*hits++
		return []changeRecord{
			{"ethernet", "reflector", "r1", "ports[\"p1\"].duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Ports[p1].Setting.AutoNegotiation":
		*hits++
		return []changeRecord{
			{"ethernet", "reflector", "r1", "ports[\"p1\"].auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Ports[p1].AutoNegotiationSupported":
		*hits++
		return []changeRecord{
			{"ethernet", "reflector", "r1", "ports[\"p1\"].auto_negotiation_supported", &[2]string{"phy.capability", "Supported"}, &[2]string{"phy.capability", "Unsupported"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Ports[p1].Observed.SpeedBPS":
		*hits++
		return []changeRecord{
			{"ethernet", "reflector", "r1", "ports[\"p1\"].observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Ports[p1].Observed.Duplex":
		*hits++
		return []changeRecord{
			{"ethernet", "reflector", "r1", "ports[\"p1\"].observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Ports[p1].SupportedSpeedsBPS[0]":
		*hits++
		return []changeRecord{
			{"ethernet", "reflector", "r1", "ports[\"p1\"].supported_speeds_bps[\"1000000000\"]", &[2]string{"phy.speed_bps", "1000000000"}, nil},
			{"ethernet", "reflector", "r1", "ports[\"p1\"].supported_speeds_bps[\"10000000000\"]", nil, &[2]string{"phy.speed_bps", "10000000000"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Ports[p1].Setting.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "reflector", "r1", "ports[\"p1\"].speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"ethernet", "reflector", "r1", "ports[\"p1\"].duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
			{"ethernet", "reflector", "r1", "ports[\"p1\"].auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Ports[p1].Observed.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "reflector", "r1", "ports[\"p1\"].observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"ethernet", "reflector", "r1", "ports[\"p1\"].observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
		}, nil
	case "assembly.Config.PhyAssumption.Ethernet.Setting.SpeedBPS":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.ethernet.speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "assembly.Config.PhyAssumption.Ethernet.Setting.Duplex":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.ethernet.duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "assembly.Config.PhyAssumption.Ethernet.Setting.AutoNegotiation":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.ethernet.auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "assembly.Config.PhyAssumption.Ethernet.AutoNegotiationSupported":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.ethernet.auto_negotiation_supported", &[2]string{"phy.capability", "Supported"}, &[2]string{"phy.capability", "Unsupported"}},
		}, nil
	case "assembly.Config.PhyAssumption.Ethernet.Observed.SpeedBPS":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.ethernet.observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "assembly.Config.PhyAssumption.Ethernet.Observed.Duplex":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.ethernet.observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "assembly.Config.PhyAssumption.Ethernet.SupportedSpeedsBPS[0]":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.ethernet.supported_speeds_bps[\"1000000000\"]", &[2]string{"phy.speed_bps", "1000000000"}, nil},
			{"fabric", "fabric", "", "phy_assumption.ethernet.supported_speeds_bps[\"10000000000\"]", nil, &[2]string{"phy.speed_bps", "10000000000"}},
		}, nil
	case "assembly.Config.PhyAssumption.Ethernet.Setting.@present":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.ethernet.speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"fabric", "fabric", "", "phy_assumption.ethernet.duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
			{"fabric", "fabric", "", "phy_assumption.ethernet.auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "assembly.Config.PhyAssumption.Ethernet.Observed.@present":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.ethernet.observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"fabric", "fabric", "", "phy_assumption.ethernet.observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
		}, nil
	case "assembly.Config.Reflectors[r1].Address":
		*hits++
		return []changeRecord{
			{"fabric", "reflector", "r1", "address", &[2]string{"fabric.mac", "02:00:00:00:00:04"}, &[2]string{"fabric.mac", "02:00:00:00:00:05"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Ports[p1].@key":
		*hits++
		return []changeRecord{
			{"ethernet", "reflector", "r1", "ports[\"p1\"]", &[2]string{"phy.ethernet", "speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\"Full\",autoneg=true},observed={speed=1000000000,duplex=\"Full\"}"}, nil},
			{"ethernet", "reflector", "r1", "ports[\"p2\"]", nil, &[2]string{"phy.ethernet", "speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\"Full\",autoneg=true},observed={speed=1000000000,duplex=\"Full\"}"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Attachments[p1].Port":
		*hits++
		return []changeRecord{
			{"fabric", "reflector", "r1", "attachments[\"p1\"].port", &[2]string{"fabric.reflector_attachment_port", "p1"}, &[2]string{"fabric.reflector_attachment_port", "p2"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Attachments[p1].VLAN":
		*hits++
		return []changeRecord{
			{"fabric", "reflector", "r1", "attachments[\"p1\"].vlan", &[2]string{"fabric.vlan", "10"}, &[2]string{"fabric.vlan", "11"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Attachments[p1].VLAN.@present":
		*hits++
		return []changeRecord{
			{"fabric", "reflector", "r1", "attachments[\"p1\"].vlan", &[2]string{"fabric.vlan", "10"}, nil},
		}, nil
	case "assembly.Config.Reflectors[r1].Attachments[p1].Addresses[0]":
		*hits++
		return []changeRecord{
			{"fabric", "reflector", "r1", "attachments[\"p1\"].addresses[\"10.0.20.7/24\"]", &[2]string{"fabric.prefix", "10.0.20.7/24"}, nil},
			{"fabric", "reflector", "r1", "attachments[\"p1\"].addresses[\"192.0.2.7/24\"]", nil, &[2]string{"fabric.prefix", "192.0.2.7/24"}},
		}, nil
	case "assembly.Config.Reflectors[r1].Attachments[p1].@key":
		*hits++
		return []changeRecord{
			{"fabric", "reflector", "r1", "attachments[\"p1\"]", &[2]string{"fabric.reflector_attachment", "port=\"p1\";vlan=10;addresses=[\"10.0.20.7/24\"];"}, nil},
			{"fabric", "reflector", "r1", "attachments[\"p2\"]", nil, &[2]string{"fabric.reflector_attachment", "port=\"p1\";vlan=10;addresses=[\"10.0.20.7/24\"];"}},
		}, nil
	case "assembly.Config.Cables[0].Cable.LengthMeters":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "length", &[2]string{"fabric.length_meters", "3"}, &[2]string{"fabric.length_meters", "4"}},
		}, nil
	case "assembly.Config.Cables[0].Cable.Medium":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "medium", &[2]string{"fabric.medium", "TwistedPair"}, &[2]string{"fabric.medium", "MultimodeFiber"}},
		}, nil
	case "assembly.Config.Cables[0].Cable.Delay":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "delay", &[2]string{"fabric.delay", "2ms"}, &[2]string{"fabric.delay", "3ms"}},
		}, nil
	case "assembly.Config.Cables[0].Cable.TopSpeedBPS":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "top_speed", &[2]string{"fabric.top_speed_bps", "1000000000"}, &[2]string{"fabric.top_speed_bps", "1000000001"}},
		}, nil
	case "assembly.Config.Cables[0].Cable.Fault.Kind":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "fault.kind", &[2]string{"fabric.fault_kind", "LoseEveryNth"}, &[2]string{"fabric.fault_kind", "CorruptEveryNth"}},
		}, nil
	case "assembly.Config.Cables[0].Cable.Fault.N":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "fault.n", &[2]string{"fabric.fault_n", "5"}, &[2]string{"fabric.fault_n", "6"}},
		}, nil
	case "assembly.Config.Cables[0].Cable.Fault.Sequence[0]":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "fault.sequence[\"1\"]", &[2]string{"fabric.fault_position", "1"}, nil},
			{"fabric", "cable", "h1:-sw1:1/1/1", "fault.sequence[\"2\"]", nil, &[2]string{"fabric.fault_position", "2"}},
		}, nil
	case "assembly.Config.Cables[0].Cable.Delay.@present":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "delay", &[2]string{"fabric.delay", "2ms"}, nil},
		}, nil
	case "assembly.Config.Cables[0].A.Node":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "", &[2]string{"fabric.cable", "a.node=\"h1\";a.port=\"\";b.node=\"sw1\";b.port=\"1/1/1\";length_bits=4613937818241073152;top_speed=1000000000;fault=\"kind=\\\"LoseEveryNth\\\";n=5;sequence=[];\";medium=\"TwistedPair\";delay=2000000;"}, nil},
			{"fabric", "cable", "h1:-sw2:1/1/1", "", nil, &[2]string{"fabric.cable", "a.node=\"h1\";a.port=\"\";b.node=\"sw2\";b.port=\"1/1/1\";length_bits=4613937818241073152;top_speed=1000000000;fault=\"kind=\\\"LoseEveryNth\\\";n=5;sequence=[];\";medium=\"TwistedPair\";delay=2000000;"}},
			{"fabric", "host", "h1", "port", &[2]string{"fabric.endpoint", "sw1:1/1/1"}, &[2]string{"fabric.endpoint", "sw2:1/1/1"}},
		}, nil
	case "assembly.Config.Cables[0].A.Port":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "", &[2]string{"fabric.cable", "a.node=\"h1\";a.port=\"\";b.node=\"sw1\";b.port=\"1/1/1\";length_bits=4613937818241073152;top_speed=1000000000;fault=\"kind=\\\"LoseEveryNth\\\";n=5;sequence=[];\";medium=\"TwistedPair\";delay=2000000;"}, nil},
			{"fabric", "cable", "h1:-sw1:1/1/2", "", nil, &[2]string{"fabric.cable", "a.node=\"h1\";a.port=\"\";b.node=\"sw1\";b.port=\"1/1/2\";length_bits=4613937818241073152;top_speed=1000000000;fault=\"kind=\\\"LoseEveryNth\\\";n=5;sequence=[];\";medium=\"TwistedPair\";delay=2000000;"}},
			{"fabric", "host", "h1", "port", &[2]string{"fabric.endpoint", "sw1:1/1/1"}, &[2]string{"fabric.endpoint", "sw1:1/1/2"}},
		}, nil
	case "assembly.Config.Cables[0].B.Node":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "", &[2]string{"fabric.cable", "a.node=\"h1\";a.port=\"\";b.node=\"sw1\";b.port=\"1/1/1\";length_bits=4613937818241073152;top_speed=1000000000;fault=\"kind=\\\"LoseEveryNth\\\";n=5;sequence=[];\";medium=\"TwistedPair\";delay=2000000;"}, nil},
			{"fabric", "cable", "h2:-sw1:1/1/1", "", nil, &[2]string{"fabric.cable", "a.node=\"h2\";a.port=\"\";b.node=\"sw1\";b.port=\"1/1/1\";length_bits=4613937818241073152;top_speed=1000000000;fault=\"kind=\\\"LoseEveryNth\\\";n=5;sequence=[];\";medium=\"TwistedPair\";delay=2000000;"}},
		}, nil
	case "assembly.Config.Cables[0].B.Port":
		*hits++
		return []changeRecord{
			{"fabric", "cable", "h1:-sw1:1/1/1", "", &[2]string{"fabric.cable", "a.node=\"h1\";a.port=\"\";b.node=\"sw1\";b.port=\"1/1/1\";length_bits=4613937818241073152;top_speed=1000000000;fault=\"kind=\\\"LoseEveryNth\\\";n=5;sequence=[];\";medium=\"TwistedPair\";delay=2000000;"}, nil},
			{"fabric", "cable", "h1:p1-sw1:1/1/1", "", nil, &[2]string{"fabric.cable", "a.node=\"h1\";a.port=\"p1\";b.node=\"sw1\";b.port=\"1/1/1\";length_bits=4613937818241073152;top_speed=1000000000;fault=\"kind=\\\"LoseEveryNth\\\";n=5;sequence=[];\";medium=\"TwistedPair\";delay=2000000;"}},
		}, nil
	case "assembly.Config.Cables[0].Evidence[0]":
		*hits++
		return []changeRecord{}, nil
	case "assembly.Config.Uncabled[0].Endpoint.Node":
		*hits++
		return []changeRecord{
			{"fabric", "uncabled", "sw1:1/1/2", "", &[2]string{"fabric.endpoint", "sw1:1/1/2"}, nil},
			{"fabric", "uncabled", "sw2:1/1/2", "", nil, &[2]string{"fabric.endpoint", "sw2:1/1/2"}},
		}, nil
	case "assembly.Config.Uncabled[0].Endpoint.Port":
		*hits++
		return []changeRecord{
			{"fabric", "uncabled", "sw1:1/1/2", "", &[2]string{"fabric.endpoint", "sw1:1/1/2"}, nil},
			{"fabric", "uncabled", "sw1:1/1/3", "", nil, &[2]string{"fabric.endpoint", "sw1:1/1/3"}},
		}, nil
	case "assembly.Config.Uncabled[0].Evidence[0]":
		*hits++
		return []changeRecord{}, nil
	case "assembly.Config.PhyAssumption.Medium":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.medium", &[2]string{"fabric.medium", "MultimodeFiber"}, &[2]string{"fabric.medium", "TwistedPair"}},
		}, nil
	case "assembly.Config.PhyAssumption.@present":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "phy_assumption.presence", &[2]string{"fabric.phy_assumption", "medium=\"MultimodeFiber\";ethernet=\"speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\\\"Full\\\",autoneg=true},observed={speed=1000000000,duplex=\\\"Full\\\"}\";"}, nil},
		}, nil
	case "assembly.Config.Hosts[h1].@key":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "", &[2]string{"fabric.host", "address=\"02:00:00:00:00:02\";ethernet=\"speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\\\"Full\\\",autoneg=true},observed={speed=1000000000,duplex=\\\"Full\\\"}\";accept.promiscuous=\"true\";accept.all_multicast=\"true\";accept.multicast=[\"01:00:5e:00:00:01\"];vlan=10;ip=present;addresses=[\"10.0.10.7/24\"];gateway=\"10.0.10.1\";neighbors=[\"10.0.10.1\"=\"02:00:00:00:00:03\"];"}, nil},
			{"fabric", "host", "h2", "", nil, &[2]string{"fabric.host", "address=\"02:00:00:00:00:02\";ethernet=\"speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\\\"Full\\\",autoneg=true},observed={speed=1000000000,duplex=\\\"Full\\\"}\";accept.promiscuous=\"true\";accept.all_multicast=\"true\";accept.multicast=[\"01:00:5e:00:00:01\"];vlan=10;ip=present;addresses=[\"10.0.10.7/24\"];gateway=\"10.0.10.1\";neighbors=[\"10.0.10.1\"=\"02:00:00:00:00:03\"];"}},
		}, nil
	case "assembly.Config.Reflectors[r1].@key":
		*hits++
		return []changeRecord{
			{"fabric", "reflector", "r1", "", &[2]string{"fabric.reflector", "address=\"02:00:00:00:00:04\";ports=[{name=\"p1\";ethernet=\"speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\\\"Full\\\",autoneg=true},observed={speed=1000000000,duplex=\\\"Full\\\"}\";}];attachments=[{name=\"p1\";port=\"p1\";vlan=10;addresses=[\"10.0.20.7/24\"];}];"}, nil},
			{"fabric", "reflector", "r2", "", nil, &[2]string{"fabric.reflector", "address=\"02:00:00:00:00:04\";ports=[{name=\"p1\";ethernet=\"speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\\\"Full\\\",autoneg=true},observed={speed=1000000000,duplex=\\\"Full\\\"}\";}];attachments=[{name=\"p1\";port=\"p1\";vlan=10;addresses=[\"10.0.20.7/24\"];}];"}},
		}, nil
	case "assembly.Config.Switches[sw1].@key":
		*hits++
		return []changeRecord{
			{"fabric", "switch", "sw1", "", &[2]string{"vswitch.config", "[]"}, nil},
			{"fabric", "switch", "sw2", "", nil, &[2]string{"vswitch.config", "[]"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
