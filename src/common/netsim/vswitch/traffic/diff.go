package traffic

import (
	"fmt"
	"slices"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

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
				Layer: Layer, Subject: trace.Subject{Kind: "mirror", Key: name}, Field: "", From: nil, To: bm,
			})

			continue
		}
		if !inB {
			changes = append(changes, trace.Change{
				Layer: Layer, Subject: trace.Subject{Kind: "mirror", Key: name}, Field: "", From: am, To: nil,
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
				Layer: Layer, Subject: subject, Field: "rate", From: ap.RateBPS, To: bp.RateBPS,
			})
		}
		if ap.BurstOctets != bp.BurstOctets {
			changes = append(changes, trace.Change{
				Layer: Layer, Subject: subject, Field: "burst", From: ap.BurstOctets, To: bp.BurstOctets,
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
				From:  aRate,
				To:    bRate,
			})
		}
	}

	return changes
}

func diffMirror(changes []trace.Change, name string, a, b Mirror) []trace.Change {
	subject := trace.Subject{Kind: "mirror", Key: name}
	appendChange := func(field string, from, to any) {
		changes = append(changes, trace.Change{
			Layer: Layer, Subject: subject, Field: field, From: from, To: to,
		})
	}

	if a.SelectAll != b.SelectAll {
		appendChange("select_all", a.SelectAll, b.SelectAll)
	}
	if from, to := normalizedStrings(a.SelectSrcPorts), normalizedStrings(b.SelectSrcPorts); !slices.Equal(from, to) {
		appendChange("select_src_ports", from, to)
	}
	if from, to := normalizedStrings(a.SelectDstPorts), normalizedStrings(b.SelectDstPorts); !slices.Equal(from, to) {
		appendChange("select_dst_ports", from, to)
	}
	if from, to := normalizedVLANs(a.SelectVLANs), normalizedVLANs(b.SelectVLANs); !slices.Equal(from, to) {
		appendChange("select_vlans", from, to)
	}
	if a.OutputPort != b.OutputPort {
		appendChange("output_port", a.OutputPort, b.OutputPort)
	}
	if !equalVLANs(a.OutputVLAN, b.OutputVLAN) {
		appendChange("output_vlan", vlanValue(a.OutputVLAN), vlanValue(b.OutputVLAN))
	}
	if a.SnapLen != b.SnapLen {
		appendChange("snap_len", a.SnapLen, b.SnapLen)
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

func vlanValue(id *vlan.ID) any {
	if id == nil {
		return nil
	}

	return *id
}
