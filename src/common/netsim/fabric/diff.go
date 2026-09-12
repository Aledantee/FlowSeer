package fabric

import (
	"cmp"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
)

// LengthFact wraps a cable length in meters as a trace.Fact.
type LengthFact float64

// TypeID returns the fact type identifier for LengthFact.
func (f LengthFact) TypeID() string { return "fabric.length_meters" }

// Canonical returns the decimal string representation of the cable length.
func (f LengthFact) Canonical() string { return strconv.FormatFloat(float64(f), 'f', -1, 64) }

// TopSpeedFact wraps a top speed in bits per second as a trace.Fact.
type TopSpeedFact uint64

// TypeID returns the fact type identifier for TopSpeedFact.
func (f TopSpeedFact) TypeID() string { return "fabric.top_speed_bps" }

// Canonical returns the decimal string representation of the top speed.
func (f TopSpeedFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// DelayFact wraps a cable delay duration as a trace.Fact.
type DelayFact time.Duration

// TypeID returns the fact type identifier for DelayFact.
func (f DelayFact) TypeID() string { return "fabric.delay" }

// Canonical returns the string representation of the delay duration.
func (f DelayFact) Canonical() string { return time.Duration(f).String() }

// MACFact wraps a netaddr.MAC as a trace.Fact.
type MACFact netaddr.MAC

// TypeID returns the fact type identifier for MACFact.
func (f MACFact) TypeID() string { return "fabric.mac" }

// Canonical returns the formatted MAC address string.
func (f MACFact) Canonical() string { return netaddr.MAC(f).String() }

// VLANFact wraps a vlan.ID as a trace.Fact.
type VLANFact vlan.ID

// TypeID returns the fact type identifier for VLANFact.
func (f VLANFact) TypeID() string { return "fabric.vlan" }

// Canonical returns the decimal string representation of the VLAN ID.
func (f VLANFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

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

// GatewayFact wraps a netip.Addr as a trace.Fact.
type GatewayFact netip.Addr

// TypeID returns the fact type identifier for GatewayFact.
func (f GatewayFact) TypeID() string { return "fabric.gateway" }

// Canonical returns the string representation of the gateway IP.
func (f GatewayFact) Canonical() string { return netip.Addr(f).String() }

type faultSnapshotFact string

func (f faultSnapshotFact) TypeID() string { return "fabric.fault" }

func (f faultSnapshotFact) Canonical() string { return string(f) }

type hostSnapshotFact string

func (f hostSnapshotFact) TypeID() string { return "fabric.host" }

func (f hostSnapshotFact) Canonical() string { return string(f) }

type cableSnapshotFact string

func (f cableSnapshotFact) TypeID() string { return "fabric.cable" }

func (f cableSnapshotFact) Canonical() string { return string(f) }

// Diff computes the differences between two fabric configurations, reporting switch differences,
// cable additions, removals, and modifications, and host additions, removals, and moves.
func Diff(a, b Config) []trace.Change {
	a = a.Normalize()
	b = b.Normalize()

	var changes []trace.Change

	swNames := make(map[string]struct{})
	for name := range a.Switches {
		swNames[name] = struct{}{}
	}
	for name := range b.Switches {
		swNames[name] = struct{}{}
	}
	sortedSw := make([]string, 0, len(swNames))
	for name := range swNames {
		sortedSw = append(sortedSw, name)
	}
	slices.Sort(sortedSw)

	for _, name := range sortedSw {
		swA, inA := a.Switches[name]
		swB, inB := b.Switches[name]
		switch {
		case inA && !inB:
			changes = append(changes, trace.Change{
				Layer: Layer,
				Subject: trace.Subject{
					Kind: "switch",
					Key:  name,
				},
				Field: "",
				From:  vswitch.ConfigFact(swA),
				To:    nil,
			})
		case !inA && inB:
			changes = append(changes, trace.Change{
				Layer: Layer,
				Subject: trace.Subject{
					Kind: "switch",
					Key:  name,
				},
				Field: "",
				From:  nil,
				To:    vswitch.ConfigFact(swB),
			})
		case inA && inB:
			swChanges := vswitch.Diff(swA, swB)
			for _, ch := range swChanges {
				ch.Subject.Key = name + "/" + ch.Subject.Key
				changes = append(changes, ch)
			}
		}
	}

	cablesA := sortedCables(a.Cables)
	cablesB := sortedCables(b.Cables)

	mapA := make(map[string]Cable, len(cablesA))
	for _, c := range cablesA {
		mapA[cableKey(c)] = c
	}
	mapB := make(map[string]Cable, len(cablesB))
	for _, c := range cablesB {
		mapB[cableKey(c)] = c
	}

	for _, cA := range cablesA {
		key := cableKey(cA)
		cB, exists := mapB[key]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "",
				From:    cableSnapshot(cA),
				To:      nil,
			})
			continue
		}
		cA = canonicalCableOrientation(cA)
		cB = canonicalCableOrientation(cB)

		if cA.LengthMeters != cB.LengthMeters {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "length",
				From:    LengthFact(cA.LengthMeters),
				To:      LengthFact(cB.LengthMeters),
			})
		}
		if mA, mB := cA.Medium, cB.Medium; mA != mB {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "medium",
				From:    mA,
				To:      mB,
			})
		}
		if !samePtr(cA.Delay, cB.Delay) {
			var fromDelay, toDelay trace.Fact
			if cA.Delay != nil {
				fromDelay = DelayFact(*cA.Delay)
			}
			if cB.Delay != nil {
				toDelay = DelayFact(*cB.Delay)
			}
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "delay",
				From:    fromDelay,
				To:      toDelay,
			})
		}
		if cA.TopSpeedBPS != cB.TopSpeedBPS {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "top_speed",
				From:    TopSpeedFact(cA.TopSpeedBPS),
				To:      TopSpeedFact(cB.TopSpeedBPS),
			})
		}
		if !sameFault(cA.Fault, cB.Fault) {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "fault",
				From:    faultSnapshot(cA.Fault),
				To:      faultSnapshot(cB.Fault),
			})
		}
	}

	for _, cB := range cablesB {
		key := cableKey(cB)
		if _, exists := mapA[key]; !exists {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "",
				From:    nil,
				To:      cableSnapshot(cB),
			})
		}
	}

	hostNames := make(map[string]struct{})
	for name := range a.Hosts {
		hostNames[name] = struct{}{}
	}
	for name := range b.Hosts {
		hostNames[name] = struct{}{}
	}
	sortedHosts := make([]string, 0, len(hostNames))
	for name := range hostNames {
		sortedHosts = append(sortedHosts, name)
	}
	slices.Sort(sortedHosts)

	for _, name := range sortedHosts {
		hA, inA := a.Hosts[name]
		hB, inB := b.Hosts[name]
		switch {
		case inA && !inB:
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "host", Key: name},
				Field:   "",
				From:    hostSnapshot(hA),
				To:      nil,
			})
		case !inA && inB:
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "host", Key: name},
				Field:   "",
				From:    nil,
				To:      hostSnapshot(hB),
			})
		case inA && inB:
			epA, okA := hostEndpoint(name, a.Cables)
			epB, okB := hostEndpoint(name, b.Cables)
			if okA && okB && epA != epB {
				changes = append(changes, trace.Change{
					Layer:   Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   "port",
					From:    epA,
					To:      epB,
				})
			}
			if hA.Address != hB.Address {
				changes = append(changes, trace.Change{
					Layer:   Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   "address",
					From:    MACFact(hA.Address),
					To:      MACFact(hB.Address),
				})
			}
			if !samePtr(hA.VLAN, hB.VLAN) {
				var fromVal, toVal trace.Fact
				if hA.VLAN != nil {
					fromVal = VLANFact(*hA.VLAN)
				}
				if hB.VLAN != nil {
					toVal = VLANFact(*hB.VLAN)
				}
				changes = append(changes, trace.Change{
					Layer:   Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   "vlan",
					From:    fromVal,
					To:      toVal,
				})
			}
			diffHostIP(&changes, name, hA.IP, hB.IP)
		}
	}

	return changes
}

func diffHostIP(changes *[]trace.Change, name string, a, b *HostIP) {
	if a == nil && b == nil {
		return
	}

	var (
		pfxA []string
		pfxB []string
	)
	if a != nil {
		pfxA = sortedPrefixStrings(a.Addresses)
	}
	if b != nil {
		pfxB = sortedPrefixStrings(b.Addresses)
	}

	if !slices.Equal(pfxA, pfxB) {
		var fromVal, toVal trace.Fact
		if a != nil {
			fromVal = PrefixesFact(pfxA)
		}
		if b != nil {
			toVal = PrefixesFact(pfxB)
		}
		*changes = append(*changes, trace.Change{
			Layer:   Layer,
			Subject: trace.Subject{Kind: "host", Key: name},
			Field:   "addresses",
			From:    fromVal,
			To:      toVal,
		})
	}

	var (
		gwA netip.Addr
		gwB netip.Addr
	)
	if a != nil {
		gwA = a.Gateway
	}
	if b != nil {
		gwB = b.Gateway
	}
	if gwA != gwB {
		var fromVal, toVal trace.Fact
		if gwA.IsValid() {
			fromVal = GatewayFact(gwA)
		}
		if gwB.IsValid() {
			toVal = GatewayFact(gwB)
		}
		*changes = append(*changes, trace.Change{
			Layer:   Layer,
			Subject: trace.Subject{Kind: "host", Key: name},
			Field:   "gateway",
			From:    fromVal,
			To:      toVal,
		})
	}

	var (
		nbrA map[netip.Addr]netaddr.MAC
		nbrB map[netip.Addr]netaddr.MAC
	)
	if a != nil {
		nbrA = a.Neighbors
	}
	if b != nil {
		nbrB = b.Neighbors
	}

	neighborAddrs := make(map[netip.Addr]struct{})
	for addr := range nbrA {
		neighborAddrs[addr] = struct{}{}
	}
	for addr := range nbrB {
		neighborAddrs[addr] = struct{}{}
	}

	sortedAddrs := make([]netip.Addr, 0, len(neighborAddrs))
	for addr := range neighborAddrs {
		sortedAddrs = append(sortedAddrs, addr)
	}
	slices.SortFunc(sortedAddrs, func(x, y netip.Addr) int {
		return x.Compare(y)
	})

	for _, addr := range sortedAddrs {
		macA, inA := nbrA[addr]
		macB, inB := nbrB[addr]
		field := "neighbors." + addr.String()
		switch {
		case inA && !inB:
			*changes = append(*changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "host", Key: name},
				Field:   field,
				From:    MACFact(macA),
				To:      nil,
			})
		case !inA && inB:
			*changes = append(*changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "host", Key: name},
				Field:   field,
				From:    nil,
				To:      MACFact(macB),
			})
		case inA && inB:
			if macA != macB {
				*changes = append(*changes, trace.Change{
					Layer:   Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   field,
					From:    MACFact(macA),
					To:      MACFact(macB),
				})
			}
		}
	}
}

func sortedPrefixStrings(prefixes []netip.Prefix) []string {
	if len(prefixes) == 0 {
		return nil
	}
	strs := make([]string, len(prefixes))
	for i, p := range prefixes {
		strs[i] = p.String()
	}
	slices.Sort(strs)

	return strs
}

func sortedCables(cables []Cable) []Cable {
	if len(cables) == 0 {
		return nil
	}
	cp := make([]Cable, len(cables))
	copy(cp, cables)
	slices.SortFunc(cp, func(i, j Cable) int {
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
	return cp
}

// cableKey names a cable by its two ends with the smaller end first, so the
// same cable written in either orientation is one subject. Diff aligns
// directional faults to that ordering before comparing them.
func cableKey(c Cable) string {
	a := c.A.Canonical()
	b := c.B.Canonical()
	if b < a {
		a, b = b, a
	}

	return a + "-" + b
}

func canonicalCableOrientation(c Cable) Cable {
	a := c.A.Canonical()
	b := c.B.Canonical()
	if a <= b {
		return c
	}

	c.A, c.B = c.B, c.A
	switch c.Fault.Kind {
	case FaultDeadAToB:
		c.Fault.Kind = FaultDeadBToA
	case FaultDeadBToA:
		c.Fault.Kind = FaultDeadAToB
	}

	return c
}

// normalizedFault reads an unset kind as FaultNone, so a change reports the
// kind a reader compares against.
func normalizedFault(f Fault) Fault {
	f = f.Clone()
	if f.Kind == "" {
		f.Kind = FaultNone
	}
	if len(f.Sequence) > 0 {
		slices.Sort(f.Sequence)
		f.Sequence = slices.Compact(f.Sequence)
	}

	return f
}

func sameFault(a, b Fault) bool {
	return a.Kind == b.Kind && a.N == b.N && slices.Equal(a.Sequence, b.Sequence)
}

// normalizedMedium reads an unset medium as TwistedPair, so a change reports
// the medium a reader compares against.
func normalizedMedium(m Medium) Medium {
	if m == "" {
		return TwistedPair
	}

	return m
}

func samePtr[T comparable](a, b *T) bool {
	if (a == nil) != (b == nil) {
		return false
	}

	return a == nil || *a == *b
}

func hostEndpoint(name string, cables []Cable) (Endpoint, bool) {
	for _, c := range cables {
		if c.A.Node == name {
			return c.B, true
		}
		if c.B.Node == name {
			return c.A, true
		}
	}
	return Endpoint{}, false
}

func faultSnapshot(f Fault) faultSnapshotFact {
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

func hostSnapshot(h Host) hostSnapshotFact {
	var out strings.Builder
	writeStringField(&out, "address", h.Address.String())
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

func cableSnapshot(c Cable) cableSnapshotFact {
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
