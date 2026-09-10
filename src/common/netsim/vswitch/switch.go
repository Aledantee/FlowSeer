package vswitch

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Switch simulates a network device composed of a port table and optional
// physical-layer and bridge subsystems.
//
// A Switch is not safe for concurrent use.
type Switch struct {
	cfg    Config
	ports  port.Table
	bridge *bridge.Bridge
	speeds map[string]phy.Resolved
	power  phy.Allocation
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
	if s.bridge != nil {
		return s.bridge.Forward(now, ingress, f)
	}

	return s.forwardHub(ingress, f)
}

// Peek processes an arrival on an ingress port at the given time without mutating
// the forwarding database and returns the processing trace.
func (s *Switch) Peek(now time.Time, ingress string, f ethernet.Frame) bridge.Result {
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
