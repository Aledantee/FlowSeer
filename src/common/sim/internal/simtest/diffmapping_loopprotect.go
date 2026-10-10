package simtest

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
)

// LoopprotectDiffAllowance names only this producer's registered paths and emitted layers.
func LoopprotectDiffAllowance() DiffAllowance {
	return DiffAllowance{Producer: "loopprotect", Layers: []string{"loopprotect"}, Paths: []string{
		"loopprotect.Config.Interval",
		"loopprotect.Config.Ports[1/1/1].Action",
		"loopprotect.Config.Ports[1/1/1].Recovery.Mode",
		"loopprotect.Config.Ports[1/1/1].Recovery.Duration",
		"loopprotect.Config.Ports[1/1/1].VLANs[0]",
		"loopprotect.Config.Ports[1/1/1].@key",
	}}
}

func loopprotectDiffMapping() (diffMapping, error) {
	return newDiffMapping("loopprotect", loopprotect.Config{}, `{"Interval":"5s","Ports":{"1/1/1":{"Action":"Block","Recovery":{"Mode":"Timer","Duration":"30s"},"VLANs":[10]}}}`, func(c loopprotect.Config) loopprotect.Config { return c.Normalize(layer.Env{}) }, loopprotect.Diff, []mappedEntry{
		{Path: "loopprotect.Config.Interval", Producer: "loopprotect", AllowancePath: "loopprotect.Config.Interval", Setup: "", Before: `"5s"`, After: `"6s"`},
		{Path: "loopprotect.Config.Ports[1/1/1].Action", Producer: "loopprotect", AllowancePath: "loopprotect.Config.Ports[1/1/1].Action", Setup: "", Before: `"Block"`, After: `"Disable"`},
		{Path: "loopprotect.Config.Ports[1/1/1].Recovery.Mode", Producer: "loopprotect", AllowancePath: "loopprotect.Config.Ports[1/1/1].Recovery.Mode", Setup: "", Before: `"Timer"`, After: `"Manual"`},
		{Path: "loopprotect.Config.Ports[1/1/1].Recovery.Duration", Producer: "loopprotect", AllowancePath: "loopprotect.Config.Ports[1/1/1].Recovery.Duration", Setup: "", Before: `"30s"`, After: `"31s"`},
		{Path: "loopprotect.Config.Ports[1/1/1].VLANs[0]", Producer: "loopprotect", AllowancePath: "loopprotect.Config.Ports[1/1/1].VLANs[0]", Setup: "", Before: `10`, After: `11`},
		{Path: "loopprotect.Config.Ports[1/1/1].@key", Producer: "loopprotect", AllowancePath: "loopprotect.Config.Ports[1/1/1].@key", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
	}, loopprotectDiffExpectation)
}

func loopprotectDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "loopprotect.Config.Interval":
		*hits++
		return []changeRecord{
			{"loopprotect", "bridge", "", "interval", &[2]string{"loopprotect.duration", "5s"}, &[2]string{"loopprotect.duration", "6s"}},
		}, nil
	case "loopprotect.Config.Ports[1/1/1].Action":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "1/1/1", "action", &[2]string{"loopprotect.action", "Block"}, &[2]string{"loopprotect.action", "Disable"}},
		}, nil
	case "loopprotect.Config.Ports[1/1/1].Recovery.Mode":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "1/1/1", "recovery.mode", &[2]string{"loopprotect.recovery_mode", "Timer"}, &[2]string{"loopprotect.recovery_mode", "Manual"}},
		}, nil
	case "loopprotect.Config.Ports[1/1/1].Recovery.Duration":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "1/1/1", "recovery.duration", &[2]string{"loopprotect.duration", "30s"}, &[2]string{"loopprotect.duration", "31s"}},
		}, nil
	case "loopprotect.Config.Ports[1/1/1].VLANs[0]":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "1/1/1", "vlans[\"10\"]", &[2]string{"loopprotect.vlan_id", "10"}, nil},
			{"loopprotect", "port", "1/1/1", "vlans[\"11\"]", nil, &[2]string{"loopprotect.vlan_id", "11"}},
		}, nil
	case "loopprotect.Config.Ports[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"loopprotect", "port", "1/1/1", "", &[2]string{"loopprotect.port", "action=Block,recovery_mode=Timer,recovery_duration=30s,vlans=10"}, nil},
			{"loopprotect", "port", "1/1/2", "", nil, &[2]string{"loopprotect.port", "action=Block,recovery_mode=Timer,recovery_duration=30s,vlans=10"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
