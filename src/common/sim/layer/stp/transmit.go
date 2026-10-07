package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// transmit runs the one transmit pass at the end of a state transition. The
// pass walks trees before ports, so every tree has settled before a frame is
// built from the port's final state.
func (l *Layer) transmit(now time.Time, emissions *[]layer.Emission) {
	for _, id := range l.treeOrder {
		if l.pvst == nil && id != cistID {
			continue
		}
		t := l.trees[id]
		for _, name := range l.portNames {
			p := t.ports[name]
			link := l.links[name]
			tx := l.tx(t, name)
			if !link.up || link.bpduGuardDisabled {
				tx.count = 0
				tx.tick = time.Time{}
				tx.helloWhen = time.Time{}

				continue
			}

			l.advanceTransmitCount(tx, now)
			l.setHelloRequests(t, p, tx, now)
			if !l.transmitRequested(p, tx) || tx.count >= int(l.txHoldCount) {
				continue
			}

			msg, ok := l.transmitBPDU(t, p, now)
			if !ok {
				continue
			}

			built, err := l.frames(t, p, msg)
			if err != nil {
				continue
			}
			if len(built) == 0 {
				continue
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
			tx.helloWhen = now.Add(l.helloTime)
			tx.newInfo = false
			if msg.Type == bpdu.TypeRapid {
				tx.newInfoMsti = false
			}
		}
	}
}

func (l *Layer) settleHelloTimers(now time.Time) {
	for _, id := range l.treeOrder {
		if l.pvst == nil && id != cistID {
			continue
		}
		for _, name := range l.portNames {
			tx := l.tx(l.trees[id], name)
			for !tx.helloWhen.IsZero() && tx.helloWhen.Before(now) {
				tx.helloWhen = tx.helloWhen.Add(l.helloTime)
			}
		}
	}
}

func (l *Layer) advanceTransmitCount(tx *portTx, now time.Time) {
	for tx.count > 0 && !tx.tick.After(now) {
		tx.count--
		tx.tick = tx.tick.Add(time.Second)
	}
	if tx.count == 0 {
		tx.tick = time.Time{}
	}
}

func (l *Layer) setHelloRequests(t *tree, p *portState, tx *portTx, now time.Time) {
	if tx.helloWhen.IsZero() || tx.helloWhen.After(now) {
		return
	}

	if t.id == cistID || l.pvst != nil {
		if p.role == bpdu.RoleDesignated || (p.role == bpdu.RoleRoot && activeAt(p.tcWhile, now)) {
			tx.newInfo = true
		}
	}
	if l.pvst == nil && t.id == cistID {
		for _, id := range l.treeOrder {
			if id == cistID {
				continue
			}
			mp := l.trees[id].ports[p.name]
			if mp.role == bpdu.RoleDesignated || (mp.role == bpdu.RoleRoot && activeAt(mp.tcWhile, now)) {
				tx.newInfoMsti = true
			}
		}
	}
	tx.helloWhen = now.Add(l.helloTime)
}

func activeAt(when, now time.Time) bool {
	return !when.IsZero() && when.After(now)
}

func (l *Layer) transmitRequested(p *portState, tx *portTx) bool {
	if tx.newInfo {
		return true
	}
	if !tx.newInfoMsti || !l.links[p.name].sendRSTP {
		return false
	}
	if l.pvst != nil || !l.mstiMasterPort(p.name) {
		return true
	}

	return false
}

func (l *Layer) mstiMasterPort(name string) bool {
	if l.mst == nil || !l.links[name].external {
		return false
	}

	return l.cist().ports[name].role == bpdu.RoleRoot
}

func (l *Layer) transmitBPDU(t *tree, p *portState, now time.Time) (bpdu.BPDU, bool) {
	link := l.links[p.name]
	if link.sendRSTP {
		return l.makeBPDU(t, p, now), true
	}
	switch p.role {
	case bpdu.RoleRoot:
		return bpdu.BPDU{Version: 0, Type: bpdu.TypeTopologyChangeNotification}, true
	case bpdu.RoleDesignated:
		return l.makeBPDU(t, p, now), true
	default:
		return bpdu.BPDU{}, false
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
// mode that is the single IEEE-addressed frame, untagged. In PVST mode a
// non-CIST tree always sends its SSTP frame on its own VLAN. VLAN 1's tree
// sends an SSTP frame while the port sends RSTP, plus the IEEE-addressed frame
// the neighboring RSTP or MSTP bridge converges with. A migrated VLAN 1 tree
// sends only the IEEE-addressed frame. The two VLAN 1 frames are one
// transmission and spend one budget slot between them.
func (l *Layer) frames(t *tree, p *portState, b bpdu.BPDU) ([]taggedFrame, error) {
	if l.pvst == nil {
		frame, err := bpdu.Encode(b, l.address)
		if err != nil {
			return nil, err
		}

		return []taggedFrame{{frame: frame}}, nil
	}

	var built []taggedFrame
	link := l.links[p.name]
	if t.id != cistID || link.sendRSTP {
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

// edgeDelay is the time without a BPDU after which a port may be detected as
// an edge: migrateTime on a point-to-point link, the max age in force on a
// shared one.
func (l *Layer) edgeDelay(t *tree, link *linkRecord) time.Duration {
	if link.pointToPoint {
		return migrateTime
	}
	maxAge, _, _ := l.times(t)

	return maxAge
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
	link := l.links[p.name]

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
		if link.pointToPoint && link.sendRSTP && mp.role == bpdu.RoleDesignated && mp.state == StateDiscarding && !mp.agreed {
			flags.SetProposal(true)
		}
		if link.pointToPoint && link.sendRSTP && (mp.role == bpdu.RoleRoot || mp.role == bpdu.RoleAlternate) && l.isSynced(mt, p.name) {
			flags.SetAgreement(true)
		}
		flags.SetLearning(mp.state == StateLearning || mp.state == StateForwarding)
		flags.SetForwarding(mp.state == StateForwarding)
		if !mp.tcWhile.IsZero() && mp.tcWhile.After(now) {
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

func (l *Layer) makeBPDU(t *tree, p *portState, now time.Time) bpdu.BPDU {
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

	link := l.links[p.name]
	if !link.sendRSTP {
		b.Version = 0
		b.Type = bpdu.TypeConfiguration
	} else {
		b.Version = 2
		b.Type = bpdu.TypeRapid
		p.tcAck = false
	}

	b.SetRole(p.role)
	b.SetProposal(link.pointToPoint && link.sendRSTP && p.role == bpdu.RoleDesignated && p.state == StateDiscarding && !p.agreed)
	b.SetAgreement(link.pointToPoint && link.sendRSTP && (p.role == bpdu.RoleRoot || p.role == bpdu.RoleAlternate) && l.isSynced(t, p.name))
	b.SetLearning(p.state == StateLearning || p.state == StateForwarding)
	b.SetForwarding(p.state == StateForwarding)
	if !p.tcWhile.IsZero() && p.tcWhile.After(now) {
		b.SetTopologyChange(true)
	}
	if p.tcAck && !link.sendRSTP {
		b.SetTopologyChangeAck(true)
		p.tcAck = false
	}

	// Under MSTP only the CIST drives transmission, so this attaches the
	// region's configuration identifier and every instance's MSTI record.
	// PVST builds a separate BPDU for each VLAN and never enters this branch.
	// The MST shape is version 3, so it is withheld on a port that has migrated to legacy STP
	// (sendRSTP false, which already forced Version 0 and Configuration
	// above): bpdu.Encode picks the MST shape whenever ConfigID is set regardless
	// of Version and Type, and a legacy peer needs a Configuration BPDU, not
	// version 3.
	if l.mst != nil && link.sendRSTP {
		cid := *l.configID
		b.ConfigID = &cid
		b.RegionalRootID = t.regionalRootID
		b.InternalRootPathCost = t.internalRootPathCost
		b.RemainingHops = l.instanceRemainingHops(t)
		b.MSTIs = l.gatherMSTIRecords(now, p)
	}

	return b
}
