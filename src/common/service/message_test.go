package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestAtomicPublishAdmissionHonorsCanceledContext(t *testing.T) {
	runtime := &messageRuntime{atomicPermit: make(chan struct{}, 1)}
	runtime.atomicPermit <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.publishAtomic(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("publishAtomic() error = %v, want canceled context", err)
	}
}

func TestAtomicPublishValidatesEveryRecordBeforeStaging(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	runtime := newMessageRuntime(resources, nil, nil, telemetry{})
	makeEnvelope := func(id, target string, payload []byte) *servicev1.Message {
		return servicev1.Message_builder{
			Kind:          messageKindEvent.Enum(),
			MessageId:     proto.String(id),
			CorrelationId: proto.String(id),
			SourcePath:    proto.String("edge/publisher"),
			TargetPath:    proto.String(target),
			TypeName:      proto.String("google.protobuf.Empty"),
			Payload:       payload,
		}.Build()
	}
	err := runtime.publishAtomic(ctx, []*servicev1.Message{
		makeEnvelope("3eb8263d-f907-4637-802b-597c86974949", "edge/first", []byte{}),
		makeEnvelope("3eb8263d-f907-4637-802b-597c86974949", "edge/second", make([]byte, int(resources.connection.MaxPayload()))),
	})
	if err == nil {
		t.Fatal("oversized atomic publish succeeded")
	}
	info, err := resources.mailbox.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 0 {
		t.Fatalf("failed atomic preflight staged %d records", info.State.Msgs)
	}
}

func TestValidateSubscriptionsBuildsExplicitResolverAndRoutes(t *testing.T) {
	setup := testSetup()
	cfg := Config{Identity: testIdentity(), Modules: []Module{
		{Name: "commands", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{
			Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Aliases: []protoreflect.FullName{"legacy.Empty"}, Retries: 2,
		}}}},
		{Name: "events_a", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_EVENT, Message: &durationpb.Duration{}}}}},
		{Name: "events_b", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_EVENT, Message: &durationpb.Duration{}}}}},
		{Name: "replies", Leaf: &Leaf{Setup: setup, DeliveryConcurrency: 4, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_REPLY, Message: &emptypb.Empty{}}}}},
	}}

	declaration, err := validateDeclaration(cfg)
	if err != nil {
		t.Fatalf("validateDeclaration() error: %v", err)
	}
	if ok := declaration.registry.target("edge/commands", servicev1.MessageKind_MESSAGE_KIND_COMMAND, "google.protobuf.Empty"); !ok {
		t.Error("command target is not registered")
	}
	if ok := declaration.registry.target("edge/replies", servicev1.MessageKind_MESSAGE_KIND_REPLY, "google.protobuf.Empty"); !ok {
		t.Error("reply target is not registered")
	}
	if got, ok := declaration.registry.eventSubscribers("google.protobuf.Duration"); !ok || !equalStrings(got, []string{"edge/events_a", "edge/events_b"}) {
		t.Errorf("event subscribers = %v", got)
	}
	message, canonical, ok := declaration.registry.resolver.resolve("legacy.Empty")
	if !ok || canonical != "google.protobuf.Empty" || message.ProtoReflect().Descriptor().FullName() != canonical {
		t.Errorf("alias resolution = %T, %q, %t", message, canonical, ok)
	}
	if _, _, ok := declaration.registry.resolver.resolve("google.protobuf.Timestamp"); ok {
		t.Fatal("unregistered global protobuf type resolved")
	}
	second, _, ok := declaration.registry.resolver.resolve("legacy.Empty")
	if !ok || message == second {
		t.Fatal("resolver did not construct a fresh protobuf message")
	}
	events, _ := declaration.registry.eventSubscribers("google.protobuf.Duration")
	events[0] = "mutated"
	if got, ok := declaration.registry.eventSubscribers("google.protobuf.Duration"); !ok || got[0] != "edge/events_a" {
		t.Errorf("registry event subscribers were mutable: %v", got)
	}
	if got := declaration.modules[3].leaf.deliveryConcurrency; got != 4 {
		t.Errorf("delivery concurrency = %d, want 4", got)
	}
}

func TestValidateSubscriptionsRejectsInvalidDeclarations(t *testing.T) {
	setup := testSetup()
	var nilEmpty *emptypb.Empty
	tests := []struct {
		name string
		leaf Leaf
	}{
		{name: "unspecified kind", leaf: Leaf{Setup: setup, Subscriptions: []Subscription{{Message: &emptypb.Empty{}}}}},
		{name: "nil prototype", leaf: Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND}}}},
		{name: "typed nil prototype", leaf: Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: nilEmpty}}}},
		{name: "invalid alias", leaf: Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Aliases: []protoreflect.FullName{"bad-name"}}}}},
		{name: "canonical alias", leaf: Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Aliases: []protoreflect.FullName{"google.protobuf.Empty"}}}}},
		{name: "duplicate alias", leaf: Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Aliases: []protoreflect.FullName{"legacy.Empty", "legacy.Empty"}}}}},
		{name: "retry below bound", leaf: Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Retries: -1}}}},
		{name: "retry above bound", leaf: Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}, Retries: maxSubscriptionRetries + 1}}}},
		{name: "concurrency below bound", leaf: Leaf{Setup: setup, DeliveryConcurrency: -1}},
		{name: "concurrency above bound", leaf: Leaf{Setup: setup, DeliveryConcurrency: maxDeliveryConcurrency + 1}},
		{name: "duplicate subscription", leaf: Leaf{Setup: setup, Subscriptions: []Subscription{
			{Kind: servicev1.MessageKind_MESSAGE_KIND_EVENT, Message: &emptypb.Empty{}},
			{Kind: servicev1.MessageKind_MESSAGE_KIND_EVENT, Message: &emptypb.Empty{}},
		}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{Name: "worker", Leaf: &tt.leaf}}})
			if err == nil {
				t.Fatal("validateDeclaration() succeeded, want error")
			}
			if code, ok := errs.CodeOf(err); !ok || code.String() != "service/subscription" {
				t.Errorf("error code = %q, %t", code, ok)
			}
		})
	}
}

func TestValidateSubscriptionsDefaultsDeliveryConcurrencyToOne(t *testing.T) {
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Setup: testSetup()})
	if err != nil {
		t.Fatalf("validateDeclaration() error: %v", err)
	}
	if got := declaration.modules[0].leaf.deliveryConcurrency; got != 1 {
		t.Errorf("delivery concurrency = %d, want 1", got)
	}
}

func TestValidateSubscriptionsAllowsSameAddressedTypeAtDistinctTargets(t *testing.T) {
	setup := testSetup()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{
		{Name: "one", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}}}}},
		{Name: "two", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}}}}},
	}})
	if err != nil {
		t.Fatalf("validateDeclaration() error: %v", err)
	}
	for _, path := range []string{"edge/one", "edge/two"} {
		if !declaration.registry.target(path, servicev1.MessageKind_MESSAGE_KIND_COMMAND, "google.protobuf.Empty") {
			t.Errorf("command target %q is not registered", path)
		}
	}
}

func TestValidateSubscriptionsRejectsAliasConflicts(t *testing.T) {
	setup := testSetup()
	tests := []struct {
		name string
		mods []Module
	}{
		{
			name: "alias maps to different canonical types",
			mods: []Module{
				{Name: "one", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_EVENT, Message: &emptypb.Empty{}, Aliases: []protoreflect.FullName{"legacy.Message"}}}}},
				{Name: "two", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: servicev1.MessageKind_MESSAGE_KIND_EVENT, Message: &durationpb.Duration{}, Aliases: []protoreflect.FullName{"legacy.Message"}}}}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateDeclaration(Config{Identity: testIdentity(), Modules: tt.mods})
			if err == nil {
				t.Fatal("validateDeclaration() succeeded, want error")
			}
			if code, ok := errs.CodeOf(err); !ok || code.String() != "service/message-type" {
				t.Errorf("error code = %q, %t", code, ok)
			}
		})
	}
}

func TestAdmissionRevisionMatrixAndImmutability(t *testing.T) {
	setup := testSetup()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{
		{Name: "target", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{
			{Kind: servicev1.MessageKind_MESSAGE_KIND_COMMAND, Message: &emptypb.Empty{}},
			{Kind: servicev1.MessageKind_MESSAGE_KIND_REPLY, Message: &durationpb.Duration{}},
			{Kind: servicev1.MessageKind_MESSAGE_KIND_EVENT, Message: &emptypb.Empty{}},
		}}},
	}})
	if err != nil {
		t.Fatalf("validateDeclaration() error: %v", err)
	}

	preflight := newAdmissionRevision(declaration.registry)
	assertAdmission(t, preflight, false)
	old := preflight.withPhase(admissionActive).withModuleState("edge/target", moduleRunning)
	assertAdmission(t, old, true)
	for _, state := range []moduleState{moduleRunning, moduleSetupFailed, moduleRestarting, moduleBackoff} {
		t.Run(state.String(), func(t *testing.T) {
			assertAdmission(t, old.withModuleState("edge/target", state), true)
		})
	}
	for _, state := range []moduleState{moduleDisabled, moduleStopped} {
		t.Run(state.String(), func(t *testing.T) {
			assertAdmission(t, old.withModuleState("edge/target", state), false)
		})
	}
	assertAdmission(t, old.withPhase(admissionShuttingDown), false)
	assertAdmission(t, old, true)
}

func TestAdmissionRevisionRejectsUnregisteredEvent(t *testing.T) {
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Setup: testSetup()})
	if err != nil {
		t.Fatal(err)
	}
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive)
	if _, err := revision.admitEvent("google.protobuf.Empty"); err == nil {
		t.Fatal("unregistered event was admitted")
	} else if code, ok := errs.CodeOf(err); !ok || code != errCodeMessageType {
		t.Fatalf("unregistered event code = %q, %t; want %q", code, ok, errCodeMessageType)
	}
}

func assertAdmission(t *testing.T, revision *admissionRevision, want bool) {
	t.Helper()
	commandErr := revision.admitTarget("edge/target", servicev1.MessageKind_MESSAGE_KIND_COMMAND, "google.protobuf.Empty")
	replyErr := revision.admitTarget("edge/target", servicev1.MessageKind_MESSAGE_KIND_REPLY, "google.protobuf.Duration")
	events, eventErr := revision.admitEvent("google.protobuf.Empty")
	commandOK := commandErr == nil
	replyOK := replyErr == nil
	eventOK := eventErr == nil && len(events) == 1 && events[0] == "edge/target"
	if commandOK != want || replyOK != want || eventOK != want {
		t.Errorf("admission = command:%v reply:%v events:%v/%v, want admitted %t", commandErr, replyErr, events, eventErr, want)
	}
	for _, err := range []error{commandErr, replyErr, eventErr} {
		if want || err == nil {
			continue
		}
		if code, ok := errs.CodeOf(err); !ok || code.String() != "service/admission" {
			t.Errorf("rejection code = %q, %t", code, ok)
		}
	}
}

var _ HandlerFunc = func(context.Context, proto.Message) error { return nil }
