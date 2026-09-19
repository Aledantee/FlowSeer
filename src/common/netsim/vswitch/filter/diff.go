package filter

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// BoolFact wraps a boolean configuration property as a trace.Fact.
type BoolFact bool

// TypeID returns the fact type identifier.
func (f BoolFact) TypeID() string { return "filter.bool" }

// Canonical returns "true" or "false".
func (f BoolFact) Canonical() string { return strconv.FormatBool(bool(f)) }

// ActionFact wraps a filter Action as a trace.Fact.
type ActionFact Action

// TypeID returns the fact type identifier.
func (f ActionFact) TypeID() string { return "filter.action" }

// Canonical returns the string value of the action.
func (f ActionFact) Canonical() string { return string(f) }

// SetSnapshotFact wraps a rule set snapshot as a trace.Fact.
type SetSnapshotFact string

// TypeID returns the fact type identifier.
func (f SetSnapshotFact) TypeID() string { return "filter.set" }

// Canonical returns the serialized rule set state.
func (f SetSnapshotFact) Canonical() string { return string(f) }

// SnapshotSet returns an immutable snapshot of s.
func SnapshotSet(s RuleSet) trace.Fact {
	return SetSnapshotFact("stateful=" + strconv.FormatBool(s.Stateful) +
		";default=" + strconv.Quote(string(s.Default)) +
		";rule_count=" + strconv.Itoa(len(s.Rules)))
}

// RuleSnapshotFact wraps a rule definition as a trace.Fact.
type RuleSnapshotFact string

// TypeID returns the fact type identifier.
func (f RuleSnapshotFact) TypeID() string { return "filter.rule" }

// Canonical returns the serialized rule state.
func (f RuleSnapshotFact) Canonical() string { return string(f) }

// SnapshotRule returns an immutable, injective snapshot of r.
func SnapshotRule(r Rule) trace.Fact {
	var b strings.Builder
	b.WriteString("name=")
	b.WriteString(strconv.Quote(r.Name))
	b.WriteString(";action=")
	b.WriteString(strconv.Quote(string(r.Action)))
	b.WriteString(";proto=")
	if r.Match.Protocol != nil {
		b.WriteString(strconv.FormatUint(uint64(*r.Match.Protocol), 10))
	} else {
		b.WriteString("any")
	}
	b.WriteString(";src=[")
	for i, p := range r.Match.Src {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.String())
	}
	b.WriteString("];dst=[")
	for i, p := range r.Match.Dst {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.String())
	}
	b.WriteString("];src_ports=[")
	for i, pr := range r.Match.SrcPorts {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(int(pr.Start)))
		b.WriteByte('-')
		b.WriteString(strconv.Itoa(int(pr.End)))
	}
	b.WriteString("];dst_ports=[")
	for i, pr := range r.Match.DstPorts {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(int(pr.Start)))
		b.WriteByte('-')
		b.WriteString(strconv.Itoa(int(pr.End)))
	}
	b.WriteString("];icmp=")
	if r.Match.ICMP != nil {
		b.WriteString("type:")
		b.WriteString(strconv.Itoa(int(r.Match.ICMP.Type)))
		b.WriteString(",code:")
		if r.Match.ICMP.Code != nil {
			b.WriteString(strconv.Itoa(int(*r.Match.ICMP.Code)))
		} else {
			b.WriteString("any")
		}
	} else {
		b.WriteString("none")
	}
	b.WriteString(";tcp_flags=")
	if r.Match.TCPFlags != nil {
		b.WriteString("mask:")
		b.WriteString(strconv.FormatUint(uint64(r.Match.TCPFlags.Mask), 16))
		b.WriteString(",val:")
		b.WriteString(strconv.FormatUint(uint64(r.Match.TCPFlags.Value), 16))
	} else {
		b.WriteString("none")
	}
	return RuleSnapshotFact(b.String())
}

// BindingSnapshotFact wraps a binding destination set as a trace.Fact.
type BindingSnapshotFact string

// TypeID returns the fact type identifier.
func (f BindingSnapshotFact) TypeID() string { return "filter.binding" }

// Canonical returns the binding set representation.
func (f BindingSnapshotFact) Canonical() string { return string(f) }

// SnapshotBinding returns an immutable snapshot of a binding target set.
func SnapshotBinding(set string) trace.Fact {
	return BindingSnapshotFact("set=" + strconv.Quote(set))
}

// Diff compares two filter configurations and returns field-level changes.
func Diff(a, b Config) []trace.Change {
	a = a.Normalize()
	b = b.Normalize()

	var changes []trace.Change

	allSets := make(map[string]struct{})
	for k := range a.Sets {
		allSets[k] = struct{}{}
	}
	for k := range b.Sets {
		allSets[k] = struct{}{}
	}

	setNames := make([]string, 0, len(allSets))
	for k := range allSets {
		setNames = append(setNames, k)
	}
	slices.Sort(setNames)

	for _, setName := range setNames {
		setA, inA := a.Sets[setName]
		setB, inB := b.Sets[setName]
		setSubject := trace.Subject{Kind: "set", Key: setName}

		if !inA {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: setSubject,
				Field:   "",
				From:    nil,
				To:      SnapshotSet(setB),
			})
			for idx, r := range setB.Rules {
				ruleKey := r.Name
				if ruleKey == "" {
					ruleKey = strconv.Itoa(idx)
				}
				changes = append(changes, trace.Change{
					Layer:   LayerName,
					Subject: trace.Subject{Kind: "rule", Key: setName + "/" + ruleKey},
					Field:   "",
					From:    nil,
					To:      SnapshotRule(r),
				})
			}
			continue
		}

		if !inB {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: setSubject,
				Field:   "",
				From:    SnapshotSet(setA),
				To:      nil,
			})
			for idx, r := range setA.Rules {
				ruleKey := r.Name
				if ruleKey == "" {
					ruleKey = strconv.Itoa(idx)
				}
				changes = append(changes, trace.Change{
					Layer:   LayerName,
					Subject: trace.Subject{Kind: "rule", Key: setName + "/" + ruleKey},
					Field:   "",
					From:    SnapshotRule(r),
					To:      nil,
				})
			}
			continue
		}

		if setA.Stateful != setB.Stateful {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: setSubject,
				Field:   "stateful",
				From:    BoolFact(setA.Stateful),
				To:      BoolFact(setB.Stateful),
			})
		}
		if setA.Default != setB.Default {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: setSubject,
				Field:   "default",
				From:    ActionFact(setA.Default),
				To:      ActionFact(setB.Default),
			})
		}

		changes = diffRules(changes, setName, setA.Rules, setB.Rules)
	}

	type bindingKey struct {
		iface string
		dir   Direction
	}
	aBindings := make(map[bindingKey]string, len(a.Bindings))
	for _, bnd := range a.Bindings {
		aBindings[bindingKey{iface: bnd.Interface, dir: bnd.Direction}] = bnd.Set
	}
	bBindings := make(map[bindingKey]string, len(b.Bindings))
	for _, bnd := range b.Bindings {
		bBindings[bindingKey{iface: bnd.Interface, dir: bnd.Direction}] = bnd.Set
	}

	allBindings := make(map[bindingKey]struct{})
	for k := range aBindings {
		allBindings[k] = struct{}{}
	}
	for k := range bBindings {
		allBindings[k] = struct{}{}
	}

	bindingKeys := make([]bindingKey, 0, len(allBindings))
	for k := range allBindings {
		bindingKeys = append(bindingKeys, k)
	}
	slices.SortFunc(bindingKeys, func(x, y bindingKey) int {
		if c := cmp.Compare(x.iface, y.iface); c != 0 {
			return c
		}
		return cmp.Compare(x.dir, y.dir)
	})

	for _, k := range bindingKeys {
		setA, inA := aBindings[k]
		setB, inB := bBindings[k]
		bindingKeyStr := fmt.Sprintf("%s/%s", k.iface, k.dir)
		bindingSubject := trace.Subject{Kind: "binding", Key: bindingKeyStr}

		if !inA {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: bindingSubject,
				Field:   "",
				From:    nil,
				To:      SnapshotBinding(setB),
			})
			continue
		}
		if !inB {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: bindingSubject,
				Field:   "",
				From:    SnapshotBinding(setA),
				To:      nil,
			})
			continue
		}
		if setA != setB {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: bindingSubject,
				Field:   "set",
				From:    SnapshotBinding(setA),
				To:      SnapshotBinding(setB),
			})
		}
	}

	slices.SortFunc(changes, func(x, y trace.Change) int {
		if c := x.Subject.Compare(y.Subject); c != 0 {
			return c
		}
		if c := cmp.Compare(x.Field, y.Field); c != 0 {
			return c
		}
		return trace.CompareFact(x.To, y.To)
	})

	return changes
}

func diffRules(changes []trace.Change, setName string, rulesA, rulesB []Rule) []trace.Change {
	rulesMapA := indexRules(rulesA)
	rulesMapB := indexRules(rulesB)

	allRuleKeys := make(map[string]struct{})
	for k := range rulesMapA {
		allRuleKeys[k] = struct{}{}
	}
	for k := range rulesMapB {
		allRuleKeys[k] = struct{}{}
	}

	keys := make([]string, 0, len(allRuleKeys))
	for k := range allRuleKeys {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	for _, k := range keys {
		rA, inA := rulesMapA[k]
		rB, inB := rulesMapB[k]
		subject := trace.Subject{Kind: "rule", Key: setName + "/" + k}

		if !inA {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: subject,
				Field:   "",
				From:    nil,
				To:      SnapshotRule(rB),
			})
			continue
		}
		if !inB {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: subject,
				Field:   "",
				From:    SnapshotRule(rA),
				To:      nil,
			})
			continue
		}
		if !rA.Equal(rB) {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: subject,
				Field:   "",
				From:    SnapshotRule(rA),
				To:      SnapshotRule(rB),
			})
		}
	}

	return changes
}

func indexRules(rules []Rule) map[string]Rule {
	out := make(map[string]Rule, len(rules))
	seenNames := make(map[string]int, len(rules))

	for _, r := range rules {
		if r.Name != "" {
			seenNames[r.Name]++
		}
	}

	for idx, r := range rules {
		key := r.Name
		if key == "" || seenNames[key] > 1 {
			key = strconv.Itoa(idx)
		}
		out[key] = r
	}

	return out
}
