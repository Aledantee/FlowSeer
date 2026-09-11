package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func TestNilMessageRuntimeCapabilitiesRemainDisabled(t *testing.T) {
	var runtime *messageRuntime
	if bus := runtime.capability("edge/worker"); bus != disabledMessageBus {
		t.Fatalf("nil runtime capability = %p, want disabled capability", bus)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.runDelivery(ctx, plannedModule{}, nil); err != nil {
		t.Fatalf("nil runtime delivery error = %v, want nil", err)
	}
}

func TestMessageBusPersistsCommandAndAtomicEventSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	setup := testSetup()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{
		{Name: "commands", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}}}}},
		{Name: "events_one", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: MessageKindEvent, Message: &emptypb.Empty{}}}}},
		{Name: "events_two", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: MessageKindEvent, Message: &emptypb.Empty{}}}}},
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
	if seen["edge/commands"] != MessageKindCommand || seen["edge/events_one"] != MessageKindEvent || seen["edge/events_two"] != MessageKindEvent {
		t.Fatalf("persisted targets = %v", seen)
	}
}

func TestAtomicEventCapacityFailureCommitsNoTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{
		{Name: "one", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindEvent, Message: &wrapperspb.BytesValue{}}}}},
		{Name: "two", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindEvent, Message: &wrapperspb.BytesValue{}}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	config, err := normalizeBusConfig(testIdentity(), BusConfig{
		StoreDir:         t.TempDir(),
		MaxStoreBytes:    4 << 20,
		MailboxMaxBytes:  64 << 10,
		MetadataMaxBytes: 1 << 20,
		ReserveBytes:     1 << 20,
		FsyncPolicy:      BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatal(err)
	}
	local, err := startLocalBus(ctx, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBus(t, local, false)
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive)
	for _, path := range []string{"edge/one", "edge/two"} {
		revision = revision.withModuleState(path, moduleRunning)
	}
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMessageRuntime(local.resources, declaration.registry, func() *admissionRevision { return revision }, telemetry)
	err = runtime.capability("edge/publisher").Publish(ctx, wrapperspb.Bytes(make([]byte, 40<<10)))
	if err == nil {
		t.Fatal("event larger than the atomic stream capacity succeeded")
	}
	if code, ok := errs.CodeOf(err); !ok || code != errCodeBusCapacity {
		t.Fatalf("capacity error code = %q, %t; want %q: %v", code, ok, errCodeBusCapacity, err)
	}
	info, err := local.resources.mailbox.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 0 {
		t.Fatalf("failed atomic event committed %d targets", info.State.Msgs)
	}
	if err := runtime.capability("edge/publisher").Publish(ctx, wrapperspb.Bytes([]byte("small"))); err != nil {
		t.Fatalf("valid event after capacity failure: %v", err)
	}
	info, err = local.resources.mailbox.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 2 {
		t.Fatalf("valid atomic event committed %d targets, want 2", info.State.Msgs)
	}
}

func TestMessageBusRejectsDisabledTargetWithoutPersistence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "commands", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}}}},
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
		{Name: "one", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: MessageKindEvent, Message: &wrapperspb.BytesValue{}}}}},
		{Name: "two", Leaf: &Leaf{Setup: setup, Subscriptions: []Subscription{{Kind: MessageKindEvent, Message: &wrapperspb.BytesValue{}}}}},
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
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Retries: 1}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive).withModuleState("edge/worker", moduleRunning)
	telemetry, err := newTelemetry(Config{TracerProvider: provider})
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
	privateHandlerError := "private-handler-error-b71f2c3a"
	handlers := []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
		call++
		calls <- call
		if call == 1 {
			return errs.New().Retryable().Msg(privateHandlerError)
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
	for _, ended := range recorder.Ended() {
		if recorded := fmt.Sprint(ended.Attributes(), ended.Events(), ended.Status()); strings.Contains(recorded, privateHandlerError) {
			t.Fatalf("private handler error appeared in span %q: %s", ended.Name(), recorded)
		}
	}
}

func TestCancellationDuringBackoffDoesNotCommitRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Retries: 1}}},
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
		done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
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

func TestDeliveryRetryReceivesFreshPayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &wrapperspb.StringValue{}, Retries: 1}}},
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
	if err := runtime.capability("edge/publisher").Command(ctx, "edge/worker", wrapperspb.String("original")); err != nil {
		t.Fatal(err)
	}

	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	done := make(chan error, 1)
	values := make(chan string, 2)
	var calls atomic.Int32
	go func() {
		done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: MessageKindCommand, Message: &wrapperspb.StringValue{}, Handle: func(_ context.Context, payload proto.Message) error {
			value := payload.(*wrapperspb.StringValue)
			values <- value.GetValue()
			if calls.Add(1) == 1 {
				value.Value = "mutated"
				return errs.New().Retryable().Msg("retry")
			}
			return nil
		}}})
	}()
	for range 2 {
		select {
		case got := <-values:
			if got != "original" {
				t.Fatalf("handler payload = %q, want original", got)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	eventually(ctx, t, func() bool {
		info, infoErr := resources.mailbox.Info(ctx)
		return infoErr == nil && info.State.Msgs == 0
	})
	stopDelivery()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryExtendsAckDeadlineWhileHandlerRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "worker", Leaf: &Leaf{Setup: testSetup(), DeliveryConcurrency: 2, Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}}}},
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
	runtime.progressInterval = 10 * time.Millisecond
	if err := runtime.capability("edge/publisher").Command(ctx, "edge/worker", &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	var active atomic.Int32
	var maximum atomic.Int32
	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
			calls.Add(1)
			current := active.Add(1)
			for observed := maximum.Load(); current > observed && !maximum.CompareAndSwap(observed, current); observed = maximum.Load() {
			}
			time.Sleep(100 * time.Millisecond)
			active.Add(-1)
			return nil
		}}})
	}()
	eventually(ctx, t, func() bool {
		info, infoErr := resources.mailbox.Info(ctx)
		return infoErr == nil && info.State.Msgs == 0
	})
	stopDelivery()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
	if got := maximum.Load(); got != 1 {
		t.Fatalf("simultaneous handlers = %d, want 1", got)
	}
}

func TestDeliveryWorkerBoundsAndSequentialOrder(t *testing.T) {
	t.Run("sequential order", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
			Name: "worker", Leaf: &Leaf{Setup: testSetup(), DeliveryConcurrency: 1, Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &wrapperspb.Int32Value{}}}},
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
		for i := range int32(6) {
			if err := runtime.capability("edge/publisher").Command(ctx, "edge/worker", wrapperspb.Int32(i)); err != nil {
				t.Fatal(err)
			}
		}
		deliveryCtx, stopDelivery := context.WithCancel(ctx)
		done := make(chan error, 1)
		var mu sync.Mutex
		var completed []int32
		go func() {
			done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: MessageKindCommand, Message: &wrapperspb.Int32Value{}, Handle: func(_ context.Context, payload proto.Message) error {
				mu.Lock()
				completed = append(completed, payload.(*wrapperspb.Int32Value).GetValue())
				mu.Unlock()
				return nil
			}}})
		}()
		eventually(ctx, t, func() bool {
			info, infoErr := resources.mailbox.Info(ctx)
			return infoErr == nil && info.State.Msgs == 0
		})
		stopDelivery()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(completed) != 6 {
			t.Fatalf("completed values = %v", completed)
		}
		for i, got := range completed {
			if got != int32(i) {
				t.Fatalf("completed values = %v, want stream order", completed)
			}
		}
	})

	t.Run("parallel bound", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
			Name: "worker", Leaf: &Leaf{Setup: testSetup(), DeliveryConcurrency: 4, Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}}}},
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
		for range 8 {
			if err := runtime.capability("edge/publisher").Command(ctx, "edge/worker", &emptypb.Empty{}); err != nil {
				t.Fatal(err)
			}
		}
		var active atomic.Int32
		var maximum atomic.Int32
		entered := make(chan struct{}, 8)
		release := make(chan struct{})
		deliveryCtx, stopDelivery := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
				current := active.Add(1)
				for observed := maximum.Load(); current > observed && !maximum.CompareAndSwap(observed, current); observed = maximum.Load() {
				}
				entered <- struct{}{}
				<-release
				active.Add(-1)
				return nil
			}}})
		}()
		for range 4 {
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		if got := maximum.Load(); got != 4 {
			t.Fatalf("maximum active handlers = %d, want 4", got)
		}
		close(release)
		eventually(ctx, t, func() bool {
			info, infoErr := resources.mailbox.Info(ctx)
			return infoErr == nil && info.State.Msgs == 0
		})
		stopDelivery()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if got := maximum.Load(); got > 4 {
			t.Fatalf("maximum active handlers = %d, want at most 4", got)
		}
	})
}

func TestDeliveryDiscardsMalformedRecordWithoutHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	subject, err := mailboxSubject("edge/worker", MessageKindCommand, "google.protobuf.Empty")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resources.jetStream.Publish(ctx, subject, []byte("not protobuf")); err != nil {
		t.Fatal(err)
	}
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMessageRuntime(resources, declaration.registry, nil, telemetry)
	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	done := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
			calls.Add(1)
			return nil
		}}})
	}()
	eventually(ctx, t, func() bool {
		info, infoErr := resources.mailbox.Info(ctx)
		return infoErr == nil && info.State.Msgs == 0
	})
	stopDelivery()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("malformed record reached handler %d times", got)
	}
}

func TestDeliveryDecodesPersistedAliasesForEveryMessageKind(t *testing.T) {
	kinds := []servicev1.MessageKind{MessageKindCommand, MessageKindEvent, MessageKindReply}
	ids := []string{
		"b80f5119-d54b-48e7-83ea-fc349d90dc24",
		"21822291-3057-458b-89e2-a8cab468e450",
		"85cadbf6-5ddc-4e83-b888-49199e20a95d",
	}
	for i, kind := range kinds {
		t.Run(kind.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
				Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: kind, Message: &emptypb.Empty{}, Aliases: []protoreflect.FullName{"legacy.Empty"}}}},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			resources, closeBus := startMessageTestBus(ctx, t)
			defer closeBus()
			envelope := servicev1.Message_builder{
				Kind:          kind.Enum(),
				MessageId:     proto.String(ids[i]),
				CorrelationId: proto.String(ids[i]),
				SourcePath:    proto.String("edge/source"),
				TargetPath:    proto.String("edge/worker"),
				TypeName:      proto.String("legacy.Empty"),
				Payload:       []byte{},
				PublishedAt:   timestamppb.Now(),
			}.Build()
			data, err := proto.MarshalOptions{Deterministic: true}.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			subject, err := mailboxSubject("edge/worker", kind, "legacy.Empty")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := resources.jetStream.Publish(ctx, subject, data); err != nil {
				t.Fatal(err)
			}
			telemetry, err := newTelemetry(Config{})
			if err != nil {
				t.Fatal(err)
			}
			runtime := newMessageRuntime(resources, declaration.registry, nil, telemetry)
			deliveryCtx, stopDelivery := context.WithCancel(ctx)
			done := make(chan error, 1)
			delivered := make(chan struct{}, 1)
			go func() {
				done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: kind, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
					delivered <- struct{}{}
					return nil
				}}})
			}()
			select {
			case <-delivered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			eventually(ctx, t, func() bool {
				info, infoErr := resources.mailbox.Info(ctx)
				return infoErr == nil && info.State.Msgs == 0
			})
			stopDelivery()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeliveryRetriesPanickingHandlerToDeclaredLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Retries: 1}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive).withModuleState("edge/worker", moduleRunning)
	telemetry, err := newTelemetry(Config{TracerProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMessageRuntime(resources, declaration.registry, func() *admissionRevision { return revision }, telemetry)
	if err := runtime.capability("edge/publisher").Command(ctx, "edge/worker", &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	done := make(chan error, 1)
	var calls atomic.Int32
	privatePanic := "private-handler-panic-9d177bf2"
	go func() {
		done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
			calls.Add(1)
			panic(privatePanic)
		}}})
	}()
	eventually(ctx, t, func() bool {
		info, infoErr := resources.mailbox.Info(ctx)
		return infoErr == nil && info.State.Msgs == 0
	})
	stopDelivery()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want initial call plus one retry", got)
	}
	for _, ended := range recorder.Ended() {
		if recorded := fmt.Sprint(ended.Attributes(), ended.Events(), ended.Status()); strings.Contains(recorded, privatePanic) {
			t.Fatalf("private handler panic appeared in span %q: %s", ended.Name(), recorded)
		}
	}
}

func TestInvalidTraceContextDoesNotMakePersistedMessageMalformed(t *testing.T) {
	message := &servicev1.Message{}
	message.SetKind(MessageKindCommand)
	message.SetMessageId("123e4567-e89b-12d3-a456-426614174000")
	message.SetCorrelationId("123e4567-e89b-12d3-a456-426614174000")
	message.SetSourcePath("edge/source")
	message.SetTargetPath("edge/worker")
	message.SetTypeName("google.protobuf.Empty")
	message.SetPayload(nil)
	message.SetPublishedAt(timestamppb.Now())
	message.SetTraceparent("not-a-traceparent")
	if err := validatePersistedEnvelope(message); err != nil {
		t.Fatalf("persisted invalid trace context was malformed: %v", err)
	}
	if err := validateOutboundEnvelope(message); err == nil {
		t.Fatal("outbound invalid trace context was accepted")
	}
}

// A record written before publish time was part of the envelope is
// malformed now, on the persisted path as well as the outbound one: the
// schema says the field must be present, and the runtime check is what
// makes that true for what is already in the mailbox.
func TestPersistedMessageWithoutPublishTimeIsMalformed(t *testing.T) {
	message := &servicev1.Message{}
	message.SetKind(MessageKindCommand)
	message.SetMessageId("123e4567-e89b-12d3-a456-426614174000")
	message.SetSourcePath("edge/source")
	message.SetTargetPath("edge/worker")
	message.SetTypeName("google.protobuf.Empty")
	message.SetPayload(nil)
	if err := validatePersistedEnvelope(message); err == nil {
		t.Fatal("a persisted message without published_at was accepted")
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
				Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}}}},
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
			envelope := runtime.capability("edge/publisher").envelope(ctx, MessageKindCommand, id, "edge/worker", "google.protobuf.Empty", nil)
			if err := runtime.publishOne(ctx, envelope); err != nil {
				t.Fatal(err)
			}
			subject, _ := mailboxSubject("edge/worker", MessageKindCommand, "google.protobuf.Empty")
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
				done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
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
		{Name: "requester", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindReply, Message: &emptypb.Empty{}}}}},
		{Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}}}}},
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
	commandSubject, _ := mailboxSubject("edge/worker", MessageKindCommand, "google.protobuf.Empty")
	commandRaw, err := resources.mailbox.GetLastMsgForSubject(ctx, commandSubject)
	if err != nil {
		t.Fatal(err)
	}
	command := &servicev1.Message{}
	if err := proto.Unmarshal(commandRaw.Data, command); err != nil {
		t.Fatal(err)
	}

	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	values := telemetry.attemptContextValues(testIdentity(), "FLOWSEER_EDGE_", "edge/worker")
	values.bus = runtime.capability("edge/worker")
	deliveryCtx = withContextValues(deliveryCtx, values)
	done := make(chan error, 1)
	go func() {
		done <- runtime.runDelivery(deliveryCtx, declaration.modules[1], []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(handlerCtx context.Context, _ proto.Message) error {
			return Bus(handlerCtx).Reply(handlerCtx, &emptypb.Empty{})
		}}})
	}()
	replySubject, _ := mailboxSubject("edge/requester", MessageKindReply, "google.protobuf.Empty")
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

func TestTraceContextLinksPublicationToDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	declaration, err := validateDeclaration(Config{Identity: testIdentity(), Modules: []Module{{
		Name: "worker", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: MessageKindCommand, Message: &emptypb.Empty{}}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	resources, closeBus := startMessageTestBus(ctx, t)
	defer closeBus()
	revision := newAdmissionRevision(declaration.registry).withPhase(admissionActive).withModuleState("edge/worker", moduleRunning)
	telemetry, err := newTelemetry(Config{TracerProvider: provider, Propagator: propagation.TraceContext{}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newMessageRuntime(resources, declaration.registry, func() *admissionRevision { return revision }, telemetry)
	publisherValues := telemetry.attemptContextValues(testIdentity(), "FLOWSEER_EDGE_", "edge/publisher")
	publisherValues.bus = runtime.capability("edge/publisher")
	publisherCtx := withContextValues(ctx, publisherValues)
	publisherCtx, parent := telemetry.tracer.Start(publisherCtx, "parent")
	if err := Bus(publisherCtx).Command(publisherCtx, "edge/worker", &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	parent.End()

	workerValues := telemetry.attemptContextValues(testIdentity(), "FLOWSEER_EDGE_", "edge/worker")
	workerValues.bus = runtime.capability("edge/worker")
	deliveryCtx, stopDelivery := context.WithCancel(withContextValues(ctx, workerValues))
	done := make(chan error, 1)
	delivered := make(chan struct{}, 1)
	go func() {
		done <- runtime.runDelivery(deliveryCtx, declaration.modules[0], []Handler{{Kind: MessageKindCommand, Message: &emptypb.Empty{}, Handle: func(context.Context, proto.Message) error {
			delivered <- struct{}{}
			return nil
		}}})
	}()
	select {
	case <-delivered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	eventually(ctx, t, func() bool {
		info, infoErr := resources.mailbox.Info(ctx)
		return infoErr == nil && info.State.Msgs == 0
	})
	stopDelivery()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	var publicationTraceID trace.TraceID
	var deliveryLinks []sdktrace.Link
	for _, span := range recorder.Ended() {
		switch span.Name() {
		case publicationSpanName:
			publicationTraceID = span.SpanContext().TraceID()
		case deliverySpanName:
			deliveryLinks = span.Links()
		}
	}
	if !publicationTraceID.IsValid() {
		t.Fatal("publication span was not recorded")
	}
	if len(deliveryLinks) != 1 || deliveryLinks[0].SpanContext.TraceID() != publicationTraceID {
		t.Fatalf("delivery links = %v, want publication trace %s", deliveryLinks, publicationTraceID)
	}
}

func TestTraceContextRelayAcrossDisabledModule(t *testing.T) {
	for _, kind := range []servicev1.MessageKind{MessageKindCommand, MessageKindReply, MessageKindEvent} {
		t.Run(messageKindToken(kind), func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			defer func() { _ = provider.Shutdown(context.Background()) }()
			observability, err := newTelemetry(Config{TracerProvider: provider, Propagator: propagation.TraceContext{}})
			if err != nil {
				t.Fatal(err)
			}
			enabled := observability.view(resolvedTelemetryPolicy{logs: true, metrics: true, traces: true})
			disabled := observability.view(resolvedTelemetryPolicy{logs: true, metrics: true})
			runtime := newMessageRuntime(busResources{}, nil, nil, observability)

			upstreamCtx, upstream := enabled.tracer.Start(context.Background(), "upstream")
			publisher := runtime.capabilityWithTelemetry("edge/publisher", enabled)
			publicationCtx, publication := publisher.startPublicationTrace(upstreamCtx, kind)
			envelope := publisher.envelope(publicationCtx, kind, deterministicUUID("published", messageKindToken(kind)), "edge/relay", "google.protobuf.Empty", nil)
			publicationContext := trace.SpanContextFromContext(publicationCtx)
			propagatedContext := publicationContext.WithRemote(true)
			publication.End()
			upstream.End()

			attemptCtx, attempt := enabled.tracer.Start(context.Background(), "relay attempt")
			disabledCtx, disabledDelivery := runtime.deliveryTrace(attemptCtx, envelope, 1, disabled)
			if disabledDelivery.IsRecording() {
				t.Fatal("trace-disabled delivery created a recording span")
			}
			if got := trace.SpanContextFromContext(disabledCtx); !got.Equal(propagatedContext) {
				t.Fatalf("disabled delivery context = %v, want %v", got, propagatedContext)
			}
			relay := runtime.capabilityWithTelemetry("edge/relay", disabled)
			relayCtx, disabledPublication := relay.startPublicationTrace(disabledCtx, kind)
			if disabledPublication.IsRecording() {
				t.Fatal("trace-disabled publication created a recording span")
			}
			relayed := relay.envelope(relayCtx, kind, deterministicUUID("relayed", messageKindToken(kind)), "edge/consumer", "google.protobuf.Empty", nil)
			if got, want := relayed.GetTraceparent(), envelope.GetTraceparent(); got != want {
				t.Fatalf("relayed traceparent = %q, want %q", got, want)
			}
			if got, want := relayed.GetTracestate(), envelope.GetTracestate(); got != want {
				t.Fatalf("relayed tracestate = %q, want %q", got, want)
			}
			for _, delivered := range []uint64{1, 2} {
				retryCtx, retrySpan := runtime.deliveryTrace(attemptCtx, relayed, delivered, disabled)
				if retrySpan.IsRecording() || !trace.SpanContextFromContext(retryCtx).Equal(propagatedContext) {
					t.Fatalf("trace-disabled delivery %d did not preserve context", delivered)
				}
			}
			attempt.End()

			downstreamCtx, downstream := runtime.deliveryTrace(context.Background(), relayed, 1, enabled)
			if !downstream.IsRecording() {
				t.Fatal("trace-enabled downstream delivery did not create a recording span")
			}
			if trace.SpanContextFromContext(downstreamCtx).TraceID() == publicationContext.TraceID() {
				t.Fatal("durable delivery unexpectedly retained the publication parent")
			}
			downstream.End()
			links := recorder.Ended()[len(recorder.Ended())-1].Links()
			if len(links) != 1 || !links[0].SpanContext.Equal(propagatedContext) {
				t.Fatalf("downstream links = %v, want publication context %v", links, propagatedContext)
			}

			if got := len(recorder.Ended()); got != 4 {
				t.Fatalf("ended span count = %d, want upstream, publication, relay attempt, and downstream only", got)
			}
		})
	}
}

func TestTraceDisabledRelayPreservesUnsampledContextAndIgnoresInvalidCarrier(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	defer func() { _ = provider.Shutdown(context.Background()) }()
	observability, err := newTelemetry(Config{TracerProvider: provider, Propagator: propagation.TraceContext{}})
	if err != nil {
		t.Fatal(err)
	}
	disabled := observability.view(resolvedTelemetryPolicy{logs: true, metrics: true})
	runtime := newMessageRuntime(busResources{}, nil, nil, observability)
	relay := runtime.capabilityWithTelemetry("edge/relay", disabled)

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{2},
		TraceFlags: 0,
		TraceState: mustTraceState(t, "vendor=value"),
		Remote:     true,
	})
	carrier := caseInsensitiveHeaderCarrier(nats.Header{})
	propagation.TraceContext{}.Inject(trace.ContextWithRemoteSpanContext(context.Background(), spanContext), carrier)
	for _, kind := range []servicev1.MessageKind{MessageKindCommand, MessageKindReply, MessageKindEvent} {
		envelope := &servicev1.Message{}
		envelope.SetKind(kind)
		envelope.SetTraceparent(carrier.Get("traceparent"))
		envelope.SetTracestate(carrier.Get("tracestate"))
		deliveryCtx, span := runtime.deliveryTrace(context.Background(), envelope, 1, disabled)
		if span.IsRecording() {
			t.Fatal("unsampled disabled delivery created a recording span")
		}
		relayCtx, publication := relay.startPublicationTrace(deliveryCtx, kind)
		if publication.IsRecording() {
			t.Fatal("unsampled disabled publication created a recording span")
		}
		relayed := relay.envelope(relayCtx, kind, deterministicUUID("unsampled", messageKindToken(kind)), "edge/consumer", "google.protobuf.Empty", nil)
		if got, want := relayed.GetTraceparent(), carrier.Get("traceparent"); got != want {
			t.Fatalf("relayed traceparent = %q, want %q", got, want)
		}
		if got, want := relayed.GetTracestate(), carrier.Get("tracestate"); got != want {
			t.Fatalf("relayed tracestate = %q, want %q", got, want)
		}
	}

	attemptCtx, attempt := observability.tracer.Start(context.Background(), "attempt")
	invalid := &servicev1.Message{}
	invalid.SetTraceparent("not-a-traceparent")
	extracted, span := runtime.deliveryTrace(attemptCtx, invalid, 1, disabled)
	defer attempt.End()
	if span.IsRecording() || trace.SpanContextFromContext(extracted).IsValid() {
		t.Fatal("invalid carrier retained the local attempt span")
	}
}

func TestMessageTelemetryOmitsPrivateDispositionAndErrorData(t *testing.T) {
	var logs bytes.Buffer
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	observability, err := newTelemetry(Config{
		Logger:         slog.New(slog.NewTextHandler(&logs, nil)),
		TracerProvider: provider,
		Propagator:     propagation.TraceContext{},
	})
	if err != nil {
		t.Fatal(err)
	}
	view := observability.view(resolvedTelemetryPolicy{logs: true, metrics: true, traces: true})
	ctx, span := view.tracer.Start(context.Background(), "delivery")
	privateDispositionID := "private-disposition-2d739e61"
	settlement := servicev1.Settlement_builder{
		State:         servicev1.SettlementState_SETTLEMENT_STATE_DISCARD.Enum(),
		DispositionId: proto.String(privateDispositionID),
		RetryCount:    proto.Uint32(2),
	}.Build()
	view.recordDisposition(ctx, "edge/worker", "google.protobuf.Empty", MessageKindCommand, settlement)
	span.End()

	privatePublicationError := "publish a nil protobuf message"
	runtime := newMessageRuntime(busResources{}, nil, nil, observability)
	bus := runtime.capabilityWithTelemetry("edge/publisher", view)
	if err := bus.Command(context.Background(), "edge/worker", nil); err == nil {
		t.Fatal("nil publication unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "nil protobuf") {
		t.Fatalf("publication error = %v", err)
	}
	for _, ended := range recorder.Ended() {
		recorded := fmt.Sprint(ended.Attributes(), ended.Events(), ended.Links(), ended.Status())
		if strings.Contains(recorded, privateDispositionID) || strings.Contains(recorded, privatePublicationError) {
			t.Fatalf("private data appeared in span %q: %s", ended.Name(), recorded)
		}
		for _, event := range ended.Events() {
			if event.Name == "exception" {
				t.Fatalf("raw error exception appeared in span %q: %v", ended.Name(), event)
			}
		}
	}
	recordedLogs := logs.String()
	if strings.Contains(recordedLogs, privateDispositionID) || strings.Contains(recordedLogs, "disposition_id") {
		t.Fatalf("private disposition data appeared in logs: %s", recordedLogs)
	}
}

func TestMessageDispositionLogLevelsSeparateSuccessFromDiscard(t *testing.T) {
	logger, sink := newRecordingLogger()
	observability, err := newTelemetry(Config{Logger: logger})
	if err != nil {
		t.Fatalf("newTelemetry() error: %v", err)
	}
	view := observability.view(resolvedTelemetryPolicy{logs: true})
	for _, state := range []servicev1.SettlementState{
		servicev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE,
		servicev1.SettlementState_SETTLEMENT_STATE_DISCARD,
	} {
		view.recordDisposition(context.Background(), "edge/worker", "google.protobuf.Empty", MessageKindCommand, servicev1.Settlement_builder{
			State: state.Enum(),
		}.Build())
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	var levels []slog.Level
	for _, record := range sink.records {
		if record.Message == "message disposition" {
			levels = append(levels, record.Level)
		}
	}
	if want := []slog.Level{slog.LevelDebug, slog.LevelWarn}; !slices.Equal(levels, want) {
		t.Errorf("message disposition levels = %v, want %v", levels, want)
	}
}

func mustTraceState(t *testing.T, value string) trace.TraceState {
	t.Helper()
	state, err := trace.ParseTraceState(value)
	if err != nil {
		t.Fatal(err)
	}
	return state
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
	config, err := normalizeBusConfig(testIdentity(), *periodicBusConfig(filepath.Join(t.TempDir(), "bus")))
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
	got, err := mailboxSubject("edge/worker", MessageKindCommand, "google.protobuf.Empty")
	if err != nil {
		t.Fatal(err)
	}
	if want := mailboxSubjectRoot + ".v1.ZWRnZS93b3JrZXI.command.Z29vZ2xlLnByb3RvYnVmLkVtcHR5"; got != want {
		t.Fatalf("mailboxSubject() = %q, want %q", got, want)
	}
}
