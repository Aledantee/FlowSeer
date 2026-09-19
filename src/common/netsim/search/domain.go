// Package search provides bounded differential search over finite domains of
// network simulation scenarios, deterministic counterexample minimization,
// and first-divergence trace alignment.
package search

import (
	"net/netip"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

// Tuple represents the canonical, deterministic identity of a candidate in a search domain.
type Tuple string

// String returns the string representation of the tuple.
func (t Tuple) String() string {
	return string(t)
}

// TimedFault declares a cable fault applied at a specific simulation time.
type TimedFault struct {
	At    time.Time
	A     fabric.Endpoint
	B     fabric.Endpoint
	Fault fabric.Fault
}

// Clone returns an independent deep copy of the timed fault.
func (f TimedFault) Clone() TimedFault {
	return TimedFault{
		At:    f.At,
		A:     f.A,
		B:     f.B,
		Fault: f.Fault.Clone(),
	}
}

// Candidate encapsulates an evaluation target within a domain, pairing an ordered
// scenario of traffic injections and timed faults with its unique domain tuple identity.
type Candidate struct {
	Tuple    Tuple
	Scenario []fabric.Injection
	Faults   []TimedFault
}

// Clone returns an independent deep copy of the Candidate.
func (c Candidate) Clone() Candidate {
	cp := Candidate{
		Tuple: c.Tuple,
	}
	if c.Scenario != nil {
		cp.Scenario = make([]fabric.Injection, len(c.Scenario))
		for i, inj := range c.Scenario {
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
			cp.Scenario[i] = injCp
		}
	}
	if c.Faults != nil {
		cp.Faults = make([]TimedFault, len(c.Faults))
		for i, f := range c.Faults {
			cp.Faults[i] = f.Clone()
		}
	}
	return cp
}

// ToScenario converts candidate injections and timed faults into a scheduled fabric.Scenario.
func (c Candidate) ToScenario(budget int) fabric.Scenario {
	actions := make([]fabric.Action, 0, len(c.Scenario)+len(c.Faults))
	for i := range c.Scenario {
		inj := c.Scenario[i]
		cp := inj
		if inj.Frame.Tags != nil {
			cp.Frame.Tags = slices.Clone(inj.Frame.Tags)
		}
		if inj.Frame.Payload != nil {
			cp.Frame.Payload = slices.Clone(inj.Frame.Payload)
		}
		if inj.Packet != nil {
			pktCp := *inj.Packet
			if inj.Packet.Payload != nil {
				pktCp.Payload = slices.Clone(inj.Packet.Payload)
			}
			cp.Packet = &pktCp
		}
		actions = append(actions, fabric.Action{
			At:     inj.At,
			Kind:   fabric.ActionInject,
			Inject: &cp,
		})
	}
	for i := range c.Faults {
		f := c.Faults[i]
		actions = append(actions, fabric.Action{
			At:   f.At,
			Kind: fabric.ActionFault,
			Fault: &fabric.FaultAction{
				A:     f.A,
				B:     f.B,
				Fault: f.Fault.Clone(),
			},
		})
	}
	return fabric.Scenario{
		Name:    "search-candidate",
		Actions: actions,
		Budget:  budget,
	}
}

// Limits bounds the execution and retained difference collection of a differential search.
type Limits struct {
	// MaxCandidates limits the number of domain candidates evaluated. If 0 or negative,
	// all domain candidates are evaluated up to domain exhaustion.
	MaxCandidates int

	// MaxDifferences limits the number of distinct behavioral divergences retained
	// with full replay specifications and differences. Excess differences are counted
	// in summary statistics without full replay records.
	MaxDifferences int

	// Budget is the per-candidate simulation step budget passed to fabric.Compare.
	Budget int
}

// Domain represents a finite, deterministic enumerable set of simulation candidates.
//
// Size reports the exact count of unique candidates contained in the domain.
// Enumerate executes yield for each candidate in a total, deterministic order independent
// of map iteration or insertion order. Enumeration halts early if yield returns false.
type Domain interface {
	Size() int
	Enumerate(yield func(Candidate) bool)
}

// L3Domain is the search domain interface for routed layer-3 traffic exploration.
// Concrete enumeration is deferred to a subsequent phase.
type L3Domain interface {
	Domain
	RoutedSubnets() []netip.Prefix
}
