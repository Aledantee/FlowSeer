package service_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"

	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"go.aledante.io/FlowSeer/src/common/service"
)

// A common endpoint gives the service one managed destination for all three
// signals. The root inherits the available managed backings. A branch can turn
// one signal off while a descendant explicitly turns it back on.
func ExampleConfig_managedTelemetry() {
	config := service.Config{
		Identity: service.Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		Telemetry: service.TelemetryConfig{
			Endpoint: "https://collector.example/flowseer",
			Protocol: "http/protobuf",
		},
		Modules: []service.Module{{
			Name:      "ingest",
			Telemetry: service.TelemetryPolicy{Logs: service.TelemetryDisabled},
			Branch: &service.Branch{Children: []service.Module{
				{Name: "syslog", Leaf: &service.Leaf{Setup: serve}},
				{
					Name:      "audit",
					Telemetry: service.TelemetryPolicy{Logs: service.TelemetryEnabled},
					Leaf:      &service.Leaf{Setup: serve},
				},
			}},
		}},
	}

	fmt.Println(config.Telemetry.Endpoint)
	fmt.Println(config.Telemetry.Signals.Logs == service.TelemetryInherit)
	fmt.Println(config.Modules[0].Telemetry.Logs == service.TelemetryDisabled)
	fmt.Println(config.Modules[0].Branch.Children[1].Telemetry.Logs == service.TelemetryEnabled)
	// Output:
	// https://collector.example/flowseer
	// true
	// true
	// true
}

// A service with one unit of work declares it with Setup. The runtime names the
// implicit module after the service.
func ExampleRun() {
	err := service.Run(context.Background(), service.Config{
		Identity: service.Identity{Name: "collector", Namespace: "flowseer", Version: "v1"},
		Logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		Setup: func(context.Context) (service.Attempt, error) {
			listener, err := net.Listen("tcp", ":8080")
			if err != nil {
				return service.Attempt{}, err
			}
			return service.Attempt{Runner: func(ctx context.Context) error {
				// The runner owns what setup opened.
				defer func() { _ = listener.Close() }()
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		},
	})
	if err != nil {
		slog.Error("collector stopped", "error", err)
	}
}

// A service with several independently supervised capabilities declares a
// module tree. Each branch is its own failure boundary, and every module has a
// generated environment override such as FLOWSEER_EDGE_INGEST_SYSLOG_ENABLED.
func ExampleRun_moduleTree() {
	ingest := service.Module{Name: "ingest", Branch: &service.Branch{
		// One failing collector must not restart its sibling.
		Strategy: service.OneForOne,
		Children: []service.Module{
			{Name: "syslog", Leaf: &service.Leaf{Setup: serve}},
			{Name: "snmp", Gate: service.ProbeGate(hasTrapCapability), Leaf: &service.Leaf{Setup: serve}},
		},
	}}
	api := service.Module{
		Name: "api",
		// A normal return means the API is finished; do not restart it.
		Policy: service.Policy{Normal: service.OutcomePolicy{Action: service.Stop}},
		Leaf:   &service.Leaf{Setup: serve},
	}

	err := service.Run(context.Background(), service.Config{
		Identity: service.Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		Modules:  []service.Module{ingest, api},
	})
	if err != nil {
		slog.Error("edge stopped", "error", err)
	}
}

// Module code reads its instrumentation from the attempt context. The logger
// already carries the service identity and module path, and [service.Attributes]
// gives module-owned instruments the same dimensions the runtime records.
func ExampleAttributes() {
	setup := func(ctx context.Context) (service.Attempt, error) {
		// A module that owns an instrumentation scope names it itself.
		meter := service.MeterProvider(ctx).Meter("go.aledante.io/FlowSeer/src/edge/ingest/syslog")
		received, err := meter.Int64Counter("flowseer.syslog.messages")
		if err != nil {
			return service.Attempt{}, err
		}
		return service.Attempt{Runner: func(ctx context.Context) error {
			attributes := service.Attributes(ctx)
			ctx, span := service.Tracer(ctx).Start(ctx, "decode")
			defer span.End()

			received.Add(ctx, 1, metric.WithAttributeSet(attributes))
			service.Logger(ctx).InfoContext(ctx, "decoded a message")
			return nil
		}}, nil
	}
	_ = setup
}

// Modules exchange durable work through the service-local bus. A command names
// one target module; a reply goes back to the source of the message being
// handled.
func ExampleBus() {
	poller := service.Module{Name: "poller", Leaf: &service.Leaf{
		Subscriptions: []service.Subscription{{
			Kind:    service.MessageKindCommand,
			Message: &emptypb.Empty{},
			Retries: 3,
		}},
		Setup: func(context.Context) (service.Attempt, error) {
			return service.Attempt{
				Runner: idle,
				Handlers: []service.Handler{{
					Kind:    service.MessageKindCommand,
					Message: &emptypb.Empty{},
					Handle: func(ctx context.Context, _ proto.Message) error {
						// A handler that returns an error is retried, and the
						// message is discarded once Retries is exhausted.
						return service.Bus(ctx).Reply(ctx, &emptypb.Empty{})
					},
				}},
			}, nil
		},
	}}
	scheduler := service.Module{Name: "scheduler", Leaf: &service.Leaf{
		Setup: func(context.Context) (service.Attempt, error) {
			return service.Attempt{Runner: func(ctx context.Context) error {
				return service.Bus(ctx).Command(ctx, "edge/poller", &emptypb.Empty{})
			}}, nil
		},
	}}

	err := service.Run(context.Background(), service.Config{
		Identity: service.Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		Bus:      &service.BusConfig{},
		Modules:  []service.Module{scheduler, poller},
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("edge stopped", "error", err)
	}
}

func serve(context.Context) (service.Attempt, error) {
	return service.Attempt{Runner: idle}, nil
}

func idle(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func hasTrapCapability(ctx context.Context) (bool, error) {
	_, ok := service.LookupEnv(ctx, "TRAP_LISTEN_ADDRESS")
	return ok, nil
}
