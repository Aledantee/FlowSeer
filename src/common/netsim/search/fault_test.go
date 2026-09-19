package search

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

func sampleFaultConfig() TimedFaultDomainConfig {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	return TimedFaultDomainConfig{
		Faults: []FaultSpec{
			{
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				Fault: fabric.Fault{Kind: fabric.FaultCut},
			},
			{
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:     fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				Fault: fabric.Fault{Kind: fabric.FaultDeadAToB},
			},
			{
				A:     fabric.Endpoint{Node: "sw2", Port: "1/1/3"},
				B:     fabric.Endpoint{Node: "sw3", Port: "1/1/3"},
				Fault: fabric.Fault{Kind: fabric.FaultLoseEveryNth, N: 5},
			},
		},
		Times: []time.Time{
			t0,
			t0.Add(10 * time.Millisecond),
			t0.Add(50 * time.Millisecond),
			t0.Add(100 * time.Millisecond),
		},
	}
}

func TestTimedFaultDomainStableTotalOrderOverRandomizedInput(t *testing.T) {
	t.Parallel()

	baseCfg := sampleFaultConfig()
	domBase := NewTimedFaultDomain(baseCfg)

	var canonical []Tuple
	domBase.Enumerate(func(c Candidate) bool {
		canonical = append(canonical, c.Tuple)
		return true
	})

	if len(canonical) != domBase.Size() {
		t.Fatalf("enumerated count %d != Size() %d", len(canonical), domBase.Size())
	}

	rng := rand.New(rand.NewPCG(123, 456))
	for trial := 0; trial < 25; trial++ {
		shuffledFaults := slices.Clone(baseCfg.Faults)
		rng.Shuffle(len(shuffledFaults), func(i, j int) {
			shuffledFaults[i], shuffledFaults[j] = shuffledFaults[j], shuffledFaults[i]
		})

		shuffledTimes := slices.Clone(baseCfg.Times)
		rng.Shuffle(len(shuffledTimes), func(i, j int) {
			shuffledTimes[i], shuffledTimes[j] = shuffledTimes[j], shuffledTimes[i]
		})

		shuffledDom := NewTimedFaultDomain(TimedFaultDomainConfig{
			Faults: shuffledFaults,
			Times:  shuffledTimes,
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

func TestTimedFaultDomainSizeEqualsEnumeratedCount(t *testing.T) {
	t.Parallel()

	cfg := sampleFaultConfig()
	dom := NewTimedFaultDomain(cfg)

	expectedSize := len(cfg.Faults) * len(cfg.Times)
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

func TestTimedFaultDomainEmptyYieldsNothingAndZeroSize(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		cfg  TimedFaultDomainConfig
	}{
		{"empty-faults", TimedFaultDomainConfig{
			Times: []time.Time{t0},
		}},
		{"empty-times", TimedFaultDomainConfig{
			Faults: []FaultSpec{
				{A: fabric.Endpoint{Node: "sw1"}, B: fabric.Endpoint{Node: "sw2"}, Fault: fabric.Fault{Kind: fabric.FaultCut}},
			},
		}},
		{"all-empty", TimedFaultDomainConfig{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dom := NewTimedFaultDomain(tc.cfg)
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

func TestTimedFaultDomainDuplicateTuplesCollapse(t *testing.T) {
	t.Parallel()

	cleanCfg := sampleFaultConfig()
	cleanDom := NewTimedFaultDomain(cleanCfg)

	dupCfg := TimedFaultDomainConfig{
		Faults: append(slices.Clone(cleanCfg.Faults), cleanCfg.Faults[0], cleanCfg.Faults[1]),
		Times:  append(slices.Clone(cleanCfg.Times), cleanCfg.Times[0], cleanCfg.Times[2]),
	}
	dupDom := NewTimedFaultDomain(dupCfg)

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

func TestTimedFaultDomainEarlyStop(t *testing.T) {
	t.Parallel()

	cfg := sampleFaultConfig()
	dom := NewTimedFaultDomain(cfg)

	stopAfter := 4
	var count int
	dom.Enumerate(func(_ Candidate) bool {
		count++
		return count < stopAfter
	})

	if count != stopAfter {
		t.Fatalf("yielded %d items, want %d", count, stopAfter)
	}
}
