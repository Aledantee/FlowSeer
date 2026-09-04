package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestRuntimeManifestIsDeterministicAndIncludesDisabledModules(t *testing.T) {
	config := manifestTestConfig(t, []Module{
		{Name: "zeta", Gate: FixedGate(false), Leaf: &Leaf{Setup: testSetup()}},
		{Name: "alpha", Leaf: &Leaf{Setup: testSetup()}},
	})
	runtime, err := preflight(context.Background(), config, mapLookup(map[string]string{
		"FLOWSEER_BUS_TEST_ZETA_ENABLED": "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtimeManifest(runtime)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtimeManifest(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(first, second) {
		t.Fatal("identical declarations produced different manifests")
	}
	if got, want := first.GetModulePaths(), []string{"bus_test/alpha", "bus_test/zeta"}; !equalStrings(got, want) {
		t.Fatalf("manifest paths = %v, want %v", got, want)
	}
	if got := first.GetModules(); len(got) != 2 || got[0].GetPath() != "bus_test/alpha" || got[1].GetPath() != "bus_test/zeta" {
		t.Fatalf("manifest modules = %v, want sorted static leaves", got)
	}
}

func TestRuntimeManifestSortsSubscriptionsByKindAndType(t *testing.T) {
	config := manifestTestConfig(t, []Module{{
		Name: "worker",
		Leaf: &Leaf{
			Setup: testSetup(),
			Subscriptions: []Subscription{
				{Kind: MessageKindEvent, Message: &wrapperspb.Int32Value{}},
				{Kind: MessageKindCommand, Message: &wrapperspb.Int32Value{}},
				{Kind: MessageKindCommand, Message: &emptypb.Empty{}},
			},
		},
	}})
	runtime, err := preflight(context.Background(), config, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := runtimeManifest(runtime)
	if err != nil {
		t.Fatal(err)
	}
	subscriptions := manifest.GetModules()[0].GetSubscriptions()
	if len(subscriptions) != 3 {
		t.Fatalf("manifest subscriptions = %v, want three", subscriptions)
	}
	wantKinds := []servicev1.MessageKind{MessageKindCommand, MessageKindCommand, MessageKindEvent}
	wantTypes := []string{"google.protobuf.Empty", "google.protobuf.Int32Value", "google.protobuf.Int32Value"}
	for i, subscription := range subscriptions {
		if subscription.GetKind() != wantKinds[i] || subscription.GetTypeName() != wantTypes[i] {
			t.Fatalf("manifest subscription %d = (%v, %q), want (%v, %q)", i, subscription.GetKind(), subscription.GetTypeName(), wantKinds[i], wantTypes[i])
		}
	}
}

func TestManifestReconciliationAcceptsAdditionsAndRejectsRemovals(t *testing.T) {
	storeDir := t.TempDir()
	base := manifestTestConfigWithStore(storeDir, []Module{{Name: "first", Leaf: &Leaf{Setup: testSetup()}}})
	additive := manifestTestConfigWithStore(storeDir, []Module{
		{Name: "first", Leaf: &Leaf{Setup: testSetup()}},
		{Name: "second", Leaf: &Leaf{Setup: testSetup()}},
	})

	startAndCloseManifestBus(t, base)
	startAndCloseManifestBus(t, additive)

	runtime, err := preflight(context.Background(), base, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = startLocalBus(ctx, *runtime.bus, reconcileRuntimeManifest(runtime))
	if err == nil {
		t.Fatal("reconciliation accepted a persisted module removal")
	}
	if code, ok := errs.CodeOf(err); !ok || code != errCodeBusMigration {
		t.Fatalf("removal error code = %q, %t; want %q: %v", code, ok, errCodeBusMigration, err)
	}

	startAndCloseManifestBus(t, additive)
}

func TestStoreProvenanceRejectsDifferentNATSPinBeforeOpen(t *testing.T) {
	config, err := normalizeBusConfig(testBusIdentity(), BusConfig{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := reconcileStoreProvenance(config); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.storeDir, storeProvenanceFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	provenance := &servicev1.StoreProvenance{}
	if err := proto.Unmarshal(data, provenance); err != nil {
		t.Fatal(err)
	}
	provenance.SetNatsVersion("1.0.0")
	data, err = proto.MarshalOptions{Deterministic: true}.Marshal(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = startLocalBus(ctx, config, nil)
	if err == nil {
		t.Fatal("different NATS pin opened the existing store")
	}
	if code, ok := errs.CodeOf(err); !ok || code != errCodeBusMigration {
		t.Fatalf("pin error code = %q, %t; want %q: %v", code, ok, errCodeBusMigration, err)
	}
	if _, err := os.Stat(filepath.Join(config.storeDir, "jetstream")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("NATS touched store before provenance rejection: %v", err)
	}
}

func TestStoreProvenanceIgnoresInterruptedOwnedTemporaryFile(t *testing.T) {
	storeDir := t.TempDir()
	config, err := normalizeBusConfig(testBusIdentity(), BusConfig{StoreDir: storeDir})
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(storeDir, storeProvenanceFile+".new-interrupted")
	if err := os.WriteFile(temporary, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reconcileStoreProvenance(config); err != nil {
		t.Fatalf("owned temporary file blocked provenance creation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeDir, storeProvenanceFile)); err != nil {
		t.Fatalf("provenance was not created: %v", err)
	}
}

func TestPreparedManifestCanResumeOrRestore(t *testing.T) {
	for _, restorePrevious := range []bool{false, true} {
		name := "resume_desired"
		if restorePrevious {
			name = "restore_previous"
		}
		t.Run(name, func(t *testing.T) {
			storeDir := t.TempDir()
			baseConfig := manifestTestConfigWithStore(storeDir, []Module{{Name: "first", Leaf: &Leaf{Setup: testSetup()}}})
			additiveConfig := manifestTestConfigWithStore(storeDir, []Module{
				{Name: "first", Leaf: &Leaf{Setup: testSetup()}},
				{Name: "second", Leaf: &Leaf{Setup: testSetup()}},
			})
			base, err := preflight(context.Background(), baseConfig, mapLookup(nil))
			if err != nil {
				t.Fatal(err)
			}
			additive, err := preflight(context.Background(), additiveConfig, mapLookup(nil))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			bus, err := startLocalBus(ctx, *base.bus, reconcileRuntimeManifest(base))
			if err != nil {
				t.Fatal(err)
			}
			current, sequence, err := readReconciliation(ctx, bus.resources.metadata)
			if err != nil {
				t.Fatal(err)
			}
			desired, err := runtimeManifest(additive)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := newReconciliation(current.GetDesired(), desired, servicev1.ReconciliationPhase_RECONCILIATION_PHASE_PREPARED)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := publishReconciliation(ctx, bus.resources, prepared, sequence); err != nil {
				t.Fatal(err)
			}
			closeBus(t, bus, true)

			starting := additive
			if restorePrevious {
				starting = base
			}
			bus, err = startLocalBus(ctx, *starting.bus, reconcileRuntimeManifest(starting))
			if err != nil {
				t.Fatalf("reconcile prepared manifest: %v", err)
			}
			defer closeBus(t, bus, true)
			committed, _, err := readReconciliation(ctx, bus.resources.metadata)
			if err != nil {
				t.Fatal(err)
			}
			want, err := runtimeManifest(starting)
			if err != nil {
				t.Fatal(err)
			}
			if committed.GetPhase() != servicev1.ReconciliationPhase_RECONCILIATION_PHASE_COMMITTED || !proto.Equal(committed.GetDesired(), want) {
				t.Fatalf("committed manifest = %v, want %v", committed, want)
			}
		})
	}
}

func TestSettlementReconciliationRemovesOnlyOrphans(t *testing.T) {
	config := manifestTestConfig(t, []Module{{Name: "worker", Leaf: &Leaf{Setup: testSetup()}}})
	runtime, err := preflight(context.Background(), config, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bus, err := startLocalBus(ctx, *runtime.bus, reconcileRuntimeManifest(runtime))
	if err != nil {
		t.Fatal(err)
	}

	target := "bus_test/worker"
	messageID := "7d92135e-832f-4a83-aeda-27f0d66b0d11"
	envelope := servicev1.Message_builder{
		Kind:       servicev1.MessageKind_MESSAGE_KIND_COMMAND.Enum(),
		MessageId:  proto.String(messageID),
		SourcePath: proto.String("bus_test/source"),
		TargetPath: proto.String(target),
		TypeName:   proto.String("google.protobuf.Empty"),
		Payload:    []byte{},
	}.Build()
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := mailboxSubject(target, envelope.GetKind(), envelope.GetTypeName())
	if err != nil {
		t.Fatal(err)
	}
	ack, err := bus.resources.jetStream.Publish(ctx, subject, data)
	if err != nil {
		t.Fatal(err)
	}
	messages := newMessageRuntime(bus.resources, runtime.registry, nil, telemetry{})
	extantSubject := settlementSubject(target, messageID)
	if _, err := messages.commitSettlement(ctx, extantSubject, 0, ack.Sequence,
		newSettlement(target, messageID, 0, servicev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE)); err != nil {
		t.Fatal(err)
	}
	orphanID := "00ccde45-1fb6-42ef-a4ad-aadf03f298dd"
	orphanSubject := settlementSubject(target, orphanID)
	if _, err := messages.commitSettlement(ctx, orphanSubject, 0, ack.Sequence+1000,
		newSettlement(target, orphanID, 0, servicev1.SettlementState_SETTLEMENT_STATE_DISCARD)); err != nil {
		t.Fatal(err)
	}
	closeBus(t, bus, true)

	bus, err = startLocalBus(ctx, *runtime.bus, reconcileRuntimeManifest(runtime))
	if err != nil {
		t.Fatal(err)
	}
	defer closeBus(t, bus, true)
	if _, err := bus.resources.metadata.GetLastMsgForSubject(ctx, extantSubject); err != nil {
		t.Fatalf("matching settlement removed: %v", err)
	}
	if _, err := bus.resources.metadata.GetLastMsgForSubject(ctx, orphanSubject); !errors.Is(err, jetstream.ErrMsgNotFound) {
		t.Fatalf("orphaned settlement remains: %v", err)
	}
}

func manifestTestConfig(t *testing.T, modules []Module) Config {
	t.Helper()
	return manifestTestConfigWithStore(t.TempDir(), modules)
}

func manifestTestConfigWithStore(storeDir string, modules []Module) Config {
	return Config{
		Identity: testBusIdentity(),
		Bus: &BusConfig{
			StoreDir:         storeDir,
			MaxStoreBytes:    8 << 20,
			MailboxMaxBytes:  4 << 20,
			MetadataMaxBytes: 2 << 20,
			ReserveBytes:     2 << 20,
			HealthInterval:   10 * time.Millisecond,
		},
		Modules: modules,
	}
}

func startAndCloseManifestBus(t *testing.T, config Config) {
	t.Helper()
	runtime, err := preflight(context.Background(), config, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bus, err := startLocalBus(ctx, *runtime.bus, reconcileRuntimeManifest(runtime))
	if err != nil {
		t.Fatal(err)
	}
	closeBus(t, bus, true)
}
