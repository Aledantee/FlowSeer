package host

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Host is a single-port endpoint. Its optional stack answers local requests
// without learning or resolving neighbors. Calls require serial access.
type Host struct {
	name  string
	cfg   Config
	ports port.Table
	stack *routing.Layer
}

// New clones and validates a named host's configuration and builds its stack.
// The returned host is not safe for concurrent use.
func New(name string, cfg Config) (*Host, error) {
	if name == "" {
		return nil, errs.Msg("host name cannot be empty")
	}
	if err := cfg.Accept.Validate("hosts." + name + ".accept"); err != nil {
		return nil, err
	}
	if cfg.IP != nil && len(cfg.IP.Addresses) == 0 {
		return nil, errs.New().Attr("host", name).Msgf("host %q with IP stack must configure at least one address", name)
	}
	cfg = cfg.Clone()
	rtCfg, tbl := RoutingConfig(name, cfg)
	h := &Host{name: name, cfg: cfg, ports: tbl}
	if cfg.IP != nil {
		stack, err := routing.New(rtCfg, layer.Env{Ports: tbl, NodeID: name})
		if err != nil {
			return nil, errs.Wrapf(err, "host %q routing", name)
		}
		h.stack = stack
	}
	return h, nil
}

// Originate builds a packet through the stack or applies the host's VLAN form
// to a supplied frame. PCP and DEI survive the VLAN rewrite. A host requires an empty
// port and refuses packet/frame conflicts or an unresolved next hop.
func (h *Host) Originate(at time.Time, port string, frame ethernet.Frame, packet *device.Packet) (device.Origination, error) {
	if port != "" {
		return device.Origination{}, errs.New().Attr("node", h.name).Attr("port", port).Msgf("host origin %q must have empty port", h.name)
	}
	pcp, _ := frame.Priority()
	if pcp > 7 {
		return device.Origination{}, errs.New().Attr("pcp", pcp).Msg("priority code point exceeds 3 bits")
	}
	if packet != nil {
		if frame.Dst != (netaddr.MAC{}) || frame.Src != (netaddr.MAC{}) || frame.EtherType != 0 || len(frame.Tags) != 0 || len(frame.Payload) != 0 {
			return device.Origination{}, errs.New().Attr("host", h.name).Msg("packet injection cannot specify frame fields")
		}
		if h.stack == nil {
			return device.Origination{}, errs.New().Attr("host", h.name).Msgf("host %q has no IP stack", h.name)
		}
		res := h.stack.Originate(at, routing.DefaultVRF, packet.To, packet.Protocol, packet.Payload, true)
		if res.Reason != "" {
			return device.Origination{}, errs.New().Attr("host", h.name).Attr("address", packet.To).Attr("reason", res.Reason).Msgf("host %q cannot originate packet to %s: %s", h.name, packet.To, res.Reason)
		}
		frame = res.Frame
	}
	frame.Payload = slices.Clone(frame.Payload)
	frame = h.tag(frame, pcp)
	return device.Origination{Emissions: []device.Emission{{Frame: frame, PCP: pcp}}}, nil
}

func (h *Host) tag(frame ethernet.Frame, pcp vlan.PCP) ethernet.Frame {
	_, dei := frame.Priority()
	frame.Tags = nil
	if h.cfg.VLAN != nil {
		frame.Tags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: *h.cfg.VLAN, PCP: pcp, DEI: dei}}
	}
	return frame
}

// Receive decides acceptance and returns any local neighbor answer.
func (h *Host) Receive(at time.Time, _ string, frame ethernet.Frame) device.Output {
	out := device.Output{Result: h.accept(frame)}
	if out.Outcome != trace.Consumed || h.stack == nil {
		return out
	}
	effects := h.stack.Receive(at, h.name, frame)
	for _, em := range effects.Emissions {
		out.Emissions = append(out.Emissions, device.Emission{Frame: h.tag(em.Frame, 0), Protocol: true})
	}
	return out
}

// ReceiveMirrored applies the same endpoint acceptance to a mirrored frame.
func (h *Host) ReceiveMirrored(at time.Time, port string, frame ethernet.Frame) device.Output {
	return h.Receive(at, port, frame)
}

// AdmitEgress applies the default unstated-buffer queue policy without mutation.
func (h *Host) AdmitEgress(name string, pcp vlan.PCP, depth, octets uint64) device.QueueDecision {
	var queues *traffic.Layer
	d := queues.AdmitEgress(name, pcp, depth, octets, 0)
	return device.QueueDecision{Admitted: d.Admitted, Reason: d.Reason, Step: d.Step, MaxRateBPS: d.MaxRateBPS}
}

// Advance returns no timer output because the host resolves nothing.
func (h *Host) Advance(time.Time) device.Output { return device.Output{} }

// NextWake reports no timers.
func (h *Host) NextWake() (time.Time, bool) { return time.Time{}, false }

// LinkChange updates the single port's operational state. Port must be empty.
func (h *Host) LinkChange(_ time.Time, name string, link device.Link) (device.Output, error) {
	if name != "" {
		return device.Output{}, errs.New().Attr("port", name).Msg("host link requires an empty port")
	}
	tbl, err := h.ports.WithOperStatus(h.name, link.State)
	if err != nil {
		return device.Output{}, err
	}
	h.ports = tbl
	return device.Output{}, nil
}

// Fork returns independent runtime state and shares private immutable configuration.
func (h *Host) Fork() device.Device {
	cp := *h
	if h.stack != nil {
		cp.stack = h.stack.Clone()
	}
	return &cp
}

// Ports returns the immutable single-port table.
func (h *Host) Ports() port.Table { return h.ports }

// State samples the host's port and configured neighbors without advancing time.
func (h *Host) State(time.Time) device.State {
	state := device.State{Ports: h.ports.Ports()}
	if h.stack != nil {
		for _, n := range h.stack.Neighbors() {
			state.Neighbors = append(state.Neighbors, device.NeighborEntry{VRF: n.VRF, Interface: n.Interface, Addr: n.Addr, MAC: n.MAC, State: device.NeighborState(n.State), HoldDepth: n.HoldDepth})
		}
	}
	return state
}

// Spec returns an independent copy of the configured host.
func (h *Host) Spec() Config { return h.cfg.Clone() }
