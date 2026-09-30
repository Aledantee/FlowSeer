package edgebus

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

func startStorageTestHub(t *testing.T, edgeStreamMaxBytes int64) *Hub {
	t.Helper()
	hub, err := StartHub(context.Background(), HubConfig{
		StateDir:           t.TempDir(),
		FsyncPolicy:        service.BusFsyncPeriodic,
		MaxStoreBytes:      640 << 20,
		CentralBudgetBytes: 512 << 20,
		EdgeBudgetBytes:    128 << 20,
		EdgeStreamMaxBytes: edgeStreamMaxBytes,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	return hub
}

func TestAttachEdgeRefusedPastStoreCeiling(t *testing.T) {
	ctx := context.Background()
	hub := startStorageTestHub(t, 0)

	if err := hub.AttachEdge(ctx, "0192e6a0-0000-7000-8000-000000000001"); err != nil {
		t.Fatalf("first edge attach: %v", err)
	}

	err := hub.AttachEdge(ctx, "0192e6a0-0000-7000-8000-000000000002")
	if err == nil {
		t.Fatal("second edge attach succeeded past store ceiling")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "storage") {
		t.Fatalf("second edge attach error %q does not contain storage", err.Error())
	}
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeStorage {
		t.Fatalf("second edge attach error code = %q, want %q", code, ErrCodeStorage)
	}
}

func TestAttachEdgeRefusedPastStoreCeilingAfterFailedAttach(t *testing.T) {
	for _, test := range []struct {
		name    string
		context func(context.Context) (context.Context, context.CancelFunc)
	}{
		{
			name: "canceled",
			context: func(ctx context.Context) (context.Context, context.CancelFunc) {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				return canceled, func() {}
			},
		},
		{
			name: "deadline",
			context: func(ctx context.Context) (context.Context, context.CancelFunc) {
				return context.WithTimeout(ctx, 50*time.Millisecond)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			hub := startStorageTestHub(t, 256<<20)
			ctx := context.Background()

			firstErr := hub.AttachEdge(ctx, "0192e6a0-0000-7000-8000-000000000001")
			if firstErr == nil {
				t.Fatal("first edge attach succeeded with a stream larger than its account budget")
			}
			if code, ok := errs.CodeOf(firstErr); !ok || code != ErrCodeHub {
				t.Fatalf("first edge attach error code = %q, want %q", code, ErrCodeHub)
			}
			if !strings.Contains(strings.ToLower(firstErr.Error()), "storage") {
				t.Fatalf("first edge attach error %q does not contain storage", firstErr.Error())
			}

			secondCtx, cancel := test.context(ctx)
			defer cancel()
			secondErr := hub.AttachEdge(secondCtx, "0192e6a0-0000-7000-8000-000000000002")
			if secondErr == nil {
				t.Fatal("second edge attach succeeded past store ceiling")
			}
			if !strings.Contains(strings.ToLower(secondErr.Error()), "storage") {
				t.Fatalf("second edge attach error %q does not contain storage", secondErr.Error())
			}
			if code, ok := errs.CodeOf(secondErr); !ok || code != ErrCodeStorage {
				t.Fatalf("second edge attach error code = %q, want %q", code, ErrCodeStorage)
			}
		})
	}
}

func TestAttachEdgeCanceledOnFreshHubIsNotStorage(t *testing.T) {
	hub := startStorageTestHub(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := hub.AttachEdge(ctx, "0192e6a0-0000-7000-8000-000000000001")
	if err == nil {
		t.Fatal("canceled edge attach succeeded")
	}
	if strings.Contains(strings.ToLower(err.Error()), "storage") {
		t.Fatalf("canceled edge attach error %q incorrectly names storage", err.Error())
	}
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeHub {
		t.Fatalf("canceled edge attach error code = %q, want %q", code, ErrCodeHub)
	}

	if err := hub.AttachEdge(context.Background(), "0192e6a0-0000-7000-8000-000000000001"); err != nil {
		t.Fatalf("retry edge attach: %v", err)
	}
}

func TestAttachEdgeRetryAfterFailedStreamSetupIsNotStorage(t *testing.T) {
	hub := startStorageTestHub(t, 256<<20)

	err := hub.AttachEdge(context.Background(), "0192e6a0-0000-7000-8000-000000000001")
	if err == nil {
		t.Fatal("edge attach succeeded with a stream larger than its account budget")
	}
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeHub {
		t.Fatalf("failed stream setup error code = %q, want %q", code, ErrCodeHub)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "storage") {
		t.Fatalf("failed stream setup error %q does not contain storage", err.Error())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = hub.AttachEdge(ctx, "0192e6a0-0000-7000-8000-000000000001")
	if err == nil {
		t.Fatal("retry edge attach succeeded")
	}
	if strings.Contains(strings.ToLower(err.Error()), "storage") {
		t.Fatalf("retry edge attach error %q incorrectly names storage", err.Error())
	}
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeHub {
		t.Fatalf("retry edge attach error code = %q, want %q", code, ErrCodeHub)
	}
}

func TestAttachEdgeCloseRaceStillNamesStorage(t *testing.T) {
	hub := startStorageTestHub(t, 0)
	if err := hub.AttachEdge(context.Background(), "0192e6a0-0000-7000-8000-000000000001"); err != nil {
		t.Fatalf("first edge attach: %v", err)
	}

	connected := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHook := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseHook)
	hub.edgeAccountConnectHook = func() {
		close(connected)
		<-release
	}

	attachDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spawn.Go(ctx, "test edge attach", func() {
		attachDone <- hub.AttachEdge(ctx, "0192e6a0-0000-7000-8000-000000000002")
	})
	<-connected

	closeStarted := make(chan struct{})
	closeDone := make(chan struct{})
	spawn.Go(context.Background(), "test hub close", func() {
		close(closeStarted)
		hub.Close()
		close(closeDone)
	})
	<-closeStarted
	select {
	case <-closeDone:
		t.Fatal("Hub.Close returned while attach held the account read lock")
	case <-time.After(100 * time.Millisecond):
	}

	releaseHook()
	err := <-attachDone
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeStorage {
		t.Fatalf("racing attach error code = %q, want %q", code, ErrCodeStorage)
	}
	<-closeDone
}

func TestAttachEdgeCloseRaceFittingAccountIsNotStorage(t *testing.T) {
	hub := startStorageTestHub(t, 0)

	connected := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHook := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseHook)
	hub.edgeAccountConnectHook = func() {
		close(connected)
		<-release
	}

	attachDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spawn.Go(ctx, "test edge attach", func() {
		attachDone <- hub.AttachEdge(ctx, "0192e6a0-0000-7000-8000-000000000001")
	})
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("edge account connect hook was not called")
	}

	closeStarted := make(chan struct{})
	closeDone := make(chan struct{})
	spawn.Go(context.Background(), "test hub close", func() {
		close(closeStarted)
		hub.Close()
		close(closeDone)
	})
	<-closeStarted
	select {
	case <-closeDone:
		t.Fatal("Hub.Close returned while attach held the account read lock")
	case <-time.After(100 * time.Millisecond):
	}

	releaseHook()
	err := <-attachDone
	if strings.Contains(strings.ToLower(err.Error()), "storage") {
		t.Fatalf("fitting edge attach error %q incorrectly names storage", err.Error())
	}
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeHub {
		t.Fatalf("fitting edge attach error code = %q, want %q", code, ErrCodeHub)
	}
	<-closeDone
}
