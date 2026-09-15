package stp

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// TestTreeForAnswersForEveryVLAN pins the mapping this phase lands: one tree
// carries every VLAN, including the ones vlan.ID.Valid rejects, so no caller has
// to check a VID before asking the gate about it.
func TestTreeForAnswersForEveryVLAN(t *testing.T) {
	t.Parallel()

	l := newLayer(Config{
		Ports: map[string]Port{"1/1/1": {}, "1/1/2": {}},
	}.Normalize())

	cist := l.cist()
	if cist == nil {
		t.Fatal("cist() = nil, want the tree every bridge runs")
	}

	for vid := vlan.ID(0); vid <= 4095; vid++ {
		got := l.treeFor(vid)
		if got == nil {
			t.Fatalf("treeFor(%d) = nil, want the CIST", vid)
		}
		if got != cist {
			t.Fatalf("treeFor(%d) = tree %d, want the CIST", vid, got.id)
		}
	}

	if n := len(l.trees); n != 1 {
		t.Errorf("trees = %d, want exactly one while the bridge runs rapid spanning tree", n)
	}
}

// TestPortNamesStayBridgeGlobal guards the seam the tree keying could have
// broken: the identifier and the iteration order belong to the bridge, not to a
// tree, so a second tree cannot renumber a port or reorder a flush list.
func TestPortNamesStayBridgeGlobal(t *testing.T) {
	t.Parallel()

	l := newLayer(Config{
		Ports: map[string]Port{"1/1/3": {}, "1/1/1": {}, "1/1/2": {}},
	}.Normalize())

	want := []string{"1/1/1", "1/1/2", "1/1/3"}
	if len(l.portNames) != len(want) {
		t.Fatalf("portNames = %v, want %v", l.portNames, want)
	}
	for i, name := range want {
		if l.portNames[i] != name {
			t.Fatalf("portNames = %v, want %v", l.portNames, want)
		}
	}

	// The identifier's low byte is the index in that order, counted from one.
	for i, name := range want {
		p := l.cist().ports[name]
		if got, wantIdx := p.portID&0xff, uint16(i+1); got != wantIdx {
			t.Errorf("port %q identifier index = %d, want %d", name, got, wantIdx)
		}
	}
}
