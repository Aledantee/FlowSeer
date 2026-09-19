package search

import (
	"bytes"
	"cmp"
	"fmt"
	"hash/fnv"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

// FrameShape specifies the protocol EtherType and payload pattern for a candidate frame.
type FrameShape struct {
	EtherType ethernet.EtherType
	Payload   []byte
}

// L2TrafficDomainConfig configures a finite layer-2 traffic exploration domain.
type L2TrafficDomainConfig struct {
	Sources      []fabric.Endpoint
	Destinations []netaddr.MAC
	VLANs        []vlan.ID
	Shapes       []FrameShape
	SourceMACs   map[fabric.Endpoint]netaddr.MAC
	At           time.Time
}

// L2TrafficDomain enumerates the cross product of declared sources, destinations,
// VLANs, and frame shapes in a deterministic, total order.
type L2TrafficDomain struct {
	sources      []fabric.Endpoint
	destinations []netaddr.MAC
	vlans        []vlan.ID
	shapes       []FrameShape
	sourceMACs   map[fabric.Endpoint]netaddr.MAC
	at           time.Time
	size         int
}

// NewL2TrafficDomain validates, deduplicates, and deterministically sorts declared
// inputs into an enumerable L2 search domain.
func NewL2TrafficDomain(cfg L2TrafficDomainConfig) *L2TrafficDomain {
	t0 := cfg.At
	if t0.IsZero() {
		t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	srcs := deduplicateAndSortEndpoints(cfg.Sources)
	dsts := deduplicateAndSortMACs(cfg.Destinations)
	vlans := deduplicateAndSortVLANs(cfg.VLANs)
	shapes := deduplicateAndSortShapes(cfg.Shapes)

	size := 0
	if len(srcs) > 0 && len(dsts) > 0 && len(vlans) > 0 && len(shapes) > 0 {
		size = len(srcs) * len(dsts) * len(vlans) * len(shapes)
	}

	sMACs := make(map[fabric.Endpoint]netaddr.MAC, len(cfg.SourceMACs))
	for k, v := range cfg.SourceMACs {
		sMACs[k] = v
	}

	return &L2TrafficDomain{
		sources:      srcs,
		destinations: dsts,
		vlans:        vlans,
		shapes:       shapes,
		sourceMACs:   sMACs,
		at:           t0,
		size:         size,
	}
}

// Size returns the total count of distinct candidates in the domain.
func (d *L2TrafficDomain) Size() int {
	return d.size
}

// Enumerate executes yield for every candidate in total deterministic order.
func (d *L2TrafficDomain) Enumerate(yield func(Candidate) bool) {
	if d.size == 0 {
		return
	}

	for _, src := range d.sources {
		srcMAC, ok := d.sourceMACs[src]
		if !ok {
			srcMAC = defaultSourceMAC(src)
		}

		for _, dst := range d.destinations {
			for _, vid := range d.vlans {
				for _, shape := range d.shapes {
					cand := d.buildCandidate(src, srcMAC, dst, vid, shape)
					if !yield(cand) {
						return
					}
				}
			}
		}
	}
}

func (d *L2TrafficDomain) buildCandidate(
	src fabric.Endpoint,
	srcMAC, dst netaddr.MAC,
	vid vlan.ID,
	shape FrameShape,
) Candidate {
	var tags []vlan.Tag
	if vid > 0 {
		tags = []vlan.Tag{{VID: vid}}
	}

	etype := shape.EtherType
	if etype == 0 {
		etype = ethernet.EtherTypeIPv4
	}

	frame := ethernet.Frame{
		Dst:       dst,
		Src:       srcMAC,
		Tags:      tags,
		EtherType: etype,
		Payload:   slices.Clone(shape.Payload),
	}

	inj := fabric.Injection{
		At:     d.at,
		Origin: src,
		Frame:  frame,
	}

	tuple := formatL2Tuple(src, dst, vid, etype, shape.Payload)

	return Candidate{
		Tuple:    tuple,
		Scenario: []fabric.Injection{inj},
	}
}

func formatL2Tuple(
	src fabric.Endpoint,
	dst netaddr.MAC,
	vid vlan.ID,
	etype ethernet.EtherType,
	payload []byte,
) Tuple {
	srcStr := src.Node
	if src.Port != "" {
		srcStr += "/" + src.Port
	}
	var tagStr string
	if vid > 0 {
		tagStr = fmt.Sprintf(" vlan:%d", vid)
	}
	var payloadStr string
	if len(payload) > 0 {
		payloadStr = fmt.Sprintf(" payload:%x", payload)
	}
	return Tuple(fmt.Sprintf("%s -> %s etype:0x%04x%s%s", srcStr, dst, uint16(etype), tagStr, payloadStr))
}

func defaultSourceMAC(ep fabric.Endpoint) netaddr.MAC {
	h := fnv.New64a()
	_, _ = h.Write([]byte(ep.Node))
	_, _ = h.Write([]byte("/"))
	_, _ = h.Write([]byte(ep.Port))
	sum := h.Sum64()

	var mac netaddr.MAC
	mac[0] = 0x02 // locally administered unicast
	mac[1] = byte(sum >> 32)
	mac[2] = byte(sum >> 24)
	mac[3] = byte(sum >> 16)
	mac[4] = byte(sum >> 8)
	mac[5] = byte(sum)
	return mac
}

func deduplicateAndSortEndpoints(in []fabric.Endpoint) []fabric.Endpoint {
	seen := make(map[fabric.Endpoint]bool, len(in))
	var out []fabric.Endpoint
	for _, ep := range in {
		if !seen[ep] {
			seen[ep] = true
			out = append(out, ep)
		}
	}
	slices.SortFunc(out, func(a, b fabric.Endpoint) int {
		if r := cmp.Compare(a.Node, b.Node); r != 0 {
			return r
		}
		return cmp.Compare(a.Port, b.Port)
	})
	return out
}

func deduplicateAndSortMACs(in []netaddr.MAC) []netaddr.MAC {
	seen := make(map[netaddr.MAC]bool, len(in))
	var out []netaddr.MAC
	for _, m := range in {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b netaddr.MAC) int {
		return bytes.Compare(a[:], b[:])
	})
	return out
}

func deduplicateAndSortVLANs(in []vlan.ID) []vlan.ID {
	seen := make(map[vlan.ID]bool, len(in))
	var out []vlan.ID
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b vlan.ID) int {
		return cmp.Compare(uint16(a), uint16(b))
	})
	return out
}

func deduplicateAndSortShapes(in []FrameShape) []FrameShape {
	type key struct {
		etype   ethernet.EtherType
		payload string
	}
	seen := make(map[key]bool, len(in))
	var out []FrameShape
	for _, s := range in {
		etype := s.EtherType
		if etype == 0 {
			etype = ethernet.EtherTypeIPv4
		}
		k := key{etype: etype, payload: string(s.Payload)}
		if !seen[k] {
			seen[k] = true
			out = append(out, FrameShape{
				EtherType: etype,
				Payload:   slices.Clone(s.Payload),
			})
		}
	}
	slices.SortFunc(out, func(a, b FrameShape) int {
		if r := cmp.Compare(uint16(a.EtherType), uint16(b.EtherType)); r != 0 {
			return r
		}
		return bytes.Compare(a.Payload, b.Payload)
	})
	return out
}
