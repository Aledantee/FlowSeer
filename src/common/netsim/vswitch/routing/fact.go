package routing

import (
	"net/netip"
	"strconv"
	"strings"

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

// routeSnapshot records what one lookup answered: the route the packet takes, the whole
// equal-cost set it came from in canonical order, and the layer-3 fields and hash that
// picked it out, so a reader can tell an alternative path from a path that was never there.
func routeSnapshot(vrf string, destination netip.Addr, sel *selection) trace.Fact {
	if sel == nil {
		return routeDecisionFact("vrf=" + strconv.Quote(vrf) +
			";destination=" + strconv.Quote(destination.String()) + ";matched=false")
	}

	route := sel.route()
	var b strings.Builder
	b.WriteString("vrf=" + strconv.Quote(vrf) +
		";destination=" + strconv.Quote(destination.String()) +
		";matched=true;prefix=" + strconv.Quote(route.Prefix.String()) +
		";next_hop=" + strconv.Quote(route.NextHop.String()) +
		";interface=" + strconv.Quote(route.Interface) +
		";kind=" + strconv.Quote(string(route.kind)) +
		";hash_src=" + strconv.Quote(sel.src.String()) +
		";hash_dst=" + strconv.Quote(sel.dst.String()) +
		";hash_flow_label=" + strconv.FormatUint(uint64(sel.flowLabel), 10) +
		";hash=" + strconv.FormatUint(uint64(sel.hash), 10) +
		";chosen=" + strconv.Itoa(sel.chosen) +
		";candidates=[")
	for i, c := range sel.candidates {
		if i > 0 {
			b.WriteString(",")
		}
		forwarded := c.NextHop
		if c.resolvedNextHop.IsValid() {
			forwarded = c.resolvedNextHop
		}
		b.WriteString(c.NextHop.String() + "|" + c.Interface + "|" + forwarded.String())
	}
	b.WriteString("]")

	return routeDecisionFact(b.String())
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
