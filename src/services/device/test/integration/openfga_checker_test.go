//go:build authz_integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
)

func wantIntegrationCode(t *testing.T, err error, want errs.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want %s", want)
	}
	got, ok := errs.CodeOf(err)
	if !ok {
		t.Fatalf("error carries no code: %v", err)
	}
	if got != want {
		t.Errorf("error code = %s, want %s (%v)", got, want, err)
	}
}

func writeTempKeyFile(t testing.TB, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "psk.key")
	if err := os.WriteFile(p, []byte(content+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile key: %v", err)
	}
	return p
}

func TestOpenFGACheckerAgainstServer(t *testing.T) {
	env := startOpenFGAEnv(t, nil)

	keyFile := writeTempKeyFile(t, testPresharedKey)
	wrongKeyFile := writeTempKeyFile(t, "wrong-preshared-key")

	// The embedded model is the one the server returns.
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

	// An absent store is a store mismatch.
	const absentStoreID = "01JK9999999999999999999999"
	_, err = openfga.New(context.Background(), openfga.Options{
		Endpoint: "https://" + env.endpoint,
		StoreID:  absentStoreID,
		ModelID:  env.modelID,
		KeyFile:  keyFile,
		CAFile:   env.certPath,
	})
	wantIntegrationCode(t, err, openfga.ErrCodeStoreMismatch)

	// An absent model is a model mismatch.
	const absentModelID = "01JK9999999999999999999998"
	_, err = openfga.New(context.Background(), openfga.Options{
		Endpoint: "https://" + env.endpoint,
		StoreID:  env.storeID,
		ModelID:  absentModelID,
		KeyFile:  keyFile,
		CAFile:   env.certPath,
	})
	wantIntegrationCode(t, err, openfga.ErrCodeModelMismatch)

	// A stored model one relation short of the embedded one is a model mismatch.
	embModel, err := openfga.Model()
	if err != nil {
		t.Fatalf("openfga.Model: %v", err)
	}
	shortModel := proto.Clone(embModel).(*openfgav1.AuthorizationModel)
	for _, td := range shortModel.GetTypeDefinitions() {
		if td.GetType() == "tenant" {
			delete(td.Relations, "full_payload")
			delete(td.GetMetadata().GetRelations(), "full_payload")
		}
	}
	shortResp, err := env.client.WriteAuthorizationModel(env.AuthContext(context.Background()), &openfgav1.WriteAuthorizationModelRequest{
		StoreId:         env.storeID,
		SchemaVersion:   shortModel.GetSchemaVersion(),
		TypeDefinitions: shortModel.GetTypeDefinitions(),
		Conditions:      shortModel.GetConditions(),
	})
	if err != nil {
		t.Fatalf("write short model: %v", err)
	}
	_, err = openfga.New(context.Background(), openfga.Options{
		Endpoint: "https://" + env.endpoint,
		StoreID:  env.storeID,
		ModelID:  shortResp.GetAuthorizationModelId(),
		KeyFile:  keyFile,
		CAFile:   env.certPath,
	})
	wantIntegrationCode(t, err, openfga.ErrCodeModelMismatch)

	// A wrong key is refused.
	_, err = openfga.New(context.Background(), openfga.Options{
		Endpoint: "https://" + env.endpoint,
		StoreID:  env.storeID,
		ModelID:  env.modelID,
		KeyFile:  wrongKeyFile,
		CAFile:   env.certPath,
	})
	wantIntegrationCode(t, err, openfga.ErrCodeRefused)

	// A batch holding one query twice answers both.
	if err := env.WriteTuple(context.Background(), "user:alice", "claimed", "platform:global"); err != nil {
		t.Fatalf("WriteTuple: %v", err)
	}
	if err := env.WriteTuple(context.Background(), "user:alice", "enrolled", "platform:global"); err != nil {
		t.Fatalf("WriteTuple: %v", err)
	}
	q := authz.Query{
		Object:   "platform:global",
		Relation: "admin",
		User:     "user:alice",
	}
	batchAnswers, err := checker.BatchCheck(context.Background(), []authz.Query{q, q})
	if err != nil {
		t.Fatalf("BatchCheck duplicate query: %v", err)
	}
	if len(batchAnswers) != 2 || !batchAnswers[0] || !batchAnswers[1] {
		t.Fatalf("got %v, want [true, true]", batchAnswers)
	}

	// The engine answers a ListStores call with no key with status 1010.
	_, err = env.client.ListStores(context.Background(), &openfgav1.ListStoresRequest{})
	if err == nil {
		t.Fatal("expected error calling ListStores without auth metadata")
	}
	if code := status.Code(err); code != 1010 {
		t.Fatalf("ListStores without metadata: got code %d (%v), want 1010", code, err)
	}

	// The engine answers a ListStores call with a wrong key with status 1500.
	wrongCtx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer wrong-key")
	_, err = env.client.ListStores(wrongCtx, &openfgav1.ListStoresRequest{})
	if err == nil {
		t.Fatal("expected error calling ListStores with wrong key")
	}
	if code := status.Code(err); code != 1500 {
		t.Fatalf("ListStores with wrong key: got code %d (%v), want 1500", code, err)
	}
}
