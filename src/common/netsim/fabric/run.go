package fabric

import (
	"cmp"
	"net/netip"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// Packet specifies an IP datagram destination address, protocol, and payload to originate
// from a host with an IP stack.
type Packet struct {
	To       netip.Addr
	Protocol uint8
	Payload  []byte
}

// Injection specifies a frame or packet to introduce into the fabric at a particular origin endpoint and timestamp.
type Injection struct {
	At     time.Time
	Origin Endpoint
	Frame  ethernet.Frame
	Packet *Packet
}

// Device represents the instantaneous subsystem state of a virtual switch in the fabric.
type Device struct {
	Entries  []bridge.Entry
	Ports    []port.Port
	Power    phy.Allocation
	Counters map[string]Counters
	Roles    map[string]stp.PortInfo
	// RelayCounters is what the relay's learning table counted, beside the
	// per-port Counters.
	RelayCounters bridge.Counters
}

// Snapshot captures an instantaneous view of simulation time, in-flight arrivals, physical links, device states,
// and the endpoints still transmitting past the clock.
type Snapshot struct {
	Clock   time.Time
	Queue   []Arrival
	Links   []Link
	Devices map[string]Device
	Busy    map[Endpoint]time.Time
}

func wireOctets(frame ethernet.Frame) int {
	raw, _ := frame.Encode()
	n := len(raw)
	minOctets := 60 + 4*len(frame.Tags)
	if n < minOctets {
		n = minOctets
	}

	return n + 24
}

// serialization is the time the frame occupies the wire at rateBPS. Every
// caller transmits on a link that negotiated, so the rate is never 0.
func serialization(frame ethernet.Frame, rateBPS uint64) time.Duration {
	bits := uint64(wireOctets(frame)) * 8
	nanos := (bits*1_000_000_000 + rateBPS - 1) / rateBPS

	return time.Duration(nanos) * time.Nanosecond
}

// Inject queues a frame or originated packet for introduction into the fabric at the requested origin endpoint and time.
//
// For a host origin, the host's configured VLAN form is applied (adding its C-TAG or leaving the frame untagged)
// and the frame is transmitted on the host's cable end: the journey records a crossing, the host end's busy clock
// is charged, and the arrival at the connected switch port is the transmission end plus the cable's propagation.
// When Packet is set, the frame is originated by the host's IP stack and Frame must have no field set. For a device
// port origin, the frame is queued directly as given at At.
// Inject returns an error if the origin names an unknown host, an unknown switch port, a host without a connected cable,
// a host whose link has no negotiated speed (naming the link's reason), a switch origin with Packet set, a host without
// an IP stack when Packet is set, a Packet injection specifying Frame fields, a non-empty origin port for host packet
// injection, or if packet origination fails.
func (f *Fabric) Inject(inj Injection) (FrameID, error) {
	f.initRunState()

	var (
		targetDevice string
		targetPort   string
		frame        ethernet.Frame
		hostRef      *linkEndRef
	)

	if host, isHost := f.cfg.Hosts[inj.Origin.Node]; isHost {
		if inj.Origin.Port != "" {
			return 0, errs.New().
				Attr("node", inj.Origin.Node).
				Attr("port", inj.Origin.Port).
				Msgf("host origin %q must have empty port", inj.Origin.Node)
		}

		ref, ok := f.linkEnd(inj.Origin.Node, "")
		if !ok {
			return 0, errs.New().
				Attr("host", inj.Origin.Node).
				Msgf("host %q has no connected cable", inj.Origin.Node)
		}

		if inj.Packet != nil {
			if inj.Frame.Dst != (netaddr.MAC{}) || inj.Frame.Src != (netaddr.MAC{}) || inj.Frame.EtherType != 0 ||
				len(inj.Frame.Tags) != 0 || len(inj.Frame.Payload) != 0 {
				return 0, errs.New().
					Attr("host", inj.Origin.Node).
					Msg("packet injection cannot specify frame fields")
			}
			stack, ok := f.hostStacks[inj.Origin.Node]
			if !ok {
				return 0, errs.New().
					Attr("host", inj.Origin.Node).
					Msgf("host %q has no IP stack", inj.Origin.Node)
			}
			res := stack.Originate(routing.DefaultVRF, inj.Packet.To, inj.Packet.Protocol, inj.Packet.Payload)
			if res.Reason != "" {
				return 0, errs.New().
					Attr("host", inj.Origin.Node).
					Attr("address", inj.Packet.To).
					Attr("reason", res.Reason).
					Msgf("host %q cannot originate packet to %s: %s", inj.Origin.Node, inj.Packet.To, res.Reason)
			}
			frame = res.Frame
		} else {
			frame = cloneFrame(inj.Frame)
		}

		// A host emits one form only, so its tag replaces whatever the caller
		// put on the frame; a second tag would be a form no host port emits.
		if host.VLAN != nil {
			frame.Tags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: *host.VLAN}}
		} else {
			frame.Tags = nil
		}

		if ref.end.Speed.SpeedBPS == 0 {
			return 0, errs.New().
				Attr("host", inj.Origin.Node).
				Attr("reason", ref.end.Reason).
				Msgf("host %q link is down: %s", inj.Origin.Node, ref.end.Reason)
		}

		hostRef = &ref
	} else if swCfg, isSwitch := f.cfg.Switches[inj.Origin.Node]; isSwitch {
		if inj.Packet != nil {
			return 0, errs.New().
				Attr("switch", inj.Origin.Node).
				Msgf("cannot inject packet at switch origin %q", inj.Origin.Node)
		}
		if _, ok := swCfg.Ports.Port(inj.Origin.Port); !ok {
			return 0, errs.New().
				Attr("node", inj.Origin.Node).
				Attr("port", inj.Origin.Port).
				Msgf("port %q not found on switch %q", inj.Origin.Port, inj.Origin.Node)
		}

		frame = cloneFrame(inj.Frame)
		targetDevice = inj.Origin.Node
		targetPort = inj.Origin.Port
	} else {
		return 0, errs.New().
			Attr("node", inj.Origin.Node).
			Msgf("origin node %q not found in fabric", inj.Origin.Node)
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
		Injection: inj,
		Entries: []Entry{
			{
				At:     inj.At,
				Kind:   EntryInjection,
				Device: inj.Origin.Node,
				Port:   inj.Origin.Port,
			},
		},
	}
	f.journeys[fid] = journey

	if hostRef != nil {
		f.transmitCable(inj.At, hostRef.end.Endpoint, *hostRef, frame, seq, fid, journey)
	} else {
		arr := Arrival{
			At:      inj.At,
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

// Step advances simulation time to the earliest queued arrival and processes it through the destination device.
//
// If the arrival device port was previously visited by the frame, a loop entry is recorded before processing.
// Corrupted arrivals are discarded with [ReasonBadFrame]. Normal arrivals trigger dynamic MAC aging and bridge
// forwarding, enqueuing un-dropped egress frames onto connected cables or delivering them immediately to target hosts.
// Step returns false when the arrival queue is empty.
func (f *Fabric) Step() (Entry, bool) {
	if len(f.queue) == 0 {
		return Entry{}, false
	}
	f.initRunState()

	arr := f.queue[0]
	f.queue = f.queue[1:]
	f.clock = arr.At

	if arr.Wake {
		delete(f.wakes, arr.Device)
		sw := f.switches[arr.Device]
		if sw != nil {
			sw.Wake(arr.At)
			for _, em := range sw.Drain() {
				f.injectEmission(arr.At, arr.Device, em)
			}
			f.scheduleWake(arr.Device)
		}

		return Entry{
			At:     arr.At,
			Kind:   EntryWake,
			Device: arr.Device,
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
		journey.Entries = append(journey.Entries, loopEntry)
	}
	f.entered[arr.FrameID][ep] = true

	sw := f.switches[arr.Device]
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

		dropEntry := Entry{
			At:     arr.At,
			Kind:   EntryDrop,
			Device: arr.Device,
			Port:   arr.Port,
			Reason: ReasonBadFrame,
		}
		journey.Entries = append(journey.Entries, dropEntry)

		return dropEntry, true
	}

	inClass := classifyMAC(arr.Frame.Dst)
	for _, p := range inPorts {
		f.countIngress(arr.Device, p, inOctets, inClass)
	}

	sw.Age(arr.At)
	res := sw.Forward(arr.At, arr.Port, arr.Frame)

	for _, em := range sw.Drain() {
		f.injectEmission(arr.At, arr.Device, em)
	}
	f.scheduleWake(arr.Device)

	hopEntry := Entry{
		At:     arr.At,
		Kind:   EntryHop,
		Device: arr.Device,
		Port:   arr.Port,
		Result: cloneResult(res),
	}
	journey.Entries = append(journey.Entries, hopEntry)

	if res.Outcome == trace.Dropped {
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
		journey.Entries = append(journey.Entries, dropEntry)
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
			journey.Entries = append(journey.Entries, Entry{
				At:     arr.At,
				Kind:   EntryDrop,
				Device: arr.Device,
				Port:   eg.Port,
				Reason: eg.Dropped,
			})

			continue
		}

		f.transmit(arr.At, arr.Device, eg.Port, eg.Member, eg.Frame, arr.Seq, arr.FrameID, journey)
	}

	return hopEntry, true
}

func (f *Fabric) transmit(now time.Time, device, portName, memberName string, frame ethernet.Frame, seq uint64, fid FrameID, journey *Journey) {
	outPort := portName
	if memberName != "" {
		outPort = memberName
	} else if sw, ok := f.switches[device]; ok {
		if p, ok := sw.Ports().Port(portName); ok && p.Kind == port.Lag {
			mems := sw.Ports().Members(portName)
			var fwdMembers []port.Port
			for _, m := range mems {
				if m.Forwards() {
					fwdMembers = append(fwdMembers, m)
				}
			}
			if len(fwdMembers) == 0 {
				return
			}
			slices.SortFunc(fwdMembers, func(i, j port.Port) int {
				return cmp.Compare(i.Name, j.Name)
			})
			memberName = fwdMembers[0].Name
			outPort = memberName
		}
	}

	rawOut, _ := frame.Encode()
	outOctets := uint64(len(rawOut))
	outClass := classifyMAC(frame.Dst)

	outPorts := []string{portName}
	if memberName != "" {
		outPorts = append(outPorts, memberName)
	}
	for _, p := range outPorts {
		f.countEgress(device, p, outOctets, outClass)
	}

	ref, ok := f.linkEnd(device, outPort)
	if !ok {
		return
	}

	f.transmitCable(now, ref.end.Endpoint, ref, frame, seq, fid, journey)
}

func (f *Fabric) transmitCable(now time.Time, txEnd Endpoint, ref linkEndRef, frame ethernet.Frame, seq uint64, fid FrameID, journey *Journey) {
	cable := ref.link.Cable
	rate := ref.end.Speed.SpeedBPS
	ser := serialization(frame, rate)

	start := now
	if busy, ok := f.busyUntil[txEnd]; ok && busy.After(start) {
		start = busy
	}
	wait := start.Sub(now)
	end := start.Add(ser)
	f.busyUntil[txEnd] = end

	prop := Propagation(cable.LengthMeters, cable.Medium)
	if cable.Delay != nil {
		prop = *cable.Delay
	}
	deliveryAt := end.Add(prop)

	f.cableCrossings[cable.A]++
	count := f.cableCrossings[cable.A]

	var (
		lost    bool
		corrupt bool
	)
	switch cable.Fault.Kind {
	case FaultLoseEveryNth:
		if cable.Fault.N > 0 && count%cable.Fault.N == 0 {
			lost = true
		}
	case FaultLoseSequence:
		if slices.Contains(cable.Fault.Sequence, count) {
			lost = true
		}
	case FaultCorruptEveryNth:
		if cable.Fault.N > 0 && count%cable.Fault.N == 0 {
			corrupt = true
		}
	case FaultDeadAToB:
		if ref.end.Endpoint == cable.A {
			lost = true
		}
	case FaultDeadBToA:
		if ref.end.Endpoint == cable.B {
			lost = true
		}
	}

	cableCopy := cable.Clone()

	if lost {
		lossEntry := Entry{
			At:     now,
			Kind:   EntryLoss,
			Cable:  &cableCopy,
			Reason: ReasonCableLoss,
		}
		journey.Entries = append(journey.Entries, lossEntry)

		return
	}

	farEnd := ref.peer.Endpoint
	if _, isHost := f.cfg.Hosts[farEnd.Node]; isHost {
		if corrupt {
			journey.Entries = append(journey.Entries, Entry{
				At:     deliveryAt,
				Kind:   EntryDrop,
				Device: farEnd.Node,
				Cable:  &cableCopy,
				Reason: ReasonBadFrame,
			})

			return
		}
		del := Delivery{
			Host:  farEnd.Node,
			At:    deliveryAt,
			Frame: cloneFrame(frame),
		}
		journey.Deliveries = append(journey.Deliveries, del)

		delEntry := Entry{
			At:     deliveryAt,
			Kind:   EntryDelivery,
			Device: farEnd.Node,
		}
		journey.Entries = append(journey.Entries, delEntry)

		return
	}

	crossingEntry := Entry{
		At:            now,
		Kind:          EntryCrossing,
		Device:        farEnd.Node,
		Port:          farEnd.Port,
		Cable:         &cableCopy,
		Latency:       prop,
		Serialization: ser,
		Wait:          wait,
	}
	journey.Entries = append(journey.Entries, crossingEntry)

	// A copy keeps its injection's sequence, so two copies of one frame that
	// arrive at the same instant fall through to the device and port order
	// rather than to the order the bridge listed them in.
	f.enqueue(Arrival{
		At:      deliveryAt,
		Seq:     seq,
		Device:  farEnd.Node,
		Port:    farEnd.Port,
		FrameID: fid,
		Frame:   cloneFrame(frame),
		Corrupt: corrupt,
	})
}

func (f *Fabric) injectEmission(now time.Time, device string, em stp.Emission) {
	f.initRunState()

	inj := Injection{
		At:     now,
		Origin: Endpoint{Node: device, Port: em.Port},
		Frame:  cloneFrame(em.Frame),
	}

	fid := f.nextFrameID
	f.nextFrameID++

	seq := f.nextSeq
	f.nextSeq++

	journey := &Journey{
		FrameID:   fid,
		Protocol:  true,
		Injection: inj,
		Entries: []Entry{
			{
				At:     now,
				Kind:   EntryInjection,
				Device: device,
				Port:   em.Port,
			},
		},
	}
	f.journeys[fid] = journey

	f.transmit(now, device, em.Port, "", em.Frame, seq, fid, journey)
}

func (f *Fabric) scheduleWake(device string) {
	sw, ok := f.switches[device]
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
		Seq:    0,
		Device: device,
		Wake:   true,
	})
}

func (f *Fabric) removeWake(device string) {
	delete(f.wakes, device)
	f.queue = slices.DeleteFunc(f.queue, func(arr Arrival) bool {
		return arr.Wake && arr.Device == device
	})
}

// Run repeatedly invokes [Fabric.Step] until the arrival queue is empty or n steps have executed,
// returning the number of steps taken. A non-positive budget executes zero steps.
func (f *Fabric) Run(n int) int {
	if n <= 0 {
		return 0
	}

	for count := 0; count < n; count++ {
		_, ok := f.Step()
		if !ok {
			return count
		}
	}

	return n
}

// Snapshot captures the current simulation clock, queued arrivals, link states, switch subsystem states, and Busy,
// the endpoints whose transmission ends after the clock.
func (f *Fabric) Snapshot() Snapshot {
	var q []Arrival
	if len(f.queue) > 0 {
		q = make([]Arrival, len(f.queue))
		copy(q, f.queue)
	}

	links := f.Links()

	devices := make(map[string]Device, len(f.switches))
	for name, sw := range f.switches {
		devices[name] = Device{
			Entries:       sw.Entries(),
			Ports:         sw.Ports().Ports(),
			Power:         sw.Power(),
			Counters:      f.snapshotCounters(name),
			Roles:         sw.Roles(),
			RelayCounters: sw.RelayCounters(),
		}
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

	return Snapshot{
		Clock:   f.clock,
		Queue:   q,
		Links:   links,
		Devices: devices,
		Busy:    busy,
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
	if f.wakes == nil {
		f.wakes = make(map[string]time.Time)
	}
	if f.nextFrameID == 0 {
		f.nextFrameID = 1
	}
	if f.nextSeq == 0 {
		f.nextSeq = 1
	}
}
