package bridge

import (
	"slices"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

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
