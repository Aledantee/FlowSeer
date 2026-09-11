// Package bridge implements an Ethernet transparent bridge with optional IEEE 802.1Q VLAN awareness.
package bridge

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Gate decides whether a port learns addresses and forwards frames.
type Gate interface {
	Learns(port string) bool
	Forwards(port string) bool
}

// Bridge simulates an Ethernet transparent bridge with optional IEEE 802.1Q VLAN awareness.
//
// A Bridge is not safe for concurrent use.
type Bridge struct {
	cfg       Config
	ports     port.Table
	agingTime time.Duration
	fdb       map[fdbKey]Entry
	gate      Gate
	counters  Counters
}

// Counters returns a snapshot of the forwarding database lifecycle counters.
func (b *Bridge) Counters() Counters {
	return b.counters
}

// New constructs a [Bridge] with the provided configuration and port table.
func New(cfg Config, ports port.Table) *Bridge {
	aging := effectiveAgingTime(cfg.AgingTime)

	clonedCfg := cfg.Clone()
	if clonedCfg.VLAN != nil {
		for name, sw := range clonedCfg.VLAN.Switchports {
			slices.Sort(sw.Tagged)
			slices.Sort(sw.Untagged)
			clonedCfg.VLAN.Switchports[name] = sw
		}
	}

	return &Bridge{
		cfg:       clonedCfg,
		ports:     ports.Clone(),
		agingTime: aging,
		fdb:       make(map[fdbKey]Entry),
	}
}

// Validate verifies the invariants of the bridge configuration against the given port table.
func (b *Bridge) Validate(ports port.Table) error {
	return b.cfg.Validate(ports)
}

// SetGate installs g as the bridge forwarding and learning gate.
// A nil gate allows every port to learn and forward.
func (b *Bridge) SetGate(g Gate) {
	b.gate = g
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
			}
		}
	}
}

// SetOperStatus updates the operational link state of the named port in the bridge's port table.
func (b *Bridge) SetOperStatus(portName string, state port.LinkState) {
	builder := port.NewBuilder()
	for _, p := range b.ports.Ports() {
		if p.Name == portName {
			p.OperStatus = state
		}
		builder.Add(p)
	}
	// Only OperStatus changed on a table that already validated, so the
	// rebuild cannot fail.
	if tbl, err := builder.Build(); err == nil {
		b.ports = tbl
	}
}

// Learn preloads the forwarding database with the provided seeds. A seed naming a
// LAG member is stored under the LAG, as the relay learns it, so a later lookup
// treats the aggregation as one port. A seed with a group address is ignored,
// since the relay never learns one and the export could not carry it.
// Non-static seeds count as learned and are bounded by MaxEntries, evicting the
// oldest dynamic entry when the table exceeds the bound. Static seeds count nothing.
func (b *Bridge) Learn(seeds []Seed) {
	for _, s := range seeds {
		if s.MAC.IsGroup() {
			continue
		}
		if p, ok := b.ports.Resolve(s.Port); ok {
			s.Port = p.Name
		}
		key := fdbKey{fid: s.FID, mac: s.MAC}
		if s.Static {
			b.fdb[key] = Entry(s)
			continue
		}

		existing, exists := b.fdb[key]
		if !exists || existing.Static {
			if b.cfg.MaxEntries > 0 && b.dynamicCount() >= b.cfg.MaxEntries {
				b.evictOldestDynamic()
			}
			b.fdb[key] = Entry(s)
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
	if _, exists := b.fdb[key]; exists {
		delete(b.fdb, key)
		return true
	}

	return false
}

// Age removes dynamic forwarding database entries older than the configured aging time relative to now.
func (b *Bridge) Age(now time.Time) {
	for key, e := range b.fdb {
		if !e.Static && now.Sub(e.LearnedAt) > b.agingTime {
			delete(b.fdb, key)
			b.counters.Expired++
		}
	}
}

func (b *Bridge) dynamicCount() int {
	count := 0
	for _, e := range b.fdb {
		if !e.Static {
			count++
		}
	}

	return count
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
	Port          string
	FID           vlan.ID
	PCP           vlan.PCP
	DEI           bool
	RemainingTags []vlan.Tag
	Steps         []trace.Step
}

// Ingress processes an incoming frame through port validation, IEEE reserved address checks,
// forwarding gate, VLAN classification, admission control, ingress filtering, and MAC learning.
// It returns false and a populated Result on any drop; on success it returns true and an Ingress
// descriptor for subsequent egress forwarding.
func (b *Bridge) Ingress(now time.Time, ingress string, f ethernet.Frame, learn bool) (Ingress, Result, bool) {
	var res Result
	res.Outcome = trace.Dropped

	inPort, reason := b.ports.Receive(ingress)
	if reason != "" {
		res.Reason = reason
		res.Ingress = inPort.Name
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpDrop,
			Detail: "ingress port down",
		})

		return Ingress{}, res, false
	}
	res.Ingress = inPort.Name

	if !b.cfg.ForwardBPDU && ethernet.IsReserved(f.Dst) {
		res.Reason = ReasonReservedAddress
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpDrop,
			Detail: "reserved bridge address",
		})

		return Ingress{}, res, false
	}

	ingressLearns := true
	ingressForwards := true
	if b.gate != nil {
		ingressLearns = b.gate.Learns(res.Ingress)
		ingressForwards = b.gate.Forwards(res.Ingress)
	}

	if !ingressLearns && !ingressForwards {
		res.Reason = ReasonPortBlocked
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpDrop,
			Detail: "port-blocked",
		})

		return Ingress{}, res, false
	}

	var (
		classifiedFID    vlan.ID
		ingressPCP       vlan.PCP
		ingressDEI       bool
		remainingTags    []vlan.Tag
		isTagged         bool
		isPriorityTagged bool
		isUntagged       bool
	)

	if b.cfg.VLAN == nil {
		classifiedFID = 0
		res.FID = 0
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpClassify,
			Detail: "filtering database 0",
		})
	} else {
		sw, swOk := b.cfg.VLAN.Switchports[res.Ingress]

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
						Layer:  port.LayerVlan,
						Op:     trace.OpFilter,
						Detail: "admission tagged-only rejected untagged frame",
					},
					trace.Step{
						Layer:  port.LayerVlan,
						Op:     trace.OpDrop,
						Detail: "admission",
					},
				)

				return Ingress{}, res, false
			}
		case UntaggedAndPriorityTaggedOnly:
			if isTagged {
				res.Reason = ReasonAdmission
				res.Steps = append(res.Steps,
					trace.Step{
						Layer:  port.LayerVlan,
						Op:     trace.OpFilter,
						Detail: "admission untagged-only rejected tagged frame",
					},
					trace.Step{
						Layer:  port.LayerVlan,
						Op:     trace.OpDrop,
						Detail: "admission",
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
					Layer:  port.LayerVlan,
					Op:     trace.OpDrop,
					Detail: "no PVID configured",
				})

				return Ingress{}, res, false
			}
			classifiedFID = *sw.PVID
		}

		res.FID = classifiedFID
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerVlan,
			Op:     trace.OpClassify,
			Detail: fmt.Sprintf("vlan %d", classifiedFID),
		})

		if sw.IngressFiltering {
			isMember := slices.Contains(sw.Tagged, classifiedFID) || slices.Contains(sw.Untagged, classifiedFID)
			if !isMember {
				res.Reason = ReasonIngressFilter
				res.Steps = append(res.Steps,
					trace.Step{
						Layer:  port.LayerVlan,
						Op:     trace.OpFilter,
						Detail: fmt.Sprintf("ingress filter rejected vlan %d", classifiedFID),
					},
					trace.Step{
						Layer:  port.LayerVlan,
						Op:     trace.OpDrop,
						Detail: "ingress-filter",
					},
				)

				return Ingress{}, res, false
			}
		}

		if _, exists := b.cfg.VLAN.Table[classifiedFID]; !exists {
			res.Reason = ReasonUndefinedVLAN
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerVlan,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("vlan %d undefined", classifiedFID),
			})

			return Ingress{}, res, false
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
			if b.cfg.MaxEntries > 0 && b.dynamicCount() >= b.cfg.MaxEntries {
				evicted, wasEvicted = b.evictOldestDynamic()
			}
			b.fdb[srcKey] = Entry{
				FID:       classifiedFID,
				MAC:       f.Src,
				Port:      res.Ingress,
				Static:    false,
				LearnedAt: now,
			}
			b.counters.Learned++
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpLearn,
				Detail: fmt.Sprintf("%s -> %s", f.Src, res.Ingress),
			})
			if wasEvicted {
				res.Steps = append(res.Steps, trace.Step{
					Layer:  port.LayerRelay,
					Op:     trace.OpLearn,
					Detail: fmt.Sprintf("evicted %s %s", evicted.MAC, evicted.Port),
				})
			}
		} else if !existing.Static {
			detail := fmt.Sprintf("%s -> %s", f.Src, res.Ingress)
			if existing.Port != res.Ingress {
				detail = fmt.Sprintf("%s moved %s -> %s", f.Src, existing.Port, res.Ingress)
				b.counters.Moved++
			}
			existing.Port = res.Ingress
			existing.LearnedAt = now
			b.fdb[srcKey] = existing
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpLearn,
				Detail: detail,
			})
		}
	}

	if !ingressForwards {
		res.Reason = ReasonPortBlocked
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpDrop,
			Detail: "port-blocked",
		})

		return Ingress{}, res, false
	}

	in := Ingress{
		Port:          res.Ingress,
		FID:           classifiedFID,
		PCP:           ingressPCP,
		DEI:           ingressDEI,
		RemainingTags: remainingTags,
		Steps:         res.Steps,
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
				Layer:  port.LayerRelay,
				Op:     trace.OpLookup,
				Detail: "flood vlan",
			})
		} else {
			dstKey := fdbKey{fid: in.FID, mac: f.Dst}
			if entry, exists := b.fdb[dstKey]; exists {
				isHit = true
				hitPort = entry.Port
				res.Steps = append(res.Steps, trace.Step{
					Layer:  port.LayerRelay,
					Op:     trace.OpLookup,
					Detail: fmt.Sprintf("hit %s", hitPort),
				})
			} else {
				res.Steps = append(res.Steps, trace.Step{
					Layer:  port.LayerRelay,
					Op:     trace.OpLookup,
					Detail: "unicast miss",
				})
			}
		}
	} else {
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpLookup,
			Detail: "group destination",
		})
	}

	if isHit {
		if hitPort == in.Port {
			res.Reason = ReasonSamePort
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: "same-port",
			})

			return res
		}

		destPort, exists := b.ports.Port(hitPort)
		if !exists {
			res.Reason = port.ReasonPortDown
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: "destination port not found",
			})

			return res
		}

		member, txReason := b.ports.Transmit(destPort.Name, len(f.Payload))
		if txReason == port.ReasonPortDown {
			res.Reason = port.ReasonPortDown
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Dropped: port.ReasonPortDown,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: "port-down",
			})

			return res
		}

		egressFrame, isMember := b.buildEgressFrame(destPort.Name, in.FID, f, in.PCP, in.DEI, in.RemainingTags)
		if b.cfg.VLAN != nil && !isMember {
			res.Reason = ReasonNotMember
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Dropped: ReasonNotMember,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerVlan,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("port %s is not a member of vlan %d", destPort.Name, in.FID),
			})

			return res
		}

		if b.gate != nil && !b.gate.Forwards(destPort.Name) {
			res.Reason = ReasonPortBlocked
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Member:  member,
				Frame:   egressFrame,
				Dropped: ReasonPortBlocked,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("port %s: port-blocked", destPort.Name),
			})

			return res
		}

		if b.isProtected(in.Port) && b.isProtected(destPort.Name) {
			res.Reason = ReasonProtected
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Member:  member,
				Frame:   egressFrame,
				Dropped: ReasonProtected,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("port %s: protected", destPort.Name),
			})

			return res
		}

		if txReason == port.ReasonMTUExceeded {
			res.Reason = port.ReasonMTUExceeded
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Member:  member,
				Frame:   egressFrame,
				Dropped: port.ReasonMTUExceeded,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("port %s: mtu-exceeded", destPort.Name),
			})

			return res
		}

		if b.cfg.VLAN != nil {
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerVlan,
				Op:     trace.OpRewrite,
				Detail: fmt.Sprintf("port %s egress tag form", destPort.Name),
			})
		}
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpTransmit,
			Detail: fmt.Sprintf("port %s", destPort.Name),
		})
		res.Egress = append(res.Egress, Egress{
			Port:   destPort.Name,
			Member: member,
			Frame:  egressFrame,
		})
		res.Outcome = trace.Forwarded

		return res
	}

	var (
		candidates  []port.Port
		memberNames []string
		txReasons   []trace.Reason
	)
	for _, cand := range b.ports.Ports() {
		if cand.LagParent != "" || cand.Name == in.Port {
			continue
		}
		member, txReason := b.ports.Transmit(cand.Name, len(f.Payload))
		if txReason == port.ReasonPortDown {
			continue
		}
		if b.cfg.VLAN != nil {
			sw, exists := b.cfg.VLAN.Switchports[cand.Name]
			if !exists {
				continue
			}
			isMember := slices.Contains(sw.Tagged, in.FID) || slices.Contains(sw.Untagged, in.FID)
			if !isMember {
				continue
			}
		}
		candidates = append(candidates, cand)
		memberNames = append(memberNames, member)
		txReasons = append(txReasons, txReason)
	}

	if len(candidates) == 0 {
		res.Reason = ReasonNoEgress
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpDrop,
			Detail: "no other forwarding member port",
		})

		return res
	}

	res.Steps = append(res.Steps, trace.Step{
		Layer:  port.LayerRelay,
		Op:     trace.OpReplicate,
		Detail: fmt.Sprintf("%d candidate ports", len(candidates)),
	})

	var transmitted int
	for i, cand := range candidates {
		mem := memberNames[i]
		txReason := txReasons[i]
		egressFrame, _ := b.buildEgressFrame(cand.Name, in.FID, f, in.PCP, in.DEI, in.RemainingTags)
		if b.gate != nil && !b.gate.Forwards(cand.Name) {
			res.Egress = append(res.Egress, Egress{
				Port:    cand.Name,
				Member:  mem,
				Frame:   egressFrame,
				Dropped: ReasonPortBlocked,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("port %s: port-blocked", cand.Name),
			})

			continue
		}

		if b.isProtected(in.Port) && b.isProtected(cand.Name) {
			res.Egress = append(res.Egress, Egress{
				Port:    cand.Name,
				Member:  mem,
				Frame:   egressFrame,
				Dropped: ReasonProtected,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("port %s: protected", cand.Name),
			})

			continue
		}

		if txReason == port.ReasonMTUExceeded {
			res.Egress = append(res.Egress, Egress{
				Port:    cand.Name,
				Member:  mem,
				Frame:   egressFrame,
				Dropped: port.ReasonMTUExceeded,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("port %s: mtu-exceeded", cand.Name),
			})

			continue
		}

		if b.cfg.VLAN != nil {
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerVlan,
				Op:     trace.OpRewrite,
				Detail: fmt.Sprintf("port %s egress tag form", cand.Name),
			})
		}
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpTransmit,
			Detail: fmt.Sprintf("port %s", cand.Name),
		})
		res.Egress = append(res.Egress, Egress{
			Port:   cand.Name,
			Member: mem,
			Frame:  egressFrame,
		})
		transmitted++
	}

	if transmitted > 0 {
		res.Outcome = trace.Flooded
	} else {
		res.Outcome = trace.Dropped
		if len(res.Egress) > 0 {
			res.Reason = res.Egress[0].Dropped
		} else {
			res.Reason = port.ReasonMTUExceeded
		}
	}

	return res
}

func (b *Bridge) buildEgressFrame(
	portName string,
	vid vlan.ID,
	f ethernet.Frame,
	ingressPCP vlan.PCP,
	ingressDEI bool,
	remainingTags []vlan.Tag,
) (ethernet.Frame, bool) {
	out := f
	if b.cfg.VLAN == nil {
		return out, true
	}

	sw, ok := b.cfg.VLAN.Switchports[portName]
	if !ok {
		return out, false
	}

	if slices.Contains(sw.Tagged, vid) {
		cTag := vlan.Tag{
			TPID: uint16(ethernet.EtherTypeDot1Q),
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
