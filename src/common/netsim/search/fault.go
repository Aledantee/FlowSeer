package search

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

// FaultSpec specifies a cable fault between two endpoints.
type FaultSpec struct {
	A     fabric.Endpoint
	B     fabric.Endpoint
	Fault fabric.Fault
}

// TimedFaultDomainConfig configures a finite timed cable fault exploration domain.
type TimedFaultDomainConfig struct {
	Faults      []FaultSpec
	Times       []time.Time
	BaseTraffic []fabric.Injection
}

// TimedFaultDomain enumerates declared cable faults across declared simulation times
// in a deterministic, total order.
type TimedFaultDomain struct {
	faults      []FaultSpec
	times       []time.Time
	baseTraffic []fabric.Injection
	size        int
}

// NewTimedFaultDomain constructs, deduplicates, and deterministically sorts declared
// cable faults and times into an enumerable fault domain.
func NewTimedFaultDomain(cfg TimedFaultDomainConfig) *TimedFaultDomain {
	faults := deduplicateAndSortFaultSpecs(cfg.Faults)
	times := deduplicateAndSortTimes(cfg.Times)

	size := 0
	if len(faults) > 0 && len(times) > 0 {
		size = len(faults) * len(times)
	}

	var baseTraffic []fabric.Injection
	if cfg.BaseTraffic != nil {
		baseTraffic = make([]fabric.Injection, len(cfg.BaseTraffic))
		for i, inj := range cfg.BaseTraffic {
			injCp := inj
			if inj.Frame.Tags != nil {
				injCp.Frame.Tags = slices.Clone(inj.Frame.Tags)
			}
			if inj.Frame.Payload != nil {
				injCp.Frame.Payload = slices.Clone(inj.Frame.Payload)
			}
			if inj.Packet != nil {
				pktCp := *inj.Packet
				if inj.Packet.Payload != nil {
					pktCp.Payload = slices.Clone(inj.Packet.Payload)
				}
				injCp.Packet = &pktCp
			}
			baseTraffic[i] = injCp
		}
	}

	return &TimedFaultDomain{
		faults:      faults,
		times:       times,
		baseTraffic: baseTraffic,
		size:        size,
	}
}

// Size returns the total count of distinct candidates in the domain.
func (d *TimedFaultDomain) Size() int {
	return d.size
}

// Enumerate executes yield for every candidate in total deterministic order.
func (d *TimedFaultDomain) Enumerate(yield func(Candidate) bool) {
	if d.size == 0 {
		return
	}

	for _, f := range d.faults {
		for _, at := range d.times {
			cand := d.buildCandidate(f, at)
			if !yield(cand) {
				return
			}
		}
	}
}

func (d *TimedFaultDomain) buildCandidate(f FaultSpec, at time.Time) Candidate {
	var scenario []fabric.Injection
	if len(d.baseTraffic) > 0 {
		scenario = make([]fabric.Injection, len(d.baseTraffic))
		for i, inj := range d.baseTraffic {
			injCp := inj
			if inj.Frame.Tags != nil {
				injCp.Frame.Tags = slices.Clone(inj.Frame.Tags)
			}
			if inj.Frame.Payload != nil {
				injCp.Frame.Payload = slices.Clone(inj.Frame.Payload)
			}
			if inj.Packet != nil {
				pktCp := *inj.Packet
				if inj.Packet.Payload != nil {
					pktCp.Payload = slices.Clone(inj.Packet.Payload)
				}
				injCp.Packet = &pktCp
			}
			scenario[i] = injCp
		}
	}

	faults := []TimedFault{
		{
			At:    at,
			A:     f.A,
			B:     f.B,
			Fault: f.Fault.Clone(),
		},
	}

	tuple := formatFaultTuple(f, at)

	return Candidate{
		Tuple:    tuple,
		Scenario: scenario,
		Faults:   faults,
	}
}

func formatFaultTuple(f FaultSpec, at time.Time) Tuple {
	aStr := f.A.Node
	if f.A.Port != "" {
		aStr += ":" + f.A.Port
	}
	bStr := f.B.Node
	if f.B.Port != "" {
		bStr += ":" + f.B.Port
	}

	detail := string(f.Fault.Kind)
	if f.Fault.N > 0 {
		detail += fmt.Sprintf("(N=%d)", f.Fault.N)
	}
	if len(f.Fault.Sequence) > 0 {
		detail += fmt.Sprintf("(Seq=%v)", f.Fault.Sequence)
	}

	return Tuple(fmt.Sprintf("%s--%s@%s:%s", aStr, bStr, at.Format(time.RFC3339Nano), detail))
}

func deduplicateAndSortTimes(in []time.Time) []time.Time {
	seen := make(map[int64]bool, len(in))
	var out []time.Time
	for _, t := range in {
		key := t.UnixNano()
		if !seen[key] {
			seen[key] = true
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b time.Time) int {
		return a.Compare(b)
	})
	return out
}

func deduplicateAndSortFaultSpecs(in []FaultSpec) []FaultSpec {
	type key struct {
		aNode, aPort string
		bNode, bPort string
		kind         fabric.FaultKind
		n            uint
		seq          string
	}
	seen := make(map[key]bool, len(in))
	var out []FaultSpec
	for _, f := range in {
		k := key{
			aNode: f.A.Node,
			aPort: f.A.Port,
			bNode: f.B.Node,
			bPort: f.B.Port,
			kind:  f.Fault.Kind,
			n:     f.Fault.N,
			seq:   fmt.Sprint(f.Fault.Sequence),
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, FaultSpec{
				A:     f.A,
				B:     f.B,
				Fault: f.Fault.Clone(),
			})
		}
	}
	slices.SortFunc(out, func(a, b FaultSpec) int {
		if r := cmp.Compare(a.A.Node, b.A.Node); r != 0 {
			return r
		}
		if r := cmp.Compare(a.A.Port, b.A.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(a.B.Node, b.B.Node); r != 0 {
			return r
		}
		if r := cmp.Compare(a.B.Port, b.B.Port); r != 0 {
			return r
		}
		if r := cmp.Compare(string(a.Fault.Kind), string(b.Fault.Kind)); r != 0 {
			return r
		}
		if r := cmp.Compare(a.Fault.N, b.Fault.N); r != 0 {
			return r
		}
		return cmp.Compare(fmt.Sprint(a.Fault.Sequence), fmt.Sprint(b.Fault.Sequence))
	})
	return out
}
