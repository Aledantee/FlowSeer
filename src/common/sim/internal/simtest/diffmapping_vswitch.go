package simtest

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
)

func vswitchDiffMapping() (diffMapping, error) {
	return newDiffMapping("vswitch", vswitch.Config{}, `{"MAC":"02:00:00:00:00:01","Ports":[{"Name":"1/1/1","IfIndex":1,"Kind":"Other","AdminStatus":"Up","OperStatus":"Up","MTU":1500,"LagParent":""}],"Bridge":{"AgingTime":"300s","MaxEntries":1024,"FloodVLANs":[10],"ProtectedPorts":["1/1/1"],"ForwardBPDU":true,"VLAN":{"Table":{"10":"ten"},"Switchports":{"1/1/1":{"PVID":10,"Tagged":[20],"Untagged":[10],"IngressFiltering":true,"Admission":"TaggedOnly","Tunnel":{"VID":30,"CustomerVIDs":[100],"TPID":34984},"PriorityTags":"IfNonzero"}}}},"Phy":{"Ethernet":{"1/1/1":{"SupportedSpeedsBPS":[1000000000],"AutoNegotiationSupported":"Supported","Setting":{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true},"Observed":{"SpeedBPS":1000000000,"Duplex":"Full"}}},"PoE":{"Groups":{"g1":{"PowerNanowatts":370000000000}},"Ports":{"1/1/1":{"Group":"g1","MaxClass":4,"Enabled":true,"Limit":5000000000,"Priority":"High","PD":"Attached","PDClass":4}}}},"LoopProtect":{"Interval":"5s","Ports":{"1/1/1":{"Action":"Block","Recovery":{"Mode":"Timer","Duration":"30s"},"VLANs":[10]}}},"Mcast":{"VLANs":{"10":{"FloodUnregistered":true,"FastLeave":true,"RouterPorts":["1/1/1"],"MembershipInterval":"260s","RouterPortInterval":"260s","LastMemberQueryInterval":"1s","LastMemberQueryCount":2}}},"LAG":{"LAGs":{"lag1":{"Mode":"BalanceSLB","Primary":"1/1/1","UpDelay":"2s","DownDelay":"1s","HashBasis":7,"MinLinks":1,"RebalanceInterval":"45s","LACP":{"Mode":"Active","Fast":true,"SystemPriority":32768,"SystemID":"02:00:00:00:00:01","Key":1,"Fallback":true},"Members":{"1/1/1":{"Priority":128,"Key":1}}}}},"STP":{"Priority":0,"PriorityPresent":true,"Address":"02:00:00:00:00:01","HelloTime":"2s","MaxAge":"20s","ForwardDelay":"15s","TxHoldCount":6,"Ports":{"1/1/1":{"Priority":0,"PriorityPresent":true,"PathCost":20000,"AdminEdge":true,"AutoEdge":true,"PointToPoint":"ForceTrue","BPDUGuard":true,"RestrictedRole":true,"RestrictedTCN":true,"LoopGuard":true}},"MST":{"Name":"region1","Revision":1,"MaxHops":20,"Instances":{"1":{"Priority":0,"PriorityPresent":true,"VLANs":[10],"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}},"PVST":{"Trees":{"10":{"Priority":8192,"PriorityPresent":true,"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}}},"Filter":{"Sets":{"ingress":{"Stateful":true,"Default":"drop","Rules":[{"Name":"rule1","Action":"accept","Match":{"Protocol":6,"Src":["10.0.1.0/24"],"Dst":["10.0.2.0/24"],"SrcPorts":[{"Start":1000,"End":2000}],"DstPorts":[{"Start":80,"End":80}],"ICMP":{"Type":8,"Code":0},"TCPFlags":{"Mask":2,"Value":2}}}]}},"Bindings":[{"Interface":"vlan10","Direction":"in","Set":"ingress"}]},"Routing":{"ClearRouterFlag":false,"VRFs":{"default":{"Interfaces":{"vlan10":{"VLAN":10,"Port":"1/1/1","MAC":"02:00:00:00:00:01","Prefixes":["10.0.10.1/24"]}},"Routes":[{"Prefix":"10.0.20.0/24","NextHop":"10.0.10.7","Interface":"vlan10","Preference":1,"Metric":10}],"Neighbors":[{"Interface":"vlan10","Addr":"10.0.10.7","MAC":"02:00:00:00:00:02"}],"NeighborPolicy":{"Mode":"disabled","ReachableTime":"30s","RetransTimer":"3s","HoldDepth":4}}}},"Traffic":{"Mirrors":[{"Name":"m1","SelectAll":true,"SelectSrcPorts":["1/1/1"],"SelectDstPorts":["1/1/2"],"SelectVLANs":[10],"OutputPort":"1/1/3","OutputVLAN":20,"SnapLen":128}],"Policers":{"1/1/1":{"RateBPS":1000000,"BurstOctets":1500}},"Queues":{"1/1/1":{"MaxRateBPS":{"0":1000000},"BufferOctets":{"0":4096}}}}}`, vswitch.Config.Normalize, vswitch.Diff, []mappedEntry{
		{Path: "vswitch.Config.Bridge.AgingTime", Producer: "bridge", Setup: "", Before: `"300s"`, After: `"301s"`},
		{Path: "vswitch.Config.Bridge.MaxEntries", Producer: "bridge", Setup: "", Before: `1024`, After: `1025`},
		{Path: "vswitch.Config.Bridge.FloodVLANs[0]", Producer: "bridge", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.Bridge.ProtectedPorts[0]", Producer: "bridge", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Bridge.ForwardBPDU", Producer: "bridge", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.Bridge.VLAN.Table[10]", Producer: "bridge", Setup: "", Before: `"ten"`, After: `"eleven"`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].PVID", Producer: "bridge", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tagged[0]", Producer: "bridge", Setup: "", Before: `20`, After: `21`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Untagged[0]", Producer: "bridge", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].IngressFiltering", Producer: "bridge", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Admission", Producer: "bridge", Setup: "", Before: `"TaggedOnly"`, After: `"All"`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].PriorityTags", Producer: "bridge", Setup: "", Before: `"IfNonzero"`, After: `"Never"`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.VID", Producer: "bridge", Setup: "", Before: `30`, After: `31`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.CustomerVIDs[0]", Producer: "bridge", Setup: "", Before: `100`, After: `101`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.TPID", Producer: "bridge", Setup: "", Before: `0`, After: `34984`, Reason: "TPID zero and 34984 have the same effective value"},
		{Path: "vswitch.Config.Bridge.VLAN.@present", Producer: "bridge", Setup: "", Before: `{"Table":{"10":"ten"},"Switchports":{"1/1/1":{"PVID":10,"Tagged":[20],"Untagged":[10],"IngressFiltering":true,"Admission":"TaggedOnly","Tunnel":{"VID":30,"CustomerVIDs":[100],"TPID":34984},"PriorityTags":"IfNonzero"}}}`, After: `null`},
		{Path: "vswitch.Config.Bridge.VLAN.Table[10].@key", Producer: "bridge", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].@key", Producer: "bridge", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].PVID.@present", Producer: "bridge", Setup: "", Before: `10`, After: `null`},
		{Path: "vswitch.Config.Bridge.VLAN.Switchports[1/1/1].Tunnel.@present", Producer: "bridge", Setup: "", Before: `{"VID":30,"CustomerVIDs":[100],"TPID":34984}`, After: `null`},
		{Path: "vswitch.Config.Phy.Ethernet[1/1/1].Setting.SpeedBPS", Producer: "phy", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "vswitch.Config.Phy.Ethernet[1/1/1].Setting.Duplex", Producer: "phy", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "vswitch.Config.Phy.Ethernet[1/1/1].Setting.AutoNegotiation", Producer: "phy", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.Phy.Ethernet[1/1/1].AutoNegotiationSupported", Producer: "phy", Setup: "", Before: `"Supported"`, After: `"Unsupported"`},
		{Path: "vswitch.Config.Phy.Ethernet[1/1/1].Observed.SpeedBPS", Producer: "phy", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "vswitch.Config.Phy.Ethernet[1/1/1].Observed.Duplex", Producer: "phy", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "vswitch.Config.Phy.Ethernet[1/1/1].SupportedSpeedsBPS[0]", Producer: "phy", Setup: "", Before: `1000000000`, After: `10000000000`},
		{Path: "vswitch.Config.Phy.PoE.Groups[g1].PowerNanowatts", Producer: "phy", Setup: "", Before: `370000000000`, After: `370000000001`},
		{Path: "vswitch.Config.Phy.PoE.Ports[1/1/1].Group", Producer: "phy", Setup: "", Before: `"g1"`, After: `"g2"`},
		{Path: "vswitch.Config.Phy.PoE.Ports[1/1/1].MaxClass", Producer: "phy", Setup: "", Before: `4`, After: `5`},
		{Path: "vswitch.Config.Phy.PoE.Ports[1/1/1].Enabled", Producer: "phy", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.Phy.PoE.Ports[1/1/1].Limit", Producer: "phy", Setup: "", Before: `5000000000`, After: `5000000001`},
		{Path: "vswitch.Config.Phy.PoE.Ports[1/1/1].Priority", Producer: "phy", Setup: "", Before: `"High"`, After: `"Low"`},
		{Path: "vswitch.Config.Phy.PoE.Ports[1/1/1].PD", Producer: "phy", Setup: "", Before: `"Attached"`, After: `"Absent"`},
		{Path: "vswitch.Config.Phy.PoE.Ports[1/1/1].PDClass", Producer: "phy", Setup: "", Before: `4`, After: `5`},
		{Path: "vswitch.Config.Phy.Ethernet[1/1/1].@key", Producer: "phy", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Phy.Ethernet[1/1/1].Setting.@present", Producer: "phy", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true}`, After: `null`},
		{Path: "vswitch.Config.Phy.Ethernet[1/1/1].Observed.@present", Producer: "phy", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full"}`, After: `null`},
		{Path: "vswitch.Config.Phy.PoE.@present", Producer: "phy", Setup: "", Before: `{"Groups":{"g1":{"PowerNanowatts":370000000000}},"Ports":{"1/1/1":{"Group":"g1","MaxClass":4,"Enabled":true,"Limit":5000000000,"Priority":"High","PD":"Attached","PDClass":4}}}`, After: `null`},
		{Path: "vswitch.Config.Phy.PoE.Groups[g1].@key", Producer: "phy", Setup: "", Before: `"g1"`, After: `"g2"`},
		{Path: "vswitch.Config.Phy.PoE.Ports[1/1/1].@key", Producer: "phy", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Phy.PoE.Ports[1/1/1].Limit.@present", Producer: "phy", Setup: "", Before: `5000000000`, After: `null`},
		{Path: "vswitch.Config.Phy.PoE.Ports[1/1/1].PDClass.@present", Producer: "phy", Setup: "", Before: `4`, After: `null`},
		{Path: "vswitch.Config.LoopProtect.Interval", Producer: "loopprotect", Setup: "", Before: `"5s"`, After: `"6s"`},
		{Path: "vswitch.Config.LoopProtect.Ports[1/1/1].Action", Producer: "loopprotect", Setup: "", Before: `"Block"`, After: `"Disable"`},
		{Path: "vswitch.Config.LoopProtect.Ports[1/1/1].Recovery.Mode", Producer: "loopprotect", Setup: "", Before: `"Timer"`, After: `"Manual"`},
		{Path: "vswitch.Config.LoopProtect.Ports[1/1/1].Recovery.Duration", Producer: "loopprotect", Setup: "", Before: `"30s"`, After: `"31s"`},
		{Path: "vswitch.Config.LoopProtect.Ports[1/1/1].VLANs[0]", Producer: "loopprotect", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.LoopProtect.Ports[1/1/1].@key", Producer: "loopprotect", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Mcast.VLANs[10].FloodUnregistered", Producer: "mcast", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.Mcast.VLANs[10].FastLeave", Producer: "mcast", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.Mcast.VLANs[10].MembershipInterval", Producer: "mcast", Setup: "", Before: `"260s"`, After: `"261s"`},
		{Path: "vswitch.Config.Mcast.VLANs[10].RouterPortInterval", Producer: "mcast", Setup: "", Before: `"260s"`, After: `"261s"`},
		{Path: "vswitch.Config.Mcast.VLANs[10].LastMemberQueryInterval", Producer: "mcast", Setup: "", Before: `"1s"`, After: `"2s"`},
		{Path: "vswitch.Config.Mcast.VLANs[10].LastMemberQueryCount", Producer: "mcast", Setup: "", Before: `2`, After: `3`},
		{Path: "vswitch.Config.Mcast.VLANs[10].RouterPorts[0]", Producer: "mcast", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Mcast.VLANs[10].@key", Producer: "mcast", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.Mcast.VLANs[10].FloodUnregistered.@present", Producer: "mcast", Setup: "", Before: `true`, After: `null`, Reason: "Nil flood policy and true have the same effective value"},
		{Path: "vswitch.Config.LAG.LAGs[lag1].Mode", Producer: "lag", Setup: "", Before: `"BalanceSLB"`, After: `"BalanceTCP"`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].Primary", Producer: "lag", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].UpDelay", Producer: "lag", Setup: "", Before: `"2s"`, After: `"3s"`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].DownDelay", Producer: "lag", Setup: "", Before: `"1s"`, After: `"2s"`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].HashBasis", Producer: "lag", Setup: "", Before: `7`, After: `8`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].MinLinks", Producer: "lag", Setup: "", Before: `1`, After: `2`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].RebalanceInterval", Producer: "lag", Setup: "", Before: `"45s"`, After: `"46s"`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].LACP.Mode", Producer: "lag", Setup: "", Before: `"Active"`, After: `"Passive"`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].LACP.Fast", Producer: "lag", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].LACP.SystemPriority", Producer: "lag", Setup: "", Before: `32768`, After: `32769`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].LACP.SystemID", Producer: "lag", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].LACP.Key", Producer: "lag", Setup: "", Before: `1`, After: `2`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].LACP.Fallback", Producer: "lag", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].Members[1/1/1].Priority", Producer: "lag", Setup: "", Before: `128`, After: `129`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].Members[1/1/1].Key", Producer: "lag", Setup: "", Before: `1`, After: `2`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].@key", Producer: "lag", Setup: "", Before: `"lag1"`, After: `"lag2"`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].RebalanceInterval.@present", Producer: "lag", Setup: "", Before: `"45s"`, After: `null`},
		{Path: "vswitch.Config.LAG.LAGs[lag1].Members[1/1/1].@key", Producer: "lag", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.STP.Priority", Producer: "stp", Setup: "", Before: `0`, After: `4096`},
		{Path: "vswitch.Config.STP.Address", Producer: "stp", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
		{Path: "vswitch.Config.STP.HelloTime", Producer: "stp", Setup: "", Before: `"2s"`, After: `"3s"`},
		{Path: "vswitch.Config.STP.MaxAge", Producer: "stp", Setup: "", Before: `"20s"`, After: `"21s"`},
		{Path: "vswitch.Config.STP.ForwardDelay", Producer: "stp", Setup: "", Before: `"15s"`, After: `"16s"`},
		{Path: "vswitch.Config.STP.TxHoldCount", Producer: "stp", Setup: "", Before: `6`, After: `7`},
		{Path: "vswitch.Config.STP.PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].Priority", Producer: "stp", Setup: "", Before: `0`, After: `16`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].PathCost", Producer: "stp", Setup: "", Before: `20000`, After: `20001`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].AdminEdge", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].AutoEdge", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].PointToPoint", Producer: "stp", Setup: "", Before: `"ForceTrue"`, After: `"ForceFalse"`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].BPDUGuard", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].RestrictedRole", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].RestrictedTCN", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].LoopGuard", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.MST.Name", Producer: "stp", Setup: "", Before: `"region1"`, After: `"region2"`},
		{Path: "vswitch.Config.STP.MST.Revision", Producer: "stp", Setup: "", Before: `1`, After: `2`},
		{Path: "vswitch.Config.STP.MST.MaxHops", Producer: "stp", Setup: "", Before: `20`, After: `21`},
		{Path: "vswitch.Config.STP.MST.Instances[1].Priority", Producer: "stp", Setup: "", Before: `0`, After: `4096`},
		{Path: "vswitch.Config.STP.MST.Instances[1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.MST.Instances[1].VLANs[0]", Producer: "stp", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].Priority", Producer: "stp", Setup: "", Before: `128`, After: `129`},
		{Path: "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].PathCost", Producer: "stp", Setup: "", Before: `20000`, After: `20001`},
		{Path: "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].Priority", Producer: "stp", Setup: "", Before: `128`, After: `129`},
		{Path: "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].PathCost", Producer: "stp", Setup: "", Before: `20000`, After: `20001`},
		{Path: "vswitch.Config.STP.PVST.Trees[10].Priority", Producer: "stp", Setup: "", Before: `8192`, After: `12288`},
		{Path: "vswitch.Config.STP.PVST.Trees[10].PriorityPresent", Producer: "stp", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.STP.Ports[1/1/1].@key", Producer: "stp", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.STP.MST.@present", Producer: "stp", Setup: "", Before: `{"Name":"region1","Revision":1,"MaxHops":20,"Instances":{"1":{"Priority":0,"PriorityPresent":true,"VLANs":[10],"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}}`, After: `null`},
		{Path: "vswitch.Config.STP.PVST.@present", Producer: "stp", Setup: "", Before: `{"Trees":{"10":{"Priority":8192,"PriorityPresent":true,"Ports":{"1/1/1":{"Priority":128,"PriorityPresent":true,"PathCost":20000}}}}}`, After: `null`},
		{Path: "vswitch.Config.STP.MST.Instances[1].@key", Producer: "stp", Setup: "", Before: `1`, After: `2`},
		{Path: "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].@key", Producer: "stp", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.STP.PVST.Trees[10].@key", Producer: "stp", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.STP.PVST.Trees[10].Ports[1/1/1].@key", Producer: "stp", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Stateful", Producer: "filter", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Default", Producer: "filter", Setup: "", Before: `"drop"`, After: `"accept"`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Name", Producer: "filter", Setup: "", Before: `"rule1"`, After: `"rule2"`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Action", Producer: "filter", Setup: "", Before: `"accept"`, After: `"drop"`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Protocol", Producer: "filter", Setup: "", Before: `6`, After: `17`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Src[0]", Producer: "filter", Setup: "", Before: `"10.0.1.0/24"`, After: `"192.0.2.0/25"`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Dst[0]", Producer: "filter", Setup: "", Before: `"10.0.2.0/24"`, After: `"192.0.2.0/25"`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.SrcPorts[0].Start", Producer: "filter", Setup: "", Before: `1000`, After: `1001`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.SrcPorts[0].End", Producer: "filter", Setup: "", Before: `2000`, After: `2001`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.DstPorts[0].Start", Producer: "filter", Setup: "", Before: `80`, After: `81`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.DstPorts[0].End", Producer: "filter", Setup: "", Before: `80`, After: `81`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.Type", Producer: "filter", Setup: "", Before: `8`, After: `9`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.Code", Producer: "filter", Setup: "", Before: `0`, After: `1`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.Mask", Producer: "filter", Setup: "", Before: `2`, After: `18`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.Value", Producer: "filter", Setup: "", Before: `2`, After: `0`},
		{Path: "vswitch.Config.Filter.Bindings[0].Interface", Producer: "filter", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
		{Path: "vswitch.Config.Filter.Bindings[0].Direction", Producer: "filter", Setup: "", Before: `"in"`, After: `"out"`},
		{Path: "vswitch.Config.Filter.Bindings[0].Set", Producer: "filter", Setup: "", Before: `"ingress"`, After: `"ingress2"`},
		{Path: "vswitch.Config.Filter.Sets[ingress].@key", Producer: "filter", Setup: "", Before: `"ingress"`, After: `"ingress2"`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Protocol.@present", Producer: "filter", Setup: "", Before: `6`, After: `null`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.@present", Producer: "filter", Setup: "", Before: `{"Type":8,"Code":0}`, After: `null`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.Code.@present", Producer: "filter", Setup: "", Before: `0`, After: `null`},
		{Path: "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.@present", Producer: "filter", Setup: "", Before: `{"Mask":2,"Value":2}`, After: `null`},
		{Path: "vswitch.Config.Routing.ClearRouterFlag", Producer: "routing", Setup: "", Before: `false`, After: `true`},
		{Path: "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].VLAN", Producer: "routing", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].Port", Producer: "routing", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].MAC", Producer: "routing", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
		{Path: "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].Prefixes[0]", Producer: "routing", Setup: "", Before: `"10.0.10.1/24"`, After: `"192.0.2.0/25"`},
		{Path: "vswitch.Config.Routing.VRFs[default].Routes[0].Prefix", Producer: "routing", Setup: "", Before: `"10.0.20.0/24"`, After: `"192.0.2.0/25"`},
		{Path: "vswitch.Config.Routing.VRFs[default].Routes[0].NextHop", Producer: "routing", Setup: "", Before: `"10.0.10.7"`, After: `"10.0.10.8"`},
		{Path: "vswitch.Config.Routing.VRFs[default].Routes[0].Interface", Producer: "routing", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
		{Path: "vswitch.Config.Routing.VRFs[default].Routes[0].Preference", Producer: "routing", Setup: "", Before: `1`, After: `2`},
		{Path: "vswitch.Config.Routing.VRFs[default].Routes[0].Metric", Producer: "routing", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.Routing.VRFs[default].Neighbors[0].Interface", Producer: "routing", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
		{Path: "vswitch.Config.Routing.VRFs[default].Neighbors[0].Addr", Producer: "routing", Setup: "", Before: `"10.0.10.7"`, After: `"10.0.10.8"`},
		{Path: "vswitch.Config.Routing.VRFs[default].Neighbors[0].MAC", Producer: "routing", Setup: "", Before: `"02:00:00:00:00:02"`, After: `"02:00:00:00:00:03"`},
		{Path: "vswitch.Config.Routing.VRFs[default].NeighborPolicy.Mode", Producer: "routing", Setup: "", Before: `"disabled"`, After: `""`},
		{Path: "vswitch.Config.Routing.VRFs[default].NeighborPolicy.ReachableTime", Producer: "routing", Setup: "", Before: `"30s"`, After: `"31s"`},
		{Path: "vswitch.Config.Routing.VRFs[default].NeighborPolicy.RetransTimer", Producer: "routing", Setup: "", Before: `"3s"`, After: `"4s"`},
		{Path: "vswitch.Config.Routing.VRFs[default].NeighborPolicy.HoldDepth", Producer: "routing", Setup: "", Before: `4`, After: `5`},
		{Path: "vswitch.Config.Routing.VRFs[default].@key", Producer: "routing", Setup: "", Before: `"default"`, After: `"default2"`},
		{Path: "vswitch.Config.Routing.VRFs[default].Interfaces[vlan10].@key", Producer: "routing", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
		{Path: "vswitch.Config.Traffic.Mirrors[0].Name", Producer: "traffic", Setup: "", Before: `"m1"`, After: `"m2"`},
		{Path: "vswitch.Config.Traffic.Mirrors[0].SelectAll", Producer: "traffic", Setup: "", Before: `true`, After: `false`},
		{Path: "vswitch.Config.Traffic.Mirrors[0].OutputPort", Producer: "traffic", Setup: "", Before: `"1/1/3"`, After: `"1/1/4"`},
		{Path: "vswitch.Config.Traffic.Mirrors[0].OutputVLAN", Producer: "traffic", Setup: "", Before: `20`, After: `21`},
		{Path: "vswitch.Config.Traffic.Mirrors[0].SnapLen", Producer: "traffic", Setup: "", Before: `128`, After: `129`},
		{Path: "vswitch.Config.Traffic.Mirrors[0].SelectSrcPorts[0]", Producer: "traffic", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Traffic.Mirrors[0].SelectDstPorts[0]", Producer: "traffic", Setup: "", Before: `"1/1/2"`, After: `"1/1/3"`},
		{Path: "vswitch.Config.Traffic.Mirrors[0].SelectVLANs[0]", Producer: "traffic", Setup: "", Before: `10`, After: `11`},
		{Path: "vswitch.Config.Traffic.Policers[1/1/1].RateBPS", Producer: "traffic", Setup: "", Before: `1000000`, After: `1000001`},
		{Path: "vswitch.Config.Traffic.Policers[1/1/1].BurstOctets", Producer: "traffic", Setup: "", Before: `1500`, After: `1501`},
		{Path: "vswitch.Config.Traffic.Queues[1/1/1].MaxRateBPS[0]", Producer: "traffic", Setup: "", Before: `1000000`, After: `1000001`},
		{Path: "vswitch.Config.Traffic.Queues[1/1/1].BufferOctets[0]", Producer: "traffic", Setup: "", Before: `4096`, After: `4097`},
		{Path: "vswitch.Config.Traffic.Mirrors[0].OutputVLAN.@present", Producer: "traffic", Setup: "", Before: `20`, After: `null`},
		{Path: "vswitch.Config.Traffic.Policers[1/1/1].@key", Producer: "traffic", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Traffic.Queues[1/1/1].@key", Producer: "traffic", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Traffic.Queues[1/1/1].MaxRateBPS[0].@key", Producer: "traffic", Setup: "", Before: `0`, After: `1`},
		{Path: "vswitch.Config.Traffic.Queues[1/1/1].BufferOctets[0].@key", Producer: "traffic", Setup: "", Before: `0`, After: `1`},
		{Path: "vswitch.Config.Ports.Ports()[1/1/1].Name", Producer: "port", Setup: "port-wrapper", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "vswitch.Config.Ports.Ports()[1/1/1].IfIndex", Producer: "port", Setup: "port-wrapper", Before: `1`, After: `2`},
		{Path: "vswitch.Config.Ports.Ports()[1/1/1].Kind", Producer: "port", Setup: "port-wrapper", Before: `"Other"`, After: `"Physical"`},
		{Path: "vswitch.Config.Ports.Ports()[1/1/1].AdminStatus", Producer: "port", Setup: "port-wrapper", Before: `"Up"`, After: `"Down"`},
		{Path: "vswitch.Config.Ports.Ports()[1/1/1].OperStatus", Producer: "port", Setup: "port-wrapper", Before: `"Up"`, After: `"Down"`},
		{Path: "vswitch.Config.Ports.Ports()[1/1/1].MTU", Producer: "port", Setup: "port-wrapper", Before: `1500`, After: `1501`},
		{Path: "vswitch.Config.Ports.Ports()[1/1/1].LagParent", Producer: "port", Setup: "port-wrapper", Before: `"lag1"`, After: `""`},
		{Path: "vswitch.Config.MAC", Producer: "vswitch", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
		{Path: "vswitch.Config.Phy.@present", Producer: "vswitch", Setup: "", Before: `{"Ethernet":{},"PoE":{"Groups":{},"Ports":{}}}`, After: `null`},
		{Path: "vswitch.Config.Bridge.@present", Producer: "vswitch", Setup: "", Before: `{"VLAN":{"Table":{},"Switchports":{}}}`, After: `null`},
		{Path: "vswitch.Config.LAG.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
		{Path: "vswitch.Config.STP.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
		{Path: "vswitch.Config.LoopProtect.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
		{Path: "vswitch.Config.Mcast.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
		{Path: "vswitch.Config.Routing.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
		{Path: "vswitch.Config.Traffic.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
		{Path: "vswitch.Config.Filter.@present", Producer: "vswitch", Setup: "", Before: `{}`, After: `null`},
	}, vswitchDiffExpectation)
}

func vswitchDiffExpectation(path string, hits *int) ([]changeRecord, error) {
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
	case "vswitch.Config.LAG.LAGs[lag1].Mode":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "mode", &[2]string{"lag.bond_mode", "BalanceSLB"}, &[2]string{"lag.bond_mode", "BalanceTCP"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].Primary":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "primary", &[2]string{"lag.primary", "1/1/1"}, &[2]string{"lag.primary", "1/1/2"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].UpDelay":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "up_delay", &[2]string{"lag.duration", "2s"}, &[2]string{"lag.duration", "3s"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].DownDelay":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "down_delay", &[2]string{"lag.duration", "1s"}, &[2]string{"lag.duration", "2s"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].HashBasis":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "hash_basis", &[2]string{"lag.hash_basis", "7"}, &[2]string{"lag.hash_basis", "8"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].MinLinks":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "min_links", &[2]string{"lag.min_links", "1"}, &[2]string{"lag.min_links", "2"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].RebalanceInterval":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "rebalance_interval", &[2]string{"lag.rebalance_interval", "45s"}, &[2]string{"lag.rebalance_interval", "46s"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].LACP.Mode":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_mode", &[2]string{"lag.lacp_mode", "Active"}, &[2]string{"lag.lacp_mode", "Passive"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].LACP.Fast":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_fast", &[2]string{"lag.bool", "true"}, &[2]string{"lag.bool", "false"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].LACP.SystemPriority":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_system_priority", &[2]string{"lag.uint16", "32768"}, &[2]string{"lag.uint16", "32769"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].LACP.SystemID":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_system_id", &[2]string{"lag.mac", "02:00:00:00:00:01"}, &[2]string{"lag.mac", "02:00:00:00:00:02"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].LACP.Key":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_key", &[2]string{"lag.uint16", "1"}, &[2]string{"lag.uint16", "2"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].LACP.Fallback":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_fallback", &[2]string{"lag.bool", "true"}, &[2]string{"lag.bool", "false"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].Members[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"lag", "lag_member", "\"lag1\"/\"1/1/1\"", "priority", &[2]string{"lag.port_priority", "128"}, &[2]string{"lag.port_priority", "129"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].Members[1/1/1].Key":
		*hits++
		return []changeRecord{
			{"lag", "lag_member", "\"lag1\"/\"1/1/1\"", "key", &[2]string{"lag.uint16", "1"}, &[2]string{"lag.uint16", "2"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].@key":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "", &[2]string{"lag.lag", "mode=\"BalanceSLB\";primary=\"1/1/1\";up_delay_ns=2000000000;down_delay_ns=1000000000;hash_basis=7;min_links=1;rebalance_interval=45s;lacp={mode=\"Active\";fast=true;system_priority=32768;system_id=\"02:00:00:00:00:01\";key=1;fallback=true};members={\"1/1/1\":{priority=128;key=1}}"}, nil},
			{"lag", "lag", "lag2", "", nil, &[2]string{"lag.lag", "mode=\"BalanceSLB\";primary=\"1/1/1\";up_delay_ns=2000000000;down_delay_ns=1000000000;hash_basis=7;min_links=1;rebalance_interval=45s;lacp={mode=\"Active\";fast=true;system_priority=32768;system_id=\"02:00:00:00:00:01\";key=1;fallback=true};members={\"1/1/1\":{priority=128;key=1}}"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].RebalanceInterval.@present":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "rebalance_interval", &[2]string{"lag.rebalance_interval", "45s"}, &[2]string{"lag.rebalance_interval", "10s"}},
		}, nil
	case "vswitch.Config.LAG.LAGs[lag1].Members[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"lag", "lag_member", "\"lag1\"/\"1/1/1\"", "", &[2]string{"lag.member", "priority=128,key=1"}, nil},
			{"lag", "lag_member", "\"lag1\"/\"1/1/2\"", "", nil, &[2]string{"lag.member", "priority=128,key=1"}},
		}, nil
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
	case "vswitch.Config.STP.MST.Name":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "mst.name", &[2]string{"stp.mst.name", "region1"}, &[2]string{"stp.mst.name", "region2"}},
		}, nil
	case "vswitch.Config.STP.MST.Revision":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "mst.revision", &[2]string{"stp.mst.revision", "1"}, &[2]string{"stp.mst.revision", "2"}},
		}, nil
	case "vswitch.Config.STP.MST.MaxHops":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "mst.max_hops", &[2]string{"stp.mst.max_hops", "20"}, &[2]string{"stp.mst.max_hops", "21"}},
		}, nil
	case "vswitch.Config.STP.MST.Instances[1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "1", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "4096"}},
		}, nil
	case "vswitch.Config.STP.MST.Instances[1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "1", "priority", &[2]string{"stp.priority", "0"}, &[2]string{"stp.priority", "32768"}},
		}, nil
	case "vswitch.Config.STP.MST.Instances[1].VLANs[0]":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "1", "vlans[\"10\"]", &[2]string{"stp.mst.vlan_id", "10"}, nil},
			{"stp", "mst_instance", "1", "vlans[\"11\"]", nil, &[2]string{"stp.mst.vlan_id", "11"}},
		}, nil
	case "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"1\"/\"1/1/1\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "129"}},
		}, nil
	case "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].PriorityPresent":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"1\"/\"1/1/1\"", "priority", &[2]string{"stp.port_priority", "128"}, &[2]string{"stp.port_priority", "0"}},
		}, nil
	case "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].PathCost":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"1\"/\"1/1/1\"", "path_cost", &[2]string{"stp.path_cost", "20000"}, &[2]string{"stp.path_cost", "20001"}},
		}, nil
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
	case "vswitch.Config.STP.Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"stp", "port", "1/1/1", "", &[2]string{"stp.port", "priority=0,path_cost=20000,admin_edge=true,auto_edge=true,point_to_point=\"ForceTrue\",bpdu_guard=true,restricted_role=true,restricted_tcn=true,loop_guard=true"}, nil},
			{"stp", "port", "1/1/2", "", nil, &[2]string{"stp.port", "priority=0,path_cost=20000,admin_edge=true,auto_edge=true,point_to_point=\"ForceTrue\",bpdu_guard=true,restricted_role=true,restricted_tcn=true,loop_guard=true"}},
		}, nil
	case "vswitch.Config.STP.MST.@present":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "mst.presence", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "vswitch.Config.STP.PVST.@present":
		*hits++
		return []changeRecord{
			{"stp", "bridge", "", "pvst.presence", &[2]string{"stp.bool", "true"}, &[2]string{"stp.bool", "false"}},
		}, nil
	case "vswitch.Config.STP.MST.Instances[1].@key":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance", "1", "", &[2]string{"stp.mst.instance", "priority=0,vlans=[10],ports=[1/1/1:{priority=128,path_cost=20000}]"}, nil},
			{"stp", "mst_instance", "2", "", nil, &[2]string{"stp.mst.instance", "priority=0,vlans=[10],ports=[1/1/1:{priority=128,path_cost=20000}]"}},
		}, nil
	case "vswitch.Config.STP.MST.Instances[1].Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"stp", "mst_instance_port", "\"1\"/\"1/1/1\"", "", &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}, nil},
			{"stp", "mst_instance_port", "\"1\"/\"1/1/2\"", "", nil, &[2]string{"stp.mst.instance_port", "priority=128,path_cost=20000"}},
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
	case "vswitch.Config.Filter.Sets[ingress].Stateful":
		*hits++
		return []changeRecord{
			{"filter", "set", "ingress", "stateful", &[2]string{"filter.bool", "true"}, &[2]string{"filter.bool", "false"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Default":
		*hits++
		return []changeRecord{
			{"filter", "set", "ingress", "default", &[2]string{"filter.action", "drop"}, &[2]string{"filter.action", "accept"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Name":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "", &[2]string{"filter.rule", "name=\"rule1\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}, nil},
			{"filter", "rule", "\"ingress\"/\"rule2\"", "", nil, &[2]string{"filter.rule", "name=\"rule2\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Action":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "action", &[2]string{"filter.action", "accept"}, &[2]string{"filter.action", "drop"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Protocol":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.protocol", &[2]string{"filter.protocol", "6"}, &[2]string{"filter.protocol", "17"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Src[0]":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.src[\"10.0.1.0/24\"]", &[2]string{"filter.prefix", "10.0.1.0/24"}, nil},
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.src[\"192.0.2.0/25\"]", nil, &[2]string{"filter.prefix", "192.0.2.0/25"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Dst[0]":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.dst[\"10.0.2.0/24\"]", &[2]string{"filter.prefix", "10.0.2.0/24"}, nil},
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.dst[\"192.0.2.0/25\"]", nil, &[2]string{"filter.prefix", "192.0.2.0/25"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.SrcPorts[0].Start":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.src_ports[0].start", &[2]string{"filter.port", "1000"}, &[2]string{"filter.port", "1001"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.SrcPorts[0].End":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.src_ports[0].end", &[2]string{"filter.port", "2000"}, &[2]string{"filter.port", "2001"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.DstPorts[0].Start":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.dst_ports[0].start", &[2]string{"filter.port", "80"}, &[2]string{"filter.port", "81"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.DstPorts[0].End":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.dst_ports[0].end", &[2]string{"filter.port", "80"}, &[2]string{"filter.port", "81"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.Type":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.type", &[2]string{"filter.icmp_type", "8"}, &[2]string{"filter.icmp_type", "9"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.Code":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, &[2]string{"filter.icmp_code", "1"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.Mask":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.tcp_flags.mask", &[2]string{"filter.tcp_flags", "2"}, &[2]string{"filter.tcp_flags", "18"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.Value":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.tcp_flags.value", &[2]string{"filter.tcp_flags", "2"}, &[2]string{"filter.tcp_flags", "0"}},
		}, nil
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
	case "vswitch.Config.Filter.Sets[ingress].@key":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "", &[2]string{"filter.rule", "name=\"rule1\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}, nil},
			{"filter", "rule", "\"ingress2\"/\"rule1\"", "", nil, &[2]string{"filter.rule", "name=\"rule1\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}},
			{"filter", "set", "ingress", "", &[2]string{"filter.set", "stateful=true;default=\"drop\";rule_count=1"}, nil},
			{"filter", "set", "ingress2", "", nil, &[2]string{"filter.set", "stateful=true;default=\"drop\";rule_count=1"}},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.Protocol.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.protocol", &[2]string{"filter.protocol", "6"}, nil},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.type", &[2]string{"filter.icmp_type", "8"}, nil},
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, nil},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.Code.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, nil},
		}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.tcp_flags.mask", &[2]string{"filter.tcp_flags", "2"}, nil},
			{"filter", "rule", "\"ingress\"/\"rule1\"", "match.tcp_flags.value", &[2]string{"filter.tcp_flags", "2"}, nil},
		}, nil
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
	case "vswitch.Config.Traffic.Mirrors[0].Name":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "", &[2]string{"traffic.mirror", "name=\"m1\";select_all=true;select_src_ports=[\"1/1/1\"];select_dst_ports=[\"1/1/2\"];select_vlans=[10];output_port=\"1/1/3\";output_vlan=20;snap_len=128"}, nil},
			{"traffic", "mirror", "m2", "", nil, &[2]string{"traffic.mirror", "name=\"m2\";select_all=true;select_src_ports=[\"1/1/1\"];select_dst_ports=[\"1/1/2\"];select_vlans=[10];output_port=\"1/1/3\";output_vlan=20;snap_len=128"}},
		}, nil
	case "vswitch.Config.Traffic.Mirrors[0].SelectAll":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "select_all", &[2]string{"traffic.select_all", "true"}, &[2]string{"traffic.select_all", "false"}},
		}, nil
	case "vswitch.Config.Traffic.Mirrors[0].OutputPort":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "output_port", &[2]string{"traffic.output_port", "1/1/3"}, &[2]string{"traffic.output_port", "1/1/4"}},
		}, nil
	case "vswitch.Config.Traffic.Mirrors[0].OutputVLAN":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "output_vlan", &[2]string{"traffic.output_vlan", "20"}, &[2]string{"traffic.output_vlan", "21"}},
		}, nil
	case "vswitch.Config.Traffic.Mirrors[0].SnapLen":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "snap_len", &[2]string{"traffic.snap_len", "128"}, &[2]string{"traffic.snap_len", "129"}},
		}, nil
	case "vswitch.Config.Traffic.Mirrors[0].SelectSrcPorts[0]":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "select_src_ports[\"1/1/1\"]", &[2]string{"traffic.port", "1/1/1"}, nil},
			{"traffic", "mirror", "m1", "select_src_ports[\"1/1/2\"]", nil, &[2]string{"traffic.port", "1/1/2"}},
		}, nil
	case "vswitch.Config.Traffic.Mirrors[0].SelectDstPorts[0]":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "select_dst_ports[\"1/1/2\"]", &[2]string{"traffic.port", "1/1/2"}, nil},
			{"traffic", "mirror", "m1", "select_dst_ports[\"1/1/3\"]", nil, &[2]string{"traffic.port", "1/1/3"}},
		}, nil
	case "vswitch.Config.Traffic.Mirrors[0].SelectVLANs[0]":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "select_vlans[\"10\"]", &[2]string{"traffic.output_vlan", "10"}, nil},
			{"traffic", "mirror", "m1", "select_vlans[\"11\"]", nil, &[2]string{"traffic.output_vlan", "11"}},
		}, nil
	case "vswitch.Config.Traffic.Policers[1/1/1].RateBPS":
		*hits++
		return []changeRecord{
			{"traffic", "port", "1/1/1", "rate", &[2]string{"traffic.rate_bps", "1000000"}, &[2]string{"traffic.rate_bps", "1000001"}},
		}, nil
	case "vswitch.Config.Traffic.Policers[1/1/1].BurstOctets":
		*hits++
		return []changeRecord{
			{"traffic", "port", "1/1/1", "burst", &[2]string{"traffic.burst_octets", "1500"}, &[2]string{"traffic.burst_octets", "1501"}},
		}, nil
	case "vswitch.Config.Traffic.Queues[1/1/1].MaxRateBPS[0]":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"1/1/1\"/\"0\"", "max_rate", &[2]string{"traffic.max_rate_bps", "1000000"}, &[2]string{"traffic.max_rate_bps", "1000001"}},
		}, nil
	case "vswitch.Config.Traffic.Queues[1/1/1].BufferOctets[0]":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"1/1/1\"/\"0\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "4096"}, &[2]string{"traffic.queue_buffer_octets", "4097"}},
		}, nil
	case "vswitch.Config.Traffic.Mirrors[0].OutputVLAN.@present":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "output_vlan", &[2]string{"traffic.output_vlan", "20"}, nil},
		}, nil
	case "vswitch.Config.Traffic.Policers[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "1/1/1", "rate", &[2]string{"traffic.rate_bps", "1000000"}, &[2]string{"traffic.rate_bps", "0"}},
			{"traffic", "port", "1/1/1", "burst", &[2]string{"traffic.burst_octets", "1500"}, &[2]string{"traffic.burst_octets", "0"}},
			{"traffic", "port", "1/1/2", "rate", &[2]string{"traffic.rate_bps", "0"}, &[2]string{"traffic.rate_bps", "1000000"}},
			{"traffic", "port", "1/1/2", "burst", &[2]string{"traffic.burst_octets", "0"}, &[2]string{"traffic.burst_octets", "1500"}},
		}, nil
	case "vswitch.Config.Traffic.Queues[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"1/1/1\"/\"0\"", "max_rate", &[2]string{"traffic.max_rate_bps", "1000000"}, &[2]string{"traffic.max_rate_bps", "0"}},
			{"traffic", "port", "\"1/1/1\"/\"0\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "4096"}, &[2]string{"traffic.queue_buffer_octets", "0"}},
			{"traffic", "port", "\"1/1/2\"/\"0\"", "max_rate", &[2]string{"traffic.max_rate_bps", "0"}, &[2]string{"traffic.max_rate_bps", "1000000"}},
			{"traffic", "port", "\"1/1/2\"/\"0\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "0"}, &[2]string{"traffic.queue_buffer_octets", "4096"}},
		}, nil
	case "vswitch.Config.Traffic.Queues[1/1/1].MaxRateBPS[0].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"1/1/1\"/\"0\"", "max_rate", &[2]string{"traffic.max_rate_bps", "1000000"}, &[2]string{"traffic.max_rate_bps", "0"}},
			{"traffic", "port", "\"1/1/1\"/\"1\"", "max_rate", &[2]string{"traffic.max_rate_bps", "0"}, &[2]string{"traffic.max_rate_bps", "1000000"}},
		}, nil
	case "vswitch.Config.Traffic.Queues[1/1/1].BufferOctets[0].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"1/1/1\"/\"0\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "4096"}, &[2]string{"traffic.queue_buffer_octets", "0"}},
			{"traffic", "port", "\"1/1/1\"/\"1\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "0"}, &[2]string{"traffic.queue_buffer_octets", "4096"}},
		}, nil
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
