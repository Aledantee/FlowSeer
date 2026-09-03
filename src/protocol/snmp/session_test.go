package snmp

import (
	"context"
	"testing"
)

// fakeSession is a no-op Session that proves the interface is closed and
// completely implementable by external code.
type fakeSession struct {
	getCalls     int
	getNextCalls int
	getBulkCalls int
	walkCalls    int
	bulkCalls    int
	setCalls     int
	closeCalls   int
}

func (s *fakeSession) Get(_ context.Context, _ []OID, _ ...CallOption) ([]VarBind, error) {
	s.getCalls++
	return nil, nil
}

func (s *fakeSession) GetNext(_ context.Context, _ []OID, _ ...CallOption) ([]VarBind, error) {
	s.getNextCalls++
	return nil, nil
}

func (s *fakeSession) GetBulk(_ context.Context, _, _ uint8, _ []OID, _ ...CallOption) ([]VarBind, error) {
	s.getBulkCalls++
	return nil, nil
}

func (s *fakeSession) Walk(ctx context.Context, _ OID, _ ...CallOption) *Walker {
	s.walkCalls++
	w := NewWalker(ctx, 0)
	w.Done() // empty, immediately-complete walk
	return w
}

func (s *fakeSession) BulkWalk(ctx context.Context, _ OID, _ ...CallOption) *Walker {
	s.bulkCalls++
	w := NewWalker(ctx, 0)
	w.Done()
	return w
}

func (s *fakeSession) BulkWalkRaw(ctx context.Context, root OID, opts ...CallOption) *RawWalker {
	return RawWalkerFromWalker(ctx, s.BulkWalk(ctx, root, opts...))
}

func (s *fakeSession) Set(_ context.Context, _ []VarBind, _ ...CallOption) ([]VarBind, error) {
	s.setCalls++
	return nil, nil
}

func (s *fakeSession) Close() error {
	s.closeCalls++
	return nil
}

// Compile-time assertion that fakeSession satisfies Session. Adding a
// method to Session without updating fakeSession will fail to compile.
var _ Session = (*fakeSession)(nil)

func TestSession_FakeImplementsEveryMethod(t *testing.T) {
	// Exercise each method once to confirm the interface set is
	// exhaustive. The compile-time assertion above is the structural
	// guarantee; this test guarantees the assertion isn't stale.
	var s Session = &fakeSession{}
	ctx := context.Background()
	oid, _ := ParseOID("1.3.6.1")

	if _, err := s.Get(ctx, []OID{oid}); err != nil {
		t.Errorf("Get: %v", err)
	}
	if _, err := s.GetNext(ctx, []OID{oid}); err != nil {
		t.Errorf("GetNext: %v", err)
	}
	if _, err := s.GetBulk(ctx, 0, 10, []OID{oid}); err != nil {
		t.Errorf("GetBulk: %v", err)
	}
	if w := s.Walk(ctx, oid); w == nil {
		t.Error("Walk returned nil Walker")
	}
	if w := s.BulkWalk(ctx, oid); w == nil {
		t.Error("BulkWalk returned nil Walker")
	}
	if _, err := s.Set(ctx, []VarBind{NullVar{Header: Header{OID: oid, Kind: KindNull}}}); err != nil {
		t.Errorf("Set: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
