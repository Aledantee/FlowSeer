package stp

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// macFact wraps a netaddr.MAC as a trace.Fact.
type macFact netaddr.MAC

// TypeID returns the fact type identifier for macFact.
func (f macFact) TypeID() string { return "stp.mac" }

// Canonical returns the formatted MAC string.
func (f macFact) Canonical() string { return netaddr.MAC(f).String() }

// priorityFact wraps a bridge priority as a trace.Fact.
type priorityFact uint16

// TypeID returns the fact type identifier for priorityFact.
func (f priorityFact) TypeID() string { return "stp.priority" }

// Canonical returns the decimal string of the priority.
func (f priorityFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// durationFact wraps a time.Duration as a trace.Fact.
type durationFact time.Duration

// TypeID returns the fact type identifier for durationFact.
func (f durationFact) TypeID() string { return "stp.duration" }

// Canonical returns the formatted duration string.
func (f durationFact) Canonical() string { return time.Duration(f).String() }

// txHoldCountFact wraps a tx hold count as a trace.Fact.
type txHoldCountFact uint8

// TypeID returns the fact type identifier for txHoldCountFact.
func (f txHoldCountFact) TypeID() string { return "stp.tx_hold_count" }

// Canonical returns the decimal string of the tx hold count.
func (f txHoldCountFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// portPriorityFact wraps a port priority as a trace.Fact.
type portPriorityFact uint8

// TypeID returns the fact type identifier for portPriorityFact.
func (f portPriorityFact) TypeID() string { return "stp.port_priority" }

// Canonical returns the decimal string of the port priority.
func (f portPriorityFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// pathCostFact wraps an admin path cost as a trace.Fact.
type pathCostFact uint32

// TypeID returns the fact type identifier for pathCostFact.
func (f pathCostFact) TypeID() string { return "stp.path_cost" }

// Canonical returns the decimal string of the path cost.
func (f pathCostFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// boolFact wraps a boolean value as a trace.Fact.
type boolFact bool

// TypeID returns the fact type identifier for boolFact.
func (f boolFact) TypeID() string { return "stp.bool" }

// Canonical returns "true" or "false".
func (f boolFact) Canonical() string { return strconv.FormatBool(bool(f)) }

// mstNameFact wraps an MST region name as a trace.Fact.
type mstNameFact string

// TypeID returns the fact type identifier for mstNameFact.
func (f mstNameFact) TypeID() string { return "stp.mst.name" }

// Canonical returns the region name.
func (f mstNameFact) Canonical() string { return string(f) }

// mstRevisionFact wraps an MST region revision as a trace.Fact.
type mstRevisionFact uint16

// TypeID returns the fact type identifier for mstRevisionFact.
func (f mstRevisionFact) TypeID() string { return "stp.mst.revision" }

// Canonical returns the decimal string of the revision.
func (f mstRevisionFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// maxHopsFact wraps an MST region maximum hop count as a trace.Fact.
type maxHopsFact uint8

// TypeID returns the fact type identifier for maxHopsFact.
func (f maxHopsFact) TypeID() string { return "stp.mst.max_hops" }

// Canonical returns the decimal string of the maximum hop count.
func (f maxHopsFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// vlanListFact wraps a set of VLAN identifiers as a trace.Fact.
type vlanListFact []vlan.ID

// TypeID returns the fact type identifier for vlanListFact.
func (f vlanListFact) TypeID() string { return "stp.mst.vlans" }

// Canonical returns the VLAN identifiers sorted and joined with commas.
func (f vlanListFact) Canonical() string {
	sorted := slices.Clone(f)
	slices.Sort(sorted)

	ids := make([]string, len(sorted))
	for i, vid := range sorted {
		ids[i] = strconv.FormatUint(uint64(vid), 10)
	}

	return strings.Join(ids, ",")
}

// Diff computes the difference between two spanning tree configurations,
// reporting changes to bridge priority, hello time, max age, forward delay,
// tx hold count, and per-port priority, admin path cost, admin edge,
// point-to-point mode, auto edge, and the four guards.
// Diff is kept as one function to perform a single, unified comparison across
// all spanning tree configuration fields in field order.
func Diff(a, b Config) []trace.Change {
	a = a.Normalize(layer.Env{})
	b = b.Normalize(layer.Env{})

	var changes []trace.Change

	lyr := LayerName

	if a.Address != b.Address {
		changes = append(changes, trace.Change{
			Layer:   lyr,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "address",
			From:    macFact(a.Address),
			To:      macFact(b.Address),
		})
	}

	if a.Priority != b.Priority {
		changes = append(changes, trace.Change{
			Layer:   lyr,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "priority",
			From:    priorityFact(a.Priority),
			To:      priorityFact(b.Priority),
		})
	}

	if a.HelloTime != b.HelloTime {
		changes = append(changes, trace.Change{
			Layer:   lyr,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "hello_time",
			From:    durationFact(a.HelloTime),
			To:      durationFact(b.HelloTime),
		})
	}

	if a.MaxAge != b.MaxAge {
		changes = append(changes, trace.Change{
			Layer:   lyr,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "max_age",
			From:    durationFact(a.MaxAge),
			To:      durationFact(b.MaxAge),
		})
	}

	if a.ForwardDelay != b.ForwardDelay {
		changes = append(changes, trace.Change{
			Layer:   lyr,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "forward_delay",
			From:    durationFact(a.ForwardDelay),
			To:      durationFact(b.ForwardDelay),
		})
	}

	if a.TxHoldCount != b.TxHoldCount {
		changes = append(changes, trace.Change{
			Layer:   lyr,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "tx_hold_count",
			From:    txHoldCountFact(a.TxHoldCount),
			To:      txHoldCountFact(b.TxHoldCount),
		})
	}

	for _, name := range sortedKeys(a.Ports) {
		ap := a.Ports[name]
		bp, exists := b.Ports[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "",
				From:    ap,
				To:      nil,
			})

			continue
		}

		if ap.Priority != bp.Priority {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "priority",
				From:    portPriorityFact(ap.Priority),
				To:      portPriorityFact(bp.Priority),
			})
		}

		if ap.PathCost != bp.PathCost {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "admin_path_cost",
				From:    pathCostFact(ap.PathCost),
				To:      pathCostFact(bp.PathCost),
			})
		}

		if ap.AdminEdge != bp.AdminEdge {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "admin_edge",
				From:    boolFact(ap.AdminEdge),
				To:      boolFact(bp.AdminEdge),
			})
		}

		if ap.PointToPoint != bp.PointToPoint {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "admin_point_to_point",
				From:    ap.PointToPoint,
				To:      bp.PointToPoint,
			})
		}

		if ap.AutoEdge != bp.AutoEdge {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "auto_edge",
				From:    boolFact(ap.AutoEdge),
				To:      boolFact(bp.AutoEdge),
			})
		}

		for _, guard := range []struct {
			field string
			from  bool
			to    bool
		}{
			{"bpdu_guard", ap.BPDUGuard, bp.BPDUGuard},
			{"restricted_role", ap.RestrictedRole, bp.RestrictedRole},
			{"restricted_tcn", ap.RestrictedTCN, bp.RestrictedTCN},
			{"loop_guard", ap.LoopGuard, bp.LoopGuard},
		} {
			if guard.from == guard.to {
				continue
			}
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   guard.field,
				From:    boolFact(guard.from),
				To:      boolFact(guard.to),
			})
		}
	}

	for _, name := range sortedKeys(b.Ports) {
		if _, exists := a.Ports[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "",
				From:    nil,
				To:      b.Ports[name],
			})
		}
	}

	if a.MST != nil || b.MST != nil {
		if (a.MST == nil) != (b.MST == nil) {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "bridge", Key: ""},
				Field:   "mst",
				From:    boolFact(a.MST != nil),
				To:      boolFact(b.MST != nil),
			})
		}
		if a.MST != nil && b.MST != nil {
			changes = append(changes, diffMST(*a.MST, *b.MST, lyr)...)
		}
	}

	if a.PVST != nil || b.PVST != nil {
		if (a.PVST == nil) != (b.PVST == nil) {
			changes = append(changes, trace.Change{
				Layer: lyr, Subject: trace.Subject{Kind: "bridge", Key: ""}, Field: "pvst",
				From: boolFact(a.PVST != nil), To: boolFact(b.PVST != nil),
			})
		}
		if a.PVST != nil && b.PVST != nil {
			changes = append(changes, diffPVST(*a.PVST, *b.PVST, lyr)...)
		}
	}

	return changes
}

// diffMST computes the differences between two normalized MST region
// configurations: name, revision, and maximum hop count at the region level,
// then each instance's priority, VLAN membership, and per-port settings.
func diffMST(a, b MST, lyr trace.Layer) []trace.Change {
	var changes []trace.Change

	bridge := trace.Subject{Kind: "bridge", Key: ""}

	if a.Name != b.Name {
		changes = append(changes, trace.Change{
			Layer: lyr, Subject: bridge, Field: "mst.name",
			From: mstNameFact(a.Name), To: mstNameFact(b.Name),
		})
	}
	if a.Revision != b.Revision {
		changes = append(changes, trace.Change{
			Layer: lyr, Subject: bridge, Field: "mst.revision",
			From: mstRevisionFact(a.Revision), To: mstRevisionFact(b.Revision),
		})
	}
	if a.MaxHops != b.MaxHops {
		changes = append(changes, trace.Change{
			Layer: lyr, Subject: bridge, Field: "mst.max_hops",
			From: maxHopsFact(a.MaxHops), To: maxHopsFact(b.MaxHops),
		})
	}

	for _, id := range sortedMSTIDs(a.Instances) {
		ai := a.Instances[id]
		key := strconv.FormatUint(uint64(id), 10)
		bi, exists := b.Instances[id]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "mst_instance", Key: key},
				Field:   "",
				From:    ai,
				To:      nil,
			})

			continue
		}
		changes = append(changes, diffMSTInstance(ai, bi, key, lyr)...)
	}

	for _, id := range sortedMSTIDs(b.Instances) {
		if _, exists := a.Instances[id]; !exists {
			key := strconv.FormatUint(uint64(id), 10)
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "mst_instance", Key: key},
				Field:   "",
				From:    nil,
				To:      b.Instances[id],
			})
		}
	}

	return changes
}

// diffMSTInstance computes the differences between two normalized MST
// instances identified by key (the MSTID as a decimal string).
func diffMSTInstance(a, b Instance, key string, lyr trace.Layer) []trace.Change {
	var changes []trace.Change

	subject := trace.Subject{Kind: "mst_instance", Key: key}

	if a.Priority != b.Priority {
		changes = append(changes, trace.Change{
			Layer: lyr, Subject: subject, Field: "priority",
			From: priorityFact(a.Priority), To: priorityFact(b.Priority),
		})
	}
	if !slices.Equal(a.VLANs, b.VLANs) {
		changes = append(changes, trace.Change{
			Layer: lyr, Subject: subject, Field: "vlans",
			From: vlanListFact(a.VLANs), To: vlanListFact(b.VLANs),
		})
	}

	for _, name := range sortedKeys(a.Ports) {
		ap := a.Ports[name]
		portKey := trace.CompositeKey(key, name)
		portSubject := trace.Subject{Kind: "mst_instance_port", Key: portKey}
		bp, exists := b.Ports[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer: lyr, Subject: portSubject, Field: "", From: ap, To: nil,
			})

			continue
		}
		if ap.Priority != bp.Priority {
			changes = append(changes, trace.Change{
				Layer: lyr, Subject: portSubject, Field: "priority",
				From: portPriorityFact(ap.Priority), To: portPriorityFact(bp.Priority),
			})
		}
		if ap.PathCost != bp.PathCost {
			changes = append(changes, trace.Change{
				Layer: lyr, Subject: portSubject, Field: "path_cost",
				From: pathCostFact(ap.PathCost), To: pathCostFact(bp.PathCost),
			})
		}
	}

	for _, name := range sortedKeys(b.Ports) {
		if _, exists := a.Ports[name]; !exists {
			portKey := trace.CompositeKey(key, name)
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "mst_instance_port", Key: portKey},
				Field:   "",
				From:    nil,
				To:      b.Ports[name],
			})
		}
	}

	return changes
}

// diffPVST computes the differences between two normalized PVST
// configurations: each VLAN's tree added, removed, or changed in priority
// and per-port settings, walked in VLAN ID order.
func diffPVST(a, b PVST, lyr trace.Layer) []trace.Change {
	var changes []trace.Change

	for _, vid := range sortedVLANIDs(a.Trees) {
		at := a.Trees[vid]
		key := strconv.FormatUint(uint64(vid), 10)
		bt, exists := b.Trees[vid]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "pvst_tree", Key: key},
				Field:   "",
				From:    at,
				To:      nil,
			})

			continue
		}
		changes = append(changes, diffPVSTTree(at, bt, key, lyr)...)
	}

	for _, vid := range sortedVLANIDs(b.Trees) {
		if _, exists := a.Trees[vid]; !exists {
			key := strconv.FormatUint(uint64(vid), 10)
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "pvst_tree", Key: key},
				Field:   "",
				From:    nil,
				To:      b.Trees[vid],
			})
		}
	}

	return changes
}

// diffPVSTTree computes the differences between two normalized PVST trees
// identified by key (the VLAN ID as a decimal string).
func diffPVSTTree(a, b Tree, key string, lyr trace.Layer) []trace.Change {
	var changes []trace.Change

	subject := trace.Subject{Kind: "pvst_tree", Key: key}

	if a.Priority != b.Priority {
		changes = append(changes, trace.Change{
			Layer: lyr, Subject: subject, Field: "priority",
			From: priorityFact(a.Priority), To: priorityFact(b.Priority),
		})
	}

	for _, name := range sortedKeys(a.Ports) {
		ap := a.Ports[name]
		portKey := trace.CompositeKey(key, name)
		portSubject := trace.Subject{Kind: "pvst_tree_port", Key: portKey}
		bp, exists := b.Ports[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer: lyr, Subject: portSubject, Field: "", From: ap, To: nil,
			})

			continue
		}
		if ap.Priority != bp.Priority {
			changes = append(changes, trace.Change{
				Layer: lyr, Subject: portSubject, Field: "priority",
				From: portPriorityFact(ap.Priority), To: portPriorityFact(bp.Priority),
			})
		}
		if ap.PathCost != bp.PathCost {
			changes = append(changes, trace.Change{
				Layer: lyr, Subject: portSubject, Field: "path_cost",
				From: pathCostFact(ap.PathCost), To: pathCostFact(bp.PathCost),
			})
		}
	}

	for _, name := range sortedKeys(b.Ports) {
		if _, exists := a.Ports[name]; !exists {
			portKey := trace.CompositeKey(key, name)
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "pvst_tree_port", Key: portKey},
				Field:   "",
				From:    nil,
				To:      b.Ports[name],
			})
		}
	}

	return changes
}
