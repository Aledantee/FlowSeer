package routing

import (
	"cmp"
	"net/netip"
	"slices"
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
// in the same Configured/Observed vocabulary [bridge.Origin] and [mcast.Origin] use for their own
// retained records. [Layer.Observe] reads it to refuse ever overwriting a configured binding,
// which is what keeps config.go's claim that "a static binding never ages out" true; a lookup
// otherwise answers the same for either origin. It is surfaced on [neighborSnapshot]'s trace fact
// but not on any exported type: a neighbor entry's lifetime is already the state machine's five
// states, not a second boolean, so there is no [Lifetime] type here to pair it with.
type neighborOrigin string

const (
	configured neighborOrigin = "configured"
	observed   neighborOrigin = "observed"
)

// heldEntry is one frame queued on an Incomplete neighbor entry: everything Route or Originate
// had already decided (egress interface, EtherType, and the header and payload before the hop
// limit decrement) except the destination link-layer address, which is what it is waiting on.
// pcp and dei are the ingress 802.1Q priority the frame arrived with: Route's closure captures
// them from the frame it was given, and Originate's closure leaves them at their zero value,
// since a self-originated datagram never arrived tagged in the first place.
type heldEntry struct {
	iface     string
	etherType ethernet.EtherType
	header    ip.Header
	payload   []byte
	pcp       vlan.PCP
	dei       bool
}

// HeldCause names how a frame left a neighbor's hold queue. The three values are exactly the
// exits the routing layer itself observes; what a caller then does with a released frame, and
// whether that succeeds, is the caller's own answer to report, not a fourth value here.
type HeldCause string

const (
	// HeldReleased indicates the neighbor resolved and the frame left with a destination MAC.
	HeldReleased HeldCause = "released"

	// HeldTimedOut indicates the entry's resolution deadline passed with the frame still queued.
	HeldTimedOut HeldCause = "timed-out"

	// HeldEvicted indicates [appendHeld] pushed the frame out of a full queue to make room.
	HeldEvicted HeldCause = "evicted"
)

// HeldFrame is one frame [Layer.Wake] reports leaving a neighbor entry's hold queue, with Cause
// naming how it left: under [HeldReleased] Frame is ready to leave by Interface, and under the
// other two causes Frame carries no resolved destination and exists to name what was held. Port
// is the egress port Interface resolves to: empty for a VLAN interface with no parent port,
// which has no single port until the bridge picks one, so a caller counting the exit against a
// port counts it against no port at all rather than inventing one, and the parent port for a
// sub-interface, which leaves by that port tagged. PCP and DEI are the ingress 802.1Q priority the held
// frame arrived with (zero for a self-originated frame), so a caller that releases it can carry
// the same priority the live, non-held path would have used.
type HeldFrame struct {
	Interface string
	Port      string
	Cause     HeldCause
	Frame     ethernet.Frame
	PCP       vlan.PCP
	DEI       bool
}

// neighborEntry is one row of a VRF's runtime neighbor table: the state it currently occupies,
// the bound link-layer address once one is known, the time the entry next changes on its own
// (a resolution deadline for Incomplete, a reachability deadline for Reachable, and zero for
// every other state), whether it came from static configuration or a live observation, the
// frames queued while the entry is Incomplete, and any frame [appendHeld] has since evicted from
// that queue but [Layer.Wake] has not yet reported.
type neighborEntry struct {
	state   NeighborState
	mac     netaddr.MAC
	expiry  time.Time
	origin  neighborOrigin
	queue   []heldEntry
	evicted []heldEntry
}

func (e *neighborEntry) clone() *neighborEntry {
	cp := *e
	cp.queue = append([]heldEntry(nil), e.queue...)
	cp.evicted = append([]heldEntry(nil), e.evicted...)
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
// (RFC 4861 section 7.2.2) and returning it as evicted rather than discarding it: a frame the
// hold queue pushes out is still reported, as a [HeldEvicted] exit in [Layer.Wake]'s next
// Effects, not silently dropped and not reported as a resolution that failed.
func appendHeld(queue []heldEntry, h heldEntry, depth int) (kept, evicted []heldEntry) {
	queue = append(queue, h)
	if len(queue) > depth {
		evicted = queue[:len(queue)-depth]
		queue = queue[len(queue)-depth:]
	}
	return queue, evicted
}

// neighborLookup is the outcome [vrfState.resolveNeighbor] found for one address: a resolved
// MAC ready to forward on, or the state that explains why not. origin is the zero value when no
// entry exists to have one.
type neighborLookup struct {
	mac    netaddr.MAC
	state  NeighborState
	origin neighborOrigin
	ok     bool
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
			origin: observed,
			expiry: now.Add(vs.policy.ResolutionTimeout),
		}
		var evicted []heldEntry
		entry.queue, evicted = appendHeld(entry.queue, held(), vs.policy.HoldDepth)
		entry.evicted = append(entry.evicted, evicted...)
		vs.neighbors[key] = entry
		return neighborLookup{state: NeighborIncomplete, origin: entry.origin}
	}

	switch entry.state {
	case NeighborReachable, NeighborStale:
		return neighborLookup{state: entry.state, mac: entry.mac, origin: entry.origin, ok: true}
	case NeighborFailed:
		return neighborLookup{state: NeighborFailed, origin: entry.origin}
	case NeighborIncomplete:
		if commit {
			var evicted []heldEntry
			entry.queue, evicted = appendHeld(entry.queue, held(), vs.policy.HoldDepth)
			entry.evicted = append(entry.evicted, evicted...)
		}
		return neighborLookup{state: NeighborIncomplete, origin: entry.origin}
	default:
		// A stored zero [NeighborState] is unreachable through any exported path today, but a
		// stored entry's zero value is legal Go, and [NeighborUnobserved] is meaningful only as
		// a lookup answer, never as something to hold frames for: naming it here rather than
		// folding it into the Incomplete arm above keeps a defect like that a miss, not a
		// silent, unbounded hold.
		return neighborLookup{state: NeighborUnobserved}
	}
}

// Observe applies the RFC 4861 section 7.2.5 rules for receipt of a Neighbor Advertisement to
// adv, which also serves an ARP reply or request mapped as the package README describes. If no
// entry exists for adv's interface and address, the advertisement is silently discarded per
// section 7.2.5: "There is no need to create an entry if none exists, since the recipient has
// apparently not initiated any communication with the target." A configured binding is likewise
// left alone: [New] installs it Reachable with no expiry so it never ages out, and an
// advertisement adopting its address or handing it an expiry would falsify that. Observe changes
// state alone; the frames an entry's transition frees or gives up on are reported by the next
// [Layer.Wake].
func (l *Layer) Observe(now time.Time, adv Advertisement) {
	vrfName, ok := l.ifaceVRF[adv.Interface]
	if !ok {
		return
	}
	vs := l.vrfs[vrfName]
	entry, ok := vs.neighbors[neighborKey{iface: adv.Interface, addr: adv.Addr}]
	if !ok || entry.origin == configured {
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

// Effects lists every frame a call to [Layer.Wake] observed leaving a hold queue, in the order
// Wake produced them, each carrying the [HeldCause] that says how it left. One slice rather than
// one per cause: the cause is on the element, so a reader that switches on it cannot silently
// inherit a meaning from whichever slice it happened to drain.
type Effects struct {
	Exits []HeldFrame
}

// finishHeld re-encodes h's header, already in the egress form the caller queued it in (Route's
// closure pre-decrements the hop limit; Originate's leaves it at 64, matching each one's direct,
// non-held path), and builds the Ethernet frame it leaves as (or, with a zero mac, the frame a
// failure step names). An encode error is unreachable: both callers encode the exact header
// they queue before queuing it, Route the hop-limit-decremented form and Originate the form
// unchanged, and refuse to queue at all when that encode fails, so this omits the frame rather
// than carry a spurious error path. cause is stamped onto the result so no reader downstream has
// to infer it from context.
func (l *Layer) finishHeld(h heldEntry, mac netaddr.MAC, cause HeldCause) (HeldFrame, bool) {
	newPayload, err := h.header.Encode(h.payload)
	if err != nil {
		return HeldFrame{}, false
	}
	return HeldFrame{
		Interface: h.iface,
		Port:      l.ifaces[h.iface].Port,
		Cause:     cause,
		Frame: ethernet.Frame{
			Src:       l.ifaces[h.iface].MAC,
			Dst:       mac,
			EtherType: h.etherType,
			Payload:   newPayload,
		},
		PCP: h.pcp,
		DEI: h.dei,
	}, true
}

// neighborTableEntry names one row Wake acts on, keyed for the total order below: a Go map
// iterates its VRFs and neighbors in random order, and Effects.Exits is an ordered slice the
// switch consumes in sequence, so Wake sorts before it acts rather than leaving emission order,
// Drain order, and the fabric's frame numbering to inherit map order. The order is by VRF name,
// then interface, then address.
type neighborTableEntry struct {
	vrfName string
	key     neighborKey
	entry   *neighborEntry
}

// Wake advances every VRF's neighbor table to now and reports every frame that leaves a hold
// queue as a result, in Effects.Exits: an Incomplete entry whose resolution deadline has passed
// becomes Failed and its held frames exit [HeldTimedOut]; an entry that already holds frames and
// is no longer Incomplete (because [Layer.Observe] resolved it since the previous Wake) has them
// exit [HeldReleased]. Either way the entry's queue is drained, so calling Wake again before
// anything else changes reports nothing further. A frame [appendHeld] evicted from a queue exits
// [HeldEvicted] unconditionally, on the first Wake after the eviction, regardless of the entry's
// current state or whether its queue currently holds anything.
func (l *Layer) Wake(now time.Time) Effects {
	var eff Effects

	var rows []neighborTableEntry
	for vrfName, vs := range l.vrfs {
		for key, entry := range vs.neighbors {
			rows = append(rows, neighborTableEntry{vrfName: vrfName, key: key, entry: entry})
		}
	}
	slices.SortFunc(rows, func(a, b neighborTableEntry) int {
		if c := cmp.Compare(a.vrfName, b.vrfName); c != 0 {
			return c
		}
		if c := cmp.Compare(a.key.iface, b.key.iface); c != 0 {
			return c
		}
		return a.key.addr.Compare(b.key.addr)
	})

	for _, row := range rows {
		entry := row.entry

		for _, h := range entry.evicted {
			if hf, ok := l.finishHeld(h, netaddr.MAC{}, HeldEvicted); ok {
				eff.Exits = append(eff.Exits, hf)
			}
		}
		entry.evicted = nil

		if entry.state == NeighborIncomplete {
			if entry.expiry.IsZero() || entry.expiry.After(now) {
				continue
			}
			for _, h := range entry.queue {
				if hf, ok := l.finishHeld(h, netaddr.MAC{}, HeldTimedOut); ok {
					eff.Exits = append(eff.Exits, hf)
				}
			}
			entry.state = NeighborFailed
			entry.expiry = time.Time{}
			entry.queue = nil
			continue
		}

		if len(entry.queue) == 0 {
			continue
		}
		for _, h := range entry.queue {
			if hf, ok := l.finishHeld(h, entry.mac, HeldReleased); ok {
				eff.Exits = append(eff.Exits, hf)
			}
		}
		entry.queue = nil
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
		byPort:   make(map[string]map[vlan.ID]string, len(l.byPort)),
		ifaceVRF: make(map[string]string, len(l.ifaceVRF)),
		ifaces:   make(map[string]Interface, len(l.ifaces)),
		vrfs:     make(map[string]*vrfState, len(l.vrfs)),
	}
	for k, v := range l.byVLAN {
		cp.byVLAN[k] = v
	}
	for portName, vids := range l.byPort {
		cpVIDs := make(map[vlan.ID]string, len(vids))
		for vid, iface := range vids {
			cpVIDs[vid] = iface
		}
		cp.byPort[portName] = cpVIDs
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

// DiscardHeld drops every neighbor entry's held-frame queue and any frame
// [appendHeld] has evicted from it but [Layer.Wake] has not yet reported,
// without changing the entry's state, expiry, or link-layer address. A frame
// in flight belongs to the run that queued it, not to configuration a later
// derive retains: a cloned layer that kept someone else's in-flight or
// evicted frames would make two forks compare unequal for a reason neither
// configuration shows, or report a frame from a run that never woke it.
//
// DiscardHeld leaves an Incomplete entry's state and expiry untouched on
// purpose, even though emptying its queue looks like settling it: [Layer.Wake]
// alone decides when a resolution deadline passing moves the entry to Failed,
// and it does that whether or not the queue it drains is empty. Settling the
// entry here as well would just duplicate that decision under a second name.
func (l *Layer) DiscardHeld() {
	for _, vs := range l.vrfs {
		for _, entry := range vs.neighbors {
			entry.queue = nil
			entry.evicted = nil
		}
	}
}

// NeighborEntry is one neighbor table entry reported by a layer snapshot.
type NeighborEntry struct {
	VRF       string
	Interface string
	Addr      netip.Addr
	MAC       netaddr.MAC
	State     NeighborState
	HoldDepth int
}

// Neighbors returns a snapshot of every neighbor table entry across all VRFs,
// ordered canonically by VRF name, then interface name, then address.
func (l *Layer) Neighbors() []NeighborEntry {
	var rows []neighborTableEntry
	for vrfName, vs := range l.vrfs {
		for key, entry := range vs.neighbors {
			rows = append(rows, neighborTableEntry{vrfName: vrfName, key: key, entry: entry})
		}
	}
	slices.SortFunc(rows, func(a, b neighborTableEntry) int {
		if c := cmp.Compare(a.vrfName, b.vrfName); c != 0 {
			return c
		}
		if c := cmp.Compare(a.key.iface, b.key.iface); c != 0 {
			return c
		}
		return a.key.addr.Compare(b.key.addr)
	})

	entries := make([]NeighborEntry, len(rows))
	for i, r := range rows {
		entries[i] = NeighborEntry{
			VRF:       r.vrfName,
			Interface: r.key.iface,
			Addr:      r.key.addr,
			MAC:       r.entry.mac,
			State:     r.entry.state,
			HoldDepth: len(r.entry.queue),
		}
	}
	return entries
}
