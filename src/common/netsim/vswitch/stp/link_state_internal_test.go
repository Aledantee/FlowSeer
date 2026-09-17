package stp

import (
	"fmt"
	"reflect"
	"testing"
	"time"
	"unsafe"

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

// fieldSpec classifies one portState field and, for a classTreeOwned field
// syncInstancePorts clears on the link-down branch, both that it clears and
// the value it clears to.
type fieldSpec struct {
	class          propertyClass
	linkDownClears bool
	linkDownValue  any
}

// portStateFieldClasses is the classification this package's own test lives
// by: every field of portState, including the ones embedded through
// linkState, must have an entry here. A field with no entry is this
// package's stop condition (see TestPortStateFieldsAreClassified), not a
// judgment call left for a reader to make later. The tests below drive their
// assertions off this table by reflection, field by field, so a class or a
// linkDownClears flag that stops matching what the code does fails the test
// that names the field, not just the table's own membership check.
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
	"role":                    {class: classTreeOwned, linkDownClears: true, linkDownValue: RoleDisabled},
	"state":                   {class: classTreeOwned, linkDownClears: true, linkDownValue: StateDiscarding},
	"pvidInconsistent":        {class: classTreeOwned, linkDownClears: true, linkDownValue: false},
	"proposing":               {class: classTreeOwned},
	"agreed":                  {class: classTreeOwned},
	"fwdDelayTimer":           {class: classTreeOwned},
	"edgeDelayWhile":          {class: classTreeOwned},
	"forwardTransitions":      {class: classTreeOwned},
	"txBPDUs":                 {class: classTreeOwned},
	"rcvRegionalRootID":       {class: classTreeOwned},
	"rcvInternalRootPathCost": {class: classTreeOwned},
	"rcvRemainingHops":        {class: classTreeOwned},
	"rcvInfoValid":            {class: classTreeOwned, linkDownClears: true, linkDownValue: false},
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
// does not name. It also fails on a field whose struct position disagrees
// with its class: a field embedded through linkState that the table does not
// classify classLinkReplicated, or a field classified classLinkReplicated
// that is not embedded through linkState. That position check is what a
// field added to (or misplaced from) linkState cannot pass by accident, since
// linkState is the struct syncInstancePorts assigns wholesale — it is the
// mechanism the class name promises, not just a label next to the field.
func TestPortStateFieldsAreClassified(t *testing.T) {
	seen := map[string]bool{}
	walkPortStateFields(t, reflect.TypeOf(portState{}), false, seen)

	for name := range portStateFieldClasses {
		if !seen[name] {
			t.Errorf("portStateFieldClasses names %q, which portState no longer has", name)
		}
	}
}

func walkPortStateFields(t *testing.T, typ reflect.Type, inLinkState bool, seen map[string]bool) {
	t.Helper()

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Anonymous {
			walkPortStateFields(t, f.Type, f.Type == reflect.TypeOf(linkState{}), seen)

			continue
		}

		seen[f.Name] = true

		spec, ok := portStateFieldClasses[f.Name]
		if !ok {
			t.Errorf("portState field %q has no entry in portStateFieldClasses: classify it as "+
				"link-replicated, link-on-cist, link-derived, tree-owned, or port-constant", f.Name)

			continue
		}

		if replicated := spec.class == classLinkReplicated; replicated != inLinkState {
			switch {
			case inLinkState:
				t.Errorf("portState field %q is embedded in linkState but classified %v, want classLinkReplicated",
					f.Name, spec.class)
			default:
				t.Errorf("portState field %q is classified classLinkReplicated but declared outside the "+
					"embedded linkState, so syncInstancePorts's wholesale assignment never reaches it", f.Name)
			}
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

// settableField returns a readable, settable reflect.Value for the named
// field of a portState reached through v (a pointer to portState). portState
// is a value type entirely internal to this package, so every field is
// unexported; reflect refuses both Set and Interface on a value obtained
// straight from FieldByName because of that. Re-wrapping the field's address
// with reflect.NewAt drops the read-only flag reflect attaches for that
// reason, which is safe here because every field this is called with is
// already addressable (v is a pointer's Elem()) and package-private, so
// nothing outside this test can observe the bypass. If portStateFieldClasses
// names a field portState no longer has, FieldByName returns the zero
// reflect.Value, whose UnsafeAddr panics; this repository bans panics outside
// a Must/must helper, and a reflection subtest that crashed the binary would
// hide TestPortStateFieldsAreClassified's own readable Errorf for the same
// mismatch, so this fails the calling subtest instead.
func settableField(t *testing.T, v reflect.Value, name string) reflect.Value {
	t.Helper()

	f := v.FieldByName(name)
	if !f.IsValid() {
		t.Fatalf("portState has no field %q; portStateFieldClasses is out of date", name)
	}

	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

// distinctValue returns a value of typ that differs from typ's zero value, so
// a table-driven test can tell whether a sync actually touched a field or
// left its start-of-test value alone. seed only needs to vary the value
// enough to be recognizable in a failure message; it plays no role in
// correctness.
func distinctValue(t *testing.T, typ reflect.Type, seed int) reflect.Value {
	t.Helper()

	switch typ {
	case reflect.TypeOf(time.Time{}):
		return reflect.ValueOf(time.Unix(int64(1_700_000_000+seed), 0))
	case reflect.TypeOf(BridgeID{}):
		return reflect.ValueOf(BridgeID{
			Priority: uint16(seed + 1),
			Address:  netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, byte(seed + 1)},
		})
	}

	v := reflect.New(typ).Elem()
	switch typ.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.String:
		v.SetString(fmt.Sprintf("distinct-%d", seed))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(seed + 1))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(seed + 1))
	default:
		t.Fatalf("distinctValue: portState field type %s has no case; add one", typ)
	}

	return v
}

// TestSyncInstancePortsReplicatesEveryLinkReplicatedField is evidence for the
// classLinkReplicated rule (R3), driven from portStateFieldClasses instead of
// a hand-written field list: for every field the table classifies
// classLinkReplicated, zeroing the MSTI's own copy, setting the CIST's to a
// value distinct from that zero, and calling syncInstancePorts leaves the
// MSTI's copy equal to the CIST's. Zeroing the MSTI's copy first (rather than
// leaving it at whatever syncTestLayer's LinkChange left) keeps the check
// live for a bool field, where distinctValue always returns true and the two
// trees already agree once both have seen the same link-up call. A field the
// table misclassifies this way, or a change to syncInstancePorts that stops
// assigning linkState wholesale, fails on the specific field name rather than
// passing a membership check alone.
func TestSyncInstancePortsReplicatesEveryLinkReplicatedField(t *testing.T) {
	t.Parallel()

	for name, spec := range portStateFieldClasses {
		if spec.class != classLinkReplicated {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l, cistP, mstP := syncTestLayer(t)

			cistField := settableField(t, reflect.ValueOf(cistP).Elem(), name)
			want := distinctValue(t, cistField.Type(), 1)

			// Plant the opposing value on the MSTI's own copy first: both
			// LinkChange calls in syncTestLayer leave every classLinkReplicated
			// field already equal to want for a bool field (distinctValue
			// always returns true), which would make cistField.Set(want) below
			// a no-op and the assertion pass whether or not syncInstancePorts
			// actually replicates the field.
			mstField := settableField(t, reflect.ValueOf(mstP).Elem(), name)
			mstField.Set(reflect.Zero(mstField.Type()))
			cistField.Set(want)
			if reflect.DeepEqual(mstField.Interface(), want.Interface()) {
				t.Fatal("test setup: the MSTI already holds the value the sync must carry")
			}

			l.syncInstancePorts("p1", cistP)

			got := mstField.Interface()
			if !reflect.DeepEqual(got, want.Interface()) {
				t.Errorf("MSTI %s after sync = %v, want the CIST's %v", name, got, want.Interface())
			}
		})
	}
}

// TestSyncInstancePortsLeavesLinkOnCISTFieldsUntouched is evidence for the
// classLinkOnCIST rule (R4's other half), driven from portStateFieldClasses:
// for every field the table classifies classLinkOnCIST, changing the CIST's
// copy and calling syncInstancePorts leaves the MSTI's own copy exactly as it
// was, since every reader is expected to read the CIST's copy instead.
func TestSyncInstancePortsLeavesLinkOnCISTFieldsUntouched(t *testing.T) {
	t.Parallel()

	for name, spec := range portStateFieldClasses {
		if spec.class != classLinkOnCIST {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l, cistP, mstP := syncTestLayer(t)

			cistField := settableField(t, reflect.ValueOf(cistP).Elem(), name)
			cistField.Set(distinctValue(t, cistField.Type(), 2))

			mstField := settableField(t, reflect.ValueOf(mstP).Elem(), name)
			before := mstField.Interface()

			l.syncInstancePorts("p1", cistP)

			after := mstField.Interface()
			if !reflect.DeepEqual(before, after) {
				t.Errorf("MSTI %s changed by sync: got %v, want the untouched %v", name, after, before)
			}
		})
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

// TestSyncInstancePortsOnLinkDownFollowsTheLinkDownClearsFlag is evidence for
// the linkDownClears flag, driven from portStateFieldClasses instead of a
// hand-written field list: for every classTreeOwned field, setting it on the
// MSTI's own port to a distinct value and then bringing the CIST's port down
// through syncInstancePorts either overwrites that value with the table's
// recorded linkDownValue (the fields the table flags linkDownClears) or
// leaves it alone (every other tree-owned field). Asserting the specific
// cleared-to value, not just that the planted value is gone, is what catches
// a link-down branch that clears a field to the wrong thing, such as a role
// or state other than the disabled/discarding pair a dead link must produce.
// A field whose flag or value stops matching what syncInstancePorts's
// link-down branch actually does fails on that field's own name.
func TestSyncInstancePortsOnLinkDownFollowsTheLinkDownClearsFlag(t *testing.T) {
	t.Parallel()

	for name, spec := range portStateFieldClasses {
		if spec.class != classTreeOwned {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l, cistP, mstP := syncTestLayer(t)

			mstField := settableField(t, reflect.ValueOf(mstP).Elem(), name)
			set := distinctValue(t, mstField.Type(), 3)
			mstField.Set(set)

			// A planted value that already equals what the link-down branch
			// must write would assert nothing, which is how the sibling
			// replication check went vacuous for three of its four fields.
			if spec.linkDownClears && reflect.DeepEqual(set.Interface(), spec.linkDownValue) {
				t.Fatalf("test setup: the planted %s already equals linkDownValue %v", name, spec.linkDownValue)
			}

			cistP.up = false
			l.syncInstancePorts("p1", cistP)

			after := mstField.Interface()
			survived := reflect.DeepEqual(after, set.Interface())

			if spec.linkDownClears {
				if !reflect.DeepEqual(after, spec.linkDownValue) {
					t.Errorf("MSTI %s after link down = %v, want the table's linkDownValue %v",
						name, after, spec.linkDownValue)
				}
			} else if !survived {
				t.Errorf("MSTI %s changed by link down = %v, want the untouched %v "+
					"(portStateFieldClasses does not flag it linkDownClears)", name, after, set.Interface())
			}
		})
	}
}

// TestPortLinkedAgreesWithReceiveSSTPsOwnPortDownCheck is evidence for the
// divergence PortLinked closes (switch.go's Peek arm has no other read-only
// way to ask what ReceiveSSTP itself checks first): for a port this layer
// never configured, for one it configured but has not yet linked, and for
// one it has linked, PortLinked(port) is false in exactly the cases
// ReceiveSSTP itself returns SSTPPortDown, never diverging on either input.
// switch.go relies on this equivalence to make Peek agree with Forward
// without calling ReceiveSSTP itself, since Peek must not mutate the link
// half of a receive.
func TestPortLinkedAgreesWithReceiveSSTPsOwnPortDownCheck(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Priority: 32768,
		Address:  netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
		Ports: map[string]Port{
			"p1": {PathCost: 100},
			"p3": {PathCost: 100},
		},
	}.Normalize()

	l := newLayer(cfg)
	t0 := time.Unix(0, 0)
	l.LinkChange(t0, "p1", true, true, 1_000_000_000)
	// "p2" names no port at all: never configured. "p3" is configured but
	// LinkChange is never called for it, so it stays down. "p1" is linked.

	bpdu := BPDU{
		Version:      2,
		Type:         BPDUTypeRapid,
		RootID:       BridgeID{Priority: 4096, Address: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}},
		BridgeID:     BridgeID{Priority: 4096, Address: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	bpdu.SetRole(RoleDesignated)

	for _, port := range []string{"p1", "p2", "p3"} {
		linked := l.PortLinked(port)

		_, outcome := l.ReceiveSSTP(t0.Add(time.Second), port, SSTPArrival{ArrivalVID: 1, TLVVID: 1, Admitted: true}, bpdu)
		down := outcome == SSTPPortDown

		if linked == down {
			t.Errorf("port %q: PortLinked = %v, ReceiveSSTP outcome = %v (down = %v), want PortLinked == !down",
				port, linked, outcome, down)
		}
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
