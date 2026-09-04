package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
)

func BenchmarkCommandPublish(b *testing.B) {
	bus := benchmarkMessageBus(b, 1)
	ctx := context.Background()
	payload := &emptypb.Empty{}
	b.ResetTimer()
	for range b.N {
		if err := bus.Command(ctx, "bench/target_0", payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEventFanoutPublish(b *testing.B) {
	bus := benchmarkMessageBus(b, 8)
	ctx := context.Background()
	payload := &emptypb.Empty{}
	b.ResetTimer()
	for range b.N {
		if err := bus.Publish(ctx, payload); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkMessageBus(b *testing.B, eventSubscribers int) *MessageBus {
	b.Helper()
	modules := make([]Module, eventSubscribers)
	for index := range modules {
		subscriptions := []Subscription{{Kind: messageKindEvent, Message: &emptypb.Empty{}}}
		if index == 0 {
			subscriptions = append(subscriptions, Subscription{Kind: messageKindCommand, Message: &emptypb.Empty{}})
		}
		modules[index] = Module{
			Name: "target_" + strconv.Itoa(index),
			Leaf: &Leaf{Setup: testSetup(), Subscriptions: subscriptions},
		}
	}
	config := Config{
		Identity: Identity{Namespace: "flowseer", Name: "bench", Version: "1.0.0"},
		Bus: &BusConfig{
			StoreDir:         b.TempDir(),
			MaxStoreBytes:    1 << 30,
			MailboxMaxBytes:  1<<30 - 16<<20,
			MetadataMaxBytes: 8 << 20,
			ReserveBytes:     8 << 20,
			HealthInterval:   time.Minute,
		},
		Modules: modules,
	}
	runtime, err := preflight(context.Background(), config, mapLookup(nil))
	if err != nil {
		b.Fatal(err)
	}
	observability, err := newTelemetry(config)
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	local, err := startLocalBus(ctx, *runtime.bus, reconcileRuntimeManifest(runtime))
	cancel()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := local.close(closeCtx, true); err != nil {
			b.Error(err)
		}
	})
	admission := newAdmissionState(runtime.admission.withModuleSnapshot(runtime.modules))
	messages := newMessageRuntime(local.resources, runtime.registry, admission.load, observability)
	return messages.capability("bench/publisher", context.Background())
}
