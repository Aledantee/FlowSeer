package fabric

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

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

func (f *Fabric) runStarted() bool {
	return f.stepped && !f.clock.IsZero()
}
