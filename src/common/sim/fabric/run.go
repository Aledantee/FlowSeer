package fabric

import (
	"math"
	"math/bits"
	"net/netip"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Packet specifies an IP datagram destination address, protocol, and payload to originate
// from a host with an IP stack.
type Packet struct {
	To       netip.Addr
	Protocol uint8
	Payload  []byte
}

// JourneyRetention states whether a settled journey is kept in [Fabric.Report] or
// freed, leaving only its flow's statistics.
type JourneyRetention uint8

const (
	// RetainJourney keeps a settled journey in [Fabric.Report].
	RetainJourney JourneyRetention = iota
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
	Retention JourneyRetention
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

// StepResult identifies the executed queue event and its immediate frame entry.
// A wake or dequeue has an empty Entry.Kind because it is not a journey event.
type StepResult struct {
	Entry
	Arrival ArrivalKind
}

type queued struct {
	frame      ethernet.Frame
	octets     uint64
	seq        uint64
	fid        FrameID
	journey    *journey
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
	return rateInterval((max(encodedOctets(frame), uint64(60+4*len(frame.Tags)))+24)*8, rateBPS)
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

// Step advances simulation time to the earliest queued arrival and processes it through the destination device.
//
// Host transmissions, scenario actions, and stream frames are processed in queue order.
// An event dated before the clock is processed at the clock.
// If the arrival device port was previously visited by the frame, a loop entry is recorded before processing.
// Corrupted arrivals are discarded with [cablemodel.ReasonBadFrame]. An arrival at a reflector runs its acceptance decision
// and, when accepted, originates the reflected copies; it never reaches the switch forwarding path below.
// A switch's normal arrivals are policed before dynamic MAC aging and forwarding, then un-dropped egress frames
// are enqueued onto connected cables or delivered to target hosts.
// Step returns false when the arrival queue is empty, a scheduling fault has
// been recorded, or the arrival was a scenario action the fabric refused;
// [Fabric.Err] is non-nil only for the fault. A refused action is consumed and
// does not fault the fabric, so the next Step proceeds. Only a scenario run
// queues actions, and it returns the refusal as its error.
func (f *Fabric) Step() (StepResult, bool) {
	if f.err != nil || len(f.queue) == 0 {
		return StepResult{}, false
	}
	kind := f.queue[0].Kind
	entry, ok, _ := f.step()
	return StepResult{Entry: entry, Arrival: kind}, ok
}

// step processes the earliest arrival. It reports a scenario action the fabric
// refused in refused and leaves the fabric unfaulted, since the action is
// caller input, not a scheduling invariant.
func (f *Fabric) step() (entry Entry, ok bool, refused error) {
	if f.err != nil {
		return Entry{}, false, nil
	}
	if len(f.queue) == 0 {
		return Entry{}, false, nil
	}
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
			return Entry{}, false, err
		}
		return Entry{At: arr.At, Kind: EntryAction}, true, nil
	}
	if arr.Kind == ArrivalStream {
		f.stepStream(arr.At, arr.source)
		return Entry{At: arr.At, Kind: EntryInjection, Device: arr.Device, Port: arr.Port}, true, nil
	}
	if arr.Kind == ArrivalHost {
		f.injectHostFrame(arr.At, Endpoint{Node: arr.Device}, arr.Frame, arr.pcp, arr.Seq, arr.FrameID, f.journeys[arr.FrameID])
		return Entry{At: arr.At, Kind: EntryInjection, Device: arr.Device}, true, nil
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
			Device: arr.Device,
		}, true, nil
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
			Device: arr.Device,
			Port:   arr.Port,
		}, true, nil
	}

	entry, ok = f.stepFrame(arr)

	return entry, ok, nil
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
// none occurred. A refused scenario action is not a breach and leaves it nil.
// A short [Fabric.Run] count or a false [Fabric.Step] means an empty queue, a
// fault, or a refused action; Err tells a fault from the other two.
func (f *Fabric) Err() error {
	return f.err
}

// RunOptions bounds execution independently of its action schedule.
type RunOptions struct {
	// Budget bounds the number of executed steps.
	Budget int
	// Window enables convergence detection when positive. Zero skips fingerprints.
	Window int
}

func (f *Fabric) stepFrame(arr Arrival) (Entry, bool) {
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
	if p, ok := f.topology.nodes[arr.Device].ports.Port(arr.Port); ok && p.LagParent != "" {
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
		if f.held == nil {
			f.held = make(map[holdKey]FrameID)
		}
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

	return f.forwardFrame(arr, journey, res, out, inPorts, acceptanceEntry)
}

func (f *Fabric) forwardFrame(arr Arrival, journey *journey, res device.Result, out device.Output, inPorts []string, acceptanceEntry *Entry) (Entry, bool) {
	hopEntry := Entry{
		At:     arr.At,
		Kind:   EntryHop,
		Device: arr.Device,
		Port:   arr.Port,
	}
	if journey.Injection.Retention == RetainJourney {
		hopEntry.Result = cloneResult(res)
	}
	if acceptanceEntry == nil {
		dependencyEntry := hopEntry
		dependencyEntry.Result = &res
		journey.Metadata = journey.Metadata.Merge(res.Metadata)
		if scopes := f.dependencies(dependencyEntry); len(scopes) > 0 {
			journey.Metadata = journey.Metadata.Merge(scopedMetadata(f.Metadata(), scopes))
		}
		journey.Entries = append(journey.Entries, hopEntry)
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

func encodedOctets(frame ethernet.Frame) uint64 {
	return uint64(14 + 4*len(frame.Tags) + len(frame.Payload))
}
