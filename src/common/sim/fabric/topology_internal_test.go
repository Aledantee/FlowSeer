package fabric

import (
	"errors"
	"strings"
	"testing"
	"time"

	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/trace"

	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

type linkErrorDevice struct {
	device.Device
	ports port.Table
	err   error
}

func (d *linkErrorDevice) Fork() device.Device { cp := *d; cp.ports = d.ports.Clone(); return &cp }

func (d *linkErrorDevice) NextWake() (time.Time, bool) { return time.Time{}, false }

func (d *linkErrorDevice) Ports() port.Table { return d.ports }
func (d *linkErrorDevice) State(time.Time) device.State {
	return device.State{Ports: d.ports.Ports()}
}

func (d *linkErrorDevice) LinkChange(time.Time, string, device.Link) (device.Output, error) {
	return device.Output{}, d.err
}

func TestLinkChangeErrorReachesNew(t *testing.T) {
	ports, err := port.NewBuilder().Add(port.Port{Name: "p", Kind: port.Physical, AdminStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("built device rejected link")
	node := Node{Device: &linkErrorDevice{ports: ports, err: want}, Snapshot: true}
	t.Run("build-and-start", func(t *testing.T) {
		f, err := New(Config{Nodes: map[string]Node{"n": node}})
		if !errors.Is(err, want) || f != nil {
			t.Fatalf("built topology = %v, %v, want nil, %v", f, err, want)
		}
	})
	t.Run("replace", func(t *testing.T) {
		oldPorts, err := port.NewBuilder().Add(port.Port{Name: "old", Kind: port.Physical, AdminStatus: port.Up}).Build()
		if err != nil {
			t.Fatal(err)
		}
		old := Node{Device: &linkErrorDevice{ports: oldPorts}, Snapshot: true}
		f, err := New(Config{Nodes: map[string]Node{"n": old}})
		if err != nil {
			t.Fatal(err)
		}
		if got := f.Snapshot().Devices["n"].Ports; len(got) != 1 || got[0].Name != "old" {
			t.Fatalf("initial node ports = %v, want one port named old", got)
		}
		if err := Replace(f, "n", node); !errors.Is(err, want) {
			t.Fatalf("replace error = %v, want %v", err, want)
		}
		if got := f.Snapshot().Devices["n"].Ports; len(got) != 1 || got[0].Name != "old" {
			t.Fatalf("node ports after failed replace = %v, want one port named old", got)
		}
	})
	t.Run("operate", func(t *testing.T) {
		d := &linkErrorDevice{ports: ports}
		start := time.Unix(10, 0)
		f, err := New(Config{Nodes: map[string]Node{"n": {Device: d}}, Start: start})
		if err != nil {
			t.Fatal(err)
		}
		d.err = want
		called := false
		err = Operate(f, "n", func(live device.Device, now time.Time) (device.Output, error) {
			called = true
			if now != start {
				t.Errorf("operation clock = %v, want %v", now, start)
			}
			return live.LinkChange(now, "p", device.Link{State: port.Up})
		})
		if !called || !errors.Is(err, want) {
			t.Fatalf("operate called = %v, error = %v, want true, %v", called, err, want)
		}
	})
}

func TestFabricRefusesUnknownEndpoint(t *testing.T) {
	for _, ep := range []Endpoint{{Node: "missing"}, {Node: "n", Port: "missing"}} {
		t.Run(ep.Canonical(), func(t *testing.T) {
			f, err := New(Config{Nodes: map[string]Node{"n": {Device: &linkErrorDevice{}, Physical: map[string]cablemodel.End{"p": {}}}}, Cables: []Cable{{A: Endpoint{Node: "n", Port: "p"}, B: ep}}})
			if err == nil || f != nil || !strings.Contains(err.Error(), "missing") {
				t.Fatalf("unknown endpoint = %v, %v, want nil and missing", f, err)
			}
		})
	}
}

func TestFabricRefusesTwoCablesOnAPort(t *testing.T) {
	d := &linkErrorDevice{}
	c := Cable{A: Endpoint{Node: "a", Port: "p"}, B: Endpoint{Node: "b", Port: "p"}}
	f, err := New(Config{Nodes: map[string]Node{
		"a": {Device: d, Physical: map[string]cablemodel.End{"p": {}}},
		"b": {Device: d, Physical: map[string]cablemodel.End{"p": {}}},
	}, Cables: []Cable{c, c}})
	if err == nil || f != nil || !strings.Contains(err.Error(), "two cables") {
		t.Fatalf("duplicate port error = %v, want two cables", err)
	}
}

type stubOperation struct{ ports port.Table }

func (o stubOperation) Apply(f *Fabric, node string) error {
	return Operate(f, node, func(d device.Device, _ time.Time) (device.Output, error) {
		d.(*linkErrorDevice).ports = o.ports
		return device.Output{}, nil
	})
}

func (o stubOperation) Diff(other Operation) []trace.Change {
	next := other.(stubOperation)
	return []trace.Change{{Layer: "stub", Subject: trace.Subject{Kind: "stub-device", Key: "n"}, Field: "ports", From: Endpoint{Port: o.ports.Ports()[0].Name}, To: Endpoint{Port: next.ports.Ports()[0].Name}}}
}

func TestOperationNeedsNoAssembly(t *testing.T) {
	oldPorts, err := port.NewBuilder().Add(port.Port{Name: "old", Kind: port.Physical}).Build()
	if err != nil {
		t.Fatal(err)
	}
	newPorts, err := port.NewBuilder().Add(port.Port{Name: "new", Kind: port.Physical}).Build()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(10, 0)
	f, err := New(Config{Start: start, Nodes: map[string]Node{"n": {Device: &linkErrorDevice{ports: oldPorts}, Ports: oldPorts, Snapshot: true}}})
	if err != nil {
		t.Fatal(err)
	}
	before := Action{ID: "topology_internal_test-1", At: start.Add(time.Second), Index: 7, Kind: ActionConfigure, Configure: &ConfigureAction{Node: "n", Operation: stubOperation{ports: oldPorts}}}
	after := before.Clone()
	after.Configure.Operation = stubOperation{ports: newPorts}
	result, err := f.RunScenario(Scenario{Name: "stub-operation", Actions: []Action{after}}, RunOptions{Budget: 10, Window: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Snapshot().Devices["n"].Ports; len(got) != 1 || got[0].Name != "new" {
		t.Fatalf("operation ports = %v, want new", got)
	}
	if !result.Clock.Equal(after.At) {
		t.Fatalf("operation clock = %v, want %v", result.Clock, after.At)
	}
	changes := before.Diff(after)
	if len(changes) != 1 {
		t.Fatalf("operation changes = %v, want one", changes)
	}
	c := changes[0]
	if c.Layer != "stub" || c.Subject.Kind != "scenario.action" || c.Subject.Key != `"topology_internal_test-1"/"stub-device"/"n"` || c.Field != "ports" || c.From.Canonical() != (Endpoint{Port: "old"}).Canonical() || c.To.Canonical() != (Endpoint{Port: "new"}).Canonical() {
		t.Fatalf("operation change = %+v, want old -> new with nested subject", c)
	}
}
