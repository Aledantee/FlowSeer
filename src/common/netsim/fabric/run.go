package fabric

import (
	"math"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Injection specifies a frame to introduce into the fabric at a particular origin endpoint and timestamp.
type Injection struct {
	At     time.Time
	Origin Endpoint
	Frame  ethernet.Frame
}

// Device represents the instantaneous subsystem state of a virtual switch in the fabric.
type Device struct {
	Entries  []bridge.Entry
	Ports    []port.Port
	Power    phy.Allocation
	Counters map[string]Counters
}

// Snapshot captures an instantaneous view of simulation time, in-flight arrivals, physical links, and device states.
type Snapshot struct {
	Clock   time.Time
	Queue   []Arrival
	Links   []Link
	Devices map[string]Device
}

// Inject queues a frame for introduction into the fabric at the requested origin endpoint and time.
//
// For a host origin, the host's configured VLAN form is applied (adding its C-TAG or leaving the frame untagged)
// and the arrival is scheduled on the switch port connected to the host. For a device port origin, the frame is
// queued directly as given. Inject returns an error if the origin names an unknown host, an unknown switch port,
// or a host without a connected cable.
func (f *Fabric) Inject(inj Injection) (FrameID, error) {
	f.initRunState()

	var (
		targetDevice string
		targetPort   string
		frame        = cloneFrame(inj.Frame)
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

		// A host emits one form only, so its tag replaces whatever the caller
		// put on the frame; a second tag would be a form no host port emits.
		if host.VLAN != nil {
			frame.Tags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: *host.VLAN}}
		} else {
			frame.Tags = nil
		}

		targetDevice = ref.peer.Node
		targetPort = ref.peer.Port
	} else if swCfg, isSwitch := f.cfg.Switches[inj.Origin.Node]; isSwitch {
		if _, ok := swCfg.Ports.Port(inj.Origin.Port); !ok {
			return 0, errs.New().
				Attr("node", inj.Origin.Node).
				Attr("port", inj.Origin.Port).
				Msgf("port %q not found on switch %q", inj.Origin.Port, inj.Origin.Node)
		}

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

		rawOut, _ := eg.Frame.Encode()
		outOctets := uint64(len(rawOut))
		outClass := classifyMAC(eg.Frame.Dst)
		for _, p := range outPorts {
			f.countEgress(arr.Device, p, outOctets, outClass)
		}

		outPort := eg.Port
		if eg.Member != "" {
			outPort = eg.Member
		}

		ref, ok := f.linkEnd(arr.Device, outPort)
		if !ok {
			continue
		}

		cable := ref.link.Cable
		latency := cableLatency(cable.LengthMeters)

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
				At:     arr.At,
				Kind:   EntryLoss,
				Cable:  &cableCopy,
				Reason: ReasonCableLoss,
			}
			journey.Entries = append(journey.Entries, lossEntry)

			continue
		}

		farEnd := ref.peer.Endpoint
		deliveryAt := arr.At.Add(latency)

		if _, isHost := f.cfg.Hosts[farEnd.Node]; isHost {
			if corrupt {
				journey.Entries = append(journey.Entries, Entry{
					At:     deliveryAt,
					Kind:   EntryDrop,
					Device: farEnd.Node,
					Cable:  &cableCopy,
					Reason: ReasonBadFrame,
				})

				continue
			}
			del := Delivery{
				Host:  farEnd.Node,
				At:    deliveryAt,
				Frame: cloneFrame(eg.Frame),
			}
			journey.Deliveries = append(journey.Deliveries, del)

			delEntry := Entry{
				At:     deliveryAt,
				Kind:   EntryDelivery,
				Device: farEnd.Node,
			}
			journey.Entries = append(journey.Entries, delEntry)

			continue
		}

		crossingEntry := Entry{
			At:      arr.At,
			Kind:    EntryCrossing,
			Device:  farEnd.Node,
			Port:    farEnd.Port,
			Cable:   &cableCopy,
			Latency: latency,
		}
		journey.Entries = append(journey.Entries, crossingEntry)

		// A copy keeps its injection's sequence, so two copies of one frame
		// that arrive at the same instant fall through to the device and
		// port order rather than to the order the bridge listed them in.
		f.enqueue(Arrival{
			At:      deliveryAt,
			Seq:     arr.Seq,
			Device:  farEnd.Node,
			Port:    farEnd.Port,
			FrameID: arr.FrameID,
			Frame:   cloneFrame(eg.Frame),
			Corrupt: corrupt,
		})
	}

	return hopEntry, true
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

// Snapshot captures the current simulation clock, queued arrivals, link states, and switch subsystem states.
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
			Entries:  sw.Entries(),
			Ports:    sw.Ports().Ports(),
			Power:    sw.Power(),
			Counters: f.snapshotCounters(name),
		}
	}

	return Snapshot{
		Clock:   f.clock,
		Queue:   q,
		Links:   links,
		Devices: devices,
	}
}

func cableLatency(lengthMeters float64) time.Duration {
	if lengthMeters <= 0 {
		return 0
	}

	const lightSpeedMPS = 299792458.0
	seconds := lengthMeters / ((2.0 / 3.0) * lightSpeedMPS)
	nanoseconds := math.Round(seconds * 1e9)

	return time.Duration(nanoseconds) * time.Nanosecond
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
	if f.nextFrameID == 0 {
		f.nextFrameID = 1
	}
	if f.nextSeq == 0 {
		f.nextSeq = 1
	}
}
