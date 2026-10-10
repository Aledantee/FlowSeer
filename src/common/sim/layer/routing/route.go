package routing

import (
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Route routes an incoming Ethernet frame arriving on iface through layer 3 processing,
// updating hop limit, IP checksum, and link-layer addressing.
//
// If the packet header fails to decode, Route drops the frame with [ReasonBadHeader]
// (RFC 1812 section 5.2.2). If the destination matches an interface address of the VRF,
// Route marks the frame [ReasonNotRouted]. If the hop limit is 1 or less, Route drops
// the frame with [ReasonTTLExpired] (RFC 1812 section 5.3.1). If no route matches the
// destination, Route drops with [ReasonNoRoute] (RFC 1812 section 5.2.4.3). If the next
// hop's neighbor entry cannot resolve it, either because the VRF never resolves neighbors
// or because the egress interface has no address of the next hop's family, Route drops with
// [ReasonNeighborMiss]; if the next hop is simply unresolved yet under [NeighborObserved],
// Route reports [ReasonNeighborPending] instead — see the package README's "Neighbor
// lifecycle" section.
//
// now is the instant the neighbor lifecycle reasons against, both for a newly created entry's
// resolution deadline and (through [Layer.Advance]) an existing one's reachability deadline. commit
// gates every mutation Route can make to the neighbor table: with it clear, Route never creates
// an entry or queues a frame, so a preview cannot change what a later call observes.
//
// Route never mutates f; the returned Result carries a newly constructed frame and payload.
func (l *Layer) Route(now time.Time, iface string, f ethernet.Frame, commit bool) Result {
	vrfName, ok := l.ifaceVRF[iface]
	if !ok {
		return Result{
			Steps: []trace.Step{
				{Layer: LayerName, Op: trace.OpClassify, RuleID: RuleClassify, Subject: trace.Subject{Kind: "interface", Key: iface}, Outputs: []trace.Fact{packetSnapshot(iface, f, ip.Header{}, false, ReasonNoRoute)}},
				{Layer: LayerName, Op: trace.OpLookup, RuleID: RuleNoRoute, Subject: trace.Subject{Kind: "interface", Key: iface}, Outputs: []trace.Fact{routeSnapshot("", netip.Addr{}, nil)}},
				{Layer: LayerName, Op: trace.OpDrop, RuleID: RuleUnknownInterface, Subject: trace.Subject{Kind: "interface", Key: iface}, Outputs: []trace.Fact{packetSnapshot(iface, f, ip.Header{}, false, ReasonNoRoute)}},
			},
			Reason: ReasonNoRoute,
		}
	}

	vrf := l.vrfs[vrfName]
	hdr, payload, err := ip.Decode(f.Payload)
	// The egress frame keeps the EtherType, so a header whose family the
	// EtherType does not name would leave with a label no receiver can parse.
	if err == nil && (hdr.Version() != 4 || f.EtherType != ethernet.EtherTypeIPv4) &&
		(hdr.Version() != 6 || f.EtherType != ethernet.EtherTypeIPv6) {
		err = errs.New().Attr("field", "version").Msg("IP version disagrees with the frame's EtherType")
	}
	classifyReason := trace.Reason("")
	if err != nil {
		classifyReason = ReasonBadHeader
	}
	res := Result{VRF: vrfName}
	res.Steps = append(res.Steps, trace.Step{
		Layer:   LayerName,
		Op:      trace.OpClassify,
		RuleID:  RuleClassify,
		Subject: trace.Subject{Kind: "interface", Key: iface},
		Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, err == nil, classifyReason)},
		Outputs: []trace.Fact{routeInterfaceFact(iface)},
	})

	if err != nil {
		res.Reason = ReasonBadHeader
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonBadHeader),
			Subject: trace.Subject{Kind: "interface", Key: iface},
			Outputs: []trace.Fact{packetSnapshot(iface, f, hdr, false, ReasonBadHeader)},
		})
		return res
	}
	res.consult(localAddressLookupScope(l.nodeID, vrfName, hdr.Dst))

	if _, isLocal := vrf.localAddrs[hdr.Dst]; isLocal {
		res.Reason = ReasonNotRouted
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpLookup,
			RuleID:  RuleLocalDelivery,
			Subject: trace.Subject{Kind: "ip", Key: hdr.Dst.String()},
			Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, "")},
			Outputs: []trace.Fact{routeSnapshot(vrfName, hdr.Dst, nil)},
		})
		return res
	}

	if hdr.HopLimit <= 1 {
		res.Reason = ReasonTTLExpired
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonTTLExpired),
			Subject: trace.Subject{Kind: "ip", Key: hdr.Dst.String()},
			Outputs: []trace.Fact{packetSnapshot(iface, f, hdr, false, ReasonTTLExpired)},
		})
		return res
	}

	res.consult(RouteLookupScope(l.nodeID, vrfName, hdr.Dst))
	var flowLabel uint32
	if hdr.V6 != nil {
		flowLabel = hdr.V6.FlowLabel
	}
	sel := selectRoute(vrf.lookup(hdr.Dst), hdr.Src, hdr.Dst, flowLabel)
	if sel == nil {
		res.Reason = ReasonNoRoute
		res.Steps = append(res.Steps,
			trace.Step{
				Layer:   LayerName,
				Op:      trace.OpLookup,
				RuleID:  RuleNoRoute,
				Subject: trace.Subject{Kind: "ip", Key: hdr.Dst.String()},
				Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, "")},
				Outputs: []trace.Fact{routeSnapshot(vrfName, hdr.Dst, nil)},
			},
			trace.Step{
				Layer:   LayerName,
				Op:      trace.OpDrop,
				RuleID:  RuleNoRoute,
				Subject: trace.Subject{Kind: "vrf", Key: vrfName},
				Outputs: []trace.Fact{packetSnapshot(iface, f, hdr, false, ReasonNoRoute)},
			},
		)
		return res
	}

	res.candidates = candidateSet(sel.candidates)
	matchedRoute := sel.route()
	res.Steps = append(res.Steps, trace.Step{
		Layer:   LayerName,
		Op:      trace.OpLookup,
		RuleID:  trace.RuleID(matchedRoute.kind),
		Subject: trace.Subject{Kind: "prefix", Key: matchedRoute.Prefix.String()},
		Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, "")},
		Outputs: []trace.Fact{routeSnapshot(vrfName, hdr.Dst, sel)},
	})

	targetAddr := hdr.Dst
	if matchedRoute.resolvedNextHop.IsValid() {
		targetAddr = matchedRoute.resolvedNextHop
	}

	targetIface := matchedRoute.Interface
	res.Interface = targetIface
	res.NextHop = targetAddr

	// Encode before resolving, in the egress form both paths use: heldHdr's hop limit is already
	// decremented, the same value the direct path's newHdr computes below, so one encode here
	// validates both, and its bytes serve the direct path too — mirroring Originate, which
	// encodes before resolving its own next hop for the same reason.
	heldHdr := hdr
	heldHdr.HopLimit--
	pktBytes, err := heldHdr.Encode(payload)
	if err != nil {
		res.Reason = ReasonBadHeader
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonBadHeader),
			Subject: trace.Subject{Kind: "interface", Key: targetIface},
			Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, "")},
			Outputs: []trace.Fact{packetSnapshot(iface, f, hdr, false, ReasonBadHeader)},
		})
		return res
	}

	res.consult(NeighborLookupScope(l.nodeID, vrfName, targetIface, targetAddr))
	key := neighborKey{iface: targetIface, addr: targetAddr}
	lookup := l.resolveNeighbor(vrf, now, key, l.ifaces[targetIface], commit, func() heldEntry {
		// heldHdr is already encoded above, so finishHeld need not (and must not) decrement it
		// again once resolution completes.
		pcp, dei := f.Priority()
		l.lastHold++
		res.Hold = l.lastHold
		return heldEntry{token: res.Hold, ingress: iface, iface: targetIface, etherType: f.EtherType, header: heldHdr, payload: payload, pcp: pcp, dei: dei}
	})

	res.Emissions = lookup.emissions
	if lookup.state == NeighborIncomplete {
		res.Reason = ReasonNeighborPending
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpLookup,
			RuleID:  trace.RuleID(ReasonNeighborPending),
			Subject: trace.Subject{Kind: "ip", Key: targetAddr.String()},
			Inputs:  []trace.Fact{routeSnapshot(vrfName, hdr.Dst, sel)},
			Outputs: []trace.Fact{neighborSnapshot(targetIface, targetAddr, netaddr.MAC{}, lookup.state, lookup.origin)},
		})
		return res
	}
	if !lookup.ok {
		res.Reason = ReasonNeighborMiss
		res.Steps = append(res.Steps, trace.Step{
			Layer:   LayerName,
			Op:      trace.OpDrop,
			RuleID:  trace.RuleID(ReasonNeighborMiss),
			Subject: trace.Subject{Kind: "ip", Key: targetAddr.String()},
			Inputs:  []trace.Fact{routeSnapshot(vrfName, hdr.Dst, sel)},
			Outputs: []trace.Fact{neighborSnapshot(targetIface, targetAddr, netaddr.MAC{}, lookup.state, lookup.origin)},
		})
		return res
	}

	egressIfaceObj := l.ifaces[targetIface]
	egressMAC := egressIfaceObj.MAC

	res.Steps = append(res.Steps, trace.Step{
		Layer:   LayerName,
		Op:      trace.OpRewrite,
		RuleID:  RuleDecrementTTL,
		Subject: trace.Subject{Kind: "interface", Key: targetIface},
		Inputs:  []trace.Fact{packetSnapshot(iface, f, hdr, true, ""), neighborSnapshot(targetIface, targetAddr, lookup.mac, lookup.state, lookup.origin)},
		Outputs: []trace.Fact{packetSnapshot(targetIface, f, heldHdr, true, "")},
	})

	res.Interface = targetIface
	res.Frame = ethernet.Frame{
		Src:       egressMAC,
		Dst:       lookup.mac,
		EtherType: f.EtherType,
		Payload:   pktBytes,
	}

	return res
}
