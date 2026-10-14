package fabric_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"

	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestReflectorDiffAdded proves that a reflector present only in the target
// configuration reports one change under the reflector subject, addition
// carried on To rather than From.
func TestReflectorDiffAdded(t *testing.T) {
	t.Parallel()

	base := reflectorBaseConfig(t)
	a := base
	a.Reflectors = nil

	changes := assembly.Diff(a, base)
	found := 0
	for _, ch := range changes {
		if ch.Subject.Kind != "reflector" {
			continue
		}
		found++
		if ch.Subject != (trace.Subject{Kind: "reflector", Key: "r1"}) {
			t.Errorf("subject = %+v, want reflector r1", ch.Subject)
		}
		if ch.Field != "" {
			t.Errorf("field = %q, want empty for a whole-reflector addition", ch.Field)
		}
		if ch.From != nil {
			t.Errorf("from = %+v, want nil for an addition", ch.From)
		}
		if ch.To == nil || ch.To.TypeID() != "fabric.reflector" {
			t.Errorf("to = %+v, want a fabric.reflector snapshot", ch.To)
		}
	}
	if found != 1 {
		t.Fatalf("reflector changes = %d, want 1: %+v", found, changes)
	}
}

// TestReflectorDiffRemoved proves that a reflector present only in the source
// configuration reports one change carried on From rather than To.
func TestReflectorDiffRemoved(t *testing.T) {
	t.Parallel()

	base := reflectorBaseConfig(t)
	b := base
	b.Reflectors = nil

	changes := assembly.Diff(base, b)
	found := 0
	for _, ch := range changes {
		if ch.Subject.Kind != "reflector" {
			continue
		}
		found++
		if ch.Subject != (trace.Subject{Kind: "reflector", Key: "r1"}) {
			t.Errorf("subject = %+v, want reflector r1", ch.Subject)
		}
		if ch.Field != "" {
			t.Errorf("field = %q, want empty for a whole-reflector removal", ch.Field)
		}
		if ch.To != nil {
			t.Errorf("to = %+v, want nil for a removal", ch.To)
		}
		if ch.From == nil || ch.From.TypeID() != "fabric.reflector" {
			t.Errorf("from = %+v, want a fabric.reflector snapshot", ch.From)
		}
	}
	if found != 1 {
		t.Fatalf("reflector changes = %d, want 1: %+v", found, changes)
	}
}

// TestReflectorDiffChangedAttachmentVLAN proves that a changed attachment
// VLAN reports the field path relative to the reflector subject, not the
// dotted "reflectors.r1.attachments.a.vlan" form Validate's error uses.
func TestReflectorDiffChangedAttachmentVLAN(t *testing.T) {
	t.Parallel()

	a := reflectorBaseConfig(t)
	b := a
	b.Reflectors = map[string]reflectormodel.Config{"r1": a.Reflectors["r1"].Clone()}
	attach := b.Reflectors["r1"].Attachments["a"]
	newVID := vlan.ID(20)
	attach.VLAN = &newVID
	b.Reflectors["r1"].Attachments["a"] = attach

	changes := assembly.Diff(a, b)
	var match *trace.Change
	for i := range changes {
		if changes[i].Subject == (trace.Subject{Kind: "reflector", Key: "r1"}) && changes[i].Field == `attachments["a"].vlan` {
			match = &changes[i]
		}
	}
	if match == nil {
		t.Fatalf("changes = %+v, want a reflector r1 change on field attachments[\"a\"].vlan", changes)
	}

	fromID, toID := "", ""
	if match.From != nil {
		fromID = match.From.Canonical()
	}
	if match.To != nil {
		toID = match.To.Canonical()
	}
	if fromID != "10" || toID != "20" || match.From.TypeID() != "fabric.vlan" || match.To.TypeID() != "fabric.vlan" {
		t.Errorf("attachments[\"a\"].vlan change = %q -> %q, want 10 -> 20", fromID, toID)
	}

	for _, ch := range changes {
		if ch.Subject.Kind == "reflector" && ch.Field != `attachments["a"].vlan` {
			t.Errorf("unexpected extra reflector change: %+v", ch)
		}
	}
}

// TestReflectorDiffPortAdded proves that a reflector port present only in
// the target configuration reports one change under the reflector subject,
// field `ports["<name>"]`, addition carried on To rather than From: a port and
// its cable decide link negotiation exactly as a host's do, so a diff that
// only compared the reflector's address and attachments could never report
// it.
func TestReflectorDiffPortAdded(t *testing.T) {
	t.Parallel()

	a := reflectorBaseConfig(t)
	b := a
	b.Reflectors = map[string]reflectormodel.Config{"r1": a.Reflectors["r1"].Clone()}
	b.Reflectors["r1"].Ports["p2"] = phy.Ethernet{}

	changes := assembly.Diff(a, b)
	var match *trace.Change
	for i := range changes {
		if changes[i].Subject == (trace.Subject{Kind: "reflector", Key: "r1"}) && changes[i].Field == `ports["p2"]` {
			match = &changes[i]
		}
	}
	if match == nil {
		t.Fatalf("changes = %+v, want a reflector r1 change on field ports[\"p2\"]", changes)
	}
	if match.From != nil {
		t.Errorf("from = %+v, want nil for an addition", match.From)
	}
	if match.To == nil || match.To.TypeID() != "phy.ethernet" {
		t.Errorf("to = %+v, want a phy.ethernet snapshot", match.To)
	}
}

// TestReflectorDiffChangedPortSpeed proves that a changed port field reports
// phy's own field name relative to the reflector subject.
func TestReflectorDiffChangedPortSpeed(t *testing.T) {
	t.Parallel()

	a := reflectorBaseConfig(t)
	r1 := a.Reflectors["r1"].Clone()
	r1.Ports["p1"] = phy.Ethernet{Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full}}
	a.Reflectors["r1"] = r1

	b := a
	b.Reflectors = map[string]reflectormodel.Config{"r1": a.Reflectors["r1"].Clone()}
	b.Reflectors["r1"].Ports["p1"] = phy.Ethernet{Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full}}

	changes := assembly.Diff(a, b)
	found := 0
	for _, ch := range changes {
		if ch.Subject != (trace.Subject{Kind: "reflector", Key: "r1"}) {
			continue
		}
		found++
		if ch.Field != `ports["p1"].speed_bps` {
			t.Errorf("field = %q, want ports[\"p1\"].speed_bps", ch.Field)
		}
		if ch.From == nil || ch.To == nil || ch.From.TypeID() != "phy.speed_bps" || ch.From.Canonical() != "100000000" || ch.To.TypeID() != "phy.speed_bps" || ch.To.Canonical() != "1000000000" {
			t.Errorf("speed facts = %v -> %v, want phy.speed_bps 100000000 -> 1000000000", ch.From, ch.To)
		}
	}
	if found != 1 {
		t.Fatalf("reflector changes = %d, want 1: %+v", found, changes)
	}
}

// TestReflectorDiffAttachmentAddressChange checks removal before addition on a retained attachment.
func TestReflectorDiffAttachmentAddressChange(t *testing.T) {
	t.Parallel()

	a := reflectorBaseConfig(t)
	b := a
	b.Reflectors = map[string]reflectormodel.Config{"r1": a.Reflectors["r1"].Clone()}
	attach := b.Reflectors["r1"].Attachments["a"]
	attach.Addresses = []netip.Prefix{netip.MustParsePrefix("10.0.10.10/24")}
	b.Reflectors["r1"].Attachments["a"] = attach

	changes := assembly.Diff(a, b)
	want := []simtest.ChangeExpectation{
		{Layer: "fabric", Subject: trace.Subject{Kind: "reflector", Key: "r1"}, Field: `attachments["a"].addresses["10.0.10.9/24"]`, From: &simtest.FactExpectation{TypeID: "fabric.prefix", Canonical: "10.0.10.9/24"}},
		{Layer: "fabric", Subject: trace.Subject{Kind: "reflector", Key: "r1"}, Field: `attachments["a"].addresses["10.0.10.10/24"]`, To: &simtest.FactExpectation{TypeID: "fabric.prefix", Canonical: "10.0.10.10/24"}},
	}
	if len(changes) != 2 {
		t.Fatalf("reflector changes = %d, want 2: %+v", len(changes), changes)
	}
	for i, expected := range want {
		if !expected.Matches(changes[i]) {
			t.Errorf("change[%d] = %+v, want %+v", i, changes[i], expected)
		}
	}
}
