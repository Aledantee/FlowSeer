package openfga_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
)

func TestLazyVerificationFirstCheckAndConcurrency(t *testing.T) {
	t.Run("first check makes GetStore, ReadAuthorizationModel, and Check", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		if harness.fake.getStoreCallsCount.Load() != 0 {
			t.Errorf("got %d GetStore calls before Check, want 0", harness.fake.getStoreCallsCount.Load())
		}
		if harness.fake.readModelCallsCount.Load() != 0 {
			t.Errorf("got %d ReadAuthorizationModel calls before Check, want 0", harness.fake.readModelCallsCount.Load())
		}
		if harness.fake.checkCallsCount.Load() != 0 {
			t.Errorf("got %d Check calls before Check, want 0", harness.fake.checkCallsCount.Load())
		}

		allowed, err := checker.Check(context.Background(), validQuery)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if !allowed {
			t.Errorf("got allowed %v, want true", allowed)
		}

		if harness.fake.getStoreCallsCount.Load() != 1 {
			t.Errorf("got %d GetStore calls, want 1", harness.fake.getStoreCallsCount.Load())
		}
		if harness.fake.readModelCallsCount.Load() != 1 {
			t.Errorf("got %d ReadAuthorizationModel calls, want 1", harness.fake.readModelCallsCount.Load())
		}
		if harness.fake.checkCallsCount.Load() != 1 {
			t.Errorf("got %d Check calls, want 1", harness.fake.checkCallsCount.Load())
		}
	})

	t.Run("20 concurrent first calls make one start check", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		const concurrency = 20
		var wg sync.WaitGroup
		wg.Add(concurrency)

		for i := 0; i < concurrency; i++ {
			spawn.Go(context.Background(), fmt.Sprintf("test.concurrent.check.%d", i), func() {
				defer wg.Done()
				allowed, err := checker.Check(context.Background(), validQuery)
				if err != nil {
					t.Errorf("concurrent Check: %v", err)
				}
				if !allowed {
					t.Errorf("got allowed %v, want true", allowed)
				}
			})
		}
		wg.Wait()

		if harness.fake.getStoreCallsCount.Load() != 1 {
			t.Errorf("got %d GetStore calls, want 1", harness.fake.getStoreCallsCount.Load())
		}
		if harness.fake.readModelCallsCount.Load() != 1 {
			t.Errorf("got %d ReadAuthorizationModel calls, want 1", harness.fake.readModelCallsCount.Load())
		}
		if harness.fake.checkCallsCount.Load() != concurrency {
			t.Errorf("got %d Check calls, want %d", harness.fake.checkCallsCount.Load(), concurrency)
		}
	})
}

func TestLazyVerificationFailureCoolDown(t *testing.T) {
	harness := newTestServerHarness(t)

	var mu sync.Mutex
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	checker := newChecker(t, harness, func(o *openfga.Options) {
		o.Clock = clock
	})

	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = func(_ context.Context, _ *openfgav1.GetStoreRequest) (*openfgav1.GetStoreResponse, error) {
		return nil, status.Error(codes.Unavailable, "unavailable")
	}
	harness.fake.mu.Unlock()

	// First call fails with engine-unreachable.
	_, err := checker.Check(context.Background(), validQuery)
	wantCode(t, err, openfga.ErrCodeUnreachable)
	if harness.fake.getStoreCallsCount.Load() != 1 {
		t.Errorf("got %d GetStore calls, want 1", harness.fake.getStoreCallsCount.Load())
	}

	// Second call inside 5 s makes no start call.
	advance(2 * time.Second)
	_, err = checker.Check(context.Background(), validQuery)
	wantCode(t, err, openfga.ErrCodeUnreachable)
	if harness.fake.getStoreCallsCount.Load() != 1 {
		t.Errorf("got %d GetStore calls inside 5s, want 1", harness.fake.getStoreCallsCount.Load())
	}

	// Server becomes healthy, clock passes 5 s -> call succeeds.
	harness.fake.mu.Lock()
	harness.fake.getStoreFunc = nil
	harness.fake.mu.Unlock()

	advance(4 * time.Second)
	allowed, err := checker.Check(context.Background(), validQuery)
	if err != nil {
		t.Fatalf("Check after cooldown: %v", err)
	}
	if !allowed {
		t.Errorf("got allowed %v, want true", allowed)
	}
	if harness.fake.getStoreCallsCount.Load() != 2 {
		t.Errorf("got %d GetStore calls after recovery, want 2", harness.fake.getStoreCallsCount.Load())
	}
}

func TestModelOneRelationShortRefusesAllMethods(t *testing.T) {
	harness := newTestServerHarness(t)
	checker := newChecker(t, harness, nil)

	// Model one relation short: remove admin relation from platform.
	harness.fake.mu.Lock()
	harness.fake.readModelFunc = func(_ context.Context, req *openfgav1.ReadAuthorizationModelRequest) (*openfgav1.ReadAuthorizationModelResponse, error) {
		emb, _ := openfga.Model()
		m := proto.Clone(emb).(*openfgav1.AuthorizationModel)
		m.Id = req.GetId()
		for _, td := range m.GetTypeDefinitions() {
			if td.GetType() == "platform" {
				delete(td.Relations, "admin")
			}
		}
		return &openfgav1.ReadAuthorizationModelResponse{AuthorizationModel: m}, nil
	}
	harness.fake.mu.Unlock()

	ctx := context.Background()
	_, err := checker.Check(ctx, validQuery)
	wantCode(t, err, openfga.ErrCodeModelMismatch)

	_, err = checker.BatchCheck(ctx, []authz.Query{validQuery})
	wantCode(t, err, openfga.ErrCodeModelMismatch)

	tuple := authz.Tuple{Object: "edge:e1", Relation: "view", User: "user:u1"}
	err = checker.Write(ctx, []authz.Tuple{tuple}, nil)
	wantCode(t, err, openfga.ErrCodeModelMismatch)

	_, err = checker.Read(ctx, "edge:e1")
	wantCode(t, err, openfga.ErrCodeModelMismatch)

	err = checker.Scan(ctx, func(authz.Tuple) error { return nil })
	wantCode(t, err, openfga.ErrCodeModelMismatch)

	if harness.fake.checkCallsCount.Load() != 0 {
		t.Errorf("got %d Check calls, want 0", harness.fake.checkCallsCount.Load())
	}
	if harness.fake.batchCheckCallsCount.Load() != 0 {
		t.Errorf("got %d BatchCheck calls, want 0", harness.fake.batchCheckCallsCount.Load())
	}
	if harness.fake.writeCallsCount.Load() != 0 {
		t.Errorf("got %d Write calls, want 0", harness.fake.writeCallsCount.Load())
	}
	if harness.fake.readCallsCount.Load() != 0 {
		t.Errorf("got %d Read calls, want 0", harness.fake.readCallsCount.Load())
	}
}

func TestWriteRelations(t *testing.T) {
	t.Run("write of 250 tuples makes calls of 100, 100, and 50 each carrying both options", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		writes := make([]authz.Tuple, 125)
		for i := range writes {
			writes[i] = authz.Tuple{
				Object:   fmt.Sprintf("edge:w%04d", i),
				Relation: "view",
				User:     fmt.Sprintf("user:u%04d", i),
			}
		}
		deletes := make([]authz.Tuple, 125)
		for i := range deletes {
			deletes[i] = authz.Tuple{
				Object:   fmt.Sprintf("edge:d%04d", i),
				Relation: "view",
				User:     fmt.Sprintf("user:u%04d", i),
			}
		}

		err := checker.Write(context.Background(), writes, deletes)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		if harness.fake.writeCallsCount.Load() != 3 {
			t.Fatalf("got %d Write calls, want 3", harness.fake.writeCallsCount.Load())
		}

		harness.fake.mu.Lock()
		reqs := harness.fake.recordedWriteReqs
		harness.fake.mu.Unlock()

		if len(reqs) != 3 {
			t.Fatalf("got %d recorded requests, want 3", len(reqs))
		}

		wantSizes := []int{100, 100, 50}
		wantDeletes := []int{100, 25, 0}
		wantWrites := []int{0, 75, 50}
		var sentWrites, sentDeletes []authz.Tuple
		for i, wantSize := range wantSizes {
			req := reqs[i]
			if req.GetAuthorizationModelId() != testModelID {
				t.Errorf("call %d model ID = %q, want %q", i, req.GetAuthorizationModelId(), testModelID)
			}
			numWrites := len(req.GetWrites().GetTupleKeys())
			numDeletes := len(req.GetDeletes().GetTupleKeys())
			total := numWrites + numDeletes
			if total != wantSize {
				t.Errorf("call %d total items = %d, want %d (w=%d, d=%d)", i, total, wantSize, numWrites, numDeletes)
			}
			if numDeletes != wantDeletes[i] || numWrites != wantWrites[i] {
				t.Errorf("call %d split = (d=%d, w=%d), want (d=%d, w=%d)", i, numDeletes, numWrites, wantDeletes[i], wantWrites[i])
			}
			if numWrites > 0 {
				if req.GetWrites().GetOnDuplicate() != "ignore" {
					t.Errorf("call %d OnDuplicate = %q, want 'ignore'", i, req.GetWrites().GetOnDuplicate())
				}
				for _, tk := range req.GetWrites().GetTupleKeys() {
					sentWrites = append(sentWrites, authz.Tuple{
						Object:   tk.GetObject(),
						Relation: tk.GetRelation(),
						User:     tk.GetUser(),
					})
				}
			} else if req.GetWrites() != nil {
				t.Errorf("call %d writes must be nil when empty", i)
			}
			if numDeletes > 0 {
				if req.GetDeletes().GetOnMissing() != "ignore" {
					t.Errorf("call %d OnMissing = %q, want 'ignore'", i, req.GetDeletes().GetOnMissing())
				}
				for _, tk := range req.GetDeletes().GetTupleKeys() {
					sentDeletes = append(sentDeletes, authz.Tuple{
						Object:   tk.GetObject(),
						Relation: tk.GetRelation(),
						User:     tk.GetUser(),
					})
				}
			} else if req.GetDeletes() != nil {
				t.Errorf("call %d deletes must be nil when empty", i)
			}
		}

		if !slices.Equal(sentWrites, writes) {
			t.Errorf("sent writes do not equal input writes")
		}
		if !slices.Equal(sentDeletes, deletes) {
			t.Errorf("sent deletes do not equal input deletes")
		}
	})

	t.Run("one-sided write asserts empty side is nil", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		tuple := authz.Tuple{Object: "edge:e1", Relation: "view", User: "user:u1"}

		// Writes only
		if err := checker.Write(context.Background(), []authz.Tuple{tuple}, nil); err != nil {
			t.Fatalf("Write: %v", err)
		}
		harness.fake.mu.Lock()
		reqs := harness.fake.recordedWriteReqs
		harness.fake.mu.Unlock()
		if len(reqs) != 1 {
			t.Fatalf("got %d reqs, want 1", len(reqs))
		}
		if reqs[0].GetDeletes() != nil {
			t.Errorf("expected deletes to be nil for writes-only call, got %v", reqs[0].GetDeletes())
		}
		if reqs[0].GetWrites() == nil {
			t.Error("expected writes to be non-nil")
		}

		// Deletes only
		harness.fake.mu.Lock()
		harness.fake.recordedWriteReqs = nil
		harness.fake.mu.Unlock()
		harness.fake.writeCallsCount.Store(0)

		if err := checker.Write(context.Background(), nil, []authz.Tuple{tuple}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		harness.fake.mu.Lock()
		reqs = harness.fake.recordedWriteReqs
		harness.fake.mu.Unlock()
		if len(reqs) != 1 {
			t.Fatalf("got %d reqs, want 1", len(reqs))
		}
		if reqs[0].GetWrites() != nil {
			t.Errorf("expected writes to be nil for deletes-only call, got %v", reqs[0].GetWrites())
		}
		if reqs[0].GetDeletes() == nil {
			t.Error("expected deletes to be non-nil")
		}
	})

	t.Run("drops repeats within writes and deletes", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		tuple1 := authz.Tuple{Object: "edge:e1", Relation: "view", User: "user:u1"}
		tuple2 := authz.Tuple{Object: "edge:e2", Relation: "view", User: "user:u2"}

		err := checker.Write(context.Background(), []authz.Tuple{tuple1, tuple1, tuple1}, []authz.Tuple{tuple2, tuple2})
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		harness.fake.mu.Lock()
		reqs := harness.fake.recordedWriteReqs
		harness.fake.mu.Unlock()

		if len(reqs) != 1 {
			t.Fatalf("got %d calls, want 1", len(reqs))
		}
		if len(reqs[0].GetWrites().GetTupleKeys()) != 1 {
			t.Errorf("got %d writes, want 1", len(reqs[0].GetWrites().GetTupleKeys()))
		}
		if len(reqs[0].GetDeletes().GetTupleKeys()) != 1 {
			t.Errorf("got %d deletes, want 1", len(reqs[0].GetDeletes().GetTupleKeys()))
		}
	})

	t.Run("nothing to send makes no call", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		err := checker.Write(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if harness.fake.writeCallsCount.Load() != 0 {
			t.Errorf("got %d Write calls, want 0", harness.fake.writeCallsCount.Load())
		}
	})

	t.Run("invalid tuple fails with engine-invalid-tuple and no call", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		badTuple := authz.Tuple{Object: "invalid", Relation: "view", User: "user:u1"}
		err := checker.Write(context.Background(), []authz.Tuple{badTuple}, nil)
		wantCode(t, err, openfga.ErrCodeInvalidTuple)

		if harness.fake.writeCallsCount.Load() != 0 {
			t.Errorf("got %d Write calls, want 0", harness.fake.writeCallsCount.Load())
		}
	})

	t.Run("invalid tuple on deletes side fails with engine-invalid-tuple and no call", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		badTuple := authz.Tuple{Object: "invalid", Relation: "view", User: "user:u1"}
		err := checker.Write(context.Background(), nil, []authz.Tuple{badTuple})
		wantCode(t, err, openfga.ErrCodeInvalidTuple)

		if harness.fake.writeCallsCount.Load() != 0 {
			t.Errorf("got %d Write calls, want 0", harness.fake.writeCallsCount.Load())
		}
	})

	t.Run("tuple in both writes and deletes fails with engine-invalid-tuple before any call", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		tuple := authz.Tuple{Object: "edge:e1", Relation: "view", User: "user:u1"}
		err := checker.Write(context.Background(), []authz.Tuple{tuple}, []authz.Tuple{tuple})
		wantCode(t, err, openfga.ErrCodeInvalidTuple)

		if harness.fake.writeCallsCount.Load() != 0 {
			t.Errorf("got %d Write calls, want 0", harness.fake.writeCallsCount.Load())
		}
	})

	t.Run("tuple in both writes and deletes past 100 tuples fails with engine-invalid-tuple before any call", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		overlap := authz.Tuple{Object: "edge:shared", Relation: "view", User: "user:shared"}
		writes := make([]authz.Tuple, 60)
		for i := range writes {
			writes[i] = authz.Tuple{
				Object:   fmt.Sprintf("edge:w%04d", i),
				Relation: "view",
				User:     fmt.Sprintf("user:u%04d", i),
			}
		}
		writes = append(writes, overlap)

		deletes := make([]authz.Tuple, 60)
		for i := range deletes {
			deletes[i] = authz.Tuple{
				Object:   fmt.Sprintf("edge:d%04d", i),
				Relation: "view",
				User:     fmt.Sprintf("user:u%04d", i),
			}
		}
		deletes = append(deletes, overlap)

		err := checker.Write(context.Background(), writes, deletes)
		wantCode(t, err, openfga.ErrCodeInvalidTuple)

		if harness.fake.writeCallsCount.Load() != 0 {
			t.Errorf("got %d Write calls, want 0", harness.fake.writeCallsCount.Load())
		}
	})

	t.Run("Aborted yields authz/engine-conflict", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		harness.fake.mu.Lock()
		harness.fake.writeFunc = func(_ context.Context, _ *openfgav1.WriteRequest) (*openfgav1.WriteResponse, error) {
			return nil, status.Error(codes.Aborted, "concurrent write conflict")
		}
		harness.fake.mu.Unlock()

		tuple := authz.Tuple{Object: "edge:e1", Relation: "view", User: "user:u1"}
		err := checker.Write(context.Background(), []authz.Tuple{tuple}, nil)
		wantCode(t, err, openfga.ErrCodeConflict)
		if !errs.Retryable(err) {
			t.Error("expected conflict error to be retryable")
		}
	})
}

func TestReadAndScanTwoPages(t *testing.T) {
	t.Run("two-page Read returns both pages", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		harness.fake.mu.Lock()
		harness.fake.readFunc = func(_ context.Context, req *openfgav1.ReadRequest) (*openfgav1.ReadResponse, error) {
			if req.GetTupleKey().GetObject() != "edge:e1" {
				return nil, status.Errorf(codes.InvalidArgument, "wrong object filter: %s", req.GetTupleKey().GetObject())
			}
			if req.GetConsistency() != openfgav1.ConsistencyPreference_HIGHER_CONSISTENCY {
				return nil, status.Errorf(codes.InvalidArgument, "wrong consistency: %v", req.GetConsistency())
			}
			if req.GetPageSize().GetValue() != 100 {
				return nil, status.Errorf(codes.InvalidArgument, "wrong page size: %d", req.GetPageSize().GetValue())
			}

			if req.GetContinuationToken() == "" {
				tuples := make([]*openfgav1.Tuple, 100)
				for i := range tuples {
					tuples[i] = &openfgav1.Tuple{
						Key: &openfgav1.TupleKey{
							Object:   "edge:e1",
							Relation: "view",
							User:     fmt.Sprintf("user:u%04d", i),
						},
					}
				}
				return &openfgav1.ReadResponse{
					Tuples:            tuples,
					ContinuationToken: "token-page-2",
				}, nil
			}

			if req.GetContinuationToken() == "token-page-2" {
				tuples := make([]*openfgav1.Tuple, 25)
				for i := range tuples {
					tuples[i] = &openfgav1.Tuple{
						Key: &openfgav1.TupleKey{
							Object:   "edge:e1",
							Relation: "view",
							User:     fmt.Sprintf("user:u%04d", 100+i),
						},
					}
				}
				return &openfgav1.ReadResponse{
					Tuples:            tuples,
					ContinuationToken: "",
				}, nil
			}

			return nil, status.Error(codes.InvalidArgument, "unexpected continuation token")
		}
		harness.fake.mu.Unlock()

		results, err := checker.Read(context.Background(), "edge:e1")
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if len(results) != 125 {
			t.Errorf("got %d tuples, want 125", len(results))
		}
		if harness.fake.readCallsCount.Load() != 2 {
			t.Errorf("got %d Read calls, want 2", harness.fake.readCallsCount.Load())
		}
	})

	t.Run("two-page Scan returns both pages sending no tuple key", func(t *testing.T) {
		harness := newTestServerHarness(t)
		checker := newChecker(t, harness, nil)

		harness.fake.mu.Lock()
		harness.fake.readFunc = func(_ context.Context, req *openfgav1.ReadRequest) (*openfgav1.ReadResponse, error) {
			if req.GetTupleKey() != nil {
				return nil, status.Errorf(codes.InvalidArgument, "expected no tuple key on Scan, got %v", req.GetTupleKey())
			}
			if req.GetConsistency() != openfgav1.ConsistencyPreference_HIGHER_CONSISTENCY {
				return nil, status.Errorf(codes.InvalidArgument, "wrong consistency: %v", req.GetConsistency())
			}
			if req.GetPageSize().GetValue() != 100 {
				return nil, status.Errorf(codes.InvalidArgument, "wrong page size: %d", req.GetPageSize().GetValue())
			}

			if req.GetContinuationToken() == "" {
				tuples := make([]*openfgav1.Tuple, 100)
				for i := range tuples {
					tuples[i] = &openfgav1.Tuple{
						Key: &openfgav1.TupleKey{
							Object:   fmt.Sprintf("edge:e%04d", i),
							Relation: "view",
							User:     fmt.Sprintf("user:u%04d", i),
						},
					}
				}
				return &openfgav1.ReadResponse{
					Tuples:            tuples,
					ContinuationToken: "token-page-2",
				}, nil
			}

			if req.GetContinuationToken() == "token-page-2" {
				tuples := make([]*openfgav1.Tuple, 25)
				for i := range tuples {
					tuples[i] = &openfgav1.Tuple{
						Key: &openfgav1.TupleKey{
							Object:   fmt.Sprintf("edge:e%04d", 100+i),
							Relation: "view",
							User:     fmt.Sprintf("user:u%04d", 100+i),
						},
					}
				}
				return &openfgav1.ReadResponse{
					Tuples:            tuples,
					ContinuationToken: "",
				}, nil
			}

			return nil, status.Error(codes.InvalidArgument, "unexpected continuation token")
		}
		harness.fake.mu.Unlock()

		var scanned []authz.Tuple
		err := checker.Scan(context.Background(), func(t authz.Tuple) error {
			scanned = append(scanned, t)
			return nil
		})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(scanned) != 125 {
			t.Errorf("got %d tuples, want 125", len(scanned))
		}
		if harness.fake.readCallsCount.Load() != 2 {
			t.Errorf("got %d Read calls, want 2", harness.fake.readCallsCount.Load())
		}
	})
}
