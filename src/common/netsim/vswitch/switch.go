package vswitch

import (
	"cmp"
	"fmt"
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

var stpGroupAddress = netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}

// Switch simulates a network device composed of a port table and optional
// physical-layer, bridge, spanning tree, and layer 3 routing subsystems.
//
// A Switch is not safe for concurrent use.
type Switch struct {
	cfg       Config
	ports     port.Table
	bridge    *bridge.Bridge
	speeds    map[string]phy.Resolved
	power     phy.Allocation
	stp       *stp.Layer
	routing   *routing.Layer
	emissions []stp.Emission
	portP2P   map[string]bool
	portSpeed map[string]uint64
}

// New constructs a [Switch] from the provided configuration, cloning the configuration,
// assigning missing MAC addresses, and initializing each present subsystem. When the base
// MAC is zero, New selects the first unused local MAC address; two standalone switches
// may pick the same address. A fabric assigns unique addresses across nodes.
func New(cfg Config) *Switch {
	cloned := cfg.Clone()

	if cloned.MAC == (netaddr.MAC{}) {
		explicit := make(map[netaddr.MAC]struct{})
		if cloned.STP != nil && cloned.STP.Address != (netaddr.MAC{}) {
			explicit[cloned.STP.Address] = struct{}{}
		}
		if cloned.Routing != nil {
			for _, vrf := range cloned.Routing.VRFs {
				for _, iface := range vrf.Interfaces {
					if iface.MAC != (netaddr.MAC{}) {
						explicit[iface.MAC] = struct{}{}
					}
				}
			}
		}
		for n := uint32(1); ; n++ {
			cand := netaddr.Local(n)
			if _, ok := explicit[cand]; !ok {
				cloned.MAC = cand
				break
			}
		}
	}

	if cloned.STP != nil && cloned.STP.Address == (netaddr.MAC{}) {
		cloned.STP.Address = cloned.MAC
	}
	if cloned.Routing != nil {
		for vrfName, vrf := range cloned.Routing.VRFs {
			for ifaceName, iface := range vrf.Interfaces {
				if iface.MAC == (netaddr.MAC{}) {
					iface.MAC = cloned.MAC
					vrf.Interfaces[ifaceName] = iface
				}
			}
			cloned.Routing.VRFs[vrfName] = vrf
		}
	}

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

	if cloned.Routing != nil {
		sw.routing = routing.New(*cloned.Routing)
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

// Learn preloads the switch's bridge forwarding database with the provided seeds.
// It is a no-op when the switch has no bridge relay.
func (s *Switch) Learn(seeds []bridge.Seed) {
	if s.bridge == nil {
		return
	}
	s.bridge.Learn(seeds)
}

// Forget removes a forwarding database entry with the given FID and MAC address from
// the switch's bridge relay, reporting whether an entry was present. It returns false
// when the switch has no bridge relay.
func (s *Switch) Forget(fid vlan.ID, mac netaddr.MAC) bool {
	if s.bridge == nil {
		return false
	}

	return s.bridge.Forget(fid, mac)
}

// RelayCounters returns the forwarding database lifecycle counters from the switch's
// bridge relay, or a zero-value Counters struct if the switch has no bridge relay.
func (s *Switch) RelayCounters() bridge.Counters {
	if s.bridge == nil {
		return bridge.Counters{}
	}

	return s.bridge.Counters()
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
	return s.forward(now, ingress, f, true)
}

// Peek processes an arrival on an ingress port at the given time without mutating
// the forwarding database and returns the processing trace.
func (s *Switch) Peek(now time.Time, ingress string, f ethernet.Frame) bridge.Result {
	return s.forward(now, ingress, f, false)
}

func (s *Switch) forward(now time.Time, ingress string, f ethernet.Frame, learn bool) bridge.Result {
	if s.stp != nil && f.Dst == stpGroupAddress {
		return s.interceptBPDU(now, ingress, f, learn)
	}

	if s.routing != nil {
		resolved, reason := s.ports.Receive(ingress)
		if resolved.Name != "" {
			if iface, ok := s.routing.ByPort(resolved.Name); ok {
				if reason != "" {
					return bridge.Result{
						Trace: trace.Trace{
							Outcome: trace.Dropped,
							Reason:  port.ReasonPortDown,
							Steps: []trace.Step{
								{
									Layer:  port.LayerRouting,
									Op:     trace.OpDrop,
									Detail: string(port.ReasonPortDown),
								},
							},
						},
						Ingress: resolved.Name,
						FID:     0,
					}
				}

				if !s.routing.Owns(iface, f) {
					return bridge.Result{
						Trace: trace.Trace{
							Outcome: trace.Dropped,
							Reason:  routing.ReasonNotBridged,
							Steps: []trace.Step{
								{
									Layer:  port.LayerRouting,
									Op:     trace.OpDrop,
									Detail: string(routing.ReasonNotBridged),
								},
							},
						},
						Ingress: resolved.Name,
						FID:     0,
					}
				}

				routeRes := s.routing.Route(iface, f)
				return s.assembleRouteResult(resolved.Name, 0, 0, false, nil, routeRes)
			}
		}
	}

	if s.bridge != nil {
		in, res, ok := s.bridge.Ingress(now, ingress, f, learn)
		if !ok {
			return res
		}

		if s.routing != nil {
			if iface, ok := s.routing.ByVLAN(in.FID); ok && s.routing.Owns(iface, f) {
				routeRes := s.routing.Route(iface, f)
				return s.assembleRouteResult(in.Port, in.FID, in.PCP, in.DEI, in.Steps, routeRes)
			}
		}

		return s.bridge.Egress(in, f)
	}

	return s.forwardHub(ingress, f)
}

func (s *Switch) assembleRouteResult(
	ingressPort string,
	ingressFID vlan.ID,
	ingressPCP vlan.PCP,
	ingressDEI bool,
	ingressSteps []trace.Step,
	routeRes routing.Result,
) bridge.Result {
	if routeRes.Reason != "" {
		outcome := trace.Dropped
		if routeRes.Reason == routing.ReasonNotRouted {
			outcome = trace.Consumed
		}
		steps := make([]trace.Step, 0, len(ingressSteps)+len(routeRes.Steps))
		steps = append(steps, ingressSteps...)
		steps = append(steps, routeRes.Steps...)

		return bridge.Result{
			Trace: trace.Trace{
				Outcome: outcome,
				Reason:  routeRes.Reason,
				Steps:   steps,
			},
			Ingress: ingressPort,
			FID:     ingressFID,
		}
	}

	// Route names only an interface of its own table, so the lookup cannot miss.
	egressIface, _ := s.routing.Interface(routeRes.Interface)

	if egressIface.VLAN != 0 {
		stepsSoFar := make([]trace.Step, 0, len(ingressSteps)+len(routeRes.Steps))
		stepsSoFar = append(stepsSoFar, ingressSteps...)
		stepsSoFar = append(stepsSoFar, routeRes.Steps...)

		bridgeIn := bridge.Ingress{
			Port:  "",
			FID:   egressIface.VLAN,
			PCP:   ingressPCP,
			DEI:   ingressDEI,
			Steps: stepsSoFar,
		}
		res := s.bridge.Egress(bridgeIn, routeRes.Frame)
		res.FID = egressIface.VLAN
		res.Ingress = ingressPort
		return res
	}

	steps := make([]trace.Step, 0, len(ingressSteps)+len(routeRes.Steps)+1)
	steps = append(steps, ingressSteps...)
	steps = append(steps, routeRes.Steps...)

	member, txReason := s.ports.Transmit(egressIface.Port, len(routeRes.Frame.Payload))
	if txReason == "" {
		steps = append(steps, trace.Step{
			Layer:  port.LayerRouting,
			Op:     trace.OpTransmit,
			Detail: fmt.Sprintf("port %s", egressIface.Port),
		})
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Forwarded,
				Steps:   steps,
			},
			Ingress: ingressPort,
			FID:     0,
			Egress: []bridge.Egress{
				{
					Port:   egressIface.Port,
					Member: member,
					Frame:  routeRes.Frame,
				},
			},
		}
	}

	steps = append(steps, trace.Step{
		Layer:  port.LayerRouting,
		Op:     trace.OpDrop,
		Detail: fmt.Sprintf("port %s: %s", egressIface.Port, txReason),
	})
	return bridge.Result{
		Trace: trace.Trace{
			Outcome: trace.Dropped,
			Reason:  txReason,
			Steps:   steps,
		},
		Ingress: ingressPort,
		FID:     0,
		Egress: []bridge.Egress{
			{
				Port:    egressIface.Port,
				Member:  member,
				Frame:   routeRes.Frame,
				Dropped: txReason,
			},
		},
	}
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
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerPort,
			Op:     trace.OpDrop,
			Detail: "ingress port not found",
		})

		return res
	}

	resolved, ok := s.ports.Resolve(ingress)
	if !ok {
		res.Reason = port.ReasonPortDown
		res.Steps = append(res.Steps, trace.Step{
			Layer:  port.LayerPort,
			Op:     trace.OpDrop,
			Detail: "ingress LAG parent not found",
		})

		return res
	}
	res.Ingress = resolved.Name

	if !p.Forwards() || !resolved.Forwards() {
		res.Reason = port.ReasonPortDown
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
				Dropped: port.ReasonMTUExceeded,
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
		res.Reason = port.ReasonMTUExceeded
	}

	return res
}

func (s *Switch) interceptBPDU(now time.Time, ingress string, f ethernet.Frame, mutate bool) bridge.Result {
	// The relay's first checks apply to a BPDU too: a dead or unknown port
	// received nothing, and a trace saying Consumed there would hide a BPDU
	// that died on a cut cable.
	resolved, reason := s.ports.Receive(ingress)
	if reason != "" {
		return bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Dropped,
				Reason:  port.ReasonPortDown,
				Steps:   []trace.Step{{Layer: port.LayerStp, Op: trace.OpDrop, Detail: "ingress port down"}},
			},
			Ingress: resolved.Name,
		}
	}
	resolvedPort := resolved.Name

	bpdu, err := stp.Decode(f)
	if err != nil {
		if mutate {
			s.stp.BadBPDU(resolvedPort)
		}

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
		speed := s.linkSpeed(p)
		s.portP2P[p.Name] = p2p
		s.portSpeed[p.Name] = speed

		fx := s.stp.LinkChange(now, p.Name, p.Forwards(), p2p, speed)
		s.applyEffects(fx)
	}
}

// linkSpeed is the resolved speed of a port, or of a LAG's fastest member,
// since a LAG has no physical layer of its own.
func (s *Switch) linkSpeed(p port.Port) uint64 {
	if s.speeds == nil {
		return 0
	}
	if p.Kind != port.Lag {
		return s.speeds[p.Name].SpeedBPS
	}
	var best uint64
	for _, m := range s.ports.Members(p.Name) {
		best = max(best, s.speeds[m.Name].SpeedBPS)
	}

	return best
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

// BridgeID returns the spanning tree bridge identifier with the priority in
// effect, or a zero value if the spanning tree layer is absent.
func (s *Switch) BridgeID() stp.BridgeID {
	if s.stp == nil {
		return stp.BridgeID{}
	}

	return s.stp.BridgeID()
}

// Times returns the spanning tree max age, hello time, and forward delay in
// force, or zero values if the spanning tree layer is absent.
func (s *Switch) Times() (maxAge, hello, forwardDelay time.Duration) {
	if s.stp == nil {
		return 0, 0, 0
	}

	return s.stp.Times()
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

// Mcheck triggers protocol migration checking on the named port, forcing it to
// transmit RSTP BPDUs and restarting the migration delay.
// On a switch without spanning tree configuration, Mcheck is a no-op.
func (s *Switch) Mcheck(now time.Time, port string) {
	if s.stp == nil {
		return
	}
	resolvedPort := port
	if p, ok := s.ports.Resolve(port); ok {
		resolvedPort = p.Name
	}
	fx := s.stp.Mcheck(now, resolvedPort)
	s.applyEffects(fx)
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

// SetOperStatus updates the operational link state of the named port in the
// switch's port table and the relay's. It tells the spanning tree layer
// nothing: only the caller knows whether a member's change moves its LAG or
// what the link's speed and kind are, so the caller follows with LinkChange.
func (s *Switch) SetOperStatus(portName string, state port.LinkState) {
	builder := port.NewBuilder()
	for _, p := range s.ports.Ports() {
		if p.Name == portName {
			p.OperStatus = state
		}
		builder.Add(p)
	}
	// Only OperStatus changed on a table that already validated, so the
	// rebuild cannot fail.
	if tbl, err := builder.Build(); err == nil {
		s.ports = tbl
	}

	if s.bridge != nil {
		s.bridge.SetOperStatus(portName, state)
	}
}
