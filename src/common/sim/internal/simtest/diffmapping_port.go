package simtest

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// PortDiffAllowance names only this producer's registered paths and emitted layers.
func PortDiffAllowance() DiffAllowance {
	return DiffAllowance{Producer: "port", Layers: []string{"port"}, Paths: []string{
		"port.Config.Name",
		"port.Config.IfIndex",
		"port.Config.Kind",
		"port.Config.AdminStatus",
		"port.Config.OperStatus",
		"port.Config.MTU",
		"port.Config.LagParent",
	}}
}

func portDiffMapping() (diffMapping, error) {
	return newDiffMapping("port", port.Port{}, `{"Name":"1/1/1","IfIndex":1,"Kind":"Other","AdminStatus":"Up","OperStatus":"Up","MTU":1500,"LagParent":"lag1"}`, func(c port.Port) port.Port { return c }, func(a, b port.Port) []trace.Change {
		return port.Diff(mustBuildPortTable(a, []port.Port{{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}}), mustBuildPortTable(b, []port.Port{{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}}))
	}, []mappedEntry{
		{Path: "port.Config.Name", Producer: "port", AllowancePath: "port.Config.Name", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "port.Config.IfIndex", Producer: "port", AllowancePath: "port.Config.IfIndex", Setup: "", Before: `1`, After: `2`},
		{Path: "port.Config.Kind", Producer: "port", AllowancePath: "port.Config.Kind", Setup: "", Before: `"Other"`, After: `"Physical"`},
		{Path: "port.Config.AdminStatus", Producer: "port", AllowancePath: "port.Config.AdminStatus", Setup: "", Before: `"Up"`, After: `"Down"`},
		{Path: "port.Config.OperStatus", Producer: "port", AllowancePath: "port.Config.OperStatus", Setup: "", Before: `"Up"`, After: `"Down"`},
		{Path: "port.Config.MTU", Producer: "port", AllowancePath: "port.Config.MTU", Setup: "", Before: `1500`, After: `1501`},
		{Path: "port.Config.LagParent", Producer: "port", AllowancePath: "port.Config.LagParent", Setup: "", Before: `"lag1"`, After: `""`},
	}, portDiffExpectation)
}

func portDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "port.Config.Name":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "", &[2]string{"port.port", "name=\"1/1/1\",kind=\"Other\",ifindex=1,admin=\"Up\",oper=\"Up\",mtu=1500,lag=\"lag1\""}, nil},
			{"port", "port", "1/1/2", "", nil, &[2]string{"port.port", "name=\"1/1/2\",kind=\"Other\",ifindex=1,admin=\"Up\",oper=\"Up\",mtu=1500,lag=\"lag1\""}},
		}, nil
	case "port.Config.IfIndex":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "ifindex", &[2]string{"port.ifindex", "1"}, &[2]string{"port.ifindex", "2"}},
		}, nil
	case "port.Config.Kind":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "kind", &[2]string{"port.kind", "Other"}, &[2]string{"port.kind", "Physical"}},
		}, nil
	case "port.Config.AdminStatus":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "admin_status", &[2]string{"port.link_state", "Up"}, &[2]string{"port.link_state", "Down"}},
		}, nil
	case "port.Config.OperStatus":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "oper_status", &[2]string{"port.link_state", "Up"}, &[2]string{"port.link_state", "Down"}},
		}, nil
	case "port.Config.MTU":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "mtu", &[2]string{"port.mtu", "1500"}, &[2]string{"port.mtu", "1501"}},
		}, nil
	case "port.Config.LagParent":
		*hits++
		return []changeRecord{
			{"port", "port", "1/1/1", "lag_parent", &[2]string{"port.lag_parent", "lag1"}, &[2]string{"port.lag_parent", ""}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
