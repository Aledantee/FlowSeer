package simtest

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
)

func phyDiffMapping() (diffMapping, error) {
	return newDiffMapping("phy", phy.Config{}, `{"Ethernet":{"1/1/1":{"SupportedSpeedsBPS":[1000000000],"AutoNegotiationSupported":"Supported","Setting":{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true},"Observed":{"SpeedBPS":1000000000,"Duplex":"Full"}}},"PoE":{"Groups":{"g1":{"PowerNanowatts":370000000000}},"Ports":{"1/1/1":{"Group":"g1","MaxClass":4,"Enabled":true,"Limit":5000000000,"Priority":"High","PD":"Attached","PDClass":4}}}}`, func(c phy.Config) phy.Config { return c.Normalize(layer.Env{}) }, phy.Diff, []mappedEntry{
		{Path: "phy.Config.Ethernet[1/1/1].Setting.SpeedBPS", Producer: "phy", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "phy.Config.Ethernet[1/1/1].Setting.Duplex", Producer: "phy", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "phy.Config.Ethernet[1/1/1].Setting.AutoNegotiation", Producer: "phy", Setup: "", Before: `true`, After: `false`},
		{Path: "phy.Config.Ethernet[1/1/1].AutoNegotiationSupported", Producer: "phy", Setup: "", Before: `"Supported"`, After: `"Unsupported"`},
		{Path: "phy.Config.Ethernet[1/1/1].Observed.SpeedBPS", Producer: "phy", Setup: "", Before: `1000000000`, After: `1000000001`},
		{Path: "phy.Config.Ethernet[1/1/1].Observed.Duplex", Producer: "phy", Setup: "", Before: `"Full"`, After: `"Half"`},
		{Path: "phy.Config.Ethernet[1/1/1].SupportedSpeedsBPS[0]", Producer: "phy", Setup: "", Before: `1000000000`, After: `10000000000`},
		{Path: "phy.Config.PoE.Groups[g1].PowerNanowatts", Producer: "phy", Setup: "", Before: `370000000000`, After: `370000000001`},
		{Path: "phy.Config.PoE.Ports[1/1/1].Group", Producer: "phy", Setup: "", Before: `"g1"`, After: `"g2"`},
		{Path: "phy.Config.PoE.Ports[1/1/1].MaxClass", Producer: "phy", Setup: "", Before: `4`, After: `5`},
		{Path: "phy.Config.PoE.Ports[1/1/1].Enabled", Producer: "phy", Setup: "", Before: `true`, After: `false`},
		{Path: "phy.Config.PoE.Ports[1/1/1].Limit", Producer: "phy", Setup: "", Before: `5000000000`, After: `5000000001`},
		{Path: "phy.Config.PoE.Ports[1/1/1].Priority", Producer: "phy", Setup: "", Before: `"High"`, After: `"Low"`},
		{Path: "phy.Config.PoE.Ports[1/1/1].PD", Producer: "phy", Setup: "", Before: `"Attached"`, After: `"Absent"`},
		{Path: "phy.Config.PoE.Ports[1/1/1].PDClass", Producer: "phy", Setup: "", Before: `4`, After: `5`},
		{Path: "phy.Config.Ethernet[1/1/1].@key", Producer: "phy", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "phy.Config.Ethernet[1/1/1].Setting.@present", Producer: "phy", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full","AutoNegotiation":true}`, After: `null`},
		{Path: "phy.Config.Ethernet[1/1/1].Observed.@present", Producer: "phy", Setup: "", Before: `{"SpeedBPS":1000000000,"Duplex":"Full"}`, After: `null`},
		{Path: "phy.Config.PoE.@present", Producer: "phy", Setup: "", Before: `{"Groups":{"g1":{"PowerNanowatts":370000000000}},"Ports":{"1/1/1":{"Group":"g1","MaxClass":4,"Enabled":true,"Limit":5000000000,"Priority":"High","PD":"Attached","PDClass":4}}}`, After: `null`},
		{Path: "phy.Config.PoE.Groups[g1].@key", Producer: "phy", Setup: "", Before: `"g1"`, After: `"g2"`},
		{Path: "phy.Config.PoE.Ports[1/1/1].@key", Producer: "phy", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "phy.Config.PoE.Ports[1/1/1].Limit.@present", Producer: "phy", Setup: "", Before: `5000000000`, After: `null`},
		{Path: "phy.Config.PoE.Ports[1/1/1].PDClass.@present", Producer: "phy", Setup: "", Before: `4`, After: `null`},
	}, phyDiffExpectation)
}

func phyDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "phy.Config.Ethernet[1/1/1].Setting.SpeedBPS":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "phy.Config.Ethernet[1/1/1].Setting.Duplex":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "phy.Config.Ethernet[1/1/1].Setting.AutoNegotiation":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "phy.Config.Ethernet[1/1/1].AutoNegotiationSupported":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "auto_negotiation_supported", &[2]string{"phy.capability", "Supported"}, &[2]string{"phy.capability", "Unsupported"}},
		}, nil
	case "phy.Config.Ethernet[1/1/1].Observed.SpeedBPS":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "1000000001"}},
		}, nil
	case "phy.Config.Ethernet[1/1/1].Observed.Duplex":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", "Half"}},
		}, nil
	case "phy.Config.Ethernet[1/1/1].SupportedSpeedsBPS[0]":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "supported_speeds_bps[\"1000000000\"]", &[2]string{"phy.speed_bps", "1000000000"}, nil},
			{"ethernet", "port", "1/1/1", "supported_speeds_bps[\"10000000000\"]", nil, &[2]string{"phy.speed_bps", "10000000000"}},
		}, nil
	case "phy.Config.PoE.Groups[g1].PowerNanowatts":
		*hits++
		return []changeRecord{
			{"poe", "pse_group", "g1", "power_nanowatts", &[2]string{"phy.power_nw", "370000000000"}, &[2]string{"phy.power_nw", "370000000001"}},
		}, nil
	case "phy.Config.PoE.Ports[1/1/1].Group":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "group", &[2]string{"phy.string", "g1"}, &[2]string{"phy.string", "g2"}},
		}, nil
	case "phy.Config.PoE.Ports[1/1/1].MaxClass":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "max_class", &[2]string{"phy.class", "4"}, &[2]string{"phy.class", "5"}},
		}, nil
	case "phy.Config.PoE.Ports[1/1/1].Enabled":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "phy.Config.PoE.Ports[1/1/1].Limit":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "power_limit_nanowatts", &[2]string{"phy.power_nw", "5000000000"}, &[2]string{"phy.power_nw", "5000000001"}},
		}, nil
	case "phy.Config.PoE.Ports[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "priority", &[2]string{"phy.priority", "High"}, &[2]string{"phy.priority", "Low"}},
		}, nil
	case "phy.Config.PoE.Ports[1/1/1].PD":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "pd", &[2]string{"phy.pd_state", "Attached"}, &[2]string{"phy.pd_state", "Absent"}},
		}, nil
	case "phy.Config.PoE.Ports[1/1/1].PDClass":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "pd_class", &[2]string{"phy.class", "4"}, &[2]string{"phy.class", "5"}},
		}, nil
	case "phy.Config.Ethernet[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "", &[2]string{"phy.ethernet", "speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\"Full\",autoneg=true},observed={speed=1000000000,duplex=\"Full\"}"}, nil},
			{"ethernet", "port", "1/1/2", "", nil, &[2]string{"phy.ethernet", "speeds=[1000000000],autoneg_sup=Supported,setting={speed=1000000000,duplex=\"Full\",autoneg=true},observed={speed=1000000000,duplex=\"Full\"}"}},
		}, nil
	case "phy.Config.Ethernet[1/1/1].Setting.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"ethernet", "port", "1/1/1", "duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
			{"ethernet", "port", "1/1/1", "auto_negotiation_enabled", &[2]string{"phy.bool", "true"}, &[2]string{"phy.bool", "false"}},
		}, nil
	case "phy.Config.Ethernet[1/1/1].Observed.@present":
		*hits++
		return []changeRecord{
			{"ethernet", "port", "1/1/1", "observed_speed_bps", &[2]string{"phy.speed_bps", "1000000000"}, &[2]string{"phy.speed_bps", "0"}},
			{"ethernet", "port", "1/1/1", "observed_duplex", &[2]string{"phy.duplex", "Full"}, &[2]string{"phy.duplex", ""}},
		}, nil
	case "phy.Config.PoE.@present":
		*hits++
		return []changeRecord{
			{"poe", "pse_group", "g1", "", &[2]string{"phy.pse_group", "370000000000"}, nil},
			{"poe", "port", "1/1/1", "", &[2]string{"phy.pse_port", "group=\"g1\",max_class=4,enabled=true,limit=5000000000,priority=\"High\",pd=Attached,pd_class=4"}, nil},
		}, nil
	case "phy.Config.PoE.Groups[g1].@key":
		*hits++
		return []changeRecord{
			{"poe", "pse_group", "g1", "", &[2]string{"phy.pse_group", "370000000000"}, nil},
			{"poe", "pse_group", "g2", "", nil, &[2]string{"phy.pse_group", "370000000000"}},
		}, nil
	case "phy.Config.PoE.Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "", &[2]string{"phy.pse_port", "group=\"g1\",max_class=4,enabled=true,limit=5000000000,priority=\"High\",pd=Attached,pd_class=4"}, nil},
			{"poe", "port", "1/1/2", "", nil, &[2]string{"phy.pse_port", "group=\"g1\",max_class=4,enabled=true,limit=5000000000,priority=\"High\",pd=Attached,pd_class=4"}},
		}, nil
	case "phy.Config.PoE.Ports[1/1/1].Limit.@present":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "power_limit_nanowatts", &[2]string{"phy.power_nw", "5000000000"}, nil},
		}, nil
	case "phy.Config.PoE.Ports[1/1/1].PDClass.@present":
		*hits++
		return []changeRecord{
			{"poe", "port", "1/1/1", "pd_class", &[2]string{"phy.class", "4"}, nil},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
