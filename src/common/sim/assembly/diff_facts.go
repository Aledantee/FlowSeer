package assembly

import (
	"math"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// lengthFact wraps a cable length in meters as a trace.Fact.
type lengthFact float64

// TypeID returns the fact type identifier for lengthFact.
func (f lengthFact) TypeID() string { return "fabric.length_meters" }

// Canonical returns the decimal string representation of the cable length.
func (f lengthFact) Canonical() string { return strconv.FormatFloat(float64(f), 'f', -1, 64) }

// topSpeedFact wraps a top speed in bits per second as a trace.Fact.
type topSpeedFact uint64

// TypeID returns the fact type identifier for topSpeedFact.
func (f topSpeedFact) TypeID() string { return "fabric.top_speed_bps" }

// Canonical returns the decimal string representation of the top speed.
func (f topSpeedFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// delayFact wraps a cable delay duration as a trace.Fact.
type delayFact time.Duration

// TypeID returns the fact type identifier for delayFact.
func (f delayFact) TypeID() string { return "fabric.delay" }

// Canonical returns the string representation of the delay duration.
func (f delayFact) Canonical() string { return time.Duration(f).String() }

// macFact wraps a netaddr.MAC as a trace.Fact.
type macFact netaddr.MAC

// TypeID returns the fact type identifier for macFact.
func (f macFact) TypeID() string { return "fabric.mac" }

// Canonical returns the formatted MAC address string.
func (f macFact) Canonical() string { return netaddr.MAC(f).String() }

// vlanFact wraps a vlan.ID as a trace.Fact.
type vlanFact vlan.ID

// TypeID returns the fact type identifier for vlanFact.
func (f vlanFact) TypeID() string { return "fabric.vlan" }

// Canonical returns the decimal string representation of the VLAN ID.
func (f vlanFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

type prefixesFact string

func (f prefixesFact) TypeID() string    { return "fabric.prefixes" }
func (f prefixesFact) Canonical() string { return string(f) }

// PrefixesFact returns an immutable, injective snapshot of prefix strings in their supplied order.
func PrefixesFact(prefixes []string) trace.Fact {
	var out strings.Builder
	for i, prefix := range prefixes {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(prefix))
	}

	return prefixesFact(out.String())
}

// boolFact wraps a boolean as a trace.Fact.
type boolFact bool

// TypeID returns the fact type identifier for boolFact.
func (f boolFact) TypeID() string { return "fabric.bool" }

// Canonical returns "true" or "false".
func (f boolFact) Canonical() string { return strconv.FormatBool(bool(f)) }

// gatewayFact wraps a netip.Addr as a trace.Fact.
type gatewayFact netip.Addr

// TypeID returns the fact type identifier for gatewayFact.
func (f gatewayFact) TypeID() string { return "fabric.gateway" }

// Canonical returns the string representation of the gateway IP.
func (f gatewayFact) Canonical() string { return netip.Addr(f).String() }

type faultSnapshotFact string

func (f faultSnapshotFact) TypeID() string { return "fabric.fault" }

func (f faultSnapshotFact) Canonical() string { return string(f) }

type hostSnapshotFact string

func (f hostSnapshotFact) TypeID() string { return "fabric.host" }

func (f hostSnapshotFact) Canonical() string { return string(f) }

type reflectorSnapshotFact string

func (f reflectorSnapshotFact) TypeID() string { return "fabric.reflector" }

func (f reflectorSnapshotFact) Canonical() string { return string(f) }

type attachmentSnapshotFact string

func (f attachmentSnapshotFact) TypeID() string { return "fabric.reflector_attachment" }

func (f attachmentSnapshotFact) Canonical() string { return string(f) }

type attachmentPortFact string

func (f attachmentPortFact) TypeID() string { return "fabric.reflector_attachment_port" }

func (f attachmentPortFact) Canonical() string { return string(f) }

type cableSnapshotFact string

func (f cableSnapshotFact) TypeID() string { return "fabric.cable" }

func (f cableSnapshotFact) Canonical() string { return string(f) }

type phyAssumptionSnapshotFact string

func (f phyAssumptionSnapshotFact) TypeID() string { return "fabric.phy_assumption" }

func (f phyAssumptionSnapshotFact) Canonical() string { return string(f) }

type constructionInputsFact string

func (f constructionInputsFact) TypeID() string { return "fabric.switch_construction_inputs" }

func (f constructionInputsFact) Canonical() string { return string(f) }

type startFact string

func (f startFact) TypeID() string { return "fabric.start" }

func (f startFact) Canonical() string { return string(f) }

func newStartFact(start time.Time) startFact {
	return startFact(start.UTC().Format(time.RFC3339Nano))
}

func switchConstructionInputs(spec vswitch.ConstructionSpec) constructionInputsFact {
	var out strings.Builder
	writeStringField(&out, "node_id", spec.NodeID)
	out.WriteString("seeds=[")
	for i, seed := range spec.Seeds {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		writeUintField(&out, "fid", uint64(seed.FID))
		writeStringField(&out, "mac", seed.MAC.String())
		writeStringField(&out, "port", seed.Port)
		writeStringField(&out, "origin", string(seed.Origin))
		writeStringField(&out, "lifetime", string(seed.Lifetime))
		writeStringField(&out, "learned_at", seed.LearnedAt.UTC().Format(time.RFC3339Nano))
		out.WriteByte('}')
	}
	out.WriteString("];")
	writeMetadata(&out, spec.Metadata)

	return constructionInputsFact(out.String())
}

func writeMetadata(out *strings.Builder, metadata analysis.Metadata) {
	writeStringField(out, "metadata.scope", metadata.Scope().String())
	writeUintField(out, "metadata.status", uint64(metadata.Status()))
	out.WriteString("metadata.issues=[")
	issues := metadata.Issues()
	issueFacts := make([]string, len(issues))
	for i, issue := range issues {
		var fact strings.Builder
		fact.WriteByte('{')
		writeStringField(&fact, "code", issue.Code.String())
		writeUintField(&fact, "status", uint64(issue.Status))
		writeStringField(&fact, "scope", issue.Scope.String())
		fact.WriteByte('}')
		issueFacts[i] = fact.String()
	}
	slices.Sort(issueFacts)
	out.WriteString(strings.Join(issueFacts, ","))
	out.WriteString("];")
	out.WriteString("metadata.assumptions=[")
	for i, assumption := range metadata.Assumptions() {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		writeStringField(out, "scope", assumption.Scope.String())
		writeStringField(out, "statement", assumption.Statement)
		out.WriteByte('}')
	}
	out.WriteString("];")
}

func faultSnapshot(f cablemodel.Fault) faultSnapshotFact {
	var out strings.Builder
	writeStringField(&out, "kind", string(f.Kind))
	writeUintField(&out, "n", uint64(f.N))
	out.WriteString("sequence=[")
	for i, value := range f.Sequence {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.FormatUint(uint64(value), 10))
	}
	out.WriteString("];")

	return faultSnapshotFact(out.String())
}

func phyAssumptionSnapshot(a *cablemodel.PhyAssumption) trace.Fact {
	if a == nil {
		return nil
	}
	var out strings.Builder
	writeStringField(&out, "medium", string(a.Medium))
	writeStringField(&out, "ethernet", a.Ethernet.Canonical())

	return phyAssumptionSnapshotFact(out.String())
}

func hostSnapshot(h hostmodel.Config) hostSnapshotFact {
	var out strings.Builder
	writeStringField(&out, "address", h.Address.String())
	writeStringField(&out, "ethernet", h.Ethernet.Canonical())
	writeStringField(&out, "accept.promiscuous", strconv.FormatBool(h.Accept.Promiscuous))
	writeStringField(&out, "accept.all_multicast", strconv.FormatBool(h.Accept.AllMulticast))
	out.WriteString("accept.multicast=[")
	for i, mac := range h.Accept.Multicast {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(mac.String()))
	}
	out.WriteString("];")
	if h.VLAN == nil {
		out.WriteString("vlan=nil;")
	} else {
		writeUintField(&out, "vlan", uint64(*h.VLAN))
	}
	if h.IP == nil {
		out.WriteString("ip=nil;")

		return hostSnapshotFact(out.String())
	}

	out.WriteString("ip=present;addresses=[")
	for i, prefix := range h.IP.Addresses {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(prefix.String()))
	}
	out.WriteString("];")
	writeStringField(&out, "gateway", h.IP.Gateway.String())

	addrs := make([]netip.Addr, 0, len(h.IP.Neighbors))
	for addr := range h.IP.Neighbors {
		addrs = append(addrs, addr)
	}
	slices.SortFunc(addrs, func(a, b netip.Addr) int {
		return a.Compare(b)
	})
	out.WriteString("neighbors=[")
	for i, addr := range addrs {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(addr.String()))
		out.WriteByte('=')
		out.WriteString(strconv.Quote(h.IP.Neighbors[addr].String()))
	}
	out.WriteString("];")

	return hostSnapshotFact(out.String())
}

func reflectorSnapshot(r reflectormodel.Config) reflectorSnapshotFact {
	var out strings.Builder
	writeStringField(&out, "address", r.Address.String())

	portNames := make([]string, 0, len(r.Ports))
	for name := range r.Ports {
		portNames = append(portNames, name)
	}
	slices.Sort(portNames)
	out.WriteString("ports=[")
	for i, name := range portNames {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		writeStringField(&out, "name", name)
		writeStringField(&out, "ethernet", r.Ports[name].Canonical())
		out.WriteByte('}')
	}
	out.WriteString("];")

	attachNames := make([]string, 0, len(r.Attachments))
	for name := range r.Attachments {
		attachNames = append(attachNames, name)
	}
	slices.Sort(attachNames)
	out.WriteString("attachments=[")
	for i, name := range attachNames {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		writeStringField(&out, "name", name)
		out.WriteString(attachmentFields(r.Attachments[name]))
		out.WriteByte('}')
	}
	out.WriteString("];")

	return reflectorSnapshotFact(out.String())
}

func attachmentSnapshot(a reflectormodel.Attachment) attachmentSnapshotFact {
	return attachmentSnapshotFact(attachmentFields(a))
}

// attachmentFields writes an attachment's fields as key=value pairs, reused by both the
// whole-reflector snapshot and the standalone added/removed attachment snapshot.
func attachmentFields(a reflectormodel.Attachment) string {
	var out strings.Builder
	writeStringField(&out, "port", a.Port)
	if a.VLAN == nil {
		out.WriteString("vlan=nil;")
	} else {
		writeUintField(&out, "vlan", uint64(*a.VLAN))
	}
	out.WriteString("addresses=[")
	for i, s := range sortedPrefixStrings(a.Addresses) {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(s))
	}
	out.WriteString("];")

	return out.String()
}

func cableSnapshot(c fabric.Cable) cableSnapshotFact {
	var out strings.Builder
	writeStringField(&out, "a.node", c.A.Node)
	writeStringField(&out, "a.port", c.A.Port)
	writeStringField(&out, "b.node", c.B.Node)
	writeStringField(&out, "b.port", c.B.Port)
	writeUintField(&out, "length_bits", math.Float64bits(c.LengthMeters))
	writeUintField(&out, "top_speed", c.TopSpeedBPS)
	writeStringField(&out, "fault", faultSnapshot(c.Fault).Canonical())
	writeStringField(&out, "medium", string(c.Medium))
	if c.Delay == nil {
		out.WriteString("delay=nil;")
	} else {
		out.WriteString("delay=")
		out.WriteString(strconv.FormatInt(int64(*c.Delay), 10))
		out.WriteByte(';')
	}

	return cableSnapshotFact(out.String())
}

func writeStringField(out *strings.Builder, name, value string) {
	out.WriteString(name)
	out.WriteByte('=')
	out.WriteString(strconv.Quote(value))
	out.WriteByte(';')
}

func writeUintField(out *strings.Builder, name string, value uint64) {
	out.WriteString(name)
	out.WriteByte('=')
	out.WriteString(strconv.FormatUint(value, 10))
	out.WriteByte(';')
}
