package filter_test

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/tcp"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/filter"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func makeTCPFrame(t *testing.T, srcIP, dstIP netip.Addr, srcPort, dstPort uint16, flags tcp.Flags) ethernet.Frame {
	t.Helper()
	seg := make([]byte, 20)
	binary.BigEndian.PutUint16(seg[0:2], srcPort)
	binary.BigEndian.PutUint16(seg[2:4], dstPort)
	seg[12] = 0x50
	seg[13] = byte(flags)
	hdr := ip.Header{
		Src:      srcIP,
		Dst:      dstIP,
		HopLimit: 64,
		Protocol: 6,
		V4:       &ip.V4{},
	}
	pkt, err := hdr.Encode(seg)
	if err != nil {
		t.Fatalf("encode TCP packet: %v", err)
	}
	return ethernet.Frame{
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}
}

func makeUDPFrame(t *testing.T, srcIP, dstIP netip.Addr, srcPort, dstPort uint16) ethernet.Frame {
	t.Helper()
	dgram := make([]byte, 8)
	binary.BigEndian.PutUint16(dgram[0:2], srcPort)
	binary.BigEndian.PutUint16(dgram[2:4], dstPort)
	binary.BigEndian.PutUint16(dgram[4:6], 8)
	hdr := ip.Header{
		Src:      srcIP,
		Dst:      dstIP,
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}
	pkt, err := hdr.Encode(dgram)
	if err != nil {
		t.Fatalf("encode UDP packet: %v", err)
	}
	return ethernet.Frame{
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}
}

func TestEvaluateFirstMatch(t *testing.T) {
	protoUDP := uint8(17)
	protoTCP := uint8(6)

	cfg := filter.Config{
		Sets: map[string]filter.RuleSet{
			"test-set": {
				Stateful: false,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "drop-mdns",
						Action: filter.Drop,
						Match: filter.Match{
							Protocol: &protoUDP,
							DstPorts: []filter.PortRange{{Start: 5353, End: 5353}},
						},
					},
					{
						Name:   "reject-http",
						Action: filter.Reject,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 80, End: 80}},
						},
					},
					{
						Name:   "accept-https",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 443, End: 443}},
						},
					},
				},
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "test-set"},
		},
	}

	l, err := filter.New(cfg, port.Table{}, "sw1")
	if err != nil {
		t.Fatalf("filter.New error = %v", err)
	}

	srcIP := netip.MustParseAddr("10.0.10.5")
	dstIP := netip.MustParseAddr("10.0.20.5")

	// 1. Drop rule match
	fUDP := makeUDPFrame(t, srcIP, dstIP, 50000, 5353)
	resUDP := l.EvaluateIngress("vlan10", fUDP)
	if resUDP.Decision != filter.DecisionDrop {
		t.Errorf("UDP 5353 decision = %v, want drop", resUDP.Decision)
	}
	if resUDP.Reason != filter.ReasonFilterDrop {
		t.Errorf("UDP 5353 reason = %v, want %v", resUDP.Reason, filter.ReasonFilterDrop)
	}
	if len(resUDP.Steps) != 1 || resUDP.Steps[0].RuleID != filter.RuleDrop {
		t.Errorf("UDP 5353 rule ID = %v, want %v", resUDP.Steps[0].RuleID, filter.RuleDrop)
	}

	// 2. Reject rule match
	fHTTP := makeTCPFrame(t, srcIP, dstIP, 50000, 80, tcp.SYN)
	resHTTP := l.EvaluateIngress("vlan10", fHTTP)
	if resHTTP.Decision != filter.DecisionReject {
		t.Errorf("TCP 80 decision = %v, want reject", resHTTP.Decision)
	}
	if resHTTP.Reason != filter.ReasonFilterReject {
		t.Errorf("TCP 80 reason = %v, want %v", resHTTP.Reason, filter.ReasonFilterReject)
	}
	if len(resHTTP.Steps) != 1 || resHTTP.Steps[0].RuleID != filter.RuleReject {
		t.Errorf("TCP 80 rule ID = %v, want %v", resHTTP.Steps[0].RuleID, filter.RuleReject)
	}

	// 3. Accept rule match
	fHTTPS := makeTCPFrame(t, srcIP, dstIP, 50000, 443, tcp.SYN)
	resHTTPS := l.EvaluateIngress("vlan10", fHTTPS)
	if resHTTPS.Decision != filter.DecisionAccept {
		t.Errorf("TCP 443 decision = %v, want accept", resHTTPS.Decision)
	}
	if len(resHTTPS.Steps) != 1 || resHTTPS.Steps[0].RuleID != filter.RuleAccept {
		t.Errorf("TCP 443 rule ID = %v, want %v", resHTTPS.Steps[0].RuleID, filter.RuleAccept)
	}

	// 4. Default action (drop)
	fSSH := makeTCPFrame(t, srcIP, dstIP, 50000, 22, tcp.SYN)
	resSSH := l.EvaluateIngress("vlan10", fSSH)
	if resSSH.Decision != filter.DecisionDrop {
		t.Errorf("TCP 22 decision = %v, want drop", resSSH.Decision)
	}
	if len(resSSH.Steps) != 1 || resSSH.Steps[0].RuleID != filter.RuleDefault {
		t.Errorf("TCP 22 rule ID = %v, want %v", resSSH.Steps[0].RuleID, filter.RuleDefault)
	}
}

func TestStatefulReplyReverseMatchAcceptsAndNamesForwardRule(t *testing.T) {
	protoTCP := uint8(6)

	cfg := filter.Config{
		Sets: map[string]filter.RuleSet{
			"lan-in": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "allow-lan-to-srv-https",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							Src:      []netip.Prefix{netip.MustParsePrefix("10.0.10.0/24")},
							Dst:      []netip.Prefix{netip.MustParsePrefix("10.0.20.0/24")},
							DstPorts: []filter.PortRange{{Start: 443, End: 443}},
						},
					},
				},
			},
			"srv-in": {
				Stateful: true,
				Default:  filter.Drop,
				Rules:    []filter.Rule{},
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "lan-in"},
			{Interface: "vlan20", Direction: filter.In, Set: "srv-in"},
		},
	}

	l, err := filter.New(cfg, port.Table{}, "sw1")
	if err != nil {
		t.Fatalf("filter.New error = %v", err)
	}

	// Forward packet: client (10.0.10.7:40000) -> server (10.0.20.5:443) on vlan10 in
	clientIP := netip.MustParseAddr("10.0.10.7")
	serverIP := netip.MustParseAddr("10.0.20.5")
	fwdFrame := makeTCPFrame(t, clientIP, serverIP, 40000, 443, tcp.SYN)

	resFwd := l.EvaluateIngress("vlan10", fwdFrame)
	if resFwd.Decision != filter.DecisionAccept {
		t.Fatalf("forward decision = %v, want accept", resFwd.Decision)
	}
	if len(resFwd.Steps) != 1 || resFwd.Steps[0].RuleID != filter.RuleAccept {
		t.Fatalf("forward rule ID = %v, want %v", resFwd.Steps[0].RuleID, filter.RuleAccept)
	}

	// Reply packet: server (10.0.20.5:443) -> client (10.0.10.7:40000) on vlan20 in
	replyFrame := makeTCPFrame(t, serverIP, clientIP, 443, 40000, tcp.SYN|tcp.ACK)

	resIngress := l.EvaluateIngress("vlan20", replyFrame)
	if resIngress.Decision != filter.DecisionDeferred {
		t.Fatalf("reply ingress decision = %v, want deferred", resIngress.Decision)
	}

	// Resolve deferred ingress past routing with egress interface vlan10
	resResolved := l.ResolveDeferred(resIngress, "vlan10")
	if resResolved.Decision != filter.DecisionAccept {
		t.Fatalf("resolved decision = %v, want accept", resResolved.Decision)
	}
	if len(resResolved.Steps) != 1 {
		t.Fatalf("resolved steps len = %d, want 1", len(resResolved.Steps))
	}
	step := resResolved.Steps[0]
	if step.RuleID != filter.RuleState {
		t.Errorf("step RuleID = %v, want %v", step.RuleID, filter.RuleState)
	}

	// Verify the trace names lan-in's rule in Inputs
	var foundForwardRuleFact bool
	for _, f := range step.Inputs {
		if f.TypeID() == "filter.rule_decision" {
			canon := f.Canonical()
			if canon == `set="lan-in";rule="allow-lan-to-srv-https";action="accept";direction="in";interface="vlan10"` {
				foundForwardRuleFact = true
			}
		}
	}
	if !foundForwardRuleFact {
		t.Errorf("step inputs did not contain lan-in's forward rule fact: %+v", step.Inputs)
	}

	// Verify output fact is for srv-in state decision
	if len(step.Outputs) != 1 || step.Outputs[0].TypeID() != "filter.rule_decision" {
		t.Fatalf("step outputs invalid: %+v", step.Outputs)
	}
	wantOutput := `set="srv-in";rule="state";action="accept";direction="in";interface="vlan20"`
	if step.Outputs[0].Canonical() != wantOutput {
		t.Errorf("step output = %q, want %q", step.Outputs[0].Canonical(), wantOutput)
	}
}

func TestStatelessSetNeverConsultsCounterpart(t *testing.T) {
	protoTCP := uint8(6)

	cfg := filter.Config{
		Sets: map[string]filter.RuleSet{
			"lan-in": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "allow-https",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 443, End: 443}},
						},
					},
				},
			},
			"srv-in-stateless": {
				Stateful: false,
				Default:  filter.Drop,
				Rules:    []filter.Rule{},
			},
			"srv-in-own-rule": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "srv-accept-reply",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							SrcPorts: []filter.PortRange{{Start: 443, End: 443}},
						},
					},
				},
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "lan-in"},
			{Interface: "vlan20", Direction: filter.In, Set: "srv-in-stateless"},
			{Interface: "vlan30", Direction: filter.In, Set: "srv-in-own-rule"},
		},
	}

	l, err := filter.New(cfg, port.Table{}, "sw1")
	if err != nil {
		t.Fatalf("filter.New error = %v", err)
	}

	clientIP := netip.MustParseAddr("10.0.10.7")
	serverIP := netip.MustParseAddr("10.0.20.5")
	replyFrame := makeTCPFrame(t, serverIP, clientIP, 443, 40000, tcp.ACK)

	// Case A: stateless set on vlan20 must not defer and must not consult counterpart
	resStateless := l.EvaluateIngress("vlan20", replyFrame)
	if resStateless.Decision != filter.DecisionDrop {
		t.Fatalf("stateless decision = %v, want drop", resStateless.Decision)
	}
	if len(resStateless.Steps) != 1 || resStateless.Steps[0].RuleID != filter.RuleDefault {
		t.Errorf("stateless rule ID = %v, want %v", resStateless.Steps[0].RuleID, filter.RuleDefault)
	}
	// Check consulted scopes: should only contain vlan20 scope, not vlan10
	vlan10Scope := filter.Scope("sw1", "vlan10", filter.In)
	for _, sc := range resStateless.ConsultedScopes() {
		if sc == vlan10Scope {
			t.Errorf("stateless set consulted counterpart scope %v", sc)
		}
	}

	// Case B: stateful set whose own rule matches on vlan30 accepts immediately and names srv-accept-reply, not lan-in
	resOwn := l.EvaluateIngress("vlan30", replyFrame)
	if resOwn.Decision != filter.DecisionAccept {
		t.Fatalf("own rule decision = %v, want accept", resOwn.Decision)
	}
	if len(resOwn.Steps) != 1 || resOwn.Steps[0].RuleID != filter.RuleAccept {
		t.Errorf("own rule step rule ID = %v, want %v", resOwn.Steps[0].RuleID, filter.RuleAccept)
	}
	wantOutput := `set="srv-in-own-rule";rule="srv-accept-reply";action="accept";direction="in";interface="vlan30"`
	if resOwn.Steps[0].Outputs[0].Canonical() != wantOutput {
		t.Errorf("own rule step output = %q, want %q", resOwn.Steps[0].Outputs[0].Canonical(), wantOutput)
	}
}

func TestResolveDeferredReverseMatchHonorsFirstMatch(t *testing.T) {
	protoTCP := uint8(6)

	cfg := filter.Config{
		Sets: map[string]filter.RuleSet{
			"lan-in": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "deny-host",
						Action: filter.Drop,
						Match: filter.Match{
							Src: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/32")},
						},
					},
					{
						Name:   "allow-subnet-https",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							Src:      []netip.Prefix{netip.MustParsePrefix("10.0.10.0/24")},
							DstPorts: []filter.PortRange{{Start: 443, End: 443}},
						},
					},
				},
			},
			"srv-in": {
				Stateful: true,
				Default:  filter.Drop,
				Rules:    []filter.Rule{},
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "lan-in"},
			{Interface: "vlan20", Direction: filter.In, Set: "srv-in"},
		},
	}

	l, err := filter.New(cfg, port.Table{}, "sw1")
	if err != nil {
		t.Fatalf("filter.New error = %v", err)
	}

	clientIP := netip.MustParseAddr("10.0.10.7")
	serverIP := netip.MustParseAddr("10.0.20.5")
	replyFrame := makeTCPFrame(t, serverIP, clientIP, 443, 40000, tcp.SYN|tcp.ACK)

	resIngress := l.EvaluateIngress("vlan20", replyFrame)
	if resIngress.Decision != filter.DecisionDeferred {
		t.Fatalf("reply ingress decision = %v, want deferred", resIngress.Decision)
	}

	resResolved := l.ResolveDeferred(resIngress, "vlan10")
	if resResolved.Decision != filter.DecisionDrop {
		t.Errorf("resolved decision = %v, want drop (first matching counterpart rule denies)", resResolved.Decision)
	}
	if len(resResolved.Steps) != 1 || resResolved.Steps[0].RuleID != filter.RuleDefault {
		t.Errorf("resolved step = %+v, want a single %v step", resResolved.Steps, filter.RuleDefault)
	}
}

func TestEvaluateEgressReverseMatchHonorsFirstMatch(t *testing.T) {
	protoTCP := uint8(6)

	cfg := filter.Config{
		Sets: map[string]filter.RuleSet{
			"out-empty": {
				Stateful: true,
				Default:  filter.Drop,
				Rules:    []filter.Rule{},
			},
			"lan-out": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "deny-host",
						Action: filter.Drop,
						Match: filter.Match{
							Src: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/32")},
						},
					},
					{
						Name:   "allow-subnet-https",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							Src:      []netip.Prefix{netip.MustParsePrefix("10.0.10.0/24")},
							DstPorts: []filter.PortRange{{Start: 443, End: 443}},
						},
					},
				},
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan20", Direction: filter.Out, Set: "out-empty"},
			{Interface: "vlan10", Direction: filter.Out, Set: "lan-out"},
		},
	}

	l, err := filter.New(cfg, port.Table{}, "sw1")
	if err != nil {
		t.Fatalf("filter.New error = %v", err)
	}

	clientIP := netip.MustParseAddr("10.0.10.7")
	serverIP := netip.MustParseAddr("10.0.20.5")
	replyFrame := makeTCPFrame(t, serverIP, clientIP, 443, 40000, tcp.SYN|tcp.ACK)

	res := l.EvaluateEgress("vlan20", "vlan10", replyFrame)
	if res.Decision != filter.DecisionDrop {
		t.Errorf("egress decision = %v, want drop (first matching counterpart rule denies)", res.Decision)
	}
	if len(res.Steps) != 1 || res.Steps[0].RuleID != filter.RuleDefault {
		t.Errorf("egress step = %+v, want a single %v step", res.Steps, filter.RuleDefault)
	}
}

func TestStatefulReplyMatchesFlagQualifiedForwardRule(t *testing.T) {
	protoTCP := uint8(6)

	cfg := filter.Config{
		Sets: map[string]filter.RuleSet{
			"lan-in": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "allow-https-syn",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 443, End: 443}},
							TCPFlags: &filter.FlagMatch{Mask: tcp.SYN, Value: tcp.SYN},
						},
					},
				},
			},
			"srv-in": {
				Stateful: true,
				Default:  filter.Drop,
				Rules:    []filter.Rule{},
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "lan-in"},
			{Interface: "vlan20", Direction: filter.In, Set: "srv-in"},
		},
	}

	l, err := filter.New(cfg, port.Table{}, "sw1")
	if err != nil {
		t.Fatalf("filter.New error = %v", err)
	}

	clientIP := netip.MustParseAddr("10.0.10.7")
	serverIP := netip.MustParseAddr("10.0.20.5")

	fwdFrame := makeTCPFrame(t, clientIP, serverIP, 40000, 443, tcp.SYN)
	if resFwd := l.EvaluateIngress("vlan10", fwdFrame); resFwd.Decision != filter.DecisionAccept {
		t.Fatalf("forward decision = %v, want accept", resFwd.Decision)
	}

	replyFrame := makeTCPFrame(t, serverIP, clientIP, 443, 40000, tcp.SYN|tcp.ACK)
	resIngress := l.EvaluateIngress("vlan20", replyFrame)
	if resIngress.Decision != filter.DecisionDeferred {
		t.Fatalf("reply ingress decision = %v, want deferred", resIngress.Decision)
	}

	resResolved := l.ResolveDeferred(resIngress, "vlan10")
	if resResolved.Decision != filter.DecisionAccept {
		t.Fatalf("resolved decision = %v, want accept", resResolved.Decision)
	}
	if len(resResolved.Steps) != 1 || resResolved.Steps[0].RuleID != filter.RuleState {
		t.Errorf("resolved step = %+v, want a single %v step", resResolved.Steps, filter.RuleState)
	}
}

func TestEmptySetDefaultStep(t *testing.T) {
	cfg := filter.Config{
		Sets: map[string]filter.RuleSet{
			"allow-all": {
				Stateful: false,
				Default:  filter.Accept,
				Rules:    nil,
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "allow-all"},
		},
	}

	l, err := filter.New(cfg, port.Table{}, "sw1")
	if err != nil {
		t.Fatalf("filter.New error = %v", err)
	}

	clientIP := netip.MustParseAddr("10.0.10.7")
	serverIP := netip.MustParseAddr("10.0.20.5")
	f := makeTCPFrame(t, clientIP, serverIP, 40000, 80, tcp.SYN)

	res := l.EvaluateIngress("vlan10", f)
	if res.Decision != filter.DecisionAccept {
		t.Errorf("decision = %v, want accept", res.Decision)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("step count = %d, want exactly 1", len(res.Steps))
	}
	if res.Steps[0].RuleID != filter.RuleDefault {
		t.Errorf("rule ID = %v, want %v", res.Steps[0].RuleID, filter.RuleDefault)
	}

	wantScope := filter.Scope("sw1", "vlan10", filter.In)
	scopes := res.ConsultedScopes()
	if len(scopes) != 1 || scopes[0] != wantScope {
		t.Errorf("consulted scopes = %v, want [%v]", scopes, wantScope)
	}
}

func TestDropIsCompleteDomainOutcome(t *testing.T) {
	cfg := filter.Config{
		Sets: map[string]filter.RuleSet{
			"deny-all": {
				Stateful: false,
				Default:  filter.Drop,
				Rules:    nil,
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "deny-all"},
		},
	}

	l, err := filter.New(cfg, port.Table{}, "sw1")
	if err != nil {
		t.Fatalf("filter.New error = %v", err)
	}

	clientIP := netip.MustParseAddr("10.0.10.7")
	serverIP := netip.MustParseAddr("10.0.20.5")
	f := makeTCPFrame(t, clientIP, serverIP, 40000, 80, tcp.SYN)

	res := l.EvaluateIngress("vlan10", f)
	if res.Decision != filter.DecisionDrop {
		t.Fatalf("decision = %v, want drop", res.Decision)
	}
	if res.Outcome() != trace.Dropped {
		t.Errorf("Outcome() = %v, want %v", res.Outcome(), trace.Dropped)
	}
	if res.Status() != analysis.Complete {
		t.Errorf("Status() = %v, want %v", res.Status(), analysis.Complete)
	}
}

func TestStatefulReverseMatchRuleEnumeration(t *testing.T) {
	protoTCP := uint8(6)
	clientIP := netip.MustParseAddr("10.0.10.7")
	serverIP := netip.MustParseAddr("10.0.20.5")
	hostPrefix := netip.MustParsePrefix("10.0.10.7/32")
	httpsPort := []filter.PortRange{{Start: 443, End: 443}}

	cases := []struct {
		name         string
		rules        []filter.Rule
		wantDecision filter.Decision
		wantRuleID   trace.RuleID
	}{
		{
			name: "accept-https-admits-reply",
			rules: []filter.Rule{
				{
					Name:   "accept-https",
					Action: filter.Accept,
					Match:  filter.Match{Protocol: &protoTCP, DstPorts: httpsPort},
				},
			},
			wantDecision: filter.DecisionAccept,
			wantRuleID:   filter.RuleState,
		},
		{
			name: "pure-five-tuple-drop-shadows-accept",
			rules: []filter.Rule{
				{
					Name:   "drop-host",
					Action: filter.Drop,
					Match:  filter.Match{Protocol: &protoTCP, Src: []netip.Prefix{hostPrefix}},
				},
				{
					Name:   "accept-https",
					Action: filter.Accept,
					Match:  filter.Match{Protocol: &protoTCP, DstPorts: httpsPort},
				},
			},
			wantDecision: filter.DecisionDrop,
			wantRuleID:   filter.RuleDefault,
		},
		{
			name: "flag-qualified-accept-admits-reply",
			rules: []filter.Rule{
				{
					Name:   "accept-https-syn",
					Action: filter.Accept,
					Match: filter.Match{
						Protocol: &protoTCP,
						DstPorts: httpsPort,
						TCPFlags: &filter.FlagMatch{Mask: tcp.SYN, Value: tcp.SYN},
					},
				},
			},
			wantDecision: filter.DecisionAccept,
			wantRuleID:   filter.RuleState,
		},
		{
			name: "flag-qualified-drop-does-not-shadow-accept",
			rules: []filter.Rule{
				{
					Name:   "drop-https-rst",
					Action: filter.Drop,
					Match: filter.Match{
						Protocol: &protoTCP,
						DstPorts: httpsPort,
						TCPFlags: &filter.FlagMatch{Mask: tcp.RST, Value: tcp.RST},
					},
				},
				{
					Name:   "accept-https",
					Action: filter.Accept,
					Match:  filter.Match{Protocol: &protoTCP, DstPorts: httpsPort},
				},
			},
			wantDecision: filter.DecisionAccept,
			wantRuleID:   filter.RuleState,
		},
	}

	replyFrame := makeTCPFrame(t, serverIP, clientIP, 443, 40000, tcp.SYN|tcp.ACK)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := filter.Config{
				Sets: map[string]filter.RuleSet{
					"counterpart": {
						Stateful: true,
						Default:  filter.Drop,
						Rules:    tc.rules,
					},
					"reply-in": {
						Stateful: true,
						Default:  filter.Drop,
						Rules:    []filter.Rule{},
					},
					"out-empty": {
						Stateful: true,
						Default:  filter.Drop,
						Rules:    []filter.Rule{},
					},
				},
				Bindings: []filter.Binding{
					{Interface: "vlan10", Direction: filter.In, Set: "counterpart"},
					{Interface: "vlan10", Direction: filter.Out, Set: "counterpart"},
					{Interface: "vlan20", Direction: filter.In, Set: "reply-in"},
					{Interface: "vlan20", Direction: filter.Out, Set: "out-empty"},
				},
			}

			l, err := filter.New(cfg, port.Table{}, "sw1")
			if err != nil {
				t.Fatalf("filter.New error = %v", err)
			}

			resIngress := l.EvaluateIngress("vlan20", replyFrame)
			if resIngress.Decision != filter.DecisionDeferred {
				t.Fatalf("reply ingress decision = %v, want deferred", resIngress.Decision)
			}
			resResolved := l.ResolveDeferred(resIngress, "vlan10")
			if resResolved.Decision != tc.wantDecision {
				t.Errorf("ResolveDeferred decision = %v, want %v", resResolved.Decision, tc.wantDecision)
			}
			if len(resResolved.Steps) != 1 || resResolved.Steps[0].RuleID != tc.wantRuleID {
				t.Errorf("ResolveDeferred step = %+v, want a single %v step", resResolved.Steps, tc.wantRuleID)
			}

			resEgress := l.EvaluateEgress("vlan20", "vlan10", replyFrame)
			if resEgress.Decision != tc.wantDecision {
				t.Errorf("EvaluateEgress decision = %v, want %v", resEgress.Decision, tc.wantDecision)
			}
			if len(resEgress.Steps) != 1 || resEgress.Steps[0].RuleID != tc.wantRuleID {
				t.Errorf("EvaluateEgress step = %+v, want a single %v step", resEgress.Steps, tc.wantRuleID)
			}
		})
	}
}
