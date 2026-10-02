// Package bridge implements an Ethernet transparent bridge with optional IEEE 802.1Q VLAN awareness.
package bridge

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
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

// Counters returns a snapshot of the forwarding database lifecycle counters.
func (b *Layer) Counters() Counters {
	return b.counters
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

// Validate verifies the invariants of the bridge configuration against the given environment.
func (b *Layer) Validate(env layer.Env) error {
	return b.cfg.Validate(env)
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
// tests that must confirm [Bridge.SetGate]'s replacing semantics rather than
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
// scope contains the membership fields consulted by bridge forwarding. A nil
// resolver leaves every group frame on the ordinary flood path.
func (b *Layer) SetGroupResolver(resolver GroupResolver, scope analysis.Scope, layer trace.Layer, rule trace.RuleID) {
	b.resolverLayer = layer
	b.resolverRule = rule
	b.resolver = resolver
	b.resolverScope = scope
}

// SetFDBScope installs the protocol scope beneath which exact forwarding
// database lookup dependencies are recorded.
func (b *Layer) SetFDBScope(scope analysis.Scope) {
	b.fdbScope = scope
}

// Flush removes dynamic forwarding database entries matching the given
// targets: a target's Port must match the entry's port, and either its FIDs
// is empty or contains the entry's FID. Two targets may name the same port;
// their FID sets are unioned rather than one replacing the other, and an
// empty set on either side widens the port to every FID, so a caller that
// builds its targets tree by tree does not silently lose the earlier tree's
// flush.
func (b *Layer) Flush(targets []layer.FlushTarget) {
	if len(targets) == 0 {
		return
	}
	byPort := make(map[string][]vlan.ID, len(targets))
	for _, target := range targets {
		fids, seen := byPort[target.Port]
		switch {
		case !seen:
			byPort[target.Port] = target.FIDs
		case len(fids) == 0 || len(target.FIDs) == 0:
			byPort[target.Port] = nil
		default:
			merged := make([]vlan.ID, 0, len(fids)+len(target.FIDs))
			merged = append(merged, fids...)
			merged = append(merged, target.FIDs...)
			byPort[target.Port] = merged
		}
	}
	for key, e := range b.fdb {
		if e.Lifetime == Static {
			continue
		}
		fids, ok := byPort[e.Port]
		if !ok {
			continue
		}
		if len(fids) == 0 || slices.Contains(fids, key.fid) {
			delete(b.fdb, key)
			b.dynamic--
		}
	}
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

// Learn validates and preloads the forwarding database with the provided seeds. A seed naming a
// LAG member is stored under the LAG, as the relay learns it, so a later lookup
// treats the aggregation as one port. Invalid or duplicate seeds leave the table unchanged.
// Aging seeds count as learned and are bounded by MaxEntries, evicting the
// oldest dynamic entry when the table exceeds the bound. Static seeds count nothing.
func (b *Layer) Learn(seeds []Seed) error {
	normalized, err := NormalizeSeeds(b.cfg, layer.Env{Ports: b.ports}, seeds)
	if err != nil {
		return err
	}

	for _, s := range normalized {
		key := fdbKey{fid: s.FID, mac: s.MAC}
		existing, exists := b.fdb[key]
		if s.Lifetime == Static {
			if exists && existing.Lifetime != Static {
				b.dynamic--
			}
			b.fdb[key] = Entry(s)
			continue
		}

		if !exists || existing.Lifetime == Static {
			if b.cfg.MaxEntries > 0 && b.dynamic >= b.cfg.MaxEntries {
				b.evictOldestDynamic()
			}
			b.fdb[key] = Entry(s)
			b.dynamic++
			b.counters.Learned++
		} else {
			if existing.Port != s.Port {
				existing.Port = s.Port
				b.counters.Moved++
			}
			existing.LearnedAt = s.LearnedAt
			b.fdb[key] = existing
		}
	}

	return nil
}

// Entries returns all active forwarding database entries sorted by filtering database ID then MAC address.
func (b *Layer) Entries() []Entry {
	entries := make([]Entry, 0, len(b.fdb))
	for _, e := range b.fdb {
		entries = append(entries, e)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].FID != entries[j].FID {
			return entries[i].FID < entries[j].FID
		}

		return bytes.Compare(entries[i].MAC[:], entries[j].MAC[:]) < 0
	})

	return entries
}

// Forget removes a forwarding database entry with the given FID and MAC address,
// regardless of whether it is static or dynamic. It reports whether an entry was present.
func (b *Layer) Forget(fid vlan.ID, mac netaddr.MAC) bool {
	key := fdbKey{fid: fid, mac: mac}
	e, exists := b.fdb[key]
	if !exists {
		return false
	}
	delete(b.fdb, key)
	if e.Lifetime != Static {
		b.dynamic--
	}

	return true
}

// Advance removes dynamic forwarding database entries older than the configured aging time relative to now.
func (b *Layer) Advance(now time.Time) {
	for key, e := range b.fdb {
		if e.Lifetime != Static && now.Sub(e.LearnedAt) > b.agingTime {
			delete(b.fdb, key)
			b.dynamic--
			b.counters.Expired++
		}
	}
}

func (b *Layer) evictOldestDynamic() (Entry, bool) {
	var (
		oldestKey fdbKey
		oldest    Entry
		found     bool
	)
	for key, e := range b.fdb {
		if e.Lifetime == Static {
			continue
		}
		if !found {
			oldestKey = key
			oldest = e
			found = true
			continue
		}
		if e.LearnedAt.Before(oldest.LearnedAt) {
			oldestKey = key
			oldest = e
		} else if e.LearnedAt.Equal(oldest.LearnedAt) {
			if e.FID < oldest.FID || (e.FID == oldest.FID && bytes.Compare(e.MAC[:], oldest.MAC[:]) < 0) {
				oldestKey = key
				oldest = e
			}
		}
	}
	if !found {
		return Entry{}, false
	}
	delete(b.fdb, oldestKey)
	b.dynamic--
	b.counters.Evicted++

	return oldest, true
}

// Forward processes an ingress frame through the bridge pipeline and updates dynamic forwarding database entries.
func (b *Layer) Forward(now time.Time, ingress string, f ethernet.Frame) Result {
	return b.forward(now, ingress, f, true)
}

// Peek processes an ingress frame through the bridge pipeline without mutating the forwarding database.
func (b *Layer) Peek(now time.Time, ingress string, f ethernet.Frame) Result {
	return b.forward(now, ingress, f, false)
}

func (b *Layer) forward(now time.Time, ingress string, f ethernet.Frame, learn bool) Result {
	in, res, ok := b.Ingress(now, ingress, f, learn, learn)
	if !ok {
		return res
	}

	return b.Egress(in, f)
}

// Ingress represents an admitted, classified, and learned frame ready for egress forwarding.
// Ingress values are safe for concurrent reads but not for concurrent mutation.
type Ingress struct {
	Port          string
	FID           vlan.ID
	PCP           vlan.PCP
	DEI           bool
	RemainingTags []vlan.Tag
	TPID          uint16
	Steps         []trace.Step

	// Now is the time this ingress descriptor was composed at, and Commit
	// reports whether the forwarding call that produced it mutates state: a
	// LAG selection made during egress commits exactly when Commit is true.
	Now             time.Time
	Commit          bool
	consultedPorts  []port.Port
	consultedScopes []analysis.Scope
}

// ConsultedPorts returns independent snapshots of the port state consulted
// before this ingress descriptor was produced.
func (in Ingress) ConsultedPorts() []port.Port {
	return append([]port.Port(nil), in.consultedPorts...)
}

// Consult records port-state snapshots consulted while composing an ingress
// descriptor outside the bridge ingress pipeline.
func (in *Ingress) Consult(ports ...port.Port) {
	result := Result{consultedPorts: in.consultedPorts}
	result.Consult(ports...)
	in.consultedPorts = result.consultedPorts
}

// ConsultedScopes returns the exact analysis scopes consulted before this
// ingress descriptor was produced, in canonical order.
func (in Ingress) ConsultedScopes() []analysis.Scope {
	return append([]analysis.Scope(nil), in.consultedScopes...)
}

// ConsultScopes records exact analysis scopes consulted while composing an
// ingress descriptor outside the bridge ingress pipeline.
func (in *Ingress) ConsultScopes(scopes ...analysis.Scope) {
	in.consultedScopes = mergeScopes(in.consultedScopes, scopes)
}

// Ingress processes an incoming frame through port validation, IEEE reserved address checks,
// forwarding gate, VLAN classification, admission control, ingress filtering, and MAC learning.
// learn governs forwarding-database learning; commit governs whether egress processing of the
// returned descriptor commits mutating state such as a LAG selection. The two differ only when a
// caller defers learning past a later check, since the commit semantics of its eventual egress
// still reflect this call's caller.
// It returns false and a populated Result on any drop; on success it returns true and an Ingress
// descriptor for subsequent egress forwarding.
func (b *Layer) Ingress(now time.Time, ingress string, f ethernet.Frame, learn, commit bool) (Ingress, Result, bool) {
	var res Result
	res.Outcome = trace.Dropped
	b.consultForwardingPath(&res, ingress)

	receive := b.ports.Receive(ingress)
	res.Consult(receive.ConsultedPorts()...)
	if receive.Reason != "" {
		res.Reason = receive.Reason
		res.Ingress = receive.Resolved.Name
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  RuleIngressPortDown,
			Subject: trace.Subject{Kind: "port", Key: receive.Decisive},
			Outputs: receive.ForwardingFacts(),
		})

		return Ingress{}, res, false
	}
	inPort := receive.Resolved
	res.Ingress = inPort.Name

	if !b.cfg.ForwardBPDU && ethernet.IsReserved(f.Dst) {
		res.Reason = ReasonReservedAddress
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  RuleReservedBridgeAddress,
			Subject: trace.Subject{Kind: "mac", Key: f.Dst.String()},
			Inputs:  []trace.Fact{frameSnapshot(f)},
			Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", 0, ReasonReservedAddress, false)},
		})

		return Ingress{}, res, false
	}

	var (
		classifiedFID    vlan.ID
		ingressPCP       vlan.PCP
		ingressDEI       bool
		remainingTags    []vlan.Tag
		ingressTPID      uint16
		isTagged         bool
		isPriorityTagged bool
		isUntagged       bool
	)

	if b.cfg.VLAN == nil {
		classifiedFID = 0
		res.FID = 0
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpClassify,
			RuleID:  RuleDefaultVLAN,
			Subject: trace.Subject{Kind: "vlan", Key: "0"},
			Inputs:  []trace.Fact{frameSnapshot(f)},
			Outputs: []trace.Fact{vlanSnapshot(res.Ingress, 0, 0, false, "untagged")},
		})
	} else {
		sw, swOk := b.cfg.VLAN.Switchports[res.Ingress]

		if sw.Tunnel != nil {
			ingressTPID = sw.Tunnel.EffectiveTPID()
			if len(f.Tags) > 0 {
				outer := f.Tags[0]
				if outer.TPID == 0 || outer.TPID == uint16(ethernet.EtherTypeDot1Q) {
					ingressPCP = outer.PCP
					ingressDEI = outer.DEI
					if outer.VID != 0 && len(sw.Tunnel.CustomerVIDs) > 0 && !slices.Contains(sw.Tunnel.CustomerVIDs, outer.VID) {
						res.Reason = ReasonCustomerVLAN
						res.Steps = append(res.Steps,
							trace.Step{
								Layer:   LayerNameVLAN,
								Op:      trace.OpFilter,
								RuleID:  RuleCustomerVLANFilter,
								Subject: trace.Subject{Kind: "port", Key: res.Ingress},
								Inputs:  []trace.Fact{frameSnapshot(f)},
								Outputs: []trace.Fact{vlanSnapshot(res.Ingress, outer.VID, outer.PCP, outer.DEI, "customer-rejected")},
							},
							trace.Step{
								Layer:   LayerNameVLAN,
								Op:      trace.OpDrop,
								RuleID:  RuleCustomerVLAN,
								Subject: trace.Subject{Kind: "port", Key: res.Ingress},
								Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", outer.VID, ReasonCustomerVLAN, false)},
							},
						)

						return Ingress{}, res, false
					}
				}
			}

			classifiedFID = sw.Tunnel.VID
			remainingTags = f.Tags

			res.FID = classifiedFID
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerNameVLAN,
				Op:      trace.OpClassify,
				RuleID:  RuleVLANTunnelClassify,
				Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(classifiedFID))},
				Inputs:  []trace.Fact{frameSnapshot(f)},
				Outputs: []trace.Fact{vlanSnapshot(res.Ingress, classifiedFID, ingressPCP, ingressDEI, "tunnel")},
			})

			if _, exists := b.cfg.VLAN.Table[classifiedFID]; !exists {
				res.Reason = ReasonUndefinedVLAN
				res.Steps = append(res.Steps, trace.Step{
					Layer:   LayerNameVLAN,
					Op:      trace.OpDrop,
					RuleID:  RuleVLANUndefined,
					Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(classifiedFID))},
					Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", classifiedFID, ReasonUndefinedVLAN, false)},
				})

				return Ingress{}, res, false
			}
		} else {
			if len(f.Tags) > 0 {
				outer := f.Tags[0]
				if outer.TPID == 0 || outer.TPID == uint16(ethernet.EtherTypeDot1Q) {
					ingressPCP = outer.PCP
					ingressDEI = outer.DEI
					remainingTags = f.Tags[1:]
					if outer.VID != 0 {
						isTagged = true
						classifiedFID = outer.VID
					} else {
						isPriorityTagged = true
					}
				} else {
					isUntagged = true
					remainingTags = f.Tags
				}
			} else {
				isUntagged = true
			}

			admission := sw.Admission
			if admission == "" {
				admission = All
			}
			switch admission {
			case TaggedOnly:
				if !isTagged {
					res.Reason = ReasonAdmission
					res.Steps = append(res.Steps,
						trace.Step{
							Layer:   LayerNameVLAN,
							Op:      trace.OpFilter,
							RuleID:  RuleAdmissionFilter,
							Subject: trace.Subject{Kind: "port", Key: res.Ingress},
							Inputs:  []trace.Fact{admission},
							Outputs: []trace.Fact{vlanSnapshot(res.Ingress, classifiedFID, ingressPCP, ingressDEI, "admission-rejected")},
						},
						trace.Step{
							Layer:   LayerNameVLAN,
							Op:      trace.OpDrop,
							RuleID:  RuleAdmission,
							Subject: trace.Subject{Kind: "port", Key: res.Ingress},
							Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", classifiedFID, ReasonAdmission, false)},
						},
					)

					return Ingress{}, res, false
				}
			case UntaggedAndPriorityTaggedOnly:
				if isTagged {
					res.Reason = ReasonAdmission
					res.Steps = append(res.Steps,
						trace.Step{
							Layer:   LayerNameVLAN,
							Op:      trace.OpFilter,
							RuleID:  RuleAdmissionFilter,
							Subject: trace.Subject{Kind: "port", Key: res.Ingress},
							Inputs:  []trace.Fact{admission},
							Outputs: []trace.Fact{vlanSnapshot(res.Ingress, classifiedFID, ingressPCP, ingressDEI, "admission-rejected")},
						},
						trace.Step{
							Layer:   LayerNameVLAN,
							Op:      trace.OpDrop,
							RuleID:  RuleAdmission,
							Subject: trace.Subject{Kind: "port", Key: res.Ingress},
							Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", classifiedFID, ReasonAdmission, false)},
						},
					)

					return Ingress{}, res, false
				}
			case All:
			}

			if isPriorityTagged || isUntagged {
				if !swOk || sw.PVID == nil {
					res.Reason = ReasonNoPVID
					res.Steps = append(res.Steps, trace.Step{
						Layer:   LayerNameVLAN,
						Op:      trace.OpDrop,
						RuleID:  RuleNoPVID,
						Subject: trace.Subject{Kind: "port", Key: res.Ingress},
						Inputs:  []trace.Fact{frameSnapshot(f)},
						Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", 0, ReasonNoPVID, false)},
					})

					return Ingress{}, res, false
				}
				classifiedFID = *sw.PVID
			}

			res.FID = classifiedFID
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerNameVLAN,
				Op:      trace.OpClassify,
				RuleID:  RuleVLANClassify,
				Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(classifiedFID))},
				Inputs:  []trace.Fact{frameSnapshot(f)},
				Outputs: []trace.Fact{vlanSnapshot(res.Ingress, classifiedFID, ingressPCP, ingressDEI, ingressTagForm(isTagged, isPriorityTagged))},
			})

			if sw.IngressFiltering {
				isMember := slices.Contains(sw.Tagged, classifiedFID) || slices.Contains(sw.Untagged, classifiedFID)
				if !isMember {
					res.Reason = ReasonIngressFilter
					res.Steps = append(res.Steps,
						trace.Step{
							Layer:   LayerNameVLAN,
							Op:      trace.OpFilter,
							RuleID:  RuleIngressFilter,
							Subject: trace.Subject{Kind: "port", Key: res.Ingress},
							Inputs:  []trace.Fact{vlanSnapshot(res.Ingress, classifiedFID, ingressPCP, ingressDEI, "classified")},
							Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", classifiedFID, ReasonIngressFilter, false)},
						},
						trace.Step{
							Layer:   LayerNameVLAN,
							Op:      trace.OpDrop,
							RuleID:  RuleIngressFilter,
							Subject: trace.Subject{Kind: "port", Key: res.Ingress},
							Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", classifiedFID, ReasonIngressFilter, false)},
						},
					)

					return Ingress{}, res, false
				}
			}

			if _, exists := b.cfg.VLAN.Table[classifiedFID]; !exists {
				res.Reason = ReasonUndefinedVLAN
				res.Steps = append(res.Steps, trace.Step{
					Layer:   LayerNameVLAN,
					Op:      trace.OpDrop,
					RuleID:  RuleVLANUndefined,
					Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(classifiedFID))},
					Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", classifiedFID, ReasonUndefinedVLAN, false)},
				})

				return Ingress{}, res, false
			}
		}
	}

	// The gate runs after classification, so it is asked about the VLAN the
	// frame was classified into rather than about the port alone. A frame that
	// never passed classification is not a spanning-tree question, which is why
	// the scope is consulted here and not ahead of the classification drops.
	ingressLearns := true
	ingressForwards := true
	var (
		firstFact    trace.Fact
		denyBoth     trace.Fact
		denyForwards trace.Fact
	)
	for _, entry := range b.gates {
		if entry.scope.Compare(analysis.WholeScope()) != 0 {
			res.ConsultScopes(analysis.FieldScope(entry.scope, "ports", res.Ingress))
		}
		if entry.gate == nil {
			continue
		}
		learns := entry.gate.Learns(res.Ingress, classifiedFID)
		forwards := entry.gate.Forwards(res.Ingress, classifiedFID)
		ingressLearns = ingressLearns && learns
		ingressForwards = ingressForwards && forwards
		fact := b.gateFact(entry.gate, res.Ingress, classifiedFID, learns, forwards)
		if firstFact == nil {
			firstFact = fact
		}
		if !learns && !forwards && denyBoth == nil {
			denyBoth = fact
		}
		if !forwards && denyForwards == nil {
			denyForwards = fact
		}
	}
	// The ingress drop below fires only when both predicates are false, so its
	// fact names the gate that alone accounts for that; when no single gate
	// denies both, the forwards-only drop further down needs the first gate
	// that denied forwarding. When every gate allows, this is unused, but the
	// first non-nil gate's fact is the one attributable fact: with one gate
	// configured, that gate's own.
	ingressGate := denyBoth
	if ingressGate == nil {
		ingressGate = denyForwards
	}
	if ingressGate == nil {
		ingressGate = firstFact
	}

	if !ingressLearns && !ingressForwards {
		res.Reason = ReasonPortBlocked
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  RulePortBlocked,
			Subject: trace.Subject{Kind: "port", Key: res.Ingress},
			Inputs:  facts(ingressGate),
			Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", classifiedFID, ReasonPortBlocked, false)},
		})

		return Ingress{}, res, false
	}

	if learn && ingressLearns && !f.Src.IsGroup() && !b.isFloodVLAN(classifiedFID) {
		srcKey := fdbKey{fid: classifiedFID, mac: f.Src}
		if b.fdbScope.Compare(analysis.WholeScope()) != 0 {
			res.ConsultScopes(fdbLookupScope(b.fdbScope, classifiedFID, f.Src))
		}
		existing, exists := b.fdb[srcKey]
		if !exists {
			var (
				evicted    Entry
				wasEvicted bool
			)
			if b.cfg.MaxEntries > 0 && b.fdbScope.Compare(analysis.WholeScope()) != 0 {
				res.ConsultScopes(analysis.FieldScope(b.fdbScope, "fdb"))
			}
			if b.cfg.MaxEntries > 0 && b.dynamic >= b.cfg.MaxEntries {
				evicted, wasEvicted = b.evictOldestDynamic()
			}
			b.fdb[srcKey] = Entry{
				FID:       classifiedFID,
				MAC:       f.Src,
				Port:      res.Ingress,
				Origin:    Observed,
				Lifetime:  Aging,
				LearnedAt: now,
			}
			b.dynamic++
			b.counters.Learned++
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpLearn,
				RuleID:  RuleLearn,
				Subject: trace.Subject{Kind: "mac", Key: f.Src.String()},
				Outputs: []trace.Fact{fdbSnapshot(classifiedFID, f.Src, true, res.Ingress, false)},
			})
			if wasEvicted {
				res.Steps = append(res.Steps, trace.Step{
					Layer:   LayerName,
					Op:      trace.OpLearn,
					RuleID:  RuleEvict,
					Subject: trace.Subject{Kind: "mac", Key: evicted.MAC.String()},
					Inputs:  []trace.Fact{fdbSnapshot(evicted.FID, evicted.MAC, true, evicted.Port, evicted.Lifetime == Static)},
					Outputs: []trace.Fact{fdbSnapshot(evicted.FID, evicted.MAC, false, "", false)},
				})
			}
		} else if existing.Lifetime != Static {
			before := existing
			ruleID := RuleLearn
			if existing.Port != res.Ingress {
				ruleID = RuleMove
				b.counters.Moved++
			}
			existing.Port = res.Ingress
			existing.LearnedAt = now
			b.fdb[srcKey] = existing
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpLearn,
				RuleID:  ruleID,
				Subject: trace.Subject{Kind: "mac", Key: f.Src.String()},
				Inputs:  []trace.Fact{fdbSnapshot(before.FID, before.MAC, true, before.Port, before.Lifetime == Static)},
				Outputs: []trace.Fact{fdbSnapshot(existing.FID, existing.MAC, true, existing.Port, existing.Lifetime == Static)},
			})
		}
	}

	if !ingressForwards {
		res.Reason = ReasonPortBlocked
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  RulePortBlocked,
			Subject: trace.Subject{Kind: "port", Key: res.Ingress},
			Inputs:  facts(ingressGate),
			Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", classifiedFID, ReasonPortBlocked, false)},
		})

		return Ingress{}, res, false
	}

	in := Ingress{
		Port:            res.Ingress,
		FID:             classifiedFID,
		PCP:             ingressPCP,
		DEI:             ingressDEI,
		RemainingTags:   remainingTags,
		TPID:            ingressTPID,
		Steps:           res.Steps,
		Now:             now,
		Commit:          commit,
		consultedPorts:  res.ConsultedPorts(),
		consultedScopes: res.ConsultedScopes(),
	}

	return in, Result{}, true
}

// Egress forwards or floods a classified ingress frame to its destination ports.
//
// An Ingress with an empty Port is a frame the device itself emits; the same-port
// rule and the flood's ingress exclusion then match no port; a port name is never
// empty since [port.Table] refuses one.
func (b *Layer) Egress(in Ingress, f ethernet.Frame) Result {
	var res Result
	res.Outcome = trace.Dropped
	res.Ingress = in.Port
	res.FID = in.FID
	res.Consult(in.consultedPorts...)
	res.ConsultScopes(in.consultedScopes...)
	if len(in.Steps) > 0 {
		res.Steps = make([]trace.Step, len(in.Steps))
		copy(res.Steps, in.Steps)
	}

	var (
		isHit   bool
		hitPort string
	)
	if !f.Dst.IsGroup() {
		if b.isFloodVLAN(in.FID) {
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpLookup,
				RuleID:  RuleFloodVLAN,
				Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(in.FID))},
				Inputs:  []trace.Fact{frameSnapshot(f)},
				Outputs: []trace.Fact{vlanSnapshot(in.Port, in.FID, in.PCP, in.DEI, "flood")},
			})
		} else {
			dstKey := fdbKey{fid: in.FID, mac: f.Dst}
			if b.fdbScope.Compare(analysis.WholeScope()) != 0 {
				res.ConsultScopes(fdbLookupScope(b.fdbScope, in.FID, f.Dst))
			}
			if entry, exists := b.fdb[dstKey]; exists {
				isHit = true
				hitPort = entry.Port
				res.Steps = append(res.Steps, trace.Step{
					Layer:   LayerName,
					Op:      trace.OpLookup,
					RuleID:  RuleUnicastHit,
					Subject: trace.Subject{Kind: "mac", Key: f.Dst.String()},
					Inputs:  []trace.Fact{frameSnapshot(f)},
					Outputs: []trace.Fact{fdbSnapshot(entry.FID, entry.MAC, true, entry.Port, entry.Lifetime == Static)},
				})
			} else {
				res.Steps = append(res.Steps, trace.Step{
					Layer:   LayerName,
					Op:      trace.OpLookup,
					RuleID:  RuleUnicastMiss,
					Subject: trace.Subject{Kind: "mac", Key: f.Dst.String()},
					Inputs:  []trace.Fact{frameSnapshot(f)},
					Outputs: []trace.Fact{fdbSnapshot(in.FID, f.Dst, false, "", false)},
				})
			}
		}
	} else {
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpLookup,
			RuleID:  RuleGroupDestination,
			Subject: trace.Subject{Kind: "mac", Key: f.Dst.String()},
			Inputs:  []trace.Fact{frameSnapshot(f)},
			Outputs: []trace.Fact{egressSnapshot("", "", in.FID, "group-destination", true)},
		})
		if b.resolver != nil {
			res.ConsultScopes(analysis.FieldScope(b.resolverScope, "vlans", strconv.Itoa(int(in.FID))))
			if ports, decided := b.resolver.Resolve(in.Now, in.FID, f); decided {
				var membership trace.Fact
				if resolver, ok := b.resolver.(semanticGroupResolver); ok {
					membership = resolver.MembershipFact(in.Now, in.FID, f, ports, decided)
				}
				emptyReason := ReasonNoEgress
				if len(ports) == 0 {
					emptyReason = trace.Reason("unregistered")
				}
				return b.replicate(res, in, f, ports, emptyReason, b.resolverLayer, b.resolverRule, membership)
			}
		}
	}

	if isHit {
		if hitPort == in.Port {
			res.Reason = ReasonSamePort
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpDrop,
				RuleID:  RuleSamePort,
				Subject: trace.Subject{Kind: "port", Key: in.Port},
				Outputs: []trace.Fact{egressSnapshot(in.Port, "", in.FID, ReasonSamePort, false)},
			})

			return res
		}

		destPort, exists := b.ports.Port(hitPort)
		if !exists {
			res.Reason = port.ReasonPortDown
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpDrop,
				RuleID:  RulePortDown,
				Subject: trace.Subject{Kind: "port", Key: hitPort},
				Outputs: []trace.Fact{port.ForwardingFact(hitPort, port.Port{}, false, port.ReasonPortDown)},
			})

			return res
		}
		res.Consult(b.egressDependencies(destPort)...)

		txReason := b.ports.Transmit(destPort.Name, len(f.Payload))
		if txReason == port.ReasonPortDown {
			res.Reason = port.ReasonPortDown
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				PCP:     in.PCP,
				Dropped: port.ReasonPortDown,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpDrop,
				RuleID:  RulePortDown,
				Subject: trace.Subject{Kind: "port", Key: destPort.Name},
				Inputs:  []trace.Fact{port.ForwardingFact(destPort.Name, destPort, false, port.ReasonPortDown)},
				Outputs: []trace.Fact{egressSnapshot(destPort.Name, "", in.FID, port.ReasonPortDown, false)},
			})

			return res
		}

		egressFrame, isMember := b.buildEgressFrame(destPort.Name, in.FID, f, in.PCP, in.DEI, in.RemainingTags, in.TPID)
		if b.cfg.VLAN != nil && !isMember {
			res.Reason = ReasonNotMember
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				PCP:     in.PCP,
				Dropped: ReasonNotMember,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerNameVLAN,
				Op:      trace.OpDrop,
				RuleID:  RuleNotMember,
				Subject: trace.Subject{Kind: "port", Key: destPort.Name},
				Inputs:  []trace.Fact{vlanSnapshot(destPort.Name, in.FID, in.PCP, in.DEI, "not-member")},
				Outputs: []trace.Fact{egressSnapshot(destPort.Name, "", in.FID, ReasonNotMember, false)},
			})

			return res
		}

		egressForwards := true
		var egressDenyForwards trace.Fact
		for _, entry := range b.gates {
			if entry.scope.Compare(analysis.WholeScope()) != 0 {
				res.ConsultScopes(analysis.FieldScope(entry.scope, "ports", destPort.Name))
			}
			if entry.gate == nil {
				continue
			}
			forwards := entry.gate.Forwards(destPort.Name, in.FID)
			egressForwards = egressForwards && forwards
			if !forwards && egressDenyForwards == nil {
				egressDenyForwards = b.gateFact(entry.gate, destPort.Name, in.FID, entry.gate.Learns(destPort.Name, in.FID), false)
			}
		}
		if !egressForwards {
			gate := egressDenyForwards
			res.Reason = ReasonPortBlocked
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Frame:   egressFrame,
				PCP:     in.PCP,
				Dropped: ReasonPortBlocked,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpDrop,
				RuleID:  RulePortBlocked,
				Subject: trace.Subject{Kind: "port", Key: destPort.Name},
				Inputs:  facts(gate),
				Outputs: []trace.Fact{egressSnapshot(destPort.Name, "", in.FID, ReasonPortBlocked, false)},
			})

			return res
		}

		if b.isProtected(in.Port) && b.isProtected(destPort.Name) {
			res.Reason = ReasonProtected
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Frame:   egressFrame,
				PCP:     in.PCP,
				Dropped: ReasonProtected,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpDrop,
				RuleID:  RuleProtected,
				Subject: trace.Subject{Kind: "port", Key: destPort.Name},
				Outputs: []trace.Fact{egressSnapshot(destPort.Name, "", in.FID, ReasonProtected, false)},
			})

			return res
		}

		if txReason == port.ReasonMTUExceeded {
			res.Reason = port.ReasonMTUExceeded
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Frame:   egressFrame,
				PCP:     in.PCP,
				Dropped: port.ReasonMTUExceeded,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpDrop,
				RuleID:  RuleMTUExceeded,
				Subject: trace.Subject{Kind: "port", Key: destPort.Name},
				Inputs:  []trace.Fact{port.ForwardingFact(destPort.Name, destPort, false, port.ReasonMTUExceeded)},
				Outputs: []trace.Fact{egressSnapshot(destPort.Name, "", in.FID, port.ReasonMTUExceeded, false)},
			})

			return res
		}

		member, selection, ok := b.selectMember(&res, in, destPort, egressFrame, in.PCP)
		if !ok {
			res.Reason = ReasonNoMember

			return res
		}

		if b.cfg.VLAN != nil {
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerNameVLAN,
				Op:      trace.OpRewrite,
				RuleID:  RuleVLANTagForm,
				Subject: trace.Subject{Kind: "port", Key: destPort.Name},
				Inputs:  []trace.Fact{frameSnapshot(f)},
				Outputs: []trace.Fact{frameSnapshot(egressFrame), vlanSnapshot(destPort.Name, in.FID, in.PCP, in.DEI, "egress")},
			})
		}
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpTransmit,
			RuleID:  RuleTransmit,
			Subject: trace.Subject{Kind: "port", Key: destPort.Name},
			Inputs:  facts(frameSnapshot(egressFrame), selection),
			Outputs: []trace.Fact{egressSnapshot(destPort.Name, member, in.FID, "", true)},
		})
		res.Egress = append(res.Egress, Egress{
			Port:   destPort.Name,
			Member: member,
			Frame:  egressFrame,
			PCP:    in.PCP,
		})
		res.Outcome = trace.Forwarded

		return res
	}

	ports := make([]string, 0, b.ports.Len())
	for _, candidate := range b.ports.Ports() {
		ports = append(ports, candidate.Name)
	}

	return b.replicate(res, in, f, ports, ReasonNoEgress, LayerName, RuleFlood, nil)
}

// EgressTo replicates f to the requested logical ports after applying the same
// eligibility, isolation, LAG, MTU, and tag rules as ordinary bridge flooding.
func (b *Layer) EgressTo(in Ingress, f ethernet.Frame, ports []string, emptyReason trace.Reason) Result {
	res := Result{
		Trace:   trace.Trace{Outcome: trace.Dropped},
		Ingress: in.Port,
		FID:     in.FID,
	}
	res.Consult(in.consultedPorts...)
	res.ConsultScopes(in.consultedScopes...)
	if len(in.Steps) > 0 {
		res.Steps = slices.Clone(in.Steps)
	}

	return b.replicate(res, in, f, ports, emptyReason, LayerName, RuleFlood, nil)
}

func (b *Layer) replicate(
	res Result,
	in Ingress,
	f ethernet.Frame,
	ports []string,
	emptyReason trace.Reason,
	replicationLayer trace.Layer,
	ruleID trace.RuleID,
	decision trace.Fact,
) Result {
	noCandidateReason := ReasonNoEgress
	if len(ports) == 0 {
		noCandidateReason = emptyReason
	}
	candidates := make([]port.Port, 0, len(ports))
	txReasons := make([]trace.Reason, 0, len(ports))
	egressFrames := make([]ethernet.Frame, 0, len(ports))
	seen := make(map[string]struct{}, len(ports))
	for _, name := range ports {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		candidate, ok := b.ports.Port(name)
		if !ok || candidate.LagParent != "" || candidate.Name == in.Port {
			continue
		}
		egressFrame, isMember := b.buildEgressFrame(candidate.Name, in.FID, f, in.PCP, in.DEI, in.RemainingTags, in.TPID)
		if !isMember {
			continue
		}
		res.Consult(b.egressDependencies(candidate)...)
		txReason := b.ports.Transmit(candidate.Name, len(f.Payload))
		if txReason == port.ReasonPortDown {
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerName,
				Op:      trace.OpDrop,
				RuleID:  port.RuleStatusDown,
				Subject: trace.Subject{Kind: "port", Key: candidate.Name},
				Inputs:  []trace.Fact{port.ForwardingFact(candidate.Name, candidate, false, txReason)},
			})
			continue
		}
		candidates = append(candidates, candidate)
		txReasons = append(txReasons, txReason)
		egressFrames = append(egressFrames, egressFrame)
	}

	if len(candidates) == 0 {
		res.Reason = noCandidateReason
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(noCandidateReason),
			Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(in.FID))},
			Inputs:  facts(decision),
			Outputs: []trace.Fact{egressSnapshot("", "", in.FID, noCandidateReason, false)},
		})

		return res
	}

	if ruleID == "" {
		ruleID = RuleFlood
	}
	res.Steps = append(res.Steps, trace.Step{
		Layer:   replicationLayer,
		Op:      trace.OpReplicate,
		RuleID:  ruleID,
		Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(in.FID))},
		Inputs:  facts(frameSnapshot(f), decision),
		Outputs: replicationFacts(candidates, in.FID),
	})

	var transmitted int
	for i, candidate := range candidates {
		txReason := txReasons[i]
		egressFrame := egressFrames[i]
		floodForwards := true
		var floodDenyForwards trace.Fact
		for _, entry := range b.gates {
			if entry.scope.Compare(analysis.WholeScope()) != 0 {
				res.ConsultScopes(analysis.FieldScope(entry.scope, "ports", candidate.Name))
			}
			if entry.gate == nil {
				continue
			}
			forwards := entry.gate.Forwards(candidate.Name, in.FID)
			floodForwards = floodForwards && forwards
			if !forwards && floodDenyForwards == nil {
				floodDenyForwards = b.gateFact(entry.gate, candidate.Name, in.FID, entry.gate.Learns(candidate.Name, in.FID), false)
			}
		}
		if !floodForwards {
			gate := floodDenyForwards
			res.Egress = append(res.Egress, Egress{
				Port:    candidate.Name,
				Frame:   egressFrame,
				PCP:     in.PCP,
				Dropped: ReasonPortBlocked,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpDrop,
				RuleID:  RulePortBlocked,
				Subject: trace.Subject{Kind: "port", Key: candidate.Name},
				Inputs:  facts(gate),
				Outputs: []trace.Fact{egressSnapshot(candidate.Name, "", in.FID, ReasonPortBlocked, false)},
			})

			continue
		}

		if b.isProtected(in.Port) && b.isProtected(candidate.Name) {
			res.Egress = append(res.Egress, Egress{
				Port:    candidate.Name,
				Frame:   egressFrame,
				PCP:     in.PCP,
				Dropped: ReasonProtected,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpDrop,
				RuleID:  RuleProtected,
				Subject: trace.Subject{Kind: "port", Key: candidate.Name},
				Outputs: []trace.Fact{egressSnapshot(candidate.Name, "", in.FID, ReasonProtected, false)},
			})

			continue
		}

		if txReason == port.ReasonMTUExceeded {
			res.Egress = append(res.Egress, Egress{
				Port:    candidate.Name,
				Frame:   egressFrame,
				PCP:     in.PCP,
				Dropped: port.ReasonMTUExceeded,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerName,
				Op:      trace.OpDrop,
				RuleID:  RuleMTUExceeded,
				Subject: trace.Subject{Kind: "port", Key: candidate.Name},
				Inputs:  []trace.Fact{port.ForwardingFact(candidate.Name, candidate, false, port.ReasonMTUExceeded)},
				Outputs: []trace.Fact{egressSnapshot(candidate.Name, "", in.FID, port.ReasonMTUExceeded, false)},
			})

			continue
		}

		member, selection, ok := b.selectMember(&res, in, candidate, egressFrame, in.PCP)
		if !ok {
			continue
		}

		if b.cfg.VLAN != nil {
			res.Steps = append(res.Steps, trace.Step{
				Layer:   LayerNameVLAN,
				Op:      trace.OpRewrite,
				RuleID:  RuleVLANTagForm,
				Subject: trace.Subject{Kind: "port", Key: candidate.Name},
				Inputs:  []trace.Fact{frameSnapshot(f)},
				Outputs: []trace.Fact{frameSnapshot(egressFrame), vlanSnapshot(candidate.Name, in.FID, in.PCP, in.DEI, "egress")},
			})
		}
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpTransmit,
			RuleID:  RuleTransmit,
			Subject: trace.Subject{Kind: "port", Key: candidate.Name},
			Inputs:  facts(frameSnapshot(egressFrame), selection),
			Outputs: []trace.Fact{egressSnapshot(candidate.Name, member, in.FID, "", true)},
		})
		res.Egress = append(res.Egress, Egress{
			Port:   candidate.Name,
			Member: member,
			Frame:  egressFrame,
			PCP:    in.PCP,
		})
		transmitted++
	}

	if transmitted > 0 {
		res.Outcome = trace.Flooded
		return res
	}

	if len(res.Egress) > 0 {
		res.Reason = res.Egress[0].Dropped
	} else {
		res.Reason = port.ReasonMTUExceeded
	}

	return res
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

// selectMember asks the selector which member carries a frame out of a LAG
// port, recording an egress drop with no-member when there is no selector or
// it names none; a port that is not a LAG has no member and always passes.
// It commits the selector's choice exactly when in.Commit is true.
func (b *Layer) selectMember(res *Result, in Ingress, p port.Port, f ethernet.Frame, pcp vlan.PCP) (string, trace.Fact, bool) {
	if p.Kind != port.LAG {
		return "", nil, true
	}
	if p.Forwards() {
		res.Consult(b.ports.Members(p.Name)...)
	}
	vid := in.FID
	var selection trace.Fact
	if b.selector != nil {
		res.ConsultScopes(analysis.FieldScope(b.selectorScope, "aggregators", p.Name))
		sel := b.selector.Select(in.Now, p.Name, in.Commit, f, vid)
		reason := trace.Reason("")
		if !sel.OK {
			reason = ReasonNoMember
		}
		selection = egressSnapshot(p.Name, sel.Member, vid, reason, sel.OK)
		if semantic, hasFacts := b.selector.(semanticSelector); hasFacts {
			selection = semantic.SelectionFact(p.Name, f, vid, sel)
		}
		if sel.OK {
			return sel.Member, selection, true
		}
	}
	res.Egress = append(res.Egress, Egress{
		Port:    p.Name,
		Frame:   f,
		PCP:     pcp,
		Dropped: ReasonNoMember,
	})
	res.Steps = append(res.Steps, trace.Step{
		Layer:   LayerName,
		Op:      trace.OpDrop,
		RuleID:  RuleNoMember,
		Subject: trace.Subject{Kind: "port", Key: p.Name},
		Inputs:  facts(selection),
		Outputs: []trace.Fact{egressSnapshot(p.Name, "", vid, ReasonNoMember, false)},
	})

	return "", selection, false
}

func (b *Layer) consultForwardingPath(res *Result, name string) {
	p, ok := b.ports.Port(name)
	if !ok {
		res.Consult((port.Port{Name: name}).Normalize())
		return
	}
	res.Consult(p)
	if !p.Forwards() || p.LagParent == "" {
		return
	}
	parent, ok := b.ports.Port(p.LagParent)
	if !ok {
		return
	}
	res.Consult(parent)
	if !parent.Forwards() || b.selector == nil {
		return
	}
	res.ConsultScopes(analysis.FieldScope(b.selectorScope, "aggregators", p.LagParent))
}

func (b *Layer) egressDependencies(p port.Port) []port.Port {
	path := []port.Port{p}
	if p.Kind != port.LAG || !p.Forwards() {
		return path
	}

	return append(path, b.ports.Members(p.Name)...)
}

func (b *Layer) buildEgressFrame(
	portName string,
	vid vlan.ID,
	f ethernet.Frame,
	ingressPCP vlan.PCP,
	ingressDEI bool,
	remainingTags []vlan.Tag,
	ingressTPID uint16,
) (ethernet.Frame, bool) {
	out := f
	if b.cfg.VLAN == nil {
		return out, true
	}

	sw, ok := b.cfg.VLAN.Switchports[portName]
	if !ok {
		return out, false
	}

	if !sw.CarriesVID(vid) {
		return out, false
	}

	if sw.Tunnel != nil {
		// CarriesVID already established sw.Tunnel.VID == vid for a tunnel
		// port; no other vid reaches here.
		if len(remainingTags) > 0 {
			out.Tags = make([]vlan.Tag, len(remainingTags))
			copy(out.Tags, remainingTags)
		} else {
			out.Tags = nil
		}

		return out, true
	}

	if slices.Contains(sw.Tagged, vid) {
		tpid := ingressTPID
		if tpid == 0 {
			tpid = uint16(ethernet.EtherTypeDot1Q)
		}
		cTag := vlan.Tag{
			TPID: tpid,
			PCP:  ingressPCP,
			DEI:  ingressDEI,
			VID:  vid,
		}
		out.Tags = make([]vlan.Tag, 0, 1+len(remainingTags))
		out.Tags = append(out.Tags, cTag)
		out.Tags = append(out.Tags, remainingTags...)

		return out, true
	}

	// CarriesVID already established vid is in sw.Tagged or sw.Untagged; the
	// Tagged case returned above, so vid is in sw.Untagged here.
	if sw.PriorityTags == PriorityTagsAlways || (sw.PriorityTags == PriorityTagsIfNonzero && ingressPCP != 0) {
		cTag := vlan.Tag{
			TPID: uint16(ethernet.EtherTypeDot1Q),
			PCP:  ingressPCP,
			DEI:  ingressDEI,
			VID:  0,
		}
		out.Tags = make([]vlan.Tag, 0, 1+len(remainingTags))
		out.Tags = append(out.Tags, cTag)
		out.Tags = append(out.Tags, remainingTags...)

		return out, true
	}

	if len(remainingTags) > 0 {
		out.Tags = make([]vlan.Tag, len(remainingTags))
		copy(out.Tags, remainingTags)
	} else {
		out.Tags = nil
	}

	return out, true
}

// OriginateFrame applies portName's egress VLAN tagging to a frame the switch
// itself originates, rather than one relayed from an ingress port: no
// priority, no drop-eligible marking, no remaining tags from an earlier
// classification, and no inherited TPID. It reports false when portName does
// not carry vid; a VLAN-unaware bridge carries every VID untagged and always
// reports true.
func (b *Layer) OriginateFrame(portName string, vid vlan.ID, f ethernet.Frame) (ethernet.Frame, bool) {
	return b.buildEgressFrame(portName, vid, f, 0, false, nil, 0)
}

func (b *Layer) isFloodVLAN(fid vlan.ID) bool {
	return slices.Contains(b.cfg.FloodVLANs, fid)
}

func (b *Layer) isProtected(port string) bool {
	return slices.Contains(b.cfg.ProtectedPorts, port)
}

// RetentionKey returns a canonical encoding of every normalized input the layer's
// runtime state depends on: its own configuration as Diff sees it and port link
// states.
func RetentionKey(cfg Config, env layer.Env) string {
	if cfg.AgingTime == 0 && cfg.MaxEntries == 0 && !cfg.ForwardBPDU &&
		len(cfg.FloodVLANs) == 0 && len(cfg.ProtectedPorts) == 0 && cfg.VLAN == nil {
		return ""
	}
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
