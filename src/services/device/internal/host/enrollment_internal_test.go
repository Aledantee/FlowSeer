package host

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
)

const (
	enrollmentTenant = "0192e6a0-0000-7000-8000-00000000000a"
	enrollmentEdge   = "0192e6a0-0000-7000-8000-0000000000ed"
)

func storedEdgeWith(setupKeyID string, publicKey []byte) *storev1.StoredEdge {
	ref := edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(enrollmentEdge)}.Build()}.Build()
	return storev1.StoredEdge_builder{
		Record: edgev1.EdgeRecord_builder{
			Config: edgev1.EdgeConfig_builder{Ref: ref}.Build(),
			State: edgev1.EdgeState_builder{
				Ref:       ref,
				PublicKey: publicKey,
				SetupKey:  edgev1.SetupKey_builder{Id: proto.String(setupKeyID)}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
}

// The raw window service tells a re-enrolled edge from a rekeyed one by this
// value, so it must follow the consumed setup key and ignore the edge key.
func TestEdgeEnrollmentFollowsTheConsumedSetupKeyAndNotTheEdgeKey(t *testing.T) {
	ctx := context.Background()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	kv, err := hub.JetStream().KeyValue(ctx, edgebus.EdgeBucket)
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	edges := edgestore.New(kv)
	res := &busResources{edges: edges}

	const firstKey, secondKey = "aaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbb"
	write := func(setupKeyID string, publicKey []byte) {
		t.Helper()
		_, err := edges.Mutate(ctx, enrollmentTenant, enrollmentEdge, func(*storev1.StoredEdge) (*storev1.StoredEdge, error) {
			return storedEdgeWith(setupKeyID, publicKey), nil
		})
		if err != nil {
			t.Fatalf("write edge: %v", err)
		}
	}
	enrollment := func() string {
		t.Helper()
		got, err := res.edgeEnrollment(ctx, enrollmentTenant, enrollmentEdge)
		if err != nil {
			t.Fatalf("edgeEnrollment: %v", err)
		}
		return got
	}

	write(firstKey, []byte("edge-key-one"))
	first := enrollment()
	if first != firstKey {
		t.Fatalf("enrollment = %q, want the consumed setup key %q", first, firstKey)
	}

	write(firstKey, []byte("edge-key-two"))
	if got := enrollment(); got != first {
		t.Fatalf("a rekey changed the enrollment from %q to %q", first, got)
	}

	write(secondKey, []byte("edge-key-three"))
	if got := enrollment(); got != secondKey {
		t.Fatalf("a re-enrollment gave %q, want a new enrollment %q distinct from %q", got, secondKey, first)
	}
}
