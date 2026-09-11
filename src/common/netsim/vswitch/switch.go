package vswitch

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

var stpGroupAddress = netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}

// Switch simulates a network device composed of a port table and optional
// physical-layer, bridge, and spanning tree subsystems.
//
// A Switch is not safe for concurrent use.
type Switch struct {
	cfg       Config
	ports     port.Table
	bridge    *bridge.Bridge
	speeds    map[string]phy.Resolved
	power     phy.Allocation
	stp       *stp.Layer
	emissions []stp.Emission
	portP2P   map[string]bool
	portSpeed map[string]uint64
}

// New constructs a [Switch] from the provided configuration, cloning the configuration
// and initializing each present subsystem.
func New(cfg Config) *Switch {
	cloned := cfg.Clone()

	sw := &Switch{
		cfg:   cloned,
		ports: cloned.Ports.Clone(),
	}

	if cloned.Phy != nil {
		sw.speeds = cloned.Phy.Resolve()
		sw.power = cloned.Phy.Allocate()
	}

	if cloned.Bridge != nil {
		sw.bridge = bridge.New(*cloned.Bridge, cloned.Ports)
	}

	if cloned.STP != nil {
		sw.stp = stp.New(*cloned.STP, cloned.Ports)
		if sw.bridge != nil {
			sw.bridge.SetGate(sw.stp)
		}
	}

	return sw
}

// Config returns an independent deep copy of the switch configuration.
func (s *Switch) Config() Config {
	return s.cfg.Clone()
}

// Ports returns a copy of the switch port table.
func (s *Switch) Ports() port.Table {
	return s.ports.Clone()
}

// Entries returns the active forwarding database entries in sorted order,
// or nil if the switch does not have a bridge relay.
func (s *Switch) Entries() []bridge.Entry {
	if s.bridge == nil {
		return nil
	}

	return s.bridge.Entries()
}

// Speeds returns the resolved physical link speeds and duplex modes keyed by
// port name, or nil if the Ethernet capability is absent.
func (s *Switch) Speeds() map[string]phy.Resolved {
	if s.speeds == nil {
		return nil
	}
	cp := make(map[string]phy.Resolved, len(s.speeds))
	for k, v := range s.speeds {
		cp[k] = v
	}

	return cp
}

// Power returns the Power over Ethernet budget distribution and port allocations.
func (s *Switch) Power() phy.Allocation {
	cp := phy.Allocation{}
	if s.power.Ports != nil {
		cp.Ports = make(map[string]phy.PortAllocation, len(s.power.Ports))
		for k, v := range s.power.Ports {
			cp.Ports[k] = v
		}
	}
	if s.power.Groups != nil {
		cp.Groups = make(map[string]phy.GroupAllocation, len(s.power.Groups))
		for k, v := range s.power.Groups {
			cp.Groups[k] = v
		}
	}

	return cp
}

// Forward processes an arrival on an ingress port at the given time, updating
// the forwarding database if a bridge relay is present, and returns the processing trace.
func (s *Switch) Forward(now time.Time, ingress string, f ethernet.Frame) bridge.Result {
	if s.stp != nil && f.Dst == stpGroupAddress {
		return s.interceptBPDU(now, ingress, f, true)
	}
	if s.bridge != nil {
		return s.bridge.Forward(now, ingress, f)
	}

	return s.forwardHub(ingress, f)
}

// Peek processes an arrival on an ingress port at the given time without mutating
// the forwarding database and returns the processing trace.
func (s *Switch) Peek(now time.Time, ingress string, f ethernet.Frame) bridge.Result {
	if s.stp != nil && f.Dst == stpGroupAddress {
		return s.interceptBPDU(now, ingress, f, false)
	}
	if s.bridge != nil {
		return s.bridge.Peek(now, ingress, f)
	}

	return s.forwardHub(ingress, f)
}

// Age removes dynamic forwarding database entries older than the configured
// aging time relative to now. It is a no-op when the switch has no bridge subsystem.
func (s *Switch) Age(now time.Time) {
	if s.bridge != nil {
		s.bridge.Age(now)
	}
}

func (s *Switch) forwardHub(ingress string, f ethernet.Frame) bridge.Result {
	var res bridge.Result
	res.Outcome = trace.Dropped
	res.FID = 0

	p, ok := s.ports.Port(ingress)
	if !ok {
		res.Reason = bridge.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerPort,
			Op:     trace.OpDrop,
			Detail: "ingress port not found",
		})

		return res
	}

	resolved, ok := s.ports.Resolve(ingress)
	if !ok {
		res.Reason = bridge.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerPort,
			Op:     trace.OpDrop,
			Detail: "ingress LAG parent not found",
		})

		return res
	}
	res.Ingress = resolved.Name

	if !p.Forwards() || !resolved.Forwards() {
		res.Reason = bridge.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerPort,
			Op:     trace.OpDrop,
			Detail: "ingress port down",
		})

		return res
	}

	var (
		candidates  []port.Port
		memberNames []string
	)
	for _, cand := range s.ports.Ports() {
		if cand.LagParent != "" || cand.Name == res.Ingress || !cand.Forwards() {
			continue
		}
		var member string
		if cand.Kind == port.Lag {
			mems := s.ports.Members(cand.Name)
			var fwdMembers []port.Port
			for _, m := range mems {
				if m.Forwards() {
					fwdMembers = append(fwdMembers, m)
				}
			}
			if len(fwdMembers) == 0 {
				continue
			}
			slices.SortFunc(fwdMembers, func(i, j port.Port) int {
				return cmp.Compare(i.Name, j.Name)
			})
			member = fwdMembers[0].Name
		}
		candidates = append(candidates, cand)
		memberNames = append(memberNames, member)
	}

	if len(candidates) == 0 {
		return res
	}

	var transmitted int
	for i, cand := range candidates {
		mem := memberNames[i]
		if cand.MTU > 0 && len(f.Payload) > cand.MTU {
			res.Egress = append(res.Egress, bridge.Egress{
				Port:    cand.Name,
				Member:  mem,
				Frame:   f,
				Dropped: bridge.ReasonMTUExceeded,
			})
			res.Steps = append(res.Steps, trace.Step{
				Layer:  port.LayerPort,
				Op:     trace.OpDrop,
				Detail: fmt.Sprintf("port %s: mtu-exceeded", cand.Name),
			})

			continue
		}

		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerPort,
			Op:     trace.OpReplicate,
			Detail: fmt.Sprintf("port %s", cand.Name),
		})
		res.Egress = append(res.Egress, bridge.Egress{
			Port:   cand.Name,
			Member: mem,
			Frame:  f,
		})
		transmitted++
	}

	if transmitted > 0 {
		res.Outcome = trace.Flooded
	} else {
		res.Outcome = trace.Dropped
		res.Reason = bridge.ReasonMTUExceeded
	}

	return res
}

func (s *Switch) interceptBPDU(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	resolvedPort := ingress
	if p, ok := s.ports.Resolve(ingress); ok {
		resolvedPort = p.Name
	}

	bpdu, err := stp.Decode(f)
	if err != nil {
		reason := stp.ReasonUnsupportedBPDU
		if r, ok := errs.Attributes(err)["reason"].(trace.Reason); ok {
			reason = r
		}

		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  reason,
				Steps: []trace.Step{
					{
						Layer:  port.LayerStp,
						Op:     trace.OpDrop,
						Detail: string(reason),
					},
				},
			},
			Ingress: resolvedPort,
		}
	}

	if mutate {
		fx := s.stp.Receive(now, resolvedPort, bpdu)
		s.applyEffects(fx)
	}

	return bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Consumed,
			Steps: []trace.Step{
				{
					Layer:  port.LayerStp,
					Op:     trace.OpClassify,
					Detail: "stp",
				},
			},
		},
		Ingress: resolvedPort,
	}
}

// Start initializes the spanning tree layer with the current link state of every port in the table.
// On a switch without spanning tree configuration, Start is a no-op.
func (s *Switch) Start(now time.Time) {
	if s.stp == nil {
		return
	}
	if s.portP2P == nil {
		s.portP2P = make(map[string]bool)
		s.portSpeed = make(map[string]uint64)
	}
	for _, p := range s.ports.Ports() {
		if p.LagParent != "" {
			continue
		}
		p2p := true
		if s.cfg.STP != nil {
			if pCfg, ok := s.cfg.STP.Ports[p.Name]; ok {
				if pCfg.PointToPoint == stp.PointToPointForceFalse {
					p2p = false
				}
			}
		}
		var speed uint64
		if s.speeds != nil {
			speed = s.speeds[p.Name].SpeedBPS
		}
		s.portP2P[p.Name] = p2p
		s.portSpeed[p.Name] = speed

		fx := s.stp.LinkChange(now, p.Name, p.Forwards(), p2p, speed)
		s.applyEffects(fx)
	}
}

func (s *Switch) applyEffects(fx stp.Effects) {
	if len(fx.Flush) > 0 && s.bridge != nil {
		s.bridge.FlushPorts(fx.Flush)
	}
	if len(fx.Emissions) > 0 {
		s.emissions = append(s.emissions, fx.Emissions...)
	}
}

// Drain returns and clears pending frame emissions produced by the spanning tree layer.
func (s *Switch) Drain() []stp.Emission {
	em := s.emissions
	s.emissions = nil

	return em
}

// Roles returns the runtime spanning tree status for each configured port,
// or nil if the spanning tree layer is absent.
func (s *Switch) Roles() map[string]stp.PortInfo {
	if s.stp == nil || s.cfg.STP == nil {
		return nil
	}
	roles := make(map[string]stp.PortInfo, len(s.cfg.STP.Ports))
	for name := range s.cfg.STP.Ports {
		roles[name] = s.stp.PortInfo(name)
	}

	return roles
}

// Root returns the elected root bridge identifier, the path cost to reach it,
// and the interface name of the root port, or zero values if the spanning tree
// layer is absent.
func (s *Switch) Root() (stp.BridgeID, uint32, string) {
	if s.stp == nil {
		return stp.BridgeID{}, 0, ""
	}

	return s.stp.Root()
}

// TopologyChanges returns the total count of detected topology changes and the
// timestamp of the most recent change, or zero values if the spanning tree layer is absent.
func (s *Switch) TopologyChanges() (uint64, time.Time) {
	if s.stp == nil {
		return 0, time.Time{}
	}

	return s.stp.TopologyChanges()
}

// Wake advances the spanning tree layer to now, firing due timers and flushing bridge entries.
// On a switch without spanning tree configuration, Wake is a no-op.
func (s *Switch) Wake(now time.Time) {
	if s.stp == nil {
		return
	}
	fx := s.stp.Wake(now)
	s.applyEffects(fx)
}

// NextWake returns the earliest scheduled time at which the switch needs to be woken,
// and reports whether any timer is currently active.
func (s *Switch) NextWake() (time.Time, bool) {
	if s.stp == nil {
		return time.Time{}, false
	}

	return s.stp.NextWake()
}

// LinkChange notifies the spanning tree layer of a link transition on the named port,
// resolving a LAG member to its LAG parent.
func (s *Switch) LinkChange(now time.Time, portName string, up, pointToPoint bool, speed uint64) {
	if s.stp == nil {
		return
	}
	resolvedPort := portName
	if p, ok := s.ports.Resolve(portName); ok {
		resolvedPort = p.Name
	}
	if s.portP2P == nil {
		s.portP2P = make(map[string]bool)
		s.portSpeed = make(map[string]uint64)
	}
	s.portP2P[resolvedPort] = pointToPoint
	s.portSpeed[resolvedPort] = speed

	fx := s.stp.LinkChange(now, resolvedPort, up, pointToPoint, speed)
	s.applyEffects(fx)
}

// SetOperStatus updates the operational link state of the named port in the switch's port table,
// updates the bridge relay's port table, and notifies the spanning tree layer.
func (s *Switch) SetOperStatus(now time.Time, portName string, state port.LinkState) {
	builder := port.NewBuilder()
	for _, p := range s.ports.Ports() {
		if p.Name == portName {
			p.OperStatus = state
		}
		builder.Add(p)
	}
	tbl, err := builder.Build()
	if err == nil {
		s.ports = tbl
	}

	if s.bridge != nil {
		s.bridge.SetOperStatus(portName, state)
	}

	if s.stp != nil {
		resolvedPort := portName
		if p, ok := s.ports.Resolve(portName); ok {
			resolvedPort = p.Name
		}
		p2p := true
		if s.portP2P != nil {
			if v, ok := s.portP2P[resolvedPort]; ok {
				p2p = v
			}
		} else if s.cfg.STP != nil {
			if pCfg, ok := s.cfg.STP.Ports[resolvedPort]; ok {
				if pCfg.PointToPoint == stp.PointToPointForceFalse {
					p2p = false
				}
			}
		}
		var speed uint64
		if s.portSpeed != nil {
			if v, ok := s.portSpeed[resolvedPort]; ok {
				speed = v
			}
		} else if s.speeds != nil {
			speed = s.speeds[resolvedPort].SpeedBPS
		}
		fx := s.stp.LinkChange(now, resolvedPort, state != port.Down, p2p, speed)
		s.applyEffects(fx)
	}
}
