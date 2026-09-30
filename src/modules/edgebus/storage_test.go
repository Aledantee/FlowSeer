package edgebus_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

func TestAttachEdgeRefusedPastStoreCeiling(t *testing.T) {
	ctx := context.Background()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:           t.TempDir(),
		FsyncPolicy:        service.BusFsyncPeriodic,
		MaxStoreBytes:      640 << 20,
		CentralBudgetBytes: 512 << 20,
		EdgeBudgetBytes:    128 << 20,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	defer hub.Close()

	if err := hub.AttachEdge(ctx, "0192e6a0-0000-7000-8000-000000000001"); err != nil {
		t.Fatalf("first edge attach: %v", err)
	}

	err = hub.AttachEdge(ctx, "0192e6a0-0000-7000-8000-000000000002")
	if err == nil {
		t.Fatal("second edge attach succeeded past store ceiling")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "storage") {
		t.Fatalf("second edge attach error %q does not contain storage", err.Error())
	}
}

func TestAttachEdgeRefusedPastStoreCeilingAfterFailedAttach(t *testing.T) {
	ctx := context.Background()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:           t.TempDir(),
		FsyncPolicy:        service.BusFsyncPeriodic,
		MaxStoreBytes:      640 << 20,
		CentralBudgetBytes: 512 << 20,
		EdgeBudgetBytes:    128 << 20,
		EdgeStreamMaxBytes: 256 << 20,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	defer hub.Close()

	if err := hub.AttachEdge(ctx, "0192e6a0-0000-7000-8000-000000000001"); err == nil {
		t.Fatal("first edge attach succeeded with a stream larger than its account budget")
	}

	secondCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	err = hub.AttachEdge(secondCtx, "0192e6a0-0000-7000-8000-000000000002")
	if err == nil {
		t.Fatal("second edge attach succeeded past store ceiling")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "storage") {
		t.Fatalf("second edge attach error %q does not contain storage", err.Error())
	}
}
