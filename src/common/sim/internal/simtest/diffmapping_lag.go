package simtest

import (
	"reflect"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// AssertMappedDiffRejectsOmittedFields checks owner and delegated mappings with
// each named field removed while an unrelated record remains. It checks the
// registered independent expectations and exact ordered matching.
func AssertMappedDiffRejectsOmittedFields(t *testing.T, producer string, fields map[string]string) {
	t.Helper()
	hits := 0
	producerPath := func(path string) string {
		if strings.HasPrefix(path, producer+".Config.") {
			return path
		}
		_, tail, _ := strings.Cut(path, "."+map[string]string{"lag": "LAG", "stp": "STP"}[producer]+".")
		return producer + ".Config." + tail
	}
	for _, owner := range []string{producer, "vswitch", "assembly"} {
		m, err := diffMappingFor(owner)
		if err != nil {
			t.Fatal(err)
		}
		unrelatedPath := map[string]string{"lag": "lag.Config.LAGs[lag1].HashBasis", "stp": "stp.Config.HelloTime"}[producer]
		var unrelated []trace.Change
		for _, entry := range m.entries {
			if entry.Producer != producer || producerPath(entry.Path) != unrelatedPath {
				continue
			}
			a, b, err := prepareMappedInputs(m, entry)
			if err != nil {
				t.Fatal(err)
			}
			unrelated = m.diff(m.normalize(a), m.normalize(b))
		}
		if len(unrelated) != 1 {
			t.Fatalf("%s unrelated scalar records = %d, want 1", owner, len(unrelated))
		}
		for _, entry := range m.entries {
			field, selected := fields[producerPath(entry.Path)]
			if entry.Producer != producer || !selected {
				continue
			}
			t.Run(entry.Path, func(t *testing.T) {
				controlHits := 0
				if err := checkMappedLeaf(m, entry, &controlHits); err != nil {
					t.Fatalf("complete mapping: %v", err)
				}
				if controlHits != 1 {
					t.Fatalf("control expectations = %d, want 1", controlHits)
				}
				broken := m
				broken.diff = func(a, b reflect.Value) []trace.Change {
					got := m.diff(a, b)
					kept := append([]trace.Change{}, unrelated...)
					removed := 0
					for _, change := range got {
						if change.Layer == trace.Layer(producer) && change.Field == field {
							removed++
							continue
						}
						kept = append(kept, change)
					}
					if removed != 1 {
						t.Fatalf("removed %d %s records, want 1", removed, field)
					}
					return kept
				}
				if err := checkMappedLeaf(broken, entry, &hits); err == nil {
					t.Error("missing field accepted with an unrelated record")
				}
			})
		}
	}
	want := map[string]int{"lag": 6, "stp": 3}[producer]
	if hits != want {
		t.Errorf("concrete omission expectations = %d, want %d", hits, want)
	}
}

func lagDiffMapping() (diffMapping, error) {
	return newDiffMapping("lag", lag.Config{}, `{"LAGs":{"lag1":{"Mode":"BalanceSLB","Primary":"1/1/1","UpDelay":"2s","DownDelay":"1s","HashBasis":7,"MinLinks":1,"RebalanceInterval":"45s","LACP":{"Mode":"Active","Fast":true,"SystemPriority":32768,"SystemID":"02:00:00:00:00:01","Key":1,"Fallback":true},"Members":{"1/1/1":{"Priority":128,"Key":1}}}}}`, func(c lag.Config) lag.Config { return c.Normalize(layer.Env{}) }, lag.Diff, []mappedEntry{
		{Path: "lag.Config.LAGs[lag1].Mode", Producer: "lag", Setup: "", Before: `"BalanceSLB"`, After: `"BalanceTCP"`},
		{Path: "lag.Config.LAGs[lag1].Primary", Producer: "lag", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
		{Path: "lag.Config.LAGs[lag1].UpDelay", Producer: "lag", Setup: "", Before: `"2s"`, After: `"3s"`},
		{Path: "lag.Config.LAGs[lag1].DownDelay", Producer: "lag", Setup: "", Before: `"1s"`, After: `"2s"`},
		{Path: "lag.Config.LAGs[lag1].HashBasis", Producer: "lag", Setup: "", Before: `7`, After: `8`},
		{Path: "lag.Config.LAGs[lag1].MinLinks", Producer: "lag", Setup: "", Before: `1`, After: `2`},
		{Path: "lag.Config.LAGs[lag1].RebalanceInterval", Producer: "lag", Setup: "", Before: `"45s"`, After: `"46s"`},
		{Path: "lag.Config.LAGs[lag1].LACP.Mode", Producer: "lag", Setup: "", Before: `"Active"`, After: `"Passive"`},
		{Path: "lag.Config.LAGs[lag1].LACP.Fast", Producer: "lag", Setup: "", Before: `true`, After: `false`},
		{Path: "lag.Config.LAGs[lag1].LACP.SystemPriority", Producer: "lag", Setup: "", Before: `32768`, After: `32769`},
		{Path: "lag.Config.LAGs[lag1].LACP.SystemID", Producer: "lag", Setup: "", Before: `"02:00:00:00:00:01"`, After: `"02:00:00:00:00:02"`},
		{Path: "lag.Config.LAGs[lag1].LACP.Key", Producer: "lag", Setup: "", Before: `1`, After: `2`},
		{Path: "lag.Config.LAGs[lag1].LACP.Fallback", Producer: "lag", Setup: "", Before: `true`, After: `false`},
		{Path: "lag.Config.LAGs[lag1].Members[1/1/1].Priority", Producer: "lag", Setup: "", Before: `128`, After: `129`},
		{Path: "lag.Config.LAGs[lag1].Members[1/1/1].Key", Producer: "lag", Setup: "", Before: `1`, After: `2`},
		{Path: "lag.Config.LAGs[lag1].@key", Producer: "lag", Setup: "", Before: `"lag1"`, After: `"lag2"`},
		{Path: "lag.Config.LAGs[lag1].RebalanceInterval.@present", Producer: "lag", Setup: "", Before: `"45s"`, After: `null`},
		{Path: "lag.Config.LAGs[lag1].Members[1/1/1].@key", Producer: "lag", Setup: "", Before: `"1/1/1"`, After: `"1/1/2"`},
	}, lagDiffExpectation)
}

func lagDiffExpectation(path string, hits *int) ([]changeRecord, error) {
	switch path {
	case "lag.Config.LAGs[lag1].Mode":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "mode", &[2]string{"lag.bond_mode", "BalanceSLB"}, &[2]string{"lag.bond_mode", "BalanceTCP"}},
		}, nil
	case "lag.Config.LAGs[lag1].Primary":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "primary", &[2]string{"lag.primary", "1/1/1"}, &[2]string{"lag.primary", "1/1/2"}},
		}, nil
	case "lag.Config.LAGs[lag1].UpDelay":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "up_delay", &[2]string{"lag.duration", "2s"}, &[2]string{"lag.duration", "3s"}},
		}, nil
	case "lag.Config.LAGs[lag1].DownDelay":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "down_delay", &[2]string{"lag.duration", "1s"}, &[2]string{"lag.duration", "2s"}},
		}, nil
	case "lag.Config.LAGs[lag1].HashBasis":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "hash_basis", &[2]string{"lag.hash_basis", "7"}, &[2]string{"lag.hash_basis", "8"}},
		}, nil
	case "lag.Config.LAGs[lag1].MinLinks":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "min_links", &[2]string{"lag.min_links", "1"}, &[2]string{"lag.min_links", "2"}},
		}, nil
	case "lag.Config.LAGs[lag1].RebalanceInterval":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "rebalance_interval", &[2]string{"lag.rebalance_interval", "45s"}, &[2]string{"lag.rebalance_interval", "46s"}},
		}, nil
	case "lag.Config.LAGs[lag1].LACP.Mode":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_mode", &[2]string{"lag.lacp_mode", "Active"}, &[2]string{"lag.lacp_mode", "Passive"}},
		}, nil
	case "lag.Config.LAGs[lag1].LACP.Fast":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_fast", &[2]string{"lag.bool", "true"}, &[2]string{"lag.bool", "false"}},
		}, nil
	case "lag.Config.LAGs[lag1].LACP.SystemPriority":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_system_priority", &[2]string{"lag.uint16", "32768"}, &[2]string{"lag.uint16", "32769"}},
		}, nil
	case "lag.Config.LAGs[lag1].LACP.SystemID":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_system_id", &[2]string{"lag.mac", "02:00:00:00:00:01"}, &[2]string{"lag.mac", "02:00:00:00:00:02"}},
		}, nil
	case "lag.Config.LAGs[lag1].LACP.Key":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_key", &[2]string{"lag.uint16", "1"}, &[2]string{"lag.uint16", "2"}},
		}, nil
	case "lag.Config.LAGs[lag1].LACP.Fallback":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "lacp_fallback", &[2]string{"lag.bool", "true"}, &[2]string{"lag.bool", "false"}},
		}, nil
	case "lag.Config.LAGs[lag1].Members[1/1/1].Priority":
		*hits++
		return []changeRecord{
			{"lag", "lag_member", "\"lag1\"/\"1/1/1\"", "priority", &[2]string{"lag.port_priority", "128"}, &[2]string{"lag.port_priority", "129"}},
		}, nil
	case "lag.Config.LAGs[lag1].Members[1/1/1].Key":
		*hits++
		return []changeRecord{
			{"lag", "lag_member", "\"lag1\"/\"1/1/1\"", "key", &[2]string{"lag.uint16", "1"}, &[2]string{"lag.uint16", "2"}},
		}, nil
	case "lag.Config.LAGs[lag1].@key":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "", &[2]string{"lag.lag", "mode=\"BalanceSLB\";primary=\"1/1/1\";up_delay_ns=2000000000;down_delay_ns=1000000000;hash_basis=7;min_links=1;rebalance_interval=45s;lacp={mode=\"Active\";fast=true;system_priority=32768;system_id=\"02:00:00:00:00:01\";key=1;fallback=true};members={\"1/1/1\":{priority=128;key=1}}"}, nil},
			{"lag", "lag", "lag2", "", nil, &[2]string{"lag.lag", "mode=\"BalanceSLB\";primary=\"1/1/1\";up_delay_ns=2000000000;down_delay_ns=1000000000;hash_basis=7;min_links=1;rebalance_interval=45s;lacp={mode=\"Active\";fast=true;system_priority=32768;system_id=\"02:00:00:00:00:01\";key=1;fallback=true};members={\"1/1/1\":{priority=128;key=1}}"}},
		}, nil
	case "lag.Config.LAGs[lag1].RebalanceInterval.@present":
		*hits++
		return []changeRecord{
			{"lag", "lag", "lag1", "rebalance_interval", &[2]string{"lag.rebalance_interval", "45s"}, &[2]string{"lag.rebalance_interval", "10s"}},
		}, nil
	case "lag.Config.LAGs[lag1].Members[1/1/1].@key":
		*hits++
		return []changeRecord{
			{"lag", "lag_member", "\"lag1\"/\"1/1/1\"", "", &[2]string{"lag.member", "priority=128,key=1"}, nil},
			{"lag", "lag_member", "\"lag1\"/\"1/1/2\"", "", nil, &[2]string{"lag.member", "priority=128,key=1"}},
		}, nil
	default:
		return nil, errs.Msgf("no independent expectation for %s", path)
	}
}
