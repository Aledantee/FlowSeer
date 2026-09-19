package filter

import (
	"net/netip"
	"slices"
	"strconv"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/icmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/tcp"
	"go.aledante.io/FlowSeer/src/common/net/udp"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

const (
	// LayerName identifies the filter layer in trace steps.
	LayerName trace.Layer = "filter"

	// RuleAccept identifies a decision permitting traffic by rule match.
	RuleAccept trace.RuleID = "filter.accept"
	// RuleDrop identifies a decision dropping traffic by rule match.
	RuleDrop trace.RuleID = "filter.drop"
	// RuleReject identifies a decision rejecting traffic by rule match.
	RuleReject trace.RuleID = "filter.reject"
	// RuleDefault identifies a decision applying a set's default action.
	RuleDefault trace.RuleID = "filter.default"
	// RuleState identifies a stateful accept matching the counterpart forward rule.
	RuleState trace.RuleID = "filter.state"

	// ReasonFilterDrop indicates a frame discarded by filter policy.
	ReasonFilterDrop trace.Reason = "filter-drop"
	// ReasonFilterReject indicates a frame rejected by filter policy.
	ReasonFilterReject trace.Reason = "filter-reject"
)

type ruleDecisionFact string

func (f ruleDecisionFact) TypeID() string    { return "filter.rule_decision" }
func (f ruleDecisionFact) Canonical() string { return string(f) }

// RuleDecisionFact returns an immutable snapshot of a filter rule decision.
func RuleDecisionFact(set, rule string, action Action, dir Direction, iface string) trace.Fact {
	return ruleDecisionFact("set=" + strconv.Quote(set) +
		";rule=" + strconv.Quote(rule) +
		";action=" + strconv.Quote(string(action)) +
		";direction=" + strconv.Quote(string(dir)) +
		";interface=" + strconv.Quote(iface))
}

type matchFact string

func (f matchFact) TypeID() string    { return "filter.match" }
func (f matchFact) Canonical() string { return string(f) }

// MatchFact returns an immutable snapshot of the 5-tuple consulted by a filter.
func MatchFact(proto uint8, src, dst netip.Addr, srcPort, dstPort uint16) trace.Fact {
	return matchFact("proto=" + strconv.FormatUint(uint64(proto), 10) +
		";src=" + strconv.Quote(src.String()) +
		";dst=" + strconv.Quote(dst.String()) +
		";src_port=" + strconv.FormatUint(uint64(srcPort), 10) +
		";dst_port=" + strconv.FormatUint(uint64(dstPort), 10))
}

func rootScope(nodeID string) analysis.Scope {
	return analysis.ProtocolScope(nodeID, "filter", "")
}

// Scope returns the evaluation scope for a filter binding on iface and direction.
func Scope(nodeID, iface string, dir Direction) analysis.Scope {
	return analysis.FieldScope(rootScope(nodeID), "interfaces", iface, string(dir))
}

// BindingScope returns the evaluation scope for a filter binding on iface and direction.
func BindingScope(nodeID, iface string, dir Direction) analysis.Scope {
	return Scope(nodeID, iface, dir)
}

// Tuple captures the layer 3 and layer 4 5-tuple used for matching and stateful inspection.
type Tuple struct {
	Proto   uint8
	Src     netip.Addr
	Dst     netip.Addr
	SrcPort uint16
	DstPort uint16
}

// Reverse returns the inverted 5-tuple with source and destination addresses and ports swapped.
func (t Tuple) Reverse() Tuple {
	return Tuple{
		Proto:   t.Proto,
		Src:     t.Dst,
		Dst:     t.Src,
		SrcPort: t.DstPort,
		DstPort: t.SrcPort,
	}
}

// Decision represents the outcome of a filter evaluation.
type Decision string

const (
	// DecisionAccept allows the frame to proceed.
	DecisionAccept Decision = "accept"
	// DecisionDrop discards the frame silently.
	DecisionDrop Decision = "drop"
	// DecisionReject discards the frame with a rejection reason.
	DecisionReject Decision = "reject"
	// DecisionDeferred indicates evaluation is deferred pending egress resolution.
	DecisionDeferred Decision = "deferred"
)

// Result records the filter verdict, emitted trace steps, and consulted metadata scopes.
type Result struct {
	Decision        Decision
	Action          Action
	Reason          trace.Reason
	Steps           []trace.Step
	consultedScopes []analysis.Scope
	tuple           Tuple
	deferredIface   string
	deferredSet     string
}

// ConsultedScopes returns the exact analysis scopes whose facts could change this result.
func (r Result) ConsultedScopes() []analysis.Scope {
	return slices.Clone(r.consultedScopes)
}

// Status returns Complete: a filter decision is definite.
func (r Result) Status() analysis.Status {
	return analysis.Complete
}

// Outcome converts the decision to a trace Outcome.
func (r Result) Outcome() trace.Outcome {
	switch r.Decision {
	case DecisionAccept:
		return trace.Forwarded
	case DecisionDrop, DecisionReject:
		return trace.Dropped
	default:
		return ""
	}
}

func (r *Result) consult(scope analysis.Scope) {
	r.consultedScopes = append(r.consultedScopes, scope)
	slices.SortFunc(r.consultedScopes, func(a, b analysis.Scope) int { return a.Compare(b) })
	r.consultedScopes = slices.CompactFunc(r.consultedScopes, func(a, b analysis.Scope) bool { return a.Compare(b) == 0 })
}

type bindingKey struct {
	iface string
	dir   Direction
}

// Layer evaluates interface filter sets against frames passing through the virtual switch.
type Layer struct {
	nodeID   string
	cfg      Config
	sets     map[string]RuleSet
	bindings map[bindingKey]string
}

// New constructs a new filter layer from cfg.
func New(cfg Config, _ port.Table, nodeID string) (*Layer, error) {
	norm := cfg.Normalize()
	if err := norm.Validate(); err != nil {
		return nil, err
	}

	l := &Layer{
		nodeID:   nodeID,
		cfg:      norm,
		sets:     norm.Sets,
		bindings: make(map[bindingKey]string, len(norm.Bindings)),
	}

	for _, b := range norm.Bindings {
		l.bindings[bindingKey{iface: b.Interface, dir: b.Direction}] = b.Set
	}

	return l, nil
}

// Binding returns the rule set bound to iface in direction dir, if any.
func (l *Layer) Binding(iface string, dir Direction) (string, bool) {
	set, ok := l.bindings[bindingKey{iface: iface, dir: dir}]
	return set, ok
}

// HasBinding reports whether a rule set is bound to iface in direction dir.
func (l *Layer) HasBinding(iface string, dir Direction) bool {
	_, ok := l.bindings[bindingKey{iface: iface, dir: dir}]
	return ok
}

// Config returns a copy of the layer's configuration.
func (l *Layer) Config() Config {
	return l.cfg.Clone()
}

// EvaluateIngress tests an incoming frame on interface iface against its ingress filter binding.
func (l *Layer) EvaluateIngress(iface string, f ethernet.Frame) Result {
	setName, ok := l.Binding(iface, In)
	if !ok {
		return Result{Decision: DecisionAccept, Action: Accept}
	}

	scope := Scope(l.nodeID, iface, In)
	var res Result
	res.consult(scope)

	set, ok := l.sets[setName]
	if !ok {
		return Result{Decision: DecisionAccept, Action: Accept}
	}

	hdr, tuple, icmpHdr, tcpHdr, decoded := extractPacket(f)
	matchF := MatchFact(tuple.Proto, tuple.Src, tuple.Dst, tuple.SrcPort, tuple.DstPort)

	for idx, rule := range set.Rules {
		if ruleMatches(rule.Match, decoded, hdr, tuple, icmpHdr, tcpHdr) {
			ruleKey := rule.Name
			if ruleKey == "" {
				ruleKey = strconv.Itoa(idx)
			}
			decFact := RuleDecisionFact(setName, ruleKey, rule.Action, In, iface)
			ruleID := ruleIDForAction(rule.Action)
			op := opForAction(rule.Action)
			step := trace.Step{
				Layer:   LayerName,
				Op:      op,
				RuleID:  ruleID,
				Subject: trace.Subject{Kind: "interface", Key: iface},
				Inputs:  []trace.Fact{matchF},
				Outputs: []trace.Fact{decFact},
			}
			res.Steps = []trace.Step{step}
			res.Action = rule.Action
			switch rule.Action {
			case Accept:
				res.Decision = DecisionAccept
			case Drop:
				res.Decision = DecisionDrop
				res.Reason = ReasonFilterDrop
			case Reject:
				res.Decision = DecisionReject
				res.Reason = ReasonFilterReject
			}
			return res
		}
	}

	if set.Stateful {
		res.Decision = DecisionDeferred
		res.Action = set.Default
		res.tuple = tuple
		res.deferredIface = iface
		res.deferredSet = setName
		return res
	}

	ruleID := RuleDefault
	op := opForAction(set.Default)
	decFact := RuleDecisionFact(setName, "default", set.Default, In, iface)
	step := trace.Step{
		Layer:   LayerName,
		Op:      op,
		RuleID:  ruleID,
		Subject: trace.Subject{Kind: "interface", Key: iface},
		Inputs:  []trace.Fact{matchF},
		Outputs: []trace.Fact{decFact},
	}
	res.Steps = []trace.Step{step}
	res.Action = set.Default
	switch set.Default {
	case Accept:
		res.Decision = DecisionAccept
	case Drop:
		res.Decision = DecisionDrop
		res.Reason = ReasonFilterDrop
	case Reject:
		res.Decision = DecisionReject
		res.Reason = ReasonFilterReject
	}
	return res
}

// ResolveDeferred resolves a deferred ingress decision against the counterpart interface egressIface.
func (l *Layer) ResolveDeferred(ingressRes Result, egressIface string) Result {
	if ingressRes.Decision != DecisionDeferred {
		return ingressRes
	}

	res := ingressRes
	ingressIface := ingressRes.deferredIface
	ingressSetName := ingressRes.deferredSet
	ingressSet := l.sets[ingressSetName]
	tuple := ingressRes.tuple

	egressScope := Scope(l.nodeID, egressIface, In)
	res.consult(egressScope)

	matchF := MatchFact(tuple.Proto, tuple.Src, tuple.Dst, tuple.SrcPort, tuple.DstPort)

	if egressSetName, ok := l.Binding(egressIface, In); ok {
		egressSet, ok := l.sets[egressSetName]
		if ok && egressSet.Stateful {
			revTuple := tuple.Reverse()
			for idx, rule := range egressSet.Rules {
				if !tupleMatches(rule.Match, revTuple) {
					continue
				}
				if rule.Action != Accept {
					if rule.Match.ICMP != nil || rule.Match.TCPFlags != nil {
						continue
					}
					break
				}
				fwdRuleKey := rule.Name
				if fwdRuleKey == "" {
					fwdRuleKey = strconv.Itoa(idx)
				}
				fwdDecFact := RuleDecisionFact(egressSetName, fwdRuleKey, Accept, In, egressIface)
				stateDecFact := RuleDecisionFact(ingressSetName, "state", Accept, In, ingressIface)
				step := trace.Step{
					Layer:   LayerName,
					Op:      trace.OpFilter,
					RuleID:  RuleState,
					Subject: trace.Subject{Kind: "interface", Key: ingressIface},
					Inputs:  []trace.Fact{matchF, fwdDecFact},
					Outputs: []trace.Fact{stateDecFact},
				}
				res.Decision = DecisionAccept
				res.Action = Accept
				res.Reason = ""
				res.Steps = []trace.Step{step}
				return res
			}
		}
	}

	ruleID := RuleDefault
	op := opForAction(ingressSet.Default)
	decFact := RuleDecisionFact(ingressSetName, "default", ingressSet.Default, In, ingressIface)
	step := trace.Step{
		Layer:   LayerName,
		Op:      op,
		RuleID:  ruleID,
		Subject: trace.Subject{Kind: "interface", Key: ingressIface},
		Inputs:  []trace.Fact{matchF},
		Outputs: []trace.Fact{decFact},
	}
	res.Steps = []trace.Step{step}
	res.Action = ingressSet.Default
	switch ingressSet.Default {
	case Accept:
		res.Decision = DecisionAccept
		res.Reason = ""
	case Drop:
		res.Decision = DecisionDrop
		res.Reason = ReasonFilterDrop
	case Reject:
		res.Decision = DecisionReject
		res.Reason = ReasonFilterReject
	}
	return res
}

// EvaluateEgress tests a forwarded frame on interface egressIface against its egress filter binding.
func (l *Layer) EvaluateEgress(egressIface, ingressIface string, f ethernet.Frame) Result {
	setName, ok := l.Binding(egressIface, Out)
	if !ok {
		return Result{Decision: DecisionAccept, Action: Accept}
	}

	scope := Scope(l.nodeID, egressIface, Out)
	var res Result
	res.consult(scope)

	set, ok := l.sets[setName]
	if !ok {
		return Result{Decision: DecisionAccept, Action: Accept}
	}

	hdr, tuple, icmpHdr, tcpHdr, decoded := extractPacket(f)
	matchF := MatchFact(tuple.Proto, tuple.Src, tuple.Dst, tuple.SrcPort, tuple.DstPort)

	for idx, rule := range set.Rules {
		if ruleMatches(rule.Match, decoded, hdr, tuple, icmpHdr, tcpHdr) {
			ruleKey := rule.Name
			if ruleKey == "" {
				ruleKey = strconv.Itoa(idx)
			}
			decFact := RuleDecisionFact(setName, ruleKey, rule.Action, Out, egressIface)
			ruleID := ruleIDForAction(rule.Action)
			op := opForAction(rule.Action)
			step := trace.Step{
				Layer:   LayerName,
				Op:      op,
				RuleID:  ruleID,
				Subject: trace.Subject{Kind: "interface", Key: egressIface},
				Inputs:  []trace.Fact{matchF},
				Outputs: []trace.Fact{decFact},
			}
			res.Steps = []trace.Step{step}
			res.Action = rule.Action
			switch rule.Action {
			case Accept:
				res.Decision = DecisionAccept
			case Drop:
				res.Decision = DecisionDrop
				res.Reason = ReasonFilterDrop
			case Reject:
				res.Decision = DecisionReject
				res.Reason = ReasonFilterReject
			}
			return res
		}
	}

	if set.Stateful {
		ingressScope := Scope(l.nodeID, ingressIface, Out)
		res.consult(ingressScope)

		if ingressSetName, ok := l.Binding(ingressIface, Out); ok {
			ingressSet, ok := l.sets[ingressSetName]
			if ok && ingressSet.Stateful {
				revTuple := tuple.Reverse()
				for idx, rule := range ingressSet.Rules {
					if !tupleMatches(rule.Match, revTuple) {
						continue
					}
					if rule.Action != Accept {
						if rule.Match.ICMP != nil || rule.Match.TCPFlags != nil {
							continue
						}
						break
					}
					fwdRuleKey := rule.Name
					if fwdRuleKey == "" {
						fwdRuleKey = strconv.Itoa(idx)
					}
					fwdDecFact := RuleDecisionFact(ingressSetName, fwdRuleKey, Accept, Out, ingressIface)
					stateDecFact := RuleDecisionFact(setName, "state", Accept, Out, egressIface)
					step := trace.Step{
						Layer:   LayerName,
						Op:      trace.OpFilter,
						RuleID:  RuleState,
						Subject: trace.Subject{Kind: "interface", Key: egressIface},
						Inputs:  []trace.Fact{matchF, fwdDecFact},
						Outputs: []trace.Fact{stateDecFact},
					}
					res.Decision = DecisionAccept
					res.Action = Accept
					res.Reason = ""
					res.Steps = []trace.Step{step}
					return res
				}
			}
		}
	}

	ruleID := RuleDefault
	op := opForAction(set.Default)
	decFact := RuleDecisionFact(setName, "default", set.Default, Out, egressIface)
	step := trace.Step{
		Layer:   LayerName,
		Op:      op,
		RuleID:  ruleID,
		Subject: trace.Subject{Kind: "interface", Key: egressIface},
		Inputs:  []trace.Fact{matchF},
		Outputs: []trace.Fact{decFact},
	}
	res.Steps = []trace.Step{step}
	res.Action = set.Default
	switch set.Default {
	case Accept:
		res.Decision = DecisionAccept
	case Drop:
		res.Decision = DecisionDrop
		res.Reason = ReasonFilterDrop
	case Reject:
		res.Decision = DecisionReject
		res.Reason = ReasonFilterReject
	}
	return res
}

func extractPacket(f ethernet.Frame) (ip.Header, Tuple, *icmp.Header, *tcp.Header, bool) {
	hdr, payload, err := ip.Decode(f.Payload)
	if err != nil {
		return ip.Header{}, Tuple{}, nil, nil, false
	}

	tuple := Tuple{
		Proto: hdr.Protocol,
		Src:   hdr.Src,
		Dst:   hdr.Dst,
	}

	var icmpHdr *icmp.Header
	var tcpHdr *tcp.Header

	switch hdr.Protocol {
	case 6: // TCP
		if th, _, err := tcp.Decode(payload); err == nil {
			tcpHdr = &th
			tuple.SrcPort = th.SrcPort
			tuple.DstPort = th.DstPort
		}
	case 17: // UDP
		if uh, _, err := udp.Decode(payload); err == nil {
			tuple.SrcPort = uh.SrcPort
			tuple.DstPort = uh.DstPort
		}
	case 1, 58: // ICMPv4, ICMPv6
		if ih, _, err := icmp.Decode(payload); err == nil {
			icmpHdr = &ih
		}
	}

	return hdr, tuple, icmpHdr, tcpHdr, true
}

func ruleMatches(m Match, decoded bool, hdr ip.Header, tuple Tuple, icmpHdr *icmp.Header, tcpHdr *tcp.Header) bool {
	if !decoded {
		return m.Protocol == nil && len(m.Src) == 0 && len(m.Dst) == 0 &&
			len(m.SrcPorts) == 0 && len(m.DstPorts) == 0 &&
			m.ICMP == nil && m.TCPFlags == nil
	}

	if m.Protocol != nil && *m.Protocol != hdr.Protocol {
		return false
	}

	if len(m.Src) > 0 {
		matched := false
		for _, p := range m.Src {
			if p.Contains(hdr.Src) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if len(m.Dst) > 0 {
		matched := false
		for _, p := range m.Dst {
			if p.Contains(hdr.Dst) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if len(m.SrcPorts) > 0 {
		if hdr.Protocol != 6 && hdr.Protocol != 17 {
			return false
		}
		matched := false
		for _, pr := range m.SrcPorts {
			if pr.Contains(tuple.SrcPort) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if len(m.DstPorts) > 0 {
		if hdr.Protocol != 6 && hdr.Protocol != 17 {
			return false
		}
		matched := false
		for _, pr := range m.DstPorts {
			if pr.Contains(tuple.DstPort) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if m.ICMP != nil {
		if (hdr.Protocol != 1 && hdr.Protocol != 58) || icmpHdr == nil {
			return false
		}
		if !m.ICMP.Matches(icmpHdr.Type, icmpHdr.Code) {
			return false
		}
	}

	if m.TCPFlags != nil {
		if hdr.Protocol != 6 || tcpHdr == nil {
			return false
		}
		if !m.TCPFlags.Matches(tcpHdr.Flags) {
			return false
		}
	}

	return true
}

func tupleMatches(m Match, tuple Tuple) bool {
	if m.Protocol != nil && *m.Protocol != tuple.Proto {
		return false
	}

	if len(m.Src) > 0 {
		matched := false
		for _, p := range m.Src {
			if p.Contains(tuple.Src) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if len(m.Dst) > 0 {
		matched := false
		for _, p := range m.Dst {
			if p.Contains(tuple.Dst) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if len(m.SrcPorts) > 0 {
		if tuple.Proto != 6 && tuple.Proto != 17 {
			return false
		}
		matched := false
		for _, pr := range m.SrcPorts {
			if pr.Contains(tuple.SrcPort) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if len(m.DstPorts) > 0 {
		if tuple.Proto != 6 && tuple.Proto != 17 {
			return false
		}
		matched := false
		for _, pr := range m.DstPorts {
			if pr.Contains(tuple.DstPort) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

func ruleIDForAction(a Action) trace.RuleID {
	switch a {
	case Accept:
		return RuleAccept
	case Drop:
		return RuleDrop
	case Reject:
		return RuleReject
	default:
		return RuleDefault
	}
}

func opForAction(a Action) trace.Op {
	switch a {
	case Accept:
		return trace.OpFilter
	case Drop, Reject:
		return trace.OpDrop
	default:
		return trace.OpFilter
	}
}
