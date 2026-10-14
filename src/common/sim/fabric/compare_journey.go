package fabric

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/flow"
)

// diffFlowStats compares what the scenario added to one flow on each side, the
// statistics after the run less preA and preB, the statistics before it. The
// held gauge and the latency extremes mix the earlier traffic with the
// scenario's. Where either side held any before the run they cannot be
// separated, so they are left out and the pair stays uncertain.
func diffFlowStats(a, b, preA, preB flow.Stats) (analysis.Difference, string, bool) {
	a, b = flowStatsSince(a, preA), flowStatsSince(b, preB)
	heldSeparable := preA.Held == 0 && preB.Held == 0
	extremesSeparable := preA.Latency.Count == 0 && preB.Latency.Count == 0
	fields := []struct {
		name              string
		equal             bool
		current, expected any
	}{
		{"Offered", a.Offered == b.Offered, a.Offered, b.Offered},
		{"Delivered", maps.Equal(a.Delivered, b.Delivered), a.Delivered, b.Delivered},
		{"Drops", maps.Equal(a.Drops, b.Drops), a.Drops, b.Drops},
		{"Lost", a.Lost == b.Lost, a.Lost, b.Lost},
		{"Unresolved", a.Unresolved == b.Unresolved, a.Unresolved, b.Unresolved},
		{"Rejected", a.Rejected == b.Rejected, a.Rejected, b.Rejected},
		{"Held", !heldSeparable || a.Held == b.Held, a.Held, b.Held},
		{"Copies", maps.Equal(a.Copies, b.Copies), a.Copies, b.Copies},
		{"Latency.Min", !extremesSeparable || a.Latency.Min == b.Latency.Min, a.Latency.Min, b.Latency.Min},
		{"Latency.Max", !extremesSeparable || a.Latency.Max == b.Latency.Max, a.Latency.Max, b.Latency.Max},
		{"Latency.Sum", a.Latency.Sum == b.Latency.Sum, a.Latency.Sum, b.Latency.Sum},
		{"Latency.Count", a.Latency.Count == b.Latency.Count, a.Latency.Count, b.Latency.Count},
	}
	for _, field := range fields {
		if !field.equal {
			return analysis.Difference{Observable: "flow", Current: fmt.Sprint(field.current), Expected: fmt.Sprint(field.expected)}, field.name, true
		}
	}
	return analysis.Difference{}, "", false
}

func diffJourney(jA, jB Journey) (analysis.Difference, string, bool) {
	if jA.State != jB.State {
		return analysis.Difference{
			Observable: "journey terminal",
			Current:    string(jA.State),
			Expected:   string(jB.State),
		}, "State", true
	}

	if jA.Origin.Kind != jB.Origin.Kind {
		return analysis.Difference{
			Observable: "origin",
			Current:    string(jA.Origin.Kind),
			Expected:   string(jB.Origin.Kind),
		}, "Origin.Kind", true
	}
	if jA.Origin.Mirror != jB.Origin.Mirror {
		return analysis.Difference{
			Observable: "origin",
			Current:    jA.Origin.Mirror,
			Expected:   jB.Origin.Mirror,
		}, "Origin.Mirror", true
	}

	if jA.Protocol != jB.Protocol {
		return analysis.Difference{
			Observable: "protocol",
			Current:    strconv.FormatBool(jA.Protocol),
			Expected:   strconv.FormatBool(jB.Protocol),
		}, "Protocol", true
	}

	if diff, field, found := diffJourneyEntries(jA.Entries, jB.Entries); found {
		return diff, field, true
	}

	if len(jA.Deliveries) != len(jB.Deliveries) {
		return analysis.Difference{
			Observable: "multiplicity",
			Current:    strconv.Itoa(len(jA.Deliveries)),
			Expected:   strconv.Itoa(len(jB.Deliveries)),
		}, "Deliveries.Length", true
	}

	for k := 0; k < len(jA.Deliveries); k++ {
		dA := jA.Deliveries[k]
		dB := jB.Deliveries[k]

		if dA.Host != dB.Host {
			return analysis.Difference{
				Observable: "path",
				Current:    dA.Host,
				Expected:   dB.Host,
			}, fmt.Sprintf("Deliveries[%d].Host", k), true
		}
		if !dA.At.Equal(dB.At) {
			return analysis.Difference{
				Observable: "timing",
				Current:    dA.At.Format(time.RFC3339Nano),
				Expected:   dB.At.Format(time.RFC3339Nano),
			}, fmt.Sprintf("Deliveries[%d].At", k), true
		}
		if dA.Frame.Dst != dB.Frame.Dst {
			return analysis.Difference{
				Observable: "journey terminal",
				Current:    dA.Frame.Dst.String(),
				Expected:   dB.Frame.Dst.String(),
			}, fmt.Sprintf("Deliveries[%d].Frame.Dst", k), true
		}
		if dA.Frame.Src != dB.Frame.Src {
			return analysis.Difference{
				Observable: "journey terminal",
				Current:    dA.Frame.Src.String(),
				Expected:   dB.Frame.Src.String(),
			}, fmt.Sprintf("Deliveries[%d].Frame.Src", k), true
		}
		if dA.Frame.EtherType != dB.Frame.EtherType {
			return analysis.Difference{
				Observable: "journey terminal",
				Current:    dA.Frame.EtherType.String(),
				Expected:   dB.Frame.EtherType.String(),
			}, fmt.Sprintf("Deliveries[%d].Frame.EtherType", k), true
		}
		if !slices.Equal(dA.Frame.Tags, dB.Frame.Tags) {
			return analysis.Difference{
				Observable: "journey terminal",
				Current:    fmt.Sprint(dA.Frame.Tags),
				Expected:   fmt.Sprint(dB.Frame.Tags),
			}, fmt.Sprintf("Deliveries[%d].Frame.Tags", k), true
		}
		if !bytes.Equal(dA.Frame.Payload, dB.Frame.Payload) {
			return analysis.Difference{
				Observable: "journey terminal",
				Current:    fmt.Sprintf("%x", dA.Frame.Payload),
				Expected:   fmt.Sprintf("%x", dB.Frame.Payload),
			}, fmt.Sprintf("Deliveries[%d].Frame.Payload", k), true
		}
	}

	return analysis.Difference{}, "", false
}

func diffHopResult(rA, rB device.Result) (analysis.Difference, string, bool) {
	if len(rA.Egress) != len(rB.Egress) {
		return analysis.Difference{
			Observable: "journey rewrite",
			Current:    fmt.Sprintf("%d egress frames", len(rA.Egress)),
			Expected:   fmt.Sprintf("%d egress frames", len(rB.Egress)),
		}, "Egress.Length", true
	}
	for i := range rA.Egress {
		egA := rA.Egress[i]
		egB := rB.Egress[i]
		if egA.Frame.Dst != egB.Frame.Dst {
			return analysis.Difference{
				Observable: "journey rewrite",
				Current:    egA.Frame.Dst.String(),
				Expected:   egB.Frame.Dst.String(),
			}, fmt.Sprintf("Egress[%d].Frame.Dst", i), true
		}
		if egA.Frame.Src != egB.Frame.Src {
			return analysis.Difference{
				Observable: "journey rewrite",
				Current:    egA.Frame.Src.String(),
				Expected:   egB.Frame.Src.String(),
			}, fmt.Sprintf("Egress[%d].Frame.Src", i), true
		}
		if egA.Frame.EtherType != egB.Frame.EtherType {
			return analysis.Difference{
				Observable: "journey rewrite",
				Current:    egA.Frame.EtherType.String(),
				Expected:   egB.Frame.EtherType.String(),
			}, fmt.Sprintf("Egress[%d].Frame.EtherType", i), true
		}
		if !slices.Equal(egA.Frame.Tags, egB.Frame.Tags) {
			return analysis.Difference{
				Observable: "journey rewrite",
				Current:    fmt.Sprint(egA.Frame.Tags),
				Expected:   fmt.Sprint(egB.Frame.Tags),
			}, fmt.Sprintf("Egress[%d].Frame.Tags", i), true
		}
		if !bytes.Equal(egA.Frame.Payload, egB.Frame.Payload) {
			return analysis.Difference{
				Observable: "journey rewrite",
				Current:    fmt.Sprintf("%x", egA.Frame.Payload),
				Expected:   fmt.Sprintf("%x", egB.Frame.Payload),
			}, fmt.Sprintf("Egress[%d].Frame.Payload", i), true
		}
	}
	return analysis.Difference{}, "", false
}

func diffJourneyEntries(a, b []Entry) (analysis.Difference, string, bool) {
	entriesA := slices.DeleteFunc(slices.Clone(a), func(e Entry) bool { return e.Kind == EntryQueueThreshold })
	entriesB := slices.DeleteFunc(slices.Clone(b), func(e Entry) bool { return e.Kind == EntryQueueThreshold })
	minEntries := min(len(entriesA), len(entriesB))
	for k := 0; k < minEntries; k++ {
		eA := entriesA[k]
		eB := entriesB[k]

		if eA.Kind != eB.Kind {
			if eA.Kind == EntryDrop || eB.Kind == EntryDrop {
				return analysis.Difference{
					Observable: "journey terminal",
					Current:    string(eA.Kind),
					Expected:   string(eB.Kind),
				}, fmt.Sprintf("Entries[%d].Kind", k), true
			}
			return analysis.Difference{
				Observable: "path",
				Current:    string(eA.Kind),
				Expected:   string(eB.Kind),
			}, fmt.Sprintf("Entries[%d].Kind", k), true
		}

		if eA.Device != eB.Device || eA.Port != eB.Port {
			if eA.Kind == EntryDrop || eB.Kind == EntryDrop {
				return analysis.Difference{
					Observable: "drop location",
					Current:    formatEndpoint(eA.Device, eA.Port),
					Expected:   formatEndpoint(eB.Device, eB.Port),
				}, fmt.Sprintf("Entries[%d].%s", k, differingEndpointField(eA, eB)), true
			}
			return analysis.Difference{
				Observable: "path",
				Current:    formatEndpoint(eA.Device, eA.Port),
				Expected:   formatEndpoint(eB.Device, eB.Port),
			}, fmt.Sprintf("Entries[%d].%s", k, differingEndpointField(eA, eB)), true
		}

		if !sameCable(eA.Cable, eB.Cable) {
			return analysis.Difference{
				Observable: "path",
				Current:    formatCable(eA.Cable),
				Expected:   formatCable(eB.Cable),
			}, fmt.Sprintf("Entries[%d].Cable.%s", k, cableField(eA.Cable, eB.Cable)), true
		}

		if eA.Reason != eB.Reason {
			return analysis.Difference{
				Observable: "drop reason",
				Current:    string(eA.Reason),
				Expected:   string(eB.Reason),
			}, fmt.Sprintf("Entries[%d].Reason", k), true
		}

		if eA.PCP != eB.PCP {
			return analysis.Difference{
				Observable: "journey rewrite",
				Current:    strconv.Itoa(int(eA.PCP)),
				Expected:   strconv.Itoa(int(eB.PCP)),
			}, fmt.Sprintf("Entries[%d].PCP", k), true
		}

		if !eA.At.Equal(eB.At) {
			return analysis.Difference{
				Observable: "timing",
				Current:    eA.At.Format(time.RFC3339Nano),
				Expected:   eB.At.Format(time.RFC3339Nano),
			}, fmt.Sprintf("Entries[%d].At", k), true
		}
		if eA.Latency != eB.Latency {
			return analysis.Difference{
				Observable: "timing",
				Current:    eA.Latency.String(),
				Expected:   eB.Latency.String(),
			}, fmt.Sprintf("Entries[%d].Latency", k), true
		}
		if eA.Serialization != eB.Serialization {
			return analysis.Difference{
				Observable: "timing",
				Current:    eA.Serialization.String(),
				Expected:   eB.Serialization.String(),
			}, fmt.Sprintf("Entries[%d].Serialization", k), true
		}
		if eA.Wait != eB.Wait {
			return analysis.Difference{
				Observable: "timing",
				Current:    eA.Wait.String(),
				Expected:   eB.Wait.String(),
			}, fmt.Sprintf("Entries[%d].Wait", k), true
		}

		if eA.Result != nil && eB.Result != nil {
			if diff, field, hasDiff := diffHopResult(*eA.Result, *eB.Result); hasDiff {
				return diff, fmt.Sprintf("Entries[%d].Result.%s", k, field), true
			}
		}
	}

	if len(entriesA) != len(entriesB) {
		return analysis.Difference{
			Observable: "path",
			Current:    fmt.Sprintf("%d entries", len(entriesA)),
			Expected:   fmt.Sprintf("%d entries", len(entriesB)),
		}, "Entries.Length", true
	}

	return analysis.Difference{}, "", false
}
