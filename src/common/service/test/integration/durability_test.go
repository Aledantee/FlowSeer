package integration_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
	"go.aledante.io/FlowSeer/src/common/service"
)

func TestPublicBusPublishesAndDeliversDurableCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	delivered := make(chan struct{})
	setupResult := make(chan error, 1)
	config := service.Config{
		Identity: service.Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		Bus:      &service.BusConfig{StoreDir: filepath.Join(t.TempDir(), "bus")},
		Modules: []service.Module{{
			Name: "worker",
			Leaf: &service.Leaf{
				Subscriptions: []service.Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}}},
				Setup: func(attemptCtx context.Context) (service.Attempt, error) {
					setupResult <- service.Bus(attemptCtx).Command(attemptCtx, "edge/worker", &emptypb.Empty{})
					return service.Attempt{
						Runner: func(runCtx context.Context) error {
							<-runCtx.Done()
							return nil
						},
						Handlers: []service.Handler{{
							Kind:    servicev1.MessageKind_MESSAGE_KIND_COMMAND,
							Message: &emptypb.Empty{},
							Handle: func(context.Context, proto.Message) error {
								close(delivered)
								return nil
							},
						}},
					}, nil
				},
			},
		}},
	}
	runDone := make(chan error, 1)
	go func() { runDone <- service.Run(ctx, config) }()
	select {
	case err := <-setupResult:
		if err != nil {
			t.Fatalf("Command() error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-delivered:
		cancel()
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error: %v", err)
	}
}
