package authztest_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/authztest"
)

func TestEngine_WriteAndRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := authztest.New()

	tuple1 := authz.Tuple{Object: "device:d1", Relation: "operate", User: "user:u1"}
	tuple2 := authz.Tuple{Object: "device:d1", Relation: "view", User: "user:u2"}

	// Initial write
	if err := e.Write(ctx, []authz.Tuple{tuple1, tuple2}, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	read, err := e.Read(ctx, "device:d1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(read) != 2 || !slices.Contains(read, tuple1) || !slices.Contains(read, tuple2) {
		t.Fatalf("Read = %v, want [%v, %v]", read, tuple1, tuple2)
	}

	// Idempotent write of same tuple
	if err := e.Write(ctx, []authz.Tuple{tuple1}, nil); err != nil {
		t.Fatalf("Write duplicate: %v", err)
	}
	read, err = e.Read(ctx, "device:d1")
	if err != nil {
		t.Fatalf("Read after duplicate: %v", err)
	}
	if len(read) != 2 {
		t.Fatalf("Read count after duplicate = %d, want 2 (idempotent write)", len(read))
	}

	// Delete
	if err := e.Write(ctx, nil, []authz.Tuple{tuple1}); err != nil {
		t.Fatalf("Write delete: %v", err)
	}
	read, err = e.Read(ctx, "device:d1")
	if err != nil {
		t.Fatalf("Read after delete: %v", err)
	}
	if len(read) != 1 || read[0] != tuple2 {
		t.Fatalf("Read after delete = %v, want [%v]", read, tuple2)
	}

	// Idempotent delete of absent tuple
	if err := e.Write(ctx, nil, []authz.Tuple{tuple1}); err != nil {
		t.Fatalf("Write delete missing: %v", err)
	}
	read, err = e.Read(ctx, "device:d1")
	if err != nil {
		t.Fatalf("Read after missing delete: %v", err)
	}
	if len(read) != 1 {
		t.Fatalf("Read count after missing delete = %d, want 1", len(read))
	}
}

func TestEngine_CheckStoredAndContextual(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := authztest.New()

	storedTuple := authz.Tuple{Object: "device:d1", Relation: "operate", User: "user:u1"}
	contextualTuple := authz.Tuple{Object: "tenant:t1", Relation: "member", User: "user:u1"}

	if err := e.Write(ctx, []authz.Tuple{storedTuple}, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Stored tuple match
	ok, err := e.Check(ctx, authz.Query{Object: "device:d1", Relation: "operate", User: "user:u1"})
	if err != nil || !ok {
		t.Fatalf("Check stored = (%v, %v), want (true, nil)", ok, err)
	}

	// Unstored tuple mismatch
	ok, err = e.Check(ctx, authz.Query{Object: "device:d2", Relation: "operate", User: "user:u1"})
	if err != nil || ok {
		t.Fatalf("Check unstored = (%v, %v), want (false, nil)", ok, err)
	}

	// Contextual tuple match
	ok, err = e.Check(ctx, authz.Query{
		Object:           "tenant:t1",
		Relation:         "member",
		User:             "user:u1",
		ContextualTuples: []authz.Tuple{contextualTuple},
	})
	if err != nil || !ok {
		t.Fatalf("Check contextual = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestEngine_Grant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := authztest.New()

	e.Grant("user:alice", "download", "capture_session")

	// Matches any capture_session object
	ok, err := e.Check(ctx, authz.Query{Object: "capture_session:s1", Relation: "download", User: "user:alice"})
	if err != nil || !ok {
		t.Fatalf("Check grant s1 = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = e.Check(ctx, authz.Query{Object: "capture_session:s2", Relation: "download", User: "user:alice"})
	if err != nil || !ok {
		t.Fatalf("Check grant s2 = (%v, %v), want (true, nil)", ok, err)
	}

	// Does not match other object type
	ok, err = e.Check(ctx, authz.Query{Object: "device:d1", Relation: "download", User: "user:alice"})
	if err != nil || ok {
		t.Fatalf("Check grant wrong type = (%v, %v), want (false, nil)", ok, err)
	}

	// Does not match other relation
	ok, err = e.Check(ctx, authz.Query{Object: "capture_session:s1", Relation: "delete", User: "user:alice"})
	if err != nil || ok {
		t.Fatalf("Check grant wrong relation = (%v, %v), want (false, nil)", ok, err)
	}

	// Does not match other user
	ok, err = e.Check(ctx, authz.Query{Object: "capture_session:s1", Relation: "download", User: "user:bob"})
	if err != nil || ok {
		t.Fatalf("Check grant wrong user = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestEngine_Scan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := authztest.New()

	tuples := []authz.Tuple{
		{Object: "device:d1", Relation: "operate", User: "user:u1"},
		{Object: "device:d2", Relation: "operate", User: "user:u2"},
		{Object: "device:d3", Relation: "operate", User: "user:u3"},
	}
	if err := e.Write(ctx, tuples, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	var scanned []authz.Tuple
	err := e.Scan(ctx, func(t authz.Tuple) error {
		scanned = append(scanned, t)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(scanned) != 3 {
		t.Fatalf("Scan count = %d, want 3", len(scanned))
	}

	// Scan stops on error
	scanned = nil
	expectedErr := errors.New("stop scan")
	err = e.Scan(ctx, func(t authz.Tuple) error {
		scanned = append(scanned, t)
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("Scan error = %v, want %v", err, expectedErr)
	}
	if len(scanned) != 1 {
		t.Fatalf("Scan count after error = %d, want 1", len(scanned))
	}
}

func TestEngine_ConcurrentUse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := authztest.New()

	const workers = 8
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := range workers {
		workerID := i
		spawn.Go(ctx, fmt.Sprintf("test.worker.%d", workerID), func() {
			defer wg.Done()
			for j := range iterations {
				obj := fmt.Sprintf("device:%d", j%5)
				user := fmt.Sprintf("user:%d", workerID)
				tuple := authz.Tuple{Object: obj, Relation: "operate", User: user}

				_ = e.Write(ctx, []authz.Tuple{tuple}, nil)
				_, _ = e.Read(ctx, obj)
				_, _ = e.Check(ctx, authz.Query{Object: obj, Relation: "operate", User: user})
				_ = e.Scan(ctx, func(authz.Tuple) error { return nil })
				if j%2 == 0 {
					_ = e.Write(ctx, nil, []authz.Tuple{tuple})
				}
			}
		})
	}

	wg.Wait()
}
