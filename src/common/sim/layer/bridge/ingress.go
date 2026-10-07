package bridge

import (
	"slices"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

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
		if res.Ingress == "" {
			res.Ingress = ingress
		}
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

	in, ok := b.classifyIngress(&res, f)
	if !ok {
		return Ingress{}, res, false
	}
	ingressLearns, ingressForwards, ingressGate := b.ingressGate(&res, in.FID)

	if !ingressLearns && !ingressForwards {
		res.Reason = ReasonPortBlocked
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  RulePortBlocked,
			Subject: trace.Subject{Kind: "port", Key: res.Ingress},
			Inputs:  facts(ingressGate),
			Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", in.FID, ReasonPortBlocked, false)},
		})

		return Ingress{}, res, false
	}

	if learn && ingressLearns {
		b.learnSource(&res, in, now, f)
	}
	if !ingressForwards {
		res.Reason = ReasonPortBlocked
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  RulePortBlocked,
			Subject: trace.Subject{Kind: "port", Key: res.Ingress},
			Inputs:  facts(ingressGate),
			Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", in.FID, ReasonPortBlocked, false)},
		})

		return Ingress{}, res, false
	}

	in.Port = res.Ingress
	in.Steps = res.Steps
	in.Now = now
	in.Commit = commit
	in.consultedPorts = res.ConsultedPorts()
	in.consultedScopes = res.ConsultedScopes()
	return in, Result{}, true
}

func (b *Layer) classifyIngress(res *Result, f ethernet.Frame) (Ingress, bool) {
	if b.cfg.VLAN == nil {
		res.FID = 0
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpClassify,
			RuleID:  RuleDefaultVLAN,
			Subject: trace.Subject{Kind: "vlan", Key: "0"},
			Inputs:  []trace.Fact{frameSnapshot(f)},
			Outputs: []trace.Fact{vlanSnapshot(res.Ingress, 0, 0, false, "untagged")},
		})
		return Ingress{}, true
	}
	sw, ok := b.cfg.VLAN.Switchports[res.Ingress]
	if sw.Tunnel != nil {
		return b.classifyTunnel(res, f, sw)
	}
	return b.classifyOrdinary(res, f, sw, ok)
}

func (b *Layer) classifyTunnel(res *Result, f ethernet.Frame, sw Switchport) (Ingress, bool) {
	in := Ingress{}
	in.TPID = sw.Tunnel.EffectiveTPID()
	if len(f.Tags) > 0 {
		outer := f.Tags[0]
		if outer.TPID == 0 || outer.TPID == uint16(ethernet.EtherTypeDot1Q) {
			in.PCP = outer.PCP
			in.DEI = outer.DEI
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

				return Ingress{}, false
			}
		}
	}

	in.FID = sw.Tunnel.VID
	in.RemainingTags = f.Tags

	res.FID = in.FID
	res.Steps = append(res.Steps, trace.Step{
		Layer:   LayerNameVLAN,
		Op:      trace.OpClassify,
		RuleID:  RuleVLANTunnelClassify,
		Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(in.FID))},
		Inputs:  []trace.Fact{frameSnapshot(f)},
		Outputs: []trace.Fact{vlanSnapshot(res.Ingress, in.FID, in.PCP, in.DEI, "tunnel")},
	})

	if _, exists := b.cfg.VLAN.Table[in.FID]; !exists {
		res.Reason = ReasonUndefinedVLAN
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerNameVLAN,
			Op:      trace.OpDrop,
			RuleID:  RuleVLANUndefined,
			Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(in.FID))},
			Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", in.FID, ReasonUndefinedVLAN, false)},
		})

		return Ingress{}, false
	}
	return in, true
}

func (b *Layer) classifyOrdinary(res *Result, f ethernet.Frame, sw Switchport, swOk bool) (Ingress, bool) {
	in := Ingress{}
	var isTagged, isPriorityTagged, isUntagged bool
	if len(f.Tags) > 0 {
		outer := f.Tags[0]
		if outer.TPID == 0 || outer.TPID == uint16(ethernet.EtherTypeDot1Q) {
			in.PCP = outer.PCP
			in.DEI = outer.DEI
			in.RemainingTags = f.Tags[1:]
			if outer.VID != 0 {
				isTagged = true
				in.FID = outer.VID
			} else {
				isPriorityTagged = true
			}
		} else {
			isUntagged = true
			in.RemainingTags = f.Tags
		}
	} else {
		isUntagged = true
	}

	if !admitIngress(res, in, sw.Admission, isTagged) {
		return Ingress{}, false
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

			return Ingress{}, false
		}
		in.FID = *sw.PVID
	}

	res.FID = in.FID
	res.Steps = append(res.Steps, trace.Step{
		Layer:   LayerNameVLAN,
		Op:      trace.OpClassify,
		RuleID:  RuleVLANClassify,
		Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(in.FID))},
		Inputs:  []trace.Fact{frameSnapshot(f)},
		Outputs: []trace.Fact{vlanSnapshot(res.Ingress, in.FID, in.PCP, in.DEI, ingressTagForm(isTagged, isPriorityTagged))},
	})

	if sw.IngressFiltering {
		isMember := slices.Contains(sw.Tagged, in.FID) || slices.Contains(sw.Untagged, in.FID)
		if !isMember {
			res.Reason = ReasonIngressFilter
			res.Steps = append(res.Steps,
				trace.Step{
					Layer:   LayerNameVLAN,
					Op:      trace.OpFilter,
					RuleID:  RuleIngressFilter,
					Subject: trace.Subject{Kind: "port", Key: res.Ingress},
					Inputs:  []trace.Fact{vlanSnapshot(res.Ingress, in.FID, in.PCP, in.DEI, "classified")},
					Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", in.FID, ReasonIngressFilter, false)},
				},
				trace.Step{
					Layer:   LayerNameVLAN,
					Op:      trace.OpDrop,
					RuleID:  RuleIngressFilter,
					Subject: trace.Subject{Kind: "port", Key: res.Ingress},
					Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", in.FID, ReasonIngressFilter, false)},
				},
			)

			return Ingress{}, false
		}
	}

	if _, exists := b.cfg.VLAN.Table[in.FID]; !exists {
		res.Reason = ReasonUndefinedVLAN
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerNameVLAN,
			Op:      trace.OpDrop,
			RuleID:  RuleVLANUndefined,
			Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(in.FID))},
			Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", in.FID, ReasonUndefinedVLAN, false)},
		})

		return Ingress{}, false
	}
	return in, true
}

func admitIngress(res *Result, in Ingress, policy Admission, isTagged bool) bool {
	admission := policy
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
					Outputs: []trace.Fact{vlanSnapshot(res.Ingress, in.FID, in.PCP, in.DEI, "admission-rejected")},
				},
				trace.Step{
					Layer:   LayerNameVLAN,
					Op:      trace.OpDrop,
					RuleID:  RuleAdmission,
					Subject: trace.Subject{Kind: "port", Key: res.Ingress},
					Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", in.FID, ReasonAdmission, false)},
				},
			)

			return false
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
					Outputs: []trace.Fact{vlanSnapshot(res.Ingress, in.FID, in.PCP, in.DEI, "admission-rejected")},
				},
				trace.Step{
					Layer:   LayerNameVLAN,
					Op:      trace.OpDrop,
					RuleID:  RuleAdmission,
					Subject: trace.Subject{Kind: "port", Key: res.Ingress},
					Outputs: []trace.Fact{egressSnapshot(res.Ingress, "", in.FID, ReasonAdmission, false)},
				},
			)

			return false
		}
	case All:
	}

	return true
}

func (b *Layer) ingressGate(res *Result, classifiedFID vlan.ID) (bool, bool, trace.Fact) {
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

	return ingressLearns, ingressForwards, ingressGate
}

func (b *Layer) learnSource(res *Result, in Ingress, now time.Time, f ethernet.Frame) {
	if !f.Src.IsGroup() && !b.isFloodVLAN(in.FID) && b.hasVLANMember(in.FID) {
		srcKey := fdbKey{fid: in.FID, mac: f.Src}
		if b.fdbScope.Compare(analysis.WholeScope()) != 0 {
			res.ConsultScopes(fdbLookupScope(b.fdbScope, in.FID, f.Src))
		}
		existing, exists := b.fdb[srcKey]
		if exists && b.expired(existing, now) {
			delete(b.fdb, srcKey)
			b.dynamic--
			b.counters.Expired++
			exists = false
		}
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
				FID:       in.FID,
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
				Outputs: []trace.Fact{fdbSnapshot(in.FID, f.Src, true, res.Ingress, false)},
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
}

func (b *Layer) hasVLANMember(fid vlan.ID) bool {
	if b.cfg.VLAN == nil {
		return true
	}
	for _, switchport := range b.cfg.VLAN.Switchports {
		if switchport.CarriesVID(fid) {
			return true
		}
	}
	return false
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
