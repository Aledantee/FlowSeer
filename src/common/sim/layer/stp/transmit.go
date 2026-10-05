package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

type emissionKind uint8

const (
	emissionDesignated emissionKind = iota
	emissionAgreement
)

func (l *Layer) emit(t *tree, p *portState, now time.Time, kind emissionKind, emissions *[]layer.Emission) {
	// SSTP has no legacy shape: bpdu.EncodeSSTP forces a version of at least 2 and
	// bpdu.DecodeSSTP refuses anything else, so a non-CIST tree that migrated to
	// legacy STP has no frame it can send. It builds and meters nothing
	// rather than sending a per-VLAN frame whose header would contradict its
	// content.
	lk := l.link(p.name)
	if l.pvst != nil && t.id != cistID && !lk.sendRSTP {
		return
	}

	tx := l.tx(t, p.name)

	for tx.count > 0 && !tx.tick.After(now) {
		tx.count--
		tx.tick = tx.tick.Add(time.Second)
	}
	if tx.count == 0 {
		tx.tick = time.Time{}
	}

	if tx.count < int(l.txHoldCount) {
		var msg bpdu.BPDU
		switch kind {
		case emissionDesignated:
			proposal := lk.pointToPoint && p.state == StateDiscarding && !p.agreed && lk.sendRSTP
			msg = l.makeBPDU(t, p, now, proposal)
		case emissionAgreement:
			msg = l.makeAgreementBPDU(t, p, now)
		}

		built, err := l.frames(t, p, msg)
		if err != nil {
			// MST.Validate rejects a region with more instances than one
			// BPDU can carry, so this is unreachable for a Layer built
			// through New; the handling exists so that a future caller
			// building a Layer another way degrades to sending nothing
			// rather than to sending an empty frame.
			return
		}

		for _, f := range built {
			*emissions = append(*emissions, layer.Emission{Port: p.name, VID: f.vid, Frame: f.frame})
		}
		p.txBPDUs++
		wasZero := tx.count == 0
		tx.count++
		if wasZero {
			tx.tick = now.Add(time.Second)
		}
	} else {
		switch kind {
		case emissionDesignated:
			tx.pendingDesignated = true
		case emissionAgreement:
			tx.pendingAgreement = true
		}
	}
}

// taggedFrame is one frame an emission puts on the wire, with the VLAN it
// rides. A zero vid leaves the frame untagged and unchecked against the
// port's VLAN membership.
type taggedFrame struct {
	vid   vlan.ID
	frame ethernet.Frame
}

// frames builds the wire form of one BPDU for tree t on port p. Outside PVST
// mode that is the single IEEE-addressed frame, untagged. Inside it, every
// tree sends its BPDU to the SSTP address on its own VLAN, and VLAN 1's tree
// sends a second, IEEE-addressed and untagged, which is the one an RSTP or
// MSTP neighbor converges with — unless the port has migrated to legacy STP,
// in which case the SSTP copy is dropped and only the IEEE Configuration BPDU
// goes out: SSTP has no legacy shape to carry it in, so sending the SSTP copy
// would relabel a legacy BPDU under a version-2 RST header. emit already
// withholds a non-CIST tree's frame entirely on a migrated port, so this
// branch is only ever reached with the link's sendRSTP true there. The two frames are
// one transmission and spend one budget slot between them.
func (l *Layer) frames(t *tree, p *portState, b bpdu.BPDU) ([]taggedFrame, error) {
	if l.pvst == nil {
		frame, err := bpdu.Encode(b, l.address)
		if err != nil {
			return nil, err
		}

		return []taggedFrame{{frame: frame}}, nil
	}

	var built []taggedFrame
	if l.link(p.name).sendRSTP {
		sstp, err := bpdu.EncodeSSTP(b, t.vid, l.address)
		if err != nil {
			return nil, err
		}
		built = append(built, taggedFrame{vid: t.vid, frame: sstp})
	}

	if t.id != cistID {
		return built, nil
	}

	ieee, err := bpdu.Encode(b, l.address)
	if err != nil {
		return nil, err
	}

	return append(built, taggedFrame{frame: ieee}), nil
}

// instanceRemainingHops computes the hop count an MSTI tree originates with:
// the region's MaxHops when this bridge is the instance's own regional root,
// and otherwise one fewer than its root port received.
func (l *Layer) instanceRemainingHops(t *tree) uint8 {
	maxHops := effectiveMaxHops(l.mst.MaxHops)

	isRegionalRoot := t.rootID == t.bridgeID
	if t.id == cistID {
		isRegionalRoot = t.regionalRootID == t.bridgeID
	}
	if isRegionalRoot {
		return maxHops
	}

	rp, ok := t.ports[t.rootPort]
	if !ok || rp.rcvRemainingHops == 0 {
		return maxHops
	}

	return rp.rcvRemainingHops - 1
}

// gatherMSTIRecords builds one MSTI record per configured instance for
// transmission on port p, carrying that instance's current regional root,
// internal cost, and per-instance bridge and port priority. It is called only
// while building the CIST's own BPDU: an MST bridge always emits its MSTI
// records alongside the CIST, on every up port, boundary ports included,
// because a port is classified internal or external only on reception.
func (l *Layer) gatherMSTIRecords(now time.Time, p *portState) []bpdu.MSTIRecord {
	var recs []bpdu.MSTIRecord

	for _, id := range l.treeOrder {
		if id == cistID {
			continue
		}
		mstid := bpdu.MSTID(id)
		mt := l.trees[id]
		mp, ok := mt.ports[p.name]
		if !ok {
			continue
		}

		// Flags reuses the CIST's own role/learning/forwarding bit layout
		// (SetRole/SetLearning/SetForwarding/SetTopologyChange), applied to
		// this instance port's role and state and this instance's own
		// topology change timer rather than the CIST's, so the record says
		// what the sender's per-instance port is doing and lets a peer
		// reconverge that instance without waiting on its filtering database
		// to age out. A zero-value BPDU used only to borrow its bit-setting
		// methods never gets encoded itself.
		var flags bpdu.BPDU
		flags.SetRole(mp.role)
		flags.SetLearning(mp.state == StateLearning || mp.state == StateForwarding)
		flags.SetForwarding(mp.state == StateForwarding)
		if !mt.topologyChangeTimer.IsZero() && mt.topologyChangeTimer.After(now) {
			flags.SetTopologyChange(true)
		}

		recs = append(recs, bpdu.MSTIRecord{
			MSTID:                mstid,
			Flags:                flags.Flags,
			RegionalRootID:       mt.rootID,
			InternalRootPathCost: mt.rootPathCost,
			BridgePriority:       uint8(mt.bridgeID.Priority >> 8),
			PortPriority:         uint8(mp.portID >> 8),
			RemainingHops:        l.instanceRemainingHops(mt),
		})
	}

	return recs
}

func (l *Layer) makeBPDU(t *tree, p *portState, now time.Time, proposal bool) bpdu.BPDU {
	sendRSTP := l.link(p.name).sendRSTP
	var msgAge time.Duration
	maxAge, hello, fwdDelay := l.times(t)

	if t.rootPort != "" {
		if rp, ok := t.ports[t.rootPort]; ok && rp.rcvInfoValid {
			msgAge = rp.rcvMessageAge + time.Second
		}
	}

	// The tree's own bridge identifier, not the layer's: under PVST every
	// tree carries its VLAN in the system-ID extension, and a BPDU sent under
	// the layer's identifier would never match the receiving tree's Backup
	// test (rcvBridgeID == t.bridgeID). Outside PVST the CIST's identifier is
	// the layer's, and only the CIST ever builds a BPDU.
	b := bpdu.BPDU{
		RootID:       t.rootID,
		RootPathCost: t.rootPathCost,
		BridgeID:     t.bridgeID,
		PortID:       p.portID,
		MessageAge:   msgAge,
		MaxAge:       maxAge,
		HelloTime:    hello,
		ForwardDelay: fwdDelay,
	}

	if !sendRSTP {
		b.Version = 0
		b.Type = bpdu.TypeConfiguration
		proposal = false
	} else {
		b.Version = 2
		b.Type = bpdu.TypeRapid
	}

	b.SetRole(p.role)
	b.SetProposal(proposal)
	b.SetLearning(p.state == StateLearning || p.state == StateForwarding)
	b.SetForwarding(p.state == StateForwarding)
	if !t.topologyChangeTimer.IsZero() && t.topologyChangeTimer.After(now) {
		b.SetTopologyChange(true)
	}

	// Only the CIST drives emission (see recomputeAll), so this is also the
	// one place that attaches the region's configuration identifier and every
	// instance's MSTI record. t is always the CIST here. The MST shape is
	// version 3, so it is withheld on a port that has migrated to legacy STP
	// (sendRSTP false, which already forced Version 0 and Configuration
	// above): bpdu.Encode picks the MST shape whenever ConfigID is set regardless
	// of Version and Type, and a legacy peer needs a Configuration BPDU, not
	// version 3.
	if l.mst != nil && sendRSTP {
		cid := *l.configID
		b.ConfigID = &cid
		b.RegionalRootID = t.regionalRootID
		b.InternalRootPathCost = t.internalRootPathCost
		b.RemainingHops = l.instanceRemainingHops(t)
		b.MSTIs = l.gatherMSTIRecords(now, p)
	}

	return b
}

func (l *Layer) makeAgreementBPDU(t *tree, p *portState, now time.Time) bpdu.BPDU {
	b := l.makeBPDU(t, p, now, false)
	b.SetRole(p.role)
	if l.link(p.name).sendRSTP {
		b.SetAgreement(true)
	}
	b.SetProposal(false)
	b.SetLearning(p.state == StateLearning || p.state == StateForwarding)
	b.SetForwarding(p.state == StateForwarding)

	return b
}
