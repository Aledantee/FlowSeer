// Package bridge implements an Ethernet transparent bridge with optional IEEE 802.1Q VLAN awareness.
package bridge

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
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
	tbl, err := builder.Build()
	if err == nil {
		b.ports = tbl
	}
}

// Learn preloads the forwarding database with the provided seeds. A seed naming a
// LAG member is stored under the LAG, as the relay learns it, so a later lookup
// treats the aggregation as one port. A seed with a group address is ignored,
// since the relay never learns one and the export could not carry it.
func (b *Bridge) Learn(seeds []Seed) {
	for _, s := range seeds {
		if s.MAC.IsGroup() {
			continue
		}
		if p, ok := b.ports.Resolve(s.Port); ok {
			s.Port = p.Name
		}
		key := fdbKey{fid: s.FID, mac: s.MAC}
		b.fdb[key] = Entry(s)
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

// Age removes dynamic forwarding database entries older than the configured aging time relative to now.
func (b *Bridge) Age(now time.Time) {
	for key, e := range b.fdb {
		if !e.Static && now.Sub(e.LearnedAt) > b.agingTime {
			delete(b.fdb, key)
		}
	}
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
	var res Result
	res.Outcome = trace.Dropped

	p, ok := b.ports.Port(ingress)
	if !ok {
		res.Reason = ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpDrop,
			Detail: "ingress port not found",
		})

		return res
	}

	resolved, ok := b.ports.Resolve(ingress)
	if !ok {
		res.Reason = ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpDrop,
			Detail: "ingress LAG parent not found",
		})

		return res
	}
	res.Ingress = resolved.Name

	if !p.Forwards() || !resolved.Forwards() {
		res.Reason = ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpDrop,
			Detail: "ingress port down",
		})

		return res
	}

	if ethernet.IsReserved(f.Dst) {
		res.Reason = ReasonReservedAddress
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpDrop,
			Detail: "reserved bridge address",
		})

		return res
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

		return res
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

				return res
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

				return res
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

				return res
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

				return res
			}
		}

		if _, exists := b.cfg.VLAN.Table[classifiedFID]; !exists {
			res.Reason = ReasonUndefinedVLAN
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerVlan,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("vlan %d undefined", classifiedFID),
			})

			return res
		}
	}

	if learn && ingressLearns && !f.Src.IsGroup() {
		srcKey := fdbKey{fid: classifiedFID, mac: f.Src}
		existing, exists := b.fdb[srcKey]
		if !exists {
			b.fdb[srcKey] = Entry{
				FID:       classifiedFID,
				MAC:       f.Src,
				Port:      res.Ingress,
				Static:    false,
				LearnedAt: now,
			}
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpLearn,
				Detail: fmt.Sprintf("%s -> %s", f.Src, res.Ingress),
			})
		} else if !existing.Static {
			detail := fmt.Sprintf("%s -> %s", f.Src, res.Ingress)
			if existing.Port != res.Ingress {
				detail = fmt.Sprintf("%s moved %s -> %s", f.Src, existing.Port, res.Ingress)
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

		return res
	}

	var (
		isHit   bool
		hitPort string
	)
	if !f.Dst.IsGroup() {
		dstKey := fdbKey{fid: classifiedFID, mac: f.Dst}
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
	} else {
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerRelay,
			Op:     trace.OpLookup,
			Detail: "group destination",
		})
	}

	if isHit {
		if hitPort == res.Ingress {
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
			res.Reason = ReasonPortDown
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: "destination port not found",
			})

			return res
		}

		var memberName string
		if destPort.Kind == port.Lag {
			mems := b.ports.Members(destPort.Name)
			var fwdMembers []port.Port
			for _, m := range mems {
				if m.Forwards() {
					fwdMembers = append(fwdMembers, m)
				}
			}
			if len(fwdMembers) == 0 || !destPort.Forwards() {
				res.Reason = ReasonPortDown
				res.Egress = append(res.Egress, Egress{
					Port:    destPort.Name,
					Dropped: ReasonPortDown,
				})
				res.Steps = append(res.Steps, trace.Step{
					Layer:  port.LayerRelay,
					Op:     trace.OpDrop,
					Detail: "port-down",
				})

				return res
			}
			sort.Slice(fwdMembers, func(i, j int) bool {
				return fwdMembers[i].Name < fwdMembers[j].Name
			})
			memberName = fwdMembers[0].Name
		} else if !destPort.Forwards() {
			res.Reason = ReasonPortDown
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Dropped: ReasonPortDown,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerRelay,
				Op:     trace.OpDrop,
				Detail: "port-down",
			})

			return res
		}

		egressFrame, isMember := b.buildEgressFrame(destPort.Name, classifiedFID, f, ingressPCP, ingressDEI, remainingTags)
		if b.cfg.VLAN != nil && !isMember {
			res.Reason = ReasonNotMember
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Dropped: ReasonNotMember,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerVlan,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("port %s is not a member of vlan %d", destPort.Name, classifiedFID),
			})

			return res
		}

		if b.gate != nil && !b.gate.Forwards(destPort.Name) {
			res.Reason = ReasonPortBlocked
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Member:  memberName,
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

		if destPort.MTU > 0 && len(f.Payload) > destPort.MTU {
			res.Reason = ReasonMTUExceeded
			res.Egress = append(res.Egress, Egress{
				Port:    destPort.Name,
				Member:  memberName,
				Frame:   egressFrame,
				Dropped: ReasonMTUExceeded,
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
			Member: memberName,
			Frame:  egressFrame,
		})
		res.Outcome = trace.Forwarded

		return res
	}

	var (
		candidates  []port.Port
		memberNames []string
	)
	for _, cand := range b.ports.Ports() {
		if cand.LagParent != "" || cand.Name == res.Ingress || !cand.Forwards() {
			continue
		}
		var member string
		if cand.Kind == port.Lag {
			mems := b.ports.Members(cand.Name)
			var fwdMembers []port.Port
			for _, m := range mems {
				if m.Forwards() {
					fwdMembers = append(fwdMembers, m)
				}
			}
			if len(fwdMembers) == 0 {
				continue
			}
			sort.Slice(fwdMembers, func(i, j int) bool {
				return fwdMembers[i].Name < fwdMembers[j].Name
			})
			member = fwdMembers[0].Name
		}
		if b.cfg.VLAN != nil {
			sw, exists := b.cfg.VLAN.Switchports[cand.Name]
			if !exists {
				continue
			}
			isMember := slices.Contains(sw.Tagged, classifiedFID) || slices.Contains(sw.Untagged, classifiedFID)
			if !isMember {
				continue
			}
		}
		candidates = append(candidates, cand)
		memberNames = append(memberNames, member)
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
		egressFrame, _ := b.buildEgressFrame(cand.Name, classifiedFID, f, ingressPCP, ingressDEI, remainingTags)
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

		if cand.MTU > 0 && len(f.Payload) > cand.MTU {
			res.Egress = append(res.Egress, Egress{
				Port:    cand.Name,
				Member:  mem,
				Frame:   egressFrame,
				Dropped: ReasonMTUExceeded,
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
			res.Reason = ReasonMTUExceeded
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
