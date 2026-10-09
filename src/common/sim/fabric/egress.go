package fabric

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
)

func (f *Fabric) transmit(now time.Time, device, portName, memberName string, frame ethernet.Frame, seq uint64, fid FrameID, journey *Journey, pcp vlan.PCP, mirror string) {
	outPort := portName
	if memberName != "" {
		outPort = memberName
	}

	rawOut, _ := frame.Encode()
	outOctets := uint64(len(rawOut))
	outClass := classifyMAC(frame.Dst)

	outPorts := []string{portName}
	if memberName != "" {
		outPorts = append(outPorts, memberName)
	}
	countOut := func() {
		for _, p := range outPorts {
			f.countEgress(device, p, outOctets, outClass)
		}
	}

	ref, ok := f.linkEnd(device, outPort)
	if !ok {
		countOut()

		return
	}

	if ref.end.Speed.SpeedBPS == 0 {
		countOut()
		cableCopy := ref.link.Cable.Clone()
		f.record(journey, Entry{
			At:     now,
			Kind:   EntryLoss,
			Cable:  &cableCopy,
			Reason: cablemodel.ReasonCableLoss,
		})

		return
	}

	// A frame the stated-buffer tail drop refuses never left the port, so it
	// counts as a discard only, not as a transmission.
	if admitted := f.enqueueEgress(now, ref.end.Endpoint, portName, frame, seq, fid, journey, pcp, mirror); admitted {
		countOut()
	}
}

// enqueueEgress appends a frame to txEnd's shaped egress queue and reports
// whether the queue admitted it. A stated buffer that the frame would overflow
// tail-drops it and returns false, so the caller counts a discard, not a
// transmission.
func (f *Fabric) enqueueEgress(now time.Time, txEnd Endpoint, egressPort string, frame ethernet.Frame, seq uint64, fid FrameID, journey *Journey, pcp vlan.PCP, mirror string) bool {
	q := f.egress[txEnd]
	if q == nil {
		q = &egressQueue{}
		f.egress[txEnd] = q
	}
	raw, _ := frame.Encode()
	octets := uint64(len(raw))
	queueDevice := f.nodeDevice(txEnd.Node)
	if queueDevice == nil {
		queueDevice = f.topology.defaultQueue
	}
	decision := queueDevice.AdmitEgress(egressPort, pcp, q.depth[pcp], octets)
	if !decision.Admitted {
		f.record(journey, Entry{
			At: now, Kind: EntryDrop, Device: txEnd.Node, Port: egressPort,
			Reason: decision.Reason, Step: decision.Step,
		})
		f.countEgressDrop(txEnd.Node, egressPort, decision.Reason)
		if egressPort != txEnd.Port {
			f.countEgressDrop(txEnd.Node, txEnd.Port, decision.Reason)
		}
		return false
	}
	if decision.Step != nil {
		f.markQueueBufferUnstated(now, txEnd, egressPort, pcp, fid, *decision.Step, journey)
	}
	q.pending[pcp] = append(q.pending[pcp], queued{
		frame:      cloneFrame(frame),
		octets:     octets,
		seq:        seq,
		fid:        fid,
		journey:    journey,
		pcp:        pcp,
		egressPort: egressPort,
		enqueued:   now,
		mirror:     mirror,
	})
	q.depth[pcp] += octets
	q.peak[pcp] = max(q.peak[pcp], q.depth[pcp])
	f.inflightAdd(fid)
	f.serve(now, txEnd)

	return true
}

func (f *Fabric) serve(now time.Time, txEnd Endpoint) {
	q := f.egress[txEnd]
	if q == nil {
		return
	}
	if busy := f.busyUntil[txEnd]; busy.After(now) {
		if q.dequeuePending && !busy.Before(q.dequeueAt) {
			return
		}
		if q.dequeuePending {
			f.removeDequeue(txEnd)
		}
		f.scheduleDequeue(txEnd, busy)

		return
	}
	if f.topology.nodes[txEnd.Node].snapshot && len(f.queue) > 0 && f.queue[0].At.Equal(now) && f.queue[0].Kind < ArrivalDequeue {
		if q.dequeuePending && !q.dequeueAt.After(now) {
			return
		}
		if q.dequeuePending {
			f.removeDequeue(txEnd)
		}
		f.scheduleDequeue(txEnd, now)

		return
	}

	for {
		selected := -1
		var earliest time.Time
		for pcp := len(q.pending) - 1; pcp >= 0; pcp-- {
			if len(q.pending[pcp]) == 0 {
				continue
			}
			if !q.rateClock[pcp].After(now) {
				selected = pcp

				break
			}
			if earliest.IsZero() || q.rateClock[pcp].Before(earliest) {
				earliest = q.rateClock[pcp]
			}
		}
		if selected < 0 {
			if !earliest.IsZero() && (!q.dequeuePending || earliest.Before(q.dequeueAt)) {
				if q.dequeuePending {
					f.removeDequeue(txEnd)
				}
				f.scheduleDequeue(txEnd, earliest)
			}

			return
		}

		if q.dequeuePending {
			if !q.dequeueAt.After(now) {
				return
			}
			f.removeDequeue(txEnd)
		}

		pending := q.pending[selected]
		item := pending[0]
		pending[0] = queued{}
		if len(pending) == 1 {
			q.pending[selected] = nil
		} else {
			q.pending[selected] = pending[1:]
		}
		q.depth[selected] -= item.octets
		f.inflightRemove(item.fid)

		ref, ok := f.linkEnd(txEnd.Node, txEnd.Port)
		if !ok {
			continue
		}
		if ref.end.Speed.SpeedBPS == 0 {
			cableCopy := ref.link.Cable.Clone()
			f.record(item.journey, Entry{
				At:     now,
				Kind:   EntryLoss,
				Cable:  &cableCopy,
				Reason: cablemodel.ReasonCableLoss,
			})

			continue
		}

		end := f.transmitCable(now, txEnd, ref, item)
		if d := f.nodeDevice(txEnd.Node); d != nil {
			rate := d.AdmitEgress(item.egressPort, item.pcp, 0, 0).MaxRateBPS
			if rate != 0 {
				q.rateClock[selected] = now.Add(serialization(item.frame, rate))
			}
		}
		if q.pendingCount() > 0 {
			f.scheduleDequeue(txEnd, end)
		}

		return
	}
}

// scheduleDequeue queues the serve of txEnd's pending egress at time at.
//
// Both guards below are fabric-internal invariants no topology or injection
// can provoke: the serve path never schedules a dequeue in the past or a
// second one while the first is pending. A breach is a bug in fabric's own
// scheduling, so it records a sticky fault (see [Fabric.err]) and returns
// without touching the queue rather than panicking. [Fabric.Step] then stops
// advancing, and [Fabric.Err] surfaces the fault to the caller.
func (f *Fabric) scheduleDequeue(txEnd Endpoint, at time.Time) {
	if f.runStarted() && at.Before(f.clock) {
		f.recordFault(errs.Msgf("fabric: scheduled dequeue at %s precedes clock %s", at, f.clock))
		return
	}
	q := f.egress[txEnd]
	if q.dequeuePending {
		f.recordFault(errs.Msgf("fabric: endpoint %s/%s already has a pending dequeue", txEnd.Node, txEnd.Port))
		return
	}
	q.dequeueAt = at
	q.dequeuePending = true
	f.enqueue(Arrival{
		At:     at,
		Kind:   ArrivalDequeue,
		Device: txEnd.Node,
		Port:   txEnd.Port,
	})
}

func (f *Fabric) removeDequeue(txEnd Endpoint) {
	q := f.egress[txEnd]
	q.dequeueAt = time.Time{}
	q.dequeuePending = false
	f.removeDequeueArrival(txEnd)
}

func (q *egressQueue) pendingCount() int {
	total := 0
	for _, pending := range q.pending {
		total += len(pending)
	}

	return total
}

func (f *Fabric) transmitCable(start time.Time, txEnd Endpoint, ref linkEndRef, item queued) time.Time {
	cable := ref.link.Cable
	rate := ref.end.Speed.SpeedBPS
	ser := serialization(item.frame, rate)
	end := start.Add(ser)
	f.busyUntil[txEnd] = end

	prop := cable.Propagation()
	deliveryAt := end.Add(prop)

	f.cableCrossings[cable.A]++
	count := f.cableCrossings[cable.A]

	lost, corrupt := cable.Crossing(count, ref.end.Endpoint == cable.A)

	cableCopy := cable.Clone()

	if lost {
		f.record(item.journey, Entry{
			At:     start,
			Kind:   EntryLoss,
			Cable:  &cableCopy,
			Reason: cablemodel.ReasonCableLoss,
		})

		return end
	}

	farEnd := ref.peer.Endpoint

	crossingEntry := Entry{
		At:            start,
		Kind:          EntryCrossing,
		Device:        farEnd.Node,
		Port:          farEnd.Port,
		Cable:         &cableCopy,
		Latency:       prop,
		Serialization: ser,
		Wait:          start.Sub(item.enqueued),
		PCP:           item.pcp,
	}
	f.record(item.journey, crossingEntry)

	f.enqueue(Arrival{
		At:      deliveryAt,
		Kind:    ArrivalFrame,
		Seq:     item.seq,
		Device:  farEnd.Node,
		Port:    farEnd.Port,
		FrameID: item.fid,
		Frame:   cloneFrame(item.frame),
		Corrupt: corrupt,
	})

	return end
}
