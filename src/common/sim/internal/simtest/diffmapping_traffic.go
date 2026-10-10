package simtest

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
)

// TrafficDiffAllowance names only this producer's registered paths and emitted layers.
func TrafficDiffAllowance() DiffAllowance {
	return DiffAllowance{Producer: "traffic", Layers: []string{"traffic"}, Paths: []string{
		"traffic.Config.Mirrors[0].Name",
		"traffic.Config.Mirrors[0].SelectAll",
		"traffic.Config.Mirrors[0].OutputPort",
		"traffic.Config.Mirrors[0].OutputVLAN",
		"traffic.Config.Mirrors[0].SnapLen",
		"traffic.Config.Mirrors[0].SelectSrcPorts[0]",
		"traffic.Config.Mirrors[0].SelectDstPorts[0]",
		"traffic.Config.Mirrors[0].SelectVLANs[0]",
		"traffic.Config.Policers[1/1/1].RateBPS",
		"traffic.Config.Policers[1/1/1].BurstOctets",
		"traffic.Config.Queues[1/1/1].MaxRateBPS[0]",
		"traffic.Config.Queues[1/1/1].BufferOctets[0]",
		"traffic.Config.Mirrors[0].OutputVLAN.@present",
		"traffic.Config.Policers[1/1/1].@key",
		"traffic.Config.Queues[1/1/1].@key",
		"traffic.Config.Queues[1/1/1].MaxRateBPS[0].@key",
		"traffic.Config.Queues[1/1/1].BufferOctets[0].@key",
	}}
}

func trafficDiffMapping() (diffMapping, error) {
	return newDiffMapping("traffic", traffic.Config{}, `{"Mirrors":[{"Name":"m1","SelectAll":true,"SelectSrcPorts":["1/1/1"],"SelectDstPorts":["1/1/2"],"SelectVLANs":[10],"OutputPort":"1/1/3","OutputVLAN":20,"SnapLen":128}],"Policers":{"1/1/1":{"RateBPS":1000000,"BurstOctets":1500}},"Queues":{"1/1/1":{"MaxRateBPS":{"0":1000000},"BufferOctets":{"0":4096}}}}`, func(c traffic.Config) traffic.Config { return c.Normalize(layer.Env{}) }, traffic.Diff, []mappedEntry{
		{Path: "traffic.Config.Mirrors[0].Name", Producer: "traffic", AllowancePath: "traffic.Config.Mirrors[0].Name", Setup: "", Before: `"m1"`, After: `"m2"`},
		{Path: "traffic.Config.Mirrors[0].SelectAll", Producer: "traffic", AllowancePath: "traffic.Config.Mirrors[0].SelectAll", Setup: "", Before: `true`, After: `false`},
		{Path: "traffic.Config.Mirrors[0].OutputPort", Producer: "traffic", AllowancePath: "traffic.Config.Mirrors[0].OutputPort", Setup: "", Before: `"1/1/3"`, After: `"1/1/4"`},
		{Path: "traffic.Config.Mirrors[0].OutputVLAN", Producer: "traffic", AllowancePath: "traffic.Config.Mirrors[0].OutputVLAN", Setup: "", Before: `20`, After: `21`},
		{Path: "traffic.Config.Mirrors[0].SnapLen", Producer: "traffic", AllowancePath: "traffic.Config.Mirrors[0].SnapLen", Setup: "", Before: `128`, After: `129`},
		{Path: "traffic.Config.Mirrors[0].SelectSrcPorts[0]", Producer: "traffic", AllowancePath: "traffic.Config.Mirrors[0].SelectSrcPorts[0]", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "traffic.Config.Mirrors[0].SelectDstPorts[0]", Producer: "traffic", AllowancePath: "traffic.Config.Mirrors[0].SelectDstPorts[0]", Setup: "", Before: `"1/1/2"`, After: `"1/1/3"`},
		{Path: "traffic.Config.Mirrors[0].SelectVLANs[0]", Producer: "traffic", AllowancePath: "traffic.Config.Mirrors[0].SelectVLANs[0]", Setup: "", Before: `10`, After: `11`},
		{Path: "traffic.Config.Policers[1/1/1].RateBPS", Producer: "traffic", AllowancePath: "traffic.Config.Policers[1/1/1].RateBPS", Setup: "", Before: `1000000`, After: `1000001`},
		{Path: "traffic.Config.Policers[1/1/1].BurstOctets", Producer: "traffic", AllowancePath: "traffic.Config.Policers[1/1/1].BurstOctets", Setup: "", Before: `1500`, After: `1501`},
		{Path: "traffic.Config.Queues[1/1/1].MaxRateBPS[0]", Producer: "traffic", AllowancePath: "traffic.Config.Queues[1/1/1].MaxRateBPS[0]", Setup: "", Before: `1000000`, After: `1000001`},
		{Path: "traffic.Config.Queues[1/1/1].BufferOctets[0]", Producer: "traffic", AllowancePath: "traffic.Config.Queues[1/1/1].BufferOctets[0]", Setup: "", Before: `4096`, After: `4097`},
		{Path: "traffic.Config.Mirrors[0].OutputVLAN.@present", Producer: "traffic", AllowancePath: "traffic.Config.Mirrors[0].OutputVLAN.@present", Setup: "", Before: `20`, After: `null`},
		{Path: "traffic.Config.Policers[1/1/1].@key", Producer: "traffic", AllowancePath: "traffic.Config.Policers[1/1/1].@key", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "traffic.Config.Queues[1/1/1].@key", Producer: "traffic", AllowancePath: "traffic.Config.Queues[1/1/1].@key", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "traffic.Config.Queues[1/1/1].MaxRateBPS[0].@key", Producer: "traffic", AllowancePath: "traffic.Config.Queues[1/1/1].MaxRateBPS[0].@key", Setup: "", Before: `0`, After: `1`},
		{Path: "traffic.Config.Queues[1/1/1].BufferOctets[0].@key", Producer: "traffic", AllowancePath: "traffic.Config.Queues[1/1/1].BufferOctets[0].@key", Setup: "", Before: `0`, After: `1`},
	}, trafficDiffExpectation)
}

func trafficDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "traffic.Config.Mirrors[0].Name":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "", &[2]string{"traffic.mirror", "name=\"m1\";select_all=true;select_src_ports=[\"1/1/1\"];select_dst_ports=[\"1/1/2\"];select_vlans=[10];output_port=\"1/1/3\";output_vlan=20;snap_len=128"}, nil},
			{"traffic", "mirror", "m2", "", nil, &[2]string{"traffic.mirror", "name=\"m2\";select_all=true;select_src_ports=[\"1/1/1\"];select_dst_ports=[\"1/1/2\"];select_vlans=[10];output_port=\"1/1/3\";output_vlan=20;snap_len=128"}},
		}, nil
	case "traffic.Config.Mirrors[0].SelectAll":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "select_all", &[2]string{"traffic.select_all", "true"}, &[2]string{"traffic.select_all", "false"}},
		}, nil
	case "traffic.Config.Mirrors[0].OutputPort":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "output_port", &[2]string{"traffic.output_port", "1/1/3"}, &[2]string{"traffic.output_port", "1/1/4"}},
		}, nil
	case "traffic.Config.Mirrors[0].OutputVLAN":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "output_vlan", &[2]string{"traffic.output_vlan", "20"}, &[2]string{"traffic.output_vlan", "21"}},
		}, nil
	case "traffic.Config.Mirrors[0].SnapLen":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "snap_len", &[2]string{"traffic.snap_len", "128"}, &[2]string{"traffic.snap_len", "129"}},
		}, nil
	case "traffic.Config.Mirrors[0].SelectSrcPorts[0]":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "select_src_ports[\"1/1/1\"]", &[2]string{"traffic.port", "1/1/1"}, nil},
			{"traffic", "mirror", "m1", "select_src_ports[\"1/1/2\"]", nil, &[2]string{"traffic.port", "1/1/2"}},
		}, nil
	case "traffic.Config.Mirrors[0].SelectDstPorts[0]":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "select_dst_ports[\"1/1/2\"]", &[2]string{"traffic.port", "1/1/2"}, nil},
			{"traffic", "mirror", "m1", "select_dst_ports[\"1/1/3\"]", nil, &[2]string{"traffic.port", "1/1/3"}},
		}, nil
	case "traffic.Config.Mirrors[0].SelectVLANs[0]":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "select_vlans[\"10\"]", &[2]string{"traffic.output_vlan", "10"}, nil},
			{"traffic", "mirror", "m1", "select_vlans[\"11\"]", nil, &[2]string{"traffic.output_vlan", "11"}},
		}, nil
	case "traffic.Config.Policers[1/1/1].RateBPS":
		*hits++
		return []changeRecord{
			{"traffic", "port", "1/1/1", "rate", &[2]string{"traffic.rate_bps", "1000000"}, &[2]string{"traffic.rate_bps", "1000001"}},
		}, nil
	case "traffic.Config.Policers[1/1/1].BurstOctets":
		*hits++
		return []changeRecord{
			{"traffic", "port", "1/1/1", "burst", &[2]string{"traffic.burst_octets", "1500"}, &[2]string{"traffic.burst_octets", "1501"}},
		}, nil
	case "traffic.Config.Queues[1/1/1].MaxRateBPS[0]":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"1/1/1\"/\"0\"", "max_rate", &[2]string{"traffic.max_rate_bps", "1000000"}, &[2]string{"traffic.max_rate_bps", "1000001"}},
		}, nil
	case "traffic.Config.Queues[1/1/1].BufferOctets[0]":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"1/1/1\"/\"0\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "4096"}, &[2]string{"traffic.queue_buffer_octets", "4097"}},
		}, nil
	case "traffic.Config.Mirrors[0].OutputVLAN.@present":
		*hits++
		return []changeRecord{
			{"traffic", "mirror", "m1", "output_vlan", &[2]string{"traffic.output_vlan", "20"}, nil},
		}, nil
	case "traffic.Config.Policers[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "1/1/1", "rate", &[2]string{"traffic.rate_bps", "1000000"}, &[2]string{"traffic.rate_bps", "0"}},
			{"traffic", "port", "1/1/1", "burst", &[2]string{"traffic.burst_octets", "1500"}, &[2]string{"traffic.burst_octets", "0"}},
			{"traffic", "port", "1/1/2", "rate", &[2]string{"traffic.rate_bps", "0"}, &[2]string{"traffic.rate_bps", "1000000"}},
			{"traffic", "port", "1/1/2", "burst", &[2]string{"traffic.burst_octets", "0"}, &[2]string{"traffic.burst_octets", "1500"}},
		}, nil
	case "traffic.Config.Queues[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"1/1/1\"/\"0\"", "max_rate", &[2]string{"traffic.max_rate_bps", "1000000"}, &[2]string{"traffic.max_rate_bps", "0"}},
			{"traffic", "port", "\"1/1/1\"/\"0\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "4096"}, &[2]string{"traffic.queue_buffer_octets", "0"}},
			{"traffic", "port", "\"1/1/2\"/\"0\"", "max_rate", &[2]string{"traffic.max_rate_bps", "0"}, &[2]string{"traffic.max_rate_bps", "1000000"}},
			{"traffic", "port", "\"1/1/2\"/\"0\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "0"}, &[2]string{"traffic.queue_buffer_octets", "4096"}},
		}, nil
	case "traffic.Config.Queues[1/1/1].MaxRateBPS[0].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"1/1/1\"/\"0\"", "max_rate", &[2]string{"traffic.max_rate_bps", "1000000"}, &[2]string{"traffic.max_rate_bps", "0"}},
			{"traffic", "port", "\"1/1/1\"/\"1\"", "max_rate", &[2]string{"traffic.max_rate_bps", "0"}, &[2]string{"traffic.max_rate_bps", "1000000"}},
		}, nil
	case "traffic.Config.Queues[1/1/1].BufferOctets[0].@key":
		*hits++
		return []changeRecord{
			{"traffic", "port", "\"1/1/1\"/\"0\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "4096"}, &[2]string{"traffic.queue_buffer_octets", "0"}},
			{"traffic", "port", "\"1/1/1\"/\"1\"", "buffer_octets", &[2]string{"traffic.queue_buffer_octets", "0"}, &[2]string{"traffic.queue_buffer_octets", "4096"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
