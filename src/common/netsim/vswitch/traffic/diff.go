package traffic

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// RateFact wraps a uint64 rate in bits per second as a trace.Fact.
type RateFact uint64

// TypeID returns the fact type identifier for RateFact.
func (f RateFact) TypeID() string { return "traffic.rate_bps" }

// Canonical returns the decimal string of the rate.
func (f RateFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// BurstFact wraps a burst size in octets as a trace.Fact.
type BurstFact int

// TypeID returns the fact type identifier for BurstFact.
func (f BurstFact) TypeID() string { return "traffic.burst_octets" }

// Canonical returns the decimal string of the burst.
func (f BurstFact) Canonical() string { return strconv.Itoa(int(f)) }

// MaxRateFact wraps a maximum rate in bits per second as a trace.Fact.
type MaxRateFact uint64

// TypeID returns the fact type identifier for MaxRateFact.
func (f MaxRateFact) TypeID() string { return "traffic.max_rate_bps" }

// Canonical returns the decimal string of the max rate.
func (f MaxRateFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// SelectAllFact wraps a boolean select_all flag as a trace.Fact.
type SelectAllFact bool

// TypeID returns the fact type identifier for SelectAllFact.
func (f SelectAllFact) TypeID() string { return "traffic.select_all" }

// Canonical returns "true" or "false".
func (f SelectAllFact) Canonical() string { return strconv.FormatBool(bool(f)) }

// PortsFact wraps a slice of port names as a trace.Fact.
type PortsFact []string

// TypeID returns the fact type identifier for PortsFact.
func (f PortsFact) TypeID() string { return "traffic.ports" }

// Canonical returns the comma-separated port names.
func (f PortsFact) Canonical() string { return strings.Join(f, ",") }

// VLANsFact wraps a slice of VLAN IDs as a trace.Fact.
type VLANsFact []vlan.ID

// TypeID returns the fact type identifier for VLANsFact.
func (f VLANsFact) TypeID() string { return "traffic.vlans" }

// Canonical returns the comma-separated VLAN IDs.
func (f VLANsFact) Canonical() string {
	if len(f) == 0 {
		return ""
	}
	strs := make([]string, len(f))
	for i, vid := range f {
		strs[i] = strconv.Itoa(int(vid))
	}
	return strings.Join(strs, ",")
}

// OutputPortFact wraps an output port name as a trace.Fact.
type OutputPortFact string

// TypeID returns the fact type identifier for OutputPortFact.
func (f OutputPortFact) TypeID() string { return "traffic.output_port" }

// Canonical returns the output port name string.
func (f OutputPortFact) Canonical() string { return string(f) }

// OutputVLANFact wraps an output VLAN ID as a trace.Fact.
type OutputVLANFact vlan.ID

// TypeID returns the fact type identifier for OutputVLANFact.
func (f OutputVLANFact) TypeID() string { return "traffic.output_vlan" }

// Canonical returns the decimal string of the VLAN ID.
func (f OutputVLANFact) Canonical() string { return strconv.Itoa(int(f)) }

// SnapLenFact wraps a snap length as a trace.Fact.
type SnapLenFact int

// TypeID returns the fact type identifier for SnapLenFact.
func (f SnapLenFact) TypeID() string { return "traffic.snap_len" }

// Canonical returns the decimal string of the snap length.
func (f SnapLenFact) Canonical() string { return strconv.Itoa(int(f)) }

type mirrorSnapshotFact string

func (f mirrorSnapshotFact) TypeID() string    { return "traffic.mirror" }
func (f mirrorSnapshotFact) Canonical() string { return string(f) }

func snapshotMirror(m Mirror) mirrorSnapshotFact {
	var b strings.Builder
	b.WriteString("name=")
	b.WriteString(strconv.Quote(m.Name))
	b.WriteString(";select_all=")
	b.WriteString(strconv.FormatBool(m.SelectAll))
	b.WriteString(";select_src_ports=[")
	writeQuotedStrings(&b, normalizedStrings(m.SelectSrcPorts))
	b.WriteString("];select_dst_ports=[")
	writeQuotedStrings(&b, normalizedStrings(m.SelectDstPorts))
	b.WriteString("];select_vlans=[")
	for i, vid := range normalizedVLANs(m.SelectVLANs) {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(int(vid)))
	}
	b.WriteString("];output_port=")
	b.WriteString(strconv.Quote(m.OutputPort))
	b.WriteString(";output_vlan=")
	if m.OutputVLAN == nil {
		b.WriteString("none")
	} else {
		b.WriteString(strconv.Itoa(int(*m.OutputVLAN)))
	}
	b.WriteString(";snap_len=")
	b.WriteString(strconv.Itoa(m.SnapLen))

	return mirrorSnapshotFact(b.String())
}

func writeQuotedStrings(b *strings.Builder, values []string) {
	for i, value := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(value))
	}
}

// Diff computes field-level changes between two traffic configurations.
// Mirror selector slices are sets, so their order does not produce a change.
func Diff(a, b Config) []trace.Change {
	var changes []trace.Change

	aMirrors := mirrorsByName(a.Mirrors)
	bMirrors := mirrorsByName(b.Mirrors)
	for _, name := range unionKeys(aMirrors, bMirrors) {
		am, inA := aMirrors[name]
		bm, inB := bMirrors[name]
		if !inA {
			changes = append(changes, trace.Change{
				Layer: Layer, Subject: trace.Subject{Kind: "mirror", Key: name}, Field: "", From: nil, To: snapshotMirror(bm),
			})

			continue
		}
		if !inB {
			changes = append(changes, trace.Change{
				Layer: Layer, Subject: trace.Subject{Kind: "mirror", Key: name}, Field: "", From: snapshotMirror(am), To: nil,
			})

			continue
		}

		changes = diffMirror(changes, name, am, bm)
	}

	for _, name := range unionKeys(a.Policers, b.Policers) {
		ap := a.Policers[name]
		bp := b.Policers[name]
		subject := trace.Subject{Kind: "port", Key: name}
		if ap.RateBPS != bp.RateBPS {
			changes = append(changes, trace.Change{
				Layer: Layer, Subject: subject, Field: "rate", From: RateFact(ap.RateBPS), To: RateFact(bp.RateBPS),
			})
		}
		if ap.BurstOctets != bp.BurstOctets {
			changes = append(changes, trace.Change{
				Layer: Layer, Subject: subject, Field: "burst", From: BurstFact(ap.BurstOctets), To: BurstFact(bp.BurstOctets),
			})
		}
	}

	for _, portName := range unionKeys(a.Queues, b.Queues) {
		aRates := a.Queues[portName].MaxRateBPS
		bRates := b.Queues[portName].MaxRateBPS
		for _, pcp := range unionPCPs(aRates, bRates) {
			aRate := aRates[pcp]
			bRate := bRates[pcp]
			if aRate == bRate {
				continue
			}
			changes = append(changes, trace.Change{
				Layer: Layer,
				Subject: trace.Subject{
					Kind: "port",
					Key:  fmt.Sprintf("%s/%d", portName, pcp),
				},
				Field: "max_rate",
				From:  MaxRateFact(aRate),
				To:    MaxRateFact(bRate),
			})
		}
	}

	return changes
}

func diffMirror(changes []trace.Change, name string, a, b Mirror) []trace.Change {
	subject := trace.Subject{Kind: "mirror", Key: name}
	appendChange := func(field string, from, to trace.Fact) {
		changes = append(changes, trace.Change{
			Layer: Layer, Subject: subject, Field: field, From: from, To: to,
		})
	}

	if a.SelectAll != b.SelectAll {
		appendChange("select_all", SelectAllFact(a.SelectAll), SelectAllFact(b.SelectAll))
	}
	if from, to := normalizedStrings(a.SelectSrcPorts), normalizedStrings(b.SelectSrcPorts); !slices.Equal(from, to) {
		appendChange("select_src_ports", PortsFact(from), PortsFact(to))
	}
	if from, to := normalizedStrings(a.SelectDstPorts), normalizedStrings(b.SelectDstPorts); !slices.Equal(from, to) {
		appendChange("select_dst_ports", PortsFact(from), PortsFact(to))
	}
	if from, to := normalizedVLANs(a.SelectVLANs), normalizedVLANs(b.SelectVLANs); !slices.Equal(from, to) {
		appendChange("select_vlans", VLANsFact(from), VLANsFact(to))
	}
	if a.OutputPort != b.OutputPort {
		appendChange("output_port", OutputPortFact(a.OutputPort), OutputPortFact(b.OutputPort))
	}
	if !equalVLANs(a.OutputVLAN, b.OutputVLAN) {
		appendChange("output_vlan", vlanFact(a.OutputVLAN), vlanFact(b.OutputVLAN))
	}
	if a.SnapLen != b.SnapLen {
		appendChange("snap_len", SnapLenFact(a.SnapLen), SnapLenFact(b.SnapLen))
	}

	return changes
}

func mirrorsByName(mirrors []Mirror) map[string]Mirror {
	byName := make(map[string]Mirror, len(mirrors))
	for _, mirror := range mirrors {
		byName[mirror.Name] = mirror
	}

	return byName
}

func unionKeys[V any](a, b map[string]V) []string {
	keys := make(map[string]struct{}, len(a)+len(b))
	for key := range a {
		keys[key] = struct{}{}
	}
	for key := range b {
		keys[key] = struct{}{}
	}

	return sortedKeys(keys)
}

func unionPCPs(a, b map[vlan.PCP]uint64) []vlan.PCP {
	keys := make(map[vlan.PCP]struct{}, len(a)+len(b))
	for key := range a {
		keys[key] = struct{}{}
	}
	for key := range b {
		keys[key] = struct{}{}
	}
	pcps := make([]vlan.PCP, 0, len(keys))
	for key := range keys {
		pcps = append(pcps, key)
	}
	slices.Sort(pcps)

	return pcps
}

func normalizedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}

	return sortedKeys(set)
}

func normalizedVLANs(values []vlan.ID) []vlan.ID {
	set := make(map[vlan.ID]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	result := make([]vlan.ID, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	slices.Sort(result)

	return result
}

func equalVLANs(a, b *vlan.ID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return *a == *b
}

func vlanFact(id *vlan.ID) trace.Fact {
	if id == nil {
		return nil
	}

	return OutputVLANFact(*id)
}
