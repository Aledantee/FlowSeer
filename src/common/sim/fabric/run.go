package fabric

import (
	"fmt"
	"maps"
	"math"
	"math/bits"
	"net/netip"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Packet specifies an IP datagram destination address, protocol, and payload to originate
// from a host with an IP stack.
type Packet struct {
	To       netip.Addr
	Protocol uint8
	Payload  []byte
}

// Retention states whether a settled journey is kept in [Fabric.Report] or
// freed, leaving only its flow's statistics.
type Retention uint8

const (
	// RetainJourney keeps a settled journey in [Fabric.Report].
	RetainJourney Retention = iota
	// RetainAggregate frees a settled journey once its flow's statistics hold
	// a frame's worth of what became of it. It requires a nonzero flow.
	// A held frame settles only after its hold and all transmissions end.
	RetainAggregate
)

// Injection specifies a frame or packet to introduce into the fabric at a particular origin endpoint and timestamp.
//
// Retention and Flow decide what the fabric keeps of the frame once it
// settles: a zero Flow folds into no statistics, and RetainAggregate frees
// the settled journey. A hold keeps the original journey in flight.
// A mirror or reflector copy inherits both from the frame it copies.
type Injection struct {
	At        time.Time
	Origin    Endpoint
	Frame     ethernet.Frame
	Packet    *Packet
	Retention Retention
	Flow      FlowID
}

// Snapshot captures an instantaneous view of simulation time, in-flight arrivals, pending egress frames,
// physical links, device states, and the endpoints still transmitting past the clock.
type Snapshot struct {
	Clock   time.Time
	Queue   []Arrival
	Queued  map[Endpoint]int
	Links   []Link
	Devices map[string]device.State
	Busy    map[Endpoint]time.Time
	// EgressDepths carries each endpoint queue's current and peak depth in
	// encoded frame octets. It is absent from [Snapshot.Fingerprint] and from
	// [Compare], because queue depth is transient.
	EgressDepths map[Endpoint]map[vlan.PCP]EgressDepth
}

type queued struct {
	frame      ethernet.Frame
	octets     uint64
	seq        uint64
	fid        FrameID
	journey    *Journey
	pcp        vlan.PCP
	egressPort string
	enqueued   time.Time
	mirror     string
}

type egressQueue struct {
	pending        [8][]queued
	dequeueAt      time.Time
	dequeuePending bool
	rateClock      [8]time.Time
	depth          [8]uint64
	peak           [8]uint64
}

// EgressDepth is an egress queue's current and greatest depth, in encoded frame
// octets, for one endpoint and priority.
type EgressDepth struct {
	Depth uint64
	Peak  uint64
}

// serialization is the time the frame occupies the wire at rateBPS. Every
// caller transmits on a link that negotiated, so the rate is never 0.
func serialization(frame ethernet.Frame, rateBPS uint64) time.Duration {
	return rateInterval(uint64(frame.WireOctets())*8, rateBPS)
}

func rateInterval(wireBits, rateBPS uint64) time.Duration {
	seconds := wireBits / rateBPS
	remainder := wireBits % rateBPS
	maxNanos := uint64(math.MaxInt64)
	if seconds > maxNanos/uint64(time.Second) {
		return time.Duration(math.MaxInt64)
	}
	nanos := seconds * uint64(time.Second)

	hi, lo := bits.Mul64(remainder, uint64(time.Second))
	fraction, rem := bits.Div64(hi, lo, rateBPS)
	if rem != 0 {
		fraction++
	}
	if fraction > maxNanos-nanos {
		return time.Duration(math.MaxInt64)
	}

	return time.Duration(nanos + fraction)
}

// portCable returns a copy of the cable at an endpoint, or nil for a port
// without one.
func (f *Fabric) portCable(node, portName string) *Cable {
	ref, ok := f.linkEnd(node, portName)
	if !ok {
		return nil
	}
	cable := ref.link.Cable.Clone()

	return &cable
}

func (f *Fabric) runStarted() bool {
	return f.stepped && !f.clock.IsZero()
}

// Inject queues a frame or originated packet for introduction into the fabric at the requested origin endpoint and time.
//
// For a host origin, the host's configured VLAN form is applied (adding its C-TAG or leaving the frame untagged).
// A host transmission is queued at At. Step checks its link and charges its egress at that time.
// A transmitted frame records a crossing and arrives at the connected switch port
// after serialization and cable propagation.
// When Packet is set, the frame is originated by the host's IP stack and Frame must have no field set. For a device
// port origin, the frame is queued directly as given at At.
// A host whose link is not Up transmits nothing, and the injection is still valid: the journey records a drop with
// the link's reason at At when the link is Down, and an [EntryUnresolved] with the link's reason when it is Unknown.
// Inject returns an error if the origin names an unknown host, an unknown switch port, a reflector (which has
// no port of its own to inject at), a host without a connected cable, a switch origin with Packet set, a host
// without an IP stack when Packet is set, a Packet injection specifying Frame fields, a non-empty origin port
// for host packet injection, if packet origination fails, if At precedes the fabric clock,
// if Retention is above [RetainAggregate], or if [RetainAggregate] has a zero Flow.
func (f *Fabric) Inject(inj Injection) (FrameID, error) {
	return f.inject(inj, true)
}

func (f *Fabric) inject(inj Injection, queueHost bool) (FrameID, error) {
	f.initRunState()
	if inj.At.Before(f.clock) {
		return 0, errs.New().Attr("at", inj.At).Attr("clock", f.clock).
			Msg("injection time precedes fabric clock")
	}

	if inj.Retention > RetainAggregate {
		return 0, errs.New().
			Attr("retention", inj.Retention).
			Msgf("unknown retention %d", inj.Retention)
	}
	if inj.Retention == RetainAggregate && inj.Flow == 0 {
		return 0, errs.New().
			Attr("retention", inj.Retention).
			Msg("aggregate retention requires a nonzero flow")
	}
	defer f.settleTouched()

	var (
		targetDevice string
		targetPort   string
		frame        ethernet.Frame
		hostRef      *linkEndRef
	)

	d := f.nodeDevice(inj.Origin.Node)
	ok := d != nil
	if !ok {
		return 0, errs.New().Attr("node", inj.Origin.Node).Msgf("origin node %q not found in fabric", inj.Origin.Node)
	}
	var packet *device.Packet
	if inj.Packet != nil {
		packet = &device.Packet{To: inj.Packet.To, Protocol: inj.Packet.Protocol, Payload: inj.Packet.Payload}
	}
	originated, err := d.Originate(inj.At, inj.Origin.Port, inj.Frame, packet)
	if err != nil {
		return 0, err
	}
	if originated.Arrival != nil {
		targetDevice, targetPort, frame = inj.Origin.Node, originated.Arrival.Port, originated.Arrival.Frame
	} else {
		ref, ok := f.linkEnd(inj.Origin.Node, "")
		if !ok {
			return 0, errs.New().Attr("host", inj.Origin.Node).Msgf("host %q has no connected cable", inj.Origin.Node)
		}
		hostRef = &ref
		frame = originated.Emissions[0].Frame
	}

	// The run counts octets from the encoded frame, so a frame the codec
	// refuses is refused here rather than counted as zero bytes later.
	if _, err := frame.Encode(); err != nil {
		return 0, errs.Wrap(err, "encode the injected frame")
	}

	fid := f.nextFrameID
	f.nextFrameID++

	seq := f.nextSeq
	f.nextSeq++

	journey := &Journey{
		FrameID:   fid,
		Origin:    JourneyOrigin{Kind: OriginInjection},
		Injection: inj,
	}
	f.journeys[fid] = journey
	f.touch(fid)
	f.record(journey, Entry{
		At:     inj.At,
		Kind:   EntryInjection,
		Device: inj.Origin.Node,
		Port:   inj.Origin.Port,
		Cable:  f.portCable(inj.Origin.Node, inj.Origin.Port),
	})

	switch {
	case hostRef != nil && queueHost:
		f.enqueue(Arrival{
			At: inj.At, Kind: ArrivalHost, Seq: seq, Device: inj.Origin.Node,
			Frame: cloneFrame(frame), FrameID: fid, pcp: originated.Emissions[0].PCP,
		})
	case hostRef != nil:
		f.injectHostFrame(inj.At, inj.Origin, frame, originated.Emissions[0].PCP, seq, fid, journey)
	default:
		arr := Arrival{
			At:      inj.At,
			Kind:    ArrivalFrame,
			Seq:     seq,
			Device:  targetDevice,
			Port:    targetPort,
			FrameID: fid,
			Frame:   frame,
			Corrupt: false,
		}
		f.enqueue(arr)
	}

	return fid, nil
}

func (f *Fabric) injectHostFrame(at time.Time, origin Endpoint, frame ethernet.Frame, pcp vlan.PCP, seq uint64, fid FrameID, journey *Journey) {
	ref, _ := f.linkEnd(origin.Node, "")
	if ref.end.Oper != port.Up {
		kind := EntryDrop
		if ref.end.Oper == port.Unknown {
			kind = EntryUnresolved
		}
		cable := ref.link.Cable.Clone()
		f.record(journey, Entry{
			At: at, Kind: kind, Device: origin.Node, Cable: &cable, Reason: ref.end.Reason,
		})
		return
	}
	f.enqueueEgress(at, ref.end.Endpoint, ref.end.Port, frame, seq, fid, journey, pcp, "")
}

// Step advances simulation time to the earliest queued arrival and processes it through the destination device.
//
// Host transmissions, scenario actions, and stream frames are processed in queue order.
// An event dated before the clock is processed at the clock.
// If the arrival device port was previously visited by the frame, a loop entry is recorded before processing.
// Corrupted arrivals are discarded with [cablemodel.ReasonBadFrame]. An arrival at a reflector runs its acceptance decision
// and, when accepted, originates the reflected copies; it never reaches the switch forwarding path below.
// A switch's normal arrivals are policed before dynamic MAC aging and forwarding, then un-dropped egress frames
// are enqueued onto connected cables or delivered to target hosts.
// Step returns false when the arrival queue is empty or a scheduling fault has
// been recorded; [Fabric.Err] distinguishes the two.
func (f *Fabric) Step() (Entry, bool) {
	if f.err != nil {
		return Entry{}, false
	}
	if len(f.queue) == 0 {
		return Entry{}, false
	}
	f.initRunState()
	defer f.settleTouched()

	arr := f.popArrival()
	if arr.At.Before(f.clock) {
		arr.At = f.clock
	}
	f.clock = arr.At
	f.stepped = true
	if arr.Kind == ArrivalFrame || arr.Kind == ArrivalHost {
		f.inflightRemove(arr.FrameID)
	}
	if arr.Kind == ArrivalAction {
		action := arr.action.Clone()
		action.At = arr.At
		if action.Inject != nil {
			action.Inject.At = arr.At
		}
		if action.Record != nil {
			action.Record.At = arr.At
		}
		if err := f.applyAction(action); err != nil {
			f.recordFault(err)
			return Entry{}, false
		}
		return Entry{At: arr.At, Kind: EntryAction}, true
	}
	if arr.Kind == ArrivalStream {
		f.stepStream(arr.At, arr.source)
		return Entry{At: arr.At, Kind: EntryInjection, Device: arr.Device, Port: arr.Port}, true
	}
	if arr.Kind == ArrivalHost {
		f.injectHostFrame(arr.At, Endpoint{Node: arr.Device}, arr.Frame, arr.pcp, arr.Seq, arr.FrameID, f.journeys[arr.FrameID])
		return Entry{At: arr.At, Kind: EntryInjection, Device: arr.Device}, true
	}

	if arr.Kind == ArrivalWake {
		delete(f.wakes, arr.Device)
		sw := f.nodeDevice(arr.Device)
		if sw != nil {
			out := sw.Advance(arr.At)
			f.applyOutput(arr.At, arr.Device, out)
			f.scheduleWake(arr.Device)
		}

		return Entry{
			At:     arr.At,
			Kind:   EntryWake,
			Device: arr.Device,
		}, true
	}
	if arr.Kind == ArrivalDequeue {
		ep := Endpoint{Node: arr.Device, Port: arr.Port}
		if q := f.egress[ep]; q != nil {
			q.dequeueAt = time.Time{}
			q.dequeuePending = false
			f.serve(arr.At, ep)
		}

		return Entry{
			At:     arr.At,
			Kind:   EntryDequeue,
			Device: arr.Device,
			Port:   arr.Port,
		}, true
	}

	journey := f.journeys[arr.FrameID]

	ep := Endpoint{Node: arr.Device, Port: arr.Port}
	if f.entered[arr.FrameID] == nil {
		f.entered[arr.FrameID] = make(map[Endpoint]bool)
	}
	if f.entered[arr.FrameID][ep] {
		loopEntry := Entry{
			At:     arr.At,
			Kind:   EntryLoop,
			Device: arr.Device,
			Port:   arr.Port,
		}
		f.record(journey, loopEntry)
	}
	f.entered[arr.FrameID][ep] = true

	sw := f.nodeDevice(arr.Device)
	var inPorts []string
	inPorts = append(inPorts, arr.Port)
	if p, ok := sw.Ports().Port(arr.Port); ok && p.LagParent != "" {
		inPorts = append(inPorts, p.LagParent)
	}

	// Inject refused any frame the codec cannot encode, and a bridge only
	// rewrites the tag stack with valid tags, so Encode cannot fail here.
	rawIn, _ := arr.Frame.Encode()
	inOctets := uint64(len(rawIn))

	if arr.Corrupt {
		for _, p := range inPorts {
			f.countCorruptIngress(arr.Device, p, inOctets)
		}

		var cable *Cable
		if arr.Port == "" {
			cable = f.portCable(arr.Device, arr.Port)
		}
		dropEntry := Entry{
			At:     arr.At,
			Kind:   EntryDrop,
			Device: arr.Device,
			Port:   arr.Port,
			Reason: cablemodel.ReasonBadFrame,
			Cable:  cable,
		}
		f.record(journey, dropEntry)

		return dropEntry, true
	}

	inClass := classifyMAC(arr.Frame.Dst)
	for _, p := range inPorts {
		f.countIngress(arr.Device, p, inOctets, inClass)
	}
	// A SPAN mirror copy carries a non-empty Origin.Mirror and has already
	// passed the original ingress policy, so a downstream switch must not charge
	// its bytes again. A reflection carries OriginReflection for provenance but no
	// mirror name; it is a first-class datagram the policer must still see.
	var out device.Output
	if journey.Origin.Mirror == "" {
		out = sw.Receive(arr.At, arr.Port, arr.Frame)
	} else {
		out = sw.ReceiveMirrored(arr.At, arr.Port, arr.Frame)
	}
	res := out.Result
	var acceptanceEntry *Entry
	if res.Acceptance != device.NoAcceptance {
		e := f.recordAcceptance(journey, arr.Device, arr.Port, arr.Frame, arr.At, res)
		acceptanceEntry = &e
	}
	if out.Err != nil {
		f.recordFault(out.Err)
	}
	if out.Hold != 0 {
		f.held[holdKey{node: arr.Device, token: out.Hold}] = arr.FrameID
		f.inflightAdd(arr.FrameID)
	}

	f.scheduleWake(arr.Device)
	if out.Policed {
		for _, p := range inPorts {
			f.countWholeFrameDrop(arr.Device, p, res.Reason)
		}
		dropEntry := Entry{
			At: arr.At, Kind: EntryDrop, Device: arr.Device,
			Port: arr.Port, Result: &res, Reason: res.Reason,
		}
		f.record(journey, dropEntry)
		return dropEntry, true
	}

	hopEntry := Entry{
		At:     arr.At,
		Kind:   EntryHop,
		Device: arr.Device,
		Port:   arr.Port,
		Result: cloneResult(res),
	}
	if acceptanceEntry == nil {
		f.record(journey, hopEntry)
	}

	if res.Outcome == trace.Dropped && acceptanceEntry == nil {
		for _, p := range inPorts {
			f.countWholeFrameDrop(arr.Device, p, res.Reason)
		}

		dropEntry := Entry{
			At:     arr.At,
			Kind:   EntryDrop,
			Device: arr.Device,
			Port:   arr.Port,
			Reason: res.Reason,
		}
		f.record(journey, dropEntry)
	}

	for _, eg := range res.Egress {
		var outPorts []string
		outPorts = append(outPorts, eg.Port)
		if eg.Member != "" {
			outPorts = append(outPorts, eg.Member)
		}

		if eg.Dropped != "" {
			for _, p := range outPorts {
				f.countEgressDrop(arr.Device, p, eg.Dropped)
			}
			f.record(journey, Entry{
				At:     arr.At,
				Kind:   EntryDrop,
				Device: arr.Device,
				Port:   eg.Port,
				Reason: eg.Dropped,
			})

			continue
		}

		f.transmit(arr.At, arr.Device, eg.Port, eg.Member, eg.Frame, arr.Seq, arr.FrameID, journey, eg.PCP, "")
	}

	f.applyDeviceOutput(arr.At, arr.Device, out, journey)

	if acceptanceEntry != nil {
		return *acceptanceEntry, true
	}
	return hopEntry, true
}

// cloneEntered returns an independent copy of a frame's re-entry set, so a
// reflected copy's set can be seeded from its parent's without the two ever
// sharing one map.
func cloneEntered(src map[Endpoint]bool) map[Endpoint]bool {
	cp := make(map[Endpoint]bool, len(src))
	for ep, v := range src {
		cp[ep] = v
	}

	return cp
}

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

// inflightAdd records that fid gained an in-flight arrival and marks it for
// settlement at the end of the calling Inject or Step.
func (f *Fabric) inflightAdd(fid FrameID) {
	if f.inflight == nil {
		f.inflight = make(map[FrameID]int)
	}
	f.inflight[fid]++
	f.touched = append(f.touched, fid)
}

// inflightRemove records that fid lost an in-flight arrival and marks it for
// settlement, so a frame whose last arrival left folds at the end of the call
// rather than at the decrement.
func (f *Fabric) inflightRemove(fid FrameID) {
	if n := f.inflight[fid]; n > 1 {
		f.inflight[fid] = n - 1
	} else {
		delete(f.inflight, fid)
	}
	f.touched = append(f.touched, fid)
}

// touch marks fid for settlement without changing its in-flight count.
func (f *Fabric) touch(fid FrameID) {
	f.touched = append(f.touched, fid)
}

// settleTouched settles every frame the current call's count changes touched,
// folding and, for aggregate retention, freeing each journey whose last
// in-flight arrival has left.
func (f *Fabric) settleTouched() {
	if len(f.touched) == 0 {
		return
	}
	touched := f.touched
	f.touched = nil
	for _, fid := range touched {
		f.settle(fid)
	}
}

// recordFault stores the first scheduling fault. Later faults are dropped so
// Err reports the one that ended the run, not whatever followed it.
func (f *Fabric) recordFault(err error) {
	if f.err == nil {
		f.err = err
	}
}

// Err reports the first scheduling-invariant breach a run recorded, or nil if
// none occurred. A short [Fabric.Run] count or a false [Fabric.Step] means
// either an empty queue or a fault; Err is how a caller tells them apart.
func (f *Fabric) Err() error {
	return f.err
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

// applyDeviceOutput keeps token resolutions until every emission, drop, and
// copy has used its parent. Device tokens can repeat across nodes.
func (f *Fabric) applyDeviceOutput(now time.Time, node string, out device.Output, current *Journey) {
	f.initRunState()
	if out.Err != nil {
		f.recordFault(out.Err)
	}
	var resolved map[device.HoldToken]*Journey
	resolve := func(token device.HoldToken, ingress string, frame ethernet.Frame, protocol bool) *Journey {
		if token != 0 {
			if j := resolved[token]; j != nil {
				return j
			}
		}
		var j *Journey
		if fid := f.held[holdKey{node: node, token: token}]; token != 0 && fid != 0 {
			j = f.journeys[fid]
		}
		if j == nil {
			fid := f.nextFrameID
			f.nextFrameID++
			j = &Journey{
				FrameID: fid, Protocol: protocol, Origin: JourneyOrigin{Kind: OriginInjection},
				Injection: Injection{At: now, Origin: Endpoint{Node: node, Port: ingress}, Frame: cloneFrame(frame)},
			}
			f.journeys[fid] = j
			f.touch(fid)
			f.record(j, Entry{At: now, Kind: EntryInjection, Device: node, Port: ingress, Cable: f.portCable(node, ingress)})
		}
		if token != 0 {
			if resolved == nil {
				resolved = make(map[device.HoldToken]*Journey)
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
		if drop.Port != "" {
			f.countEgressDrop(node, drop.Port, drop.Reason)
		}
		step := drop.Step
		f.record(j, Entry{At: now, Kind: EntryDrop, Device: node, Port: drop.Port, Reason: drop.Reason, Step: &step})
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
			Frame:     cloneFrame(copy.Frame),
			Retention: parent.Injection.Retention,
			Flow:      parent.Injection.Flow,
		}
		kind := OriginMirror
		if out.Acceptance == device.ReflectionAcceptance {
			kind = OriginReflection
		}
		copyJourney := &Journey{
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

// Run repeatedly invokes [Fabric.Step] until the arrival queue is empty, budget steps have executed,
// or a scheduling fault or convergence condition halts simulation.
// A non-positive budget executes zero steps and returns [StopNotRun].
// A positive window enables fingerprint convergence and cycle detection.
// A zero window computes no fingerprints.
func (f *Fabric) Run(options RunOptions) RunResult {
	return f.run(options.Budget, options.Window)
}

// RunOptions bounds execution independently of its action schedule.
type RunOptions struct {
	// Budget bounds the number of executed steps.
	Budget int
	// Window enables convergence detection when positive. Zero skips fingerprints.
	Window int
}

// Validate requires a positive step budget and a nonnegative observation window.
func (o RunOptions) Validate() error {
	if o.Budget <= 0 {
		return errs.Msgf("run budget must be positive, got %d", o.Budget)
	}
	if o.Window < 0 {
		return errs.Msgf("run window cannot be negative, got %d", o.Window)
	}
	return nil
}

// RunScenario validates and executes a declared scenario under options.
// It refuses any action before the fabric clock and retains a pre-action checkpoint for replay.
func (f *Fabric) RunScenario(s Scenario, options RunOptions) (RunResult, error) {
	if err := options.Validate(); err != nil {
		return RunResult{}, err
	}
	norm, err := s.Normalize()
	if err != nil {
		return RunResult{}, err
	}
	if err := norm.Validate(); err != nil {
		return RunResult{}, err
	}
	if err := f.validateActionTimes(norm.Actions); err != nil {
		return RunResult{}, err
	}

	checkpoint := f.Fork()

	res, err := f.runWithActions(options.Budget, options.Window, norm.Actions)
	res.Replay = ReplaySpec{
		checkpoint: checkpoint,
		Scenario:   norm.Clone(),
		Options:    options,
	}
	return res, err
}

// Replay executes the scenario on a fresh fork of its checkpoint.
// It refuses a zero replay, which has no checkpoint.
func Replay(spec ReplaySpec) (RunResult, error) {
	fab := spec.Checkpoint()
	if fab == nil {
		return RunResult{}, errs.Msg("replay refused: missing checkpoint")
	}
	return fab.RunScenario(spec.Scenario, spec.Options)
}

func (f *Fabric) validateActionTimes(actions []Action) error {
	for i, a := range actions {
		if a.At.Before(f.clock) {
			return errs.Msgf("action %d at %s precedes fabric clock %s", i, a.At, f.clock)
		}
	}
	return nil
}

func (f *Fabric) applyAction(a Action) error {
	switch a.Kind {
	case ActionInject:
		_, err := f.Inject(*a.Inject)
		return err
	case ActionFault:
		if a.At.After(f.clock) {
			f.clock = a.At
		}
		return f.SetFault(a.Fault.A, a.Fault.B, a.Fault.Fault)
	case ActionMcheck:
		if a.At.After(f.clock) {
			f.clock = a.At
		}
		return a.Mcheck.Operation.Apply(f, a.Mcheck.Node)
	case ActionRecord:
		rec := a.Record
		inj := Injection{
			At:     rec.At,
			Origin: rec.Origin,
			Frame:  *rec.Frame,
		}
		fid, err := f.Inject(inj)
		if err != nil {
			return err
		}
		if rec.OriginalLen > rec.CapturedLen {
			if j := f.journeys[fid]; j != nil {
				j.State = JourneyTruncated
				issue := analysis.Issue{
					Code:    IssueTruncatedRecord,
					Status:  analysis.Incomplete,
					Scope:   analysis.WholeScope(),
					Message: fmt.Sprintf("record from source %q truncated: captured %d octets of %d", rec.Source, rec.CapturedLen, rec.OriginalLen),
				}
				j.Metadata = analysis.NewMetadata(
					analysis.WholeScope(),
					append(j.Metadata.Issues(), issue),
					j.Metadata.Evidence(),
					j.Metadata.Assumptions(),
				)
			}
		}
		return nil
	case ActionConfigure:
		if a.At.After(f.clock) {
			f.clock = a.At
		}
		return a.Configure.Operation.Apply(f, a.Configure.Node)
	default:
		return errs.Msgf("unknown action kind %q", a.Kind)
	}
}

func (f *Fabric) run(budget int, window int) RunResult {
	res, _ := f.runWithActions(budget, window, nil)
	return res
}

func (f *Fabric) runWithActions(budget int, window int, actions []Action) (RunResult, error) {
	f.initRunState()

	f.enqueueActions(actions)

	if budget <= 0 {
		return f.buildRunResult(StopNotRun, 0, nil, nil, budget, len(actions)), nil
	}

	if f.err != nil {
		return f.buildRunResult(StopFault, 0, nil, nil, budget, len(actions)), nil
	}

	var (
		fingerprints            []string
		cycle                   []string
		stop                    StopReason
		steps                   int
		lastFingerprint         string
		wakeArrivalsSinceChange int
		roots                   []runRoot
		actionOrdinal           int
		actionErr               error
	)
	if window > 0 {
		lastFingerprint = f.Fingerprint()
	}

	for steps < budget {
		if f.err != nil {
			stop = StopFault
			break
		}

		if len(f.queue) == 0 {
			stop = StopQueueDrained
			break
		}

		arr := f.queue[0]
		fid := f.nextFrameID
		ordinal := 0
		if arr.Kind == ArrivalStream {
			ordinal = f.attachments[arr.source].ordinal
		}
		entry, ok := f.Step()
		if !ok {
			if arr.Kind == ArrivalAction && f.err != nil {
				actionErr = f.err
			}
			if f.err != nil {
				stop = StopFault
			} else {
				stop = StopQueueDrained
			}
			break
		}
		steps++
		if arr.Kind == ArrivalAction && (arr.action.Kind == ActionInject || arr.action.Kind == ActionRecord) && f.nextFrameID > fid {
			root := runRoot{fid: fid, ordinal: actionOrdinal, injection: &InjectionLocation{Index: arr.action.Index, At: arr.At.Round(0).UTC()}}
			actionOrdinal++
			if arr.action.Inject != nil {
				root.flow, root.retention = arr.action.Inject.Flow, arr.action.Inject.Retention
			}
			roots = append(roots, root)
		}
		if arr.Kind == ArrivalStream {
			if f.nextFrameID > fid {
				att := f.attachments[arr.source].attachment
				roots = append(roots, runRoot{fid: fid, flow: att.Flow, retention: att.Retention, source: &SourceLocation{Attachment: arr.source, Frame: ordinal}})
			}
		}

		if window > 0 {
			fp := f.Fingerprint()
			fingerprints = append(fingerprints, fp)
			if fp != lastFingerprint {
				lastFingerprint = fp
				wakeArrivalsSinceChange = 0
			}
			if entry.Kind == EntryWake {
				wakeArrivalsSinceChange++
			}

			if window >= 2 && len(fingerprints) >= 2*2 {
				if foundCycle, ok := detectCycle(fingerprints, window); ok {
					stop = StopOscillating
					cycle = foundCycle
					break
				}
			}

			if wakeArrivalsSinceChange >= window && f.queueHoldsOnlyWakes() && !f.hasPendingJourneys() && !f.sourcesPending() {
				stop = StopConverged
				break
			}
		}

		if f.err != nil {
			stop = StopFault
			break
		}
	}

	if stop == "" {
		stop = StopBudget
	}

	res := f.buildRunResult(stop, steps, fingerprints, cycle, budget, f.pendingActionCount())
	res.roots = roots
	return res, actionErr
}

func detectCycle(fps []string, window int) ([]string, bool) {
	n := len(fps)
	maxPeriod := window
	for p := 2; p <= maxPeriod && 2*p <= n; p++ {
		match := true
		for i := 0; i < p; i++ {
			if fps[n-2*p+i] != fps[n-p+i] {
				match = false
				break
			}
		}
		if match {
			allSame := true
			first := fps[n-p]
			for i := 1; i < p; i++ {
				if fps[n-p+i] != first {
					allSame = false
					break
				}
			}
			if !allSame {
				cycle := make([]string, p)
				copy(cycle, fps[n-p:n])
				return cycle, true
			}
		}
	}
	return nil, false
}

func (f *Fabric) queueHoldsOnlyWakes() bool {
	if len(f.queue) == 0 {
		return false
	}
	for _, arr := range f.queue {
		if arr.Kind != ArrivalWake {
			return false
		}
	}
	for _, q := range f.egress {
		if q != nil && q.pendingCount() > 0 {
			return false
		}
	}
	return true
}

func (f *Fabric) hasPendingJourneys() bool {
	return len(f.inflight) != 0
}

func (f *Fabric) pendingWork() PendingWork {
	var arrivals, wakes, egress, journeys, inputs int
	for _, arr := range f.queue {
		switch arr.Kind {
		case ArrivalFrame:
			arrivals++
		case ArrivalWake:
			wakes++
		case ArrivalHost, ArrivalStream, ArrivalAction:
			inputs++
		}
	}
	for _, q := range f.egress {
		if q != nil {
			egress += q.pendingCount()
		}
	}
	journeys = len(f.inflight)
	return PendingWork{
		Arrivals: arrivals,
		Inputs:   inputs,
		Wakes:    wakes,
		Egress:   egress,
		Journeys: journeys,
	}
}

func (f *Fabric) buildRunResult(stop StopReason, steps int, fingerprints, cycle []string, budget int, droppedActions int) RunResult {
	var issues []analysis.Issue
	for _, id := range slices.Sorted(maps.Keys(f.journeys)) {
		issues = append(issues, f.journeys[id].Metadata.Issues()...)
	}
	for _, id := range slices.Sorted(maps.Keys(f.flows)) {
		issues = append(issues, f.flows[id].Metadata.Issues()...)
	}
	var uniqueIssues []analysis.Issue
	for _, iss := range issues {
		if !slices.ContainsFunc(uniqueIssues, func(kept analysis.Issue) bool { return analysis.SameIssue(kept, iss) }) {
			uniqueIssues = append(uniqueIssues, iss)
		}
	}

	if stop == StopBudget {
		uniqueIssues = append(uniqueIssues, analysis.Issue{
			Code:    IssueBudgetExhausted,
			Status:  analysis.Exhausted,
			Scope:   analysis.WholeScope(),
			Message: fmt.Sprintf("step budget %d exhausted", budget),
		})
	}
	if droppedActions > 0 {
		noun := "actions"
		if droppedActions == 1 {
			noun = "action"
		}
		uniqueIssues = append(uniqueIssues, analysis.Issue{
			Code:    IssueActionsDropped,
			Status:  analysis.Exhausted,
			Scope:   analysis.WholeScope(),
			Message: fmt.Sprintf("%d timed %s did not fire before the run stopped", droppedActions, noun),
		})
	}
	if f.err != nil {
		uniqueIssues = append(uniqueIssues, analysis.Issue{
			Code:    IssueSchedulingFault,
			Status:  analysis.Unsupported,
			Scope:   analysis.WholeScope(),
			Message: f.err.Error(),
		})
	}
	if stop == StopOscillating {
		uniqueIssues = append(uniqueIssues, analysis.Issue{
			Code:    IssueOscillating,
			Status:  analysis.Unstable,
			Scope:   analysis.WholeScope(),
			Message: "fingerprint sequence oscillating in periodic cycle",
		})
	}

	status := analysis.Summarize(uniqueIssues)
	canonicalIssues := analysis.CanonicalIssues(uniqueIssues)

	return RunResult{
		Stop:           stop,
		Steps:          steps,
		Clock:          f.clock,
		Pending:        f.pendingWork(),
		Status:         status,
		Issues:         canonicalIssues,
		Err:            f.err,
		Replay:         ReplaySpec{},
		Fingerprints:   fingerprints,
		Cycle:          cycle,
		DroppedActions: droppedActions,
	}
}

// Snapshot captures the current simulation clock, queued arrivals and egress frames, link states, switch subsystem
// states, and Busy, the endpoints whose transmission ends after the clock.
func (f *Fabric) Snapshot() Snapshot {
	var q []Arrival
	if len(f.queue) > 0 {
		q = slices.Clone(f.queue)
		slices.SortFunc(q, compareArrival)
		for i := range q {
			q[i].index = 0
		}
	}

	links := f.Links()

	devices := make(map[string]device.State, len(f.switchNames()))
	for _, name := range f.switchNames() {
		state := f.nodeDevice(name).State(f.clock)
		state.Counters = make(map[string]device.PortCounters)
		for portName, counters := range f.snapshotCounters(name) {
			state.Counters[portName] = device.PortCounters(counters)
		}
		devices[name] = state
	}

	var busy map[Endpoint]time.Time
	for ep, until := range f.busyUntil {
		if until.After(f.clock) {
			if busy == nil {
				busy = make(map[Endpoint]time.Time)
			}
			busy[ep] = until
		}
	}
	var queued map[Endpoint]int
	var egressDepths map[Endpoint]map[vlan.PCP]EgressDepth
	for ep, eq := range f.egress {
		if count := eq.pendingCount(); count > 0 {
			if queued == nil {
				queued = make(map[Endpoint]int)
			}
			queued[ep] = count
		}
		for pcp := range eq.depth {
			if eq.depth[pcp] == 0 && eq.peak[pcp] == 0 {
				continue
			}
			if egressDepths == nil {
				egressDepths = make(map[Endpoint]map[vlan.PCP]EgressDepth)
			}
			if egressDepths[ep] == nil {
				egressDepths[ep] = make(map[vlan.PCP]EgressDepth)
			}
			egressDepths[ep][vlan.PCP(pcp)] = EgressDepth{Depth: eq.depth[pcp], Peak: eq.peak[pcp]}
		}
	}

	return Snapshot{
		Clock:        f.clock,
		Queue:        q,
		Queued:       queued,
		Links:        links,
		Devices:      devices,
		Busy:         busy,
		EgressDepths: egressDepths,
	}
}

func (f *Fabric) initRunState() {
	if f.journeys == nil {
		f.journeys = make(map[FrameID]*Journey)
	}
	if f.entered == nil {
		f.entered = make(map[FrameID]map[Endpoint]bool)
	}
	if f.cableCrossings == nil {
		f.cableCrossings = make(map[Endpoint]uint)
	}
	if f.busyUntil == nil {
		f.busyUntil = make(map[Endpoint]time.Time)
	}
	if f.egress == nil {
		f.egress = make(map[Endpoint]*egressQueue)
	}
	f.initQueueIndexes()
	if f.wakes == nil {
		f.wakes = make(map[string]time.Time)
	}
	if f.inflight == nil {
		f.inflight = make(map[FrameID]int)
	}
	if f.held == nil {
		f.held = make(map[holdKey]FrameID)
	}
	if f.nextFrameID == 0 {
		f.nextFrameID = 1
	}
	if f.nextSeq == 0 {
		f.nextSeq = 1
	}
}

func (f *Fabric) pendingActionCount() int {
	count := 0
	for _, arr := range f.queue {
		if arr.Kind == ArrivalAction {
			count++
		}
	}
	return count
}
