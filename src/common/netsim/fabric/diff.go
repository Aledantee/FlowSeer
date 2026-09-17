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
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
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

// BoolFact wraps a boolean as a trace.Fact.
type BoolFact bool

// TypeID returns the fact type identifier for BoolFact.
func (f BoolFact) TypeID() string { return "fabric.bool" }

// Canonical returns "true" or "false".
func (f BoolFact) Canonical() string { return strconv.FormatBool(bool(f)) }

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
			Layer:   Layer,
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
// changed), Uncabled entries added or removed, and a change of the physical assumption as one
// fabric field. Evidence references produce no change.
func Diff(a, b Config) []trace.Change {
	a = a.Normalize()
	b = b.Normalize()

	var changes []trace.Change
	if !a.Start.Equal(b.Start) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: trace.Subject{Kind: "fabric"},
			Field:   "start",
			From:    newStartFact(a.Start),
			To:      newStartFact(b.Start),
		})
	}
	if !a.PhyAssumption.Equal(b.PhyAssumption) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: trace.Subject{Kind: "fabric"},
			Field:   "phy_assumption",
			From:    phyAssumptionSnapshot(a.PhyAssumption),
			To:      phyAssumptionSnapshot(b.PhyAssumption),
		})
	}
	changes = append(changes, diffUncabled(a.Uncabled, b.Uncabled)...)

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
				ch.Subject.Key = nestedSubjectKey(name, ch.Subject.Key)
				changes = append(changes, ch)
			}
		}
	}

	cablesA := sortedCables(a.Cables)
	cablesB := sortedCables(b.Cables)

	mapA := make(map[cableEndpoints]Cable, len(cablesA))
	for _, c := range cablesA {
		mapA[cableEndpointsFor(c)] = c
	}
	mapB := make(map[cableEndpoints]Cable, len(cablesB))
	for _, c := range cablesB {
		mapB[cableEndpointsFor(c)] = c
	}

	for _, cA := range cablesA {
		endpoints := cableEndpointsFor(cA)
		key := endpoints.Canonical()
		cB, exists := mapB[endpoints]
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

		if !sameLengthMeters(cA.LengthMeters, cB.LengthMeters) {
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
		endpoints := cableEndpointsFor(cB)
		key := endpoints.Canonical()
		if _, exists := mapA[endpoints]; !exists {
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
			diffHostEthernet(&changes, name, hA.Ethernet, hB.Ethernet)
			diffHostAccept(&changes, name, hA.Accept, hB.Accept)
		}
	}

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
				Layer:   Layer,
				Subject: trace.Subject{Kind: "reflector", Key: name},
				Field:   "",
				From:    reflectorSnapshot(rA),
				To:      nil,
			})
		case !inA && inB:
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "reflector", Key: name},
				Field:   "",
				From:    nil,
				To:      reflectorSnapshot(rB),
			})
		case inA && inB:
			if rA.Address != rB.Address {
				changes = append(changes, trace.Change{
					Layer:   Layer,
					Subject: trace.Subject{Kind: "reflector", Key: name},
					Field:   "address",
					From:    MACFact(rA.Address),
					To:      MACFact(rB.Address),
				})
			}
			diffPorts(&changes, name, rA.Ports, rB.Ports)
			diffAttachments(&changes, name, rA.Attachments, rB.Attachments)
		}
	}

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

func diffUncabled(a, b []Uncabled) []trace.Change {
	inA := make(map[Endpoint]bool, len(a))
	for _, entry := range a {
		inA[entry.Endpoint] = true
	}
	inB := make(map[Endpoint]bool, len(b))
	for _, entry := range b {
		inB[entry.Endpoint] = true
	}

	var changes []trace.Change
	for _, entry := range a {
		if !inB[entry.Endpoint] {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "uncabled", Key: entry.Endpoint.Canonical()},
				From:    entry.Endpoint,
			})
		}
	}
	for _, entry := range b {
		if !inA[entry.Endpoint] {
			changes = append(changes, trace.Change{
				Layer:   Layer,
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

func diffHostAccept(changes *[]trace.Change, name string, a, b HostAccept) {
	subject := trace.Subject{Kind: "host", Key: name}
	if a.Promiscuous != b.Promiscuous {
		*changes = append(*changes, trace.Change{Layer: Layer, Subject: subject, Field: "accept.promiscuous", From: BoolFact(a.Promiscuous), To: BoolFact(b.Promiscuous)})
	}
	if a.AllMulticast != b.AllMulticast {
		*changes = append(*changes, trace.Change{Layer: Layer, Subject: subject, Field: "accept.all_multicast", From: BoolFact(a.AllMulticast), To: BoolFact(b.AllMulticast)})
	}
	for _, mac := range a.Multicast {
		if !slices.Contains(b.Multicast, mac) {
			*changes = append(*changes, trace.Change{Layer: Layer, Subject: subject, Field: "accept.multicast." + mac.String(), From: MACFact(mac)})
		}
	}
	for _, mac := range b.Multicast {
		if !slices.Contains(a.Multicast, mac) {
			*changes = append(*changes, trace.Change{Layer: Layer, Subject: subject, Field: "accept.multicast." + mac.String(), To: MACFact(mac)})
		}
	}
}

// diffAttachments reports a reflector's attachment changes under the reflector subject,
// walked in sorted attachment-name order so a report names the same field on every run.
// An added or removed attachment reports its whole snapshot under "attachments.<name>"; a
// changed one reports each differing sub-field under "attachments.<name>.port",
// ".vlan", or ".addresses".
func diffAttachments(changes *[]trace.Change, reflectorName string, a, b map[string]Attachment) {
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
			*changes = append(*changes, trace.Change{Layer: Layer, Subject: subject, Field: field, From: attachmentSnapshot(attA), To: nil})
		case !inA && inB:
			*changes = append(*changes, trace.Change{Layer: Layer, Subject: subject, Field: field, From: nil, To: attachmentSnapshot(attB)})
		case inA && inB:
			if attA.Port != attB.Port {
				*changes = append(*changes, trace.Change{Layer: Layer, Subject: subject, Field: field + ".port", From: attachmentPortFact(attA.Port), To: attachmentPortFact(attB.Port)})
			}
			if !samePtr(attA.VLAN, attB.VLAN) {
				var fromVal, toVal trace.Fact
				if attA.VLAN != nil {
					fromVal = VLANFact(*attA.VLAN)
				}
				if attB.VLAN != nil {
					toVal = VLANFact(*attB.VLAN)
				}
				*changes = append(*changes, trace.Change{Layer: Layer, Subject: subject, Field: field + ".vlan", From: fromVal, To: toVal})
			}
			pfxA := sortedPrefixStrings(attA.Addresses)
			pfxB := sortedPrefixStrings(attB.Addresses)
			if !slices.Equal(pfxA, pfxB) {
				*changes = append(*changes, trace.Change{Layer: Layer, Subject: subject, Field: field + ".addresses", From: PrefixesFact(pfxA), To: PrefixesFact(pfxB)})
			}
		}
	}
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
		writeStringField(&out, "static", strconv.FormatBool(seed.Static))
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
		fact.WriteString("evidence=[")
		for j, ref := range issue.Evidence {
			if j > 0 {
				fact.WriteByte(',')
			}
			fact.WriteString(strconv.Quote(string(ref)))
		}
		fact.WriteString("]}")
		issueFacts[i] = fact.String()
	}
	slices.Sort(issueFacts)
	out.WriteString(strings.Join(issueFacts, ","))
	out.WriteString("];")
	out.WriteString("metadata.evidence=[")
	for i, entry := range metadata.Evidence().Entries() {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		writeStringField(out, "ref", string(entry.Ref))
		writeStringField(out, "kind", entry.Evidence.Kind.String())
		writeStringField(out, "origin", entry.Evidence.Origin)
		writeStringField(out, "context", entry.Evidence.Context)
		out.WriteByte('}')
	}
	out.WriteString("];")
	out.WriteString("metadata.assumptions=[")
	for i, assumption := range metadata.Assumptions() {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		writeStringField(out, "scope", assumption.Scope.String())
		writeStringField(out, "statement", assumption.Statement)
		out.WriteString("evidence=[")
		for j, ref := range assumption.Evidence {
			if j > 0 {
				out.WriteByte(',')
			}
			out.WriteString(strconv.Quote(string(ref)))
		}
		out.WriteString("]}")
	}
	out.WriteString("];")
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

func nestedSubjectKey(outer, inner string) string {
	escape := strings.NewReplacer("%", "%25", "/", "%2F").Replace
	return escape(outer) + "/" + escape(inner)
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

type cableEndpoints struct {
	A Endpoint
	B Endpoint
}

func cableEndpointsFor(c Cable) cableEndpoints {
	c = canonicalCableOrientation(c)
	return cableEndpoints{A: c.A, B: c.B}
}

func (e cableEndpoints) Canonical() string {
	return e.A.Canonical() + "-" + e.B.Canonical()
}

// normalizedFault reads an unset kind as FaultNone, so a change reports the
// kind a reader compares against.
func normalizedFault(f Fault) Fault {
	f = f.Clone()
	if f.Kind == "" {
		f.Kind = FaultNone
	}
	switch f.Kind {
	case FaultLoseEveryNth, FaultCorruptEveryNth:
		f.Sequence = nil
	case FaultLoseSequence:
		f.N = 0
		slices.Sort(f.Sequence)
		f.Sequence = slices.Compact(f.Sequence)
	default:
		f.N = 0
		f.Sequence = nil
	}

	return f
}

func sameFault(a, b Fault) bool {
	return a.Kind == b.Kind && a.N == b.N && slices.Equal(a.Sequence, b.Sequence)
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

func phyAssumptionSnapshot(a *PhyAssumption) trace.Fact {
	if a == nil {
		return nil
	}
	var out strings.Builder
	writeStringField(&out, "medium", string(a.Medium))
	writeStringField(&out, "ethernet", a.Ethernet.Canonical())

	return phyAssumptionSnapshotFact(out.String())
}

func hostSnapshot(h Host) hostSnapshotFact {
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

func reflectorSnapshot(r Reflector) reflectorSnapshotFact {
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

func attachmentSnapshot(a Attachment) attachmentSnapshotFact {
	return attachmentSnapshotFact(attachmentFields(a))
}

// attachmentFields writes an attachment's fields as key=value pairs, reused by both the
// whole-reflector snapshot and the standalone added/removed attachment snapshot.
func attachmentFields(a Attachment) string {
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
