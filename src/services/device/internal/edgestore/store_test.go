package edgestore_test

import (
	"context"
	"crypto/ed25519"
	"testing"

	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
)

const edgeID = "0192e6a0-0000-7000-8000-0000000000ed"

func newStore(t *testing.T) *edgestore.Store {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	kv, err := hub.JetStream().KeyValue(context.Background(), edgebus.EdgeBucket)
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	return edgestore.New(kv)
}

func edgeRef() *edgev1.EdgeGlobalRef {
	return edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build()}.Build()
}

func enrolledEdge(publicKey ed25519.PublicKey) *storev1.StoredEdge {
	lifecycle := edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED
	return storev1.StoredEdge_builder{
		Record: edgev1.EdgeRecord_builder{
			Config: edgev1.EdgeConfig_builder{Ref: edgeRef()}.Build(),
			State: edgev1.EdgeState_builder{
				Ref:       edgeRef(),
				Lifecycle: &lifecycle,
				PublicKey: publicKey,
			}.Build(),
		}.Build(),
	}.Build()
}

func TestLookupOfAnUnknownEdgeIsNotFoundNotAnError(t *testing.T) {
	s := newStore(t)
	key, lifecycle, err := s.Lookup(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("Lookup of an unknown edge errored: %v", err)
	}
	if key != nil {
		t.Fatal("an unknown edge returned a key; the verifier must fail it as a bad signature")
	}
	if lifecycle != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_UNSPECIFIED {
		t.Fatalf("lifecycle = %v, want unspecified", lifecycle)
	}
}

func TestMutateStoresAndLookupReadsTheKeyAndLifecycle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	public, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	if _, err := s.Mutate(ctx, edgeID, func(current *storev1.StoredEdge) (*storev1.StoredEdge, error) {
		if current != nil {
			t.Fatal("a fresh edge should have no record")
		}
		return enrolledEdge(public), nil
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	key, lifecycle, err := s.Lookup(ctx, edgeID)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !key.Equal(public) {
		t.Fatal("Lookup returned a different key than was stored")
	}
	if lifecycle != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED {
		t.Fatalf("lifecycle = %v, want enrolled", lifecycle)
	}

	// A re-key updates the stored key under CAS.
	rekeyed, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if _, err := s.Mutate(ctx, edgeID, func(current *storev1.StoredEdge) (*storev1.StoredEdge, error) {
		if current == nil {
			t.Fatal("the enrolled edge should have a record")
		}
		current.GetRecord().GetState().SetPublicKey(rekeyed)
		return current, nil
	}); err != nil {
		t.Fatalf("rekey: %v", err)
	}
	key, _, err = s.Lookup(ctx, edgeID)
	if err != nil {
		t.Fatalf("Lookup after rekey: %v", err)
	}
	if !key.Equal(rekeyed) {
		t.Fatal("Lookup did not return the re-keyed public key")
	}
}

func TestMutateSkipWritesNothing(t *testing.T) {
	s := newStore(t)
	if _, err := s.Mutate(context.Background(), edgeID, func(*storev1.StoredEdge) (*storev1.StoredEdge, error) {
		return nil, edgestore.ErrSkip
	}); err != nil {
		t.Fatalf("skip: %v", err)
	}
	if _, revision, err := s.Get(context.Background(), edgeID); err != nil || revision != 0 {
		t.Fatalf("a skipped mutate wrote a record: revision %d, err %v", revision, err)
	}
}
