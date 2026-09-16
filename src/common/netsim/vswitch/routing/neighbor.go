package routing

import (
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// NeighborState names where one neighbor table entry sits in the state machine netsim runs
// for both ARP and Neighbor Discovery bindings alike, RFC 4861 section 7.3.2's set mapped onto
// one vocabulary rather than a second one invented for ARP. Delay and Probe are deliberately
// absent: both exist only to schedule a unicast solicitation, and netsim never solicits, so no
// input reaches either one. See the package README's "Neighbor lifecycle" section.
type NeighborState string

const (
	// NeighborUnobserved is the zero value: no entry exists for the address, because nothing
	// has been routed to it and it was not configured.
	NeighborUnobserved NeighborState = ""

	// NeighborIncomplete is an entry with no known link-layer address; resolution is in
	// progress and frames addressed to it are held, bounded by the VRF's neighbor policy.
	NeighborIncomplete NeighborState = "incomplete"

	// NeighborReachable is an entry confirmed within the policy's ReachableTime.
	NeighborReachable NeighborState = "reachable"

	// NeighborStale is an entry whose link-layer address is trusted but whose reachability is
	// unverified. Netsim forwards on it without probing and leaves it Stale; see the package
	// README for why.
	NeighborStale NeighborState = "stale"

	// NeighborFailed is an entry that reached Incomplete and observed nothing within the
	// policy's ResolutionTimeout. Netsim never solicits, so Failed names a timeout, never an
	// exhausted retransmission count.
	NeighborFailed NeighborState = "failed"
)

// neighborOrigin distinguishes a statically configured binding from one learned by observation,
// which matters only for [neighborEntry.clone]'s bookkeeping today; both answer lookups alike.
type neighborOrigin string

const (
	originConfigured neighborOrigin = "configured"
	originObserved   neighborOrigin = "observed"
)

// heldEntry is one frame queued on an Incomplete neighbor entry: everything Route or Originate
// had already decided (egress interface, EtherType, and the header and payload before the hop
// limit decrement) except the destination link-layer address, which is what it is waiting on.
type heldEntry struct {
	iface     string
	etherType ethernet.EtherType
	header    ip.Header
	payload   []byte
}

// HeldFrame is one frame [Layer.Wake] reports on for a neighbor entry it acted on: released
// with a frame ready to leave by Interface, or failed because resolution never completed, in
// which case Frame carries no resolved destination and exists to name what was held.
type HeldFrame struct {
	Interface string
	Frame     ethernet.Frame
}

// neighborEntry is one row of a VRF's runtime neighbor table: the state it currently occupies,
// the bound link-layer address once one is known, the time the entry next changes on its own
// (a resolution deadline for Incomplete, a reachability deadline for Reachable, and zero for
// every other state), whether it came from static configuration or a live observation, and the
// frames queued while the entry is Incomplete.
type neighborEntry struct {
	state  NeighborState
	mac    netaddr.MAC
	expiry time.Time
	origin neighborOrigin
	queue  []heldEntry
}

func (e *neighborEntry) clone() *neighborEntry {
	cp := *e
	cp.queue = append([]heldEntry(nil), e.queue...)
	return &cp
}

// Advertisement is a family-neutral record of one observed link-layer address binding, decoded
// from an ARP reply or request or a Neighbor Advertisement or Solicitation. Solicited, Override,
// and Router carry the RFC 4861 section 7.2.5 flags directly; an ARP reply maps to
// {Solicited: true, Override: true, Router: false} and an ARP request's sender fields map to
// {Solicited: false, Override: true, Router: false} — see the package README for why.
type Advertisement struct {
	Interface string
	Addr      netip.Addr
	MAC       netaddr.MAC
	HasMAC    bool
	Solicited bool
	Override  bool
	Router    bool
}

// appendHeld appends h to queue, dropping the oldest entry once the queue exceeds depth
// (RFC 4861 section 7.2.2).
func appendHeld(queue []heldEntry, h heldEntry, depth int) []heldEntry {
	queue = append(queue, h)
	if len(queue) > depth {
		queue = queue[len(queue)-depth:]
	}
	return queue
}

// neighborLookup is the outcome [vrfState.resolveNeighbor] found for one address: a resolved
// MAC ready to forward on, or the state that explains why not.
type neighborLookup struct {
	mac   netaddr.MAC
	state NeighborState
	ok    bool
}

// resolveNeighbor looks up key in the VRF's neighbor table and applies the commit-gated
// pending-or-miss rule: under [NeighborObserved], a miss with commit set creates an Incomplete
// entry and queues held(), and with commit clear reports pending without changing anything.
// held is called at most once, and only when a frame is actually queued.
func (vs *vrfState) resolveNeighbor(now time.Time, key neighborKey, commit bool, held func() heldEntry) neighborLookup {
	entry, ok := vs.neighbors[key]
	if !ok {
		if vs.policy.Mode != NeighborObserved {
			return neighborLookup{state: NeighborUnobserved}
		}
		if !commit {
			return neighborLookup{state: NeighborIncomplete}
		}
		entry = &neighborEntry{
			state:  NeighborIncomplete,
			origin: originObserved,
			expiry: now.Add(vs.policy.ResolutionTimeout),
		}
		entry.queue = appendHeld(entry.queue, held(), vs.policy.HoldDepth)
		vs.neighbors[key] = entry
		return neighborLookup{state: NeighborIncomplete}
	}

	switch entry.state {
	case NeighborReachable, NeighborStale:
		return neighborLookup{state: entry.state, mac: entry.mac, ok: true}
	case NeighborFailed:
		return neighborLookup{state: NeighborFailed}
	default: // NeighborIncomplete
		if commit {
			entry.queue = appendHeld(entry.queue, held(), vs.policy.HoldDepth)
		}
		return neighborLookup{state: NeighborIncomplete}
	}
}

// Observe applies the RFC 4861 section 7.2.5 rules for receipt of a Neighbor Advertisement to
// adv, which also serves an ARP reply or request mapped as the package README describes. If no
// entry exists for adv's interface and address, the advertisement is silently discarded per
// section 7.2.5: "There is no need to create an entry if none exists, since the recipient has
// apparently not initiated any communication with the target." Observe changes state alone; the
// frames an entry's transition frees or gives up on are reported by the next [Layer.Wake].
func (l *Layer) Observe(now time.Time, adv Advertisement) {
	vrfName, ok := l.ifaceVRF[adv.Interface]
	if !ok {
		return
	}
	vs := l.vrfs[vrfName]
	entry, ok := vs.neighbors[neighborKey{iface: adv.Interface, addr: adv.Addr}]
	if !ok {
		return
	}

	if entry.state == NeighborIncomplete {
		if !adv.HasMAC {
			// "If the link layer has addresses and no Target Link-Layer Address option is
			// included, the receiving node SHOULD silently discard the received
			// advertisement." Netsim's link layer always has addresses.
			return
		}
		entry.mac = adv.MAC
		if adv.Solicited {
			entry.state = NeighborReachable
			entry.expiry = now.Add(vs.policy.ReachableTime)
		} else {
			entry.state = NeighborStale
			entry.expiry = time.Time{}
		}
		// The Override flag is ignored in the INCOMPLETE case (RFC 4861 section 7.2.5); the
		// entry's held frames are released by the next Wake, not here.
		return
	}

	macDiffers := adv.HasMAC && adv.MAC != entry.mac
	if !adv.Override && macDiffers {
		// Rule I: Override clear and the supplied address differs from the cached one.
		if entry.state == NeighborReachable {
			entry.state = NeighborStale
			entry.expiry = time.Time{}
		}
		return
	}

	// Rule II: Override set, the supplied address matches the cache, or none was supplied.
	updated := false
	if adv.HasMAC && adv.MAC != entry.mac {
		entry.mac = adv.MAC
		updated = true
	}
	switch {
	case adv.Solicited:
		entry.state = NeighborReachable
		entry.expiry = now.Add(vs.policy.ReachableTime)
	case updated:
		entry.state = NeighborStale
		entry.expiry = time.Time{}
	}
}

// Age applies RFC 4861 section 7.3.2 reachability expiry: a Reachable entry whose expiry is not
// after now moves to Stale. It is the only place aging mutates the stored state; Route and
// Originate read whatever state Age last left, so a Reachable entry that outlives its
// ReachableTime keeps forwarding as Reachable until a caller ages the layer.
func (l *Layer) Age(now time.Time) {
	for _, vs := range l.vrfs {
		for _, entry := range vs.neighbors {
			if entry.state == NeighborReachable && !entry.expiry.IsZero() && !entry.expiry.After(now) {
				entry.state = NeighborStale
				entry.expiry = time.Time{}
			}
		}
	}
}

// Effects lists the frames a call to [Layer.Wake] released onto the wire and the frames it gave
// up on, matching the shape of [go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag.Effects].
type Effects struct {
	Released []HeldFrame
	Failed   []HeldFrame
}

// finishHeld decrements h's hop limit, re-encodes its header and payload, and builds the
// Ethernet frame it leaves as (or, with a zero mac, the frame a failure step names). An encode
// error is unreachable in practice: h's header decoded successfully moments before it was
// queued and only its hop limit changes here, so this silently omits the frame rather than
// carry a spurious error path.
func (l *Layer) finishHeld(h heldEntry, mac netaddr.MAC) (HeldFrame, bool) {
	newHdr := h.header
	newHdr.HopLimit--
	newPayload, err := newHdr.Encode(h.payload)
	if err != nil {
		return HeldFrame{}, false
	}
	return HeldFrame{
		Interface: h.iface,
		Frame: ethernet.Frame{
			Src:       l.ifaces[h.iface].MAC,
			Dst:       mac,
			EtherType: h.etherType,
			Payload:   newPayload,
		},
	}, true
}

// Wake advances every VRF's neighbor table to now: an Incomplete entry whose resolution
// deadline has passed becomes Failed, and its held frames are reported in Effects.Failed; any
// entry that already holds frames and is no longer Incomplete (because [Layer.Observe] resolved
// it since the previous Wake) has them reported in Effects.Released. Either way the entry's
// queue is drained, so calling Wake again before anything else changes reports nothing further.
func (l *Layer) Wake(now time.Time) Effects {
	var eff Effects
	for _, vs := range l.vrfs {
		for _, entry := range vs.neighbors {
			if len(entry.queue) == 0 {
				continue
			}

			if entry.state == NeighborIncomplete {
				if entry.expiry.IsZero() || entry.expiry.After(now) {
					continue
				}
				for _, h := range entry.queue {
					if hf, ok := l.finishHeld(h, netaddr.MAC{}); ok {
						eff.Failed = append(eff.Failed, hf)
					}
				}
				entry.state = NeighborFailed
				entry.expiry = time.Time{}
				entry.queue = nil
				continue
			}

			for _, h := range entry.queue {
				if hf, ok := l.finishHeld(h, entry.mac); ok {
					eff.Released = append(eff.Released, hf)
				}
			}
			entry.queue = nil
		}
	}
	return eff
}

// NextWake returns the earliest resolution deadline among the layer's Incomplete neighbor
// entries, and reports whether any is pending.
func (l *Layer) NextWake() (time.Time, bool) {
	var (
		earliest time.Time
		hasTimer bool
	)
	for _, vs := range l.vrfs {
		for _, entry := range vs.neighbors {
			if entry.state != NeighborIncomplete || entry.expiry.IsZero() {
				continue
			}
			if !hasTimer || entry.expiry.Before(earliest) {
				earliest = entry.expiry
				hasTimer = true
			}
		}
	}
	return earliest, hasTimer
}

// Clone returns a deep copy of the layer, including its runtime neighbor table. Held frames
// travel with it; a fork that never calls Wake before diverging keeps the same pending queues
// the original had at the moment of the clone.
func (l *Layer) Clone() *Layer {
	cp := &Layer{
		nodeID:   l.nodeID,
		byVLAN:   make(map[vlan.ID]string, len(l.byVLAN)),
		byPort:   make(map[string]string, len(l.byPort)),
		ifaceVRF: make(map[string]string, len(l.ifaceVRF)),
		ifaces:   make(map[string]Interface, len(l.ifaces)),
		vrfs:     make(map[string]*vrfState, len(l.vrfs)),
	}
	for k, v := range l.byVLAN {
		cp.byVLAN[k] = v
	}
	for k, v := range l.byPort {
		cp.byPort[k] = v
	}
	for k, v := range l.ifaceVRF {
		cp.ifaceVRF[k] = v
	}
	for k, v := range l.ifaces {
		cp.ifaces[k] = Interface{VLAN: v.VLAN, Port: v.Port, MAC: v.MAC, Prefixes: append([]netip.Prefix(nil), v.Prefixes...)}
	}
	for vrfName, vs := range l.vrfs {
		cp.vrfs[vrfName] = vs.clone()
	}
	return cp
}
