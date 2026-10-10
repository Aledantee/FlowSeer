package filter

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/net/tcp"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// boolFact wraps a boolean configuration property as a trace.Fact.
type boolFact bool

// TypeID returns the fact type identifier.
func (f boolFact) TypeID() string { return "filter.bool" }

// Canonical returns "true" or "false".
func (f boolFact) Canonical() string { return strconv.FormatBool(bool(f)) }

// actionFact wraps a filter Action as a trace.Fact.
type actionFact Action

// TypeID returns the fact type identifier.
func (f actionFact) TypeID() string { return "filter.action" }

// Canonical returns the string value of the action.
func (f actionFact) Canonical() string { return string(f) }

// setSnapshotFact wraps a rule set snapshot as a trace.Fact.
type setSnapshotFact string

// TypeID returns the fact type identifier.
func (f setSnapshotFact) TypeID() string { return "filter.set" }

// Canonical returns the serialized rule set state.
func (f setSnapshotFact) Canonical() string { return string(f) }

// snapshotSet returns an immutable snapshot of s.
func snapshotSet(s RuleSet) trace.Fact {
	return setSnapshotFact("stateful=" + strconv.FormatBool(s.Stateful) +
		";default=" + strconv.Quote(string(s.Default)) +
		";rule_count=" + strconv.Itoa(len(s.Rules)))
}

// ruleSnapshotFact wraps a rule definition as a trace.Fact.
type ruleSnapshotFact string

// TypeID returns the fact type identifier.
func (f ruleSnapshotFact) TypeID() string { return "filter.rule" }

// Canonical returns the serialized rule state.
func (f ruleSnapshotFact) Canonical() string { return string(f) }

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
	return ruleSnapshotFact(b.String())
}

// bindingSnapshotFact wraps a binding destination set as a trace.Fact.
type bindingSnapshotFact string

// TypeID returns the fact type identifier.
func (f bindingSnapshotFact) TypeID() string { return "filter.binding" }

// Canonical returns the binding set representation.
func (f bindingSnapshotFact) Canonical() string { return string(f) }

// snapshotBinding returns an immutable snapshot of a binding target set.
func snapshotBinding(set string) trace.Fact {
	return bindingSnapshotFact("set=" + strconv.Quote(set))
}

// Diff compares two filter configurations and returns field-level changes.
func Diff(a, b Config) []trace.Change {
	a = a.Normalize(layer.Env{})
	b = b.Normalize(layer.Env{})

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
				To:      snapshotSet(setB),
			})
			for ruleKey, r := range indexRules(setName, setB.Rules) {
				changes = append(changes, trace.Change{
					Layer:   LayerName,
					Subject: trace.Subject{Kind: "rule", Key: ruleKey},
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
				From:    snapshotSet(setA),
				To:      nil,
			})
			for ruleKey, r := range indexRules(setName, setA.Rules) {
				changes = append(changes, trace.Change{
					Layer:   LayerName,
					Subject: trace.Subject{Kind: "rule", Key: ruleKey},
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
				From:    boolFact(setA.Stateful),
				To:      boolFact(setB.Stateful),
			})
		}
		if setA.Default != setB.Default {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: setSubject,
				Field:   "default",
				From:    actionFact(setA.Default),
				To:      actionFact(setB.Default),
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
		bindingKeyStr := trace.CompositeKey(k.iface, string(k.dir))
		bindingSubject := trace.Subject{Kind: "binding", Key: bindingKeyStr}

		if !inA {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: bindingSubject,
				Field:   "",
				From:    nil,
				To:      snapshotBinding(setB),
			})
			continue
		}
		if !inB {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: bindingSubject,
				Field:   "",
				From:    snapshotBinding(setA),
				To:      nil,
			})
			continue
		}
		if setA != setB {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: bindingSubject,
				Field:   "set",
				From:    snapshotBinding(setA),
				To:      snapshotBinding(setB),
			})
		}
	}

	slices.SortStableFunc(changes, func(x, y trace.Change) int {
		return x.Subject.Compare(y.Subject)
	})

	return changes
}

func diffRules(changes []trace.Change, setName string, rulesA, rulesB []Rule) []trace.Change {
	rulesMapA := indexRules(setName, rulesA)
	rulesMapB := indexRules(setName, rulesB)

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
		subject := trace.Subject{Kind: "rule", Key: k}

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
		add := func(field string, from, to trace.Fact) {
			if trace.CompareFact(from, to) != 0 {
				changes = append(changes, trace.Change{Layer: LayerName, Subject: subject, Field: field, From: from, To: to})
			}
		}
		add("action", actionFact(rA.Action), actionFact(rB.Action))
		var protocolA, protocolB trace.Fact
		if rA.Match.Protocol != nil {
			protocolA = protocolFact(*rA.Match.Protocol)
		}
		if rB.Match.Protocol != nil {
			protocolB = protocolFact(*rB.Match.Protocol)
		}
		add("match.protocol", protocolA, protocolB)
		for _, list := range []struct {
			field string
			a, b  []netip.Prefix
		}{
			{"match.src", rA.Match.Src, rB.Match.Src},
			{"match.dst", rA.Match.Dst, rB.Match.Dst},
		} {
			for _, p := range list.a {
				if !slices.Contains(list.b, p) {
					add(list.field+"["+strconv.Quote(p.String())+"]", prefixFact(p), nil)
				}
			}
			for _, p := range list.b {
				if !slices.Contains(list.a, p) {
					add(list.field+"["+strconv.Quote(p.String())+"]", nil, prefixFact(p))
				}
			}
		}
		for _, list := range []struct {
			field string
			a, b  []PortRange
		}{
			{"match.src_ports", rA.Match.SrcPorts, rB.Match.SrcPorts},
			{"match.dst_ports", rA.Match.DstPorts, rB.Match.DstPorts},
		} {
			for i := 0; i < max(len(list.a), len(list.b)); i++ {
				var startA, endA, startB, endB trace.Fact
				if i < len(list.a) {
					startA, endA = portFact(list.a[i].Start), portFact(list.a[i].End)
				}
				if i < len(list.b) {
					startB, endB = portFact(list.b[i].Start), portFact(list.b[i].End)
				}
				field := list.field + "[" + strconv.Itoa(i) + "]"
				add(field+".start", startA, startB)
				add(field+".end", endA, endB)
			}
		}
		var typeA, codeA, typeB, codeB trace.Fact
		if rA.Match.ICMP != nil {
			typeA = icmpTypeFact(rA.Match.ICMP.Type)
			if rA.Match.ICMP.Code != nil {
				codeA = icmpCodeFact(*rA.Match.ICMP.Code)
			}
		}
		if rB.Match.ICMP != nil {
			typeB = icmpTypeFact(rB.Match.ICMP.Type)
			if rB.Match.ICMP.Code != nil {
				codeB = icmpCodeFact(*rB.Match.ICMP.Code)
			}
		}
		add("match.icmp.type", typeA, typeB)
		add("match.icmp.code", codeA, codeB)
		var maskA, valueA, maskB, valueB trace.Fact
		if rA.Match.TCPFlags != nil {
			maskA, valueA = tcpFlagsFact(rA.Match.TCPFlags.Mask), tcpFlagsFact(rA.Match.TCPFlags.Value)
		}
		if rB.Match.TCPFlags != nil {
			maskB, valueB = tcpFlagsFact(rB.Match.TCPFlags.Mask), tcpFlagsFact(rB.Match.TCPFlags.Value)
		}
		add("match.tcp_flags.mask", maskA, maskB)
		add("match.tcp_flags.value", valueA, valueB)
	}

	return changes
}

type protocolFact uint8

func (f protocolFact) TypeID() string    { return "filter.protocol" }
func (f protocolFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

type prefixFact netip.Prefix

func (f prefixFact) TypeID() string    { return "filter.prefix" }
func (f prefixFact) Canonical() string { return netip.Prefix(f).String() }

type portFact uint16

func (f portFact) TypeID() string    { return "filter.port" }
func (f portFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

type icmpTypeFact uint8

func (f icmpTypeFact) TypeID() string    { return "filter.icmp_type" }
func (f icmpTypeFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

type icmpCodeFact uint8

func (f icmpCodeFact) TypeID() string    { return "filter.icmp_code" }
func (f icmpCodeFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

type tcpFlagsFact tcp.Flags

func (f tcpFlagsFact) TypeID() string    { return "filter.tcp_flags" }
func (f tcpFlagsFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

func indexRules(setName string, rules []Rule) map[string]Rule {
	out := make(map[string]Rule, len(rules))
	seenNames := make(map[string]int, len(rules))

	for _, r := range rules {
		if r.Name != "" {
			seenNames[r.Name]++
		}
	}

	for idx, r := range rules {
		key := trace.CompositeKey(setName, r.Name)
		if r.Name == "" || seenNames[r.Name] > 1 {
			key = trace.CompositeKey(setName, "", strconv.Itoa(idx))
		}
		out[key] = r
	}

	return out
}
