// Package netmodel translates between FlowSeer network model messages and the virtual switch.
package netmodel

import (
	"maps"
	"slices"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// SourceContext describes stable source identity and caller context for model loading
// without importing transport provenance types.
type SourceContext struct {
	DeviceID string
	Origin   string
	Context  string
}

// Skipped records an entity, facet, or row that was omitted during loading,
// along with its exact affected scope, reason, and supporting evidence references.
type Skipped struct {
	Scope    analysis.Scope
	Port     string
	What     string
	Why      string
	Evidence []trace.EvidenceRef
}

// Default records a field whose value was defaulted or coerced during loading,
// along with its exact affected scope, defaulted value, and supporting evidence references.
type Default struct {
	Scope    analysis.Scope
	Port     string
	Field    string
	Value    string
	Evidence []trace.EvidenceRef
}

// Conflict records conflicting source records or rows for the same entity or key,
// along with its exact affected scope, detail, and supporting evidence references.
type Conflict struct {
	Scope    analysis.Scope
	What     string
	Key      string
	Detail   string
	Evidence []trace.EvidenceRef
}

// Report details the capability set inferred or requested, how each layer was
// chosen, omitted entities or facets, ports without switchport configuration,
// defaulted fields, and conflicts.
//
// Every slice is sorted so two reports compare deterministically.
type Report struct {
	Capabilities      []port.Layer
	CapabilitySources map[port.Layer]string
	Skipped           []Skipped
	Defaults          []Default
	Conflicts         []Conflict
	NoSwitchport      []string
}

// Clone returns an independent deep copy of the report.
func (r Report) Clone() Report {
	cp := Report{
		Capabilities: slices.Clone(r.Capabilities),
		NoSwitchport: slices.Clone(r.NoSwitchport),
	}
	if r.CapabilitySources != nil {
		cp.CapabilitySources = maps.Clone(r.CapabilitySources)
	}
	if len(r.Skipped) > 0 {
		cp.Skipped = make([]Skipped, len(r.Skipped))
		for i, s := range r.Skipped {
			cp.Skipped[i] = Skipped{
				Scope:    s.Scope,
				Port:     s.Port,
				What:     s.What,
				Why:      s.Why,
				Evidence: slices.Clone(s.Evidence),
			}
		}
	}
	if len(r.Defaults) > 0 {
		cp.Defaults = make([]Default, len(r.Defaults))
		for i, d := range r.Defaults {
			cp.Defaults[i] = Default{
				Scope:    d.Scope,
				Port:     d.Port,
				Field:    d.Field,
				Value:    d.Value,
				Evidence: slices.Clone(d.Evidence),
			}
		}
	}
	if len(r.Conflicts) > 0 {
		cp.Conflicts = make([]Conflict, len(r.Conflicts))
		for i, c := range r.Conflicts {
			cp.Conflicts[i] = Conflict{
				Scope:    c.Scope,
				What:     c.What,
				Key:      c.Key,
				Detail:   c.Detail,
				Evidence: slices.Clone(c.Evidence),
			}
		}
	}
	return cp
}

// Result contains the output of loading network model inputs into a virtual switch
// construction specification, detailed loading report, and analysis readiness metadata.
// The zero value is safe for concurrent use. Result is copy-isolated.
type Result struct {
	Spec     vswitch.ConstructionSpec
	Report   Report
	Metadata analysis.Metadata
}

// Readiness returns the analysis status derived from all issues affecting the loaded scope.
func (r Result) Readiness() analysis.Status {
	return r.Metadata.Status()
}

// Clone returns an independent deep copy of the result.
func (r Result) Clone() Result {
	return Result{
		Spec:     r.Spec.Clone(),
		Report:   r.Report.Clone(),
		Metadata: r.Metadata,
	}
}
