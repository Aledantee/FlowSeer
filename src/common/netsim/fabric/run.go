package fabric

import (
	"bytes"
	"fmt"
	"maps"
	"math"
	"math/bits"
	"net/netip"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/udp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
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
	Entries     []bridge.Entry
	Groups      map[vlan.ID][]mcast.Entry
	RouterPorts map[vlan.ID][]mcast.RouterPort
	Ports       []port.Port
	Power       vswitch.PowerResult
	Counters    map[string]Counters
	Roles       map[string]stp.PortInfo
	TreeRoles   map[vlan.ID]map[string]stp.PortInfo
	// RelayCounters is what the relay's learning table counted, beside the
	// per-port Counters.
	RelayCounters bridge.Counters
	Neighbors     []routing.NeighborEntry
}

// Snapshot captures an instantaneous view of simulation time, in-flight arrivals, pending egress frames,
// physical links, device states, and the endpoints still transmitting past the clock.
type Snapshot struct {
	Clock   time.Time
	Queue   []Arrival
	Queued  map[Endpoint]int
	Links   []Link
	Devices map[string]Device
	Busy    map[Endpoint]time.Time
}

type queued struct {
	frame      ethernet.Frame
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
	return rateInterval(uint64(wireOctets(frame))*8, rateBPS)
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
	cable := ref.link.Clone()

	return &cable
}

func (f *Fabric) runStarted() bool {
	return f.stepped && !f.clock.IsZero()
}

// Inject queues a frame or originated packet for introduction into the fabric at the requested origin endpoint and time.
//
// For a host origin, the host's configured VLAN form is applied (adding its C-TAG or leaving the frame untagged)
// and the frame is transmitted on the host's cable end: the journey records a crossing, the host end's busy clock
// is charged, and the arrival at the connected switch port is the transmission end plus the cable's propagation.
// When Packet is set, the frame is originated by the host's IP stack and Frame must have no field set. For a device
// port origin, the frame is queued directly as given at At.
// A host whose link is not Up transmits nothing, and the injection is still valid: the journey records a drop with
// the link's reason at At when the link is Down, and an [EntryUnresolved] with the link's reason when it is Unknown.
// Inject returns an error if the origin names an unknown host, an unknown switch port, a reflector (which has
// no port of its own to inject at), a host without a connected cable, a switch origin with Packet set, a host
// without an IP stack when Packet is set, a Packet injection specifying Frame fields, a non-empty origin port
// for host packet injection, if packet origination fails, or if At precedes the fabric clock after a
// nonzero-time step has run.
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
			res := stack.Originate(inj.At, routing.DefaultVRF, inj.Packet.To, inj.Packet.Protocol, inj.Packet.Payload, true)
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
	} else if _, isReflector := f.cfg.Reflectors[inj.Origin.Node]; isReflector {
		return 0, errs.New().
			Attr("node", inj.Origin.Node).
			Msgf("reflector %q cannot originate an injection: it has no host or switch port to inject at", inj.Origin.Node)
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
	if f.runStarted() && inj.At.Before(f.clock) {
		return 0, errs.New().
			Attr("at", inj.At).
			Attr("clock", f.clock).
			Msg("injection time precedes fabric clock")
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
	f.record(journey, Entry{
		At:     inj.At,
		Kind:   EntryInjection,
		Device: inj.Origin.Node,
		Port:   inj.Origin.Port,
		Cable:  f.portCable(inj.Origin.Node, inj.Origin.Port),
	})

	switch {
	case hostRef != nil && hostRef.end.Oper != port.Up:
		kind := EntryDrop
		if hostRef.end.Oper == port.Unknown {
			kind = EntryUnresolved
		}
		cable := hostRef.link.Clone()
		f.record(journey, Entry{
			At:     inj.At,
			Kind:   kind,
			Device: inj.Origin.Node,
			Cable:  &cable,
			Reason: hostRef.end.Reason,
		})
	case hostRef != nil:
		pcp, _ := frame.Priority()
		f.enqueueEgress(inj.At, hostRef.end.Endpoint, hostRef.end.Port, frame, seq, fid, journey, pcp, "")
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

// Step advances simulation time to the earliest queued arrival and processes it through the destination device.
//
// If the arrival device port was previously visited by the frame, a loop entry is recorded before processing.
// Corrupted arrivals are discarded with [ReasonBadFrame]. An arrival at a reflector runs its acceptance decision
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

	arr := f.queue[0]
	f.queue = f.queue[1:]
	f.clock = arr.At
	f.stepped = true

	if arr.Kind == ArrivalWake {
		delete(f.wakes, arr.Device)
		sw := f.switches[arr.Device]
		if sw != nil {
			sw.Wake(arr.At)
			for _, em := range sw.Drain() {
				f.injectEmission(arr.At, arr.Device, em)
			}
			for _, drop := range sw.DrainNeighborFailures() {
				f.recordNeighborFailure(arr.At, arr.Device, drop)
			}
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

	// A reflector arrival is decided here rather than through the switch
	// lookup below, which is unguarded and would nil-dereference on a
	// reflector's arrival: f.switches holds no entry for it. Corruption is
	// checked first, as the host far-end branch checks it before ever calling
	// arrive, so a corrupt arrival never reaches a clause that could accept
	// it.
	if refl, isReflector := f.cfg.Reflectors[arr.Device]; isReflector {
		if arr.Corrupt {
			dropEntry := Entry{
				At:     arr.At,
				Kind:   EntryDrop,
				Device: arr.Device,
				Port:   arr.Port,
				Reason: ReasonBadFrame,
			}
			f.record(journey, dropEntry)

			return dropEntry, true
		}

		entry := f.arriveReflector(journey, arr.Device, refl, arr.Port, arr.Frame, arr.At)
		if entry.Kind == EntryReflection {
			f.reflectFrame(journey, arr.Device, refl, arr.Port, arr.Frame, arr.At)
		}

		return entry, true
	}

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
		f.record(journey, dropEntry)

		return dropEntry, true
	}

	inClass := classifyMAC(arr.Frame.Dst)
	for _, p := range inPorts {
		f.countIngress(arr.Device, p, inOctets, inClass)
	}
	// A mirror journey has already passed the original ingress policy, so a
	// downstream switch must not charge the copy's bytes again.
	frameWireOctets := wireOctets(arr.Frame)
	if journey.Origin.Kind != OriginMirror && !sw.Police(arr.At, arr.Port, frameWireOctets) {
		for _, p := range inPorts {
			f.countWholeFrameDrop(arr.Device, p, traffic.ReasonPoliced)
		}

		policer := f.cfg.Switches[arr.Device].Traffic.Policers[arr.Port]
		result := sw.ComposeForwardResult(
			bridge.Result{
				Trace: trace.Trace{
					Outcome: trace.Dropped,
					Reason:  traffic.ReasonPoliced,
					Steps: []trace.Step{{
						Layer:   traffic.Layer,
						Op:      trace.OpDrop,
						RuleID:  traffic.RulePolicerRefuse,
						Subject: trace.Subject{Kind: "port", Key: arr.Port},
						Outputs: []trace.Fact{traffic.PolicerDecisionFact(policer.RateBPS, policer.BurstOctets, frameWireOctets, false)},
					}},
				},
				Ingress: arr.Port,
			},
			arr.Port,
		)
		dropEntry := Entry{
			At:     arr.At,
			Kind:   EntryDrop,
			Device: arr.Device,
			Port:   arr.Port,
			Result: &result,
			Reason: traffic.ReasonPoliced,
		}
		f.record(journey, dropEntry)

		return dropEntry, true
	}

	sw.Age(arr.At)
	res := sw.Forward(arr.At, arr.Port, arr.Frame)

	for _, em := range sw.Drain() {
		f.injectEmission(arr.At, arr.Device, em)
	}
	for _, drop := range sw.DrainNeighborFailures() {
		f.recordNeighborFailure(arr.At, arr.Device, drop)
	}
	f.scheduleWake(arr.Device)

	hopEntry := Entry{
		At:     arr.At,
		Kind:   EntryHop,
		Device: arr.Device,
		Port:   arr.Port,
		Result: cloneResult(res),
	}
	f.record(journey, hopEntry)

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

	copies := sw.Copies()
	if journey.Origin.Kind == OriginMirror {
		// Draining and discarding downstream copies makes mirror provenance a
		// single generation instead of a recursively mirrored frame.
		copies = nil
	}
	for _, copy := range copies {
		fid := f.nextFrameID
		f.nextFrameID++
		seq := f.nextSeq
		f.nextSeq++
		inj := Injection{
			At:     arr.At,
			Origin: Endpoint{Node: arr.Device, Port: copy.Port},
			Frame:  cloneFrame(copy.Frame),
		}
		copyJourney := &Journey{
			FrameID: fid,
			Origin: JourneyOrigin{
				Kind:   OriginMirror,
				Of:     arr.FrameID,
				Mirror: copy.Mirror,
			},
			Injection: inj,
		}
		f.journeys[fid] = copyJourney
		f.record(copyJourney, Entry{
			At:     arr.At,
			Kind:   EntryInjection,
			Device: arr.Device,
			Port:   copy.Port,
			Cable:  f.portCable(arr.Device, copy.Port),
		})
		pcp, _ := copy.Frame.Priority()
		f.transmit(arr.At, arr.Device, copy.Port, copy.Member, copy.Frame, seq, fid, copyJourney, pcp, copy.Mirror)
	}

	return hopEntry, true
}

// reflectFrame originates one copy of an accepted mDNS query per other
// attachment of the datagram's address family, on the reflector's own
// acceptance the Step reflector branch just recorded. It is the counterpart
// to arriveReflector: arriveReflector decides whether the frame is taken for
// reflection, and reflectFrame is what "taken for reflection" does. The
// arriving frame's IP and UDP headers already decoded once inside
// acceptReflector to reach EntryReflection, so decoding them again here
// cannot fail.
func (f *Fabric) reflectFrame(parent *Journey, name string, refl Reflector, arrivalPort string, frame ethernet.Frame, at time.Time) {
	header, ipPayload, err := ip.Decode(frame.Payload)
	if err != nil {
		f.recordFault(errs.Wrap(err, "decode accepted reflector frame's IP header"))
		return
	}
	udpHeader, udpPayload, err := udp.Decode(ipPayload)
	if err != nil {
		f.recordFault(errs.Wrap(err, "decode accepted reflector frame's UDP header"))
		return
	}

	arrivalName, _ := reflectorArrivalAttachment(refl, arrivalPort, frame.Tags)
	family4 := header.Dst.Is4()

	for _, attName := range slices.Sorted(maps.Keys(refl.Attachments)) {
		if attName == arrivalName {
			continue
		}
		att := refl.Attachments[attName]
		addr, ok := attachmentAddress(att, family4)
		if !ok {
			f.record(parent, Entry{
				At:     at,
				Kind:   EntryDrop,
				Device: name,
				Port:   att.Port,
				Reason: ReasonReflectorNoAddress,
			})

			continue
		}

		f.originateReflection(parent, name, refl, att, addr, frame, header, udpHeader, udpPayload, at)
	}
}

// reflectorArrivalAttachment returns the name of the attachment whose port
// and tag form match the arriving frame. [Reflector.acceptsTags] answers the
// same question as a bool, so the two share this function rather than each
// carrying the tag-form guards on their own: more than one tag refuses, and
// a first tag whose TPID names neither the zero value nor a C-TAG refuses,
// before any attachment is considered. acceptReflector already established
// that exactly one attachment matches before this runs, so a caller that
// gets ok == false has nothing to exclude and reflects onto every
// attachment; that only happens if this invariant breaks.
func reflectorArrivalAttachment(refl Reflector, arrivalPort string, tags []vlan.Tag) (string, bool) {
	if len(tags) > 1 {
		return "", false
	}
	if len(tags) == 1 && tags[0].TPID != 0 && tags[0].TPID != uint16(ethernet.EtherTypeDot1Q) {
		return "", false
	}
	for attName, a := range refl.Attachments {
		if a.Port != arrivalPort {
			continue
		}
		if a.VLAN == nil {
			if len(tags) == 0 || tags[0].VID == 0 {
				return attName, true
			}

			continue
		}
		if len(tags) == 1 && tags[0].VID == *a.VLAN {
			return attName, true
		}
	}

	return "", false
}

// attachmentAddress returns one of the attachment's addresses matching the
// requested family (true for IPv4), or false if it names none.
func attachmentAddress(att Attachment, family4 bool) (netip.Addr, bool) {
	for _, prefix := range att.Addresses {
		if addr := prefix.Addr(); addr.Is4() == family4 {
			return addr, true
		}
	}

	return netip.Addr{}, false
}

// attachmentTags returns the tag stack a reflector copy carries onto an
// attachment: no tag for an untagged attachment, or one C-TAG with the
// attachment's VLAN. Mirrors the tag form Inject gives a host origin.
func attachmentTags(att Attachment) []vlan.Tag {
	if att.VLAN == nil {
		return nil
	}

	return []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: *att.VLAN}}
}

// originateReflection rebuilds the accepted datagram end to end onto one
// attachment and originates it as a new journey: fresh FrameID, Parent set to
// the arriving frame, and its own entered set seeded from a copy of the
// parent's. Seeding rather than sharing is load-bearing: a shared set cannot
// tell a sibling copy from an ancestor, and a reflector with three
// attachments would report a false loop on its first query.
//
// The datagram is rebuilt through udp.Encode and then ip.Header.Encode rather
// than patched in place, because changing the IP source address invalidates
// the IPv4 header checksum; ip.Decode refuses a header whose checksum does
// not match, so a receiving host would record an unrecomputed copy as
// undecodable instead of accepting it. Hop limit is 255 and UDP source port
// is 5353 (RFC 6762 section 11 and section 5.2); every other IPv4 field is
// carried from the arriving header unchanged.
func (f *Fabric) originateReflection(parent *Journey, name string, refl Reflector, att Attachment, addr netip.Addr, frame ethernet.Frame, header ip.Header, udpHeader udp.Header, udpPayload []byte, at time.Time) {
	copyUDP := udpHeader
	copyUDP.SrcPort = mdnsUDPPort
	udpBytes, err := udp.Encode(copyUDP, udpPayload, addr, header.Dst)
	if err != nil {
		// The arriving datagram already encoded at this length over a
		// same-family address pair, so re-encoding it with another
		// same-family source cannot fail.
		f.recordFault(errs.Wrap(err, "encode reflected UDP datagram"))
		return
	}

	copyHeader := header
	copyHeader.Src = addr
	copyHeader.HopLimit = 255
	ipBytes, err := copyHeader.Encode(udpBytes)
	if err != nil {
		// The arriving header already encoded this options length and total
		// length; replacing only the source address and hop limit cannot
		// make it exceed either limit.
		f.recordFault(errs.Wrap(err, "encode reflected IP header"))
		return
	}

	copyFrame := ethernet.Frame{
		Dst:       frame.Dst,
		Src:       refl.Address,
		Tags:      attachmentTags(att),
		EtherType: frame.EtherType,
		Payload:   ipBytes,
	}

	fid := f.nextFrameID
	f.nextFrameID++
	seq := f.nextSeq
	f.nextSeq++

	inj := Injection{
		At:     at,
		Origin: Endpoint{Node: name, Port: att.Port},
		Frame:  copyFrame,
	}
	copyJourney := &Journey{
		FrameID: fid,
		Origin: JourneyOrigin{
			Kind: OriginMirror,
			Of:   parent.FrameID,
		},
		Injection: inj,
	}
	f.journeys[fid] = copyJourney
	f.entered[fid] = cloneEntered(f.entered[parent.FrameID])
	f.record(copyJourney, Entry{
		At:     at,
		Kind:   EntryInjection,
		Device: name,
		Port:   att.Port,
		Cable:  f.portCable(name, att.Port),
	})

	ref, ok := f.linkEnd(name, att.Port)
	if !ok {
		// Validate requires every reflector port to be cabled, so a
		// configured fabric never reaches this.
		return
	}
	if ref.end.Oper != port.Up {
		kind := EntryDrop
		if ref.end.Oper == port.Unknown {
			kind = EntryUnresolved
		}
		cable := ref.link.Clone()
		f.record(copyJourney, Entry{
			At:     at,
			Kind:   kind,
			Device: name,
			Cable:  &cable,
			Reason: ref.end.Reason,
		})

		return
	}

	pcp, _ := copyFrame.Priority()
	f.enqueueEgress(at, ref.end.Endpoint, ref.end.Port, copyFrame, seq, fid, copyJourney, pcp, "")
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
	} else if sw, ok := f.switches[device]; ok {
		if p, ok := sw.Ports().Port(portName); ok && p.Kind == port.Lag {
			var vid vlan.ID
			for _, tag := range frame.Tags {
				if tag.TPID == uint16(ethernet.EtherTypeDot1Q) {
					vid = tag.VID
					break
				}
			}
			selected, ok := sw.SelectMember(now, portName, frame, vid)
			if !ok {
				// The switch takes the LAG's spanning tree link down with its
				// last enabled member, so a protocol frame reaches here only
				// if that invariant breaks; the drop names it rather than
				// losing the frame in silence.
				f.record(journey, Entry{
					At:     now,
					Kind:   EntryDrop,
					Device: device,
					Port:   portName,
					Reason: bridge.ReasonNoMember,
				})
				f.countEgressDrop(device, portName, bridge.ReasonNoMember)

				return
			}
			memberName = selected
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

	if ref.end.Speed.SpeedBPS == 0 {
		cableCopy := ref.link.Clone()
		f.record(journey, Entry{
			At:     now,
			Kind:   EntryLoss,
			Cable:  &cableCopy,
			Reason: ReasonCableLoss,
		})

		return
	}

	f.enqueueEgress(now, ref.end.Endpoint, portName, frame, seq, fid, journey, pcp, mirror)
}

func (f *Fabric) enqueueEgress(now time.Time, txEnd Endpoint, egressPort string, frame ethernet.Frame, seq uint64, fid FrameID, journey *Journey, pcp vlan.PCP, mirror string) {
	q := f.egress[txEnd]
	if q == nil {
		q = &egressQueue{}
		f.egress[txEnd] = q
	}
	q.pending[pcp] = append(q.pending[pcp], queued{
		frame:      cloneFrame(frame),
		seq:        seq,
		fid:        fid,
		journey:    journey,
		pcp:        pcp,
		egressPort: egressPort,
		enqueued:   now,
		mirror:     mirror,
	})
	f.serve(now, txEnd)
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
	if len(f.queue) > 0 && f.queue[0].At.Equal(now) && f.queue[0].Kind < ArrivalDequeue {
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

		ref, ok := f.linkEnd(txEnd.Node, txEnd.Port)
		if !ok {
			continue
		}
		if ref.end.Speed.SpeedBPS == 0 {
			cableCopy := ref.link.Clone()
			f.record(item.journey, Entry{
				At:     now,
				Kind:   EntryLoss,
				Cable:  &cableCopy,
				Reason: ReasonCableLoss,
			})

			continue
		}

		end := f.transmitCable(now, txEnd, ref, item)
		if sw := f.switches[txEnd.Node]; sw != nil {
			if rate, limited := sw.QueueMaxRate(item.egressPort, item.pcp); limited {
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
	f.queue = slices.DeleteFunc(f.queue, func(arr Arrival) bool {
		return arr.Kind == ArrivalDequeue && arr.Device == txEnd.Node && arr.Port == txEnd.Port
	})
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
		f.record(item.journey, Entry{
			At:     start,
			Kind:   EntryLoss,
			Cable:  &cableCopy,
			Reason: ReasonCableLoss,
		})

		return end
	}

	farEnd := ref.peer.Endpoint
	if _, isHost := f.cfg.Hosts[farEnd.Node]; isHost {
		if corrupt {
			f.record(item.journey, Entry{
				At:     deliveryAt,
				Kind:   EntryDrop,
				Device: farEnd.Node,
				Cable:  &cableCopy,
				Reason: ReasonBadFrame,
			})

			return end
		}
		f.arrive(item.journey, farEnd.Node, cable, item.frame, deliveryAt)

		return end
	}

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

func (f *Fabric) injectEmission(now time.Time, device string, em vswitch.Emission) {
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

	origin := JourneyOrigin{Kind: OriginInjection}
	if !em.Protocol {
		if holdingFID := f.findAndPopHeld(device, em.Frame); holdingFID != 0 {
			origin = JourneyOrigin{
				Kind: OriginRelease,
				Of:   holdingFID,
			}
		}
	}

	journey := &Journey{
		FrameID:   fid,
		Protocol:  em.Protocol,
		Origin:    origin,
		Injection: inj,
	}
	f.journeys[fid] = journey
	f.record(journey, Entry{
		At:     now,
		Kind:   EntryInjection,
		Device: device,
		Port:   em.Port,
		Cable:  f.portCable(device, em.Port),
	})

	// em.PCP rather than a priority re-derived from em.Frame: a released held frame leaving an
	// untagged access port or a routed port carries no tag to derive from, so deriving would
	// queue at 0 a frame the live path queues at the priority it arrived with.
	f.transmit(now, device, em.Port, "", em.Frame, seq, fid, journey, em.PCP, "")
}

func (f *Fabric) findAndPopHeld(device string, frame ethernet.Frame) FrameID {
	released := make(map[FrameID]bool)
	for _, j := range f.journeys {
		if j.Origin.Kind == OriginRelease && j.Origin.Of != 0 {
			released[j.Origin.Of] = true
		}
	}

	var candidateIDs []FrameID
	for fid, j := range f.journeys {
		if released[fid] {
			continue
		}
		if isJourneyHeld(j) {
			if len(j.Entries) > 0 && j.Entries[len(j.Entries)-1].Device == device {
				candidateIDs = append(candidateIDs, fid)
			}
		}
	}
	slices.Sort(candidateIDs)

	for _, fid := range candidateIDs {
		j := f.journeys[fid]
		if bytes.Equal(j.Injection.Frame.Payload, frame.Payload) {
			return fid
		}
	}

	if len(candidateIDs) > 0 {
		return candidateIDs[0]
	}

	return 0
}

// recordNeighborFailure turns one [vswitch.NeighborDrop] reported for a held
// frame that reached no wire into the same kind of answer a live drop gets: a
// counter, and a journey entry. Both the port and the reason come from the
// drop itself: the switch knows which stage refused the frame and which port,
// if any, that stage was acting for, and a reader that recovered either from
// the step's subject would be guessing — a subject naming a VLAN interface is
// not a port, and the counter it created could never appear in a Snapshot.
//
// A drop with no port increments nothing. An [Endpoint] whose Port is not a
// port is worse than an absent counter, because snapshotCounters iterates the
// device's real ports and would never show it, while a later reader finding
// it would believe it meant something.
//
// The held frame carried no fabric frame ID of its own — no [FrameID]
// travels with a held frame while it waits — so this opens a new one-entry
// journey for it, the way injectEmission opens one for a released frame's own
// injection, rather than attaching it to a frame journey it was never part
// of.
func (f *Fabric) recordNeighborFailure(now time.Time, device string, drop vswitch.NeighborDrop) {
	f.initRunState()

	if drop.Port != "" {
		f.countEgressDrop(device, drop.Port, drop.Reason)
	}

	fid := f.nextFrameID
	f.nextFrameID++

	step := drop.Step
	journey := &Journey{
		FrameID: fid,
		Origin:  JourneyOrigin{Kind: OriginInjection},
	}
	f.journeys[fid] = journey
	f.record(journey, Entry{
		At:     now,
		Kind:   EntryDrop,
		Device: device,
		Port:   drop.Port,
		Step:   &step,
		Reason: drop.Reason,
	})
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
		Kind:   ArrivalWake,
		Seq:    0,
		Device: device,
	})
}

func (f *Fabric) removeWake(device string) {
	delete(f.wakes, device)
	f.queue = slices.DeleteFunc(f.queue, func(arr Arrival) bool {
		return arr.Kind == ArrivalWake && arr.Device == device
	})
}

// Run repeatedly invokes [Fabric.Step] until the arrival queue is empty, budget steps have executed,
// or a scheduling fault or convergence condition halts simulation.
// A non-positive budget executes zero steps and returns [StopNotRun].
func (f *Fabric) Run(budget int) RunResult {
	return f.run(budget, 0)
}

// RunScenario validates and executes a declared scenario under its specified budget and window.
func (f *Fabric) RunScenario(s Scenario) (RunResult, error) {
	norm, err := s.Normalize()
	if err != nil {
		return RunResult{}, err
	}
	if err := norm.Validate(); err != nil {
		return RunResult{}, err
	}

	initialSpec := f.Spec()

	res, err := f.runWithActions(norm.Budget, norm.Window, norm.Actions)
	if err != nil {
		return RunResult{}, err
	}
	replayScenario := norm.Clone()
	replayScenario.Spec = ConstructionSpec{}
	res.Replay = ReplaySpec{
		Contract: ReplayContract,
		Spec:     initialSpec,
		Scenario: replayScenario,
	}
	return res, nil
}

// Replay executes a simulation from a recorded replay specification. Spec is the authoritative
// construction specification for the fabric topology; any Spec declared on Scenario is ignored
// to eliminate topology divergence.
func Replay(spec ReplaySpec) (RunResult, error) {
	if spec.Contract != ReplayContract {
		return RunResult{}, errs.New().
			Attr("contract", spec.Contract).
			Attr("expected", ReplayContract).
			Msgf("replay refused: unexpected contract %q, want %q", spec.Contract, ReplayContract)
	}
	fab, err := NewWithSpec(spec.Spec)
	if err != nil {
		return RunResult{}, err
	}
	scenario := spec.Scenario.Clone()
	scenario.Spec = ConstructionSpec{}
	return fab.RunScenario(scenario)
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
		return f.Mcheck(a.Mcheck.Node, a.Mcheck.Port)
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
		lastFingerprint         = f.Fingerprint()
		wakeArrivalsSinceChange int
		pendingActions          = actions
	)

	for steps < budget {
		if f.err != nil {
			stop = StopFault
			break
		}

		if len(pendingActions) > 0 {
			if len(f.queue) == 0 || !pendingActions[0].At.After(f.queue[0].At) {
				act := pendingActions[0]
				pendingActions = pendingActions[1:]
				if err := f.applyAction(act); err != nil {
					return RunResult{}, err
				}
				continue
			}
		}

		if len(f.queue) == 0 {
			stop = StopQueueDrained
			break
		}

		entry, ok := f.Step()
		if !ok {
			if f.err != nil {
				stop = StopFault
			} else {
				stop = StopQueueDrained
			}
			break
		}
		steps++

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

		if window > 0 && len(pendingActions) == 0 && wakeArrivalsSinceChange >= window && f.queueHoldsOnlyWakes() && !f.hasPendingJourneys() {
			stop = StopConverged
			break
		}

		if f.err != nil {
			stop = StopFault
			break
		}
	}

	if stop == "" {
		stop = StopBudget
	}

	return f.buildRunResult(stop, steps, fingerprints, cycle, budget, len(pendingActions)), nil
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
	for _, j := range f.Report() {
		if j.State == JourneyPending {
			return true
		}
	}
	return false
}

func (f *Fabric) pendingWork() PendingWork {
	var arrivals, wakes, egress, journeys int
	for _, arr := range f.queue {
		switch arr.Kind {
		case ArrivalFrame:
			arrivals++
		case ArrivalWake:
			wakes++
		}
	}
	for _, q := range f.egress {
		if q != nil {
			egress += q.pendingCount()
		}
	}
	for _, j := range f.Report() {
		if j.State == JourneyPending {
			journeys++
		}
	}
	return PendingWork{
		Arrivals: arrivals,
		Wakes:    wakes,
		Egress:   egress,
		Journeys: journeys,
	}
}

func (f *Fabric) buildRunResult(stop StopReason, steps int, fingerprints, cycle []string, budget int, droppedActions int) RunResult {
	rep := f.Report()
	var issues []analysis.Issue
	for _, j := range rep {
		issues = append(issues, j.Metadata.Issues()...)
	}
	var uniqueIssues []analysis.Issue
	for _, iss := range issues {
		if !slices.ContainsFunc(uniqueIssues, func(kept analysis.Issue) bool { return sameIssue(kept, iss) }) {
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
			Message: fmt.Sprintf("%d timed %s did not fire before budget exhaustion", droppedActions, noun),
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
		q = make([]Arrival, len(f.queue))
		copy(q, f.queue)
	}

	links := f.Links()

	devices := make(map[string]Device, len(f.switches))
	for name, sw := range f.switches {
		var groups map[vlan.ID][]mcast.Entry
		var routerPorts map[vlan.ID][]mcast.RouterPort
		cfg := sw.Config()
		if cfg.Mcast != nil {
			groups = make(map[vlan.ID][]mcast.Entry, len(cfg.Mcast.VLANs))
			routerPorts = make(map[vlan.ID][]mcast.RouterPort, len(cfg.Mcast.VLANs))
			for vid := range cfg.Mcast.VLANs {
				groups[vid] = sw.Groups(vid)
				routerPorts[vid] = sw.RouterPorts(vid)
			}
		}
		devices[name] = Device{
			Entries:       sw.Entries(),
			Groups:        groups,
			RouterPorts:   routerPorts,
			Ports:         sw.Ports().Ports(),
			Power:         sw.Power(),
			Counters:      f.snapshotCounters(name),
			Roles:         sw.Roles(),
			TreeRoles:     sw.TreeRoles(),
			RelayCounters: sw.RelayCounters(),
			Neighbors:     sw.Neighbors(),
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
	var queued map[Endpoint]int
	for ep, q := range f.egress {
		if count := q.pendingCount(); count > 0 {
			if queued == nil {
				queued = make(map[Endpoint]int)
			}
			queued[ep] = count
		}
	}

	return Snapshot{
		Clock:   f.clock,
		Queue:   q,
		Queued:  queued,
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
	if f.egress == nil {
		f.egress = make(map[Endpoint]*egressQueue)
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
