package search

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

func sampleL2Config() L2TrafficDomainConfig {
	return L2TrafficDomainConfig{
		Sources: []fabric.Endpoint{
			{Node: "h1", Port: "eth0"},
			{Node: "h2", Port: "eth0"},
		},
		Destinations: []netaddr.MAC{
			{0x02, 0, 0, 0, 0, 1},
			{0x02, 0, 0, 0, 0, 2},
		},
		VLANs: []vlan.ID{10, 20, 30},
		Shapes: []FrameShape{
			{EtherType: ethernet.EtherTypeIPv4, Payload: []byte{1, 2}},
			{EtherType: ethernet.EtherTypeIPv6, Payload: []byte{3, 4}},
		},
		At: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
}

func TestL2TrafficDomainStableTotalOrderOverRandomizedInput(t *testing.T) {
	t.Parallel()

	baseCfg := sampleL2Config()
	domBase := NewL2TrafficDomain(baseCfg)

	var canonical []Tuple
	domBase.Enumerate(func(c Candidate) bool {
		canonical = append(canonical, c.Tuple)
		return true
	})

	if len(canonical) != domBase.Size() {
		t.Fatalf("enumerated count %d != Size() %d", len(canonical), domBase.Size())
	}

	rng := rand.New(rand.NewPCG(42, 99))
	for trial := 0; trial < 25; trial++ {
		shuffledSrcs := slices.Clone(baseCfg.Sources)
		rng.Shuffle(len(shuffledSrcs), func(i, j int) {
			shuffledSrcs[i], shuffledSrcs[j] = shuffledSrcs[j], shuffledSrcs[i]
		})

		shuffledDsts := slices.Clone(baseCfg.Destinations)
		rng.Shuffle(len(shuffledDsts), func(i, j int) {
			shuffledDsts[i], shuffledDsts[j] = shuffledDsts[j], shuffledDsts[i]
		})

		shuffledVLANs := slices.Clone(baseCfg.VLANs)
		rng.Shuffle(len(shuffledVLANs), func(i, j int) {
			shuffledVLANs[i], shuffledVLANs[j] = shuffledVLANs[j], shuffledVLANs[i]
		})

		shuffledShapes := slices.Clone(baseCfg.Shapes)
		rng.Shuffle(len(shuffledShapes), func(i, j int) {
			shuffledShapes[i], shuffledShapes[j] = shuffledShapes[j], shuffledShapes[i]
		})

		shuffledDom := NewL2TrafficDomain(L2TrafficDomainConfig{
			Sources:      shuffledSrcs,
			Destinations: shuffledDsts,
			VLANs:        shuffledVLANs,
			Shapes:       shuffledShapes,
			At:           baseCfg.At,
		})

		var trialTuples []Tuple
		shuffledDom.Enumerate(func(c Candidate) bool {
			trialTuples = append(trialTuples, c.Tuple)
			return true
		})

		if !slices.Equal(canonical, trialTuples) {
			t.Fatalf("trial %d produced different enumeration order: got %v, want %v", trial, trialTuples, canonical)
		}
	}
}

func TestL2TrafficDomainSizeEqualsEnumeratedCount(t *testing.T) {
	t.Parallel()

	cfg := sampleL2Config()
	dom := NewL2TrafficDomain(cfg)

	expectedSize := len(cfg.Sources) * len(cfg.Destinations) * len(cfg.VLANs) * len(cfg.Shapes)
	if dom.Size() != expectedSize {
		t.Fatalf("dom.Size() = %d, want %d", dom.Size(), expectedSize)
	}

	var count int
	dom.Enumerate(func(_ Candidate) bool {
		count++
		return true
	})

	if count != dom.Size() {
		t.Errorf("enumerated count %d != Size %d", count, dom.Size())
	}
}

func TestL2TrafficDomainEmptyYieldsNothingAndZeroSize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  L2TrafficDomainConfig
	}{
		{"empty-sources", L2TrafficDomainConfig{
			Destinations: []netaddr.MAC{{0x02, 0, 0, 0, 0, 1}},
			VLANs:        []vlan.ID{10},
			Shapes:       []FrameShape{{EtherType: ethernet.EtherTypeIPv4}},
		}},
		{"empty-destinations", L2TrafficDomainConfig{
			Sources: []fabric.Endpoint{{Node: "h1"}},
			VLANs:   []vlan.ID{10},
			Shapes:  []FrameShape{{EtherType: ethernet.EtherTypeIPv4}},
		}},
		{"empty-vlans", L2TrafficDomainConfig{
			Sources:      []fabric.Endpoint{{Node: "h1"}},
			Destinations: []netaddr.MAC{{0x02, 0, 0, 0, 0, 1}},
			Shapes:       []FrameShape{{EtherType: ethernet.EtherTypeIPv4}},
		}},
		{"empty-shapes", L2TrafficDomainConfig{
			Sources:      []fabric.Endpoint{{Node: "h1"}},
			Destinations: []netaddr.MAC{{0x02, 0, 0, 0, 0, 1}},
			VLANs:        []vlan.ID{10},
		}},
		{"all-empty", L2TrafficDomainConfig{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dom := NewL2TrafficDomain(tc.cfg)
			if dom.Size() != 0 {
				t.Fatalf("dom.Size() = %d, want 0", dom.Size())
			}
			var yielded int
			dom.Enumerate(func(_ Candidate) bool {
				yielded++
				return true
			})
			if yielded != 0 {
				t.Errorf("enumerated %d items on empty domain, want 0", yielded)
			}
		})
	}
}

func TestL2TrafficDomainDuplicateTuplesCollapse(t *testing.T) {
	t.Parallel()

	cleanCfg := sampleL2Config()
	cleanDom := NewL2TrafficDomain(cleanCfg)

	dupCfg := L2TrafficDomainConfig{
		Sources:      append(slices.Clone(cleanCfg.Sources), cleanCfg.Sources[0], cleanCfg.Sources[1]),
		Destinations: append(slices.Clone(cleanCfg.Destinations), cleanCfg.Destinations[0]),
		VLANs:        append(slices.Clone(cleanCfg.VLANs), cleanCfg.VLANs[0], cleanCfg.VLANs[2]),
		Shapes:       append(slices.Clone(cleanCfg.Shapes), cleanCfg.Shapes[0]),
		At:           cleanCfg.At,
	}
	dupDom := NewL2TrafficDomain(dupCfg)

	if dupDom.Size() != cleanDom.Size() {
		t.Fatalf("dupDom.Size() = %d, want %d (collapse to clean size)", dupDom.Size(), cleanDom.Size())
	}

	var cleanTuples, dupTuples []Tuple
	cleanDom.Enumerate(func(c Candidate) bool {
		cleanTuples = append(cleanTuples, c.Tuple)
		return true
	})
	dupDom.Enumerate(func(c Candidate) bool {
		dupTuples = append(dupTuples, c.Tuple)
		return true
	})

	if !slices.Equal(cleanTuples, dupTuples) {
		t.Errorf("dup domain produced different tuples: got %v, want %v", dupTuples, cleanTuples)
	}
}

func TestL2TrafficDomainEarlyStop(t *testing.T) {
	t.Parallel()

	cfg := sampleL2Config()
	dom := NewL2TrafficDomain(cfg)

	stopAfter := 3
	var count int
	dom.Enumerate(func(_ Candidate) bool {
		count++
		return count < stopAfter
	})

	if count != stopAfter {
		t.Fatalf("yielded %d items, want %d", count, stopAfter)
	}
}

func TestL2TrafficDomainEtherTypeZeroCollapsesWithIPv4(t *testing.T) {
	t.Parallel()

	cfg := L2TrafficDomainConfig{
		Sources:      []fabric.Endpoint{{Node: "h1"}},
		Destinations: []netaddr.MAC{{0x02, 0, 0, 0, 0, 1}},
		VLANs:        []vlan.ID{10},
		Shapes: []FrameShape{
			{EtherType: 0, Payload: []byte("payload")},
			{EtherType: ethernet.EtherTypeIPv4, Payload: []byte("payload")},
		},
	}
	dom := NewL2TrafficDomain(cfg)
	if dom.Size() != 1 {
		t.Fatalf("dom.Size() = %d, want 1", dom.Size())
	}

	var candidates []Candidate
	dom.Enumerate(func(c Candidate) bool {
		candidates = append(candidates, c)
		return true
	})
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if candidates[0].Scenario[0].Frame.EtherType != ethernet.EtherTypeIPv4 {
		t.Errorf("EtherType = 0x%04x, want 0x%04x", candidates[0].Scenario[0].Frame.EtherType, ethernet.EtherTypeIPv4)
	}
}
