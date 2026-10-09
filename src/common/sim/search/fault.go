package search

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
)

// TimedFaultDomainConfig configures a finite timed cable fault exploration domain. Its
// values are safe for concurrent reads but not for concurrent mutation.
type TimedFaultDomainConfig struct {
	Faults      []fabric.FaultAction
	Times       []time.Time
	BaseTraffic []fabric.Action
}

// Validate rejects invalid faults, zero times, invalid base injections, and
// domain sizes beyond int. Empty fault or time dimensions yield an empty domain.
func (c TimedFaultDomainConfig) Validate() error {
	for i, fault := range c.Faults {
		if err := fault.Validate(); err != nil {
			return errs.Wrapf(err, "fault %d", i)
		}
	}
	for i, at := range c.Times {
		if at.IsZero() {
			return errs.Msgf("fault time %d cannot be zero", i)
		}
	}
	for i, action := range c.BaseTraffic {
		if action.Kind != fabric.ActionInject {
			return errs.Msgf("base traffic action %d must be injection", i)
		}
	}
	if err := (Candidate{Actions: c.BaseTraffic}).Validate(); err != nil {
		return errs.Wrap(err, "base traffic")
	}
	size, err := productSize(len(deduplicateAndSortFaults(c.Faults)), len(deduplicateAndSortTimes(c.Times)))
	if err != nil {
		return err
	}
	if size > 0 {
		for _, action := range c.BaseTraffic {
			if action.Index == math.MaxInt {
				return errs.Msg("generated fault index exceeds int")
			}
		}
	}
	return nil
}

// TimedFaultDomain enumerates declared cable faults across declared simulation times
// in a deterministic, total order.
type TimedFaultDomain struct {
	faults      []fabric.FaultAction
	times       []time.Time
	baseTraffic []fabric.Action
	size        int
}

// NewTimedFaultDomain constructs, deduplicates, and deterministically sorts declared
// cable faults and times into an enumerable fault domain.
func NewTimedFaultDomain(cfg TimedFaultDomainConfig) (*TimedFaultDomain, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	faults := deduplicateAndSortFaults(cfg.Faults)
	times := deduplicateAndSortTimes(cfg.Times)

	size, err := productSize(len(faults), len(times))
	if err != nil {
		return nil, err
	}
	baseTraffic := (Candidate{Actions: cfg.BaseTraffic}).ToScenario().Actions

	return &TimedFaultDomain{
		faults:      faults,
		times:       times,
		baseTraffic: baseTraffic,
		size:        size,
	}, nil
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

func (d *TimedFaultDomain) buildCandidate(f fabric.FaultAction, at time.Time) Candidate {
	actions := (Candidate{Actions: d.baseTraffic}).Clone().Actions
	index := 0
	for _, action := range actions {
		index = max(index, action.Index)
	}
	fault := f
	fault.Fault = f.Fault.Clone()
	actions = append(actions, fabric.Action{ID: fmt.Sprintf("fault-%d", index+1), At: at, Index: index + 1, Kind: fabric.ActionFault, Fault: &fault})
	return Candidate{Tuple: formatFaultTuple(f, at), Actions: actions}
}

func formatFaultTuple(f fabric.FaultAction, at time.Time) Tuple {
	aStr := f.A.Canonical()
	bStr := f.B.Canonical()

	detail := string(f.Fault.Kind)
	if f.Fault.N > 0 {
		detail += fmt.Sprintf("(N=%d)", f.Fault.N)
	}
	if len(f.Fault.Sequence) > 0 {
		detail += fmt.Sprintf("(Seq=%v)", f.Fault.Sequence)
	}

	return Tuple(fmt.Sprintf("%s--%s@%s:%s", aStr, bStr, at.Round(0).UTC().Format(time.RFC3339Nano), detail))
}

func deduplicateAndSortTimes(in []time.Time) []time.Time {
	seen := make(map[time.Time]bool, len(in))
	var out []time.Time
	for _, t := range in {
		key := t.Round(0).UTC()
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	slices.SortFunc(out, func(a, b time.Time) int {
		return a.Compare(b)
	})
	return out
}

func deduplicateAndSortFaults(in []fabric.FaultAction) []fabric.FaultAction {
	type key struct {
		aNode, aPort string
		bNode, bPort string
		kind         cablemodel.FaultKind
		n            uint
		seq          string
	}
	seen := make(map[key]bool, len(in))
	var out []fabric.FaultAction
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
			out = append(out, fabric.FaultAction{
				A:     f.A,
				B:     f.B,
				Fault: f.Fault.Clone(),
			})
		}
	}
	slices.SortFunc(out, func(a, b fabric.FaultAction) int {
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
