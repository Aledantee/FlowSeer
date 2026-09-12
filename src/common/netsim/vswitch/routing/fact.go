package routing

import (
	"net/netip"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

type packetDecisionFact string

func (f packetDecisionFact) TypeID() string    { return "routing.packet_decision" }
func (f packetDecisionFact) Canonical() string { return string(f) }

type routeDecisionFact string

func (f routeDecisionFact) TypeID() string    { return "routing.lookup_decision" }
func (f routeDecisionFact) Canonical() string { return string(f) }

type neighborDecisionFact string

func (f neighborDecisionFact) TypeID() string    { return "routing.neighbor_decision" }
func (f neighborDecisionFact) Canonical() string { return string(f) }

func packetSnapshot(iface string, f ethernet.Frame, header ip.Header, valid bool, reason trace.Reason) trace.Fact {
	return packetDecisionFact("interface=" + strconv.Quote(iface) +
		";ether_type=" + strconv.FormatUint(uint64(f.EtherType), 10) +
		";src=" + strconv.Quote(header.Src.String()) +
		";dst=" + strconv.Quote(header.Dst.String()) +
		";hop_limit=" + strconv.FormatUint(uint64(header.HopLimit), 10) +
		";valid=" + strconv.FormatBool(valid) +
		";reason=" + strconv.Quote(string(reason)))
}

func routeSnapshot(vrf string, destination netip.Addr, route *routeEntry) trace.Fact {
	if route == nil {
		return routeDecisionFact("vrf=" + strconv.Quote(vrf) +
			";destination=" + strconv.Quote(destination.String()) + ";matched=false")
	}

	return routeDecisionFact("vrf=" + strconv.Quote(vrf) +
		";destination=" + strconv.Quote(destination.String()) +
		";matched=true;prefix=" + strconv.Quote(route.Prefix.String()) +
		";next_hop=" + strconv.Quote(route.NextHop.String()) +
		";interface=" + strconv.Quote(route.Interface) +
		";kind=" + strconv.Quote(string(route.kind)))
}

func neighborSnapshot(iface string, addr netip.Addr, neighbor Neighbor, present bool) trace.Fact {
	return neighborDecisionFact("interface=" + strconv.Quote(iface) +
		";address=" + strconv.Quote(addr.String()) +
		";present=" + strconv.FormatBool(present) +
		";mac=" + strconv.Quote(neighbor.MAC.String()))
}

// EgressFact returns an immutable snapshot of the routed interface and selected
// physical or aggregate member used by switch composition.
func EgressFact(iface, portName, member string, reason trace.Reason) trace.Fact {
	return routeDecisionFact("interface=" + strconv.Quote(iface) +
		";port=" + strconv.Quote(portName) +
		";member=" + strconv.Quote(member) +
		";reason=" + strconv.Quote(string(reason)))
}
