package simtest

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
)

func optionalFilterAdditionExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "filter.Config.Sets[set1].Rules[0].Match.ICMP.@present":
		*hits++
		return []changeRecord{{"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.type", nil, &[2]string{"filter.icmp_type", "8"}}, {"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.code", nil, &[2]string{"filter.icmp_code", "0"}}}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.@present":
		*hits++
		return []changeRecord{{"filter", "rule", "\"set1\"/\"rule1\"", "match.tcp_flags.mask", nil, &[2]string{"filter.tcp_flags", "2"}}, {"filter", "rule", "\"set1\"/\"rule1\"", "match.tcp_flags.value", nil, &[2]string{"filter.tcp_flags", "2"}}}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.ICMP.@present":
		*hits++
		return []changeRecord{{"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.type", nil, &[2]string{"filter.icmp_type", "8"}}, {"filter", "rule", "\"ingress\"/\"rule1\"", "match.icmp.code", nil, &[2]string{"filter.icmp_code", "0"}}}, nil
	case "vswitch.Config.Filter.Sets[ingress].Rules[0].Match.TCPFlags.@present":
		*hits++
		return []changeRecord{{"filter", "rule", "\"ingress\"/\"rule1\"", "match.tcp_flags.mask", nil, &[2]string{"filter.tcp_flags", "2"}}, {"filter", "rule", "\"ingress\"/\"rule1\"", "match.tcp_flags.value", nil, &[2]string{"filter.tcp_flags", "2"}}}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.ICMP.@present":
		*hits++
		return []changeRecord{{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.type", nil, &[2]string{"filter.icmp_type", "8"}}, {"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.icmp.code", nil, &[2]string{"filter.icmp_code", "0"}}}, nil
	case "assembly.Config.Switches[sw1].Filter.Sets[ingress].Rules[0].Match.TCPFlags.@present":
		*hits++
		return []changeRecord{{"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.tcp_flags.mask", nil, &[2]string{"filter.tcp_flags", "2"}}, {"filter", "rule", "\"sw1\"/\"\\\"ingress\\\"/\\\"rule1\\\"\"", "match.tcp_flags.value", nil, &[2]string{"filter.tcp_flags", "2"}}}, nil
	default:
		return nil, errs.Msgf("no optional filter addition expectation for %s", path)
	}
}

// FilterDiffAllowance returns no exceptions for this exact producer.
func FilterDiffAllowance() DiffAllowance {
	return DiffAllowance{}
}

func filterDiffMapping() (diffMapping, error) {
	return newDiffMapping("filter", filter.Config{}, `{"Sets":{"set1":{"Stateful":true,"Default":"drop","Rules":[{"Name":"rule1","Action":"accept","Match":{"Protocol":6,"Src":["10.0.1.0/24"],"Dst":["10.0.2.0/24"],"SrcPorts":[{"Start":1000,"End":2000}],"DstPorts":[{"Start":80,"End":80}],"ICMP":{"Type":8,"Code":0},"TCPFlags":{"Mask":2,"Value":2}}}]}},"Bindings":[{"Interface":"vlan10","Direction":"in","Set":"set1"}]}`, func(c filter.Config) filter.Config { return c.Normalize(layer.Env{}) }, filter.Diff, []mappedEntry{
		{Path: "filter.Config.Sets[set1].Stateful", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Stateful", Setup: "", Before: `true`, After: `false`},
		{Path: "filter.Config.Sets[set1].Default", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Default", Setup: "", Before: `"drop"`, After: `"accept"`},
		{Path: "filter.Config.Sets[set1].Rules[0].Name", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Name", Setup: "", Before: `"rule1"`, After: `"rule2"`},
		{Path: "filter.Config.Sets[set1].Rules[0].Action", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Action", Setup: "", Before: `"accept"`, After: `"drop"`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.Protocol", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.Protocol", Setup: "", Before: `6`, After: `17`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.Src[0]", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.Src[0]", Setup: "", Before: `"10.0.1.0/24"`, After: `"192.0.2.0/25"`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.Dst[0]", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.Dst[0]", Setup: "", Before: `"10.0.2.0/24"`, After: `"192.0.2.0/25"`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.SrcPorts[0].Start", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.SrcPorts[0].Start", Setup: "", Before: `1000`, After: `1001`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.SrcPorts[0].End", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.SrcPorts[0].End", Setup: "", Before: `2000`, After: `2001`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.DstPorts[0].Start", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.DstPorts[0].Start", Setup: "", Before: `80`, After: `81`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.DstPorts[0].End", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.DstPorts[0].End", Setup: "", Before: `80`, After: `81`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.ICMP.Type", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.ICMP.Type", Setup: "", Before: `8`, After: `9`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.ICMP.Code", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.ICMP.Code", Setup: "", Before: `0`, After: `1`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.Mask", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.Mask", Setup: "", Before: `2`, After: `18`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.Value", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.Value", Setup: "", Before: `2`, After: `0`},
		{Path: "filter.Config.Bindings[0].Interface", Producer: "filter", AllowancePath: "filter.Config.Bindings[0].Interface", Setup: "", Before: `"vlan10"`, After: `"vlan11"`},
		{Path: "filter.Config.Bindings[0].Direction", Producer: "filter", AllowancePath: "filter.Config.Bindings[0].Direction", Setup: "", Before: `"in"`, After: `"out"`},
		{Path: "filter.Config.Bindings[0].Set", Producer: "filter", AllowancePath: "filter.Config.Bindings[0].Set", Setup: "", Before: `"set1"`, After: `"set2"`},
		{Path: "filter.Config.Sets[set1].@key", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].@key", Setup: "", Before: `"set1"`, After: `"set2"`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.Protocol.@present", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.Protocol.@present", Setup: "", Before: `6`, After: `null`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.ICMP.@present", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.ICMP.@present", Setup: "", Before: `{"Type":8,"Code":0}`, After: `null`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.ICMP.Code.@present", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.ICMP.Code.@present", Setup: "", Before: `0`, After: `null`},
		{Path: "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.@present", Producer: "filter", AllowancePath: "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.@present", Setup: "", Before: `{"Mask":2,"Value":2}`, After: `null`},
	}, filterDiffExpectation)
}

func filterDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "filter.Config.Sets[set1].Stateful":
		*hits++
		return []changeRecord{
			{"filter", "set", "set1", "stateful", &[2]string{"filter.bool", "true"}, &[2]string{"filter.bool", "false"}},
		}, nil
	case "filter.Config.Sets[set1].Default":
		*hits++
		return []changeRecord{
			{"filter", "set", "set1", "default", &[2]string{"filter.action", "drop"}, &[2]string{"filter.action", "accept"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Name":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "", &[2]string{"filter.rule", "name=\"rule1\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}, nil},
			{"filter", "rule", "\"set1\"/\"rule2\"", "", nil, &[2]string{"filter.rule", "name=\"rule2\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Action":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "action", &[2]string{"filter.action", "accept"}, &[2]string{"filter.action", "drop"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.Protocol":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.protocol", &[2]string{"filter.protocol", "6"}, &[2]string{"filter.protocol", "17"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.Src[0]":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.src[\"10.0.1.0/24\"]", &[2]string{"filter.prefix", "10.0.1.0/24"}, nil},
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.src[\"192.0.2.0/25\"]", nil, &[2]string{"filter.prefix", "192.0.2.0/25"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.Dst[0]":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.dst[\"10.0.2.0/24\"]", &[2]string{"filter.prefix", "10.0.2.0/24"}, nil},
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.dst[\"192.0.2.0/25\"]", nil, &[2]string{"filter.prefix", "192.0.2.0/25"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.SrcPorts[0].Start":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.src_ports[0].start", &[2]string{"filter.port", "1000"}, &[2]string{"filter.port", "1001"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.SrcPorts[0].End":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.src_ports[0].end", &[2]string{"filter.port", "2000"}, &[2]string{"filter.port", "2001"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.DstPorts[0].Start":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.dst_ports[0].start", &[2]string{"filter.port", "80"}, &[2]string{"filter.port", "81"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.DstPorts[0].End":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.dst_ports[0].end", &[2]string{"filter.port", "80"}, &[2]string{"filter.port", "81"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.ICMP.Type":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.type", &[2]string{"filter.icmp_type", "8"}, &[2]string{"filter.icmp_type", "9"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.ICMP.Code":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, &[2]string{"filter.icmp_code", "1"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.Mask":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.tcp_flags.mask", &[2]string{"filter.tcp_flags", "2"}, &[2]string{"filter.tcp_flags", "18"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.Value":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.tcp_flags.value", &[2]string{"filter.tcp_flags", "2"}, &[2]string{"filter.tcp_flags", "0"}},
		}, nil
	case "filter.Config.Bindings[0].Interface":
		*hits++
		return []changeRecord{
			{"filter", "binding", "\"vlan10\"/\"in\"", "", &[2]string{"filter.binding", "set=\"set1\""}, nil},
			{"filter", "binding", "\"vlan11\"/\"in\"", "", nil, &[2]string{"filter.binding", "set=\"set1\""}},
		}, nil
	case "filter.Config.Bindings[0].Direction":
		*hits++
		return []changeRecord{
			{"filter", "binding", "\"vlan10\"/\"in\"", "", &[2]string{"filter.binding", "set=\"set1\""}, nil},
			{"filter", "binding", "\"vlan10\"/\"out\"", "", nil, &[2]string{"filter.binding", "set=\"set1\""}},
		}, nil
	case "filter.Config.Bindings[0].Set":
		*hits++
		return []changeRecord{
			{"filter", "binding", "\"vlan10\"/\"in\"", "set", &[2]string{"filter.binding", "set=\"set1\""}, &[2]string{"filter.binding", "set=\"set2\""}},
		}, nil
	case "filter.Config.Sets[set1].@key":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "", &[2]string{"filter.rule", "name=\"rule1\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}, nil},
			{"filter", "rule", "\"set2\"/\"rule1\"", "", nil, &[2]string{"filter.rule", "name=\"rule1\";action=\"accept\";proto=6;src=[10.0.1.0/24];dst=[10.0.2.0/24];src_ports=[1000-2000];dst_ports=[80-80];icmp=type:8,code:0;tcp_flags=mask:2,val:2"}},
			{"filter", "set", "set1", "", &[2]string{"filter.set", "stateful=true;default=\"drop\";rule_count=1"}, nil},
			{"filter", "set", "set2", "", nil, &[2]string{"filter.set", "stateful=true;default=\"drop\";rule_count=1"}},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.Protocol.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.protocol", &[2]string{"filter.protocol", "6"}, nil},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.ICMP.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.type", &[2]string{"filter.icmp_type", "8"}, nil},
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, nil},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.ICMP.Code.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.icmp.code", &[2]string{"filter.icmp_code", "0"}, nil},
		}, nil
	case "filter.Config.Sets[set1].Rules[0].Match.TCPFlags.@present":
		*hits++
		return []changeRecord{
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.tcp_flags.mask", &[2]string{"filter.tcp_flags", "2"}, nil},
			{"filter", "rule", "\"set1\"/\"rule1\"", "match.tcp_flags.value", &[2]string{"filter.tcp_flags", "2"}, nil},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
