package port_test

import (
	"fmt"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func TestTableBuiltUnderCallerNaming(t *testing.T) {
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 24, port.Port{Kind: port.Physical})
	b.Range("1/3/%d", 1, 4, port.Port{Kind: port.Physical})
	b.Add(port.Port{Name: "mgmt"})

	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}

	if got, want := tbl.Len(), 29; got != want {
		t.Fatalf("tbl.Len() = %d, want %d", got, want)
	}

	ports := tbl.Ports()
	if got, want := len(ports), 29; got != want {
		t.Fatalf("len(tbl.Ports()) = %d, want %d", got, want)
	}

	for i := 1; i <= 24; i++ {
		wantName := fmt.Sprintf("1/1/%d", i)
		if gotName := ports[i-1].Name; gotName != wantName {
			t.Errorf("ports[%d].Name = %q, want %q", i-1, gotName, wantName)
		}
		if gotKind := ports[i-1].Kind; gotKind != port.Physical {
			t.Errorf("ports[%d].Kind = %q, want %q", i-1, gotKind, port.Physical)
		}
	}

	for i := 1; i <= 4; i++ {
		wantName := fmt.Sprintf("1/3/%d", i)
		idx := 24 + i - 1
		if gotName := ports[idx].Name; gotName != wantName {
			t.Errorf("ports[%d].Name = %q, want %q", idx, gotName, wantName)
		}
		if gotKind := ports[idx].Kind; gotKind != port.Physical {
			t.Errorf("ports[%d].Kind = %q, want %q", idx, gotKind, port.Physical)
		}
	}

	if gotName := ports[28].Name; gotName != "mgmt" {
		t.Errorf("ports[28].Name = %q, want %q", gotName, "mgmt")
	}

	// Second Add of 1/1/1 fails with the duplicate name as an attribute.
	b2 := port.NewBuilder()
	b2.Range("1/1/%d", 1, 24, port.Port{Kind: port.Physical})
	b2.Range("1/3/%d", 1, 4, port.Port{Kind: port.Physical})
	b2.Add(port.Port{Name: "mgmt"})
	b2.Add(port.Port{Name: "1/1/1"})

	_, err2 := b2.Build()
	if err2 == nil {
		t.Fatal("Build() with duplicate port succeeded, want error")
	}

	attrs := errs.Attributes(err2)
	if got, want := attrs["name"], "1/1/1"; got != want {
		t.Errorf("errs.Attributes(err)[\"name\"] = %v, want %v", got, want)
	}
}

func TestResolution(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "lag1", Kind: port.Lag})
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, LagParent: "lag1"})

	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	t.Run("member resolution", func(t *testing.T) {
		got, ok := tbl.Resolve("1/1/2")
		if !ok {
			t.Fatal("Resolve(\"1/1/2\") ok = false, want true")
		}
		if got.Name != "lag1" {
			t.Errorf("Resolve(\"1/1/2\").Name = %q, want %q", got.Name, "lag1")
		}
		if got.Kind != port.Lag {
			t.Errorf("Resolve(\"1/1/2\").Kind = %q, want %q", got.Kind, port.Lag)
		}
	})

	t.Run("plain port resolution", func(t *testing.T) {
		got, ok := tbl.Resolve("1/1/1")
		if !ok {
			t.Fatal("Resolve(\"1/1/1\") ok = false, want true")
		}
		if got.Name != "1/1/1" {
			t.Errorf("Resolve(\"1/1/1\").Name = %q, want %q", got.Name, "1/1/1")
		}
		if got.Kind != port.Physical {
			t.Errorf("Resolve(\"1/1/1\").Kind = %q, want %q", got.Kind, port.Physical)
		}
	})

	t.Run("lag port resolution", func(t *testing.T) {
		got, ok := tbl.Resolve("lag1")
		if !ok {
			t.Fatal("Resolve(\"lag1\") ok = false, want true")
		}
		if got.Name != "lag1" {
			t.Errorf("Resolve(\"lag1\").Name = %q, want %q", got.Name, "lag1")
		}
	})

	t.Run("missing port resolution", func(t *testing.T) {
		_, ok := tbl.Resolve("unknown")
		if ok {
			t.Error("Resolve(\"unknown\") ok = true, want false")
		}
	})

	t.Run("lag members", func(t *testing.T) {
		members := tbl.Members("lag1")
		if got, want := len(members), 2; got != want {
			t.Fatalf("len(Members(\"lag1\")) = %d, want %d", got, want)
		}
		if members[0].Name != "1/1/2" || members[1].Name != "1/1/3" {
			t.Errorf("Members(\"lag1\") = [%s, %s], want [1/1/2, 1/1/3]", members[0].Name, members[1].Name)
		}
	})
}

func TestBuilderRules(t *testing.T) {
	t.Run("pattern without exactly one %d verb", func(t *testing.T) {
		cases := []struct {
			name    string
			pattern string
		}{
			{name: "no verbs", pattern: "port"},
			{name: "wrong verb string", pattern: "port/%s"},
			{name: "wrong verb value", pattern: "port/%v"},
			{name: "two d verbs", pattern: "port/%d/%d"},
			{name: "escaped percent", pattern: "port/%%d"},
			{name: "incomplete verb", pattern: "port/%"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				b := port.NewBuilder()
				b.Range(tc.pattern, 1, 4, port.Port{Kind: port.Physical})
				_, err := b.Build()
				if err == nil {
					t.Fatalf("Range(%q) succeeded, want error", tc.pattern)
				}
				attrs := errs.Attributes(err)
				if got, want := attrs["pattern"], tc.pattern; got != want {
					t.Errorf("errs.Attributes(err)[\"pattern\"] = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "eth0", Kind: port.Physical})
		b.Add(port.Port{Name: "eth0", Kind: port.Physical})
		_, err := b.Build()
		if err == nil {
			t.Fatal("duplicate Add succeeded, want error")
		}
		attrs := errs.Attributes(err)
		if got, want := attrs["name"], "eth0"; got != want {
			t.Errorf("errs.Attributes(err)[\"name\"] = %v, want %v", got, want)
		}
	})

	t.Run("parent that is not a LAG", func(t *testing.T) {
		t.Run("parent is physical port", func(t *testing.T) {
			b := port.NewBuilder()
			b.Add(port.Port{Name: "eth0", Kind: port.Physical})
			b.Add(port.Port{Name: "eth1", Kind: port.Physical, LagParent: "eth0"})
			_, err := b.Build()
			if err == nil {
				t.Fatal("parent is physical port succeeded, want error")
			}
			attrs := errs.Attributes(err)
			if got, want := attrs["parent"], "eth0"; got != want {
				t.Errorf("errs.Attributes(err)[\"parent\"] = %v, want %v", got, want)
			}
		})

		t.Run("parent does not exist", func(t *testing.T) {
			b := port.NewBuilder()
			b.Add(port.Port{Name: "eth1", Kind: port.Physical, LagParent: "missing_lag"})
			_, err := b.Build()
			if err == nil {
				t.Fatal("missing parent succeeded, want error")
			}
			attrs := errs.Attributes(err)
			if got, want := attrs["parent"], "missing_lag"; got != want {
				t.Errorf("errs.Attributes(err)[\"parent\"] = %v, want %v", got, want)
			}
		})
	})

	t.Run("LAG with a parent", func(t *testing.T) {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "lag1", Kind: port.Lag})
		b.Add(port.Port{Name: "lag2", Kind: port.Lag, LagParent: "lag1"})
		_, err := b.Build()
		if err == nil {
			t.Fatal("LAG with a parent succeeded, want error")
		}
		attrs := errs.Attributes(err)
		if got, want := attrs["parent"], "lag1"; got != want {
			t.Errorf("errs.Attributes(err)[\"parent\"] = %v, want %v", got, want)
		}
	})
}

func TestDiff(t *testing.T) {
	t.Run("one added port and one admin change", func(t *testing.T) {
		t1, err := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Down}).
			Build()
		if err != nil {
			t.Fatalf("Build t1: %v", err)
		}

		t2, err := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up}).
			Build()
		if err != nil {
			t.Fatalf("Build t2: %v", err)
		}

		diffs := port.Diff(t1, t2)
		if got, want := len(diffs), 2; got != want {
			t.Fatalf("len(diffs) = %d, want %d", got, want)
		}

		c0 := diffs[0]
		if c0.Layer != port.LayerPort {
			t.Errorf("c0.Layer = %q, want %q", c0.Layer, port.LayerPort)
		}
		if c0.Subject.Kind != "port" || c0.Subject.Key != "1/1/1" {
			t.Errorf("c0.Subject = %+v, want port:1/1/1", c0.Subject)
		}
		if c0.Field != "admin_status" {
			t.Errorf("c0.Field = %q, want %q", c0.Field, "admin_status")
		}
		if c0.From != port.Down {
			t.Errorf("c0.From = %v, want %v", c0.From, port.Down)
		}
		if c0.To != port.Up {
			t.Errorf("c0.To = %v, want %v", c0.To, port.Up)
		}

		c1 := diffs[1]
		if c1.Layer != port.LayerPort {
			t.Errorf("c1.Layer = %q, want %q", c1.Layer, port.LayerPort)
		}
		if c1.Subject.Kind != "port" || c1.Subject.Key != "1/1/2" {
			t.Errorf("c1.Subject = %+v, want port:1/1/2", c1.Subject)
		}
		if c1.Field != "" {
			t.Errorf("c1.Field = %q, want empty", c1.Field)
		}
		if c1.From != nil {
			t.Errorf("c1.From = %v, want nil", c1.From)
		}
		p2, ok := c1.To.(port.Port)
		if !ok || p2.Name != "1/1/2" {
			t.Errorf("c1.To = %+v, want port 1/1/2", c1.To)
		}
	})

	t.Run("removed port, mtu, and lag parent changes", func(t *testing.T) {
		t1, err := port.NewBuilder().
			Add(port.Port{Name: "lag1", Kind: port.Lag}).
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, MTU: 1500, LagParent: "lag1"}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
			Build()
		if err != nil {
			t.Fatalf("Build t1: %v", err)
		}

		t2, err := port.NewBuilder().
			Add(port.Port{Name: "lag1", Kind: port.Lag}).
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, MTU: 9000, LagParent: ""}).
			Build()
		if err != nil {
			t.Fatalf("Build t2: %v", err)
		}

		diffs := port.Diff(t1, t2)
		if got, want := len(diffs), 3; got != want {
			t.Fatalf("len(diffs) = %d, want %d", got, want)
		}

		if diffs[0].Field != "mtu" || diffs[0].From != 1500 || diffs[0].To != 9000 {
			t.Errorf("diffs[0] = %+v, want mtu change 1500 -> 9000", diffs[0])
		}
		if diffs[1].Field != "lag_parent" || diffs[1].From != "lag1" || diffs[1].To != "" {
			t.Errorf("diffs[1] = %+v, want lag_parent change lag1 -> \"\"", diffs[1])
		}
		if diffs[2].Field != "" || diffs[2].Subject.Key != "1/1/2" || diffs[2].To != nil {
			t.Errorf("diffs[2] = %+v, want removed port 1/1/2", diffs[2])
		}
	})

	t.Run("identical tables yield empty diff", func(t *testing.T) {
		tbl, err := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, MTU: 1500}).
			Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}

		diffs := port.Diff(tbl, tbl)
		if len(diffs) != 0 {
			t.Errorf("Diff(tbl, tbl) = %+v, want empty", diffs)
		}
	})
}

func TestPortForwards(t *testing.T) {
	cases := []struct {
		name        string
		admin       port.LinkState
		oper        port.LinkState
		wantForward bool
	}{
		{name: "up and up", admin: port.Up, oper: port.Up, wantForward: true},
		{name: "admin down", admin: port.Down, oper: port.Up, wantForward: false},
		{name: "oper down", admin: port.Up, oper: port.Down, wantForward: false},
		{name: "both down", admin: port.Down, oper: port.Down, wantForward: false},
		{name: "unreported admin", admin: port.Unreported, oper: port.Up, wantForward: true},
		{name: "unreported oper", admin: port.Up, oper: port.Unreported, wantForward: true},
		{name: "both unreported", admin: port.Unreported, oper: port.Unreported, wantForward: true},
		{name: "zero values", admin: "", oper: "", wantForward: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := port.Port{AdminStatus: tc.admin, OperStatus: tc.oper}
			if got := p.Forwards(); got != tc.wantForward {
				t.Errorf("Port{admin: %q, oper: %q}.Forwards() = %v, want %v", tc.admin, tc.oper, got, tc.wantForward)
			}
		})
	}
}

func TestTableClone(t *testing.T) {
	t1, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, IfIndex: 10, MTU: 1500}).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	t2 := t1.Clone()
	if got, want := t2.Len(), t1.Len(); got != want {
		t.Fatalf("t2.Len() = %d, want %d", got, want)
	}

	p1, _ := t1.Port("1/1/1")
	p2, _ := t2.Port("1/1/1")

	if p1 != p2 {
		t.Errorf("cloned port = %+v, want %+v", p2, p1)
	}
}

func TestMembersOfANameThatIsNotALag(t *testing.T) {
	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"}).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, name := range []string{"", "1/1/1", "absent"} {
		if got := tbl.Members(name); got != nil {
			t.Errorf("Members(%q) = %v, want nil", name, got)
		}
	}
	if got := tbl.Members("lag1"); len(got) != 1 || got[0].Name != "1/1/2" {
		t.Errorf("Members(lag1) = %v, want [1/1/2]", got)
	}
}

func TestLayerConstants(t *testing.T) {
	layers := []struct {
		constant trace.Layer
		expected string
	}{
		{port.LayerPort, "port"},
		{port.LayerLag, "lag"},
		{port.LayerEthernet, "ethernet"},
		{port.LayerPoe, "poe"},
		{port.LayerRelay, "relay"},
		{port.LayerVlan, "vlan"},
	}

	for _, l := range layers {
		if string(l.constant) != l.expected {
			t.Errorf("layer constant %q != expected %q", l.constant, l.expected)
		}
	}
}

func TestTableValidate(t *testing.T) {
	t.Run("empty table is valid", func(t *testing.T) {
		var tbl port.Table
		if err := tbl.Validate(); err != nil {
			t.Errorf("empty table Validate() error = %v, want nil", err)
		}
	})
}
