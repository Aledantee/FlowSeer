package reflector

import (
	"maps"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/udp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Reflector accepts mDNS queries and rebuilds one copy per other attachment.
// Calls require serial access. Configuration is private and immutable.
type Reflector struct {
	name  string
	cfg   Config
	ports port.Table
}

// New validates and clones a named reflector configuration.
func New(name string, cfg Config) (*Reflector, error) {
	if name == "" {
		return nil, errs.Msg("reflector name cannot be empty")
	}
	if err := cfg.Validate("reflectors." + name); err != nil {
		return nil, err
	}
	b := port.NewBuilder()
	for _, name := range slices.Sorted(maps.Keys(cfg.Ports)) {
		b.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown})
	}
	ports, err := b.Build()
	if err != nil {
		return nil, err
	}
	return &Reflector{name: name, cfg: cfg.Clone(), ports: ports}, nil
}

// Spec returns an independent copy of the configured reflector.
func (r *Reflector) Spec() Config { return r.cfg.Clone() }

// Originate refuses caller injections because a reflector only copies accepted queries.
func (r *Reflector) Originate(time.Time, string, ethernet.Frame, *device.Packet) (device.Origination, error) {
	return device.Origination{}, errs.New().Attr("node", r.name).Msgf("reflector %q cannot originate an injection: it has no host or switch port to inject at", r.name)
}

// Receive decides acceptance and returns copies in attachment name order.
func (r *Reflector) Receive(_ time.Time, ingress string, frame ethernet.Frame) device.Output {
	outcome, step, reason := accept(r.name, r.cfg, ingress, frame)
	out := device.Output{Result: device.Result{Trace: trace.Trace{Outcome: outcome, Reason: reason, Steps: []trace.Step{step}}, Acceptance: device.ReflectionAcceptance}}
	if reason == ReasonReflectorIPHeaderUndecodable || reason == ReasonReflectorUDPHeaderUndecodable {
		scope := analysis.NodeScope(r.name)
		out.Metadata = analysis.NewMetadata(scope, []analysis.Issue{{Code: analysis.IssueCode(reason), Status: analysis.Incomplete, Scope: scope, Message: "reflector header does not decode"}}, analysis.EvidenceCatalog{}, nil)
	}
	if outcome != trace.Consumed {
		return out
	}
	header, payload, err := ip.Decode(frame.Payload)
	if err != nil {
		out.Err = errs.Wrap(err, "decode accepted reflector frame's IP header")
		return out
	}
	udpHeader, udpPayload, err := udp.Decode(payload)
	if err != nil {
		out.Err = errs.Wrap(err, "decode accepted reflector frame's UDP header")
		return out
	}
	arrival, _ := arrivalAttachment(r.cfg, ingress, frame.Tags)
	for _, name := range slices.Sorted(maps.Keys(r.cfg.Attachments)) {
		if name == arrival {
			continue
		}
		att := r.cfg.Attachments[name]
		addr, ok := attachmentAddress(att, header.Dst.Is4())
		if !ok {
			out.Drops = append(out.Drops, device.Drop{Port: att.Port, Reason: ReasonReflectorNoAddress})
			continue
		}
		copyUDP := udpHeader
		copyUDP.SrcPort = mdnsUDPPort
		udpBytes, err := udp.Encode(copyUDP, udpPayload, addr, header.Dst)
		if err != nil {
			out.Err = errs.Wrap(err, "encode reflected UDP datagram")
			continue
		}
		copyHeader := header
		copyHeader.Src, copyHeader.HopLimit = addr, 255
		ipBytes, err := copyHeader.Encode(udpBytes)
		if err != nil {
			out.Err = errs.Wrap(err, "encode reflected IP header")
			continue
		}
		out.Copies = append(out.Copies, device.Copy{Port: att.Port, Frame: ethernet.Frame{Dst: frame.Dst, Src: r.cfg.Address, Tags: attachmentTags(att), EtherType: frame.EtherType, Payload: ipBytes}})
	}
	return out
}

// ReceiveMirrored applies the same acceptance rules to a mirror copy.
func (r *Reflector) ReceiveMirrored(at time.Time, ingress string, frame ethernet.Frame) device.Output {
	return r.Receive(at, ingress, frame)
}

// AdmitEgress applies the default unstated-buffer queue policy.
func (r *Reflector) AdmitEgress(name string, pcp vlan.PCP, depth, octets uint64) device.QueueDecision {
	var queues *traffic.Layer
	d := queues.AdmitEgress(name, pcp, depth, octets, 0)
	return device.QueueDecision{Admitted: d.Admitted, Reason: d.Reason, Step: d.Step, MaxRateBPS: d.MaxRateBPS}
}

// Advance returns no output because a reflector has no timers.
func (r *Reflector) Advance(time.Time) device.Output { return device.Output{} }

// NextWake reports no timers.
func (r *Reflector) NextWake() (time.Time, bool) { return time.Time{}, false }

// LinkChange updates the named port's operational state.
func (r *Reflector) LinkChange(_ time.Time, name string, link device.Link) (device.Output, error) {
	tbl, err := r.ports.WithOperStatus(name, link.State)
	if err != nil {
		return device.Output{}, err
	}
	r.ports = tbl
	return device.Output{}, nil
}

// Fork shares immutable configuration and independently tracks port changes.
func (r *Reflector) Fork() device.Device { cp := *r; return &cp }

// Ports returns the immutable port table.
func (r *Reflector) Ports() port.Table { return r.ports }

// State samples the reflector's ports without advancing time.
func (r *Reflector) State(time.Time) device.State { return device.State{Ports: r.ports.Ports()} }
