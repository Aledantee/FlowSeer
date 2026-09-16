package stp

import (
	"fmt"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Tree holds the configuration of one Per-VLAN Spanning Tree instance: its
// bridge priority for that VLAN's tree, and its per-port settings.
type Tree struct {
	Priority        uint16
	PriorityPresent bool
	Ports           map[string]InstancePort
}

// TypeID returns the fact type identifier for Tree.
func (t Tree) TypeID() string { return "stp.pvst.tree" }

// Canonical returns the canonical string representation of the Tree fact:
// its effective priority and its sorted per-port settings.
func (t Tree) Canonical() string {
	names := sortedKeys(t.Ports)
	ports := make([]string, len(names))
	for idx, name := range names {
		ports[idx] = fmt.Sprintf("%s:{%s}", name, t.Ports[name].Canonical())
	}

	return fmt.Sprintf("priority=%d,ports=%v",
		effectiveInstancePriority(t.Priority, t.PriorityPresent), ports)
}

// PVST holds the Per-VLAN Rapid Spanning Tree configuration of a virtual
// switch: one independent spanning tree per VLAN. Its presence on Config
// selects PVST+ over RSTP.
type PVST struct {
	Trees map[vlan.ID]Tree
}

// TypeID returns the fact type identifier for PVST.
func (p PVST) TypeID() string { return "stp.pvst" }

// Canonical returns the canonical string representation of the PVST fact:
// the sorted list of per-VLAN trees.
func (p PVST) Canonical() string {
	vids := sortedVLANIDs(p.Trees)
	trees := make([]string, len(vids))
	for idx, vid := range vids {
		trees[idx] = fmt.Sprintf("%d:{%s}", vid, p.Trees[vid].Canonical())
	}

	return fmt.Sprintf("trees=%v", trees)
}

// Clone returns a deep copy of the PVST configuration.
func (p PVST) Clone() PVST {
	cloned := p
	if p.Trees != nil {
		cloned.Trees = make(map[vlan.ID]Tree, len(p.Trees))
		for vid, tree := range p.Trees {
			cloned.Trees[vid] = tree.clone()
		}
	}
	return cloned
}

func (t Tree) clone() Tree {
	cloned := t
	if t.Ports != nil {
		cloned.Ports = make(map[string]InstancePort, len(t.Ports))
		for name, p := range t.Ports {
			cloned.Ports[name] = p
		}
	}
	return cloned
}

// Normalize returns a normalized copy of the PVST configuration, filling
// each tree's priority with the default bridge priority when unset and
// inserting a default VLAN 1 tree when Trees holds none. An explicitly
// configured VLAN 1 tree is left alone.
func (p PVST) Normalize() PVST {
	cloned := p.Clone()
	if len(cloned.Trees) == 0 {
		cloned.Trees = map[vlan.ID]Tree{1: {}}
	}
	for vid, tree := range cloned.Trees {
		tree.Priority = effectiveInstancePriority(tree.Priority, tree.PriorityPresent)
		tree.PriorityPresent = true
		cloned.Trees[vid] = tree
	}
	return cloned
}

// Validate checks the PVST configuration: every VID must fall in 1..4094,
// each tree priority must be a multiple of 4096, and every tree port must be
// a spanning tree port with an admin path cost within bounds. stpPorts is the
// STP port set (Config.Ports): the layer only ever looks up a tree port
// there, so a name absent from it is refused even when the port table itself
// holds it.
func (p PVST) Validate(ports port.Table, stpPorts map[string]Port) error {
	for _, vid := range sortedVLANIDs(p.Trees) {
		if vid < 1 || vid > 4094 {
			return errs.New().
				Attr("field", fmt.Sprintf("pvst.trees.%d", vid)).
				Attr("vid", vid).
				Msgf("PVST tree VID %d is outside 1 through 4094", vid)
		}

		tree := p.Trees[vid]
		priority := effectiveInstancePriority(tree.Priority, tree.PriorityPresent)
		if priority%4096 != 0 {
			return errs.New().
				Attr("field", fmt.Sprintf("pvst.trees.%d.priority", vid)).
				Attr("vid", vid).
				Attr("priority", priority).
				Msgf("PVST tree %d priority %d must be a multiple of 4096", vid, priority)
		}

		for _, name := range sortedKeys(tree.Ports) {
			if _, ok := ports.Port(name); !ok {
				return errs.New().
					Attr("field", fmt.Sprintf("pvst.trees.%d.ports.%s", vid, name)).
					Attr("vid", vid).
					Attr("port", name).
					Msgf("PVST tree %d port %q absent from port table", vid, name)
			}
			if _, ok := stpPorts[name]; !ok {
				return errs.New().
					Attr("field", fmt.Sprintf("pvst.trees.%d.ports.%s", vid, name)).
					Attr("vid", vid).
					Attr("port", name).
					Msgf("PVST tree %d port %q is not a spanning tree port", vid, name)
			}

			treePort := tree.Ports[name]
			if treePort.PathCost > MaxPathCost {
				return errs.New().
					Attr("field", fmt.Sprintf("pvst.trees.%d.ports.%s.path_cost", vid, name)).
					Attr("vid", vid).
					Attr("port", name).
					Attr("path_cost", treePort.PathCost).
					Msgf("PVST tree %d port %q path cost %d exceeds maximum %d", vid, name, treePort.PathCost, MaxPathCost)
			}
		}
	}

	return nil
}

func sortedVLANIDs(m map[vlan.ID]Tree) []vlan.ID {
	vids := make([]vlan.ID, 0, len(m))
	for vid := range m {
		vids = append(vids, vid)
	}
	slices.Sort(vids)

	return vids
}
