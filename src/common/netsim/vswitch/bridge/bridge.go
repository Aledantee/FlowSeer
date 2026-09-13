// Package bridge implements an Ethernet transparent bridge with optional IEEE 802.1Q VLAN awareness.
package bridge

import (
	"bytes"
	"slices"
	"sort"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Gate decides whether a port learns addresses and forwards frames.
type Gate interface {
	Learns(port string) bool
	Forwards(port string) bool
}

type semanticGate interface {
	ForwardingFact(port string, learns, forwards bool) trace.Fact
}

// Selector chooses a member port of a link aggregation group to carry an egress frame.
type Selector interface {
	Select(lag string, f ethernet.Frame, vid vlan.ID) (string, bool)
}

type semanticSelector interface {
	SelectionFact(lag string, f ethernet.Frame, vid vlan.ID, member string, selected bool) trace.Fact
}

// GroupResolver selects the logical egress ports for a group frame.
// A false decision leaves the frame on the bridge's ordinary flood path.
type GroupResolver interface {
	Resolve(vid vlan.ID, f ethernet.Frame) (ports []string, decided bool)
}

type semanticGroupResolver interface {
	MembershipFact(vid vlan.ID, f ethernet.Frame, ports []string, decided bool) trace.Fact
}

// Bridge simulates an Ethernet transparent bridge with optional IEEE 802.1Q VLAN awareness.
//
// A Bridge is not safe for concurrent use.
type Bridge struct {
	cfg           Config
	ports         port.Table
	agingTime     time.Duration
	fdb           map[fdbKey]Entry
	gate          Gate
	gateScope     analysis.Scope
	selector      Selector
	selectorScope analysis.Scope
	resolver      GroupResolver
	resolverScope analysis.Scope
	counters      Counters
	// dynamic counts the entries that are not static, so the bound is checked
	// without a scan of the table.
	dynamic int
}

// New constructs a [Bridge] with the provided configuration and port table.
// It returns an error if the configuration is invalid against the ports.
func New(cfg Config, ports port.Table) (*Bridge, error) {
	if err := cfg.Validate(ports); err != nil {
		return nil, err
	}
	return newBridge(cfg.Normalize(), ports), nil
}

func newBridge(cfg Config, ports port.Table) *Bridge {
	aging := effectiveAgingTime(cfg.AgingTime)

	return &Bridge{
		cfg:       cfg.Clone(),
		ports:     ports.Clone(),
		agingTime: aging,
		fdb:       make(map[fdbKey]Entry),
	}
}

// Counters returns a snapshot of the forwarding database lifecycle counters.
func (b *Bridge) Counters() Counters {
	return b.counters
}

// Validate verifies the invariants of the bridge configuration against the given port table.
func (b *Bridge) Validate(ports port.Table) error {
	return b.cfg.Validate(ports)
}

// SetGate installs g as the bridge forwarding and learning gate. scope is the
// protocol scope containing the per-port fields consulted by the gate. A nil
// gate allows every port to learn and forward.
func (b *Bridge) SetGate(g Gate, scope analysis.Scope) {
	b.gate = g
	b.gateScope = scope
}

// SetSelector installs sel as the member port selector for LAG egress. scope
// contains the selector fields consulted by bridge forwarding.
func (b *Bridge) SetSelector(sel Selector, scope analysis.Scope) {
	b.selector = sel
	b.selectorScope = scope
}

// SetGroupResolver installs resolver as the bridge's group destination lookup.
// scope contains the membership fields consulted by bridge forwarding. A nil
// resolver leaves every group frame on the ordinary flood path.
func (b *Bridge) SetGroupResolver(resolver GroupResolver, scope analysis.Scope) {
	b.resolver = resolver
	b.resolverScope = scope
}

// FlushPorts removes dynamic forwarding database entries learned on the named ports.
func (b *Bridge) FlushPorts(ports []string) {
	if len(ports) == 0 {
		return
	}
	portSet := make(map[string]struct{}, len(ports))
	for _, p := range ports {
		portSet[p] = struct{}{}
	}
	for key, e := range b.fdb {
		if !e.Static {
			if _, ok := portSet[e.Port]; ok {
				delete(b.fdb, key)
				b.dynamic--
			}
		}
	}
}

// SetOperStatus updates the operational link state of the named port in the
// bridge's port table. It returns a structured validation error when state is
// outside the [port.LinkState] domain and leaves the table unchanged.
func (b *Bridge) SetOperStatus(portName string, state port.LinkState) error {
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
// Non-static seeds count as learned and are bounded by MaxEntries, evicting the
// oldest dynamic entry when the table exceeds the bound. Static seeds count nothing.
func (b *Bridge) Learn(seeds []Seed) error {
	normalized, err := NormalizeSeeds(b.cfg, b.ports, seeds)
	if err != nil {
		return err
	}

	for _, s := range normalized {
		key := fdbKey{fid: s.FID, mac: s.MAC}
		existing, exists := b.fdb[key]
		if s.Static {
			if exists && !existing.Static {
				b.dynamic--
			}
			b.fdb[key] = Entry(s)
			continue
		}

		if !exists || existing.Static {
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
func (b *Bridge) Entries() []Entry {
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
func (b *Bridge) Forget(fid vlan.ID, mac netaddr.MAC) bool {
	key := fdbKey{fid: fid, mac: mac}
	e, exists := b.fdb[key]
	if !exists {
		return false
	}
	delete(b.fdb, key)
	if !e.Static {
		b.dynamic--
	}

	return true
}

// Age removes dynamic forwarding database entries older than the configured aging time relative to now.
func (b *Bridge) Age(now time.Time) {
	for key, e := range b.fdb {
		if !e.Static && now.Sub(e.LearnedAt) > b.agingTime {
			delete(b.fdb, key)
			b.dynamic--
			b.counters.Expired++
		}
	}
}

func (b *Bridge) evictOldestDynamic() (Entry, bool) {
	var (
		oldestKey fdbKey
		oldest    Entry
		found     bool
	)
	for key, e := range b.fdb {
		if e.Static {
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
func (b *Bridge) Forward(now time.Time, ingress string, f ethernet.Frame) Result {
	return b.forward(now, ingress, f, true)
}

// Peek processes an ingress frame through the bridge pipeline without mutating the forwarding database.
func (b *Bridge) Peek(now time.Time, ingress string, f ethernet.Frame) Result {
	return b.forward(now, ingress, f, false)
}

func (b *Bridge) forward(now time.Time, ingress string, f ethernet.Frame, learn bool) Result {
	in, res, ok := b.Ingress(now, ingress, f, learn)
	if !ok {
		return res
	}

	return b.Egress(in, f)
}

// Ingress represents an admitted, classified, and learned frame ready for egress forwarding.
type Ingress struct {
	Port            string
	FID             vlan.ID
	PCP             vlan.PCP
	DEI             bool
	RemainingTags   []vlan.Tag
	TPID            uint16
	Steps           []trace.Step
	consultedPorts  []port.Port
	consultedScopes []analysis.Scope
}

// ConsultedPorts returns independent snapshots of the port state consulted
// before this ingress descriptor was produced.
func (in Ingress) ConsultedPorts() []port.Port {
	return append([]port.Port(nil), in.consultedPorts...)
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
// It returns false and a populated Result on any drop; on success it returns true and an Ingress
// descriptor for subsequent egress forwarding.
func (b *Bridge) Ingress(now time.Time, ingress string, f ethernet.Frame, learn bool) (Ingress, Result, bool) {
	var res Result
	res.Outcome = trace.Dropped
	res.Consult(b.forwardingPath(ingress)...)

	receive := b.ports.Receive(ingress)
	res.Consult(receive.ConsultedPorts()...)
	if receive.Reason != "" {
		res.Reason = receive.Reason
		res.Ingress = receive.Resolved.Name
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRelay,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID("ingress-port-down"),
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
			Layer:   port.LayerRelay,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID("reserved-bridge-address"),
			Subject: trace.Subject{Kind: "mac", Key: f.Dst.String()},
			Inputs:  []trace.Fact{frameSnapshot(f)},
			Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", 0, ReasonReservedAddress, false)},
		})

		return Ingress{}, res, false
	}

	ingressLearns := true
	ingressForwards := true
	var ingressGate trace.Fact
	if b.gate != nil {
		res.ConsultScopes(analysis.FieldScope(b.gateScope, "ports", res.Ingress))
		ingressLearns = b.gate.Learns(res.Ingress)
		ingressForwards = b.gate.Forwards(res.Ingress)
		ingressGate = b.gateFact(res.Ingress, ingressLearns, ingressForwards)
	}

	if !ingressLearns && !ingressForwards {
		res.Reason = ReasonPortBlocked
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRelay,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID("port-blocked"),
			Subject: trace.Subject{Kind: "port", Key: res.Ingress},
			Inputs:  facts(ingressGate),
			Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", 0, ReasonPortBlocked, false)},
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
			Layer:   port.LayerRelay,
			Op:      trace.OpClassify,
			RuleID:  trace.RuleID("default-vlan"),
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
								Layer:   port.LayerVlan,
								Op:      trace.OpFilter,
								RuleID:  trace.RuleID("customer-vlan-filter"),
								Subject: trace.Subject{Kind: "port", Key: res.Ingress},
								Inputs:  []trace.Fact{frameSnapshot(f)},
								Outputs: []trace.Fact{vlanSnapshot(res.Ingress, outer.VID, outer.PCP, outer.DEI, "customer-rejected")},
							},
							trace.Step{
								Layer:   port.LayerVlan,
								Op:      trace.OpDrop,
								RuleID:  trace.RuleID("customer-vlan"),
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
				Layer:   port.LayerVlan,
				Op:      trace.OpClassify,
				RuleID:  trace.RuleID("vlan-tunnel-classify"),
				Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(classifiedFID))},
				Inputs:  []trace.Fact{frameSnapshot(f)},
				Outputs: []trace.Fact{vlanSnapshot(res.Ingress, classifiedFID, ingressPCP, ingressDEI, "tunnel")},
			})

			if _, exists := b.cfg.VLAN.Table[classifiedFID]; !exists {
				res.Reason = ReasonUndefinedVLAN
				res.Steps = append(res.Steps, trace.Step{
					Layer:   port.LayerVlan,
					Op:      trace.OpDrop,
					RuleID:  trace.RuleID("vlan-undefined"),
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
							Layer:   port.LayerVlan,
							Op:      trace.OpFilter,
							RuleID:  trace.RuleID("admission-filter"),
							Subject: trace.Subject{Kind: "port", Key: res.Ingress},
							Inputs:  []trace.Fact{admission},
							Outputs: []trace.Fact{vlanSnapshot(res.Ingress, classifiedFID, ingressPCP, ingressDEI, "admission-rejected")},
						},
						trace.Step{
							Layer:   port.LayerVlan,
							Op:      trace.OpDrop,
							RuleID:  trace.RuleID("admission"),
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
							Layer:   port.LayerVlan,
							Op:      trace.OpFilter,
							RuleID:  trace.RuleID("admission-filter"),
							Subject: trace.Subject{Kind: "port", Key: res.Ingress},
							Inputs:  []trace.Fact{admission},
							Outputs: []trace.Fact{vlanSnapshot(res.Ingress, classifiedFID, ingressPCP, ingressDEI, "admission-rejected")},
						},
						trace.Step{
							Layer:   port.LayerVlan,
							Op:      trace.OpDrop,
							RuleID:  trace.RuleID("admission"),
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
						Layer:   port.LayerVlan,
						Op:      trace.OpDrop,
						RuleID:  trace.RuleID("no-pvid"),
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
				Layer:   port.LayerVlan,
				Op:      trace.OpClassify,
				RuleID:  trace.RuleID("vlan-classify"),
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
							Layer:   port.LayerVlan,
							Op:      trace.OpFilter,
							RuleID:  trace.RuleID("ingress-filter"),
							Subject: trace.Subject{Kind: "port", Key: res.Ingress},
							Inputs:  []trace.Fact{vlanSnapshot(res.Ingress, classifiedFID, ingressPCP, ingressDEI, "classified")},
							Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", classifiedFID, ReasonIngressFilter, false)},
						},
						trace.Step{
							Layer:   port.LayerVlan,
							Op:      trace.OpDrop,
							RuleID:  trace.RuleID("ingress-filter"),
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
					Layer:   port.LayerVlan,
					Op:      trace.OpDrop,
					RuleID:  trace.RuleID("vlan-undefined"),
					Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(classifiedFID))},
					Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", classifiedFID, ReasonUndefinedVLAN, false)},
				})

				return Ingress{}, res, false
			}
		}
	}

	if learn && ingressLearns && !f.Src.IsGroup() && !b.isFloodVLAN(classifiedFID) {
		srcKey := fdbKey{fid: classifiedFID, mac: f.Src}
		existing, exists := b.fdb[srcKey]
		if !exists {
			var (
				evicted    Entry
				wasEvicted bool
			)
			if b.cfg.MaxEntries > 0 && b.dynamic >= b.cfg.MaxEntries {
				evicted, wasEvicted = b.evictOldestDynamic()
			}
			b.fdb[srcKey] = Entry{
				FID:       classifiedFID,
				MAC:       f.Src,
				Port:      res.Ingress,
				Static:    false,
				LearnedAt: now,
			}
			b.dynamic++
			b.counters.Learned++
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerRelay,
				Op:      trace.OpLearn,
				RuleID:  trace.RuleID("learn"),
				Subject: trace.Subject{Kind: "mac", Key: f.Src.String()},
				Outputs: []trace.Fact{fdbSnapshot(classifiedFID, f.Src, true, res.Ingress, false)},
			})
			if wasEvicted {
				res.Steps = append(res.Steps, trace.Step{
					Layer:   port.LayerRelay,
					Op:      trace.OpLearn,
					RuleID:  trace.RuleID("evict"),
					Subject: trace.Subject{Kind: "mac", Key: evicted.MAC.String()},
					Inputs:  []trace.Fact{fdbSnapshot(evicted.FID, evicted.MAC, true, evicted.Port, evicted.Static)},
					Outputs: []trace.Fact{fdbSnapshot(evicted.FID, evicted.MAC, false, "", false)},
				})
			}
		} else if !existing.Static {
			before := existing
			ruleID := trace.RuleID("learn")
			if existing.Port != res.Ingress {
				ruleID = trace.RuleID("move")
				b.counters.Moved++
			}
			existing.Port = res.Ingress
			existing.LearnedAt = now
			b.fdb[srcKey] = existing
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerRelay,
				Op:      trace.OpLearn,
				RuleID:  ruleID,
				Subject: trace.Subject{Kind: "mac", Key: f.Src.String()},
				Inputs:  []trace.Fact{fdbSnapshot(before.FID, before.MAC, true, before.Port, before.Static)},
				Outputs: []trace.Fact{fdbSnapshot(existing.FID, existing.MAC, true, existing.Port, existing.Static)},
			})
		}
	}

	if !ingressForwards {
		res.Reason = ReasonPortBlocked
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRelay,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID("port-blocked"),
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
func (b *Bridge) Egress(in Ingress, f ethernet.Frame) Result {
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
				Layer:   port.LayerRelay,
				Op:      trace.OpLookup,
				RuleID:  trace.RuleID("flood-vlan"),
				Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(in.FID))},
				Inputs:  []trace.Fact{frameSnapshot(f)},
				Outputs: []trace.Fact{vlanSnapshot(in.Port, in.FID, in.PCP, in.DEI, "flood")},
			})
		} else {
			dstKey := fdbKey{fid: in.FID, mac: f.Dst}
			if entry, exists := b.fdb[dstKey]; exists {
				isHit = true
				hitPort = entry.Port
				res.Steps = append(res.Steps, trace.Step{
					Layer:   port.LayerRelay,
					Op:      trace.OpLookup,
					RuleID:  trace.RuleID("unicast-hit"),
					Subject: trace.Subject{Kind: "mac", Key: f.Dst.String()},
					Inputs:  []trace.Fact{frameSnapshot(f)},
					Outputs: []trace.Fact{fdbSnapshot(entry.FID, entry.MAC, true, entry.Port, entry.Static)},
				})
			} else {
				res.Steps = append(res.Steps, trace.Step{
					Layer:   port.LayerRelay,
					Op:      trace.OpLookup,
					RuleID:  trace.RuleID("unicast-miss"),
					Subject: trace.Subject{Kind: "mac", Key: f.Dst.String()},
					Inputs:  []trace.Fact{frameSnapshot(f)},
					Outputs: []trace.Fact{fdbSnapshot(in.FID, f.Dst, false, "", false)},
				})
			}
		}
	} else {
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRelay,
			Op:      trace.OpLookup,
			RuleID:  trace.RuleID("group-destination"),
			Subject: trace.Subject{Kind: "mac", Key: f.Dst.String()},
			Inputs:  []trace.Fact{frameSnapshot(f)},
			Outputs: []trace.Fact{egressSnapshot("", "", in.FID, "group-destination", true)},
		})
		if b.resolver != nil {
			res.ConsultScopes(analysis.FieldScope(b.resolverScope, "vlans", strconv.Itoa(int(in.FID))))
			if ports, decided := b.resolver.Resolve(in.FID, f); decided {
				var membership trace.Fact
				if resolver, ok := b.resolver.(semanticGroupResolver); ok {
					membership = resolver.MembershipFact(in.FID, f, ports, decided)
				}
				emptyReason := ReasonNoEgress
				if len(ports) == 0 {
					emptyReason = trace.Reason("unregistered")
				}
				return b.replicate(res, in, f, ports, emptyReason, port.LayerMcast, trace.RuleID("group-members"), membership)
			}
		}
	}

	if isHit {
		if hitPort == in.Port {
			res.Reason = ReasonSamePort
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerRelay,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("same-port"),
				Subject: trace.Subject{Kind: "port", Key: in.Port},
				Outputs: []trace.Fact{egressSnapshot(in.Port, "", in.FID, ReasonSamePort, false)},
			})

			return res
		}

		destPort, exists := b.ports.Port(hitPort)
		if !exists {
			res.Reason = port.ReasonPortDown
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerRelay,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("port-down"),
				Subject: trace.Subject{Kind: "port", Key: hitPort},
				Outputs: []trace.Fact{port.ForwardingFact(hitPort, port.Port{}, false, port.ReasonPortDown)},
			})

			return res
		}
		res.Consult(b.egressDependencies(destPort)...)

		_, txReason := b.ports.Transmit(destPort.Name, len(f.Payload))
		if txReason == port.ReasonPortDown {
			res.Reason = port.ReasonPortDown
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				PCP:     in.PCP,
				Dropped: port.ReasonPortDown,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerRelay,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("port-down"),
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
				Layer:   port.LayerVlan,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("not-member"),
				Subject: trace.Subject{Kind: "port", Key: destPort.Name},
				Inputs:  []trace.Fact{vlanSnapshot(destPort.Name, in.FID, in.PCP, in.DEI, "not-member")},
				Outputs: []trace.Fact{egressSnapshot(destPort.Name, "", in.FID, ReasonNotMember, false)},
			})

			return res
		}

		if b.gate != nil {
			res.ConsultScopes(analysis.FieldScope(b.gateScope, "ports", destPort.Name))
		}
		if b.gate != nil && !b.gate.Forwards(destPort.Name) {
			gate := b.gateFact(destPort.Name, b.gate.Learns(destPort.Name), false)
			res.Reason = ReasonPortBlocked
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Frame:   egressFrame,
				PCP:     in.PCP,
				Dropped: ReasonPortBlocked,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerRelay,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("port-blocked"),
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
				Layer:   port.LayerRelay,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("protected"),
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
				Layer:   port.LayerRelay,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("mtu-exceeded"),
				Subject: trace.Subject{Kind: "port", Key: destPort.Name},
				Inputs:  []trace.Fact{port.ForwardingFact(destPort.Name, destPort, false, port.ReasonMTUExceeded)},
				Outputs: []trace.Fact{egressSnapshot(destPort.Name, "", in.FID, port.ReasonMTUExceeded, false)},
			})

			return res
		}

		member, selection, ok := b.selectMember(&res, destPort, egressFrame, in.FID, in.PCP)
		if !ok {
			res.Reason = ReasonNoMember

			return res
		}

		if b.cfg.VLAN != nil {
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerVlan,
				Op:      trace.OpRewrite,
				RuleID:  trace.RuleID("vlan-tag-form"),
				Subject: trace.Subject{Kind: "port", Key: destPort.Name},
				Inputs:  []trace.Fact{frameSnapshot(f)},
				Outputs: []trace.Fact{frameSnapshot(egressFrame), vlanSnapshot(destPort.Name, in.FID, in.PCP, in.DEI, "egress")},
			})
		}
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRelay,
			Op:      trace.OpTransmit,
			RuleID:  trace.RuleID("transmit"),
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

	return b.replicate(res, in, f, ports, ReasonNoEgress, port.LayerRelay, trace.RuleID("flood"), nil)
}

// EgressTo replicates f to the requested logical ports after applying the same
// eligibility, isolation, LAG, MTU, and tag rules as ordinary bridge flooding.
func (b *Bridge) EgressTo(in Ingress, f ethernet.Frame, ports []string, emptyReason trace.Reason) Result {
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

	return b.replicate(res, in, f, ports, emptyReason, port.LayerRelay, trace.RuleID("flood"), nil)
}

func (b *Bridge) replicate(
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
		_, txReason := b.ports.Transmit(candidate.Name, len(f.Payload))
		if txReason == port.ReasonPortDown {
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerPort,
				Op:      trace.OpDrop,
				RuleID:  "port.status.down",
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
			Layer:   port.LayerRelay,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(noCandidateReason),
			Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(in.FID))},
			Inputs:  facts(decision),
			Outputs: []trace.Fact{egressSnapshot("", "", in.FID, noCandidateReason, false)},
		})

		return res
	}

	if ruleID == "" {
		ruleID = trace.RuleID("flood")
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
		if b.gate != nil {
			res.ConsultScopes(analysis.FieldScope(b.gateScope, "ports", candidate.Name))
		}
		if b.gate != nil && !b.gate.Forwards(candidate.Name) {
			gate := b.gateFact(candidate.Name, b.gate.Learns(candidate.Name), false)
			res.Egress = append(res.Egress, Egress{
				Port:    candidate.Name,
				Frame:   egressFrame,
				PCP:     in.PCP,
				Dropped: ReasonPortBlocked,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerRelay,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("port-blocked"),
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
				Layer:   port.LayerRelay,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("protected"),
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
				Layer:   port.LayerRelay,
				Op:      trace.OpDrop,
				RuleID:  trace.RuleID("mtu-exceeded"),
				Subject: trace.Subject{Kind: "port", Key: candidate.Name},
				Inputs:  []trace.Fact{port.ForwardingFact(candidate.Name, candidate, false, port.ReasonMTUExceeded)},
				Outputs: []trace.Fact{egressSnapshot(candidate.Name, "", in.FID, port.ReasonMTUExceeded, false)},
			})

			continue
		}

		member, selection, ok := b.selectMember(&res, candidate, egressFrame, in.FID, in.PCP)
		if !ok {
			continue
		}

		if b.cfg.VLAN != nil {
			res.Steps = append(res.Steps, trace.Step{
				Layer:   port.LayerVlan,
				Op:      trace.OpRewrite,
				RuleID:  trace.RuleID("vlan-tag-form"),
				Subject: trace.Subject{Kind: "port", Key: candidate.Name},
				Inputs:  []trace.Fact{frameSnapshot(f)},
				Outputs: []trace.Fact{frameSnapshot(egressFrame), vlanSnapshot(candidate.Name, in.FID, in.PCP, in.DEI, "egress")},
			})
		}
		res.Steps = append(res.Steps, trace.Step{
			Layer:   port.LayerRelay,
			Op:      trace.OpTransmit,
			RuleID:  trace.RuleID("transmit"),
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

func (b *Bridge) gateFact(name string, learns, forwards bool) trace.Fact {
	gate, ok := b.gate.(semanticGate)
	if !ok {
		return nil
	}

	return gate.ForwardingFact(name, learns, forwards)
}

// selectMember asks the selector which member carries a frame out of a LAG
// port, recording an egress drop with no-member when there is no selector or
// it names none; a port that is not a LAG has no member and always passes.
func (b *Bridge) selectMember(res *Result, p port.Port, f ethernet.Frame, vid vlan.ID, pcp vlan.PCP) (string, trace.Fact, bool) {
	if p.Kind != port.Lag {
		return "", nil, true
	}
	if p.Forwards() {
		res.Consult(b.ports.Members(p.Name)...)
	}
	var selection trace.Fact
	if b.selector != nil {
		res.ConsultScopes(analysis.FieldScope(b.selectorScope, "aggregators", p.Name))
		member, ok := b.selector.Select(p.Name, f, vid)
		reason := trace.Reason("")
		if !ok {
			reason = ReasonNoMember
		}
		selection = egressSnapshot(p.Name, member, vid, reason, ok)
		if semantic, hasFacts := b.selector.(semanticSelector); hasFacts {
			selection = semantic.SelectionFact(p.Name, f, vid, member, ok)
		}
		if ok {
			return member, selection, true
		}
	}
	res.Egress = append(res.Egress, Egress{
		Port:    p.Name,
		Frame:   f,
		PCP:     pcp,
		Dropped: ReasonNoMember,
	})
	res.Steps = append(res.Steps, trace.Step{
		Layer:   port.LayerRelay,
		Op:      trace.OpDrop,
		RuleID:  trace.RuleID("no-member"),
		Subject: trace.Subject{Kind: "port", Key: p.Name},
		Inputs:  facts(selection),
		Outputs: []trace.Fact{egressSnapshot(p.Name, "", vid, ReasonNoMember, false)},
	})

	return "", selection, false
}

func (b *Bridge) forwardingPath(name string) []port.Port {
	p, ok := b.ports.Port(name)
	if !ok {
		return []port.Port{(port.Port{Name: name}).Normalize()}
	}
	path := []port.Port{p}
	if p.LagParent != "" {
		if parent, exists := b.ports.Port(p.LagParent); exists {
			path = append(path, parent)
		}
	}

	return path
}

func (b *Bridge) egressDependencies(p port.Port) []port.Port {
	path := []port.Port{p}
	if p.Kind != port.Lag {
		return path
	}

	return append(path, b.ports.Members(p.Name)...)
}

func (b *Bridge) buildEgressFrame(
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

	if sw.Tunnel != nil {
		if sw.Tunnel.VID == vid {
			if len(remainingTags) > 0 {
				out.Tags = make([]vlan.Tag, len(remainingTags))
				copy(out.Tags, remainingTags)
			} else {
				out.Tags = nil
			}

			return out, true
		}

		return out, false
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

	if slices.Contains(sw.Untagged, vid) {
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

	return out, false
}

func (b *Bridge) isFloodVLAN(fid vlan.ID) bool {
	return slices.Contains(b.cfg.FloodVLANs, fid)
}

func (b *Bridge) isProtected(port string) bool {
	return slices.Contains(b.cfg.ProtectedPorts, port)
}
