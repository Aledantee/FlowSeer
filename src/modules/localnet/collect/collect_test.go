package collect_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	"go.aledante.io/FlowSeer/generated/go/mib/sysobjectid"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/collect"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

var (
	ifEntry        = snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
	ifXEntry       = snmp.MustOID(1, 3, 6, 1, 2, 1, 31, 1, 1, 1)
	ifTableRoot    = ifmib.IfTable.Descriptor().Root
	ifXTableRoot   = ifmib.IfXTable.Descriptor().Root
	sysObjectIDOID = snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 2, 0)
)

// vbFixture is one OID+VarBind pair the fake session answers from.
type vbFixture struct {
	oid snmp.OID
	vb  snmp.VarBind
}

// fakeSession answers Get, GetNext, and GetBulk from a fixture set, the
// same pattern the snmpmap tests use, and counts the bulk requests it
// receives per table root so a test can tell one walk from two. A failing
// root answers every bulk request under it with failErr.
type fakeSession struct {
	vbs      []vbFixture
	failRoot snmp.OID
	failErr  error

	mu       sync.Mutex
	roots    []snmp.OID
	requests map[string]int
}

func (s *fakeSession) Get(_ context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	out := make([]snmp.VarBind, 0, len(oids))

	for _, o := range oids {
		var vb snmp.VarBind = snmp.NoSuchObjectVar{Header: snmp.Header{OID: o, Kind: snmp.KindNoSuchObject}}
		for _, f := range s.vbs {
			if f.oid.Compare(o) == 0 {
				vb = f.vb

				break
			}
		}
		out = append(out, vb)
	}

	return out, nil
}

// GetNext serves presence probes and is not counted: the count is of
// walk requests.
func (s *fakeSession) GetNext(ctx context.Context, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	return s.next(ctx, 1, oids)
}

func (s *fakeSession) GetBulk(ctx context.Context, _ uint8, reps uint8, oids []snmp.OID, _ ...snmp.CallOption) ([]snmp.VarBind, error) {
	if len(oids) > 0 {
		s.mu.Lock()
		for _, root := range s.roots {
			if oids[0].HasPrefix(root) {
				s.requests[root.String()]++
			}
		}
		s.mu.Unlock()
	}

	return s.next(ctx, reps, oids)
}

func (s *fakeSession) next(ctx context.Context, reps uint8, oids []snmp.OID) ([]snmp.VarBind, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(oids) > 0 && s.failRoot.Len() > 0 && oids[0].HasPrefix(s.failRoot) {
		return nil, s.failErr
	}

	cur := append([]snmp.OID(nil), oids...)
	var out []snmp.VarBind
	for range reps {
		for i, o := range cur {
			var next *vbFixture
			for j := range s.vbs {
				f := &s.vbs[j]
				if f.oid.Compare(o) > 0 && (next == nil || f.oid.Compare(next.oid) < 0) {
					next = f
				}
			}
			if next == nil {
				out = append(out, snmp.EndOfMibViewVar{Header: snmp.Header{OID: o, Kind: snmp.KindEndOfMibView}})
			} else {
				out = append(out, next.vb)
				cur[i] = next.oid
			}
		}
	}

	return out, nil
}

func (s *fakeSession) Set(context.Context, []snmp.VarBind, ...snmp.CallOption) ([]snmp.VarBind, error) {
	return nil, nil
}

func (s *fakeSession) Close() error { return nil }

func (s *fakeSession) Walk(ctx context.Context, root snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	return s.pump(ctx, root)
}

func (s *fakeSession) BulkWalk(ctx context.Context, root snmp.OID, _ ...snmp.CallOption) *snmp.Walker {
	return s.pump(ctx, root)
}

func (s *fakeSession) BulkWalkRaw(ctx context.Context, root snmp.OID, opts ...snmp.CallOption) *snmp.RawWalker {
	return snmp.RawWalkerFromWalker(ctx, s.BulkWalk(ctx, root, opts...))
}

func (s *fakeSession) pump(ctx context.Context, root snmp.OID) *snmp.Walker {
	subtree := make([]vbFixture, 0, len(s.vbs))
	for _, f := range s.vbs {
		if f.oid.HasPrefix(root) {
			subtree = append(subtree, f)
		}
	}

	slices.SortFunc(subtree, func(a, b vbFixture) int { return a.oid.Compare(b.oid) })

	w := snmp.NewWalker(ctx, 64)
	w.Pump(func(_ context.Context) {
		for _, f := range subtree {
			if !w.Send(f.oid, f.vb) {
				return
			}
		}
	})

	return w
}

// count starts counting bulk requests under root.
func (s *fakeSession) count(root snmp.OID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.requests == nil {
		s.requests = make(map[string]int)
	}

	s.roots = append(s.roots, root)
	s.requests[root.String()] = 0
}

func (s *fakeSession) counted(root snmp.OID) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.requests[root.String()]
}

func integerVar(entry snmp.OID, col, idx uint32, value int32) vbFixture {
	oid := entry.Append(col, idx)

	return vbFixture{oid: oid, vb: snmp.Integer32Var{Header: snmp.Header{OID: oid, Kind: snmp.KindInteger32}, Value: value}}
}

func stringVar(entry snmp.OID, col, idx uint32, value string) vbFixture {
	oid := entry.Append(col, idx)

	return vbFixture{oid: oid, vb: snmp.OctetStringVar{Header: snmp.Header{OID: oid, Kind: snmp.KindOctetString}, Value: []byte(value)}}
}

func oidVar(oid, value snmp.OID) vbFixture {
	return vbFixture{oid: oid, vb: snmp.ObjectIDVar{Header: snmp.Header{OID: oid, Kind: snmp.KindObjectID}, Value: value}}
}

// identified adds a sysObjectID no vendored MIB names to vbs, for a cycle
// whose subject is not identity.
func identified(vbs []vbFixture) []vbFixture {
	return append(vbs, oidVar(sysObjectIDOID, snmp.MustOID(1, 3, 6, 1, 4, 1, 4294967295, 1)))
}

// ifRows is an agent with two interfaces in ifTable and one ifXTable row.
func ifRows() []vbFixture {
	return []vbFixture{
		stringVar(ifEntry, 2, 1, "eth0"),
		integerVar(ifEntry, 3, 1, 6),
		stringVar(ifEntry, 2, 2, "eth1"),
		integerVar(ifEntry, 3, 2, 6),
		stringVar(ifXEntry, 1, 1, "Ethernet0"),
	}
}

var (
	descrRead = collect.NewTable[ifmib.IfTableRow](ifmib.IfTable.Descriptor(), ifmib.IfTable.Walk, ifmib.IfDescr)
	typeRead  = collect.NewTable[ifmib.IfTableRow](ifmib.IfTable.Descriptor(), ifmib.IfTable.Walk, ifmib.IfType)
	nameRead  = collect.NewTable[ifmib.IfXTableRow](ifmib.IfXTable.Descriptor(), ifmib.IfXTable.Walk, ifmib.IfName)
)

// testMapper is a mapper whose Map reports which columns it observed on
// each ifTable row, and the ifXTable names when it declared that table.
type testMapper struct {
	spec collect.Spec
	fn   func(snap *collect.Snapshot) (any, error)
}

func (m testMapper) Spec() collect.Spec                      { return m.spec }
func (m testMapper) Map(snap *collect.Snapshot) (any, error) { return m.fn(snap) }

// descrMapper reads ifDescr from ifTable and returns the names.
var descrMapper = testMapper{
	spec: collect.Spec{Name: "descr", Required: []collect.TableRead{descrRead}},
	fn: func(snap *collect.Snapshot) (any, error) {
		var names []string
		for _, r := range descrRead.Rows(snap) {
			if r.Observed(ifmib.IfDescr) {
				names = append(names, r.IfDescr)
			}
		}

		return names, descrRead.Err(snap)
	},
}

// typeMapper reads ifType from ifTable and ifName from the optional
// ifXTable, declining on a failed ifTable and degrading on a failed
// ifXTable.
var typeMapper = testMapper{
	spec: collect.Spec{Name: "types", Required: []collect.TableRead{typeRead}, Optional: []collect.TableRead{nameRead}},
	fn: func(snap *collect.Snapshot) (any, error) {
		if err := typeRead.Err(snap); err != nil {
			return nil, err
		}

		var out []string
		for _, r := range typeRead.Rows(snap) {
			if r.Observed(ifmib.IfType) {
				out = append(out, "type")
			}
		}
		for _, r := range nameRead.Rows(snap) {
			out = append(out, r.IfName)
		}

		return out, nameRead.Err(snap)
	},
}

func TestApplies(t *testing.T) {
	vendorA := snmp.MustOID(1, 3, 6, 1, 4, 1, 9)
	device := vendorA.Append(1, 2)

	tests := []struct {
		name string
		vbs  []vbFixture
		spec collect.Spec
		oid  snmp.OID
		want bool
	}{
		{"required present", ifRows(), collect.Spec{Required: []collect.TableRead{descrRead}}, device, true},
		{"required absent", nil, collect.Spec{Required: []collect.TableRead{descrRead}}, device, false},
		{"optional absent still applies", ifRows()[:2], collect.Spec{Required: []collect.TableRead{descrRead}, Optional: []collect.TableRead{nameRead}}, device, true},
		{"prefix match", ifRows(), collect.Spec{Required: []collect.TableRead{descrRead}, SysObjectIDPrefixes: []snmp.OID{vendorA}}, device, true},
		{"prefix miss", ifRows(), collect.Spec{Required: []collect.TableRead{descrRead}, SysObjectIDPrefixes: []snmp.OID{snmp.MustOID(1, 3, 6, 1, 4, 1, 11)}}, device, false},
		{"prefix with unknown identity", ifRows(), collect.Spec{SysObjectIDPrefixes: []snmp.OID{vendorA}}, snmp.OID{}, false},
		{"no requirements", nil, collect.Spec{}, snmp.OID{}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := collect.Applies(context.Background(), &fakeSession{vbs: tc.vbs}, tc.spec, tc.oid)
			if err != nil {
				t.Fatalf("Applies: %v", err)
			}

			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestApplies_ProbeFailure(t *testing.T) {
	sess := &fakeSession{vbs: ifRows(), failRoot: ifTableRoot, failErr: errs.Msg("timeout")}

	got, err := collect.Applies(context.Background(), sess, collect.Spec{Name: "descr", Required: []collect.TableRead{descrRead}}, snmp.OID{})
	if got {
		t.Error("got applies, want not applicable on a failed probe")
	}

	if !errors.Is(err, errs.New().Code(collect.ErrCodePresence).Msg("probe")) {
		t.Errorf("got %v, want a presence code", err)
	}
}

// TestCollect_SharedTableWalkedOnce runs two mappers over ifTable, each
// needing a different column, and checks the table costs the same
// number of bulk requests as one mapper asking for both columns would.
func TestCollect_SharedTableWalkedOnce(t *testing.T) {
	baseline := &fakeSession{vbs: ifRows()}
	baseline.count(ifTableRoot)

	both := collect.NewTable[ifmib.IfTableRow](ifmib.IfTable.Descriptor(), ifmib.IfTable.Walk, ifmib.IfDescr, ifmib.IfType)
	collect.Read(context.Background(), baseline, testMapper{spec: collect.Spec{Required: []collect.TableRead{both}}})

	sess := &fakeSession{vbs: identified(ifRows())}
	sess.count(ifTableRoot)

	cycle, err := collect.New(descrMapper, typeMapper).Collect(context.Background(), sess)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if got, want := sess.counted(ifTableRoot), baseline.counted(ifTableRoot); got != want {
		t.Errorf("got %d bulk requests under ifTable, want %d (one walk for both mappers)", got, want)
	}

	if len(cycle.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(cycle.Results))
	}

	names, _ := cycle.Results[0].Output.([]string)
	if !slices.Equal(names, []string{"eth0", "eth1"}) {
		t.Errorf("descr mapper got %v, want both ifDescr values", names)
	}

	types, _ := cycle.Results[1].Output.([]string)
	if !slices.Equal(types, []string{"type", "type", "Ethernet0"}) {
		t.Errorf("type mapper got %v, want both ifType values and the ifXTable name", types)
	}
}

// TestCollect_FailedOptionalTableIsolated fails ifXTable, which only the
// type mapper reads as an optional table, and checks the descr mapper's
// output is untouched while the type mapper degrades.
func TestCollect_FailedOptionalTableIsolated(t *testing.T) {
	walkErr := errs.Msg("agent stopped answering")
	sess := &fakeSession{vbs: identified(ifRows()), failRoot: ifXTableRoot, failErr: walkErr}

	cycle, err := collect.New(descrMapper, typeMapper).Collect(context.Background(), sess)
	if !errors.Is(err, walkErr) {
		t.Fatalf("got %v, want the ifXTable failure reported", err)
	}

	if len(cycle.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(cycle.Results))
	}

	descr := cycle.Results[0]
	if descr.Err != nil {
		t.Errorf("descr mapper got error %v, want none", descr.Err)
	}

	if names, _ := descr.Output.([]string); !slices.Equal(names, []string{"eth0", "eth1"}) {
		t.Errorf("descr mapper got %v, want both interfaces", names)
	}

	types := cycle.Results[1]
	if !errors.Is(types.Err, walkErr) {
		t.Errorf("type mapper got %v, want the ifXTable failure", types.Err)
	}

	if out, _ := types.Output.([]string); !slices.Equal(out, []string{"type", "type"}) {
		t.Errorf("type mapper got %v, want the ifTable rows without names", out)
	}
}

func TestCollect_Identity(t *testing.T) {
	known := sysobjectid.Entries()[0]
	unknown := snmp.MustOID(1, 3, 6, 1, 4, 1, 4294967295, 1)

	tests := []struct {
		name      string
		vbs       []vbFixture
		wantOID   snmp.OID
		wantKnown bool
		wantErr   error
	}{
		{"resolved", []vbFixture{oidVar(sysObjectIDOID, known.OID.Append(7))}, known.OID.Append(7), true, nil},
		{"unresolved keeps the OID", []vbFixture{oidVar(sysObjectIDOID, unknown)}, unknown, false, nil},
		{"unreadable", nil, snmp.OID{}, false, errs.New().Code(collect.ErrCodeIdentity).Msg("identity")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sess := &fakeSession{vbs: append(ifRows(), tc.vbs...)}

			cycle, err := collect.New(descrMapper).Collect(context.Background(), sess)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("Collect: %v", err)
			}

			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}

			if !cycle.Identity.SysObjectID.Equal(tc.wantOID) {
				t.Errorf("got sysObjectID %v, want %v", cycle.Identity.SysObjectID, tc.wantOID)
			}

			if cycle.Identity.Known != tc.wantKnown {
				t.Errorf("got known %v, want %v", cycle.Identity.Known, tc.wantKnown)
			}

			if tc.wantKnown && cycle.Identity.Entry.Name != known.Name {
				t.Errorf("got entry %q, want %q", cycle.Identity.Entry.Name, known.Name)
			}

			if len(cycle.Results) != 1 {
				t.Errorf("got %d results, want the prefix-free mapper to run regardless of identity", len(cycle.Results))
			}
		})
	}
}

// TestCollect_SkippedMapperNotRead checks a mapper whose required table
// is absent neither runs nor costs a walk of its optional tables.
func TestCollect_SkippedMapperNotRead(t *testing.T) {
	sess := &fakeSession{vbs: identified(ifRows()[4:])}
	sess.count(ifXTableRoot)

	cycle, err := collect.New(typeMapper).Collect(context.Background(), sess)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if len(cycle.Results) != 0 {
		t.Errorf("got %d results, want none for an absent required table", len(cycle.Results))
	}

	if got := sess.counted(ifXTableRoot); got != 0 {
		t.Errorf("got %d bulk requests under ifXTable, want none for a skipped mapper", got)
	}
}
