package assembly

import (
	"cmp"
	"net/netip"
	"slices"

	"go.aledante.io/FlowSeer/src/common/sim/fabric"

	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// DiffSpecs computes the differences between two construction specifications. It reports
// switch configuration and construction-input differences, cable changes, and host changes.
// Evidence catalogs and references are not behavior and produce no change.
func DiffSpecs(a, b ConstructionSpec) ([]trace.Change, error) {
	a, err := a.Normalize()
	if err != nil {
		return nil, err
	}
	b, err = b.Normalize()
	if err != nil {
		return nil, err
	}

	changes := Diff(a.Config(), b.Config())
	names := make(map[string]struct{}, len(a.Switches)+len(b.Switches))
	for name := range a.Switches {
		names[name] = struct{}{}
	}
	for name := range b.Switches {
		names[name] = struct{}{}
	}
	sortedNames := make([]string, 0, len(names))
	for name := range names {
		sortedNames = append(sortedNames, name)
	}
	slices.Sort(sortedNames)
	for _, name := range sortedNames {
		specA, inA := a.Switches[name]
		specB, inB := b.Switches[name]
		var from, to trace.Fact
		if inA {
			from = switchConstructionInputs(specA)
		}
		if inB {
			to = switchConstructionInputs(specB)
		}
		if trace.EqualFact(from, to) {
			continue
		}
		changes = append(changes, trace.Change{
			Layer:   fabric.Layer,
			Subject: trace.Subject{Kind: "switch", Key: name},
			Field:   "construction_inputs",
			From:    from,
			To:      to,
		})
	}

	slices.SortFunc(changes, func(a, b trace.Change) int {
		if order := a.Subject.Compare(b.Subject); order != 0 {
			return order
		}
		return cmp.Compare(a.Field, b.Field)
	})

	return changes, nil
}

// Diff computes the differences between two fabric configurations, reporting switch differences,
// cable additions, removals, and modifications, host additions, removals, moves, and field changes
// (an accepted multicast MAC added or removed is one change under "accept.multicast.<mac>"),
// reflector additions, removals, and field changes (a port or an attachment added, removed, or
// changed), fabric.Uncabled entries added or removed, and a change of the physical assumption as one
// fabric field. Evidence references produce no change.
func Diff(a, b Config) []trace.Change {
	a = a.Normalize()
	b = b.Normalize()

	var changes []trace.Change
	if !a.Start.Equal(b.Start) {
		changes = append(changes, trace.Change{
			Layer:   fabric.Layer,
			Subject: trace.Subject{Kind: "fabric"},
			Field:   "start",
			From:    newStartFact(a.Start),
			To:      newStartFact(b.Start),
		})
	}
	if !a.PhyAssumption.Equal(b.PhyAssumption) {
		changes = append(changes, trace.Change{
			Layer:   fabric.Layer,
			Subject: trace.Subject{Kind: "fabric"},
			Field:   "phy_assumption",
			From:    phyAssumptionSnapshot(a.PhyAssumption),
			To:      phyAssumptionSnapshot(b.PhyAssumption),
		})
	}
	changes = append(changes, diffUncabled(a.Uncabled, b.Uncabled)...)

	changes = append(changes, diffSwitches(a, b)...)
	changes = append(changes, diffCables(a, b)...)
	changes = append(changes, diffHosts(a, b)...)
	changes = append(changes, diffReflectors(a, b)...)

	return changes
}

// diffPorts reports a reflector's port changes under the reflector subject,
// reusing phy's own per-field Ethernet comparison and walking ports in sorted
// name order the way phy.Diff already does. An added or removed port reports
// its Ethernet snapshot under "ports.<name>"; a changed one reports phy's own
// field name under "ports.<name>.<field>", such as "ports.p1.speed_bps".
func diffPorts(changes *[]trace.Change, reflectorName string, a, b map[string]phy.Ethernet) {
	subject := trace.Subject{Kind: "reflector", Key: reflectorName}

	for _, ch := range phy.Diff(phy.Config{Ethernet: a}, phy.Config{Ethernet: b}) {
		portName := ch.Subject.Key
		ch.Subject = subject
		if ch.Field == "" {
			ch.Field = "ports." + portName
		} else {
			ch.Field = "ports." + portName + "." + ch.Field
		}
		*changes = append(*changes, ch)
	}
}

func diffUncabled(a, b []fabric.Uncabled) []trace.Change {
	inA := make(map[fabric.Endpoint]bool, len(a))
	for _, entry := range a {
		inA[entry.Endpoint] = true
	}
	inB := make(map[fabric.Endpoint]bool, len(b))
	for _, entry := range b {
		inB[entry.Endpoint] = true
	}

	var changes []trace.Change
	for _, entry := range a {
		if !inB[entry.Endpoint] {
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "uncabled", Key: entry.Endpoint.Canonical()},
				From:    entry.Endpoint,
			})
		}
	}
	for _, entry := range b {
		if !inA[entry.Endpoint] {
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "uncabled", Key: entry.Endpoint.Canonical()},
				To:      entry.Endpoint,
			})
		}
	}

	return changes
}

// diffHostEthernet reports a host's Ethernet facts with phy's own field names,
// prefixed with "ethernet.", under the host subject.
func diffHostEthernet(changes *[]trace.Change, name string, a, b phy.Ethernet) {
	for _, ch := range phy.Diff(phy.Config{Ethernet: map[string]phy.Ethernet{name: a}}, phy.Config{Ethernet: map[string]phy.Ethernet{name: b}}) {
		ch.Subject = trace.Subject{Kind: "host", Key: name}
		ch.Field = "ethernet." + ch.Field
		*changes = append(*changes, ch)
	}
}

func diffHostAccept(changes *[]trace.Change, name string, a, b hostmodel.Accept) {
	subject := trace.Subject{Kind: "host", Key: name}
	if a.Promiscuous != b.Promiscuous {
		*changes = append(*changes, trace.Change{Layer: fabric.Layer, Subject: subject, Field: "accept.promiscuous", From: boolFact(a.Promiscuous), To: boolFact(b.Promiscuous)})
	}
	if a.AllMulticast != b.AllMulticast {
		*changes = append(*changes, trace.Change{Layer: fabric.Layer, Subject: subject, Field: "accept.all_multicast", From: boolFact(a.AllMulticast), To: boolFact(b.AllMulticast)})
	}
	for _, mac := range a.Multicast {
		if !slices.Contains(b.Multicast, mac) {
			*changes = append(*changes, trace.Change{Layer: fabric.Layer, Subject: subject, Field: "accept.multicast." + mac.String(), From: macFact(mac)})
		}
	}
	for _, mac := range b.Multicast {
		if !slices.Contains(a.Multicast, mac) {
			*changes = append(*changes, trace.Change{Layer: fabric.Layer, Subject: subject, Field: "accept.multicast." + mac.String(), To: macFact(mac)})
		}
	}
}

// diffAttachments reports a reflector's attachment changes under the reflector subject,
// walked in sorted attachment-name order so a report names the same field on every run.
// An added or removed attachment reports its whole snapshot under "attachments.<name>"; a
// changed one reports each differing sub-field under "attachments.<name>.port",
// ".vlan", or ".addresses".
func diffAttachments(changes *[]trace.Change, reflectorName string, a, b map[string]reflectormodel.Attachment) {
	subject := trace.Subject{Kind: "reflector", Key: reflectorName}

	names := make(map[string]struct{}, len(a)+len(b))
	for name := range a {
		names[name] = struct{}{}
	}
	for name := range b {
		names[name] = struct{}{}
	}
	sortedNames := make([]string, 0, len(names))
	for name := range names {
		sortedNames = append(sortedNames, name)
	}
	slices.Sort(sortedNames)

	for _, name := range sortedNames {
		attA, inA := a[name]
		attB, inB := b[name]
		field := "attachments." + name
		switch {
		case inA && !inB:
			*changes = append(*changes, trace.Change{Layer: fabric.Layer, Subject: subject, Field: field, From: attachmentSnapshot(attA), To: nil})
		case !inA && inB:
			*changes = append(*changes, trace.Change{Layer: fabric.Layer, Subject: subject, Field: field, From: nil, To: attachmentSnapshot(attB)})
		case inA && inB:
			if attA.Port != attB.Port {
				*changes = append(*changes, trace.Change{Layer: fabric.Layer, Subject: subject, Field: field + ".port", From: attachmentPortFact(attA.Port), To: attachmentPortFact(attB.Port)})
			}
			if !samePtr(attA.VLAN, attB.VLAN) {
				var fromVal, toVal trace.Fact
				if attA.VLAN != nil {
					fromVal = vlanFact(*attA.VLAN)
				}
				if attB.VLAN != nil {
					toVal = vlanFact(*attB.VLAN)
				}
				*changes = append(*changes, trace.Change{Layer: fabric.Layer, Subject: subject, Field: field + ".vlan", From: fromVal, To: toVal})
			}
			pfxA := sortedPrefixStrings(attA.Addresses)
			pfxB := sortedPrefixStrings(attB.Addresses)
			if !slices.Equal(pfxA, pfxB) {
				*changes = append(*changes, trace.Change{Layer: fabric.Layer, Subject: subject, Field: field + ".addresses", From: PrefixesFact(pfxA), To: PrefixesFact(pfxB)})
			}
		}
	}
}

func diffHostIP(changes *[]trace.Change, name string, a, b *hostmodel.IP) {
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
			Layer:   fabric.Layer,
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
			fromVal = gatewayFact(gwA)
		}
		if gwB.IsValid() {
			toVal = gatewayFact(gwB)
		}
		*changes = append(*changes, trace.Change{
			Layer:   fabric.Layer,
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
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "host", Key: name},
				Field:   field,
				From:    macFact(macA),
				To:      nil,
			})
		case !inA && inB:
			*changes = append(*changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "host", Key: name},
				Field:   field,
				From:    nil,
				To:      macFact(macB),
			})
		case inA && inB:
			if macA != macB {
				*changes = append(*changes, trace.Change{
					Layer:   fabric.Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   field,
					From:    macFact(macA),
					To:      macFact(macB),
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

func sortedCables(cables []fabric.Cable) []fabric.Cable {
	if len(cables) == 0 {
		return nil
	}
	cp := make([]fabric.Cable, len(cables))
	copy(cp, cables)
	slices.SortFunc(cp, func(i, j fabric.Cable) int {
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

type cableEndpoints struct {
	A fabric.Endpoint
	B fabric.Endpoint
}

func cableEndpointsFor(c fabric.Cable) cableEndpoints {
	c = canonicalCableOrientation(c)
	return cableEndpoints{A: c.A, B: c.B}
}

func (e cableEndpoints) Canonical() string {
	return e.A.Canonical() + "-" + e.B.Canonical()
}

// normalizedFault reads an unset kind as cablemodel.FaultNone, so a change reports the
// kind a reader compares against.
func normalizedFault(f cablemodel.Fault) cablemodel.Fault {
	f = f.Clone()
	if f.Kind == "" {
		f.Kind = cablemodel.FaultNone
	}
	switch f.Kind {
	case cablemodel.FaultLoseEveryNth, cablemodel.FaultCorruptEveryNth:
		f.Sequence = nil
	case cablemodel.FaultLoseSequence:
		f.N = 0
		slices.Sort(f.Sequence)
		f.Sequence = slices.Compact(f.Sequence)
	default:
		f.N = 0
		f.Sequence = nil
	}

	return f
}

func sameFault(a, b cablemodel.Fault) bool {
	return a.Kind == b.Kind && a.N == b.N && slices.Equal(a.Sequence, b.Sequence)
}

func samePtr[T comparable](a, b *T) bool {
	if (a == nil) != (b == nil) {
		return false
	}

	return a == nil || *a == *b
}

func hostEndpoint(name string, cables []fabric.Cable) (fabric.Endpoint, bool) {
	for _, c := range cables {
		if c.A.Node == name {
			return c.B, true
		}
		if c.B.Node == name {
			return c.A, true
		}
	}
	return fabric.Endpoint{}, false
}

func diffSwitches(a, b Config) []trace.Change {
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
				Layer: fabric.Layer,
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
				Layer: fabric.Layer,
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
				ch.Subject.Key = trace.CompositeKey(name, ch.Subject.Key)
				changes = append(changes, ch)
			}
		}
	}
	return changes
}

func diffCables(a, b Config) []trace.Change {
	var changes []trace.Change
	cablesA := sortedCables(a.Cables)
	cablesB := sortedCables(b.Cables)

	mapA := make(map[cableEndpoints]fabric.Cable, len(cablesA))
	for _, c := range cablesA {
		mapA[cableEndpointsFor(c)] = c
	}
	mapB := make(map[cableEndpoints]fabric.Cable, len(cablesB))
	for _, c := range cablesB {
		mapB[cableEndpointsFor(c)] = c
	}

	for _, cA := range cablesA {
		endpoints := cableEndpointsFor(cA)
		key := endpoints.Canonical()
		cB, exists := mapB[endpoints]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "",
				From:    cableSnapshot(cA),
				To:      nil,
			})
			continue
		}
		cA = canonicalCableOrientation(cA)
		cB = canonicalCableOrientation(cB)

		if !sameLengthMeters(cA.LengthMeters, cB.LengthMeters) {
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "length",
				From:    lengthFact(cA.LengthMeters),
				To:      lengthFact(cB.LengthMeters),
			})
		}
		if mA, mB := cA.Medium, cB.Medium; mA != mB {
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "medium",
				From:    mA,
				To:      mB,
			})
		}
		if !samePtr(cA.Delay, cB.Delay) {
			var fromDelay, toDelay trace.Fact
			if cA.Delay != nil {
				fromDelay = delayFact(*cA.Delay)
			}
			if cB.Delay != nil {
				toDelay = delayFact(*cB.Delay)
			}
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "delay",
				From:    fromDelay,
				To:      toDelay,
			})
		}
		if cA.TopSpeedBPS != cB.TopSpeedBPS {
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "top_speed",
				From:    topSpeedFact(cA.TopSpeedBPS),
				To:      topSpeedFact(cB.TopSpeedBPS),
			})
		}
		if !sameFault(cA.Fault, cB.Fault) {
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "fault",
				From:    faultSnapshot(cA.Fault),
				To:      faultSnapshot(cB.Fault),
			})
		}
	}

	for _, cB := range cablesB {
		endpoints := cableEndpointsFor(cB)
		key := endpoints.Canonical()
		if _, exists := mapA[endpoints]; !exists {
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "cable", Key: key},
				Field:   "",
				From:    nil,
				To:      cableSnapshot(cB),
			})
		}
	}
	return changes
}

func diffHosts(a, b Config) []trace.Change {
	var changes []trace.Change
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
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "host", Key: name},
				Field:   "",
				From:    hostSnapshot(hA),
				To:      nil,
			})
		case !inA && inB:
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
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
					Layer:   fabric.Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   "port",
					From:    epA,
					To:      epB,
				})
			}
			if hA.Address != hB.Address {
				changes = append(changes, trace.Change{
					Layer:   fabric.Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   "address",
					From:    macFact(hA.Address),
					To:      macFact(hB.Address),
				})
			}
			if !samePtr(hA.VLAN, hB.VLAN) {
				var fromVal, toVal trace.Fact
				if hA.VLAN != nil {
					fromVal = vlanFact(*hA.VLAN)
				}
				if hB.VLAN != nil {
					toVal = vlanFact(*hB.VLAN)
				}
				changes = append(changes, trace.Change{
					Layer:   fabric.Layer,
					Subject: trace.Subject{Kind: "host", Key: name},
					Field:   "vlan",
					From:    fromVal,
					To:      toVal,
				})
			}
			diffHostIP(&changes, name, hA.IP, hB.IP)
			diffHostEthernet(&changes, name, hA.Ethernet, hB.Ethernet)
			diffHostAccept(&changes, name, hA.Accept, hB.Accept)
		}
	}
	return changes
}

func diffReflectors(a, b Config) []trace.Change {
	var changes []trace.Change
	reflectorNames := make(map[string]struct{})
	for name := range a.Reflectors {
		reflectorNames[name] = struct{}{}
	}
	for name := range b.Reflectors {
		reflectorNames[name] = struct{}{}
	}
	sortedReflectors := make([]string, 0, len(reflectorNames))
	for name := range reflectorNames {
		sortedReflectors = append(sortedReflectors, name)
	}
	slices.Sort(sortedReflectors)

	for _, name := range sortedReflectors {
		rA, inA := a.Reflectors[name]
		rB, inB := b.Reflectors[name]
		switch {
		case inA && !inB:
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "reflector", Key: name},
				Field:   "",
				From:    reflectorSnapshot(rA),
				To:      nil,
			})
		case !inA && inB:
			changes = append(changes, trace.Change{
				Layer:   fabric.Layer,
				Subject: trace.Subject{Kind: "reflector", Key: name},
				Field:   "",
				From:    nil,
				To:      reflectorSnapshot(rB),
			})
		case inA && inB:
			if rA.Address != rB.Address {
				changes = append(changes, trace.Change{
					Layer:   fabric.Layer,
					Subject: trace.Subject{Kind: "reflector", Key: name},
					Field:   "address",
					From:    macFact(rA.Address),
					To:      macFact(rB.Address),
				})
			}
			diffPorts(&changes, name, rA.Ports, rB.Ports)
			diffAttachments(&changes, name, rA.Attachments, rB.Attachments)
		}
	}
	return changes
}
