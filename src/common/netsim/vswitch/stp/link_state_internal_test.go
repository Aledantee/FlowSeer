package stp

import (
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

// propertyClass names one of the five roles a portState field can play in the
// link/tree split: whether a property belongs to the physical link, shared by
// every tree running over the port, or to one tree's own computation.
type propertyClass int

const (
	// classLinkReplicated fields live in the embedded linkState and are
	// assigned as a whole struct by syncInstancePorts, so a field added there
	// later reaches every tree without a copy statement of its own.
	classLinkReplicated propertyClass = iota

	// classLinkOnCIST fields are written only on the CIST's port state, and
	// every reader, whatever tree it is answering for, must read them
	// through the CIST rather than through its own copy.
	classLinkOnCIST

	// classLinkDerived is pathCost alone: copied from the CIST's
	// linkPathCost, but only for a tree that has not fixed its own.
	classLinkDerived

	// classTreeOwned fields belong to one tree's own computation: each tree
	// writes and reads its own copy, and syncInstancePorts never assigns
	// them, except that it clears some of them when the CIST's port goes
	// down (see fieldSpec.linkDownClears).
	classTreeOwned

	// classPortConstant fields are set once at construction, identical on
	// every tree, and never assigned again.
	classPortConstant
)

// fieldSpec classifies one portState field and, for a classTreeOwned field,
// whether syncInstancePorts also clears it on the link-down branch.
type fieldSpec struct {
	class          propertyClass
	linkDownClears bool
}

// portStateFieldClasses is the classification this package's own test lives
// by: every field of portState, including the ones embedded through
// linkState, must have an entry here. A field with no entry is this
// package's stop condition (see TestPortStateFieldsAreClassified), not a
// judgment call left for a reader to make later.
var portStateFieldClasses = map[string]fieldSpec{
	// port-constant: set once at construction, never assigned again.
	"name":      {class: classPortConstant},
	"cfg":       {class: classPortConstant},
	"adminEdge": {class: classPortConstant},

	// link-replicated: linkState, assigned wholesale by syncInstancePorts.
	"up":           {class: classLinkReplicated},
	"pointToPoint": {class: classLinkReplicated},
	"edge":         {class: classLinkReplicated},
	"sendRSTP":     {class: classLinkReplicated},

	// link-on-cist: written only on the CIST's port state, read through the
	// CIST by every tree.
	"linkPathCost":      {class: classLinkOnCIST},
	"external":          {class: classLinkOnCIST},
	"bpduGuardDisabled": {class: classLinkOnCIST},
	"loopInconsistent":  {class: classLinkOnCIST},
	"pvstBoundary":      {class: classLinkOnCIST},
	"mdelayWhile":       {class: classLinkOnCIST},
	"rxBPDUs":           {class: classLinkOnCIST},
	"badBPDUs":          {class: classLinkOnCIST},

	// link-derived: pathCost alone, copied from the CIST's linkPathCost only
	// when the tree has not fixed its own.
	"pathCost": {class: classLinkDerived},

	// tree-owned: each tree writes and reads its own. role, state,
	// pvidInconsistent, and rcvInfoValid are also what syncInstancePorts
	// resets when the CIST's port goes down.
	"portID":                  {class: classTreeOwned},
	"pathCostFixed":           {class: classTreeOwned},
	"role":                    {class: classTreeOwned, linkDownClears: true},
	"state":                   {class: classTreeOwned, linkDownClears: true},
	"pvidInconsistent":        {class: classTreeOwned, linkDownClears: true},
	"proposing":               {class: classTreeOwned},
	"agreed":                  {class: classTreeOwned},
	"fwdDelayTimer":           {class: classTreeOwned},
	"edgeDelayWhile":          {class: classTreeOwned},
	"forwardTransitions":      {class: classTreeOwned},
	"txBPDUs":                 {class: classTreeOwned},
	"rcvRegionalRootID":       {class: classTreeOwned},
	"rcvInternalRootPathCost": {class: classTreeOwned},
	"rcvRemainingHops":        {class: classTreeOwned},
	"rcvInfoValid":            {class: classTreeOwned, linkDownClears: true},
	"rcvRootID":               {class: classTreeOwned},
	"rcvRootPathCost":         {class: classTreeOwned},
	"rcvBridgeID":             {class: classTreeOwned},
	"rcvPortID":               {class: classTreeOwned},
	"rcvMessageAge":           {class: classTreeOwned},
	"rcvMaxAge":               {class: classTreeOwned},
	"rcvHelloTime":            {class: classTreeOwned},
	"rcvForwardDelay":         {class: classTreeOwned},
	"rcvTime":                 {class: classTreeOwned},
}

// TestPortStateFieldsAreClassified walks every field of portState, descending
// into the embedded linkState, and fails on any field portStateFieldClasses
// does not name. A field added to portState without a classification entry
// fails this test, which is the point: the propagation rule below can only be
// trusted while every field has been placed into one of the five classes.
func TestPortStateFieldsAreClassified(t *testing.T) {
	seen := map[string]bool{}
	walkPortStateFields(t, reflect.TypeOf(portState{}), seen)

	for name := range portStateFieldClasses {
		if !seen[name] {
			t.Errorf("portStateFieldClasses names %q, which portState no longer has", name)
		}
	}
}

func walkPortStateFields(t *testing.T, typ reflect.Type, seen map[string]bool) {
	t.Helper()

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Anonymous {
			walkPortStateFields(t, f.Type, seen)

			continue
		}

		seen[f.Name] = true
		if _, ok := portStateFieldClasses[f.Name]; !ok {
			t.Errorf("portState field %q has no entry in portStateFieldClasses: classify it as "+
				"link-replicated, link-on-cist, link-derived, tree-owned, or port-constant", f.Name)
		}
	}
}

// syncTestLayer builds a two-tree layer (the CIST and one MSTI carrying VLAN
// 10) with a single up port, so syncInstancePorts has a non-CIST tree's port
// to carry a change onto.
func syncTestLayer(t *testing.T) (l *Layer, cistP, mstP *portState) {
	t.Helper()

	cfg := Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		Ports:    map[string]Port{"p1": {PathCost: 100}},
		MST: &MST{
			Name: "region-1",
			Instances: map[MSTID]Instance{
				1: {VLANs: []vlan.ID{10}},
			},
		},
	}.Normalize()

	l = newLayer(cfg)
	l.LinkChange(time.Unix(0, 0), "p1", true, true, 1_000_000_000)

	cistP = l.cist().ports["p1"]
	mstP = l.trees[treeID(1)].ports["p1"]

	return l, cistP, mstP
}

// TestSyncInstancePortsReplicatesLinkState is evidence for the
// classLinkReplicated rule (R3): setting every linkState field on the CIST's
// port to a value the MSTI's own port does not already hold, then calling
// syncInstancePorts, leaves the MSTI's copy equal to the CIST's.
func TestSyncInstancePortsReplicatesLinkState(t *testing.T) {
	t.Parallel()

	l, cistP, mstP := syncTestLayer(t)

	cistP.linkState = linkState{up: true, pointToPoint: false, edge: true, sendRSTP: false}
	if mstP.linkState == cistP.linkState {
		t.Fatal("test setup: the MSTI's linkState already matches the CIST's before syncing")
	}

	l.syncInstancePorts("p1", cistP)

	if mstP.linkState != cistP.linkState {
		t.Errorf("MSTI linkState after sync = %+v, want the CIST's %+v", mstP.linkState, cistP.linkState)
	}
}

// TestSyncInstancePortsLeavesLinkOnCISTFieldsUntouched is evidence for the
// classLinkOnCIST rule (R4's other half): syncInstancePorts never writes an
// MSTI's own copy of a link-on-cist field, since every reader is expected to
// read the CIST's copy instead.
func TestSyncInstancePortsLeavesLinkOnCISTFieldsUntouched(t *testing.T) {
	t.Parallel()

	l, cistP, mstP := syncTestLayer(t)

	cistP.linkPathCost = 12345
	cistP.external = true
	cistP.bpduGuardDisabled = true
	cistP.loopInconsistent = true
	cistP.pvstBoundary = true
	cistP.mdelayWhile = time.Unix(100, 0)
	cistP.rxBPDUs = 7
	cistP.badBPDUs = 3

	before := *mstP
	l.syncInstancePorts("p1", cistP)

	if mstP.external != before.external || mstP.bpduGuardDisabled != before.bpduGuardDisabled ||
		mstP.loopInconsistent != before.loopInconsistent || mstP.pvstBoundary != before.pvstBoundary ||
		mstP.mdelayWhile != before.mdelayWhile || mstP.rxBPDUs != before.rxBPDUs || mstP.badBPDUs != before.badBPDUs {
		t.Errorf("MSTI link-on-cist fields changed by sync: got %+v, want unchanged from %+v", mstP, before)
	}
}

// TestSyncInstancePortsDerivesUnfixedPathCostFromTheLink is evidence for the
// classLinkDerived rule: an MSTI port with no fixed cost of its own tracks
// the CIST's linkPathCost, and one with a fixed cost does not.
func TestSyncInstancePortsDerivesUnfixedPathCostFromTheLink(t *testing.T) {
	t.Parallel()

	l, cistP, mstP := syncTestLayer(t)

	cistP.linkPathCost = 5555
	l.syncInstancePorts("p1", cistP)
	if mstP.pathCost != 5555 {
		t.Errorf("unfixed MSTI pathCost after sync = %d, want the link-derived 5555", mstP.pathCost)
	}

	mstP.pathCostFixed = true
	mstP.pathCost = 42
	cistP.linkPathCost = 9999
	l.syncInstancePorts("p1", cistP)
	if mstP.pathCost != 42 {
		t.Errorf("fixed MSTI pathCost after sync = %d, want the untouched 42", mstP.pathCost)
	}
}

// TestSyncInstancePortsLeavesOtherTreeOwnedFieldsUntouched is evidence that a
// tree-owned field not named by the link-down-clear flag survives a sync
// while the port stays up, whatever the CIST's own copy holds.
func TestSyncInstancePortsLeavesOtherTreeOwnedFieldsUntouched(t *testing.T) {
	t.Parallel()

	l, cistP, mstP := syncTestLayer(t)

	mstP.role = RoleAlternate
	mstP.agreed = true
	mstP.forwardTransitions = 9
	mstP.txBPDUs = 4

	l.syncInstancePorts("p1", cistP)

	if mstP.role != RoleAlternate || !mstP.agreed || mstP.forwardTransitions != 9 || mstP.txBPDUs != 4 {
		t.Errorf("tree-owned fields changed by a sync with the port still up: got role=%v agreed=%t "+
			"forwardTransitions=%d txBPDUs=%d", mstP.role, mstP.agreed, mstP.forwardTransitions, mstP.txBPDUs)
	}
}

// TestSyncInstancePortsOnLinkDownClearsTheFourNamedFields is evidence for the
// link-down-clear flag: when the CIST's port has gone down, syncInstancePorts
// resets role, state, rcvInfoValid, and pvidInconsistent on the MSTI's own
// port, and leaves every other tree-owned field alone.
func TestSyncInstancePortsOnLinkDownClearsTheFourNamedFields(t *testing.T) {
	t.Parallel()

	l, cistP, mstP := syncTestLayer(t)

	mstP.role = RoleRoot
	mstP.state = StateForwarding
	mstP.rcvInfoValid = true
	mstP.pvidInconsistent = true
	mstP.forwardTransitions = 3
	mstP.agreed = true

	cistP.up = false
	l.syncInstancePorts("p1", cistP)

	if mstP.role != RoleDisabled {
		t.Errorf("MSTI role after link down = %v, want Disabled", mstP.role)
	}
	if mstP.state != StateDiscarding {
		t.Errorf("MSTI state after link down = %v, want Discarding", mstP.state)
	}
	if mstP.rcvInfoValid {
		t.Error("MSTI rcvInfoValid after link down = true, want false")
	}
	if mstP.pvidInconsistent {
		t.Error("MSTI pvidInconsistent after link down = true, want false")
	}
	// forwardTransitions and agreed are tree-owned but not among the four
	// link-down-clear fields; they survive the same call untouched.
	if mstP.forwardTransitions != 3 {
		t.Errorf("MSTI forwardTransitions after link down = %d, want the untouched 3", mstP.forwardTransitions)
	}
	if !mstP.agreed {
		t.Error("MSTI agreed after link down = false, want the untouched true")
	}
}

// TestAnMSTIDoesNotElectThroughAGuardDisabledPort is evidence that once BPDU
// guard fires on the CIST's copy of a port, an MSTI's own root election
// excludes it too, even though the MSTI's own rcvInfoValid survives the
// guard firing untouched (receiveLink clears rcvInfoValid on the CIST alone)
// and would otherwise look like a live candidate until it ages out on its
// own. Reaching that state through the public API alone is not possible: a
// BPDU-guarded port's very first reception fires the guard before the frame
// ever reaches an MSTI's own applyBPDU, so no BPDU can establish an MSTI's
// information on a guarded port in the first place. This test seeds the
// MSTI's port state directly with the information a peer would have
// delivered moments earlier, before the guard fired, and then drives the
// guard-firing BPDU through the public Receive.
func TestAnMSTIDoesNotElectThroughAGuardDisabledPort(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)

	cfg := Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		Ports:    map[string]Port{"l1": {BPDUGuard: true}},
		MST: &MST{
			Name: "region-1",
			Instances: map[MSTID]Instance{
				1: {VLANs: []vlan.ID{10}},
			},
		},
	}.Normalize()

	l := newLayer(cfg)
	l.LinkChange(t0, "l1", true, true, 1_000_000_000)

	mstP := l.trees[treeID(1)].ports["l1"]
	peer := BridgeID{Priority: 4096, Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0xee}}
	mstP.rcvInfoValid = true
	mstP.rcvRootID = peer
	mstP.rcvBridgeID = peer
	mstP.rcvPortID = 0x8001
	mstP.rcvRemainingHops = 20
	mstP.rcvHelloTime = 2 * time.Second
	mstP.rcvTime = t0

	rogue := BPDU{
		Version:      2,
		Type:         BPDUTypeRapid,
		RootID:       BridgeID{Priority: 0, Address: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x99}},
		BridgeID:     BridgeID{Priority: 0, Address: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x99}},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	rogue.SetRole(RoleDesignated)

	l.Receive(t0.Add(time.Second), "l1", rogue)

	if cistP := l.cist().ports["l1"]; !cistP.bpduGuardDisabled {
		t.Fatal("test setup: BPDU guard did not fire on l1")
	}

	mt := l.trees[treeID(1)]
	if mt.rootID != mt.bridgeID {
		t.Errorf("MSTI 1 root = %v, want this bridge's own %v", mt.rootID, mt.bridgeID)
	}
	if mt.rootPort != "" {
		t.Errorf("MSTI 1 root port = %q, want empty", mt.rootPort)
	}
}
