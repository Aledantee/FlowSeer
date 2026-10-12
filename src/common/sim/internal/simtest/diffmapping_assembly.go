package simtest

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func assertInactiveFaultCoverage(t *testing.T, m diffMapping) {
	t.Helper()
	controls := []mappedEntry{
		{Path: "assembly.Config.Cables[0].Cable.Fault.Sequence[0]", Before: `1`, After: `2`, Reason: "Sequence is inactive under LoseEveryNth"},
		{Path: "assembly.Config.Cables[0].Cable.Fault.N", Setup: "sequence", Before: `5`, After: `6`, Reason: "N is inactive under LoseSequence"},
		{Path: "assembly.Config.Cables[0].Cable.Fault.N", Setup: "fault-none", Before: `5`, After: `6`, Reason: "N is inactive under None"},
	}
	hits := 0
	m.expectation = func(path string, hits *int) ([]changeRecord, error) {
		switch path {
		case "assembly.Config.Cables[0].Cable.Fault.Sequence[0]", "assembly.Config.Cables[0].Cable.Fault.N":
			*hits++
			return []changeRecord{}, nil
		default:
			return nil, errs.Msgf("no inactive fault expectation for %s", path)
		}
	}
	for _, entry := range controls {
		t.Run(entry.Path+"/"+entry.Reason, func(t *testing.T) {
			if err := checkMappedLeaf(m, entry, &hits); err != nil {
				t.Error(err)
			}
		})
	}
	if hits != 3 {
		t.Errorf("inactive fault expectations reached %d, want 3", hits)
	}
}

func assemblyDiffMapping() (diffMapping, error) {
	entries := make([]mappedEntry, 0, 257)
	entries = append(entries, assemblySwitchBridgeEntries()...)
	entries = append(entries, assemblySwitchPhyEthernetEntries()...)
	entries = append(entries, assemblySwitchPhyPoEEntries()...)
	entries = append(entries, assemblySwitchPhyEthernetLifecycleEntries()...)
	entries = append(entries, assemblySwitchPhyPoELifecycleEntries()...)
	entries = append(entries, assemblySwitchLoopProtectEntries()...)
	entries = append(entries, assemblySwitchMcastEntries()...)
	entries = append(entries, assemblySwitchLAGEntries()...)
	entries = append(entries, assemblySwitchSTPEntries()...)
	entries = append(entries, assemblySwitchSTPPortsEntries()...)
	entries = append(entries, assemblySwitchSTPMSTEntries()...)
	entries = append(entries, assemblySwitchSTPPVSTEntries()...)
	entries = append(entries, assemblySwitchSTPPortsIdentityEntries()...)
	entries = append(entries, assemblySwitchSTPMSTPresenceEntries()...)
	entries = append(entries, assemblySwitchSTPPVSTPresenceEntries()...)
	entries = append(entries, assemblySwitchSTPMSTIdentityEntries()...)
	entries = append(entries, assemblySwitchSTPPVSTIdentityEntries()...)
	entries = append(entries, assemblySwitchFilterEntries()...)
	entries = append(entries, assemblySwitchRoutingEntries()...)
	entries = append(entries, assemblySwitchTrafficEntries()...)
	entries = append(entries, assemblySwitchPortsEntries()...)
	entries = append(entries, assemblySwitchMACEntries()...)
	entries = append(entries, assemblySwitchPhyPresenceEntries()...)
	entries = append(entries, assemblySwitchBridgePresenceEntries()...)
	entries = append(entries, assemblySwitchLAGPresenceEntries()...)
	entries = append(entries, assemblySwitchSTPPresenceEntries()...)
	entries = append(entries, assemblySwitchLoopProtectPresenceEntries()...)
	entries = append(entries, assemblySwitchMcastPresenceEntries()...)
	entries = append(entries, assemblySwitchRoutingPresenceEntries()...)
	entries = append(entries, assemblySwitchTrafficPresenceEntries()...)
	entries = append(entries, assemblySwitchFilterPresenceEntries()...)
	entries = append(entries, assemblyStartEntries()...)
	entries = append(entries, assemblyHostsEntries()...)
	entries = append(entries, assemblyReflectorsEntries()...)
	entries = append(entries, assemblyPhyAssumptionEntries()...)
	entries = append(entries, assemblyReflectorMemberEntries()...)
	entries = append(entries, assemblyCablesEntries()...)
	entries = append(entries, assemblyUncabledEntries()...)
	entries = append(entries, assemblyPhyAssumptionMediumEntries()...)
	entries = append(entries, assemblyHostIdentityEntries()...)
	entries = append(entries, assemblyReflectorIdentityEntries()...)
	entries = append(entries, assemblySwitchIdentityEntries()...)
	return newDiffMapping("assembly", assembly.Config{}, `{"Start":"2026-09-10T12:00:00Z","Switches":{"sw1":{"MAC":"02:00:00:00:00:01","Ports":[{"Name":"1/1/1","IfIndex":1,"Kind":"Other","AdminStatus":"Up","OperStatus":"Up","MTU":1500,"LagParent":""}],"Bridge":{"AgingTime":"300s","MaxEntries":1024,"FloodVLANs":[10],"ProtectedPorts":["1/1/1"],"ForwardBPDU":true,"VLAN":{"Table":{"10":"ten"},"Switchports":{"1/1/1":{"PVID":10,"Tagged":[20],"Untagged":[10],"IngressFiltering":true,"Admission":"TaggedOnly","Tunnel":{"VID":30,"CustomerVIDs":[100],"TPID":34984},"PriorityTags":"IfNonzero"}}}},"Phy":{"Ethernet":{"1/1/1":{"SupportedSpeedsBPS":[1000000000],"AutoNegotiationSupported":"Supported","Setting":{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true},"Observed":{"SpeedBPS":1000000000,"Duplex":"Full"}}},"PoE":{"Groups":{"g1":{"PowerNanowatts":370000000000}},"Ports":{"1/1/1":{"Group":"g1","MaxClass":4,"Enabled":true,"Limit":5000000000,"Priority":"High","PD":"Attached","PDClass":4}}}},"LoopProtect":{"Interval":"5s","Ports":{"1/1/1":{"Action":"Block","Recovery":{"Mode":"Timer","Duration":"30s"},"VLANs":[10]}}},"Mcast":{"VLANs":{"10":{"FloodUnregistered":true,"FastLeave":true,"RouterPorts":["1/1/1"],"MembershipInterval":"260s","RouterPortInterval":"260s","LastMemberQueryInterval":"1s","LastMemberQueryCount":2}}},"LAG":{"LAGs":{"lag1":{"Mode":"BalanceSLB","Primary":"1/1/1","UpDelay":"2s","DownDelay":"1s","HashBasis":7,"MinLinks":1,"RebalanceInterval":"45s","LACP":{"Mode":"Active","Fast":true,"SystemPriority":32768,"SystemID":"02:00:00:00:00:01","Key":1,"Fallback":true},"Members":{"1/1/1":{"Priority":128,"Key":1}}}}},"STP":{"Priority":0,"PriorityPresent":true,"Address":"02:00:00:00:00:01","HelloTime":"2s","MaxAge":"20s","ForwardDelay":"15s","TxHoldCount":6,"Ports":{"1/1/1":{"Priority":0,"PriorityPresent":true,"PathCost":20000,"AdminEdge":true,"AutoEdge":true,"PointToPoint":"ForceTrue","BPDUGuard":true,"RestrictedRole":true,"RestrictedTCN":true,"LoopGuard":true}},"MST":{"Name":"region1","Revision":1,"MaxHops":20,"Instances":{"1":{"Priority":0,"PriorityPresent":true,"VLANs":[10],"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}},"PVST":{"Trees":{"10":{"Priority":8192,"PriorityPresent":true,"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}}},"Filter":{"Sets":{"ingress":{"Stateful":true,"Default":"drop","Rules":[{"Name":"rule1","Action":"accept","Match":{"Protocol":6,"Src":["10.0.1.0/24"],"Dst":["10.0.2.0/24"],"SrcPorts":[{"Start":1000,"End":2000}],"DstPorts":[{"Start":80,"End":80}],"ICMP":{"Type":8,"Code":0},"TCPFlags":{"Mask":2,"Value":2}}}]}},"Bindings":[{"Interface":"vlan10","Direction":"in","Set":"ingress"}]},"Routing":{"ClearRouterFlag":false,"VRFs":{"default":{"Interfaces":{"vlan10":{"VLAN":10,"Port":"1/1/1","MAC":"02:00:00:00:00:01","Prefixes":["10.0.10.1/24"]}},"Routes":[{"Prefix":"10.0.20.0/24","NextHop":"10.0.10.7","Interface":"vlan10","Preference":1,"Metric":10}],"Neighbors":[{"Interface":"vlan10","Addr":"10.0.10.7","MAC":"02:00:00:00:00:02"}],"NeighborPolicy":{"Mode":"disabled","ReachableTime":"30s","RetransTimer":"3s","HoldDepth":4}}}},"Traffic":{"Mirrors":[{"Name":"m1","SelectAll":true,"SelectSrcPorts":["1/1/1"],"SelectDstPorts":["1/1/2"],"SelectVLANs":[10],"OutputPort":"1/1/3","OutputVLAN":20,"SnapLen":128}],"Policers":{"1/1/1":{"RateBPS":1000000,"BurstOctets":1500}},"Queues":{"1/1/1":{"MaxRateBPS":{"0":1000000},"BufferOctets":{"0":4096}}}}}},"Hosts":{"h1":{"Address":"02:00:00:00:00:02","VLAN":10,"IP":{"Addresses":["10.0.10.7/24"],"Gateway":"10.0.10.1","Neighbors":{"10.0.10.1":"02:00:00:00:00:03"}},"Ethernet":{"SupportedSpeedsBPS":[1000000000],"AutoNegotiationSupported":"Supported","Setting":{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true},"Observed":{"SpeedBPS":1000000000,"Duplex":"Full"}},"Accept":{"Promiscuous":true,"AllMulticast":true,"Multicast":["01:00:5e:00:00:01"]}}},"Reflectors":{"r1":{"Address":"02:00:00:00:00:04","Ports":{"p1":{"SupportedSpeedsBPS":[1000000000],"AutoNegotiationSupported":"Supported","Setting":{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true},"Observed":{"SpeedBPS":1000000000,"Duplex":"Full"}}},"Attachments":{"p1":{"Port":"p1","VLAN":10,"Addresses":["10.0.20.7/24"]}}}},"Cables":[{"Cable":{"LengthMeters":3,"TopSpeedBPS":1000000000,"Fault":{"Kind":"LoseEveryNth","N":5,"Sequence":[1]},"Medium":"TwistedPair","Delay":"2ms"},"A":{"Node":"sw1","Port":"1/1/1"},"B":{"Node":"h1","Port":""},"Evidence":["survey:rack-walk"]}],"Uncabled":[{"Endpoint":{"Node":"sw1","Port":"1/1/2"},"Evidence":["survey:empty-port"]}],"PhyAssumption":{"Medium":"MultimodeFiber","Ethernet":{"SupportedSpeedsBPS":[1000000000],"AutoNegotiationSupported":"Supported","Setting":{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true},"Observed":{"SpeedBPS":1000000000,"Duplex":"Full"}}}}`, assembly.Config.Normalize, assembly.Diff, entries, assemblyDiffExpectation)
}

func assemblySwitchBridgeEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Bridge.AgingTime", Producer: "bridge", Setup: "", Before: `"300s"`, After: `"301s"`},
		{Path: "assembly.Config.Switches[sw1].Bridge.MaxEntries", Producer: "bridge", Setup: "", Before: `1024`, After: `1025`},
		{Path: "assembly.Config.Switches[sw1].Bridge.FloodVLANs[0]", Producer: "bridge", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].Bridge.ProtectedPorts[0]", Producer: "bridge", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].Bridge.ForwardBPDU", Producer: "bridge", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Table[10]", Producer: "bridge", Setup: "", Before: `"ten"`, After: `"eleven"`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].PVID", Producer: "bridge", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tagged[0]", Producer: "bridge", Setup: "", Before: `20`, After: `21`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Untagged[0]", Producer: "bridge", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].IngressFiltering", Producer: "bridge", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Admission", Producer: "bridge", Setup: "", Before: `"TaggedOnly"`, After: `"All"`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].PriorityTags", Producer: "bridge", Setup: "", Before: `"IfNonzero"`, After: `"Never"`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tunnel.VID", Producer: "bridge", Setup: "", Before: `30`, After: `31`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tunnel.CustomerVIDs[0]", Producer: "bridge", Setup: "", Before: `100`, After: `101`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tunnel.TPID", Producer: "bridge", Setup: "", Before: `0`, After: `34984`, Reason: "TPID zero and 34984 have the same effective value"},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.@present", Producer: "bridge", Setup: "", Before: `{"Table":{"10":"ten"},"Switchports":{"1/1/1":{"PVID":10,"Tagged":[20],"Untagged":[10],"IngressFiltering":true,"Admission":"TaggedOnly","Tunnel":{"VID":30,"CustomerVIDs":[100],"TPID":34984},"PriorityTags":"IfNonzero"}}}`, After: `null`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Table[10].@key", Producer: "bridge", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].@key", Producer: "bridge", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].PVID.@present", Producer: "bridge", Setup: "", Before: `10`, After: `null`},
		{Path: "assembly.Config.Switches[sw1].Bridge.VLAN.Switchports[1/1/1].Tunnel.@present", Producer: "bridge", Setup: "", Before: `{"VID":30,"CustomerVIDs":[100],"TPID":34984}`, After: `null`},
	}
}

func assemblySwitchPhyEthernetEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Setting.SpeedBPS", Producer: "phy", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Setting.Duplex", Producer: "phy", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Setting.AutoNegotiation", Producer: "phy", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].AutoNegotiationSupported", Producer: "phy", Setup: "", Before: `"Supported"`, After: `"Unsupported"`},
		{Path: "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Observed.SpeedBPS", Producer: "phy", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Observed.Duplex", Producer: "phy", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].SupportedSpeedsBPS[0]", Producer: "phy", Setup: "", Before: `1000000000`, After: `10000000000`},
	}
}

func assemblySwitchPhyPoEEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Groups[g1].PowerNanowatts", Producer: "phy", Setup: "", Before: `370000000000`, After: `370000000001`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].Group", Producer: "phy", Setup: "", Before: `"g1"`, After: `"g2"`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].MaxClass", Producer: "phy", Setup: "", Before: `4`, After: `5`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].Enabled", Producer: "phy", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].Limit", Producer: "phy", Setup: "", Before: `5000000000`, After: `5000000001`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].Priority", Producer: "phy", Setup: "", Before: `"High"`, After: `"Low"`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].PD", Producer: "phy", Setup: "", Before: `"Attached"`, After: `"Absent"`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].PDClass", Producer: "phy", Setup: "", Before: `4`, After: `5`},
	}
}

func assemblySwitchPhyEthernetLifecycleEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].@key", Producer: "phy", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Setting.@present", Producer: "phy", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true}`, After: `null`},
		{Path: "assembly.Config.Switches[sw1].Phy.Ethernet[1/1/1].Observed.@present", Producer: "phy", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full"}`, After: `null`},
	}
}

func assemblySwitchPhyPoELifecycleEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.@present", Producer: "phy", Setup: "", Before: `{"Groups":{"g1":{"PowerNanowatts":370000000000}},"Ports":{"1/1/1":{"Group":"g1","MaxClass":4,"Enabled":true,"Limit":5000000000,"Priority":"High","PD":"Attached","PDClass":4}}}`, After: `null`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Groups[g1].@key", Producer: "phy", Setup: "", Before: `"g1"`, After: `"g2"`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].@key", Producer: "phy", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].Limit.@present", Producer: "phy", Setup: "", Before: `5000000000`, After: `null`},
		{Path: "assembly.Config.Switches[sw1].Phy.PoE.Ports[1/1/1].PDClass.@present", Producer: "phy", Setup: "", Before: `4`, After: `null`},
	}
}

func assemblySwitchLoopProtectEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].LoopProtect.Interval", Producer: "loopprotect", Setup: "", Before: `"5s"`, After: `"6s"`},
		{Path: "assembly.Config.Switches[sw1].LoopProtect.Ports[1/1/1].Action", Producer: "loopprotect", Setup: "", Before: `"Block"`, After: `"Disable"`},
		{Path: "assembly.Config.Switches[sw1].LoopProtect.Ports[1/1/1].Recovery.Mode", Producer: "loopprotect", Setup: "", Before: `"Timer"`, After: `"Manual"`},
		{Path: "assembly.Config.Switches[sw1].LoopProtect.Ports[1/1/1].Recovery.Duration", Producer: "loopprotect", Setup: "", Before: `"30s"`, After: `"31s"`},
		{Path: "assembly.Config.Switches[sw1].LoopProtect.Ports[1/1/1].VLANs[0]", Producer: "loopprotect", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].LoopProtect.Ports[1/1/1].@key", Producer: "loopprotect", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
	}
}

func assemblySwitchMcastEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Mcast.VLANs[10].FloodUnregistered", Producer: "mcast", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].Mcast.VLANs[10].FastLeave", Producer: "mcast", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].Mcast.VLANs[10].MembershipInterval", Producer: "mcast", Setup: "", Before: `"260s"`, After: `"261s"`},
		{Path: "assembly.Config.Switches[sw1].Mcast.VLANs[10].RouterPortInterval", Producer: "mcast", Setup: "", Before: `"260s"`, After: `"261s"`},
		{Path: "assembly.Config.Switches[sw1].Mcast.VLANs[10].LastMemberQueryInterval", Producer: "mcast", Setup: "", Before: `"1s"`, After: `"2s"`},
		{Path: "assembly.Config.Switches[sw1].Mcast.VLANs[10].LastMemberQueryCount", Producer: "mcast", Setup: "", Before: `2`, After: `3`},
		{Path: "assembly.Config.Switches[sw1].Mcast.VLANs[10].RouterPorts[0]", Producer: "mcast", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].Mcast.VLANs[10].@key", Producer: "mcast", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].Mcast.VLANs[10].FloodUnregistered.@present", Producer: "mcast", Setup: "", Before: `true`, After: `null`, Reason: "Nil flood policy and true have the same effective value"},
	}
}

func assemblySwitchLAGEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].Mode", Producer: "lag", Setup: "", Before: `"BalanceSLB"`, After: `"BalanceTCP"`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].Primary", Producer: "lag", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].UpDelay", Producer: "lag", Setup: "", Before: `"2s"`, After: `"3s"`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].DownDelay", Producer: "lag", Setup: "", Before: `"1s"`, After: `"2s"`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].HashBasis", Producer: "lag", Setup: "", Before: `7`, After: `8`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].MinLinks", Producer: "lag", Setup: "", Before: `1`, After: `2`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].RebalanceInterval", Producer: "lag", Setup: "", Before: `"45s"`, After: `"46s"`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.Mode", Producer: "lag", Setup: "", Before: `"Active"`, After: `"Passive"`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.Fast", Producer: "lag", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.SystemPriority", Producer: "lag", Setup: "", Before: `32768`, After: `32769`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.SystemID", Producer: "lag", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.Key", Producer: "lag", Setup: "", Before: `1`, After: `2`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].LACP.Fallback", Producer: "lag", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].Members[1/1/1].Priority", Producer: "lag", Setup: "", Before: `128`, After: `129`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].Members[1/1/1].Key", Producer: "lag", Setup: "", Before: `1`, After: `2`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].@key", Producer: "lag", Setup: "", Before: `"lag1"`, After: `"lag2"`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].RebalanceInterval.@present", Producer: "lag", Setup: "", Before: `"45s"`, After: `null`},
		{Path: "assembly.Config.Switches[sw1].LAG.LAGs[lag1].Members[1/1/1].@key", Producer: "lag", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
	}
}

func assemblySwitchSTPEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].STP.Priority", Producer: "stp", Setup: "", Before: `0`, After: `4096`},
		{Path: "assembly.Config.Switches[sw1].STP.Address", Producer: "stp", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
		{Path: "assembly.Config.Switches[sw1].STP.HelloTime", Producer: "stp", Setup: "", Before: `"2s"`, After: `"3s"`},
		{Path: "assembly.Config.Switches[sw1].STP.MaxAge", Producer: "stp", Setup: "", Before: `"20s"`, After: `"21s"`},
		{Path: "assembly.Config.Switches[sw1].STP.ForwardDelay", Producer: "stp", Setup: "", Before: `"15s"`, After: `"16s"`},
		{Path: "assembly.Config.Switches[sw1].STP.TxHoldCount", Producer: "stp", Setup: "", Before: `6`, After: `7`},
		{Path: "assembly.Config.Switches[sw1].STP.PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
	}
}

func assemblySwitchSTPPortsEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].Priority", Producer: "stp", Setup: "", Before: `0`, After: `16`},
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].PathCost", Producer: "stp", Setup: "", Before: `20000`, After: `20001`},
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].AdminEdge", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].AutoEdge", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].PointToPoint", Producer: "stp", Setup: "", Before: `"ForceTrue"`, After: `"ForceFalse"`},
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].BPDUGuard", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].RestrictedRole", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].RestrictedTCN", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].LoopGuard", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
	}
}

func assemblySwitchSTPMSTEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].STP.MST.Name", Producer: "stp", Setup: "", Before: `"region1"`, After: `"region2"`},
		{Path: "assembly.Config.Switches[sw1].STP.MST.Revision", Producer: "stp", Setup: "", Before: `1`, After: `2`},
		{Path: "assembly.Config.Switches[sw1].STP.MST.MaxHops", Producer: "stp", Setup: "", Before: `20`, After: `21`},
		{Path: "assembly.Config.Switches[sw1].STP.MST.Instances[1].Priority", Producer: "stp", Setup: "", Before: `0`, After: `4096`},
		{Path: "assembly.Config.Switches[sw1].STP.MST.Instances[1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].STP.MST.Instances[1].VLANs[0]", Producer: "stp", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].STP.MST.Instances[1].Ports[1/1/1].Priority", Producer: "stp", Setup: "", Before: `128`, After: `129`},
		{Path: "assembly.Config.Switches[sw1].STP.MST.Instances[1].Ports[1/1/1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].STP.MST.Instances[1].Ports[1/1/1].PathCost", Producer: "stp", Setup: "", Before: `20000`, After: `20001`},
	}
}

func assemblySwitchSTPPVSTEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].STP.PVST.Trees[10].Ports[1/1/1].Priority", Producer: "stp", Setup: "", Before: `128`, After: `129`},
		{Path: "assembly.Config.Switches[sw1].STP.PVST.Trees[10].Ports[1/1/1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].STP.PVST.Trees[10].Ports[1/1/1].PathCost", Producer: "stp", Setup: "", Before: `20000`, After: `20001`},
		{Path: "assembly.Config.Switches[sw1].STP.PVST.Trees[10].Priority", Producer: "stp", Setup: "", Before: `8192`, After: `12288`},
		{Path: "assembly.Config.Switches[sw1].STP.PVST.Trees[10].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
	}
}

func assemblySwitchSTPPortsIdentityEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].STP.Ports[1/1/1].@key", Producer: "stp", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
	}
}

func assemblySwitchSTPMSTPresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].STP.MST.@present", Producer: "stp", Setup: "", Before: `{"Name":"region1","Revision":1,"MaxHops":20,"Instances":{"1":{"Priority":0,"PriorityPresent":true,"VLANs":[10],"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}}`, After: `null`},
	}
}

func assemblySwitchSTPPVSTPresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].STP.PVST.@present", Producer: "stp", Setup: "", Before: `{"Trees":{"10":{"Priority":8192,"PriorityPresent":true,"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}}`, After: `null`},
	}
}

func assemblySwitchSTPMSTIdentityEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].STP.MST.Instances[1].@key", Producer: "stp", Setup: "", Before: `1`, After: `2`},
		{Path: "assembly.Config.Switches[sw1].STP.MST.Instances[1].Ports[1/1/1].@key", Producer: "stp", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
	}
}

func assemblySwitchSTPPVSTIdentityEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].STP.PVST.Trees[10].@key", Producer: "stp", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].STP.PVST.Trees[10].Ports[1/1/1].@key", Producer: "stp", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
	}
}

func assemblySwitchFilterEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Stateful", Producer: "filter", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Default", Producer: "filter", Setup: "", Before: `"drop"`, After: `"accept"`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Name", Producer: "filter", Setup: "", Before: `"rule1"`, After: `"rule2"`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Action", Producer: "filter", Setup: "", Before: `"accept"`, After: `"drop"`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.Protocol", Producer: "filter", Setup: "", Before: `6`, After: `17`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.Src[0]", Producer: "filter", Setup: "", Before: `"10.0.1.0/24"`, After: `"192.0.2.0/25"`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.Dst[0]", Producer: "filter", Setup: "", Before: `"10.0.2.0/24"`, After: `"192.0.2.0/25"`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.SrcPorts[0].Start", Producer: "filter", Setup: "", Before: `1000`, After: `1001`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.SrcPorts[0].End", Producer: "filter", Setup: "", Before: `2000`, After: `2001`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.DstPorts[0].Start", Producer: "filter", Setup: "", Before: `80`, After: `81`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.DstPorts[0].End", Producer: "filter", Setup: "", Before: `80`, After: `81`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.ICMP.Type", Producer: "filter", Setup: "", Before: `8`, After: `9`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.ICMP.Code", Producer: "filter", Setup: "", Before: `0`, After: `1`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.TCPFlags.Mask", Producer: "filter", Setup: "", Before: `2`, After: `18`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.TCPFlags.Value", Producer: "filter", Setup: "", Before: `2`, After: `0`},
		{Path: "assembly.Config.Switches[sw1].Filter.Bindings[0].Interface", Producer: "filter", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
		{Path: "assembly.Config.Switches[sw1].Filter.Bindings[0].Direction", Producer: "filter", Setup: "", Before: `"in"`, After: `"out"`},
		{Path: "assembly.Config.Switches[sw1].Filter.Bindings[0].Set", Producer: "filter", Setup: "", Before: `"ingress"`, After: `"ingress2"`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].@key", Producer: "filter", Setup: "", Before: `"ingress"`, After: `"ingress2"`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.Protocol.@present", Producer: "filter", Setup: "", Before: `6`, After: `null`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.ICMP.@present", Producer: "filter", Setup: "", Before: `{"Type":8,"Code":0}`, After: `null`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.ICMP.Code.@present", Producer: "filter", Setup: "", Before: `0`, After: `null`},
		{Path: "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.TCPFlags.@present", Producer: "filter", Setup: "", Before: `{"Mask":2,"Value":2}`, After: `null`},
	}
}

func assemblySwitchRoutingEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Routing.ClearRouterFlag", Producer: "routing", Setup: "", Before: `false`, After: `true`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Interfaces[vlan10].VLAN", Producer: "routing", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Interfaces[vlan10].Port", Producer: "routing", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Interfaces[vlan10].MAC", Producer: "routing", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Interfaces[vlan10].Prefixes[0]", Producer: "routing", Setup: "", Before: `"10.0.10.1/24"`, After: `"192.0.2.0/25"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Routes[0].Prefix", Producer: "routing", Setup: "", Before: `"10.0.20.0/24"`, After: `"192.0.2.0/25"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Routes[0].NextHop", Producer: "routing", Setup: "", Before: `"10.0.10.7"`, After: `"10.0.10.8"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Routes[0].Interface", Producer: "routing", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Routes[0].Preference", Producer: "routing", Setup: "", Before: `1`, After: `2`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Routes[0].Metric", Producer: "routing", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Neighbors[0].Interface", Producer: "routing", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Neighbors[0].Addr", Producer: "routing", Setup: "", Before: `"10.0.10.7"`, After: `"10.0.10.8"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Neighbors[0].MAC", Producer: "routing", Setup: "", Before: `"02:00:00:00:00:02"`, After: `"02:00:00:00:00:03"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].NeighborPolicy.Mode", Producer: "routing", Setup: "", Before: `"disabled"`, After: `""`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].NeighborPolicy.ReachableTime", Producer: "routing", Setup: "", Before: `"30s"`, After: `"31s"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].NeighborPolicy.RetransTimer", Producer: "routing", Setup: "", Before: `"3s"`, After: `"4s"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].NeighborPolicy.HoldDepth", Producer: "routing", Setup: "", Before: `4`, After: `5`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].@key", Producer: "routing", Setup: "", Before: `"default"`, After: `"default2"`},
		{Path: "assembly.Config.Switches[sw1].Routing.VRFs[default].Interfaces[vlan10].@key", Producer: "routing", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
	}
}

func assemblySwitchTrafficEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Traffic.Mirrors[0].Name", Producer: "traffic", Setup: "", Before: `"m1"`, After: `"m2"`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Mirrors[0].SelectAll", Producer: "traffic", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Mirrors[0].OutputPort", Producer: "traffic", Setup: "", Before: `"1/1/3"`, After: `"1/1/4"`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Mirrors[0].OutputVLAN", Producer: "traffic", Setup: "", Before: `20`, After: `21`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Mirrors[0].SnapLen", Producer: "traffic", Setup: "", Before: `128`, After: `129`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Mirrors[0].SelectSrcPorts[0]", Producer: "traffic", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Mirrors[0].SelectDstPorts[0]", Producer: "traffic", Setup: "", Before: `"1/1/2"`, After: `"1/1/3"`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Mirrors[0].SelectVLANs[0]", Producer: "traffic", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Policers[1/1/1].RateBPS", Producer: "traffic", Setup: "", Before: `1000000`, After: `1000001`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Policers[1/1/1].BurstOctets", Producer: "traffic", Setup: "", Before: `1500`, After: `1501`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Queues[1/1/1].MaxRateBPS[0]", Producer: "traffic", Setup: "", Before: `1000000`, After: `1000001`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Queues[1/1/1].BufferOctets[0]", Producer: "traffic", Setup: "", Before: `4096`, After: `4097`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Mirrors[0].OutputVLAN.@present", Producer: "traffic", Setup: "", Before: `20`, After: `null`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Policers[1/1/1].@key", Producer: "traffic", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Queues[1/1/1].@key", Producer: "traffic", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Queues[1/1/1].MaxRateBPS[0].@key", Producer: "traffic", Setup: "", Before: `0`, After: `1`},
		{Path: "assembly.Config.Switches[sw1].Traffic.Queues[1/1/1].BufferOctets[0].@key", Producer: "traffic", Setup: "", Before: `0`, After: `1`},
	}
}

func assemblySwitchPortsEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].Name", Producer: "port", Setup: "port-wrapper", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].IfIndex", Producer: "port", Setup: "port-wrapper", Before: `1`, After: `2`},
		{Path: "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].Kind", Producer: "port", Setup: "port-wrapper", Before: `"Other"`, After: `"Physical"`},
		{Path: "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].AdminStatus", Producer: "port", Setup: "port-wrapper", Before: `"Up"`, After: `"Down"`},
		{Path: "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].OperStatus", Producer: "port", Setup: "port-wrapper", Before: `"Up"`, After: `"Down"`},
		{Path: "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].MTU", Producer: "port", Setup: "port-wrapper", Before: `1500`, After: `1501`},
		{Path: "assembly.Config.Switches[sw1].Ports.Ports()[1/1/1].LagParent", Producer: "port", Setup: "port-wrapper", Before: `"lag1"`, After: `""`},
	}
}

func assemblySwitchMACEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].MAC", Producer: "vswitch", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
	}
}

func assemblySwitchPhyPresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Phy.@present", Producer: "vswitch", Setup: "", Before: `{"Ethernet":{},"PoE":{"Groups":{},"Ports":{}}}`, After: `null`},
	}
}

func assemblySwitchBridgePresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Bridge.@present", Producer: "vswitch", Setup: "", Before: `{"VLAN":{"Table":{},"Switchports":{}}}`, After: `null`},
	}
}

func assemblySwitchLAGPresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].LAG.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
	}
}

func assemblySwitchSTPPresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].STP.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
	}
}

func assemblySwitchLoopProtectPresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].LoopProtect.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
	}
}

func assemblySwitchMcastPresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Mcast.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
	}
}

func assemblySwitchRoutingPresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Routing.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
	}
}

func assemblySwitchTrafficPresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Traffic.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
	}
}

func assemblySwitchFilterPresenceEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].Filter.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
	}
}

func assemblyStartEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Start", Producer: "assembly", Setup: "", Before: `"2026-09-10T12:00:00Z"`, After: `"2026-09-10T12:00:01Z"`},
	}
}

func assemblyHostsEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Hosts[h1].Address", Producer: "assembly", Setup: "", Before: `"02:00:00:00:00:02"`, After: `"02:00:00:00:00:05"`},
		{Path: "assembly.Config.Hosts[h1].VLAN", Producer: "assembly", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Hosts[h1].VLAN.@present", Producer: "assembly", Setup: "", Before: `10`, After: `null`},
		{Path: "assembly.Config.Hosts[h1].IP.Addresses[0]", Producer: "assembly", Setup: "", Before: `"10.0.10.7/24"`, After: `"192.0.2.7/24"`},
		{Path: "assembly.Config.Hosts[h1].IP.Gateway", Producer: "assembly", Setup: "", Before: `"10.0.10.1"`, After: `"10.0.10.2"`},
		{Path: "assembly.Config.Hosts[h1].IP.Neighbors[10.0.10.1]", Producer: "assembly", Setup: "", Before: `"02:00:00:00:00:03"`, After: `"02:00:00:00:00:05"`},
		{Path: "assembly.Config.Hosts[h1].IP.Neighbors[10.0.10.1].@key", Producer: "assembly", Setup: "", Before: `"10.0.10.1"`, After: `"10.0.10.2"`},
		{Path: "assembly.Config.Hosts[h1].IP.@present", Producer: "assembly", Setup: "", Before: `{"Addresses":["10.0.10.7/24"],"Gateway":"10.0.10.1","Neighbors":{"10.0.10.1":"02:00:00:00:00:03"}}`, After: `null`},
		{Path: "assembly.Config.Hosts[h1].Accept.Promiscuous", Producer: "assembly", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Hosts[h1].Accept.AllMulticast", Producer: "assembly", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Hosts[h1].Accept.Multicast[0]", Producer: "assembly", Setup: "", Before: `"01:00:5e:00:00:01"`, After: `"01:00:5e:00:00:02"`},
		{Path: "assembly.Config.Hosts[h1].Ethernet.Setting.SpeedBPS", Producer: "assembly", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "assembly.Config.Hosts[h1].Ethernet.Setting.Duplex", Producer: "assembly", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "assembly.Config.Hosts[h1].Ethernet.Setting.AutoNegotiation", Producer: "assembly", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Hosts[h1].Ethernet.AutoNegotiationSupported", Producer: "assembly", Setup: "", Before: `"Supported"`, After: `"Unsupported"`},
		{Path: "assembly.Config.Hosts[h1].Ethernet.Observed.SpeedBPS", Producer: "assembly", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "assembly.Config.Hosts[h1].Ethernet.Observed.Duplex", Producer: "assembly", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "assembly.Config.Hosts[h1].Ethernet.SupportedSpeedsBPS[0]", Producer: "assembly", Setup: "", Before: `1000000000`, After: `10000000000`},
		{Path: "assembly.Config.Hosts[h1].Ethernet.Setting.@present", Producer: "assembly", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true}`, After: `null`},
		{Path: "assembly.Config.Hosts[h1].Ethernet.Observed.@present", Producer: "assembly", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full"}`, After: `null`},
	}
}

func assemblyReflectorsEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Reflectors[r1].Ports[p1].Setting.SpeedBPS", Producer: "assembly", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "assembly.Config.Reflectors[r1].Ports[p1].Setting.Duplex", Producer: "assembly", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "assembly.Config.Reflectors[r1].Ports[p1].Setting.AutoNegotiation", Producer: "assembly", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.Reflectors[r1].Ports[p1].AutoNegotiationSupported", Producer: "assembly", Setup: "", Before: `"Supported"`, After: `"Unsupported"`},
		{Path: "assembly.Config.Reflectors[r1].Ports[p1].Observed.SpeedBPS", Producer: "assembly", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "assembly.Config.Reflectors[r1].Ports[p1].Observed.Duplex", Producer: "assembly", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "assembly.Config.Reflectors[r1].Ports[p1].SupportedSpeedsBPS[0]", Producer: "assembly", Setup: "", Before: `1000000000`, After: `10000000000`},
		{Path: "assembly.Config.Reflectors[r1].Ports[p1].Setting.@present", Producer: "assembly", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true}`, After: `null`},
		{Path: "assembly.Config.Reflectors[r1].Ports[p1].Observed.@present", Producer: "assembly", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full"}`, After: `null`},
	}
}

func assemblyPhyAssumptionEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.PhyAssumption.Ethernet.Setting.SpeedBPS", Producer: "assembly", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "assembly.Config.PhyAssumption.Ethernet.Setting.Duplex", Producer: "assembly", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "assembly.Config.PhyAssumption.Ethernet.Setting.AutoNegotiation", Producer: "assembly", Setup: "", Before: `true`, After: `false`},
		{Path: "assembly.Config.PhyAssumption.Ethernet.AutoNegotiationSupported", Producer: "assembly", Setup: "", Before: `"Supported"`, After: `"Unsupported"`},
		{Path: "assembly.Config.PhyAssumption.Ethernet.Observed.SpeedBPS", Producer: "assembly", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "assembly.Config.PhyAssumption.Ethernet.Observed.Duplex", Producer: "assembly", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "assembly.Config.PhyAssumption.Ethernet.SupportedSpeedsBPS[0]", Producer: "assembly", Setup: "", Before: `1000000000`, After: `10000000000`},
		{Path: "assembly.Config.PhyAssumption.Ethernet.Setting.@present", Producer: "assembly", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true}`, After: `null`},
		{Path: "assembly.Config.PhyAssumption.Ethernet.Observed.@present", Producer: "assembly", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full"}`, After: `null`},
	}
}

func assemblyReflectorMemberEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Reflectors[r1].Address", Producer: "assembly", Setup: "", Before: `"02:00:00:00:00:04"`, After: `"02:00:00:00:00:05"`},
		{Path: "assembly.Config.Reflectors[r1].Ports[p1].@key", Producer: "assembly", Setup: "", Before: `"p1"`, After: `"p2"`},
		{Path: "assembly.Config.Reflectors[r1].Attachments[p1].Port", Producer: "assembly", Setup: "", Before: `"p1"`, After: `"p2"`},
		{Path: "assembly.Config.Reflectors[r1].Attachments[p1].VLAN", Producer: "assembly", Setup: "", Before: `10`, After: `11`},
		{Path: "assembly.Config.Reflectors[r1].Attachments[p1].VLAN.@present", Producer: "assembly", Setup: "", Before: `10`, After: `null`},
		{Path: "assembly.Config.Reflectors[r1].Attachments[p1].Addresses[0]", Producer: "assembly", Setup: "", Before: `"10.0.20.7/24"`, After: `"192.0.2.7/24"`},
		{Path: "assembly.Config.Reflectors[r1].Attachments[p1].@key", Producer: "assembly", Setup: "", Before: `"p1"`, After: `"p2"`},
	}
}

func assemblyCablesEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Cables[0].Cable.LengthMeters", Producer: "assembly", Setup: "", Before: `3`, After: `4`},
		{Path: "assembly.Config.Cables[0].Cable.Medium", Producer: "assembly", Setup: "", Before: `"TwistedPair"`, After: `"MultimodeFiber"`},
		{Path: "assembly.Config.Cables[0].Cable.Delay", Producer: "assembly", Setup: "", Before: `"2ms"`, After: `"3ms"`},
		{Path: "assembly.Config.Cables[0].Cable.TopSpeedBPS", Producer: "assembly", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "assembly.Config.Cables[0].Cable.Fault.Kind", Producer: "assembly", Setup: "", Before: `"LoseEveryNth"`, After: `"CorruptEveryNth"`},
		{Path: "assembly.Config.Cables[0].Cable.Fault.N", Producer: "assembly", Setup: "", Before: `5`, After: `6`},
		{Path: "assembly.Config.Cables[0].Cable.Fault.Sequence[0]", Producer: "assembly", Setup: "sequence", Before: `1`, After: `2`},
		{Path: "assembly.Config.Cables[0].Cable.Delay.@present", Producer: "assembly", Setup: "", Before: `"2ms"`, After: `null`},
		{Path: "assembly.Config.Cables[0].A.Node", Producer: "assembly", Setup: "", Before: `"sw1"`, After: `"sw2"`},
		{Path: "assembly.Config.Cables[0].A.Port", Producer: "assembly", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "assembly.Config.Cables[0].B.Node", Producer: "assembly", Setup: "", Before: `"h1"`, After: `"h2"`},
		{Path: "assembly.Config.Cables[0].B.Port", Producer: "assembly", Setup: "", Before: `""`, After: `"p1"`},
		{Path: "assembly.Config.Cables[0].Evidence[0]", Producer: "assembly", Setup: "", Before: `"survey:rack-walk"`, After: `"survey:changed"`, Reason: "Evidence is provenance excluded from Diff"},
	}
}

func assemblyUncabledEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Uncabled[0].Endpoint.Node", Producer: "assembly", Setup: "", Before: `"sw1"`, After: `"sw2"`},
		{Path: "assembly.Config.Uncabled[0].Endpoint.Port", Producer: "assembly", Setup: "", Before: `"1/1/2"`, After: `"1/1/3"`},
		{Path: "assembly.Config.Uncabled[0].Evidence[0]", Producer: "assembly", Setup: "", Before: `"survey:empty-port"`, After: `"survey:changed"`, Reason: "Evidence is provenance excluded from Diff"},
	}
}

func assemblyPhyAssumptionMediumEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.PhyAssumption.Medium", Producer: "assembly", Setup: "", Before: `"MultimodeFiber"`, After: `"TwistedPair"`},
		{Path: "assembly.Config.PhyAssumption.@present", Producer: "assembly", Setup: "", Before: `{"Medium":"MultimodeFiber","Ethernet":{"SupportedSpeedsBPS":[1000000000],"AutoNegotiationSupported":"Supported","Setting":{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true},"Observed":{"SpeedBPS":1000000000,"Duplex":"Full"}}}`, After: `null`},
	}
}

func assemblyHostIdentityEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Hosts[h1].@key", Producer: "assembly", Setup: "", Before: `"h1"`, After: `"h2"`},
	}
}

func assemblyReflectorIdentityEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Reflectors[r1].@key", Producer: "assembly", Setup: "", Before: `"r1"`, After: `"r2"`},
	}
}

func assemblySwitchIdentityEntries() []mappedEntry {
	return []mappedEntry{
		{Path: "assembly.Config.Switches[sw1].@key", Producer: "assembly", Setup: "switch-key", Before: `"sw1"`, After: `"sw2"`},
	}
}

func assemblySwitchSTPMSTExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
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
	case "assembly.Config.Switches[sw1].STP.MST.@present":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "\"sw1\"/\"\"", "mst.presence", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
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
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func assemblySwitchFilterExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
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
	case "assembly.Config.Switches[sw1].Filter.@present":
		*hits++
		return []changeRecord{
			{"filter", "capability", "\"sw1\"/\"filter\"", "presence", &[2]string{"vswitch.layer", "filter"}, nil},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func assemblySwitchPortsExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
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
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func assemblySwitchMACExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "assembly.Config.Switches[sw1].MAC":
		*hits++
		return []changeRecord{
			{"port", "device", "\"sw1\"/\"\"", "mac", &[2]string{"vswitch.mac", "02:00:00:00:00:01"}, &[2]string{"vswitch.mac", "02:00:00:00:00:02"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func assemblySwitchPhyPresenceExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "assembly.Config.Switches[sw1].Phy.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "capability", "\"sw1\"/\"ethernet\"", "presence", &[2]string{"vswitch.layer", "ethernet"}, nil},
			{"poe", "capability", "\"sw1\"/\"poe\"", "presence", &[2]string{"vswitch.layer", "poe"}, nil},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func assemblyStartExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "assembly.Config.Start":
		*hits++
		return []changeRecord{
			{"fabric", "fabric", "", "start", &[2]string{"fabric.start", "2026-09-10T12:00:00Z"}, &[2]string{"fabric.start", "2026-09-10T12:00:01Z"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func assemblyHostsExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
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
	case "assembly.Config.Hosts[h1].@key":
		*hits++
		return []changeRecord{
			{"fabric", "host", "h1", "", &[2]string{"fabric.host", "address=\"02:00:00:00:00:02\";ethernet=\"speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\\\"Full\\\",autoneg=true},observed={speed=1000000000,duplex=\\\"Full\\\"}\";accept.promiscuous=\"true\";accept.all_multicast=\"true\";accept.multicast=[\"01:00:5e:00:00:01\"];vlan=10;ip=present;addresses=[\"10.0.10.7/24\"];gateway=\"10.0.10.1\";neighbors=[\"10.0.10.1\"=\"02:00:00:00:00:03\"];"}, nil},
			{"fabric", "host", "h2", "", nil, &[2]string{"fabric.host", "address=\"02:00:00:00:00:02\";ethernet=\"speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\\\"Full\\\",autoneg=true},observed={speed=1000000000,duplex=\\\"Full\\\"}\";accept.promiscuous=\"true\";accept.all_multicast=\"true\";accept.multicast=[\"01:00:5e:00:00:01\"];vlan=10;ip=present;addresses=[\"10.0.10.7/24\"];gateway=\"10.0.10.1\";neighbors=[\"10.0.10.1\"=\"02:00:00:00:00:03\"];"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func assemblyPhyAssumptionExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
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
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func assemblyUncabledExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
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
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}

func assignMappedPortTable(v reflect.Value, value any) error {
	var ports []port.Port
	p := reflect.ValueOf(&ports).Elem()
	if err := assignMappedValue(p, value); err != nil {
		return err
	}
	b := port.NewBuilder()
	for _, p := range ports {
		b.Add(p)
	}
	table, err := b.Build()
	if err != nil {
		return err
	}
	v.Set(reflect.ValueOf(table))
	return nil
}

func assignMappedSpecialString(v reflect.Value, s string) (bool, error) {
	switch v.Type() {
	case reflect.TypeOf(time.Duration(0)):
		d, err := time.ParseDuration(s)
		if err != nil {
			return true, err
		}
		v.SetInt(int64(d))
		return true, nil
	case reflect.TypeOf(time.Time{}):
		d, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return true, err
		}
		v.Set(reflect.ValueOf(d))
		return true, nil
	case reflect.TypeOf(netaddr.MAC{}):
		d, err := netaddr.Parse(s)
		if err != nil {
			return true, err
		}
		v.Set(reflect.ValueOf(d))
		return true, nil
	case reflect.TypeOf(netip.Addr{}):
		d, err := netip.ParseAddr(s)
		if err != nil {
			return true, err
		}
		v.Set(reflect.ValueOf(d))
		return true, nil
	case reflect.TypeOf(netip.Prefix{}):
		d, err := netip.ParsePrefix(s)
		if err != nil {
			return true, err
		}
		v.Set(reflect.ValueOf(d))
		return true, nil
	}
	return false, nil
}
