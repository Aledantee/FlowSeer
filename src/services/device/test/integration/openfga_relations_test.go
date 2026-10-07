//go:build authz_integration

package integration_test

import (
	"context"
	"fmt"
	"testing"

	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
)

func TestOpenFGARelationsAgainstServer(t *testing.T) {
	env := startOpenFGAEnv(t, nil)
	keyFile := writeTempKeyFile(t, testPresharedKey)

	checker, err := openfga.New(context.Background(), openfga.Options{
		Endpoint: "https://" + env.endpoint,
		StoreID:  env.storeID,
		ModelID:  env.modelID,
		KeyFile:  keyFile,
		CAFile:   env.certPath,
	})
	if err != nil {
		t.Fatalf("openfga.New against server: %v", err)
	}
	defer func() { _ = checker.Close() }()

	ctx := context.Background()

	t.Run("duplicate write and delete of an absent tuple succeed", func(t *testing.T) {
		tuple := authz.Tuple{
			Object:   "edge:e-dup-test",
			Relation: "tenant",
			User:     "tenant:t1",
		}
		// A duplicate write succeeds with OnDuplicate="ignore".
		err := checker.Write(ctx, []authz.Tuple{tuple, tuple}, nil)
		if err != nil {
			t.Fatalf("duplicate write failed: %v", err)
		}
		err = checker.Write(ctx, []authz.Tuple{tuple}, nil)
		if err != nil {
			t.Fatalf("second write of same tuple failed: %v", err)
		}

		// A delete of an absent tuple succeeds with OnMissing="ignore".
		absentTuple := authz.Tuple{
			Object:   "edge:e-never-existed",
			Relation: "tenant",
			User:     "tenant:t-never-existed",
		}
		err = checker.Write(ctx, nil, []authz.Tuple{absentTuple})
		if err != nil {
			t.Fatalf("delete of absent tuple failed: %v", err)
		}

		// Clean up tuple so store has zero tuples before subsequent tests.
		if err := checker.Write(ctx, nil, []authz.Tuple{tuple}); err != nil {
			t.Fatalf("clean up tuple: %v", err)
		}
	})

	t.Run("write to capture_session#manage fails with authz/engine-protocol", func(t *testing.T) {
		tuple := authz.Tuple{
			Object:   "capture_session:0192e6a0-0000-7000-8000-000000000001",
			Relation: "manage",
			User:     "user:u1",
		}
		err := checker.Write(ctx, []authz.Tuple{tuple}, nil)
		wantIntegrationCode(t, err, openfga.ErrCodeProtocol)
	})

	t.Run("Scan returns 250 written tuples and Check is true after Write and false after delete", func(t *testing.T) {
		tuples := make([]authz.Tuple, 250)
		for i := range tuples {
			tuples[i] = authz.Tuple{
				Object:   fmt.Sprintf("edge:0192e6a0-0000-7000-8000-%012x", i+1),
				Relation: "tenant",
				User:     "tenant:t-scan-test",
			}
		}

		err := checker.Write(ctx, tuples, nil)
		if err != nil {
			t.Fatalf("Write 250 tuples: %v", err)
		}

		var scanned []authz.Tuple
		err = checker.Scan(ctx, func(t authz.Tuple) error {
			scanned = append(scanned, t)
			return nil
		})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(scanned) != 250 {
			t.Errorf("Scan got %d tuples, want 250", len(scanned))
		}

		targetTuple := tuples[0]
		allowed, err := checker.Check(ctx, authz.Query{
			Object:   targetTuple.Object,
			Relation: targetTuple.Relation,
			User:     targetTuple.User,
		})
		if err != nil {
			t.Fatalf("Check target tuple: %v", err)
		}
		if !allowed {
			t.Errorf("Check target tuple got allowed false, want true after Write")
		}

		// Delete the target tuple and verify Check is false (stop condition).
		err = checker.Write(ctx, nil, []authz.Tuple{targetTuple})
		if err != nil {
			t.Fatalf("Delete target tuple: %v", err)
		}

		allowed, err = checker.Check(ctx, authz.Query{
			Object:   targetTuple.Object,
			Relation: targetTuple.Relation,
			User:     targetTuple.User,
		})
		if err != nil {
			t.Fatalf("Check deleted tuple: %v", err)
		}
		if allowed {
			t.Errorf("Check deleted tuple got allowed true, want false after delete (stop condition)")
		}
	})
}
