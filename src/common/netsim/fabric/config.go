package fabric

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Layer is the architectural trace layer identifier for the network fabric.
const Layer trace.Layer = "fabric"

const (
	// ReasonCut records that a link cannot operate because its physical cable is severed.
	ReasonCut trace.Reason = "cut"

	// ReasonPeerDown records that a link cannot operate because the peer switch port is administratively disabled.
	ReasonPeerDown trace.Reason = "peer-down"

	// ReasonNoCable records that an interface is operationally inactive because no cable is connected.
	ReasonNoCable trace.Reason = "no-cable"

	// ReasonDeadDirection records that two-ended auto-negotiation cannot succeed because the cable is impaired in one direction.
	ReasonDeadDirection trace.Reason = "dead-direction"

	// ReasonCableLoss records that a transmitted frame was lost during cable propagation due to a configured fault.
	ReasonCableLoss trace.Reason = "cable-loss"

	// ReasonBadFrame records that an arriving frame was dropped because it was corrupted during transmission.
	ReasonBadFrame trace.Reason = "bad-frame"
)

// FaultKind identifies the nature of a cable impairment, distinguishing physical defects
// from frame-level transmission losses and corruptions.
type FaultKind string

const (
	// FaultNone indicates an unimpaired, fully operational cable.
	FaultNone FaultKind = "None"

	// FaultCut models complete physical severance, disabling transmission in both directions.
	FaultCut FaultKind = "Cut"

	// FaultDeadAToB models unidirectional failure blocking transmission from endpoint A to endpoint B.
	FaultDeadAToB FaultKind = "DeadAToB"

	// FaultDeadBToA models unidirectional failure blocking transmission from endpoint B to endpoint A.
	FaultDeadBToA FaultKind = "DeadBToA"

	// FaultLoseEveryNth models periodic packet loss dropping every Nth transmitted frame.
	FaultLoseEveryNth FaultKind = "LoseEveryNth"

	// FaultLoseSequence models deterministic packet loss at designated 1-based frame positions.
	FaultLoseSequence FaultKind = "LoseSequence"

	// FaultCorruptEveryNth models periodic frame corruption marking every Nth transmitted frame damaged.
	FaultCorruptEveryNth FaultKind = "CorruptEveryNth"
)

// Fault specifies physical defects or frame-level loss and corruption policies applied to a cable.
type Fault struct {
	Kind     FaultKind
	N        uint
	Sequence []uint
}

// Clone returns an independent deep copy of the fault configuration.
func (f Fault) Clone() Fault {
	cp := f
	if len(f.Sequence) > 0 {
		cp.Sequence = make([]uint, len(f.Sequence))
		copy(cp.Sequence, f.Sequence)
	}

	return cp
}

// Endpoint names a specific attachment point in the fabric, referencing a virtual switch port
// or a single-port host when Port is empty.
type Endpoint struct {
	Node string
	Port string
}

// Host is an endpoint with one address and no relay; modeling it as a one-port switch would give it a forwarding database it must never use.
//
// A nil VLAN emits and accepts untagged frames, while a non-nil VLAN restricts the host to C-TAG frames
// with that VID.
type Host struct {
	Address netaddr.MAC
	VLAN    *vlan.ID
}

// Clone returns an independent deep copy of the host configuration.
func (h Host) Clone() Host {
	cp := h
	if h.VLAN != nil {
		v := *h.VLAN
		cp.VLAN = &v
	}

	return cp
}

// Cable models a physical link connecting two endpoints with propagation latency,
// an optional top speed limit, and declared faults.
type Cable struct {
	A            Endpoint
	B            Endpoint
	LengthMeters float64
	TopSpeedBPS  uint64
	Fault        Fault
}

// Clone returns an independent deep copy of the cable configuration.
func (c Cable) Clone() Cable {
	cp := c
	cp.Fault = c.Fault.Clone()

	return cp
}

// Config declares the full static topology of a simulated network fabric.
type Config struct {
	Switches map[string]vswitch.Config
	Hosts    map[string]Host
	Cables   []Cable
}

// Clone returns an independent deep copy of the fabric configuration.
func (c Config) Clone() Config {
	cp := Config{}
	if c.Switches != nil {
		cp.Switches = make(map[string]vswitch.Config, len(c.Switches))
		for k, v := range c.Switches {
			cp.Switches[k] = v.Clone()
		}
	}
	if c.Hosts != nil {
		cp.Hosts = make(map[string]Host, len(c.Hosts))
		for k, v := range c.Hosts {
			cp.Hosts[k] = v.Clone()
		}
	}
	if c.Cables != nil {
		cp.Cables = make([]Cable, len(c.Cables))
		for i, cable := range c.Cables {
			cp.Cables[i] = cable.Clone()
		}
	}

	return cp
}

// Validate verifies structural and topological invariants of the configuration:
// switch configurations must pass their own validation, endpoints must reference existing
// nodes and ports, cable attachments cannot target LAGs or duplicate existing links,
// and hosts must attach to exactly one cable with an empty port name.
func (c Config) Validate() error {
	for name, swCfg := range c.Switches {
		if name == "" {
			return errs.New().Msg("switch name cannot be empty")
		}
		if err := swCfg.Validate(); err != nil {
			return errs.Wrapf(err, "switch %q", name)
		}
	}

	for name, h := range c.Hosts {
		if name == "" {
			return errs.New().Msg("host name cannot be empty")
		}
		if _, isSw := c.Switches[name]; isSw {
			return errs.New().Attr("node", name).Msgf("node %q cannot be both a switch and a host", name)
		}
		if h.VLAN != nil && !h.VLAN.Valid() {
			return errs.New().
				Attr("host", name).
				Attr("vlan", *h.VLAN).
				Msgf("host %q has invalid VLAN ID %d", name, *h.VLAN)
		}
	}

	hostCables := make(map[string]int, len(c.Hosts))
	portCables := make(map[Endpoint]int)

	for i, cable := range c.Cables {
		if cable.LengthMeters < 0 {
			return errs.New().
				Attr("cable", i).
				Attr("length", cable.LengthMeters).
				Msg("cable length cannot be negative")
		}

		if err := validateFault(cable.Fault); err != nil {
			return errs.Wrapf(err, "cable %d fault", i)
		}

		if err := c.validateEndpoint(cable.A, hostCables, portCables); err != nil {
			return errs.Wrapf(err, "cable %d endpoint A", i)
		}
		if err := c.validateEndpoint(cable.B, hostCables, portCables); err != nil {
			return errs.Wrapf(err, "cable %d endpoint B", i)
		}
	}

	hostNames := make([]string, 0, len(c.Hosts))
	for name := range c.Hosts {
		hostNames = append(hostNames, name)
	}
	slices.Sort(hostNames)

	for _, name := range hostNames {
		n := hostCables[name]
		if n == 0 {
			return errs.New().Attr("host", name).Msgf("host %q must be connected to a cable", name)
		}
		if n > 1 {
			return errs.New().Attr("host", name).Attr("count", n).Msgf("host %q is connected to %d cables", name, n)
		}
	}

	return nil
}

func (c Config) validateEndpoint(ep Endpoint, hostCables map[string]int, portCables map[Endpoint]int) error {
	if ep.Node == "" {
		return errs.New().Msg("endpoint node name cannot be empty")
	}

	if _, isHost := c.Hosts[ep.Node]; isHost {
		if ep.Port != "" {
			return errs.New().
				Attr("node", ep.Node).
				Attr("port", ep.Port).
				Msgf("host endpoint %q must have an empty port, got %q", ep.Node, ep.Port)
		}
		hostCables[ep.Node]++

		return nil
	}

	swCfg, isSwitch := c.Switches[ep.Node]
	if !isSwitch {
		return errs.New().Attr("node", ep.Node).Msgf("endpoint node %q not found", ep.Node)
	}

	if ep.Port == "" {
		return errs.New().Attr("node", ep.Node).Msgf("switch endpoint %q requires a port name", ep.Node)
	}

	p, ok := swCfg.Ports.Port(ep.Port)
	if !ok {
		return errs.New().
			Attr("node", ep.Node).
			Attr("port", ep.Port).
			Msgf("port %q not found on switch %q", ep.Port, ep.Node)
	}

	if p.Kind == port.Lag {
		return errs.New().
			Attr("node", ep.Node).
			Attr("port", ep.Port).
			Msgf("port %q on switch %q is a LAG and cannot accept cables directly", ep.Port, ep.Node)
	}

	portCables[ep]++
	if portCables[ep] > 1 {
		return errs.New().
			Attr("node", ep.Node).
			Attr("port", ep.Port).
			Msgf("port %q on switch %q is connected to multiple cables", ep.Port, ep.Node)
	}

	return nil
}

func validateFault(f Fault) error {
	switch f.Kind {
	case "", FaultNone, FaultCut, FaultDeadAToB, FaultDeadBToA:
		return nil
	case FaultLoseEveryNth, FaultCorruptEveryNth:
		if f.N == 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault parameter N must be greater than zero")
		}

		return nil
	case FaultLoseSequence:
		if len(f.Sequence) == 0 {
			return errs.New().Attr("kind", f.Kind).Msg("fault sequence cannot be empty")
		}

		return nil
	default:
		return errs.New().Attr("kind", f.Kind).Msgf("unsupported fault kind %q", f.Kind)
	}
}
