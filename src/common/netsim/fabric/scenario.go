package fabric

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// ActionKind identifies the mutation or injection performed by a scenario action.
type ActionKind string

const (
	// ActionInject introduces a caller-defined frame or packet into the simulation.
	ActionInject ActionKind = "Inject"

	// ActionFault applies or clears a physical medium fault on a cable.
	ActionFault ActionKind = "Fault"

	// ActionMcheck triggers spanning tree protocol migration checking on a switch port.
	ActionMcheck ActionKind = "Mcheck"

	// ActionRecord replays a packet captured from an external trace or source.
	ActionRecord ActionKind = "Record"
)

// FaultAction specifies cable endpoints and the fault condition to configure.
type FaultAction struct {
	A     Endpoint
	B     Endpoint
	Fault Fault
}

// Validate verifies that both endpoints of the cable fault are non-empty.
func (f FaultAction) Validate() error {
	if f.A.Node == "" || f.B.Node == "" {
		return errs.Msg("fault endpoints must have node specified")
	}
	return nil
}

// McheckAction specifies a target virtual switch port to run protocol migration check.
type McheckAction struct {
	Node string
	Port string
}

// Validate verifies that the target node and port are non-empty.
func (m McheckAction) Validate() error {
	if m.Node == "" || m.Port == "" {
		return errs.Msg("mcheck node and port cannot be empty")
	}
	return nil
}

// Action represents a single timed mutation or frame introduction within a scenario.
type Action struct {
	At     time.Time
	Index  int
	Kind   ActionKind
	Inject *Injection
	Fault  *FaultAction
	Mcheck *McheckAction
	Record *Record
}

// Validate ensures exactly one payload is present matching Kind, and inner timestamps agree.
func (a Action) Validate() error {
	if a.At.IsZero() {
		return errs.Msg("action timestamp cannot be zero")
	}
	if a.Index < 0 {
		return errs.Msgf("action index cannot be negative, got %d", a.Index)
	}

	var nonNil int
	if a.Inject != nil {
		nonNil++
	}
	if a.Fault != nil {
		nonNil++
	}
	if a.Mcheck != nil {
		nonNil++
	}
	if a.Record != nil {
		nonNil++
	}
	if nonNil != 1 {
		return errs.Msgf("action must specify exactly one payload pointer, got %d", nonNil)
	}

	switch a.Kind {
	case ActionInject:
		if a.Inject == nil {
			return errs.Msg("action kind Inject requires Inject payload")
		}
		if !a.Inject.At.IsZero() && !a.Inject.At.Equal(a.At) {
			return errs.Msgf("injection At %s disagrees with action At %s", a.Inject.At, a.At)
		}
		if a.Inject.Origin.Node == "" {
			return errs.Msg("injection origin node cannot be empty")
		}
	case ActionFault:
		if a.Fault == nil {
			return errs.Msg("action kind Fault requires Fault payload")
		}
		if err := a.Fault.Validate(); err != nil {
			return err
		}
	case ActionMcheck:
		if a.Mcheck == nil {
			return errs.Msg("action kind Mcheck requires Mcheck payload")
		}
		if err := a.Mcheck.Validate(); err != nil {
			return err
		}
	case ActionRecord:
		if a.Record == nil {
			return errs.Msg("action kind Record requires Record payload")
		}
		if !a.Record.At.IsZero() && !a.Record.At.Equal(a.At) {
			return errs.Msgf("record At %s disagrees with action At %s", a.Record.At, a.At)
		}
		if err := a.Record.Validate(); err != nil {
			return err
		}
	default:
		return errs.Msgf("unknown action kind %q", a.Kind)
	}

	return nil
}

// Normalize copies the action, stamping At onto inner payloads if zero, and normalizing records.
func (a Action) Normalize() (Action, error) {
	cp := a.Clone()
	switch cp.Kind {
	case ActionInject:
		if cp.Inject != nil {
			injCopy := *cp.Inject
			if injCopy.At.IsZero() {
				injCopy.At = cp.At
			}
			cp.Inject = &injCopy
		}
	case ActionRecord:
		if cp.Record != nil {
			recCopy := cp.Record.Clone()
			if recCopy.At.IsZero() {
				recCopy.At = cp.At
			}
			normRec, err := recCopy.Normalize()
			if err != nil {
				return Action{}, err
			}
			cp.Record = &normRec
		}
	case ActionFault:
		if cp.Fault != nil {
			fCopy := *cp.Fault
			cp.Fault = &fCopy
		}
	case ActionMcheck:
		if cp.Mcheck != nil {
			mCopy := *cp.Mcheck
			cp.Mcheck = &mCopy
		}
	}
	return cp, nil
}

// Clone returns an independent deep copy of the Action.
func (a Action) Clone() Action {
	cp := a
	if a.Inject != nil {
		injCopy := *a.Inject
		cp.Inject = &injCopy
	}
	if a.Fault != nil {
		fCopy := *a.Fault
		cp.Fault = &fCopy
	}
	if a.Mcheck != nil {
		mCopy := *a.Mcheck
		cp.Mcheck = &mCopy
	}
	if a.Record != nil {
		rCopy := a.Record.Clone()
		cp.Record = &rCopy
	}
	return cp
}

type actionKindFact string

func (f actionKindFact) TypeID() string    { return "fabric.action.kind" }
func (f actionKindFact) Canonical() string { return string(f) }

type actionIndexFact int

func (f actionIndexFact) TypeID() string    { return "fabric.action.index" }
func (f actionIndexFact) Canonical() string { return strconv.Itoa(int(f)) }

type actionTimeFact string

func (f actionTimeFact) TypeID() string    { return "fabric.action.at" }
func (f actionTimeFact) Canonical() string { return string(f) }

type actionStringFact string

func (f actionStringFact) TypeID() string    { return "fabric.action.payload" }
func (f actionStringFact) Canonical() string { return string(f) }

// Diff returns changes between two Actions.
func (a Action) Diff(other Action) []trace.Change {
	var changes []trace.Change
	subject := trace.Subject{Kind: "scenario.action", Key: strconv.Itoa(a.Index)}

	if !a.At.Equal(other.At) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "at",
			From:    actionTimeFact(a.At.UTC().Format(time.RFC3339Nano)),
			To:      actionTimeFact(other.At.UTC().Format(time.RFC3339Nano)),
		})
	}
	if a.Index != other.Index {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "index",
			From:    actionIndexFact(a.Index),
			To:      actionIndexFact(other.Index),
		})
	}
	if a.Kind != other.Kind {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "kind",
			From:    actionKindFact(a.Kind),
			To:      actionKindFact(other.Kind),
		})
	}
	if (a.Inject == nil) != (other.Inject == nil) || (a.Inject != nil && other.Inject != nil && !sameInjection(*a.Inject, *other.Inject)) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "inject",
			From:    actionStringFact(injectionSummary(a.Inject)),
			To:      actionStringFact(injectionSummary(other.Inject)),
		})
	}
	if (a.Fault == nil) != (other.Fault == nil) || (a.Fault != nil && other.Fault != nil && !sameFaultAction(a.Fault, other.Fault)) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "fault",
			From:    actionStringFact(faultSummary(a.Fault)),
			To:      actionStringFact(faultSummary(other.Fault)),
		})
	}
	if (a.Mcheck == nil) != (other.Mcheck == nil) || (a.Mcheck != nil && other.Mcheck != nil && *a.Mcheck != *other.Mcheck) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "mcheck",
			From:    actionStringFact(mcheckSummary(a.Mcheck)),
			To:      actionStringFact(mcheckSummary(other.Mcheck)),
		})
	}
	if (a.Record == nil) != (other.Record == nil) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "record",
			From:    actionStringFact(recordSummary(a.Record)),
			To:      actionStringFact(recordSummary(other.Record)),
		})
	} else if a.Record != nil && other.Record != nil {
		changes = append(changes, a.Record.Diff(*other.Record)...)
	}

	return changes
}

func sameInjection(a, b Injection) bool {
	if !a.At.Equal(b.At) || a.Origin != b.Origin {
		return false
	}
	encA, _ := a.Frame.Encode()
	encB, _ := b.Frame.Encode()
	return bytes.Equal(encA, encB)
}

func sameFaultAction(a, b *FaultAction) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.A == b.A && a.B == b.B && sameFault(a.Fault, b.Fault)
}

func injectionSummary(inj *Injection) string {
	if inj == nil {
		return ""
	}
	return fmt.Sprintf("origin=%s:%s,at=%s", inj.Origin.Node, inj.Origin.Port, inj.At.UTC().Format(time.RFC3339Nano))
}

func faultSummary(f *FaultAction) string {
	if f == nil {
		return ""
	}
	return fmt.Sprintf("a=%s,b=%s,kind=%s", f.A.Canonical(), f.B.Canonical(), f.Fault.Kind)
}

func mcheckSummary(m *McheckAction) string {
	if m == nil {
		return ""
	}
	return fmt.Sprintf("node=%s,port=%s", m.Node, m.Port)
}

func recordSummary(r *Record) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("source=%s,origin=%s", r.Source, r.Origin.Canonical())
}

// Scenario encapsulates an entire reproducible network simulation with actions and budgets.
type Scenario struct {
	Name    string
	Spec    ConstructionSpec
	Actions []Action
	Budget  int
	Window  int
}

// Validate verifies that the scenario has a valid name, positive budget, non-negative window,
// consistent action indices, unique (At, Index) pairs in numbered mode, and no actions scheduled
// before the specification start time.
func (s Scenario) Validate() error {
	numbered, err := s.validateHeader()
	if err != nil {
		return err
	}

	for i, a := range s.Actions {
		if err := a.Validate(); err != nil {
			return errs.Wrapf(err, "action %d invalid", i)
		}
		if !s.Spec.Start.IsZero() && a.At.Before(s.Spec.Start) {
			return errs.Msgf("action %d at %s precedes spec start %s", i, a.At, s.Spec.Start)
		}
	}

	if numbered {
		type actionKey struct {
			sec   int64
			nsec  int32
			index int
		}
		seen := make(map[actionKey]int, len(s.Actions))
		for i, a := range s.Actions {
			u := a.At.UTC()
			k := actionKey{sec: u.Unix(), nsec: int32(u.Nanosecond()), index: a.Index}
			if prev, ok := seen[k]; ok {
				return errs.Msgf("duplicate action (At, Index) (%s, %d) at actions %d and %d", a.At, a.Index, prev, i)
			}
			seen[k] = i
		}
	}

	return nil
}

func (s Scenario) validateHeader() (numbered bool, err error) {
	if s.Name == "" {
		return false, errs.Msg("scenario name cannot be empty")
	}
	if s.Budget <= 0 {
		return false, errs.Msgf("scenario budget must be positive, got %d", s.Budget)
	}
	if s.Window < 0 {
		return false, errs.Msgf("scenario window cannot be negative, got %d", s.Window)
	}

	var numCount, unnumCount int
	for _, a := range s.Actions {
		if a.Index > 0 {
			numCount++
		} else {
			unnumCount++
		}
	}
	if numCount > 0 && unnumCount > 0 {
		return false, errs.Msg("scenario actions must be either all numbered or none numbered")
	}
	return numCount > 0, nil
}

// Normalize normalizes the underlying specification and actions, assigns 1-based indices
// to unnumbered actions, and sorts actions by At then Index to guarantee declaration order.
func (s Scenario) Normalize() (Scenario, error) {
	numbered, err := s.validateHeader()
	if err != nil {
		return Scenario{}, err
	}

	normSpec, err := s.Spec.Normalize()
	if err != nil {
		return Scenario{}, err
	}

	actions := make([]Action, len(s.Actions))
	for i, a := range s.Actions {
		normA, err := a.Normalize()
		if err != nil {
			return Scenario{}, err
		}
		if !numbered {
			normA.Index = i + 1
		}
		actions[i] = normA
	}

	slices.SortFunc(actions, func(a, b Action) int {
		if r := a.At.Compare(b.At); r != 0 {
			return r
		}
		return cmp.Compare(a.Index, b.Index)
	})

	norm := Scenario{
		Name:    s.Name,
		Spec:    normSpec,
		Actions: actions,
		Budget:  s.Budget,
		Window:  s.Window,
	}

	if err := norm.Validate(); err != nil {
		return Scenario{}, err
	}
	return norm, nil
}

// Clone returns an independent deep copy of the Scenario.
func (s Scenario) Clone() Scenario {
	cp := Scenario{
		Name:   s.Name,
		Spec:   s.Spec.Clone(),
		Budget: s.Budget,
		Window: s.Window,
	}
	if s.Actions != nil {
		cp.Actions = make([]Action, len(s.Actions))
		for i, a := range s.Actions {
			cp.Actions[i] = a.Clone()
		}
	}
	return cp
}

type scenarioNameFact string

func (f scenarioNameFact) TypeID() string    { return "fabric.scenario.name" }
func (f scenarioNameFact) Canonical() string { return string(f) }

type scenarioBudgetFact int

func (f scenarioBudgetFact) TypeID() string    { return "fabric.scenario.budget" }
func (f scenarioBudgetFact) Canonical() string { return strconv.Itoa(int(f)) }

type scenarioWindowFact int

func (f scenarioWindowFact) TypeID() string    { return "fabric.scenario.window" }
func (f scenarioWindowFact) Canonical() string { return strconv.Itoa(int(f)) }

// DiffScenarios compares two scenarios and returns semantic changes.
// Both arguments are normalized before comparison, ensuring index assignment does not introduce false diffs.
func DiffScenarios(a, b Scenario) ([]trace.Change, error) {
	normA, err := a.Normalize()
	if err != nil {
		return nil, err
	}
	normB, err := b.Normalize()
	if err != nil {
		return nil, err
	}

	var changes []trace.Change
	subject := trace.Subject{Kind: "scenario"}

	if normA.Name != normB.Name {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "name",
			From:    scenarioNameFact(normA.Name),
			To:      scenarioNameFact(normB.Name),
		})
	}
	if normA.Budget != normB.Budget {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "budget",
			From:    scenarioBudgetFact(normA.Budget),
			To:      scenarioBudgetFact(normB.Budget),
		})
	}
	if normA.Window != normB.Window {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "window",
			From:    scenarioWindowFact(normA.Window),
			To:      scenarioWindowFact(normB.Window),
		})
	}

	specChanges, err := DiffSpecs(normA.Spec, normB.Spec)
	if err != nil {
		return nil, err
	}
	changes = append(changes, specChanges...)

	minLen := min(len(normA.Actions), len(normB.Actions))
	for i := 0; i < minLen; i++ {
		changes = append(changes, normA.Actions[i].Diff(normB.Actions[i])...)
	}
	if len(normA.Actions) < len(normB.Actions) {
		for i := len(normA.Actions); i < len(normB.Actions); i++ {
			act := normB.Actions[i]
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "scenario.action", Key: strconv.Itoa(act.Index)},
				Field:   "action",
				To:      actionStringFact(fmt.Sprintf("%s:%d", act.Kind, act.Index)),
			})
		}
	} else if len(normA.Actions) > len(normB.Actions) {
		for i := len(normB.Actions); i < len(normA.Actions); i++ {
			act := normA.Actions[i]
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: trace.Subject{Kind: "scenario.action", Key: strconv.Itoa(act.Index)},
				Field:   "action",
				From:    actionStringFact(fmt.Sprintf("%s:%d", act.Kind, act.Index)),
			})
		}
	}

	return changes, nil
}
