package assembly

import (
	"bytes"
	"cmp"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"time"

	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Config declares the full static topology of a simulated network fabric.
//
// Every non-LAG switch port is cabled, listed in fabric.Uncabled, or unresolved. A nil cablemodel.PhyAssumption leaves every
// unreported physical fact unknown.
type Config struct {
	Start         time.Time
	Switches      map[string]vswitch.Config
	Hosts         map[string]hostmodel.Config
	Reflectors    map[string]reflectormodel.Config
	Cables        []fabric.Cable
	Uncabled      []fabric.Uncabled
	PhyAssumption *cablemodel.PhyAssumption
}

// Clone returns an independent deep copy of the fabric configuration.
func (c Config) Clone() Config {
	cp := Config{
		Start:         c.Start,
		Uncabled:      cloneUncabled(c.Uncabled),
		PhyAssumption: c.PhyAssumption.Clone(),
	}
	if c.Switches != nil {
		cp.Switches = make(map[string]vswitch.Config, len(c.Switches))
		for k, v := range c.Switches {
			cp.Switches[k] = v.Clone()
		}
	}
	if c.Hosts != nil {
		cp.Hosts = make(map[string]hostmodel.Config, len(c.Hosts))
		for k, v := range c.Hosts {
			cp.Hosts[k] = v.Clone()
		}
	}
	if c.Reflectors != nil {
		cp.Reflectors = make(map[string]reflectormodel.Config, len(c.Reflectors))
		for k, v := range c.Reflectors {
			cp.Reflectors[k] = v.Clone()
		}
	}
	if c.Cables != nil {
		cp.Cables = make([]fabric.Cable, len(c.Cables))
		for i, cable := range c.Cables {
			cp.Cables[i] = cable.Clone()
		}
	}

	return cp
}

func cloneUncabled(entries []fabric.Uncabled) []fabric.Uncabled {
	if entries == nil {
		return nil
	}
	cp := make([]fabric.Uncabled, len(entries))
	for i, entry := range entries {
		cp[i] = entry.Clone()
	}

	return cp
}

// Equal reports whether two fabric configurations are identical.
func (c Config) Equal(other Config) bool {
	return equalNormalizedConfigs(c.Normalize(), other.Normalize())
}

func equalNormalizedConfigs(c, other Config) bool {
	if !c.Start.Equal(other.Start) {
		return false
	}
	if !slices.EqualFunc(c.Uncabled, other.Uncabled, fabric.Uncabled.Equal) || !c.PhyAssumption.Equal(other.PhyAssumption) {
		return false
	}
	if len(c.Switches) != len(other.Switches) || len(c.Hosts) != len(other.Hosts) ||
		len(c.Reflectors) != len(other.Reflectors) || len(c.Cables) != len(other.Cables) {
		return false
	}
	for k, sw := range c.Switches {
		otherSW, ok := other.Switches[k]
		if !ok || !sw.Equal(otherSW) {
			return false
		}
	}
	for k, h := range c.Hosts {
		otherH, ok := other.Hosts[k]
		if !ok || !h.Equal(otherH) {
			return false
		}
	}
	for k, r := range c.Reflectors {
		otherR, ok := other.Reflectors[k]
		if !ok || !r.Equal(otherR) {
			return false
		}
	}
	for i := range c.Cables {
		if !c.Cables[i].Equal(other.Cables[i]) {
			return false
		}
	}
	return true
}

// Normalize returns a normalized copy of the fabric configuration with assigned
// hardware addresses across all switches and hosts, normalized switch configurations,
// Ethernet facts normalized as a switch port's are, sorted and deduplicated evidence
// references, and deterministically ordered cables and fabric.Uncabled entries. Duplicate
// fabric.Uncabled entries are kept, so Validate still refuses them.
func (c Config) Normalize() Config {
	cloned := c.Clone()

	usedMACs := make(map[netaddr.MAC]struct{})
	for _, swCfg := range cloned.Switches {
		if swCfg.MAC != (netaddr.MAC{}) {
			usedMACs[swCfg.MAC] = struct{}{}
		}
		if swCfg.STP != nil && swCfg.STP.Address != (netaddr.MAC{}) {
			usedMACs[swCfg.STP.Address] = struct{}{}
		}
		if swCfg.Routing != nil {
			for _, vrf := range swCfg.Routing.VRFs {
				for _, iface := range vrf.Interfaces {
					if iface.MAC != (netaddr.MAC{}) {
						usedMACs[iface.MAC] = struct{}{}
					}
				}
			}
		}
	}
	for _, h := range cloned.Hosts {
		if h.Address != (netaddr.MAC{}) {
			usedMACs[h.Address] = struct{}{}
		}
	}
	for _, r := range cloned.Reflectors {
		if r.Address != (netaddr.MAC{}) {
			usedMACs[r.Address] = struct{}{}
		}
	}

	assignLocal := func() netaddr.MAC {
		for n := uint32(1); ; n++ {
			cand := netaddr.Local(n)
			if _, ok := usedMACs[cand]; !ok {
				usedMACs[cand] = struct{}{}
				return cand
			}
		}
	}

	swNames := make([]string, 0, len(cloned.Switches))
	for name := range cloned.Switches {
		swNames = append(swNames, name)
	}
	slices.Sort(swNames)
	for _, name := range swNames {
		swCfg := cloned.Switches[name]
		if swCfg.MAC == (netaddr.MAC{}) {
			swCfg.MAC = assignLocal()
		}
		cloned.Switches[name] = swCfg.Normalize()
	}

	hNames := make([]string, 0, len(cloned.Hosts))
	for name := range cloned.Hosts {
		hNames = append(hNames, name)
	}
	slices.Sort(hNames)
	for _, name := range hNames {
		h := cloned.Hosts[name]
		if h.Address == (netaddr.MAC{}) {
			h.Address = assignLocal()
		}
		if h.IP != nil && len(h.IP.Addresses) > 0 {
			slices.SortFunc(h.IP.Addresses, comparePrefix)
			h.IP.Addresses = slices.Compact(h.IP.Addresses)
		}
		h.Ethernet = cablemodel.NormalizeEthernet(h.Ethernet)
		if len(h.Accept.Multicast) > 0 {
			slices.SortFunc(h.Accept.Multicast, compareMAC)
			h.Accept.Multicast = slices.Compact(h.Accept.Multicast)
		}
		cloned.Hosts[name] = h
	}

	rNames := make([]string, 0, len(cloned.Reflectors))
	for name := range cloned.Reflectors {
		rNames = append(rNames, name)
	}
	slices.Sort(rNames)
	for _, name := range rNames {
		r := cloned.Reflectors[name]
		if r.Address == (netaddr.MAC{}) {
			r.Address = assignLocal()
		}
		if r.Ports != nil {
			normalizedPorts := make(map[string]phy.Ethernet, len(r.Ports))
			for portName, e := range r.Ports {
				normalizedPorts[portName] = cablemodel.NormalizeEthernet(e)
			}
			r.Ports = normalizedPorts
		}
		if r.Attachments != nil {
			normalizedAttachments := make(map[string]reflectormodel.Attachment, len(r.Attachments))
			for attachName, a := range r.Attachments {
				if len(a.Addresses) > 0 {
					a.Addresses = slices.Clone(a.Addresses)
					slices.SortFunc(a.Addresses, comparePrefix)
					a.Addresses = slices.Compact(a.Addresses)
				}
				normalizedAttachments[attachName] = a
			}
			r.Attachments = normalizedAttachments
		}
		cloned.Reflectors[name] = r
	}

	for i := range cloned.Cables {
		cable := cloned.Cables[i]
		if cable.LengthMeters == 0 {
			cable.LengthMeters = 0
		}
		cable.Fault = normalizedFault(cable.Fault)
		cable.Evidence = canonicalEvidence(cable.Evidence)
		cloned.Cables[i] = canonicalCableOrientation(cable)
	}

	for i := range cloned.Uncabled {
		cloned.Uncabled[i].Evidence = canonicalEvidence(cloned.Uncabled[i].Evidence)
	}
	slices.SortStableFunc(cloned.Uncabled, func(a, b fabric.Uncabled) int {
		return compareEndpoint(a.Endpoint, b.Endpoint)
	})

	if cloned.PhyAssumption != nil {
		cloned.PhyAssumption.Ethernet = cablemodel.NormalizeEthernet(cloned.PhyAssumption.Ethernet)
	}

	slices.SortFunc(cloned.Cables, func(i, j fabric.Cable) int {
		if r := cmp.Compare(i.A.Node, j.A.Node); r != 0 {
			return r
		}
		if r := cmp.Compare(i.A.Port, j.A.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(i.B.Node, j.B.Node); r != 0 {
			return r
		}

		return cmp.Compare(i.B.Port, j.B.Port)
	})

	return cloned
}

func canonicalEvidence(refs []trace.EvidenceRef) []trace.EvidenceRef {
	if len(refs) == 0 {
		return nil
	}
	sorted := slices.Clone(refs)
	slices.Sort(sorted)

	return slices.Compact(sorted)
}

func canonicalCableOrientation(c fabric.Cable) fabric.Cable {
	if compareEndpoint(c.A, c.B) <= 0 {
		return c
	}

	c.A, c.B = c.B, c.A
	switch c.Fault.Kind {
	case cablemodel.FaultDeadAToB:
		c.Fault.Kind = cablemodel.FaultDeadBToA
	case cablemodel.FaultDeadBToA:
		c.Fault.Kind = cablemodel.FaultDeadAToB
	}

	return c
}

func compareEndpoint(a, b fabric.Endpoint) int {
	if order := cmp.Compare(a.Node, b.Node); order != 0 {
		return order
	}

	return cmp.Compare(a.Port, b.Port)
}

func compareMAC(a, b netaddr.MAC) int {
	return bytes.Compare(a[:], b[:])
}

func comparePrefix(a, b netip.Prefix) int {
	if order := a.Addr().Compare(b.Addr()); order != 0 {
		return order
	}

	return cmp.Compare(a.Bits(), b.Bits())
}

// Validate verifies structural and topological invariants of the configuration:
// switch configurations must pass their own validation, endpoints must reference existing
// nodes and ports, cable attachments cannot target LAGs or duplicate existing links,
// hosts must attach to exactly one cable with an empty port name, a reflector's ports must
// all be cabled, host and assumed Ethernet facts must pass a switch port's physical
// validation, a host's accepted multicast entries must be group addresses, and every
// fabric.Uncabled entry must name an existing non-LAG switch port that no cable and no other entry
// names. Errors about host facts, reflector attachments, fabric.Uncabled entries, and the
// assumption carry a "field" path attribute such as "uncabled.1", "hosts.h1.ethernet.duplex",
// "hosts.h1.accept.multicast.0", or "reflectors.r1.attachments.b.vlan".
func (c Config) Validate() error {
	swNames := make([]string, 0, len(c.Switches))
	for name := range c.Switches {
		swNames = append(swNames, name)
	}
	slices.Sort(swNames)

	hostNames := make([]string, 0, len(c.Hosts))
	for name := range c.Hosts {
		hostNames = append(hostNames, name)
	}
	slices.Sort(hostNames)

	reflectorNames := make([]string, 0, len(c.Reflectors))
	for name := range c.Reflectors {
		reflectorNames = append(reflectorNames, name)
	}
	slices.Sort(reflectorNames)

	claimedMACs := make(map[netaddr.MAC]string)
	checkMAC := func(node string, mac netaddr.MAC) error {
		if mac == (netaddr.MAC{}) {
			return nil
		}
		if prevNode, ok := claimedMACs[mac]; ok && prevNode != node {
			return errs.New().
				Attr("node", node).
				Attr("mac", mac).
				Attr("collides_with", prevNode).
				Msgf("MAC address %s on %q collides with %q", mac, node, prevNode)
		}
		claimedMACs[mac] = node

		return nil
	}

	for _, name := range swNames {
		if name == "" {
			return errs.New().Msg("switch name cannot be empty")
		}
		if _, isReflector := c.Reflectors[name]; isReflector {
			return errs.New().Attr("node", name).Msgf("node %q cannot be both a switch and a reflector", name)
		}
		swCfg := c.Switches[name]
		// A protocol layer schedules its first hello from Start; a zero Start
		// puts every wake in year 1, ahead of any frame a caller injects.
		if swCfg.STP != nil && c.Start.IsZero() {
			return errs.New().Attr("switch", name).Msg("a fabric with a spanning tree switch needs a Start time")
		}
		if err := swCfg.Validate(); err != nil {
			return errs.Wrapf(err, "switch %q", name)
		}
		if err := checkMAC(name, swCfg.MAC); err != nil {
			return err
		}
		if swCfg.STP != nil {
			if err := checkMAC(name, swCfg.STP.Address); err != nil {
				return err
			}
		}
		if swCfg.Routing != nil {
			for _, vrf := range swCfg.Routing.VRFs {
				ifaceNames := make([]string, 0, len(vrf.Interfaces))
				for ifName := range vrf.Interfaces {
					ifaceNames = append(ifaceNames, ifName)
				}
				slices.Sort(ifaceNames)
				for _, ifName := range ifaceNames {
					if err := checkMAC(name, vrf.Interfaces[ifName].MAC); err != nil {
						return err
					}
				}
			}
		}
	}

	for _, name := range hostNames {
		if name == "" {
			return errs.New().Msg("host name cannot be empty")
		}
		if _, isSw := c.Switches[name]; isSw {
			return errs.New().Attr("node", name).Msgf("node %q cannot be both a switch and a host", name)
		}
		if _, isReflector := c.Reflectors[name]; isReflector {
			return errs.New().Attr("field", "hosts."+name).Attr("node", name).Msgf("node %q cannot be both a host and a reflector", name)
		}
		h := c.Hosts[name]
		if err := checkMAC(name, h.Address); err != nil {
			return err
		}
		if h.VLAN != nil && !h.VLAN.Valid() {
			return errs.New().
				Attr("host", name).
				Attr("vlan", *h.VLAN).
				Msgf("host %q has invalid VLAN ID %d", name, *h.VLAN)
		}
		if err := cablemodel.ValidateEthernet("hosts."+name+".ethernet", h.Ethernet); err != nil {
			return errs.Wrapf(err, "host %q", name)
		}
		if err := h.Accept.Validate("hosts." + name + ".accept"); err != nil {
			return err
		}
		if h.IP != nil {
			if len(h.IP.Addresses) == 0 {
				return errs.New().
					Attr("host", name).
					Msgf("host %q with IP stack must configure at least one address", name)
			}
			rtCfg, tbl := hostmodel.RoutingConfig(name, h)
			if err := rtCfg.Validate(layer.Env{Ports: tbl}); err != nil {
				return errs.Wrapf(err, "host %q", name)
			}
		}
	}

	// A collision between a reflector's name and a switch's or a host's is
	// caught in those loops, above, which run first and already check the
	// reflector map; checking the other direction here would never trigger.
	for _, name := range reflectorNames {
		if name == "" {
			return errs.New().Msg("reflector name cannot be empty")
		}
		refl := c.Reflectors[name]
		if err := checkMAC(name, refl.Address); err != nil {
			return err
		}
		if err := refl.Validate("reflectors." + name); err != nil {
			return err
		}
	}

	hostCables := make(map[string]int, len(c.Hosts))
	portCables := make(map[fabric.Endpoint]int)

	for i, cable := range c.Cables {
		if math.IsNaN(cable.LengthMeters) || math.IsInf(cable.LengthMeters, 0) || cable.LengthMeters < 0 {
			return errs.New().
				Attr("cable", i).
				Attr("length", cable.LengthMeters).
				Msg("cable length must be finite and non-negative")
		}

		if cable.Delay != nil && *cable.Delay < 0 {
			return errs.New().
				Attr("cable", i).
				Attr("delay", *cable.Delay).
				Msg("cable delay cannot be negative")
		}

		if err := cablemodel.ValidateMedium("cables."+strconv.Itoa(i)+".medium", cable.Medium); err != nil {
			return errs.Wrapf(err, "cable %d", i)
		}

		if err := cablemodel.ValidateFault(cable.Fault); err != nil {
			return errs.Wrapf(err, "cable %d fault", i)
		}

		if err := c.validateEndpoint(cable.A, hostCables, portCables); err != nil {
			return errs.Wrapf(err, "cable %d endpoint A", i)
		}
		if err := c.validateEndpoint(cable.B, hostCables, portCables); err != nil {
			return errs.Wrapf(err, "cable %d endpoint B", i)
		}
	}

	for _, name := range hostNames {
		n := hostCables[name]
		if n == 0 {
			return errs.New().Attr("host", name).Msgf("host %q must be connected to a cable", name)
		}
		if n > 1 {
			return errs.New().Attr("host", name).Attr("count", n).Msgf("host %q is connected to %d cables", name, n)
		}
	}

	for _, name := range reflectorNames {
		refl := c.Reflectors[name]
		portNames := make([]string, 0, len(refl.Ports))
		for portName := range refl.Ports {
			portNames = append(portNames, portName)
		}
		slices.Sort(portNames)
		for _, portName := range portNames {
			ep := fabric.Endpoint{Node: name, Port: portName}
			if portCables[ep] == 0 {
				return errs.New().
					Attr("field", "reflectors."+name+".ports."+portName).
					Attr("node", name).
					Attr("port", portName).
					Msgf("reflector port %q on %q must be connected to a cable", portName, name)
			}
		}
	}

	if err := c.validateUncabled(portCables); err != nil {
		return err
	}

	return c.PhyAssumption.Validate()
}

func (c Config) validateUncabled(portCables map[fabric.Endpoint]int) error {
	listed := make(map[fabric.Endpoint]int, len(c.Uncabled))
	for i, entry := range c.Uncabled {
		field := "uncabled." + strconv.Itoa(i)
		ep := entry.Endpoint
		failure := errs.New().Attr("field", field).Attr("node", ep.Node).Attr("port", ep.Port)

		swCfg, isSwitch := c.Switches[ep.Node]
		if !isSwitch {
			return failure.Msgf("uncabled entry names %q, which is not a switch", ep.Node)
		}
		p, ok := swCfg.Ports.Port(ep.Port)
		if !ok {
			return failure.Msgf("uncabled entry names port %q, which switch %q does not have", ep.Port, ep.Node)
		}
		if p.Kind == port.LAG {
			return failure.Msgf("uncabled entry names LAG %q on switch %q, which takes no cable", ep.Port, ep.Node)
		}
		if portCables[ep] > 0 {
			return failure.Msgf("uncabled entry names port %q on switch %q, which a cable names", ep.Port, ep.Node)
		}
		if first, dup := listed[ep]; dup {
			return failure.Attr("duplicates", "uncabled."+strconv.Itoa(first)).
				Msgf("uncabled entry names port %q on switch %q twice", ep.Port, ep.Node)
		}
		listed[ep] = i
	}

	return nil
}

func (c Config) validateEndpoint(ep fabric.Endpoint, hostCables map[string]int, portCables map[fabric.Endpoint]int) error {
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

	if refl, isReflector := c.Reflectors[ep.Node]; isReflector {
		if ep.Port == "" {
			return errs.New().
				Attr("field", "reflectors."+ep.Node+".ports").
				Attr("node", ep.Node).
				Msgf("reflector endpoint %q requires a port name", ep.Node)
		}
		if _, ok := refl.Ports[ep.Port]; !ok {
			return errs.New().
				Attr("node", ep.Node).
				Attr("port", ep.Port).
				Msgf("port %q not found on reflector %q", ep.Port, ep.Node)
		}
		portCables[ep]++
		if portCables[ep] > 1 {
			return errs.New().
				Attr("node", ep.Node).
				Attr("port", ep.Port).
				Msgf("port %q on reflector %q is connected to multiple cables", ep.Port, ep.Node)
		}

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

	if p.Kind == port.LAG {
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

func sameLengthMeters(a, b float64) bool { return cmp.Compare(a, b) == 0 }
