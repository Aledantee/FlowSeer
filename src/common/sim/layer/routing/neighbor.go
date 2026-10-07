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
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// NeighborState is the RFC 4861 section 7.3.2 state of an observed binding.
// ARP uses the same lifecycle. The package README states its limits.
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

	// NeighborStale has a cached address whose reachability needs confirmation.
	NeighborStale NeighborState = "stale"

	// NeighborDelay forwards on the cached address for five seconds before probing.
	NeighborDelay NeighborState = "delay"

	// NeighborProbe forwards while unicast solicitations seek confirmation.
	NeighborProbe NeighborState = "probe"
)

// neighborOrigin distinguishes a statically configured binding from one learned by observation,
// in the same Configured/Observed vocabulary [bridge.Origin] and [mcast.Origin] use for their own
// retained records. [Layer.Receive] reads it to refuse ever overwriting a configured binding,
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
	token     HoldToken
	ingress   string
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

// HoldToken identifies a queued frame for the lifetime of one layer. Zero means no hold.
// A token is a value and is safe for concurrent use.
type HoldToken uint64

// HeldFrame is one frame [Layer.DrainExits] reports leaving a neighbor entry's hold queue, with Cause
// naming how it left: under [HeldReleased] Frame is ready to leave by Interface, and under the
// other two causes Frame carries no resolved destination and exists to name what was held. Port
// is the egress port Interface resolves to: empty for a VLAN interface with no parent port,
// which has no single port until the bridge picks one, so a caller counting the exit against a
// port counts it against no port at all rather than inventing one, and the parent port for a
// sub-interface, which leaves by that port tagged. PCP and DEI are the ingress 802.1Q priority the held
// frame arrived with (zero for a self-originated frame), so a caller that releases it can carry
// the same priority the live, non-held path would have used.
type HeldFrame struct {
	// Token is the token returned when the frame was queued.
	Token HoldToken
	// Ingress is Route's interface, or empty for an originated frame, on every cause.
	Ingress   string
	Interface string
	Port      string
	Cause     HeldCause
	Frame     ethernet.Frame
	PCP       vlan.PCP
	DEI       bool
}

// neighborEntry holds a binding, its next transition time, the number of
// solicitations in the current resolution or probe cycle, and queued frames.
type neighborEntry struct {
	state   NeighborState
	mac     netaddr.MAC
	sent    int
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

// ARP has no solicited or override flags. A reply confirms reachability,
// while a request supplies an unsolicited override binding (RFC 826).
type advertisement struct {
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
// hold queue pushes out is still reported, as a [HeldEvicted] exit in [Layer.DrainExits] after the next
// [Layer.Advance], not silently dropped and not reported as a resolution that failed.
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
	mac       netaddr.MAC
	state     NeighborState
	origin    neighborOrigin
	emissions []layer.Emission
	ok        bool
}

// resolveNeighbor judges the entry at now without writing when commit is clear.
// held is called only when the lookup queues a frame.
func (vs *vrfState) resolveNeighbor(now time.Time, key neighborKey, iface Interface, commit bool, held func() heldEntry) neighborLookup {
	entry, ok := vs.neighbors[key]
	if !ok {
		if vs.policy.Mode != NeighborObserved {
			return neighborLookup{state: NeighborUnobserved}
		}
		em, canSolicit := solicitation(iface, key.addr, netaddr.MAC{}, false)
		if !canSolicit {
			return neighborLookup{state: NeighborUnobserved}
		}
		if !commit {
			return neighborLookup{state: NeighborIncomplete}
		}
		entry = &neighborEntry{state: NeighborIncomplete, origin: observed, sent: 1, expiry: now.Add(vs.policy.RetransTimer)}
		entry.queue, entry.evicted = appendHeld(entry.queue, held(), vs.policy.HoldDepth)
		vs.neighbors[key] = entry
		return neighborLookup{state: NeighborIncomplete, origin: entry.origin, emissions: []layer.Emission{em}}
	}

	state := entry.state
	if entry.origin == observed && state == NeighborReachable && !entry.expiry.IsZero() && !entry.expiry.After(now) {
		state = NeighborStale
	}
	switch state {
	case NeighborReachable, NeighborStale, NeighborDelay, NeighborProbe:
		if commit && entry.origin == observed && state == NeighborStale {
			entry.state = NeighborDelay
			entry.expiry = now.Add(5 * time.Second)
		}
		return neighborLookup{state: state, mac: entry.mac, origin: entry.origin, ok: true}
	case NeighborIncomplete:
		if commit {
			var evicted []heldEntry
			entry.queue, evicted = appendHeld(entry.queue, held(), vs.policy.HoldDepth)
			entry.evicted = append(entry.evicted, evicted...)
		}
		return neighborLookup{state: state, origin: entry.origin}
	default:
		return neighborLookup{state: NeighborUnobserved}
	}
}

// observe applies the advertisement rules of RFC 4861 section 7.2.5.
func (l *Layer) observe(now time.Time, adv advertisement) {
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
		// entry's held frames are released by [Layer.DrainExits] after the next [Layer.Advance], not here.
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

// Advance returns due solicitations and probes, at most one per neighbor at now.
// Each next transmission is scheduled one RetransTimer after now. Failed entries
// are deleted and held frames wait for DrainExits, in VRF/interface/address order.
func (l *Layer) Advance(now time.Time) layer.Effects {
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
	var fx layer.Effects
	for _, row := range rows {
		entry := row.entry
		vs := l.vrfs[row.vrfName]
		for _, h := range entry.evicted {
			if hf, ok := l.finishHeld(h, netaddr.MAC{}, HeldEvicted); ok {
				l.exits = append(l.exits, hf)
			}
		}
		entry.evicted = nil
		if entry.origin == observed && !entry.expiry.IsZero() && !entry.expiry.After(now) {
			switch entry.state {
			case NeighborReachable:
				entry.state = NeighborStale
				entry.expiry = time.Time{}
			case NeighborIncomplete, NeighborDelay, NeighborProbe:
				if entry.state == NeighborDelay {
					entry.state = NeighborProbe
					entry.sent = 0
				}
				if entry.sent >= 3 {
					for _, h := range entry.queue {
						if hf, ok := l.finishHeld(h, netaddr.MAC{}, HeldTimedOut); ok {
							l.exits = append(l.exits, hf)
						}
					}
					delete(vs.neighbors, row.key)
					continue
				}
				em, ok := solicitation(l.ifaces[row.key.iface], row.key.addr, entry.mac, entry.state == NeighborProbe)
				if ok {
					fx.Emissions = append(fx.Emissions, em)
				}
				entry.sent++
				entry.expiry = now.Add(vs.policy.RetransTimer)
			}
		}
		if entry.state == NeighborIncomplete {
			continue
		}
		for _, h := range entry.queue {
			if hf, ok := l.finishHeld(h, entry.mac, HeldReleased); ok {
				l.exits = append(l.exits, hf)
			}
		}
		entry.queue = nil
	}
	return fx
}

// DrainExits returns and clears any hold-queue exits waiting in the layer.
func (l *Layer) DrainExits() []HeldFrame {
	exits := l.exits
	l.exits = nil
	return exits
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
		Token:     h.token,
		Ingress:   h.ingress,
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

// neighborTableEntry names one row Advance acts on, keyed for the total order below: a Go map
// iterates its VRFs and neighbors in random order, and hold-queue exits are ordered for sequence
// consumption. The order is by VRF name, then interface, then address.
type neighborTableEntry struct {
	vrfName string
	key     neighborKey
	entry   *neighborEntry
}

// NextWake reports the earliest Incomplete, Delay, or Probe transmission time.
func (l *Layer) NextWake() (time.Time, bool) {
	var (
		earliest time.Time
		hasTimer bool
	)
	for _, vs := range l.vrfs {
		for _, entry := range vs.neighbors {
			if (entry.state != NeighborIncomplete && entry.state != NeighborDelay && entry.state != NeighborProbe) || entry.expiry.IsZero() {
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
// travel with it; a fork that never calls Advance before diverging keeps the same pending queues
// the original had at the moment of the clone.
func (l *Layer) Clone() *Layer {
	cp := &Layer{
		nodeID:   l.nodeID,
		byVLAN:   make(map[vlan.ID]string, len(l.byVLAN)),
		byPort:   make(map[string]map[vlan.ID]string, len(l.byPort)),
		ifaceVRF: make(map[string]string, len(l.ifaceVRF)),
		ifaces:   make(map[string]Interface, len(l.ifaces)),
		vrfs:     make(map[string]*vrfState, len(l.vrfs)),
		exits:    slices.Clone(l.exits),
		lastHold: l.lastHold,
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

// FailHeld drains every held frame across all VRFs with cause HeldTimedOut,
// deletes Incomplete entries, and returns the resulting exits,
// with any waiting exit first.
func (l *Layer) FailHeld() []HeldFrame {
	exits := l.exits
	l.exits = nil
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
				exits = append(exits, hf)
			}
		}
		entry.evicted = nil

		for _, h := range entry.queue {
			if hf, ok := l.finishHeld(h, netaddr.MAC{}, HeldTimedOut); ok {
				exits = append(exits, hf)
			}
		}
		if entry.state == NeighborIncomplete {
			delete(l.vrfs[row.vrfName].neighbors, row.key)
		}
		entry.queue = nil
	}
	return exits
}
