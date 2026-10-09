package fabric

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
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

	// ActionConfigure modifies the configuration of a virtual switch in the fabric.
	ActionConfigure ActionKind = "Configure"
)

// Operation applies a node change and compares its immutable inputs against
// another operation. Implementations own their inputs so actions may share them.
type Operation interface {
	Apply(*Fabric, string) error
	Diff(Operation) []trace.Change
}

// ConfigureAction specifies a target node and its configuration operation.
type ConfigureAction struct {
	Node      string
	Operation Operation
}

// Validate verifies that the target node is non-empty.
func (c ConfigureAction) Validate() error {
	if c.Node == "" || c.Operation == nil {
		return errs.Msg("configure node and operation cannot be empty")
	}
	return nil
}

// FaultAction specifies cable endpoints and the fault condition to configure.
type FaultAction struct {
	A     Endpoint
	B     Endpoint
	Fault cablemodel.Fault
}

// Validate rejects empty endpoint nodes and invalid fault parameters.
func (f FaultAction) Validate() error {
	if f.A.Node == "" || f.B.Node == "" {
		return errs.Msg("fault endpoints must have node specified")
	}
	return cablemodel.ValidateFault(f.Fault)
}

// McheckAction specifies a target virtual switch port to run protocol migration check.
type McheckAction struct {
	Node      string
	Operation Operation
}

// Validate verifies that the target node and port are non-empty.
func (m McheckAction) Validate() error {
	if m.Node == "" || m.Operation == nil {
		return errs.Msg("mcheck node and operation cannot be empty")
	}
	return nil
}

// Action represents a single timed mutation or frame introduction within a scenario.
type Action struct {
	// ID is required and unique within its scenario. It survives schedule edits.
	ID        string
	At        time.Time
	Index     int
	Kind      ActionKind
	Inject    *Injection
	Fault     *FaultAction
	Mcheck    *McheckAction
	Record    *Record
	Configure *ConfigureAction
}

// Validate ensures exactly one payload is present matching Kind, and inner timestamps agree.
func (a Action) Validate() error {
	if a.ID == "" {
		return errs.Msg("action id cannot be empty")
	}
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
	if a.Configure != nil {
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
	case ActionConfigure:
		if a.Configure == nil {
			return errs.Msg("action kind Configure requires Configure payload")
		}
		if err := a.Configure.Validate(); err != nil {
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
	case ActionConfigure:
		if cp.Configure != nil {
			cCopy := *cp.Configure
			cp.Configure = &cCopy
		}
	}
	return cp, nil
}

// Clone returns an independent deep copy of the Action.
func (a Action) Clone() Action {
	cp := a
	if a.Inject != nil {
		injCopy := a.Inject.clone()
		cp.Inject = &injCopy
	}
	if a.Fault != nil {
		fCopy := *a.Fault
		fCopy.Fault = a.Fault.Fault.Clone()
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
	if a.Configure != nil {
		cCopy := *a.Configure
		cp.Configure = &cCopy
	}
	return cp
}

type actionKindFact string

func (f actionKindFact) TypeID() string    { return "fabric.action.kind" }
func (f actionKindFact) Canonical() string { return string(f) }

type actionTimeFact string

func (f actionTimeFact) TypeID() string    { return "fabric.action.at" }
func (f actionTimeFact) Canonical() string { return string(f) }

type actionStringFact string

func (f actionStringFact) TypeID() string    { return "fabric.action.payload" }
func (f actionStringFact) Canonical() string { return string(f) }

// Diff returns changes between two Actions. A Configure payload diff keeps the
// kind "scenario.action"; its key nests the action ID and the inner operation
// subject, so two changes inside one switch configuration stay distinguishable.
func (a Action) Diff(other Action) []trace.Change {
	var changes []trace.Change
	subject := trace.Subject{Kind: "scenario.action", Key: a.ID}

	if !a.At.Equal(other.At) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "at",
			From:    actionTimeFact(a.At.UTC().Format(time.RFC3339Nano)),
			To:      actionTimeFact(other.At.UTC().Format(time.RFC3339Nano)),
		})
	}
	if a.ID != other.ID {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "id",
			From:    actionStringFact(a.ID),
			To:      actionStringFact(other.ID),
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
	if (a.Mcheck == nil) != (other.Mcheck == nil) {
		changes = append(changes, trace.Change{Layer: Layer, Subject: subject, Field: "mcheck", From: actionStringFact(mcheckSummary(a.Mcheck)), To: actionStringFact(mcheckSummary(other.Mcheck))})
	} else if a.Mcheck != nil && other.Mcheck != nil {
		if a.Mcheck.Node != other.Mcheck.Node {
			changes = append(changes, trace.Change{Layer: Layer, Subject: subject, Field: "mcheck.node", From: actionStringFact(a.Mcheck.Node), To: actionStringFact(other.Mcheck.Node)})
		} else {
			for _, d := range a.Mcheck.Operation.Diff(other.Mcheck.Operation) {
				d.Subject = trace.Subject{Kind: "scenario.action", Key: trace.CompositeKey(a.ID, d.Subject.Kind, d.Subject.Key)}
				changes = append(changes, d)
			}
		}
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
	if (a.Configure == nil) != (other.Configure == nil) {
		changes = append(changes, trace.Change{
			Layer:   Layer,
			Subject: subject,
			Field:   "configure",
			From:    actionStringFact(configureSummary(a.Configure)),
			To:      actionStringFact(configureSummary(other.Configure)),
		})
	} else if a.Configure != nil && other.Configure != nil {
		if a.Configure.Node != other.Configure.Node {
			changes = append(changes, trace.Change{
				Layer:   Layer,
				Subject: subject,
				Field:   "configure.node",
				From:    actionStringFact(a.Configure.Node),
				To:      actionStringFact(other.Configure.Node),
			})
		} else {
			diffs := a.Configure.Operation.Diff(other.Configure.Operation)
			for _, d := range diffs {
				d.Subject = trace.Subject{
					Kind: "scenario.action",
					Key:  trace.CompositeKey(a.ID, d.Subject.Kind, d.Subject.Key),
				}
				changes = append(changes, d)
			}
		}
	}

	return changes
}

func configureSummary(c *ConfigureAction) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("node=%s", c.Node)
}

func sameInjection(a, b Injection) bool {
	if !a.At.Equal(b.At) || a.Origin != b.Origin || a.Retention != b.Retention || a.Flow != b.Flow {
		return false
	}
	if (a.Packet == nil) != (b.Packet == nil) {
		return false
	}
	if a.Packet != nil && (a.Packet.To != b.Packet.To || a.Packet.Protocol != b.Packet.Protocol || !bytes.Equal(a.Packet.Payload, b.Packet.Payload)) {
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
	frame, _ := inj.Frame.Encode()
	packet := "none"
	if inj.Packet != nil {
		packet = fmt.Sprintf("to=%s,protocol=%d,payload=%x", inj.Packet.To, inj.Packet.Protocol, inj.Packet.Payload)
	}
	return fmt.Sprintf("origin=%s,at=%s,frame=%x,packet={%s},retention=%d,flow=%d", inj.Origin.Canonical(), inj.At.UTC().Format(time.RFC3339Nano), frame, packet, inj.Retention, inj.Flow)
}

func faultSummary(f *FaultAction) string {
	if f == nil {
		return ""
	}
	return fmt.Sprintf("a=%s,b=%s,kind=%s,n=%d,sequence=%v", f.A.Canonical(), f.B.Canonical(), f.Fault.Kind, f.Fault.N, f.Fault.Sequence)
}

func mcheckSummary(m *McheckAction) string {
	if m == nil {
		return ""
	}
	return fmt.Sprintf("node=%s", m.Node)
}

func recordSummary(r *Record) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("source=%s,origin=%s", r.Source, r.Origin.Canonical())
}

// Scenario declares an action schedule with persistent action identities.
type Scenario struct {
	Name    string
	Actions []Action
}

// Validate requires a name, unique action IDs, consistent action indices,
// and unique (At, Index) pairs in numbered mode.
func (s Scenario) Validate() error {
	numbered, err := s.validateHeader()
	if err != nil {
		return err
	}

	ids := make(map[string]int, len(s.Actions))
	for i, a := range s.Actions {
		if err := a.Validate(); err != nil {
			return errs.Wrapf(err, "action %d invalid", i)
		}
		if prev, ok := ids[a.ID]; ok {
			return errs.Msgf("duplicate action id %q at actions %d and %d", a.ID, prev, i)
		}
		ids[a.ID] = i
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

// Normalize normalizes actions, assigns 1-based indices
// to unnumbered actions, and sorts actions by At then Index to guarantee declaration order.
func (s Scenario) Normalize() (Scenario, error) {
	numbered, err := s.validateHeader()
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
		Actions: actions,
	}

	if err := norm.Validate(); err != nil {
		return Scenario{}, err
	}
	return norm, nil
}

// Clone returns an independent deep copy of the Scenario.
func (s Scenario) Clone() Scenario {
	cp := Scenario{
		Name: s.Name,
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

// DiffScenarios compares two scenarios and returns semantic changes.
// Actions pair by ID. Declaration indices order execution and do not identify changes.
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
	byID := make(map[string]Action, len(normB.Actions))
	for _, action := range normB.Actions {
		byID[action.ID] = action
	}
	for _, action := range normA.Actions {
		if other, ok := byID[action.ID]; ok {
			changes = append(changes, action.Diff(other)...)
			delete(byID, action.ID)
		} else {
			changes = append(changes, trace.Change{Layer: Layer, Subject: trace.Subject{Kind: "scenario.action", Key: action.ID}, Field: "action", From: actionStringFact(fmt.Sprintf("%s:%s", action.Kind, action.ID))})
		}
	}
	for _, action := range normB.Actions {
		if _, ok := byID[action.ID]; ok {
			changes = append(changes, trace.Change{Layer: Layer, Subject: trace.Subject{Kind: "scenario.action", Key: action.ID}, Field: "action", To: actionStringFact(fmt.Sprintf("%s:%s", action.Kind, action.ID))})
		}
	}
	return changes, nil
}
