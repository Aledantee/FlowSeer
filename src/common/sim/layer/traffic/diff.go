package traffic

import (
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// rateFact wraps a uint64 rate in bits per second as a trace.Fact.
type rateFact uint64

// TypeID returns the fact type identifier for rateFact.
func (f rateFact) TypeID() string { return "traffic.rate_bps" }

// Canonical returns the decimal string of the rate.
func (f rateFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// burstFact wraps a burst size in octets as a trace.Fact.
type burstFact int

// TypeID returns the fact type identifier for burstFact.
func (f burstFact) TypeID() string { return "traffic.burst_octets" }

// Canonical returns the decimal string of the burst.
func (f burstFact) Canonical() string { return strconv.Itoa(int(f)) }

// maxRateFact wraps a maximum rate in bits per second as a trace.Fact.
type maxRateFact uint64

// TypeID returns the fact type identifier for maxRateFact.
func (f maxRateFact) TypeID() string { return "traffic.max_rate_bps" }

// Canonical returns the decimal string of the max rate.
func (f maxRateFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// queueBufferFact wraps a queue buffer in encoded frame octets as a trace.Fact.
type queueBufferFact uint64

// TypeID returns the fact type identifier for queueBufferFact.
func (f queueBufferFact) TypeID() string { return "traffic.queue_buffer_octets" }

// Canonical returns the decimal string of the buffer.
func (f queueBufferFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// selectAllFact wraps a boolean select_all flag as a trace.Fact.
type selectAllFact bool

// TypeID returns the fact type identifier for selectAllFact.
func (f selectAllFact) TypeID() string { return "traffic.select_all" }

// Canonical returns "true" or "false".
func (f selectAllFact) Canonical() string { return strconv.FormatBool(bool(f)) }

type portsFact string

func (f portsFact) TypeID() string    { return "traffic.ports" }
func (f portsFact) Canonical() string { return string(f) }

// PortsFact returns an immutable, injective snapshot of port names in their supplied order.
func PortsFact(ports []string) trace.Fact {
	var out strings.Builder
	for i, portName := range ports {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(portName))
	}

	return portsFact(out.String())
}

type vlansFact string

func (f vlansFact) TypeID() string    { return "traffic.vlans" }
func (f vlansFact) Canonical() string { return string(f) }

// VLANsFact returns an immutable snapshot of VLAN IDs in their supplied order.
func VLANsFact(ids []vlan.ID) trace.Fact {
	values := make([]string, len(ids))
	for i, id := range ids {
		values[i] = strconv.Itoa(int(id))
	}

	return vlansFact(strings.Join(values, ","))
}

// outputPortFact wraps an output port name as a trace.Fact.
type outputPortFact string

// TypeID returns the fact type identifier for outputPortFact.
func (f outputPortFact) TypeID() string { return "traffic.output_port" }

// Canonical returns the output port name string.
func (f outputPortFact) Canonical() string { return string(f) }

// outputVLANFact wraps an output VLAN ID as a trace.Fact.
type outputVLANFact vlan.ID

// TypeID returns the fact type identifier for outputVLANFact.
func (f outputVLANFact) TypeID() string { return "traffic.output_vlan" }

// Canonical returns the decimal string of the VLAN ID.
func (f outputVLANFact) Canonical() string { return strconv.Itoa(int(f)) }

// snapLenFact wraps a snap length as a trace.Fact.
type snapLenFact int

// TypeID returns the fact type identifier for snapLenFact.
func (f snapLenFact) TypeID() string { return "traffic.snap_len" }

// Canonical returns the decimal string of the snap length.
func (f snapLenFact) Canonical() string { return strconv.Itoa(int(f)) }

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
	a = a.Normalize(layer.Env{})
	b = b.Normalize(layer.Env{})

	var changes []trace.Change

	aMirrors := mirrorsByName(a.Mirrors)
	bMirrors := mirrorsByName(b.Mirrors)
	for _, name := range unionKeys(aMirrors, bMirrors) {
		am, inA := aMirrors[name]
		bm, inB := bMirrors[name]
		if !inA {
			changes = append(changes, trace.Change{
				Layer: LayerName, Subject: trace.Subject{Kind: "mirror", Key: name}, Field: "", From: nil, To: snapshotMirror(bm),
			})

			continue
		}
		if !inB {
			changes = append(changes, trace.Change{
				Layer: LayerName, Subject: trace.Subject{Kind: "mirror", Key: name}, Field: "", From: snapshotMirror(am), To: nil,
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
				Layer: LayerName, Subject: subject, Field: "rate", From: rateFact(ap.RateBPS), To: rateFact(bp.RateBPS),
			})
		}
		if ap.BurstOctets != bp.BurstOctets {
			changes = append(changes, trace.Change{
				Layer: LayerName, Subject: subject, Field: "burst", From: burstFact(ap.BurstOctets), To: burstFact(bp.BurstOctets),
			})
		}
	}

	for _, portName := range unionKeys(a.Queues, b.Queues) {
		aQueues := a.Queues[portName]
		bQueues := b.Queues[portName]
		for _, pcp := range unionPCPs(aQueues.MaxRateBPS, bQueues.MaxRateBPS, aQueues.BufferOctets, bQueues.BufferOctets) {
			subject := trace.Subject{
				Kind: "port",
				Key:  trace.CompositeKey(portName, strconv.Itoa(int(pcp))),
			}
			if aRate, bRate := aQueues.MaxRateBPS[pcp], bQueues.MaxRateBPS[pcp]; aRate != bRate {
				changes = append(changes, trace.Change{
					Layer: LayerName, Subject: subject, Field: "max_rate",
					From: maxRateFact(aRate), To: maxRateFact(bRate),
				})
			}
			if aBuffer, bBuffer := aQueues.BufferOctets[pcp], bQueues.BufferOctets[pcp]; aBuffer != bBuffer {
				changes = append(changes, trace.Change{
					Layer: LayerName, Subject: subject, Field: "buffer_octets",
					From: queueBufferFact(aBuffer), To: queueBufferFact(bBuffer),
				})
			}
		}
	}

	return changes
}

func diffMirror(changes []trace.Change, name string, a, b Mirror) []trace.Change {
	subject := trace.Subject{Kind: "mirror", Key: name}
	appendChange := func(field string, from, to trace.Fact) {
		changes = append(changes, trace.Change{
			Layer: LayerName, Subject: subject, Field: field, From: from, To: to,
		})
	}

	if a.SelectAll != b.SelectAll {
		appendChange("select_all", selectAllFact(a.SelectAll), selectAllFact(b.SelectAll))
	}
	if !slices.Equal(a.SelectSrcPorts, b.SelectSrcPorts) {
		appendChange("select_src_ports", PortsFact(a.SelectSrcPorts), PortsFact(b.SelectSrcPorts))
	}
	if !slices.Equal(a.SelectDstPorts, b.SelectDstPorts) {
		appendChange("select_dst_ports", PortsFact(a.SelectDstPorts), PortsFact(b.SelectDstPorts))
	}
	if !slices.Equal(a.SelectVLANs, b.SelectVLANs) {
		appendChange("select_vlans", VLANsFact(a.SelectVLANs), VLANsFact(b.SelectVLANs))
	}
	if a.OutputPort != b.OutputPort {
		appendChange("output_port", outputPortFact(a.OutputPort), outputPortFact(b.OutputPort))
	}
	if !equalVLANs(a.OutputVLAN, b.OutputVLAN) {
		appendChange("output_vlan", vlanFact(a.OutputVLAN), vlanFact(b.OutputVLAN))
	}
	if a.SnapLen != b.SnapLen {
		appendChange("snap_len", snapLenFact(a.SnapLen), snapLenFact(b.SnapLen))
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

func unionPCPs(queues ...map[vlan.PCP]uint64) []vlan.PCP {
	keys := make(map[vlan.PCP]struct{})
	for _, q := range queues {
		for key := range q {
			keys[key] = struct{}{}
		}
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

	return outputVLANFact(*id)
}
