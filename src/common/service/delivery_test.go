package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestBusOutsideAttemptIsSafe(t *testing.T) {
	bus := Bus(context.Background())
	if bus == nil {
		t.Fatal("Bus() returned nil")
	}
	if err := bus.Publish(context.Background(), &emptypb.Empty{}); err == nil {
		t.Fatal("Publish() succeeded without an enabled bus")
	}
}

func TestMessageBusHandleExpiresWithAttempt(t *testing.T) {
	attempt, cancel := context.WithCancel(context.Background())
	bus := (&messageRuntime{}).capability("edge/worker", attempt)
	cancel()
	if err := bus.Command(context.Background(), "edge/target", &emptypb.Empty{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Command() error = %v, want canceled attempt", err)
	}
}

func TestMessageBusPersistsCommandAndAtomicEventSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	setup := testSetup()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{
		{Name: "commands", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: messageKindCommand, Message: &emptypb.Empty{}}}}},
		{Name: "events_one", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: messageKindEvent, Message: &emptypb.Empty{}}}}},
		{Name: "events_two", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: messageKindEvent, Message: &emptypb.Empty{}}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive)
	for _, path := range []string{"edge/commands", "edge/events_one", "edge/events_two"} {
		revision = revision.withModuleState(path, moduleRunning)
	}
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMessageRuntime(resources, declaration.registry, func() *admissionRevision { return revision }, telemetry)
	bus := runtime.capability("edge/publisher")
	if err := bus.Command(ctx, "edge/commands", &emptypb.Empty{}); err != nil {
		t.Fatalf("Command() error: %v", err)
	}
	if err := bus.Publish(ctx, &emptypb.Empty{}); err != nil {
		t.Fatalf("Publish() error: %v", err)
	}
	info, err := resources.mailbox.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.State.Msgs; got != 3 {
		t.Fatalf("mailbox messages = %d, want 3", got)
	}
	seen := make(map[string]servicev1.MessageKind)
	for sequence := uint64(1); sequence <= info.State.LastSeq; sequence++ {
		raw, err := resources.mailbox.GetMsg(ctx, sequence)
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		envelope := &servicev1.Message{}
		if err := proto.Unmarshal(raw.Data, envelope); err != nil {
			t.Fatal(err)
		}
		seen[envelope.GetTargetPath()] = envelope.GetKind()
		if envelope.GetMessageId() == "" || envelope.GetCorrelationId() == "" || envelope.GetSourcePath() != "edge/publisher" {
			t.Errorf("incomplete envelope: %v", envelope)
		}
	}
	if seen["edge/commands"] != messageKindCommand || seen["edge/events_one"] != messageKindEvent || seen["edge/events_two"] != messageKindEvent {
		t.Fatalf("persisted targets = %v", seen)
	}
}

func TestMessageBusRejectsDisabledTargetWithoutPersistence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "commands", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: messageKindCommand, Message: &emptypb.Empty{}}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive)
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMessageRuntime(resources, declaration.registry, func() *admissionRevision { return revision }, telemetry)
	err = runtime.capability("edge/publisher").Command(ctx, "edge/commands", &emptypb.Empty{})
	if err == nil {
		t.Fatal("Command() succeeded for disabled target")
	}
	if code, ok := errs.CodeOf(err); !ok || code.String() != "service/admission" {
		t.Fatalf("error code = %q, %t", code, ok)
	}
	info, err := resources.mailbox.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 0 {
		t.Fatalf("disabled command persisted %d records", info.State.Msgs)
	}
}

func TestAtomicEventRejectsOversizedRecordBeforeStaging(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	setup := testSetup()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{
		{Name: "one", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: messageKindEvent, Message: &wrapperspb.BytesValue{}}}}},
		{Name: "two", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: messageKindEvent, Message: &wrapperspb.BytesValue{}}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive)
	for _, path := range []string{"edge/one", "edge/two"} {
		revision = revision.withModuleState(path, moduleRunning)
	}
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMessageRuntime(resources, declaration.registry, func() *admissionRevision { return revision }, telemetry)
	payload := &wrapperspb.BytesValue{Value: make([]byte, resources.connection.MaxPayload()+1)}
	if err := runtime.capability("edge/publisher").Publish(ctx, payload); err == nil {
		t.Fatal("Publish() accepted an oversized atomic record")
	}
	info, err := resources.mailbox.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 0 {
		t.Fatalf("oversized event staged %d records", info.State.Msgs)
	}
}

func TestDeliveryRetriesOnlyAfterCommittedTransitionAndAcknowledges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: messageKindCommand, Message: &emptypb.Empty{}, Retries: 1}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive).withModuleState("edge/worker", moduleRunning)
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMessageRuntime(resources, declaration.registry, func() *admissionRevision { return revision }, telemetry)
	if err := runtime.capability("edge/publisher").Command(ctx, "edge/worker", &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}

	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	done := make(chan error, 1)
	calls := make(chan int, 2)
	call := 0
	handlers := []Handler{{Kind: messageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
		call++
		calls <- call
		if call == 1 {
			return errs.New().Retryable().Msg("try again")
		}
		info, err := resources.metadata.Info(ctx)
		if err != nil || info.State.Msgs == 0 {
			t.Errorf("retry ran before its durable transition: messages=%d error=%v", info.State.Msgs, err)
		}
		return nil
	}}}
	go func() { done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], handlers) }()
	for want := 1; want <= 2; want++ {
		select {
		case got := <-calls:
			if got != want {
				t.Fatalf("handler call = %d, want %d", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	eventually(ctx, t, func() bool {
		info, err := resources.mailbox.Info(ctx)
		return err == nil && info.State.Msgs == 0
	})
	stopDelivery()
	if err := <-done; err != nil {
		t.Fatalf("runDelivery() error: %v", err)
	}
	eventually(ctx, t, func() bool {
		info, err := resources.metadata.Info(ctx)
		return err == nil && info.State.Msgs == 0
	})
}

func TestCancellationDuringBackoffDoesNotCommitRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: messageKindCommand, Message: &emptypb.Empty{}, Retries: 1}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive).withModuleState("edge/worker", moduleRunning)
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMessageRuntime(resources, declaration.registry, func() *admissionRevision { return revision }, telemetry)
	if err := runtime.capability("edge/publisher").Command(ctx, "edge/worker", &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	done := make(chan error, 1)
	failed := make(chan struct{})
	go func() {
		done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: messageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
			close(failed)
			return errs.New().Retryable().Msg("try again")
		}}})
	}()
	select {
	case <-failed:
		stopDelivery()
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	info, err := resources.metadata.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 0 {
		t.Fatalf("canceled backoff committed %d settlement records", info.State.Msgs)
	}
}

func TestInvalidTraceContextDoesNotMakePersistedMessageMalformed(t *testing.T) {
	message := &servicev1.Message{}
	message.SetKind(messageKindCommand)
	message.SetMessageId("123e4567-e89b-12d3-a456-426614174000")
	message.SetCorrelationId("123e4567-e89b-12d3-a456-426614174000")
	message.SetSourcePath("edge/source")
	message.SetTargetPath("edge/worker")
	message.SetTypeName("google.protobuf.Empty")
	message.SetPayload(nil)
	message.SetTraceparent("not-a-traceparent")
	if err := validatePersistedEnvelope(message); err != nil {
		t.Fatalf("persisted invalid trace context was malformed: %v", err)
	}
	if err := validateOutboundEnvelope(message); err == nil {
		t.Fatal("outbound invalid trace context was accepted")
	}
}

func TestDeliveryResumesTerminalSettlementWithoutCallingHandler(t *testing.T) {
	for _, state := range []servicev1.SettlementState{
		servicev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE,
		servicev1.SettlementState_SETTLEMENT_STATE_DISCARD,
	} {
		t.Run(state.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
				Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: messageKindCommand, Message: &emptypb.Empty{}}}},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			resources, closeBus := startMessageTestBus(ctx, t)
			defer closeBus()
			revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive).withModuleState("edge/worker", moduleRunning)
			telemetry, err := newTelemetry(Config{})
			if err != nil {
				t.Fatal(err)
			}
			runtime := newMessageRuntime(resources, declaration.registry, func() *admissionRevision { return revision }, telemetry)
			id, err := newUUID()
			if err != nil {
				t.Fatal(err)
			}
			envelope := runtime.capability("edge/publisher").envelope(ctx, messageKindCommand, id, "edge/worker", "google.protobuf.Empty", nil)
			if err := runtime.publishOne(ctx, envelope); err != nil {
				t.Fatal(err)
			}
			subject, _ := mailboxSubject("edge/worker", messageKindCommand, "google.protobuf.Empty")
			raw, err := resources.mailbox.GetLastMsgForSubject(ctx, subject)
			if err != nil {
				t.Fatal(err)
			}
			settlementKey := settlementSubject("edge/worker", id)
			if _, err := runtime.commitSettlement(ctx, settlementKey, 0, raw.Sequence, newSettlement("edge/worker", id, 0, state)); err != nil {
				t.Fatal(err)
			}

			deliveryCtx, stopDelivery := context.WithCancel(ctx)
			done := make(chan error, 1)
			called := make(chan struct{}, 1)
			go func() {
				done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: messageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
					called <- struct{}{}
					return nil
				}}})
			}()
			eventually(ctx, t, func() bool {
				info, err := resources.mailbox.Info(ctx)
				return err == nil && info.State.Msgs == 0
			})
			select {
			case <-called:
				t.Fatal("handler ran after terminal settlement intent")
			default:
			}
			stopDelivery()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReplyPreservesCorrelationAndTargetsRequestSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{
		{Name: "requester", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: messageKindReply, Message: &emptypb.Empty{}}}}},
		{Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: messageKindCommand, Message: &emptypb.Empty{}}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive)
	for _, path := range []string{"edge/requester", "edge/worker"} {
		revision = revision.withModuleState(path, moduleRunning)
	}
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMessageRuntime(resources, declaration.registry, func() *admissionRevision { return revision }, telemetry)
	if err := runtime.capability("edge/requester").Command(ctx, "edge/worker", &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	commandSubject, _ := mailboxSubject("edge/worker", messageKindCommand, "google.protobuf.Empty")
	commandRaw, err := resources.mailbox.GetLastMsgForSubject(ctx, commandSubject)
	if err != nil {
		t.Fatal(err)
	}
	command := &servicev1.Message{}
	if err := proto.Unmarshal(commandRaw.Data, command); err != nil {
		t.Fatal(err)
	}

	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	values := telemetry.values(testIdentity(), "FLOWSEER_EDGE_", "edge/worker")
	values.bus = runtime.capability("edge/worker")
	deliveryCtx = withContextValues(deliveryCtx, values)
	done := make(chan error, 1)
	go func() {
		done <- runtime.runDelivery(deliveryCtx, declaration.modules[1], []Handler{{Kind: messageKindCommand, Message: &emptypb.Empty{}, Handle: func(handlerCtx context.Context, _ proto.Message) error {
			return Bus(handlerCtx).Reply(handlerCtx, &emptypb.Empty{})
		}}})
	}()
	replySubject, _ := mailboxSubject("edge/requester", messageKindReply, "google.protobuf.Empty")
	var reply *servicev1.Message
	eventually(ctx, t, func() bool {
		raw, err := resources.mailbox.GetLastMsgForSubject(ctx, replySubject)
		if err != nil {
			return false
		}
		reply = &servicev1.Message{}
		return proto.Unmarshal(raw.Data, reply) == nil
	})
	if reply.GetTargetPath() != "edge/requester" || reply.GetCorrelationId() != command.GetCorrelationId() || reply.GetCausationId() != command.GetMessageId() {
		t.Fatalf("reply routing/correlation = target:%q correlation:%q causation:%q", reply.GetTargetPath(), reply.GetCorrelationId(), reply.GetCausationId())
	}
	stopDelivery()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func eventually(ctx context.Context, t *testing.T, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func startMessageTestBus(ctx context.Context, t *testing.T) (busResources, func()) {
	t.Helper()
	config, err := normalizeBusConfig(testIdentity(), BusConfig{StoreDir: filepath.Join(t.TempDir(), "bus")})
	if err != nil {
		t.Fatal(err)
	}
	bus, err := startLocalBus(ctx, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	return bus.resources, func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := bus.close(closeCtx, true); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("close bus: %v", err)
		}
	}
}

func TestCaseInsensitiveHeaderCarrier(t *testing.T) {
	header := nats.Header{"TraceParent": {"first"}, "traceparent": {"second"}}
	carrier := caseInsensitiveHeaderCarrier(header)
	if got := carrier.Get("TRACEPARENT"); got != "first" {
		t.Fatalf("Get() = %q, want first", got)
	}
	carrier.Set("TRACESTATE", "vendor=value")
	if got := carrier.Get("tracestate"); got != "vendor=value" {
		t.Fatalf("Get(tracestate) = %q", got)
	}
	keys := carrier.Keys()
	if len(keys) != 2 {
		t.Fatalf("Keys() = %v, want two unique keys", keys)
	}
	var _ propagation.TextMapCarrier = carrier
}

func TestMailboxSubjectKeepsPathAndTypeAsSingleTokens(t *testing.T) {
	got, err := mailboxSubject("edge/worker", messageKindCommand, "google.protobuf.Empty")
	if err != nil {
		t.Fatal(err)
	}
	if want := mailboxSubjectRoot + ".v1.ZWRnZS93b3JrZXI.command.Z29vZ2xlLnByb3RvYnVmLkVtcHR5"; got != want {
		t.Fatalf("mailboxSubject() = %q, want %q", got, want)
	}
}
