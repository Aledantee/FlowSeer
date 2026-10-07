// Package bridge implements an Ethernet transparent bridge with optional IEEE 802.1Q VLAN awareness.
package bridge

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Gate decides whether a port learns addresses and forwards frames carrying a
// given VLAN. The VLAN is part of the question because a port's forwarding state
// belongs to a spanning tree, and more than one tree can run over one port.
type Gate interface {
	Learns(port string, vid vlan.ID) bool
	Forwards(port string, vid vlan.ID) bool
}

type semanticGate interface {
	ForwardingFact(port string, vid vlan.ID, learns, forwards bool) trace.Fact
}

// gateEntry pairs an installed [Gate] with the analysis scope it was
// installed under. A bridge consults every entry, keyed by scope, so more
// than one capability layer (spanning tree alongside another gating layer)
// can gate a port at once.
type gateEntry struct {
	gate  Gate
	scope analysis.Scope
}

// Selection is a bridge-level snapshot of a link aggregation member choice. It
// carries the fields a [Selector] decision reports without the bridge package
// depending on the layer that produces them.
type Selection struct {
	Member string
	OK     bool

	// Bucket and Prior describe a balanced-mode decision; both are zero for
	// an active-backup decision.
	Bucket uint8
	Prior  string

	// Cause names why the selector chose Member, in the selecting layer's own
	// vocabulary (for example "kept", "first-use", "primary").
	Cause string

	// RebalanceUnmodeled reports that this is a balanced-mode selection whose
	// bucket assignment is old enough that measured-load rebalancing could
	// have moved it, which the selecting layer does not model.
	RebalanceUnmodeled bool
}

// Selector chooses a member port of a link aggregation group to carry an
// egress frame. commit reports whether the choice mutates the selector's
// runtime state (a bucket assignment, an enabled-list rotation, or
// active-backup's last-active memory); a non-committing call computes the
// same choice without changing it.
type Selector interface {
	Select(now time.Time, lag string, commit bool, f ethernet.Frame, vid vlan.ID) Selection
}

type semanticSelector interface {
	SelectionFact(lag string, f ethernet.Frame, vid vlan.ID, sel Selection) trace.Fact
}

// GroupResolver selects the logical egress ports for a group frame.
// A false decision leaves the frame on the bridge's ordinary flood path.
type GroupResolver interface {
	Resolve(now time.Time, vid vlan.ID, f ethernet.Frame) (ports []string, decided bool)
}

type semanticGroupResolver interface {
	MembershipFact(now time.Time, vid vlan.ID, f ethernet.Frame, ports []string, decided bool) trace.Fact
}

// LayerName identifies the bridge relay forwarding layer.
const LayerName trace.Layer = "relay"

// LayerNameVLAN identifies the 802.1Q VLAN awareness and filtering layer.
const LayerNameVLAN trace.Layer = "vlan"

// Rule constants produced by bridge.
const (
	RuleIngressPortDown       trace.RuleID = "ingress-port-down"
	RuleReservedBridgeAddress trace.RuleID = "reserved-bridge-address"
	RuleDefaultVLAN           trace.RuleID = "default-vlan"
	RuleCustomerVLANFilter    trace.RuleID = "customer-vlan-filter"
	RuleCustomerVLAN          trace.RuleID = "customer-vlan"
	RuleVLANTunnelClassify    trace.RuleID = "vlan-tunnel-classify"
	RuleVLANUndefined         trace.RuleID = "vlan-undefined"
	RuleAdmissionFilter       trace.RuleID = "admission-filter"
	RuleAdmission             trace.RuleID = "admission"
	RuleNoPVID                trace.RuleID = "no-pvid"
	RuleVLANClassify          trace.RuleID = "vlan-classify"
	RuleIngressFilter         trace.RuleID = "ingress-filter"
	RulePortBlocked           trace.RuleID = "port-blocked"
	RuleLearn                 trace.RuleID = "learn"
	RuleEvict                 trace.RuleID = "evict"
	RuleMove                  trace.RuleID = "move"
	RuleFloodVLAN             trace.RuleID = "flood-vlan"
	RuleUnicastHit            trace.RuleID = "unicast-hit"
	RuleUnicastMiss           trace.RuleID = "unicast-miss"
	RuleGroupDestination      trace.RuleID = "group-destination"
	RuleSamePort              trace.RuleID = "same-port"
	RulePortDown              trace.RuleID = "port-down"
	RuleNotMember             trace.RuleID = "not-member"
	RuleProtected             trace.RuleID = "protected"
	RuleMTUExceeded           trace.RuleID = "mtu-exceeded"
	RuleVLANTagForm           trace.RuleID = "vlan-tag-form"
	RuleTransmit              trace.RuleID = "transmit"
	RuleFlood                 trace.RuleID = "flood"
	RuleNoMember              trace.RuleID = "no-member"
)

// Layer simulates an Ethernet transparent bridge with optional IEEE 802.1Q VLAN awareness.
//
// A Layer is not safe for concurrent use.
type Layer struct {
	resolverLayer trace.Layer
	resolverRule  trace.RuleID
	cfg           Config
	ports         port.Table
	agingTime     time.Duration
	fdb           map[fdbKey]Entry
	gates         []gateEntry
	selector      Selector
	selectorScope analysis.Scope
	resolver      GroupResolver
	resolverScope analysis.Scope
	fdbScope      analysis.Scope
	counters      Counters
	// dynamic counts the entries that are not static, so the bound is checked
	// without a scan of the table.
	dynamic int
}

// New constructs a [Layer] with the provided configuration and environment.
// It returns an error if the configuration is invalid against the ports.
func New(cfg Config, env layer.Env) (*Layer, error) {
	if err := cfg.Validate(env); err != nil {
		return nil, err
	}
	return newLayer(cfg.Normalize(env), env.Ports), nil
}

func newLayer(cfg Config, ports port.Table) *Layer {
	aging := effectiveAgingTime(cfg.AgingTime)

	return &Layer{
		cfg:       cfg.Clone(),
		ports:     ports.Clone(),
		agingTime: aging,
		fdb:       make(map[fdbKey]Entry),
	}
}

// Clone returns an independent copy of the bridge for executable forking.
// Immutable configuration, scopes, and port definitions are shared; active
// forwarding records, gates, counters, and dynamic entry counts are deep-copied.
// Dynamic selector and resolver bindings are reset so the enclosing switch can
// rebind them to its own layers.
func (b *Layer) Clone() *Layer {
	cp := &Layer{
		cfg:           b.cfg,
		ports:         b.ports,
		agingTime:     b.agingTime,
		fdbScope:      b.fdbScope,
		selectorScope: b.selectorScope,
		resolverScope: b.resolverScope,
		counters:      b.counters,
		dynamic:       b.dynamic,
	}
	if b.fdb != nil {
		cp.fdb = make(map[fdbKey]Entry, len(b.fdb))
		for k, v := range b.fdb {
			cp.fdb[k] = v
		}
	}
	if b.gates != nil {
		cp.gates = slices.Clone(b.gates)
	}
	return cp
}

// SetGate installs g as the bridge forwarding and learning gate for scope,
// the protocol scope containing the per-port fields the gate consults. A nil
// gate allows every port to learn and forward, but still contributes scope to
// the consulted set.
//
// SetGate replaces the entry whose scope compares equal to scope, so a caller
// that reinstalls the gate for a scope it already holds (as [Derive] does
// when it retains a converged layer over the one [NewWithSpec] just built)
// does not end up consulting both the stale and the retained layer. A scope
// SetGate has not seen before is appended as an additional gate, so more than
// one capability layer can gate a port at once, keyed by scope.
func (b *Layer) SetGate(g Gate, scope analysis.Scope) {
	for i := range b.gates {
		if b.gates[i].scope.Compare(scope) == 0 {
			b.gates[i].gate = g
			b.gates[i].scope = scope
			return
		}
	}
	b.gates = append(b.gates, gateEntry{gate: g, scope: scope})
}

// GateCount reports the number of installed gate entries. It exists for
// tests that must confirm [Layer.SetGate]'s replacing semantics rather than
// an appending one; production code has no use for the count.
func (b *Layer) GateCount() int {
	return len(b.gates)
}

// SetSelector installs sel as the member port selector for LAG egress. scope
// contains the selector fields consulted by bridge forwarding.
func (b *Layer) SetSelector(sel Selector, scope analysis.Scope) {
	b.selector = sel
	b.selectorScope = scope
}

// SetGroupResolver installs resolver as the bridge's group destination lookup.
// scope contains the membership fields consulted by bridge forwarding. traceLayer
// and rule identify the layer and rule producing the resolution in trace records.
// A nil resolver leaves every group frame on the ordinary flood path.
func (b *Layer) SetGroupResolver(resolver GroupResolver, scope analysis.Scope, traceLayer trace.Layer, rule trace.RuleID) {
	b.resolverLayer = traceLayer
	b.resolverRule = rule
	b.resolver = resolver
	b.resolverScope = scope
}

// SetFDBScope installs the protocol scope beneath which exact forwarding
// database lookup dependencies are recorded.
func (b *Layer) SetFDBScope(scope analysis.Scope) {
	b.fdbScope = scope
}

// SetOperStatus updates the operational link state of the named port in the
// bridge's port table. It returns a structured validation error when state is
// outside the [port.LinkState] domain and leaves the table unchanged.
func (b *Layer) SetOperStatus(portName string, state port.LinkState) error {
	if err := validateOperStatus(portName, state); err != nil {
		return err
	}

	builder := port.NewBuilder()
	for _, p := range b.ports.Ports() {
		if p.Name == portName {
			p.OperStatus = state
		}
		builder.Add(p)
	}
	tbl, err := builder.Build()
	if err != nil {
		return err
	}
	b.ports = tbl

	return nil
}

func validateOperStatus(portName string, state port.LinkState) error {
	switch state {
	case "", port.Unknown, port.Up, port.Down:
		return nil
	default:
		return errs.New().
			Attr("field", "ports."+portName+".oper_status").
			Attr("name", portName).
			Attr("oper_status", state).
			Msgf("port %q has invalid oper status %q", portName, state)
	}
}

// gateFact takes the deciding gate as a parameter rather than reading a
// field, since the bridge consults more than one gate and must attribute a
// fact to whichever one decided.
func (b *Layer) gateFact(gate Gate, name string, vid vlan.ID, learns, forwards bool) trace.Fact {
	sg, ok := gate.(semanticGate)
	if !ok {
		return nil
	}

	return sg.ForwardingFact(name, vid, learns, forwards)
}

// RetentionKey returns a canonical encoding of every normalized input the layer's
// runtime state depends on: its own configuration as Diff sees it and port link
// states.
func RetentionKey(cfg Config, env layer.Env) string {
	norm := cfg.Normalize(env)
	var b strings.Builder
	b.WriteString("config=")
	fmt.Fprintf(&b, "aging_time=%s;max_entries=%d;forward_bpdu=%t;",
		norm.AgingTime, norm.MaxEntries, norm.ForwardBPDU)

	b.WriteString("flood_vlans=[")
	b.WriteString(VLANsFact(norm.FloodVLANs).Canonical())
	b.WriteString("];protected_ports=[")
	b.WriteString(StringsFact(norm.ProtectedPorts).Canonical())
	b.WriteString("];")

	if norm.VLAN == nil {
		b.WriteString("vlan=none;")
	} else {
		b.WriteString("vlan={table=[")
		vlanIDs := make([]vlan.ID, 0, len(norm.VLAN.Table))
		for vid := range norm.VLAN.Table {
			vlanIDs = append(vlanIDs, vid)
		}
		slices.Sort(vlanIDs)
		for i, vid := range vlanIDs {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%d:%s", vid, strconv.Quote(norm.VLAN.Table[vid]))
		}
		b.WriteString("];switchports=[")
		swPorts := make([]string, 0, len(norm.VLAN.Switchports))
		for p := range norm.VLAN.Switchports {
			swPorts = append(swPorts, p)
		}
		slices.Sort(swPorts)
		for i, p := range swPorts {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%s:{%s}", p, snapshotSwitchport(norm.VLAN.Switchports[p]).Canonical())
		}
		b.WriteString("]};")
	}

	portNames := make(map[string]struct{})
	for _, p := range env.Ports.Ports() {
		portNames[p.Name] = struct{}{}
	}
	for _, p := range norm.ProtectedPorts {
		portNames[p] = struct{}{}
	}
	if norm.VLAN != nil {
		for p := range norm.VLAN.Switchports {
			portNames[p] = struct{}{}
		}
	}
	sortedPorts := make([]string, 0, len(portNames))
	for name := range portNames {
		sortedPorts = append(sortedPorts, name)
	}
	slices.Sort(sortedPorts)

	b.WriteString("\nport-state=")
	for _, name := range sortedPorts {
		if pt, ok := env.Ports.Port(name); ok {
			fmt.Fprintf(&b, "%s:admin=%s,oper=%s;", name, pt.AdminStatus, pt.OperStatus)
		} else {
			fmt.Fprintf(&b, "%s:absent;", name)
		}
	}

	return b.String()
}
