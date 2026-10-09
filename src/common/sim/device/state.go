package device

import (
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// State samples the forwarding and diagnostic state of one device.
// It is safe for concurrent reads.
type State struct {
	Entries       []Entry
	Groups        map[vlan.ID][]GroupEntry
	RouterPorts   map[vlan.ID][]RouterPort
	Ports         []port.Port
	Power         Allocation
	Counters      map[string]PortCounters
	Roles         map[string]PortInfo
	TreeRoles     map[TreeRef]map[string]PortInfo
	RelayCounters RelayCounters
	Neighbors     []NeighborEntry
}

// Entry records an active FDB binding and its installation time.
// It is safe for concurrent reads.
type Entry struct {
	FID       vlan.ID
	MAC       netaddr.MAC
	Port      string
	Origin    Origin
	Lifetime  Lifetime
	LearnedAt time.Time
}

// RelayCounters counts FDB learning, expiry, eviction, and moves.
// It is safe for concurrent reads.
type RelayCounters struct {
	Learned uint64
	Expired uint64
	Evicted uint64
	Moved   uint64
}

// SourceEntry records a multicast source timer. A zero Expires marks an expired timer.
// It is safe for concurrent reads.
type SourceEntry struct {
	Address netip.Addr
	Expires time.Time
}

// GroupEntry records membership for one multicast group on one port.
// GroupExpires applies in Exclude mode, and Sources is in address order.
// It is safe for concurrent reads.
type GroupEntry struct {
	Group        netip.Addr
	Port         string
	Mode         FilterMode
	GroupExpires time.Time
	Sources      []SourceEntry
}

// RouterPort records a multicast router port. Static records have zero Expires.
// It is safe for concurrent reads.
type RouterPort struct {
	Port     string
	Expires  time.Time
	Origin   Origin
	Lifetime Lifetime
}

// TreeRef identifies a spanning tree by kind and instance ID.
// It is safe for concurrent reads.
type TreeRef struct {
	Kind TreeKind
	ID   uint16
}

// PortInfo records one port's role, forwarding state, guards, and counters in a tree.
// It is safe for concurrent reads.
type PortInfo struct {
	Tree               TreeRef
	Role               bpdu.Role
	State              PortState
	BlockReason        BlockReason
	Priority           uint8
	PathCost           uint32
	DesignatedRoot     bpdu.BridgeID
	Designated         bpdu.BridgeID
	DesignatedPort     uint16
	DesignatedCost     uint32
	PointToPoint       bool
	Edge               bool
	ForwardTransitions uint64
	TxBPDUs            uint64
	RxBPDUs            uint64
	BadBPDUs           uint64
	SendRSTP           bool
}

// NeighborEntry records a binding within a VRF and the depth of its held-frame queue.
// It is safe for concurrent reads.
type NeighborEntry struct {
	VRF       string
	Interface string
	Addr      netip.Addr
	MAC       netaddr.MAC
	State     NeighborState
	HoldDepth int
}

// Allocation records PoE port grants and group budgets with their analysis metadata.
// It is safe for concurrent reads.
type Allocation struct {
	Metadata analysis.Metadata
	Ports    map[string]PortAllocation
	Groups   map[string]GroupAllocation
}

// PortAllocation records a port's power grant interval in nanowatts and refusal reason.
// It is safe for concurrent reads.
type PortAllocation struct {
	State        PowerState
	MinNanowatts uint64
	MaxNanowatts uint64
	Denial       trace.Reason
}

// GroupAllocation records a PoE group's budget, allocation, and remainder intervals in nanowatts.
// It is safe for concurrent reads.
type GroupAllocation struct {
	BudgetNanowatts       uint64
	AllocatedNanowatts    uint64
	RemainderMinNanowatts uint64
	RemainderMaxNanowatts uint64
}

// PortCounters records octets, classified frames, errors, and discards for one port.
// It is safe for concurrent reads.
type PortCounters struct {
	InBytes      uint64
	OutBytes     uint64
	InUnicast    uint64
	OutUnicast   uint64
	InMulticast  uint64
	OutMulticast uint64
	InBroadcast  uint64
	OutBroadcast uint64
	InErrors     uint64
	OutErrors    uint64
	InDiscards   uint64
	OutDiscards  uint64
	Discards     map[trace.Reason]uint64
}

// Origin identifies configuration or observation as a record's installer.
// It is safe for concurrent use.
type Origin string

const (
	// Configured identifies a record installed by configuration or a preloaded seed.
	Configured Origin = "configured"
	// Observed identifies a record installed by traffic observation.
	Observed Origin = "observed"
)

// Lifetime identifies records that age out or persist until explicitly removed.
// It is safe for concurrent use.
type Lifetime string

const (
	// Aging identifies a record eligible for timer expiry.
	Aging Lifetime = "aging"
	// Static identifies a record timer advancement does not remove.
	Static Lifetime = "static"
)

// FilterMode controls which sources a port accepts for a multicast group.
// It is safe for concurrent use.
type FilterMode uint8

const (
	// Include admits only sources named by the membership records.
	Include FilterMode = 0
	// Exclude admits every source except a recorded source whose timer is zero.
	Exclude FilterMode = 1
)

// BlockReason identifies the STP guard blocking a port. Empty means no guard.
// It is safe for concurrent use.
type BlockReason string

const (
	// BlockReasonBPDUGuard identifies a port disabled after receiving a BPDU.
	BlockReasonBPDUGuard BlockReason = "bpdu-guard"
	// BlockReasonPVIDInconsistent identifies a disagreement about the arrival VLAN.
	BlockReasonPVIDInconsistent BlockReason = "pvid-inconsistent"
	// BlockReasonLoopInconsistent identifies expired received information held closed by loop guard.
	BlockReasonLoopInconsistent BlockReason = "loop-inconsistent"
)

// TreeKind distinguishes the common tree, an MST instance, and a per-VLAN tree.
// It is safe for concurrent use.
type TreeKind string

const (
	// TreeCIST identifies the Common and Internal Spanning Tree.
	TreeCIST TreeKind = "CIST"
	// TreeMSTI identifies a Multiple Spanning Tree Instance.
	TreeMSTI TreeKind = "MSTI"
	// TreeVLAN identifies a per-VLAN spanning tree.
	TreeVLAN TreeKind = "VLAN"
)

// PortState controls whether an STP port learns addresses and forwards frames.
// It is safe for concurrent use.
type PortState string

const (
	// StateDiscarding disables forwarding and address learning.
	StateDiscarding PortState = "Discarding"
	// StateLearning permits address learning while withholding forwarding.
	StateLearning PortState = "Learning"
	// StateForwarding permits address learning and forwarding.
	StateForwarding PortState = "Forwarding"
)

// NeighborState records progress in resolving and confirming a neighbor binding.
// It is safe for concurrent use.
type NeighborState string

const (
	// NeighborUnobserved means no binding has been observed or configured.
	NeighborUnobserved NeighborState = ""
	// NeighborIncomplete holds frames while an unknown link-layer address is resolved.
	NeighborIncomplete NeighborState = "incomplete"
	// NeighborReachable identifies a binding confirmed within its reachability interval.
	NeighborReachable NeighborState = "reachable"
	// NeighborStale identifies a cached address awaiting reachability confirmation.
	NeighborStale NeighborState = "stale"
	// NeighborDelay forwards on the cached address before probing begins.
	NeighborDelay NeighborState = "delay"
	// NeighborProbe forwards while solicitations seek confirmation.
	NeighborProbe NeighborState = "probe"
)

// PowerState records whether a port received power. Empty means unobserved.
// It is safe for concurrent use.
type PowerState string

const (
	// PowerUnknown identifies an unobserved power grant.
	PowerUnknown PowerState = ""
	// PowerNoDevice identifies a port with no attached powered device.
	PowerNoDevice PowerState = "NoDevice"
	// PowerDenied identifies a refused power request.
	PowerDenied PowerState = "Denied"
	// PowerDelivered identifies a granted power request.
	PowerDelivered PowerState = "Delivered"
)

// Canonical returns Unknown for an unobserved power state.
func (s PowerState) Canonical() string {
	if s == "" {
		return "Unknown"
	}
	return string(s)
}

// String returns the canonical power state token.
func (s PowerState) String() string { return s.Canonical() }

// String returns the multicast membership mode.
func (m FilterMode) String() string {
	if m == Exclude {
		return "EXCLUDE"
	}
	return "INCLUDE"
}

type stateClass bool

const (
	behavioral stateClass = true
	diagnostic stateClass = false
)

var stateFields = []struct {
	name  string
	class stateClass
}{
	{"Entries", behavioral},
	{"Groups", behavioral},
	{"RouterPorts", behavioral},
	{"Ports", behavioral},
	{"Power", behavioral},
	{"Roles", behavioral},
	{"TreeRoles", behavioral},
	{"Neighbors", behavioral},
	{"Counters", diagnostic},
	{"RelayCounters", diagnostic},
}

// Fingerprint returns a canonical behavioral projection, excluding counters and
// timer timestamps. It is safe for concurrent reads.
func (s State) Fingerprint() string { return fingerprintStateValues(s.projection()) }

func fingerprintStateValues(values []stateValue) string {
	var b strings.Builder
	for _, value := range values {
		b.WriteString(strconv.Quote(value.field))
		b.WriteByte('=')
		b.WriteString(strconv.Quote(value.text))
		b.WriteByte('|')
	}
	return b.String()
}

// Diff returns the first behavioral difference, its field path and row values.
// Empty fields mean equality. It is safe for concurrent reads.
func (s State) Diff(other State) (field, current, expected string) {
	av, bv := reflect.ValueOf(s), reflect.ValueOf(other)
	for _, f := range stateFields {
		if f.class == diagnostic {
			continue
		}
		a := appendStateValues(nil, f.name, av.FieldByName(f.name))
		b := appendStateValues(nil, f.name, bv.FieldByName(f.name))
		if field := diffStateValues(a, b); field != "" {
			return field, fingerprintStateValues(a), fingerprintStateValues(b)
		}
	}

	return "", "", ""
}

func diffStateValues(a, b []stateValue) string {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i].field != b[i].field {
			for _, value := range a {
				if value.field == b[i].field {
					return a[i].field
				}
			}
			return b[i].field
		}
		if a[i].text != b[i].text {
			return b[i].field
		}
	}
	if len(a) > len(b) {
		return a[len(b)].field
	}
	if len(b) > len(a) {
		return b[len(a)].field
	}
	return ""
}

type stateValue struct{ field, text string }

var portInfoFields = map[string]stateClass{
	"Tree": behavioral, "Role": behavioral, "State": behavioral, "BlockReason": behavioral,
	"Priority": behavioral, "PathCost": behavioral, "DesignatedRoot": behavioral,
	"Designated": behavioral, "DesignatedPort": behavioral, "DesignatedCost": behavioral,
	"PointToPoint": behavioral, "Edge": behavioral, "SendRSTP": behavioral,
	"ForwardTransitions": diagnostic, "TxBPDUs": diagnostic, "RxBPDUs": diagnostic, "BadBPDUs": diagnostic,
}

func (s State) projection() []stateValue {
	var values []stateValue
	v := reflect.ValueOf(s)
	for _, f := range stateFields {
		if f.class == behavioral {
			values = appendStateValues(values, f.name, v.FieldByName(f.name))
		}
	}
	return values
}

func appendStateValues(out []stateValue, path string, v reflect.Value) []stateValue {
	switch value := v.Interface().(type) {
	case netip.Addr:
		return append(out, stateValue{path, value.String()})
	case netaddr.MAC:
		return append(out, stateValue{path, value.String()})
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if v.Type() == reflect.TypeFor[PortInfo]() && portInfoFields[field.Name] == diagnostic {
				continue
			}
			if field.Type == reflect.TypeFor[time.Time]() || field.Name == "Metadata" {
				continue
			}
			out = appendStateValues(out, path+"."+field.Name, v.Field(i))
		}
	case reflect.Map:
		out = append(out, stateValue{path, "map"})
		keys := v.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int {
			return strings.Compare(fmt.Sprint(a.Interface()), fmt.Sprint(b.Interface()))
		})
		for _, key := range keys {
			name := fmt.Sprint(key.Interface())
			if key.Kind() == reflect.String {
				name = strconv.Quote(key.String())
			}
			out = append(out, stateValue{path + ".[" + name + "]", "present"})
			out = appendStateValues(out, path+".["+name+"]", v.MapIndex(key))
		}
	case reflect.Slice:
		out = append(out, stateValue{path + ".Length", strconv.Itoa(v.Len())})
		rows := make([]reflect.Value, v.Len())
		for i := range rows {
			rows[i] = v.Index(i)
		}
		slices.SortFunc(rows, func(a, b reflect.Value) int {
			return strings.Compare(stateRowKey(a.Interface()), stateRowKey(b.Interface()))
		})
		for _, row := range rows {
			out = append(out, stateValue{path + ".[" + stateRowKey(row.Interface()) + "]", "present"})
			out = appendStateValues(out, path+".["+stateRowKey(row.Interface())+"]", row)
		}
	default:
		out = append(out, stateValue{path, fmt.Sprint(v.Interface())})
	}
	return out
}

func stateRowKey(value any) string {
	switch row := value.(type) {
	case Entry:
		return trace.CompositeKey(row.MAC.String(), fmt.Sprint(row.FID), row.Port)
	case GroupEntry:
		return trace.CompositeKey(row.Group.String(), row.Port)
	case RouterPort:
		return strconv.Quote(row.Port)
	case SourceEntry:
		return strconv.Quote(row.Address.String())
	case port.Port:
		return strconv.Quote(row.Name)
	case NeighborEntry:
		return trace.CompositeKey(row.VRF, row.Interface, row.Addr.String())
	default:
		return fmt.Sprint(value)
	}
}
