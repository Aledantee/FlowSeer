package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	maxSubscriptionRetries    = 100
	maxDeliveryConcurrency    = 64
	maxMessageNameLength      = 256
	maxSubscriptionAliasCount = 256
	maxAtomicEventTargets     = 1000

	atomicBatchIDHeader       = "Nats-Batch-Id"
	atomicBatchSequenceHeader = "Nats-Batch-Sequence"
	atomicBatchCommitHeader   = "Nats-Batch-Commit"
	atomicDuplicateErrorCode  = jetstream.ErrorCode(10201)
)

var (
	errCodeSubscription = errs.NewCode("service/subscription")
	errCodeMessageType  = errs.NewCode("service/message-type")
	errCodeAdmission    = errs.NewCode("service/admission")
	errCodeBusDisabled  = errs.NewCode("service/bus-disabled")
	errCodePublication  = errs.NewCode("service/publication")
	traceparentPattern  = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}(?:-.+)?$`)
)

// MessageBus is an attempt-scoped capability for durable service-local
// messaging. Its zero value is disabled, and a handle expires when its owning
// attempt ends. A handle may be shared by that attempt's goroutines. Every
// method returns a structured service error for configuration, admission,
// payload, and persistence failures. Caller cancellation is returned unchanged;
// an expired attempt returns [context.Canceled]. The implementation and broker
// connection are intentionally private.
type MessageBus struct {
	runtime     *messageRuntime
	sourcePath  string
	attemptDone <-chan struct{}
	telemetry   telemetryView
}

var disabledMessageBus = &MessageBus{}

// messageRuntime owns the shared registry, admission snapshot source, and
// serialized atomic-event publisher for one bus-enabled run.
type messageRuntime struct {
	resources        busResources
	registry         *staticRegistry
	admission        func() *admissionRevision
	telemetry        telemetry
	atomicPermit     chan struct{}
	progressInterval time.Duration
}

func newMessageRuntime(resources busResources, registry *staticRegistry, admission func() *admissionRevision, telemetry telemetry) *messageRuntime {
	return &messageRuntime{
		resources:        resources,
		registry:         registry,
		admission:        admission,
		telemetry:        telemetry,
		atomicPermit:     make(chan struct{}, 1),
		progressInterval: defaultProgressInterval,
	}
}

func (r *messageRuntime) deliveryProgressInterval() time.Duration {
	if r.progressInterval > 0 {
		return r.progressInterval
	}
	return defaultProgressInterval
}

func (r *messageRuntime) capability(sourcePath string, attempt ...context.Context) *MessageBus {
	if r == nil {
		return disabledMessageBus
	}
	return r.capabilityWithTelemetry(sourcePath, r.telemetry.view(r.telemetry.availableSignals()), attempt...)
}

func (r *messageRuntime) capabilityWithTelemetry(sourcePath string, telemetry telemetryView, attempt ...context.Context) *MessageBus {
	if r == nil {
		return disabledMessageBus
	}
	var done <-chan struct{}
	if len(attempt) > 0 && attempt[0] != nil {
		done = attempt[0].Done()
	}
	return &MessageBus{runtime: r, sourcePath: sourcePath, attemptDone: done, telemetry: telemetry}
}

// Command synchronously persists payload for one statically registered and
// admitted target. Cancellation can stop the call before persistence; a nil
// return means the broker accepted the durable record.
func (b *MessageBus) Command(ctx context.Context, target string, payload proto.Message) error {
	return b.publishAddressed(ctx, MessageKindCommand, target, payload)
}

// Publish synchronously persists one atomic event snapshot for every currently
// admitted subscriber. A registered event with no admitted subscribers is a
// successful no-op. Cancellation can stop the call before the atomic commit.
func (b *MessageBus) Publish(ctx context.Context, payload proto.Message) (err error) {
	if err = b.available(ctx); err != nil {
		return err
	}
	ctx, span := b.startPublicationTrace(ctx, MessageKindEvent)
	defer func() {
		if err != nil {
			recordSpanError(span, "event publication failed", err)
		}
		span.End()
	}()
	fullName, payloadBytes, err := marshalPayload(payload)
	if err != nil {
		return err
	}
	span.SetAttributes(attribute.String(messageTypeKey, string(fullName)))
	revision := b.runtime.admissionRevision()
	targets, err := revision.admitEvent(fullName)
	if err != nil {
		b.telemetry.recordMessage(ctx, b.sourcePath, string(fullName), MessageKindEvent, messageRejected)
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	if len(targets) > maxAtomicEventTargets {
		return publicationError(MessageKindEvent, fullName, "event snapshot exceeds the broker atomic batch bound").
			Attr("target_count", len(targets)).Msg("publish event snapshot")
	}
	logicalID, err := newUUID()
	if err != nil {
		return publicationError(MessageKindEvent, fullName, "generate message identifier").Cause(err).Msg("publish event")
	}
	envelopes := make([]*servicev1.Message, len(targets))
	for i, target := range targets {
		envelopes[i] = b.envelope(ctx, MessageKindEvent, logicalID, target, fullName, payloadBytes)
	}
	if err := b.runtime.publishAtomic(ctx, envelopes); err != nil {
		b.telemetry.recordMessage(ctx, b.sourcePath, string(fullName), MessageKindEvent, messageRejected)
		return err
	}
	b.telemetry.recordMessage(ctx, b.sourcePath, string(fullName), MessageKindEvent, messagePublished)
	return nil
}

// Reply synchronously persists payload for the source of the message being
// handled by ctx. It reports an error when ctx is not a delivery context or the
// source is no longer admitted.
func (b *MessageBus) Reply(ctx context.Context, payload proto.Message) error {
	delivery, ok := deliveryFromContext(ctx)
	if !ok {
		return errs.New().Code(errCodePublication).Msg("reply requires a message delivery context")
	}
	return b.publishAddressed(ctx, MessageKindReply, delivery.sourcePath, payload)
}

func (b *MessageBus) publishAddressed(ctx context.Context, kind servicev1.MessageKind, target string, payload proto.Message) (err error) {
	if err = b.available(ctx); err != nil {
		return err
	}
	ctx, span := b.startPublicationTrace(ctx, kind)
	defer func() {
		if err != nil {
			recordSpanError(span, "message publication failed", err)
		}
		span.End()
	}()
	fullName, payloadBytes, err := marshalPayload(payload)
	if err != nil {
		return err
	}
	span.SetAttributes(attribute.String(messageTypeKey, string(fullName)))
	revision := b.runtime.admissionRevision()
	if err := revision.admitTarget(target, kind, fullName); err != nil {
		b.telemetry.recordMessage(ctx, b.sourcePath, string(fullName), kind, messageRejected)
		return err
	}
	logicalID, err := newUUID()
	if err != nil {
		return publicationError(kind, fullName, "generate message identifier").Cause(err).Msg("publish message")
	}
	envelope := b.envelope(ctx, kind, logicalID, target, fullName, payloadBytes)
	if err := b.runtime.publishOne(ctx, envelope); err != nil {
		b.telemetry.recordMessage(ctx, b.sourcePath, string(fullName), kind, messageRejected)
		return err
	}
	b.telemetry.recordMessage(ctx, b.sourcePath, string(fullName), kind, messagePublished)
	return nil
}

// startPublicationTrace starts a producer span. Events link to the current span
// so fan-out does not make each delivery a child of the publisher operation.
func (b *MessageBus) startPublicationTrace(ctx context.Context, kind servicev1.MessageKind) (context.Context, trace.Span) {
	if !b.telemetry.policy.traces {
		ctx = contextWithoutRecordingSpan(ctx)
		return ctx, trace.SpanFromContext(ctx)
	}
	options := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String(modulePathKey, b.sourcePath),
			attribute.String(messageKindKey, messageKindToken(kind)),
		),
	}
	if kind == MessageKindEvent {
		if link := trace.LinkFromContext(ctx); link.SpanContext.IsValid() {
			options = append(options, trace.WithLinks(link))
		}
		ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	}
	return b.telemetry.tracer.Start(ctx, publicationSpanName, options...)
}

func (b *MessageBus) available(ctx context.Context) error {
	if ctx == nil {
		return errs.New().Code(errCodeBusDisabled).Msg("message bus is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b == nil || b.runtime == nil || b.sourcePath == "" {
		return errs.New().Code(errCodeBusDisabled).Msg("message bus is unavailable")
	}
	if b.attemptDone != nil {
		select {
		case <-b.attemptDone:
			return context.Canceled
		default:
		}
	}
	return nil
}

func (r *messageRuntime) admissionRevision() *admissionRevision {
	if r.admission != nil {
		return r.admission()
	}
	return &admissionRevision{phase: admissionActive, registry: r.registry, states: map[string]moduleState{}}
}

func marshalPayload(payload proto.Message) (protoreflect.FullName, []byte, error) {
	if isNilMessage(payload) {
		return "", nil, errs.New().Code(errCodePublication).Msg("publish a nil protobuf message")
	}
	fullName := payload.ProtoReflect().Descriptor().FullName()
	if !fullName.IsValid() || len(fullName) > maxMessageNameLength {
		return "", nil, publicationError(servicev1.MessageKind_MESSAGE_KIND_UNSPECIFIED, fullName, "protobuf full name is invalid").Msg("publish message")
	}
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(payload)
	if err != nil {
		return "", nil, publicationError(servicev1.MessageKind_MESSAGE_KIND_UNSPECIFIED, fullName, "marshal protobuf payload").Cause(err).Msg("publish message")
	}
	return fullName, data, nil
}

// envelope carries correlation, causation, and trace context from a delivery
// into one persisted outbound message.
func (b *MessageBus) envelope(ctx context.Context, kind servicev1.MessageKind, id, target string, fullName protoreflect.FullName, payload []byte) *servicev1.Message {
	correlation := id
	causation := ""
	if delivery, ok := deliveryFromContext(ctx); ok {
		if delivery.correlationID != "" {
			correlation = delivery.correlationID
		}
		causation = delivery.messageID
	}
	carrier := caseInsensitiveHeaderCarrier(nats.Header{})
	propagator := b.telemetry.propagator
	if propagator == nil {
		propagator = propagation.TraceContext{}
	}
	propagator.Inject(ctx, carrier)
	message := &servicev1.Message{}
	message.SetKind(kind)
	message.SetMessageId(id)
	message.SetCorrelationId(correlation)
	if causation != "" {
		message.SetCausationId(causation)
	}
	message.SetSourcePath(b.sourcePath)
	message.SetTargetPath(target)
	message.SetTypeName(string(fullName))
	message.SetPayload(payload)
	if value := carrier.Get("traceparent"); value != "" {
		message.SetTraceparent(value)
	}
	if value := carrier.Get("tracestate"); value != "" {
		message.SetTracestate(value)
	}
	return message
}

func (r *messageRuntime) publishOne(ctx context.Context, envelope *servicev1.Message) error {
	if err := validateOutboundEnvelope(envelope); err != nil {
		return err
	}
	subject, err := mailboxSubject(envelope.GetTargetPath(), envelope.GetKind(), envelope.GetTypeName())
	if err != nil {
		return err
	}
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(envelope)
	if err != nil {
		return publicationError(envelope.GetKind(), protoreflect.FullName(envelope.GetTypeName()), "marshal persisted envelope").Cause(err).Msg("persist message")
	}
	message := nats.NewMsg(subject)
	message.Data = data
	message.Header.Set(jetstream.MsgIDHeader, recordID(envelope.GetMessageId(), envelope.GetTargetPath()))
	message.Header.Set(jetstream.ExpectedStreamHeader, mailboxStreamName)
	if int64(message.Size()) > r.resources.connection.MaxPayload() {
		return publicationError(envelope.GetKind(), protoreflect.FullName(envelope.GetTypeName()), "persisted message exceeds the broker payload bound").Msg("persist message")
	}
	_, err = r.resources.jetStream.PublishMsg(ctx, message)
	if err != nil {
		if isCapacityError(err) {
			return busCapacity(err)
		}
		return publicationError(envelope.GetKind(), protoreflect.FullName(envelope.GetTypeName()), "persist message").Cause(err).Msg("persist message")
	}
	return nil
}

type atomicPubAck struct {
	Error     *jetstream.APIError `json:"error,omitempty"`
	Stream    string              `json:"stream"`
	Sequence  uint64              `json:"seq"`
	BatchID   string              `json:"batch"`
	BatchSize uint64              `json:"count"`
}

// publishAtomic serializes event batches and commits their final record by
// request so the broker exposes one durable snapshot. A duplicate commit
// acknowledgement confirms that an earlier request already committed it.
func (r *messageRuntime) publishAtomic(ctx context.Context, envelopes []*servicev1.Message) error {
	select {
	case r.atomicPermit <- struct{}{}:
		defer func() { <-r.atomicPermit }()
	case <-ctx.Done():
		return publicationError(MessageKindEvent, "", "wait for atomic event publisher").Cause(context.Cause(ctx)).Msg("publish event snapshot")
	}
	batchID, err := newUUID()
	if err != nil {
		return err
	}
	messages := make([]*nats.Msg, len(envelopes))
	for i, envelope := range envelopes {
		if err := validateOutboundEnvelope(envelope); err != nil {
			return err
		}
		subject, subjectErr := mailboxSubject(envelope.GetTargetPath(), envelope.GetKind(), envelope.GetTypeName())
		if subjectErr != nil {
			return subjectErr
		}
		data, marshalErr := (proto.MarshalOptions{Deterministic: true}).Marshal(envelope)
		if marshalErr != nil {
			return marshalErr
		}
		message := nats.NewMsg(subject)
		message.Data = data
		message.Header.Set(atomicBatchIDHeader, batchID)
		message.Header.Set(atomicBatchSequenceHeader, strconv.Itoa(i+1))
		message.Header.Set(jetstream.MsgIDHeader, recordID(envelope.GetMessageId(), envelope.GetTargetPath()))
		if i+1 == len(envelopes) {
			message.Header.Set(atomicBatchCommitHeader, "1")
		}
		if int64(message.Size()) > r.resources.connection.MaxPayload() {
			return publicationError(envelope.GetKind(), protoreflect.FullName(envelope.GetTypeName()), "persisted event record exceeds the broker payload bound").Msg("publish event snapshot")
		}
		messages[i] = message
	}
	for i, message := range messages {
		envelope := envelopes[i]
		if i+1 < len(messages) {
			if err := r.resources.connection.PublishMsg(message); err != nil {
				return publicationError(envelope.GetKind(), protoreflect.FullName(envelope.GetTypeName()), "stage atomic event").Cause(err).Msg("publish event snapshot")
			}
			continue
		}
		response, requestErr := r.resources.connection.RequestMsgWithContext(ctx, message)
		if requestErr != nil {
			probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			response, err = r.resources.connection.RequestMsgWithContext(probeCtx, message)
			cancel()
			if err != nil {
				return publicationError(envelope.GetKind(), protoreflect.FullName(envelope.GetTypeName()), "confirm atomic event").Cause(requestErr).Msg("publish event snapshot")
			}
		}
		var ack atomicPubAck
		if err := json.Unmarshal(response.Data, &ack); err != nil {
			return publicationError(envelope.GetKind(), protoreflect.FullName(envelope.GetTypeName()), "decode atomic publish acknowledgement").Cause(err).Msg("publish event snapshot")
		}
		if ack.Error != nil {
			if ack.Error.ErrorCode == atomicDuplicateErrorCode {
				return nil
			}
			if isCapacityError(ack.Error) {
				return busCapacity(ack.Error)
			}
			return publicationError(envelope.GetKind(), protoreflect.FullName(envelope.GetTypeName()), "commit atomic event").Cause(ack.Error).Msg("publish event snapshot")
		}
		if ack.Stream != mailboxStreamName || ack.Sequence == 0 || ack.BatchID != batchID || ack.BatchSize != uint64(len(envelopes)) {
			return publicationError(envelope.GetKind(), protoreflect.FullName(envelope.GetTypeName()), "atomic publish acknowledgement does not match the batch").Msg("publish event snapshot")
		}
	}
	return nil
}

func validateOutboundEnvelope(message *servicev1.Message) error {
	if err := validatePersistedEnvelope(message); err != nil {
		return publicationError(message.GetKind(), protoreflect.FullName(message.GetTypeName()), err.Error()).Msg("publish message")
	}
	if message.HasTraceparent() && (len(message.GetTraceparent()) > 128 || !traceparentPattern.MatchString(message.GetTraceparent())) {
		return publicationError(message.GetKind(), protoreflect.FullName(message.GetTypeName()), "injected traceparent is invalid").Msg("publish message")
	}
	if message.HasTracestate() && len(message.GetTracestate()) > 512 {
		return publicationError(message.GetKind(), protoreflect.FullName(message.GetTypeName()), "injected tracestate is too long").Msg("publish message")
	}
	return nil
}

func mailboxSubject(path string, kind servicev1.MessageKind, fullName string) (string, error) {
	kindToken := messageKindToken(kind)
	if kindToken == "unknown" || path == "" || fullName == "" {
		return "", errs.New().Code(errCodePublication).Msg("message subject identity is invalid")
	}
	return strings.Join([]string{mailboxSubjectRoot, "v1", encodeSubjectToken(path), kindToken, encodeSubjectToken(fullName)}, "."), nil
}

func messageKindToken(kind servicev1.MessageKind) string {
	switch kind {
	case MessageKindCommand:
		return "command"
	case MessageKindEvent:
		return "event"
	case MessageKindReply:
		return "reply"
	default:
		return "unknown"
	}
}

// recordID derives the JetStream idempotency key from a message identity and
// target.
func recordID(messageID, target string) string {
	sum := sha256.Sum256([]byte("message\x00" + messageID + "\x00" + target))
	return "v1_" + hex.EncodeToString(sum[:])
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func deterministicUUID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	sum[6] = sum[6]&0x0f | 0x50
	sum[8] = sum[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func publicationError(kind servicev1.MessageKind, fullName protoreflect.FullName, detail string) errs.Builder {
	return errs.New().Code(errCodePublication).Attr("message_kind", messageKindToken(kind)).Attr("message_type", string(fullName)).Attr("validation_detail", detail)
}

type caseInsensitiveHeaderCarrier nats.Header

var _ propagation.TextMapCarrier = caseInsensitiveHeaderCarrier{}

func (c caseInsensitiveHeaderCarrier) Get(key string) string {
	if values := nats.Header(c)[key]; len(values) > 0 {
		return values[0]
	}
	keys := make([]string, 0, len(c))
	for candidate := range c {
		if strings.EqualFold(candidate, key) {
			keys = append(keys, candidate)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 || len(c[keys[0]]) == 0 {
		return ""
	}
	return c[keys[0]][0]
}

func (c caseInsensitiveHeaderCarrier) Set(key, value string) {
	for candidate := range c {
		if strings.EqualFold(candidate, key) {
			delete(c, candidate)
		}
	}
	c[strings.ToLower(key)] = []string{value}
}

func (c caseInsensitiveHeaderCarrier) Keys() []string {
	seen := make(map[string]struct{}, len(c))
	for key := range c {
		seen[strings.ToLower(key)] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type deliveryContext struct {
	messageID     string
	correlationID string
	sourcePath    string
}

type deliveryContextKey struct{}

func withDeliveryContext(ctx context.Context, delivery deliveryContext) context.Context {
	return context.WithValue(ctx, deliveryContextKey{}, delivery)
}

func deliveryFromContext(ctx context.Context) (deliveryContext, bool) {
	if ctx == nil {
		return deliveryContext{}, false
	}
	delivery, ok := ctx.Value(deliveryContextKey{}).(deliveryContext)
	return delivery, ok
}

// MessageKind selects how the bus routes a persisted message. Declaring a
// subscription or a handler with one of these values keeps a module declaration
// independent of the generated schema package.
const (
	// MessageKindCommand addresses one statically declared target module.
	MessageKindCommand = servicev1.MessageKind_MESSAGE_KIND_COMMAND
	// MessageKindEvent addresses every enabled subscriber at publication time.
	MessageKindEvent = servicev1.MessageKind_MESSAGE_KIND_EVENT
	// MessageKindReply addresses the source of the message being handled.
	MessageKindReply = servicev1.MessageKind_MESSAGE_KIND_REPLY
)

// Subscription declares one static protobuf handler and its durable delivery
// policy. Retries counts committed retries after the initial handler call.
// Callers must not mutate a Subscription or its aliases while Run is active.
type Subscription struct {
	// Kind selects command, event, or reply routing.
	Kind servicev1.MessageKind
	// Message is a non-nil prototype whose full name is the canonical persisted type.
	Message proto.Message
	// Aliases are prior persisted full names decoded as the canonical Message type.
	Aliases []protoreflect.FullName
	// Retries is the maximum committed retry count, from zero through 100.
	Retries int
}

// HandlerFunc handles one decoded protobuf message. A nil return is successful
// handling; the runtime records acknowledgement intent before settling the
// broker record. While ctx remains active, a panic or retryable error is retried
// up to the subscription limit and any other error records discard intent.
// Cancellation after return leaves the message unsettled for redelivery. The
// function may be called more than once for the same logical message and need
// not be safe for concurrent use when its leaf's delivery concurrency is one. A
// higher delivery concurrency permits concurrent calls and requires a
// concurrency-safe HandlerFunc.
type HandlerFunc func(ctx context.Context, message proto.Message) error

// Handler binds one attempt-local function to a canonical static subscription.
// Aliases never name handlers. The zero value is invalid. Handler values are
// read-only after setup; Handle may run concurrently when the leaf allows it.
type Handler struct {
	// Kind must match the static subscription kind.
	Kind servicev1.MessageKind
	// Message must name the static subscription's canonical protobuf type.
	Message proto.Message
	// Handle processes a decoded message.
	Handle HandlerFunc
}

type plannedSubscription struct {
	kind      servicev1.MessageKind
	fullName  protoreflect.FullName
	aliases   []protoreflect.FullName
	retries   int
	typeToken string
	typeOf    protoreflect.MessageType
}

type messageKey struct {
	kind     servicev1.MessageKind
	fullName protoreflect.FullName
}

type targetKey struct {
	path string
	messageKey
}

// staticRegistry is the immutable routing and payload-resolution index built
// from the service declaration.
type staticRegistry struct {
	modules  map[string]struct{}
	targets  map[targetKey]struct{}
	events   map[protoreflect.FullName][]string
	resolver payloadResolver
}

type staticRegistryBuilder struct {
	modules  map[string]struct{}
	targets  map[targetKey]struct{}
	events   map[protoreflect.FullName][]string
	resolver payloadResolverBuilder
}

func newStaticRegistryBuilder() *staticRegistryBuilder {
	return &staticRegistryBuilder{
		modules: make(map[string]struct{}),
		targets: make(map[targetKey]struct{}),
		events:  make(map[protoreflect.FullName][]string),
		resolver: payloadResolverBuilder{
			entries: make(map[protoreflect.FullName]payloadType),
		},
	}
}

func (b *staticRegistryBuilder) addModule(path string) {
	b.modules[path] = struct{}{}
}

func (b *staticRegistryBuilder) addSubscriptions(path string, subscriptions []Subscription) ([]plannedSubscription, error) {
	planned := make([]plannedSubscription, 0, len(subscriptions))
	seen := make(map[messageKey]struct{}, len(subscriptions))
	for _, subscription := range subscriptions {
		item, err := validateSubscription(path, subscription)
		if err != nil {
			return nil, err
		}
		key := messageKey{kind: item.kind, fullName: item.fullName}
		if _, ok := seen[key]; ok {
			return nil, subscriptionError(path, "subscription is declared more than once").
				Attr("message_kind", item.kind.String()).
				Attr("message_type", string(item.fullName)).
				Msgf("module %s has a duplicate subscription", path)
		}
		seen[key] = struct{}{}

		if err := b.resolver.add(path, item); err != nil {
			return nil, err
		}
		switch item.kind {
		case servicev1.MessageKind_MESSAGE_KIND_EVENT:
			b.events[item.fullName] = append(b.events[item.fullName], path)
		case servicev1.MessageKind_MESSAGE_KIND_COMMAND, servicev1.MessageKind_MESSAGE_KIND_REPLY:
			b.targets[targetKey{path: path, messageKey: key}] = struct{}{}
		}
		planned = append(planned, item)
	}
	return planned, nil
}

func (b *staticRegistryBuilder) build() *staticRegistry {
	return &staticRegistry{
		modules:  b.modules,
		targets:  b.targets,
		events:   b.events,
		resolver: payloadResolver{entries: b.resolver.entries},
	}
}

func (r *staticRegistry) target(path string, kind servicev1.MessageKind, fullName protoreflect.FullName) bool {
	_, ok := r.targets[targetKey{path: path, messageKey: messageKey{kind: kind, fullName: fullName}}]
	return ok
}

func (r *staticRegistry) eventSubscribers(fullName protoreflect.FullName) ([]string, bool) {
	paths, ok := r.events[fullName]
	return append([]string(nil), paths...), ok
}

func validateSubscription(path string, subscription Subscription) (plannedSubscription, error) {
	switch subscription.Kind {
	case servicev1.MessageKind_MESSAGE_KIND_COMMAND,
		servicev1.MessageKind_MESSAGE_KIND_EVENT,
		servicev1.MessageKind_MESSAGE_KIND_REPLY:
	default:
		return plannedSubscription{}, subscriptionError(path, "subscription kind is unsupported").
			Attr("message_kind", subscription.Kind.String()).
			Msgf("module %s has an unsupported subscription kind", path)
	}
	if isNilMessage(subscription.Message) {
		return plannedSubscription{}, subscriptionError(path, "subscription prototype is nil").
			Msgf("module %s has a nil subscription prototype", path)
	}

	message := subscription.Message.ProtoReflect()
	fullName := message.Descriptor().FullName()
	if !fullName.IsValid() || len(fullName) > maxMessageNameLength {
		return plannedSubscription{}, subscriptionError(path, "subscription canonical full name is invalid").
			Attr("message_type", string(fullName)).
			Msgf("module %s has an invalid protobuf full name", path)
	}
	if subscription.Retries < 0 || subscription.Retries > maxSubscriptionRetries {
		return plannedSubscription{}, subscriptionError(path, "subscription retry count is outside its supported bounds").
			Attr("retry_count", subscription.Retries).
			Attr("maximum_retry_count", maxSubscriptionRetries).
			Msgf("module %s has an invalid subscription retry count", path)
	}
	if len(subscription.Aliases) > maxSubscriptionAliasCount {
		return plannedSubscription{}, subscriptionError(path, "subscription declares too many aliases").
			Attr("alias_count", len(subscription.Aliases)).
			Attr("maximum_alias_count", maxSubscriptionAliasCount).
			Msgf("module %s declares too many subscription aliases", path)
	}

	aliases := make([]protoreflect.FullName, 0, len(subscription.Aliases))
	seenAliases := make(map[protoreflect.FullName]struct{}, len(subscription.Aliases))
	for _, alias := range subscription.Aliases {
		if !alias.IsValid() || len(alias) > maxMessageNameLength || alias == fullName {
			return plannedSubscription{}, subscriptionError(path, "subscription alias is invalid").
				Attr("message_type", string(fullName)).
				Attr("message_alias", string(alias)).
				Msgf("module %s has an invalid subscription alias", path)
		}
		if _, ok := seenAliases[alias]; ok {
			return plannedSubscription{}, subscriptionError(path, "subscription alias is duplicated").
				Attr("message_type", string(fullName)).
				Attr("message_alias", string(alias)).
				Msgf("module %s has a duplicate subscription alias", path)
		}
		seenAliases[alias] = struct{}{}
		aliases = append(aliases, alias)
	}

	return plannedSubscription{
		kind:      subscription.Kind,
		fullName:  fullName,
		aliases:   aliases,
		retries:   subscription.Retries,
		typeToken: encodeSubjectToken(string(fullName)),
		typeOf:    message.Type(),
	}, nil
}

func isNilMessage(message proto.Message) bool {
	if message == nil {
		return true
	}
	value := reflect.ValueOf(message)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func subscriptionError(path, detail string) errs.Builder {
	return errs.New().
		Code(errCodeSubscription).
		Attr("module_path", path).
		Attr("validation_detail", detail)
}

func messageTypeError(path string, fullName protoreflect.FullName, detail string) errs.Builder {
	return errs.New().
		Code(errCodeMessageType).
		Attr("module_path", path).
		Attr("message_type", string(fullName)).
		Attr("validation_detail", detail)
}

type payloadType struct {
	canonical protoreflect.FullName
	typeOf    protoreflect.MessageType
}

// payloadResolver maps canonical protobuf names and accepted aliases to fresh
// canonical message instances.
type payloadResolver struct {
	entries map[protoreflect.FullName]payloadType
}

type payloadResolverBuilder struct {
	entries map[protoreflect.FullName]payloadType
}

// add rejects aliases that would resolve one persisted name to incompatible
// canonical schemas.
func (b *payloadResolverBuilder) add(path string, subscription plannedSubscription) error {
	payload := payloadType{canonical: subscription.fullName, typeOf: subscription.typeOf}
	names := append([]protoreflect.FullName{subscription.fullName}, subscription.aliases...)
	for _, name := range names {
		if previous, ok := b.entries[name]; ok {
			if previous.canonical != payload.canonical || !sameMessageDescriptor(previous.typeOf.Descriptor(), payload.typeOf.Descriptor()) {
				return messageTypeError(path, subscription.fullName, "protobuf name resolves to conflicting message schemas").
					Attr("message_alias", string(name)).
					Attr("conflicting_message_type", string(previous.canonical)).
					Msgf("protobuf name %s resolves to conflicting message schemas", name)
			}
		}
		b.entries[name] = payload
	}
	return nil
}

func (r payloadResolver) resolve(name protoreflect.FullName) (proto.Message, protoreflect.FullName, bool) {
	payload, ok := r.entries[name]
	if !ok {
		return nil, "", false
	}
	return payload.typeOf.New().Interface(), payload.canonical, true
}

func validateAttemptHandlers(path string, subscriptions []plannedSubscription, handlers []Handler) error {
	expected := make(map[messageKey]protoreflect.MessageDescriptor, len(subscriptions))
	for _, subscription := range subscriptions {
		expected[messageKey{kind: subscription.kind, fullName: subscription.fullName}] = subscription.typeOf.Descriptor()
	}
	actual := make(map[messageKey]struct{}, len(handlers))
	for _, handler := range handlers {
		if isNilMessage(handler.Message) || handler.Handle == nil {
			return handlerMismatch(path, "handler has a nil prototype or function", handler.Kind, "")
		}
		fullName := handler.Message.ProtoReflect().Descriptor().FullName()
		key := messageKey{kind: handler.Kind, fullName: fullName}
		descriptor, ok := expected[key]
		if !ok {
			return handlerMismatch(path, "handler is not statically declared", handler.Kind, fullName)
		}
		if !sameMessageDescriptor(descriptor, handler.Message.ProtoReflect().Descriptor()) {
			return handlerMismatch(path, "handler schema differs from its static declaration", handler.Kind, fullName)
		}
		if _, ok := actual[key]; ok {
			return handlerMismatch(path, "handler is declared more than once", handler.Kind, fullName)
		}
		actual[key] = struct{}{}
	}
	if len(actual) != len(expected) {
		return errs.New().
			Code(errCodeHandlerMismatch).
			Attr("module_path", path).
			Attr("declared_handler_count", len(expected)).
			Attr("attempt_handler_count", len(actual)).
			Msgf("module %s attempt handlers do not match its static declaration", path)
	}
	return nil
}

func sameMessageDescriptor(left, right protoreflect.MessageDescriptor) bool {
	return proto.Equal(protodesc.ToDescriptorProto(left), protodesc.ToDescriptorProto(right))
}

func handlerMismatch(path, detail string, kind servicev1.MessageKind, fullName protoreflect.FullName) error {
	return errs.New().
		Code(errCodeHandlerMismatch).
		Attr("module_path", path).
		Attr("message_kind", kind.String()).
		Attr("message_type", string(fullName)).
		Attr("validation_detail", detail).
		Msgf("module %s attempt handlers do not match its static declaration", path)
}

type admissionPhase uint8

const (
	admissionPreflight admissionPhase = iota
	admissionActive
	admissionShuttingDown
)

type moduleState uint8

const (
	moduleRunning moduleState = iota
	moduleSetupFailed
	moduleRestarting
	moduleBackoff
	moduleDisabled
	moduleStopped
)

func (s moduleState) String() string {
	switch s {
	case moduleRunning:
		return "running"
	case moduleSetupFailed:
		return "setup_failed"
	case moduleRestarting:
		return "restarting"
	case moduleBackoff:
		return "backoff"
	case moduleDisabled:
		return "disabled"
	case moduleStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// admissionRevision is an immutable routing snapshot. Publishing a replacement
// never mutates a revision already read by a message publisher.
type admissionRevision struct {
	number   uint64
	phase    admissionPhase
	registry *staticRegistry
	states   map[string]moduleState
}

func newAdmissionRevision(registry *staticRegistry) *admissionRevision {
	states := make(map[string]moduleState, len(registry.modules))
	for path := range registry.modules {
		states[path] = moduleDisabled
	}
	return &admissionRevision{number: 1, phase: admissionPreflight, registry: registry, states: states}
}

func (r *admissionRevision) withPhase(phase admissionPhase) *admissionRevision {
	return &admissionRevision{number: r.number + 1, phase: phase, registry: r.registry, states: r.states}
}

func (r *admissionRevision) withModuleState(path string, state moduleState) *admissionRevision {
	states := make(map[string]moduleState, len(r.states))
	for modulePath, current := range r.states {
		states[modulePath] = current
	}
	states[path] = state
	return &admissionRevision{number: r.number + 1, phase: r.phase, registry: r.registry, states: states}
}

func (r *admissionRevision) withModuleSnapshot(modules []plannedModule) *admissionRevision {
	states := make(map[string]moduleState, len(r.states))
	var addModules func([]plannedModule)
	addModules = func(items []plannedModule) {
		for _, module := range items {
			state := moduleDisabled
			if module.enabled {
				state = moduleRunning
			}
			states[module.path] = state
			addModules(module.children)
		}
	}
	addModules(modules)
	return &admissionRevision{number: r.number + 1, phase: admissionActive, registry: r.registry, states: states}
}

func (r *admissionRevision) admitTarget(path string, kind servicev1.MessageKind, fullName protoreflect.FullName) error {
	ok := r.registry.target(path, kind, fullName)
	if !ok {
		return messageTypeError(path, fullName, "message type is not registered for the addressed target").
			Attr("message_kind", kind.String()).
			Msgf("module %s does not accept %s message %s", path, kind.String(), fullName)
	}
	if r.phase != admissionActive {
		return admissionError(path, kind, fullName, r.phase.String(), "").
			Msgf("module %s is not accepting new messages", path)
	}
	state := r.states[path]
	if !isAdmitted(state) {
		return admissionError(path, kind, fullName, r.phase.String(), state.String()).
			Msgf("module %s is not accepting new messages", path)
	}
	return nil
}

func (r *admissionRevision) admitEvent(fullName protoreflect.FullName) ([]string, error) {
	if r.phase != admissionActive {
		return nil, admissionError("", servicev1.MessageKind_MESSAGE_KIND_EVENT, fullName, r.phase.String(), "").
			Msg("service is not accepting new events")
	}
	paths, registered := r.registry.eventSubscribers(fullName)
	if !registered {
		return nil, messageTypeError("", fullName, "event type is not registered").
			Attr("message_kind", MessageKindEvent.String()).
			Msgf("service does not accept event message %s", fullName)
	}
	admitted := make([]string, 0, len(paths))
	for _, path := range paths {
		if isAdmitted(r.states[path]) {
			admitted = append(admitted, path)
		}
	}
	return admitted, nil
}

func admissionError(
	path string,
	kind servicev1.MessageKind,
	fullName protoreflect.FullName,
	phase string,
	state string,
) errs.Builder {
	return errs.New().
		Code(errCodeAdmission).
		Attr("module_path", path).
		Attr("message_kind", kind.String()).
		Attr("message_type", string(fullName)).
		Attr("admission_phase", phase).
		Attr("module_state", state)
}

func (p admissionPhase) String() string {
	switch p {
	case admissionPreflight:
		return "preflight"
	case admissionActive:
		return "active"
	case admissionShuttingDown:
		return "shutting_down"
	default:
		return "unknown"
	}
}

// isAdmitted keeps a module routable while setup, restart, or backoff can still
// produce a future attempt.
func isAdmitted(state moduleState) bool {
	switch state {
	case moduleRunning, moduleSetupFailed, moduleRestarting, moduleBackoff:
		return true
	case moduleDisabled, moduleStopped:
		return false
	default:
		return false
	}
}
