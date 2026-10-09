package fabric

import (
	"maps"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// applyDeviceOutput keeps token resolutions until every emission, drop, and
// copy has used its parent. Device tokens can repeat across nodes.
func (f *Fabric) applyDeviceOutput(now time.Time, node string, out device.Output, current *journey) {
	if out.Err != nil {
		f.recordFault(out.Err)
	}
	var resolved map[device.HoldToken]*journey
	resolve := func(token device.HoldToken, ingress string, frame ethernet.Frame, protocol bool) *journey {
		if token != 0 {
			if j := resolved[token]; j != nil {
				return j
			}
		}
		var j *journey
		if fid := f.held[holdKey{node: node, token: token}]; token != 0 && fid != 0 {
			j = f.journeys[fid]
		}
		if j == nil {
			fid := f.nextFrameID
			f.nextFrameID++
			j = &journey{
				FrameID: fid, Protocol: protocol, Origin: JourneyOrigin{Kind: OriginInjection},
				Injection: Injection{At: now, Origin: Endpoint{Node: node, Port: ingress}, Frame: frame},
			}
			f.journeys[fid] = j
			f.touch(fid)
			f.record(j, Entry{At: now, Kind: EntryInjection, Device: node, Port: ingress, Cable: f.portCable(node, ingress)})
		}
		if token != 0 {
			if resolved == nil {
				resolved = make(map[device.HoldToken]*journey)
			}
			resolved[token] = j
		}
		return j
	}
	for _, em := range out.Emissions {
		j := resolve(em.Hold, em.Port, em.Frame, em.Protocol)
		seq := f.nextSeq
		f.nextSeq++
		f.transmit(now, node, em.Port, em.Member, em.Frame, seq, j.FrameID, j, em.PCP, "")
	}
	for _, drop := range out.Drops {
		j := current
		if drop.Hold != 0 || j == nil {
			j = resolve(drop.Hold, drop.Ingress, ethernet.Frame{}, false)
		}
		entry := Entry{At: now, Kind: EntryDrop, Device: node, Port: drop.Port, Reason: drop.Reason}
		// A drop that declares no step names a port it never tried to send on, so it
		// is neither a discard at that port nor an entry with an empty step.
		if stepDeclared(drop.Step) {
			step := drop.Step
			entry.Step = &step
			if drop.Port != "" {
				f.countEgressDrop(node, drop.Port, drop.Reason)
			}
		}
		f.record(j, entry)
	}
	for _, copy := range out.Copies {
		parent := current
		if copy.Hold != 0 || parent == nil {
			parent = resolve(copy.Hold, copy.Port, copy.Frame, false)
		}
		fid := f.nextFrameID
		f.nextFrameID++
		seq := f.nextSeq
		f.nextSeq++
		inj := Injection{
			At:        now,
			Origin:    Endpoint{Node: node, Port: copy.Port},
			Frame:     copy.Frame,
			Retention: parent.Injection.Retention,
			Flow:      parent.Injection.Flow,
		}
		kind := OriginMirror
		if out.Acceptance == device.ReflectionAcceptance {
			kind = OriginReflection
		}
		copyJourney := &journey{
			FrameID: fid,
			Origin: JourneyOrigin{
				Kind:   kind,
				Of:     parent.FrameID,
				Mirror: copy.Mirror,
			},
			Injection: inj,
		}
		f.journeys[fid] = copyJourney
		f.touch(fid)
		if kind == OriginReflection {
			f.entered[fid] = cloneEntered(f.entered[parent.FrameID])
		}
		f.record(copyJourney, Entry{
			At:     now,
			Kind:   EntryInjection,
			Device: node,
			Port:   copy.Port,
			Cable:  f.portCable(node, copy.Port),
		})
		if kind == OriginReflection {
			ref, ok := f.linkEnd(node, copy.Port)
			if !ok {
				continue
			}
			if ref.end.Oper != port.Up {
				entryKind := EntryDrop
				if ref.end.Oper == port.Unknown {
					entryKind = EntryUnresolved
				}
				cable := ref.link.Cable.Clone()
				f.record(copyJourney, Entry{At: now, Kind: entryKind, Device: node, Cable: &cable, Reason: ref.end.Reason})
				continue
			}
		}
		pcp, _ := copy.Frame.Priority()
		f.transmit(now, node, copy.Port, copy.Member, copy.Frame, seq, fid, copyJourney, pcp, copy.Mirror)
	}
	for _, token := range slices.Sorted(maps.Keys(resolved)) {
		key := holdKey{node: node, token: token}
		if fid, ok := f.held[key]; ok {
			delete(f.held, key)
			f.inflightRemove(fid)
		}
	}
}

// stepDeclared reports whether a step names a layer, a rule, or a subject.
func stepDeclared(s trace.Step) bool {
	return s.Layer != "" || s.RuleID != "" || s.Subject != (trace.Subject{})
}

func (f *Fabric) scheduleWake(device string) {
	sw := f.nodeDevice(device)
	ok := sw != nil
	if !ok {
		return
	}
	next, ok := sw.NextWake()
	if !ok {
		f.removeWake(device)
		return
	}
	queued, exists := f.wakes[device]
	if exists && queued.Equal(next) {
		return
	}
	f.removeWake(device)
	f.wakes[device] = next
	f.enqueue(Arrival{
		At:     next,
		Kind:   ArrivalWake,
		Seq:    0,
		Device: device,
	})
}

func (f *Fabric) removeWake(device string) {
	delete(f.wakes, device)
	f.removeWakeArrival(device)
}
